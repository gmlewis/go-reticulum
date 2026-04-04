// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornir is the Go port of the Reticulum Distributed Identity Resolver.
//
// It initializes the Reticulum network stack and exits. Use --exampleconfig
// to print a verbose configuration example, or --version to display the
// current version.
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
	configDir     string
	verbose       counter
	quiet         counter
	exampleConfig bool
	version       bool
}

func newApp() *appT {
	return &appT{}
}

func (a *appT) usage() {
	_, _ = fmt.Fprintf(flag.CommandLine.Output(), `
usage: gornir [-h] [--config CONFIG] [-v] [-q] [--exampleconfig] [--version]

Go Reticulum Distributed Identity Resolver

options:
  -h, --help       show this help message and exit
  --config CONFIG  path to alternative Reticulum config directory
  -v, --verbose    increase verbosity
  -q, --quiet      decrease verbosity
  --exampleconfig  print verbose configuration example to stdout and exit
  --version        show program's version number and exit
`)
}

func (a *appT) initFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.Var(&a.verbose, "v", "increase verbosity")
	fs.Var(&a.verbose, "verbose", "increase verbosity")
	fs.Var(&a.quiet, "q", "decrease verbosity")
	fs.Var(&a.quiet, "quiet", "decrease verbosity")
	fs.BoolVar(&a.exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	fs.BoolVar(&a.version, "version", false, "show program's version number and exit")
}

func (a *appT) run() {
	if a.version {
		fmt.Printf("gornir %v\n", rns.VERSION)
		return
	}

	if a.exampleConfig {
		fmt.Print(exampleRNSConfig)
		return
	}

	rns.SetLogDest(rns.LogStdout)
	if a.verbose != 0 || a.quiet != 0 {
		rns.SetLogLevel(int(a.verbose) - int(a.quiet))
	}

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, a.configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()
	// TODO: finish this
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
