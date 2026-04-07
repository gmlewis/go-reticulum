// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornstatus is the Go port of the Reticulum Network Stack Status utility.
//
// It queries a running Reticulum instance (local or remote) and displays
// the status of all configured network interfaces, including transfer
// rates, traffic counters, announce statistics, and transport state.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/utils"
)

type runtimeT struct {
	app    *appT
	logger *rns.Logger
}

func newRuntime(app *appT) *runtimeT {
	if app == nil {
		app = newApp()
	}
	return &runtimeT{app: app, logger: rns.NewLogger()}
}

func (rt *runtimeT) run(nameFilter string) {
	if rt == nil || rt.app == nil {
		return
	}
	app := rt.app
	logger := rt.logger

	if app.showVersion {
		fmt.Printf("gornstatus %v\n", rns.VERSION)
		return
	}

	logger.SetLogDest(rns.LogStdout)
	verbosity := int(app.verbose)

	if app.monitorMode {
		ts := rns.NewTransportSystem(logger)
		ret, err := rns.NewReticulumWithLogger(ts, app.configDir, logger)
		if err != nil {
			log.Fatal("No shared RNS instance available to get status from")
		}
		defer func() {
			if err := ret.Close(); err != nil {
				logger.Warning("Could not close Reticulum properly: %v", err)
			}
		}()
		runMonitor(ret, nameFilter, verbosity, app)
		return
	}

	exitCode := programSetup(programSetupParams{
		configDir:          app.configDir,
		dispAll:            app.showAll,
		verbosity:          verbosity,
		nameFilter:         nameFilter,
		jsonOutput:         app.jsonOutput,
		announceStats:      app.announceStats,
		linkStats:          app.linkStats,
		sorting:            app.sortKey,
		sortReverse:        app.sortReverse,
		remote:             app.remoteHash,
		managementIdentity: app.identityPath,
		remoteTimeout:      app.remoteTimeout,
		mustExit:           true,
		trafficTotals:      app.trafficTotals,
		discoveredIfaces:   app.discovered,
		configEntries:      app.detailedDiscovered,
		logger:             logger,
	})
	os.Exit(exitCode)
}

func main() {
	log.SetFlags(0)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println()
		os.Exit(0)
	}()

	app, args, err := parseFlags(os.Args[1:], os.Stdout)
	if err != nil {
		if err == utils.ErrHelp {
			return
		}
		log.Fatal(err)
	}
	nameFilter := ""
	if len(args) > 0 {
		nameFilter = args[0]
	}
	newRuntime(app).run(nameFilter)
}
