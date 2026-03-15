// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornstatus is a Reticulum-based status and statistics utility.
//
// It provides a quick overview of the current state of the Reticulum
// Network Stack, including:
//   - Configured network interfaces and their status (Up/Down).
//   - Transfer rates for each interface.
//   - Total traffic (sent and received) since the stack was initialized.
//
// Usage:
//
//	gornstatus [-a] [--config <config_dir>]
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-a    show all interfaces, including detached ones
//	-version
//	      show version and exit
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/gmlewis/go-reticulum/rns"
)

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	all := flag.Bool("a", false, "show all interfaces")
	version := flag.Bool("version", false, "show version and exit")
	flag.Parse()

	if *version {
		fmt.Printf("gornstatus %v\n", rns.VERSION)
		return
	}

	reticulum, err := rns.NewReticulum(*configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	_ = reticulum
	rns.SetCompactLogFmt(true)

	// Print summary
	fmt.Println("")
	fmt.Println(" Reticulum Network Stack status")
	fmt.Println(" ==============================")

	stats, err := reticulum.InterfaceStats()
	if err != nil {
		log.Fatalf("Could not get RNS status: %v", err)
	}

	if len(stats.Interfaces) == 0 {
		cfgPath := filepath.Join(*configDir, "config")
		if *configDir == "" {
			home, err := os.UserHomeDir()
			if err == nil {
				cfgPath = filepath.Join(home, ".reticulum", "config")
			}
		}
		log.Fatalf("Could not get RNS status: no interfaces are configured in %v. Add an [interfaces] section or pass --config <config_dir>.", cfgPath)
	}

	for _, iface := range stats.Interfaces {
		status := "Down"
		if iface.Status {
			status = "Up"
		}

		fmt.Println("")
		fmt.Printf(" %v[%v]\n", iface.Type, iface.Name)
		fmt.Printf("    Status    : %v\n", status)
		fmt.Printf("    Rate      : %v\n", rns.PrettySize(float64(iface.Bitrate), "bps"))

		rxbStr := "↓ " + rns.PrettySize(float64(iface.RXB), "B")
		txbStr := "↑ " + rns.PrettySize(float64(iface.TXB), "B")

		fmt.Printf("    Traffic   : %v\n", txbStr)
		fmt.Printf("                %v\n", rxbStr)
	}

	if *all {
		fmt.Println("")
		fmt.Printf(" Total Traffic: ↑ %v\n", rns.PrettySize(float64(stats.TXB), "B"))
		fmt.Printf("                ↓ %v\n", rns.PrettySize(float64(stats.RXB), "B"))
	}
	fmt.Println("")
}
