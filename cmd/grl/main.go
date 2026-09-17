// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// grl is the Go Reticulum Lifesaver: a sovereign, pocket-sized off-grid survival
// communicator and field assistant in a single executable.
//
// The same application is meant to run on an ultra-low-power ESP32-C5 as
// bare-metal firmware and, unchanged, on a workstation: on macOS, Linux Mint, or
// anything else Go builds for. This executable is the workstation half of that
// promise, and it is the reference the firmware is measured against.
//
// It runs the whole appliance in one process:
//
//   - A local Reticulum stack, configured by rns.config_path.
//   - A pure-Go NMEA-0183 GNSS receiver and electronic compass, each of which
//     falls back to a static value when no hardware is attached, so the
//     appliance is fully usable on a desk.
//   - The shared field-assistant engine from github.com/gmlewis/go-reticulum/bot:
//     the same command table the RRC chat bot answers over a radio link, with
//     zero radio hops. The position card, the daylight left, the nearest
//     repeater, the first-aid card, and the distress-beacon registry are all
//     computed in-process.
//   - The captive survival dashboard: an HTTP server and JSON API a smartphone
//     can read after joining the device's network. Every captive-probe route the
//     common operating systems already request (/generate_204,
//     /hotspot-detect.html, /ncsi.txt, /connecttest.txt, /gen_204) answers with a
//     redirect to the dashboard, so the phone pops it up by itself on a SoftAP
//     and reaches it at http://localhost:9111/ on a desk.
//
// Usage:
//
//	grl [--version] [--config CONFIG] [--portal-addr ADDR]
//	    [--gps-port PORT] [--compass-port PORT] [--quiet] [--verbose]
//
// State paths honor the GRL_HOME environment variable (used literally when
// truthy, no expansion) with ~/.grl as the default home.
//
// Exit status is 0 on a clean exit, 1 for an operational failure, and 2 for a
// usage error.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gmlewis/go-reticulum/bot"
	"github.com/gmlewis/go-reticulum/rns"
)

// Exit codes. A clean exit is 0, an operational failure is 1, and a command
// line that cannot mean anything is 2, which is what every other tool in this
// repository does.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	log.SetFlags(0)
	// The shared engine logs under this name too, so one appliance reports one
	// identity in the journal. It is set before anything else runs, and before
	// any goroutine that logs exists.
	bot.SetProgramName(programName)
	os.Exit(run(os.Args[1:]))
}

// run parses the command line, brings the appliance up, and serves until the
// context is cancelled. It is separate from main so every deferred teardown runs
// before the process ends.
func run(args []string) int {
	opts, err := parseFlags(args)
	if err != nil {
		if errors.Is(err, errHelp) {
			return exitOK
		}
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}

	if opts.version {
		fmt.Printf("grl %v\n", rns.VERSION)
		return exitOK
	}

	quietOutput = opts.quiet
	verboseOutput = opts.verbose

	cfg, err := loadApplianceConfig(opts)
	if err != nil {
		log.Printf("%v: %v", programName, err)
		return exitFailure
	}

	app, err := NewApp(cfg)
	if err != nil {
		log.Printf("%v", err)
		return exitFailure
	}
	app.SetLogger(newLogger(opts))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Start(ctx); err != nil {
		log.Printf("%v", err)
		return exitFailure
	}
	logf("%v %v: %v, dashboard on http://%v/", programName, rns.VERSION,
		cfg.AdvertisedName(), app.PortalAddr())

	<-ctx.Done()
	if err := app.Close(); err != nil {
		log.Printf("%v: %v", programName, err)
		return exitFailure
	}
	logf("stopped")
	return exitOK
}

// loadApplianceConfig returns the configuration the appliance runs with: the
// file named by -config, or the default one, created with documented defaults on
// the first run, with every command-line override applied on top.
//
// An override is applied only when it was given, so the command line can narrow
// what the file says but can never silently erase a setting the operator wrote.
func loadApplianceConfig(opts *options) (Config, error) {
	path := opts.configPath
	if path == "" {
		path = DefaultConfigPath()
	}
	cfg, err := EnsureConfigFile(path)
	if err != nil {
		return Config{}, err
	}
	if opts.portalAddr != "" {
		cfg.Portal.PortalAddr = opts.portalAddr
	}
	if opts.gpsPort != "" {
		cfg.GNSS.Port = opts.gpsPort
	}
	if opts.compassPort != "" {
		cfg.Compass.Port = opts.compassPort
	}
	// The overrides are validated here rather than left to NewApp, so an
	// unusable flag is reported with the same wording as an unusable file.
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if verboseOutput {
		debugf("configuration %v: callsign %v, portal %v, gnss %v, compass %v, rns %v, rooms %v",
			path, cfg.Device.Callsign, cfg.Portal.PortalAddr, cfg.GNSS.Port,
			cfg.Compass.Port, cfg.RNS.ConfigPath, cfg.Mesh.Rooms)
	}
	return cfg, nil
}

// newLogger builds the Reticulum logger the stack uses, at the level the command
// line asked for. With neither -quiet nor -verbose the logger keeps the level
// the Reticulum configuration sets, which is what an operator who edited that
// file expects.
func newLogger(opts *options) *rns.Logger {
	logger := rns.NewLogger()
	switch {
	case opts.quiet:
		logger.SetLogLevel(rns.LogWarning)
	case opts.verbose:
		logger.SetLogLevel(rns.LogInfo)
	}
	return logger
}
