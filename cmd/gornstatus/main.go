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
	"strconv"
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
)

// counter implements flag.Value for a counted flag (e.g. -v -v -v).
type counter int

func (c *counter) String() string { return strconv.Itoa(int(*c)) }
func (c *counter) Set(string) error {
	*c++
	return nil
}
func (c *counter) IsBoolFlag() bool { return true }

func init() {
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), `
usage: gornstatus [-h] [--config CONFIG] [--version] [-a] [-A] [-l] [-t]
                  [-s SORT] [-r] [-j] [-R hash] [-i path] [-w seconds]
                  [-d] [-D] [-m] [-I seconds] [-v]
                  [filter]

Reticulum Network Stack Status

positional arguments:
  filter                only display interfaces with names including filter

options:
  -h, --help            show this help message and exit
  --config CONFIG       path to alternative Reticulum config directory
  --version             show program's version number and exit
  -a, --all             show all interfaces
  -A, --announce-stats  show announce stats
  -l, --link-stats      show link stats
  -t, --totals          display traffic totals
  -s SORT, --sort SORT  sort interfaces by [rate, traffic, rx, tx, rxs, txs,
                        announces, arx, atx, held]
  -r, --reverse         reverse sorting
  -j, --json            output in JSON format
  -R hash               transport identity hash of remote instance to get
                        status from
  -i path               path to identity used for remote management
  -w seconds            timeout before giving up on remote queries
  -d, --discovered      list discovered interfaces
  -D                    show details and config entries for discovered
                        interfaces
  -m, --monitor         continuously monitor status
  -I seconds, --monitor-interval seconds
                        refresh interval for monitor mode (default: 1)
  -v, --verbose
`)
	}

	flag.StringVar(&configDir, "config", "", "path to alternative Reticulum config directory")
	flag.BoolVar(&showVersion, "version", false, "show program's version number and exit")
	flag.BoolVar(&showAll, "a", false, "show all interfaces")
	flag.BoolVar(&showAll, "all", false, "show all interfaces")
	flag.BoolVar(&announceStats, "A", false, "show announce stats")
	flag.BoolVar(&announceStats, "announce-stats", false, "show announce stats")
	flag.BoolVar(&linkStats, "l", false, "show link stats")
	flag.BoolVar(&linkStats, "link-stats", false, "show link stats")
	flag.BoolVar(&trafficTotals, "t", false, "display traffic totals")
	flag.BoolVar(&trafficTotals, "totals", false, "display traffic totals")
	flag.StringVar(&sortKey, "s", "", "sort interfaces by [rate, traffic, rx, tx, rxs, txs, announces, arx, atx, held]")
	flag.StringVar(&sortKey, "sort", "", "sort interfaces by [rate, traffic, rx, tx, rxs, txs, announces, arx, atx, held]")
	flag.BoolVar(&sortReverse, "r", false, "reverse sorting")
	flag.BoolVar(&sortReverse, "reverse", false, "reverse sorting")
	flag.BoolVar(&jsonOutput, "j", false, "output in JSON format")
	flag.BoolVar(&jsonOutput, "json", false, "output in JSON format")
	flag.StringVar(&remoteHash, "R", "", "transport identity hash of remote instance to get status from")
	flag.StringVar(&identityPath, "i", "", "path to identity used for remote management")
	flag.Float64Var(&remoteTimeout, "w", 15.0, "timeout before giving up on remote queries")
	flag.BoolVar(&discovered, "d", false, "list discovered interfaces")
	flag.BoolVar(&discovered, "discovered", false, "list discovered interfaces")
	flag.BoolVar(&detailedDiscovered, "D", false, "show details and config entries for discovered interfaces")
	flag.BoolVar(&monitorMode, "m", false, "continuously monitor status")
	flag.BoolVar(&monitorMode, "monitor", false, "continuously monitor status")
	flag.Float64Var(&monitorInterval, "I", 1.0, "refresh interval for monitor mode (default: 1)")
	flag.Float64Var(&monitorInterval, "monitor-interval", 1.0, "refresh interval for monitor mode (default: 1)")
	flag.Var(&verbose, "v", "increase verbosity")
	flag.Var(&verbose, "verbose", "increase verbosity")
}

var (
	configDir          string
	showVersion        bool
	showAll            bool
	announceStats      bool
	linkStats          bool
	trafficTotals      bool
	sortKey            string
	sortReverse        bool
	jsonOutput         bool
	remoteHash         string
	identityPath       string
	remoteTimeout      float64
	discovered         bool
	detailedDiscovered bool
	monitorMode        bool
	monitorInterval    float64
	verbose            counter
)

func main() {
	log.SetFlags(0)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println()
		os.Exit(0)
	}()

	flag.Parse()

	if showVersion {
		fmt.Printf("gornstatus %v\n", rns.VERSION)
		return
	}

	rns.SetLogDest(rns.LogStdout)
	verbosity := int(verbose)

	nameFilter := flag.Arg(0)

	if monitorMode {
		r, err := rns.NewReticulum(configDir)
		if err != nil {
			log.Fatal("No shared RNS instance available to get status from")
		}
		runMonitor(r, nameFilter, verbosity)
		return
	}

	exitCode := programSetup(programSetupParams{
		configDir:          configDir,
		dispAll:            showAll,
		verbosity:          verbosity,
		nameFilter:         nameFilter,
		jsonOutput:         jsonOutput,
		announceStats:      announceStats,
		linkStats:          linkStats,
		sorting:            sortKey,
		sortReverse:        sortReverse,
		remote:             remoteHash,
		managementIdentity: identityPath,
		remoteTimeout:      remoteTimeout,
		mustExit:           true,
		trafficTotals:      trafficTotals,
		discoveredIfaces:   discovered,
		configEntries:      detailedDiscovered,
	})
	os.Exit(exitCode)
}
