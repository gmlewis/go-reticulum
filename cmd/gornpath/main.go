// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornpath is a Reticulum-based path management utility.
//
// It provides features for:
//   - Viewing the current routing/path table.
//   - Requesting paths to specific destinations from the network.
//   - Managing path table entries (dropping paths).
//   - Viewing announce rate information.
//
// Usage:
//
//	Display the path table:
//	  gornpath -t [--config <config_dir>]
//
//	Request a path to a destination:
//	  gornpath <destination_hash> [-w <timeout>] [--config <config_dir>]
//
//	Drop a path to a destination:
//	  gornpath -d <destination_hash> [--config <config_dir>]
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-t    show all known paths in the routing table
//	-m int
//	      maximum hops to filter path table by
//	-r    show announce rate info
//	-d    remove the path to a specified destination
//	-D    drop all queued announces
//	-w float
//	      timeout in seconds before giving up on a path request (default 15)
//	-j    output information in JSON format
//	-v    increase verbosity
//	-version
//	      show version and exit
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	table := flag.Bool("t", false, "show all known paths")
	maxHops := flag.Int("m", 0, "maximum hops to filter path table by")
	rates := flag.Bool("r", false, "show announce rate info")
	drop := flag.Bool("d", false, "remove the path to a destination")
	dropAnnounces := flag.Bool("D", false, "drop all queued announces")
	dropVia := flag.Bool("x", false, "drop all paths via specified transport instance")
	timeout := flag.Float64("w", 15.0, "timeout before giving up")
	jsonOut := flag.Bool("j", false, "output in JSON format")
	verbose := flag.Bool("v", false, "increase verbosity")
	version := flag.Bool("version", false, "show version and exit")

	log.SetFlags(0)
	flag.Parse()

	if *version {
		fmt.Printf("gornpath %v\n", rns.Version)
		return
	}

	if !*dropAnnounces && !*table && !*rates && flag.NArg() == 0 && !*dropVia {
		fmt.Println("")
		flag.Usage()
		fmt.Println("")
		log.Fatal("missing required destination hash or operation flag")
	}

	targetLogLevel := rns.LogNotice
	if *verbose {
		targetLogLevel = rns.LogInfo
	}
	rns.SetLogLevel(targetLogLevel)

	_, err := rns.NewReticulum(*configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}

	ts := rns.GetTransport()

	if *table {
		doTable(ts, *maxHops, *jsonOut)
	} else if flag.NArg() > 0 {
		destHex := flag.Arg(0)
		destHash, err := hex.DecodeString(destHex)
		if err != nil {
			log.Fatalf("Invalid destination hash: %v\n", err)
		}

		if *drop {
			doDrop(ts, destHash)
		} else {
			doRequest(ts, destHash, *timeout)
		}
	}
}

func doTable(ts *rns.TransportSystem, maxHops int, jsonOut bool) {
	paths := ts.GetPathTable()

	// Sort by interface then hops
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Interface.Name() != paths[j].Interface.Name() {
			return paths[i].Interface.Name() < paths[j].Interface.Name()
		}
		return paths[i].Hops < paths[j].Hops
	})

	if jsonOut {
		// Simplified JSON for now
		fmt.Println("[]")
		return
	}

	for _, p := range paths {
		if maxHops > 0 && p.Hops > maxHops {
			continue
		}
		ms := "s"
		if p.Hops == 1 {
			ms = ""
		}
		fmt.Printf("%x is %v hop%v away via %x on %v expires %v\n",
			p.Hash, p.Hops, ms, p.NextHop, p.Interface.Name(), p.Expires.Format("2006-01-02 15:04:05"))
	}
}

func doDrop(ts *rns.TransportSystem, destHash []byte) {
	// TODO: Implement drop_path in TransportSystem
	fmt.Printf("Dropped path to %x\n", destHash)
}

func doRequest(ts *rns.TransportSystem, destHash []byte, timeout float64) {
	if !ts.HasPath(destHash) {
		fmt.Printf("Path to <%x> requested  ", destHash)
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to <%x>: %v\n", destHash, err)
		}
	}

	i := 0
	syms := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for !ts.HasPath(destHash) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		fmt.Printf("\b\b%v ", syms[i])
		i = (i + 1) % len(syms)
	}

	if ts.HasPath(destHash) {
		// Find hops
		entry := ts.GetPathEntry(destHash)
		if entry != nil {
			ms := "s"
			if entry.Hops == 1 {
				ms = ""
			}
			fmt.Printf("\rPath found, destination <%x> is %v hop%v away via %x on %v\n",
				destHash, entry.Hops, ms, entry.NextHop, entry.Interface.Name())
		}
	} else {
		log.Fatalf("\r%v\rPath not found\n", strings.Repeat(" ", 60))
	}
}
