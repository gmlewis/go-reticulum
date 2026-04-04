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

type appT struct {
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
}

func newApp() *appT {
	return &appT{}
}

func (a *appT) usage() {
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

func (a *appT) initFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.BoolVar(&a.showVersion, "version", false, "show program's version number and exit")
	fs.BoolVar(&a.showAll, "a", false, "show all interfaces")
	fs.BoolVar(&a.showAll, "all", false, "show all interfaces")
	fs.BoolVar(&a.announceStats, "A", false, "show announce stats")
	fs.BoolVar(&a.announceStats, "announce-stats", false, "show announce stats")
	fs.BoolVar(&a.linkStats, "l", false, "show link stats")
	fs.BoolVar(&a.linkStats, "link-stats", false, "show link stats")
	fs.BoolVar(&a.trafficTotals, "t", false, "display traffic totals")
	fs.BoolVar(&a.trafficTotals, "totals", false, "display traffic totals")
	fs.StringVar(&a.sortKey, "s", "", "sort interfaces by [rate, traffic, rx, tx, rxs, txs, announces, arx, atx, held]")
	fs.StringVar(&a.sortKey, "sort", "", "sort interfaces by [rate, traffic, rx, tx, rxs, txs, announces, arx, atx, held]")
	fs.BoolVar(&a.sortReverse, "r", false, "reverse sorting")
	fs.BoolVar(&a.sortReverse, "reverse", false, "reverse sorting")
	fs.BoolVar(&a.jsonOutput, "j", false, "output in JSON format")
	fs.BoolVar(&a.jsonOutput, "json", false, "output in JSON format")
	fs.StringVar(&a.remoteHash, "R", "", "transport identity hash of remote instance to get status from")
	fs.StringVar(&a.identityPath, "i", "", "path to identity used for remote management")
	fs.Float64Var(&a.remoteTimeout, "w", 15.0, "timeout before giving up on remote queries")
	fs.BoolVar(&a.discovered, "d", false, "list discovered interfaces")
	fs.BoolVar(&a.discovered, "discovered", false, "list discovered interfaces")
	fs.BoolVar(&a.detailedDiscovered, "D", false, "show details and config entries for discovered interfaces")
	fs.BoolVar(&a.monitorMode, "m", false, "continuously monitor status")
	fs.BoolVar(&a.monitorMode, "monitor", false, "continuously monitor status")
	fs.Float64Var(&a.monitorInterval, "I", 1.0, "refresh interval for monitor mode (default: 1)")
	fs.Float64Var(&a.monitorInterval, "monitor-interval", 1.0, "refresh interval for monitor mode (default: 1)")
	fs.Var(&a.verbose, "v", "increase verbosity")
	fs.Var(&a.verbose, "verbose", "increase verbosity")
}

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
