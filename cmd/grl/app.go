// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/gmlewis/go-reticulum/bot"
	"github.com/gmlewis/go-reticulum/rns"
)

// programName is the prefix every operator-facing line this executable logs
// carries. The shared engine is told the same name at start-up, so one appliance
// reports one identity in the journal rather than two.
const programName = "grl"

// storageDirMode is the mode of the appliance's own state directory.
const storageDirMode = 0o755

// App is one running Go Reticulum Lifesaver: the Reticulum stack, the two
// sensors, the in-process field assistant, and the captive dashboard, brought up
// in that order and torn down in the reverse of it.
//
// Everything the appliance answers with is computed by the shared bot.Engine,
// the same command table the RRC chat bot answers over a radio link. That is the
// point of the appliance: with no hub in range, the dashboard still reports the
// position, the daylight left, the nearest repeater, and the first-aid card,
// because none of those answers ever needed a network.
type App struct {
	// cfg is the configuration the appliance was built from.
	cfg Config
	// rns is the local Reticulum stack, nil until Start brings it up.
	rns *rns.Reticulum
	// engine is the in-process command evaluator over the live sensors.
	engine *bot.Engine
	// gps and compass are the live sensors, nil when neither a port nor a
	// static fallback is configured for them.
	gps     *bot.GPSReader
	compass *bot.CompassReader
	// portal is the captive survival dashboard, nil when no address is
	// configured.
	portal *bot.PortalServer
	// logger is the Reticulum logger the stack uses, nil to let Start make
	// its own.
	logger *rns.Logger

	mu      sync.Mutex
	started bool
	closed  bool
}

// NewApp builds an appliance from cfg. It validates the configuration and binds
// nothing: every socket, device, and goroutine belongs to Start, so an
// unusable configuration is refused before any resource exists.
func NewApp(cfg Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%v: %w", programName, err)
	}
	return &App{cfg: cfg}, nil
}

// SetLogger supplies the Reticulum logger the appliance uses, so a caller can
// choose the log level and the destination before the stack starts. A nil logger
// (the default) makes Start create a fresh one.
func (a *App) SetLogger(logger *rns.Logger) {
	if a == nil {
		return
	}
	a.logger = logger
}

// Config returns the configuration the appliance is running.
func (a *App) Config() Config {
	if a == nil {
		return Config{}
	}
	return a.cfg
}

// Engine returns the in-process command evaluator, nil before Start.
func (a *App) Engine() *bot.Engine {
	if a == nil {
		return nil
	}
	return a.engine
}

// Portal returns the captive dashboard, nil when no address is configured or
// before Start.
func (a *App) Portal() *bot.PortalServer {
	if a == nil {
		return nil
	}
	return a.portal
}

// GPS returns the live GNSS reader, nil when no receiver and no static fix are
// configured.
func (a *App) GPS() *bot.GPSReader {
	if a == nil {
		return nil
	}
	return a.gps
}

// Compass returns the live electronic-compass reader, nil when no compass and
// no static heading are configured.
func (a *App) Compass() *bot.CompassReader {
	if a == nil {
		return nil
	}
	return a.compass
}

// PortalAddr returns the address the dashboard is actually bound to, which is
// the useful form when the configuration asked for port 0.
func (a *App) PortalAddr() string {
	if a == nil || a.portal == nil {
		return ""
	}
	return a.portal.Addr()
}

// Start brings the whole appliance up: Reticulum, then the sensors, then the
// command engine over them, then the captive dashboard. Every optional
// subsystem is absent unless the configuration asked for it.
//
// A failure tears down whatever did start, so a caller never has to clean up
// after a half-started appliance, and Start may be called at most once.
func (a *App) Start(ctx context.Context) error {
	if a == nil {
		return errors.New("grl: no appliance")
	}
	a.mu.Lock()
	switch {
	case a.closed:
		a.mu.Unlock()
		return errors.New("grl: the appliance is already closed")
	case a.started:
		a.mu.Unlock()
		return errors.New("grl: the appliance is already running")
	}
	a.started = true
	a.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.startReticulum(); err != nil {
		return a.abort(err)
	}
	if err := a.startSensors(); err != nil {
		return a.abort(err)
	}
	a.startEngine()
	if err := a.startPortal(); err != nil {
		return a.abort(err)
	}
	return nil
}

// abort tears down a partially started appliance and returns the failure that
// caused it. A teardown that also fails is logged rather than returned: the
// caller needs the original reason, not a cleanup detail.
func (a *App) abort(cause error) error {
	if err := a.Close(); err != nil {
		logf("closing after a failed start: %v", err)
	}
	return cause
}

// startReticulum brings up the local Reticulum stack. A stack that cannot start
// is a start-up failure rather than a warning: an appliance that quietly ran
// without a radio would look identical to one nobody can hear.
func (a *App) startReticulum() error {
	logger := a.logger
	if logger == nil {
		logger = rns.NewLogger()
	}
	transport := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(transport, a.cfg.RNS.ConfigPath, logger)
	if err != nil {
		return fmt.Errorf("grl: could not start Reticulum: %w", err)
	}
	a.rns = ret
	return nil
}

// startSensors brings up the two position sensors the field tools read. Each is
// optional, and each falls back to its configured static value, which is what
// makes the same appliance run on a desk with nothing plugged in.
func (a *App) startSensors() error {
	sensors := sensorConfig(a.cfg)

	gps, err := bot.OpenGPS(sensors)
	if err != nil {
		return fmt.Errorf("grl: %w", err)
	}
	if gps != nil {
		a.gps = gps
		if strings.TrimSpace(a.cfg.GNSS.Port) != "" {
			logf("reading GNSS sentences from %v (%v baud)", a.cfg.GNSS.Port, a.cfg.GNSS.Baud)
		} else {
			logf("static GNSS fix %v", a.cfg.GNSS.StaticFix)
		}
	}

	compass, err := bot.OpenCompass(sensors)
	if err != nil {
		return fmt.Errorf("grl: %w", err)
	}
	if compass != nil {
		// The compass corrects a magnetic reading to true north with the World
		// Magnetic Model at the device's own position, so it reads the same one
		// fix every position-aware command reads.
		if a.gps != nil {
			compass.SetLocationSource(func() (bot.GPSFix, bool) {
				fix := a.gps.LastFix()
				return fix, fix.Valid
			})
		}
		a.compass = compass
		if strings.TrimSpace(a.cfg.Compass.Port) != "" {
			logf("reading compass sentences from %v (%v baud)", a.cfg.Compass.Port, a.cfg.Compass.Baud)
		} else {
			logf("static compass heading %v", a.cfg.Compass.StaticHeading)
		}
	}
	return nil
}

// startEngine builds the in-process command engine over the live sensors. It
// opens no socket and starts no goroutine, so an appliance whose dashboard is
// switched off still has a complete offline field assistant available to any
// later caller.
func (a *App) startEngine() {
	storage := strings.TrimSpace(a.cfg.Device.StorageDir)
	if storage != "" {
		if err := os.MkdirAll(storage, storageDirMode); err != nil {
			logf("could not create the storage directory %v: %v; "+
				"distress beacons and situation reports will not survive a restart", storage, err)
			storage = ""
		}
	}
	cfg := &bot.BotConfig{
		Nick:          a.cfg.AdvertisedName(),
		Reply:         bot.ReplyAuto,
		CooldownSecs:  bot.DefaultCooldownSecs,
		MaxReplyLines: bot.DefaultMaxReplyLines,
		StorageDir:    storage,
	}
	// The engine only needs the storage directory: the appliance owns its own
	// home and configuration, so no bot identity or TOML file is involved.
	a.engine = bot.NewEngine(cfg, bot.BotPaths{StorageDir: storage}, a.gps, a.compass)
}

// startPortal brings up the captive survival dashboard. It is opt-in: with no
// portal_addr the appliance binds no HTTP listener at all, which is the right
// default for a device on a shared network.
func (a *App) startPortal() error {
	addr := strings.TrimSpace(a.cfg.Portal.PortalAddr)
	if addr == "" {
		logf("captive portal is off (no portal_addr)")
		return nil
	}
	portal := bot.NewPortalServer(addr, a.gps, a.engine)
	if err := portal.Start(); err != nil {
		return fmt.Errorf("grl: %w", err)
	}
	a.portal = portal
	logf("captive portal on http://%v/", portal.Addr())
	return nil
}

// Close stops the dashboard, closes both sensors, and disconnects the Reticulum
// stack, in that order, so nothing is left reading a device or holding a socket.
// It is idempotent, and it is safe to call after a partial Start.
func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	portal, compass, gps, ret := a.portal, a.compass, a.gps, a.rns
	a.portal, a.compass, a.gps, a.rns = nil, nil, nil, nil
	a.engine = nil
	a.mu.Unlock()

	var errs []error
	if portal != nil {
		if err := portal.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing the captive portal: %w", err))
		}
	}
	if compass != nil {
		if err := compass.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing the compass: %w", err))
		}
	}
	if gps != nil {
		if err := gps.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing the GNSS receiver: %w", err))
		}
	}
	if ret != nil {
		if err := ret.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing Reticulum: %w", err))
		}
	}
	return errors.Join(errs...)
}

// sensorConfig translates the appliance's two sensor sections into the shape the
// shared readers take. It is the one place the appliance's configuration names
// meet the shared engine's, so a new sensor option has exactly one seam to cross.
func sensorConfig(cfg Config) *bot.BotConfig {
	return &bot.BotConfig{
		GPSPort:        cfg.GNSS.Port,
		GPSFix:         cfg.GNSS.StaticFix,
		CompassPort:    cfg.Compass.Port,
		CompassHeading: cfg.Compass.StaticHeading,
	}
}

// quietOutput and verboseOutput are the command line's verbosity switches. They
// are set once, from the parsed command line, before any goroutine starts, so
// the informational lines below never race with the choice that enables them.
var (
	quietOutput   bool
	verboseOutput bool
)

// logf writes one operator-facing line, prefixed with the appliance's name so a
// journal holding several tools stays readable. -quiet silences these lines but
// never an error.
func logf(format string, args ...any) {
	if quietOutput {
		return
	}
	log.Printf(programName+": "+format, args...)
}

// debugf writes a line only -verbose asks for: the resolved configuration and
// the state of each optional subsystem.
func debugf(format string, args ...any) {
	if !verboseOutput {
		return
	}
	log.Printf(programName+": "+format, args...)
}
