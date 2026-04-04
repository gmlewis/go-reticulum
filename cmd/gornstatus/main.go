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
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
)

func (a *appT) run() {
	if a.showVersion {
		fmt.Printf("gornstatus %v\n", rns.VERSION)
		return
	}

	rns.SetLogDest(rns.LogStdout)
	verbosity := int(a.verbose)

	nameFilter := flag.Arg(0)

	if a.monitorMode {
		ts := rns.NewTransportSystem()
		ret, err := rns.NewReticulum(ts, a.configDir)
		if err != nil {
			log.Fatal("No shared RNS instance available to get status from")
		}
		defer func() {
			if err := ret.Close(); err != nil {
				rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
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
	})
	os.Exit(exitCode)
}

func main() {
	log.SetFlags(0)
	app := newApp()
	app.initFlags(flag.CommandLine)
	flag.Usage = app.usage

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println()
		os.Exit(0)
	}()

	flag.Parse()
	app.run()
}
