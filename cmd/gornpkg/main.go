// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornpkg is the Go port of the Reticulum Meta Package Manager.
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

func init() {
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), `
usage: gornpkg [-h] [--config CONFIG] [-v] [-q] [--exampleconfig] [--version]

Reticulum Meta Package Manager

options:
  -h, --help       show this help message and exit
  --config CONFIG  path to alternative Reticulum config directory
  -v, --verbose    increase verbosity
  -q, --quiet      decrease verbosity
  --exampleconfig  print verbose configuration example to stdout and exit
  --version        show program's version number and exit
`)
	}

	flag.StringVar(&configDir, "config", "", "path to alternative Reticulum config directory")
	flag.Var(&verbose, "v", "increase verbosity")
	flag.Var(&verbose, "verbose", "increase verbosity")
	flag.Var(&quiet, "q", "decrease verbosity")
	flag.Var(&quiet, "quiet", "decrease verbosity")
	flag.BoolVar(&exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	flag.BoolVar(&version, "version", false, "show program's version number and exit")
}

var (
	configDir     string
	verbose       counter
	quiet         counter
	exampleConfig bool
	version       bool
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

	if version {
		fmt.Printf("gornpkg %v\n", rns.VERSION)
		return
	}

	if exampleConfig {
		fmt.Print(exampleRnpkgConfig)
		return
	}

	rns.SetLogDest(rns.LogStdout)
	if verbose != 0 || quiet != 0 {
		rns.SetLogLevel(int(verbose) - int(quiet))
	}

	if _, err := rns.NewReticulum(configDir); err != nil {
		log.Fatalf("Could not initialize Reticulum: %v", err)
	}
	os.Exit(0)
}
