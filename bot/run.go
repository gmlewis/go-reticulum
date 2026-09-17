// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// ErrFirstRun reports that Run created the bot's first-run files and stopped
// without connecting. A caller treats it as a successful exit: an operator must
// edit the hubs before anything reaches the network.
var ErrFirstRun = errors.New("first run: configuration and identity created")

// pprofReadHeaderTimeout bounds how long the debug server waits for a request's
// headers, so a half-open connection cannot hold a slot forever.
const pprofReadHeaderTimeout = 10 * time.Second

// Options is one bot run, after the command line and the configuration file
// have been read. The zero value is the documented default run: every path
// default, no receiver, no compass, no portal, and no pprof listener.
type Options struct {
	// ConfigDir is the Reticulum configuration directory. Empty means the
	// Reticulum default.
	ConfigDir string
	// BotConfig overrides the path of the bot's own TOML configuration file.
	BotConfig string
	// Identity overrides the path of the bot's identity file.
	Identity string
	// Home overrides the bot's home directory, which otherwise defaults to
	// the BOT_HOME environment variable or ~/.gorrcbot.
	Home string
	// Nick overrides the advertised nick on every hub.
	Nick string
	// LogLevel and LogFile configure the RNS logger every part of the bot
	// shares. Empty leaves the logger's own configuration alone.
	LogLevel string
	LogFile  string
	// PProfAddr turns on the debug pprof HTTP server when it is not empty.
	PProfAddr string
	// CheckConfig loads and validates the configuration, prints what it found
	// through Stdout, and returns without connecting to anything.
	CheckConfig bool
	// Stdout receives the CheckConfig summary. Nil means os.Stdout.
	Stdout io.Writer
	// Stderr receives the first-run message. Nil means os.Stderr.
	Stderr io.Writer
}

// Run brings up the whole bot and serves until ctx is cancelled: it loads the
// configuration and the identity, starts Reticulum, dials every configured hub
// through the engine, and brings up the optional GNSS receiver, electronic
// compass, LXMF router, and captive portal. Every optional subsystem is absent
// unless the configuration asked for it, and every one that starts is torn down
// on the way out, so Run returns with no goroutine left behind.
//
// Run returns ErrFirstRun when it created the first-run files instead of
// connecting.
func Run(ctx context.Context, opts Options) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	paths := resolvePaths(&opts)
	if !opts.CheckConfig {
		created, err := EnsureFirstRun(paths)
		if err != nil {
			return err
		}
		if created {
			if _, err := fmt.Fprint(stderr, firstRunMessage(paths)); err != nil {
				return fmt.Errorf("writing the first-run message: %w", err)
			}
			return ErrFirstRun
		}
	}

	cfg, warnings, err := LoadBotConfig(paths.ConfigPath)
	if err != nil {
		return err
	}
	// An unknown key is a warning, never a failure: a configuration written for
	// a newer bot must still start.
	for _, warning := range warnings {
		logf("%v", warning)
	}
	if opts.Nick != "" {
		cfg.Nick = opts.Nick
	}
	paths = applyConfiguredPaths(paths, cfg, &opts)

	identity, _, err := LoadBotIdentity(paths.IdentityPath)
	if err != nil {
		return err
	}
	ownHash := identity.Hash

	if opts.CheckConfig {
		if _, err := fmt.Fprint(stdout, configSummary(paths, cfg, ownHash)); err != nil {
			return fmt.Errorf("writing the configuration summary: %w", err)
		}
		return nil
	}

	// The bot owns one Reticulum instance and one manager; every hub shares
	// them, which is what gives the bot a single identity across hubs.
	logger := rns.NewLogger()
	transport := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(transport, opts.ConfigDir, logger)
	if err != nil {
		return fmt.Errorf("could not start Reticulum: %w", err)
	}
	// The Reticulum configuration file can set its own log level, so the
	// command line is applied after the instance reads it: an operator who asks
	// for LogLevel WARNING gets WARNING even when that config is chattier.
	if err := applyLogging(logger, &opts); err != nil {
		return err
	}

	dialer := newManagerDialer(identity, cfg.Nick, paths.StorageDir, ret)
	b := newBot(cfg, paths, logger, ownHash, dialer, botHooks{Greeting: greeting})
	// The registry answers commands; the reply policy decides which messages
	// are commands at all, and sends every reply.
	reg := newRegistry(b)
	// The path command reads the shared transport's path table, and the announce
	// cache watches the same transport: the registry and the bot never reach the
	// network by themselves.
	reg.paths = livePathLookup{ts: transport}
	b.pathsTable = reg.paths
	b.announceFeed = liveAnnounceFeed{ts: transport}
	b.hooks.Inbound = newResponder(cfg, ownHash, reg.Run).handle

	// The GNSS receiver is optional, and it is one source shared by every
	// position-aware command and by the captive portal, so the radio answer and
	// the dashboard answer can never disagree about where the device is.
	gps, err := openGPS(cfg)
	if err != nil {
		return err
	}
	if gps != nil {
		defer func() {
			if err := gps.Close(); err != nil {
				logf("closing the GNSS source: %v", err)
			}
		}()
		reg.gps = gps
		switch {
		case cfg.GPSPort != "":
			logf("reading GNSS sentences from %v", cfg.GPSPort)
		default:
			logf("static GNSS fix %v", cfg.GPSFix)
		}
	}

	// The electronic compass is optional and is the other half of the position
	// picture: the receiver says where the device is, and the compass says which
	// way it is pointing. It reads the same one fix the commands do, so a
	// magnetic heading is corrected to true north with the variation at the
	// device's own position.
	compass, err := openCompass(cfg)
	if err != nil {
		return err
	}
	if compass != nil {
		defer func() {
			if err := compass.Close(); err != nil {
				logf("closing the compass source: %v", err)
			}
		}()
		compass.SetLocationSource(reg.currentFix)
		reg.compass = compass
		switch {
		case cfg.CompassPort != "":
			logf("reading compass sentences from %v", cfg.CompassPort)
		default:
			logf("static compass heading %v", cfg.CompassHeading)
		}
	}

	// LXMF is opt-in. With lxmf_enabled = false this returns nothing at all, so
	// no router, no job loop and no state under the storage directory can come
	// into existence; with it true the bot owns the router from here on and
	// closes it during shutdown.
	sender, err := openLXMFDelivery(cfg, transport, identity, nil)
	if err != nil {
		return err
	}
	if sender != nil {
		reg.lxmf = sender
		b.lxmf = sender
		logf("LXMF enabled, announcing the delivery destination every %vm",
			cfg.LXMFAnnounceMinutes)
	}

	stopPProf := startPProf(opts.PProfAddr)
	defer stopPProf()

	// The captive portal is opt-in: with no portal_addr the bot binds no HTTP
	// listener at all, which is the right default for a node on a shared
	// network. With one, the dashboard is the same offline command surface the
	// radio commands are, so a phone that joins the device's Wi-Fi can read the
	// position and ask the field assistant without installing anything.
	if cfg.PortalAddr != "" {
		portal := newPortalServer(cfg.PortalAddr, gps, reg)
		if err := portal.Start(); err != nil {
			return err
		}
		defer func() {
			if err := portal.Close(); err != nil {
				logf("closing the captive portal: %v", err)
			}
		}()
		logf("captive portal on http://%v/", portal.Addr())
	}

	log.Printf("%v %v: identity %v, %v hub(s), nick %v",
		programName, rns.VERSION, hexString(ownHash), len(cfg.Hubs), cfg.Nick)
	if err := b.Run(ctx); err != nil {
		return err
	}
	logf("stopped after %v", formatDuration(b.uptime()))
	return nil
}

// applyLogging applies LogLevel and LogFile to the logger every part of the
// bot shares. The bot logs a lot during a busy hub session, so an operator
// needs one switch that quiets the whole process.
func applyLogging(logger *rns.Logger, opts *Options) error {
	if logger == nil {
		return errors.New("no logger to configure")
	}
	if opts.LogLevel != "" {
		level, err := parseLogLevel(opts.LogLevel)
		if err != nil {
			return err
		}
		logger.SetLogLevel(level)
	}
	if opts.LogFile != "" {
		logger.SetLogFilePath(opts.LogFile)
		logger.SetLogDest(rns.LogDestFile)
	}
	return nil
}

// parseLogLevel maps a level name onto the RNS logger's level. It accepts the
// names the other tools accept, and the numeric levels too, so a script that
// already passes a number keeps working.
func parseLogLevel(name string) (int, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 0, errors.New("empty log level")
	}
	switch strings.ToUpper(trimmed) {
	case "CRITICAL", "FATAL":
		return rns.LogCritical, nil
	case "ERROR":
		return rns.LogError, nil
	case "WARNING", "WARN":
		return rns.LogWarning, nil
	case "NOTICE":
		return rns.LogNotice, nil
	case "INFO":
		return rns.LogInfo, nil
	case "VERBOSE":
		return rns.LogVerbose, nil
	case "DEBUG":
		return rns.LogDebug, nil
	case "PATHING":
		return rns.LogPathing, nil
	case "EXTREME":
		return rns.LogExtreme, nil
	case "NONE", "OFF", "SILENT":
		return rns.LogNone, nil
	}
	numeric, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("unknown log level %q", name)
	}
	if numeric < rns.LogNone || numeric > rns.LogExtreme {
		return 0, fmt.Errorf("log level %v is outside the range %v to %v",
			numeric, rns.LogNone, rns.LogExtreme)
	}
	return numeric, nil
}

// applyConfiguredPaths returns the paths the bot will actually use. Precedence,
// highest first, is the command line, then the configuration file, then the home
// directory defaults. The config keys exist and are documented, and the template
// restates the defaults, so a default configuration is unchanged; before this,
// however, a deliberate override was parsed, validated, and then never consulted,
// which meant a bot told to keep its identity elsewhere quietly used the default
// path instead — and the LXMF state honoured the setting while the room history
// next to it did not.
func applyConfiguredPaths(paths BotPaths, cfg *BotConfig, opts *Options) BotPaths {
	if cfg == nil {
		return paths
	}
	if opts.Identity == "" && cfg.IdentityPath != "" {
		paths.IdentityPath = cfg.IdentityPath
	}
	if opts.Home == "" && cfg.StorageDir != "" {
		paths.StorageDir = cfg.StorageDir
	}
	return paths
}

// resolvePaths returns the paths the command line alone asks for, before the
// configuration file is read: home, if given, moves all three together.
func resolvePaths(opts *Options) BotPaths {
	paths := DefaultBotPaths()
	if opts.Home != "" {
		paths.Home = opts.Home
		paths.ConfigPath = filepath.Join(paths.Home, defaultConfigFileName)
		paths.IdentityPath = filepath.Join(paths.Home, defaultIdentityFileName)
		paths.StorageDir = filepath.Join(paths.Home, defaultStorageDirName)
	}
	if opts.BotConfig != "" {
		paths.ConfigPath = opts.BotConfig
	}
	if opts.Identity != "" {
		paths.IdentityPath = opts.Identity
	}
	return paths
}

// configSummary renders what the bot would do, for CheckConfig.
func configSummary(paths BotPaths, cfg *BotConfig, ownHash []byte) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%v %v configuration\n", programName, rns.VERSION)
	fmt.Fprintf(&sb, "config:     %v\n", paths.ConfigPath)
	fmt.Fprintf(&sb, "identity:   %v (%v)\n", paths.IdentityPath, hexString(ownHash))
	fmt.Fprintf(&sb, "storage:    %v\n", paths.StorageDir)
	fmt.Fprintf(&sb, "nick:       %v\n", cfg.Nick)
	fmt.Fprintf(&sb, "trigger:    @%v or @%v\n", cfg.Nick, shortHash(hexString(ownHash)))
	fmt.Fprintf(&sb, "reply:      %v\n", cfg.Reply)
	fmt.Fprintf(&sb, "cooldown:   %vs\n", cfg.CooldownSecs)
	fmt.Fprintf(&sb, "max lines:  %v\n", cfg.MaxReplyLines)
	fmt.Fprintf(&sb, "announce:   %v\n", pyBool(cfg.AnnounceOnJoin))
	if cfg.WeatherURL != "" {
		fmt.Fprintf(&sb, "weather:    %v\n", cfg.WeatherURL)
	}
	if cfg.FlightURL != "" {
		fmt.Fprintf(&sb, "flight:     %v\n", cfg.FlightURL)
	}
	if cfg.FlightRouteURL != "" {
		fmt.Fprintf(&sb, "flight route: %v\n", cfg.FlightRouteURL)
	}
	if cfg.LaunchURL != "" {
		fmt.Fprintf(&sb, "launches:   %v\n", cfg.LaunchURL)
	}
	if cfg.KJVTxtFile != "" {
		fmt.Fprintf(&sb, "kjv:        %v\n", cfg.KJVTxtFile)
	}
	if cfg.TideURL != "" {
		fmt.Fprintf(&sb, "tide:       %v\n", cfg.TideURL)
	}
	if cfg.BuoyURL != "" {
		fmt.Fprintf(&sb, "buoy:       %v\n", cfg.BuoyURL)
	}
	if cfg.RiverURL != "" {
		fmt.Fprintf(&sb, "river:      %v\n", cfg.RiverURL)
	}
	if cfg.RiverFloodURL != "" {
		fmt.Fprintf(&sb, "river flood: %v\n", cfg.RiverFloodURL)
	}
	if cfg.SpaceWeatherURL != "" {
		fmt.Fprintf(&sb, "spacewx:    %v\n", cfg.SpaceWeatherURL)
	}
	if cfg.MetarURL != "" {
		fmt.Fprintf(&sb, "metar:      %v\n", cfg.MetarURL)
	}
	if cfg.WeatherAlertURL != "" {
		fmt.Fprintf(&sb, "wxalert:    %v\n", cfg.WeatherAlertURL)
	}
	if cfg.LXMFEnabled {
		fmt.Fprintf(&sb, "lxmf:       enabled, announcing every %vm\n", cfg.LXMFAnnounceMinutes)
		if cfg.LXMFPropagationNode != "" {
			fmt.Fprintf(&sb, "lxmf node:  %v\n", cfg.LXMFPropagationNode)
		}
		if cfg.EmergencyLXMFDestination != "" {
			fmt.Fprintf(&sb, "sos dispatch: %v\n", cfg.EmergencyLXMFDestination)
		}
	}
	if cfg.PortalAddr != "" {
		fmt.Fprintf(&sb, "portal:     http://%v/\n", cfg.PortalAddr)
	} else {
		fmt.Fprintf(&sb, "portal:     (off)\n")
	}
	if cfg.GPSPort != "" {
		fmt.Fprintf(&sb, "gps:        reading %v\n", cfg.GPSPort)
	} else if cfg.GPSFix != "" {
		fmt.Fprintf(&sb, "gps:        static fix %v\n", cfg.GPSFix)
	} else {
		fmt.Fprintf(&sb, "gps:        (no receiver)\n")
	}
	if cfg.CompassPort != "" {
		fmt.Fprintf(&sb, "compass:    reading %v\n", cfg.CompassPort)
	} else if cfg.CompassHeading != "" {
		fmt.Fprintf(&sb, "compass:    static heading %v\n", cfg.CompassHeading)
	} else {
		fmt.Fprintf(&sb, "compass:    (no compass)\n")
	}
	fmt.Fprintf(&sb, "hubs:       %v\n", len(cfg.Hubs))
	for _, hub := range cfg.Hubs {
		fmt.Fprintf(&sb, "  - %v (%v)\n", hub.Name, hub.Destination)
		if len(hub.Rooms) == 0 {
			fmt.Fprintf(&sb, "    rooms: (none)\n")
			continue
		}
		for _, room := range hub.Rooms {
			if room.Key != "" {
				fmt.Fprintf(&sb, "    room: %v (with key)\n", room.Name)
				continue
			}
			fmt.Fprintf(&sb, "    room: %v\n", room.Name)
		}
	}
	return sb.String()
}

// startPProf starts an HTTP server exposing net/http/pprof endpoints at addr
// when addr is not empty, and returns the function that stops it. A listener
// that cannot be created is logged, not returned: the debug server is a
// convenience, and a bot must still run without it.
func startPProf(addr string) func() {
	if addr == "" {
		return func() {}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logf("pprof: listen %v: %v", addr, err)
		return func() {}
	}
	srv := &http.Server{ReadHeaderTimeout: pprofReadHeaderTimeout}
	go func() {
		logf("pprof: serving on http://%v/debug/pprof/", ln.Addr())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("pprof: serve %v: %v", addr, err)
		}
	}()
	return func() {
		if err := srv.Close(); err != nil {
			logf("pprof: close %v: %v", addr, err)
		}
	}
}

// compile-time assertions: the production dialer is what the engine expects, and
// the manager's hub connection is what a session wraps.
var (
	_ hubDialer = (*managerDialer)(nil)
	_ hubConn   = (*rrc.RRCHub)(nil)
)
