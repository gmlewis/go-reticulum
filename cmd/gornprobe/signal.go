// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
)

func setupSignalHandler(logger *rns.Logger, cleanup func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("")
		if cleanup != nil {
			cleanup()
		}
		// The rns logger writes asynchronously; flush it before exiting so
		// interrupt-time diagnostics are not silently lost.
		if logger != nil {
			logger.Close()
		}
		os.Exit(0)
	}()
}
