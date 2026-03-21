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

// setupSignalHandler installs a signal handler for SIGINT (Ctrl+C)
// that cleans up resources and links before exiting.
func setupSignalHandler(resource **rns.Resource, link **rns.Link) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("")
		if resource != nil && *resource != nil {
			fmt.Println("Cancelling resource...")
			(*resource).Cancel()
		}
		if link != nil && *link != nil {
			fmt.Println("Tearing down link...")
			(*link).Teardown()
		}
		os.Exit(0)
	}()
}
