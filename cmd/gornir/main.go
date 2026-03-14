// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornir is a Reticulum-based distributed identity resolver.
//
// In its current implementation, it acts as a placeholder for distributed identity
// and destination resolution services. It initializes the Reticulum stack and
// stays active to participate in network propagation and resolution requests.
//
// Usage:
//
//	gornir [-v] [-q] [--config <config_dir>]
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-v    increase verbosity
//	-q    decrease verbosity
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/gmlewis/go-reticulum/rns"
)

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	verbose := flag.Bool("v", false, "increase verbosity")
	quiet := flag.Bool("q", false, "decrease verbosity")
	version := flag.Bool("version", false, "show version and exit")
	flag.Parse()

	if *version {
		fmt.Printf("gornir %v\n", rns.VERSION)
		return
	}

	if *verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if *quiet {
		rns.SetLogLevel(rns.LogWarning)
	}

	if _, err := rns.NewReticulum(*configDir); err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
}
