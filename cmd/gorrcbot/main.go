// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gorrcbot is a headless, always-on RRC (Reticulum Relay Chat) bot client.
//
// It is a CLIENT, not a hub: it dials every hub named in its configuration
// file, joins that hub's configured rooms, and stays connected across link
// flaps and restarts. It answers only when it is addressed by name, and it
// says nothing at all otherwise.
//
// It brings up the bot in four steps:
//   - First-run bootstrap: creates ~/.gorrcbot/config.toml and
//     ~/.gorrcbot/bot_identity (GORRCBOT_HOME overrides the directory) and
//     exits 0, so an operator always edits the hubs before anything connects.
//   - Identity: the bot owns one Reticulum identity, used on every hub, so its
//     identity hash is the same everywhere and a peer can address it by a hash
//     prefix without knowing which hub it is on.
//   - Engine: one hub session per configured hub, each with its own reconnects
//     and joins. It introduces itself with one NOTICE per room per session only
//     when announce_on_join = true; by default it arrives and leaves as
//     silently as any other member.
//   - Commands: the reply policy plus the command registry. Nothing is answered
//     unless the message starts with @<nick>, @<hash prefix>, or is a direct
//     NOTICE to the bot.
//
// Usage:
//
//	gorrcbot [--config CONFIG] [--bot-config BOT_CONFIG]
//	        [--identity IDENTITY] [--home HOME] [--nick NICK]
//	        [--check-config] [--log-level LEVEL] [--log-file PATH]
//	        [--pprof-addr ADDR] [--version]
//
// State paths honor the GORRCBOT_HOME environment variable (used literally when
// truthy, no expansion) with ~/.gorrcbot as the default home.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

func main() {
	log.SetFlags(0)

	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if opts.version {
		fmt.Printf("gorrcbot %v\n", rns.VERSION)
		os.Exit(0)
	}

	paths := resolvePaths(opts)
	if !opts.checkConfig {
		created, err := EnsureFirstRun(paths)
		if err != nil {
			log.Fatalf("gorrcbot: %v", err)
		}
		if created {
			fmt.Fprint(os.Stderr, firstRunMessage(paths))
			os.Exit(0)
		}
	}

	cfg, warnings, err := LoadBotConfig(paths.ConfigPath)
	if err != nil {
		log.Fatalf("gorrcbot: %v", err)
	}
	// An unknown key is a warning, never a failure: a configuration written
	// for a newer bot must still start.
	for _, warning := range warnings {
		log.Printf("gorrcbot: %v", warning)
	}
	if opts.nick != "" {
		cfg.Nick = opts.nick
	}
	paths = applyConfiguredPaths(paths, cfg, opts)

	identity, _, err := LoadBotIdentity(paths.IdentityPath)
	if err != nil {
		log.Fatalf("gorrcbot: %v", err)
	}
	ownHash := identity.Hash

	if opts.checkConfig {
		fmt.Print(configSummary(paths, cfg, ownHash))
		os.Exit(0)
	}

	// The bot owns one Reticulum instance and one manager; every hub shares
	// them, which is what gives the bot a single identity across hubs.
	logger := rns.NewLogger()
	transport := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(transport, opts.configDir, logger)
	if err != nil {
		log.Fatalf("gorrcbot: could not start Reticulum: %v", err)
	}
	// The Reticulum configuration file can set its own log level, so the
	// command line is applied after the instance reads it: an operator who
	// asks for --log-level WARNING gets WARNING even when that config is
	// chattier.
	if err := applyLogging(logger, opts); err != nil {
		log.Fatalf("gorrcbot: %v", err)
	}

	dialer := newManagerDialer(identity, cfg.Nick, paths.StorageDir, ret)
	b := newBot(cfg, paths, logger, ownHash, dialer, botHooks{
		Greeting: greeting,
	})
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

	// LXMF is opt-in. With lxmf_enabled = false this returns nothing at all, so
	// no router, no job loop and no state under the storage directory can come
	// into existence; with it true the bot owns the router from here on and
	// closes it during shutdown.
	sender, err := openLXMFDelivery(cfg, transport, identity, nil)
	if err != nil {
		log.Fatalf("gorrcbot: %v", err)
	}
	if sender != nil {
		reg.lxmf = sender
		b.lxmf = sender
		log.Printf("gorrcbot: LXMF enabled, announcing the delivery destination every %vm",
			cfg.LXMFAnnounceMinutes)
	}

	startPProf(opts.pprofAddr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("gorrcbot %v: identity %v, %v hub(s), nick %v",
		rns.VERSION, hexString(ownHash), len(cfg.Hubs), cfg.Nick)
	if err := b.Run(ctx); err != nil {
		log.Fatalf("gorrcbot: %v", err)
	}
	log.Printf("gorrcbot: stopped after %v", formatDuration(b.uptime()))
}

// applyLogging applies --log-level and --log-file to the logger every part of
// the bot shares. The bot logs a lot during a busy hub session, so an operator
// needs one switch that quiets the whole process.
func applyLogging(logger *rns.Logger, opts *botOptions) error {
	if logger == nil {
		return errors.New("no logger to configure")
	}
	if opts.logLevel != "" {
		level, err := parseLogLevel(opts.logLevel)
		if err != nil {
			return err
		}
		logger.SetLogLevel(level)
	}
	if opts.logFile != "" {
		logger.SetLogFilePath(opts.logFile)
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

// resolvePaths applies the CLI overrides to the default bot paths.
// applyConfiguredPaths returns the paths the bot will actually use. Precedence,
// highest first, is the command line, then the configuration file, then the home
// directory defaults. The config keys exist and are documented, and the template
// restates the defaults, so a default configuration is unchanged; before this,
// however, a deliberate override was parsed, validated, and then never consulted,
// which meant a bot told to keep its identity elsewhere quietly used the default
// path instead — and the LXMF state honoured the setting while the room history
// next to it did not.
func applyConfiguredPaths(paths BotPaths, cfg *BotConfig, opts *botOptions) BotPaths {
	if cfg == nil {
		return paths
	}
	if opts.identity == "" && cfg.IdentityPath != "" {
		paths.IdentityPath = cfg.IdentityPath
	}
	if opts.home == "" && cfg.StorageDir != "" {
		paths.StorageDir = cfg.StorageDir
	}
	return paths
}

// resolvePaths returns the paths the command line alone asks for, before the
// configuration file is read: home, if given, moves all three together.
func resolvePaths(opts *botOptions) BotPaths {
	paths := DefaultBotPaths()
	if opts.home != "" {
		paths.Home = opts.home
		paths.ConfigPath = filepath.Join(paths.Home, defaultConfigFileName)
		paths.IdentityPath = filepath.Join(paths.Home, defaultIdentityFileName)
		paths.StorageDir = filepath.Join(paths.Home, defaultStorageDirName)
	}
	if opts.botConfig != "" {
		paths.ConfigPath = opts.botConfig
	}
	if opts.identity != "" {
		paths.IdentityPath = opts.identity
	}
	return paths
}

// configSummary renders what the bot would do, for --check-config.
func configSummary(paths BotPaths, cfg *BotConfig, ownHash []byte) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "gorrcbot %v configuration\n", rns.VERSION)
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
	if cfg.LaunchURL != "" {
		fmt.Fprintf(&sb, "launches:   %v\n", cfg.LaunchURL)
	}
	if cfg.LXMFEnabled {
		fmt.Fprintf(&sb, "lxmf:       enabled, announcing every %vm\n", cfg.LXMFAnnounceMinutes)
		if cfg.LXMFPropagationNode != "" {
			fmt.Fprintf(&sb, "lxmf node:  %v\n", cfg.LXMFPropagationNode)
		}
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
// when addr is not empty.
func startPProf(addr string) {
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("pprof: listen %v: %v", addr, err)
		return
	}
	go func() {
		log.Printf("pprof: serving on http://%v/debug/pprof/", ln.Addr())
		if err := http.Serve(ln, nil); err != nil {
			log.Printf("pprof: serve %v: %v", addr, err)
		}
	}()
}

// compile-time assertions: the production dialer is what the engine expects, and
// the manager's hub connection is what a session wraps.
var (
	_ hubDialer = (*managerDialer)(nil)
	_ hubConn   = (*rrc.RRCHub)(nil)
)
