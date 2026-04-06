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

func (a *appT) run(nameFilter string) {
	if a.showVersion {
		fmt.Printf("gornstatus %v\n", rns.VERSION)
		return
	}

	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogStdout)
	verbosity := int(a.verbose)

	if a.monitorMode {
		ts := rns.NewTransportSystem()
		ret, err := rns.NewReticulumWithLogger(ts, a.configDir, logger)
		if err != nil {
			log.Fatal("No shared RNS instance available to get status from")
		}
		defer func() {
			if err := ret.Close(); err != nil {
				logger.Log(fmt.Sprintf("Warning: Could not close Reticulum properly: %v", err), rns.LogWarning, false)
			}
		}()
		runMonitor(ret, nameFilter, verbosity, a)
		return
	}

	exitCode := programSetup(programSetupParams{
		configDir:          a.configDir,
		dispAll:            a.showAll,
		verbosity:          verbosity,
		nameFilter:         nameFilter,
		jsonOutput:         a.jsonOutput,
		announceStats:      a.announceStats,
		linkStats:          a.linkStats,
		sorting:            a.sortKey,
		sortReverse:        a.sortReverse,
		remote:             a.remoteHash,
		managementIdentity: a.identityPath,
		remoteTimeout:      a.remoteTimeout,
		mustExit:           true,
		trafficTotals:      a.trafficTotals,
		discoveredIfaces:   a.discovered,
		configEntries:      a.detailedDiscovered,
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
	app.run(nameFilter)
}
