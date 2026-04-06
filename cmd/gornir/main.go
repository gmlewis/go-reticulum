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
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/utils"
)

func (a *appT) run() {
	if a.version {
		utils.PrintVersion(os.Stdout, "gornir", rns.VERSION)
		return
	}

	if a.exampleConfig {
		fmt.Print(exampleRNSConfig)
		return
	}

	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogStdout)
	if a.verbose != 0 || a.quiet != 0 {
		logger.SetLogLevel(int(a.verbose) - int(a.quiet))
	}

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulumWithLogger(ts, a.configDir, logger)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Log(fmt.Sprintf("Warning: Could not close Reticulum properly: %v", err), rns.LogWarning, false)
		}
	}()
	// TODO: finish this
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

	app, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == utils.ErrHelp {
			return
		}
		log.Fatal(err)
	}
	app.run()
}
