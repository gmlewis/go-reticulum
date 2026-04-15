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

type runtimeT struct {
	app          *appT
	logger       *rns.Logger
	newReticulum reticulumFactory
}

func newRuntime(app *appT) *runtimeT {
	if app == nil {
		app = &appT{}
	}
	return &runtimeT{app: app, logger: rns.NewLogger(), newReticulum: rns.NewReticulumWithLogger}
}

func (rt *runtimeT) run() bool {
	if rt == nil || rt.app == nil {
		return false
	}

	if rt.app.version {
		fmt.Printf("gornpkg %v\n", rns.VERSION)
		return false
	}

	if rt.app.exampleConfig {
		fmt.Print(exampleRnpkgConfig + "\n")
		return false
	}

	if err := rt.programSetup(); err != nil {
		log.Fatalf("Could not initialize Reticulum: %v", err)
	}
	return true
}

type reticulumFactory func(rns.Transport, string, *rns.Logger) (*rns.Reticulum, error)

func (rt *runtimeT) programSetup() (err error) {
	logger := rt.logger
	logger.SetLogDest(rns.LogStdout)
	logger.SetLogLevel(int(rt.app.verbose) - int(rt.app.quiet))

	ts := rns.NewTransportSystem(logger)
	ret, err := rt.newReticulum(ts, rt.app.configDir, logger)
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
	defer signal.Stop(sig)

	if newRuntime(app).run() {
		sig := <-sig
		if sig == os.Interrupt {
			fmt.Println()
		}
	}
}
