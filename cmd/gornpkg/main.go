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
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
)

func (a *appT) run() {
	if a.version {
		fmt.Printf("gornpkg %v\n", rns.VERSION)
		return
	}

	if a.exampleConfig {
		fmt.Print(exampleRnpkgConfig)
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
	// TODO: Finish this.
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
