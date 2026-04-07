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

type runtimeT struct {
	app    *appT
	logger *rns.Logger
}

func newRuntime(app *appT) *runtimeT {
	if app == nil {
		app = newApp()
	}
	return &runtimeT{app: app, logger: rns.NewLogger()}
}

func (rt *runtimeT) run() {
	if rt == nil || rt.app == nil {
		return
	}
	app := rt.app
	logger := rt.logger

	if app.version {
		utils.PrintVersion(os.Stdout, "gornir", rns.VERSION)
		return
	}

	if app.exampleConfig {
		fmt.Print(exampleRNSConfig)
		return
	}

	logger.SetLogDest(rns.LogStdout)
	if app.verbose != 0 || app.quiet != 0 {
		logger.SetLogLevel(int(app.verbose) - int(app.quiet))
	}

	ts := rns.NewTransportSystem(rt.logger)
	ret, err := rns.NewReticulumWithLogger(ts, app.configDir, logger)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum properly: %v", err)
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
	newRuntime(app).run()
}
