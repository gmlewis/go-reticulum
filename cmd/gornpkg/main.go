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
		fmt.Print(exampleRnpkgConfig + "\n")
		return
	}

	logger := rns.NewLogger()
	if err := programSetup(logger, a.configDir, a.verbose, a.quiet, rns.NewReticulumWithLogger); err != nil {
		log.Fatalf("Could not initialize Reticulum: %v", err)
	}
}

type reticulumFactory func(rns.Transport, string, *rns.Logger) (*rns.Reticulum, error)

func programSetup(logger *rns.Logger, configDir string, verbosity, quietness counter, newReticulum reticulumFactory) (err error) {
	if logger == nil {
		logger = rns.NewLogger()
	}
	logger.SetLogDest(rns.LogStdout)
	logger.SetLogLevel(int(verbosity) - int(quietness))

	ts := rns.NewTransportSystem()
	ret, err := newReticulum(ts, configDir, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := ret.Close(); closeErr != nil {
			err = closeErr
		}
	}()
	return nil
}

func main() {
	log.SetFlags(0)
	app, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == errHelp {
			return
		}
		log.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println()
		os.Exit(0)
	}()

	app.run()
}
