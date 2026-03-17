// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornprobe is a Reticulum-based connectivity probing utility.
//
// It provides features for:
//   - Probing the reachability of remote destinations.
//   - Measuring round-trip time (RTT) to network nodes.
//   - Reporting packet loss and physical layer statistics.
//
// Usage:
//
//	Probe a destination:
//	  gornprobe <full_name> <destination_hash> [-n <count>] [-s <size>]
//
//	Example:
//	  gornprobe exampleapp.aspect1 b009b7fab8c97ae05027a1d5740d00f0 -n 5
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-n int
//	      number of probes to send (default 1)
//	-s int
//	      size of probe packet payload in bytes (default 16)
//	-t float
//	      timeout in seconds before giving up on a probe
//	-w float
//	      time in seconds to wait between each probe
//	-v    increase verbosity
//	-version
//	      show version and exit
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	// DefaultProbeSize is the default size of the probe packet payload in bytes.
	DefaultProbeSize = 16
	// DefaultTimeout is the default timeout in seconds before giving up on a probe.
	DefaultTimeout = 12.0
)

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	size := flag.Int("s", DefaultProbeSize, "size of probe packet payload in bytes")
	probes := flag.Int("n", 1, "number of probes to send")
	timeout := flag.Float64("t", 0, "timeout before giving up")
	wait := flag.Float64("w", 0, "time between each probe")
	verbose := flag.Bool("v", false, "increase verbosity")
	version := flag.Bool("version", false, "show version and exit")

	log.SetFlags(0)
	flag.Parse()

	if *version {
		fmt.Printf("gornprobe %v\n", rns.VERSION)
		return
	}

	if flag.NArg() < 2 {
		fmt.Println("")
		fmt.Println("The full destination name including application name aspects must be specified for the destination")
		fmt.Println("")
		flag.Usage()
		fmt.Println("")
		log.Fatal("destination full_name and destination_hash are required")
	}

	fullName := flag.Arg(0)
	destHex := flag.Arg(1)

	targetLogLevel := rns.LogNotice
	if *verbose {
		targetLogLevel = rns.LogInfo
	}
	rns.SetLogLevel(targetLogLevel)

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, *configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	defer ret.Close()

	destHash, err := hex.DecodeString(destHex)
	if err != nil {
		log.Fatalf("Invalid destination hash: %v\n", err)
	}

	if !ts.HasPath(destHash) {
		fmt.Printf("Path to <%x> requested  ", destHash)
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to <%x>: %v\n", destHash, err)
		}
	}

	// Wait for path
	i := 0
	syms := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
	pathTimeout := 15.0
	deadline := time.Now().Add(time.Duration(pathTimeout * float64(time.Second)))
	for !ts.HasPath(destHash) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		fmt.Printf("\b\b%v ", syms[i])
		i = (i + 1) % len(syms)
	}

	if !ts.HasPath(destHash) {
		log.Fatalf("\r%v\rPath request timed out\n", strings.Repeat(" ", 60))
	}

	remoteID := rns.RecallIdentity(ts, destHash)

	// Parse app name and aspects
	parts := strings.Split(fullName, ".")
	if len(parts) == 0 {
		log.Fatalf("Invalid full name")
	}
	appName := parts[0]
	var aspects []string
	if len(parts) > 1 {
		aspects = parts[1:]
	}

	remoteDest, err := rns.NewDestination(ts, remoteID, rns.DestinationOut, rns.DestinationSingle, appName, aspects...)
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	sent := 0
	replies := 0
	for count := 0; count < *probes; count++ {
		if sent > 0 {
			time.Sleep(time.Duration(*wait * float64(time.Second)))
		}

		payload := make([]byte, *size)
		rand.Read(payload)

		p := rns.NewPacket(remoteDest, payload)
		if err := p.Pack(); err != nil {
			log.Fatalf("Error: Probe packet size of %v bytes exceed MTU of %v bytes\n", len(p.Raw), rns.MTU)
		}

		fmt.Printf("\rSent probe %v (%v bytes) to <%x>  ", sent+1, *size, destHash)

		startTime := time.Now()
		if err := p.Send(); err != nil {
			log.Fatalf("Error sending probe: %v\n", err)
		}
		sent++

		receipt := p.Receipt
		if receipt == nil {
			fmt.Println("No receipt generated")
			continue
		}

		// Wait for delivery
		probeTimeout := *timeout
		if probeTimeout == 0 {
			probeTimeout = DefaultTimeout
		}
		deadline := time.Now().Add(time.Duration(probeTimeout * float64(time.Second)))

		delivered := false
		i = 0
		for time.Now().Before(deadline) {
			if receipt.Status == rns.ReceiptDelivered {
				delivered = true
				break
			}
			time.Sleep(100 * time.Millisecond)
			fmt.Printf("\b\b%v ", syms[i])
			i = (i + 1) % len(syms)
		}

		if delivered {
			fmt.Printf("\b\b \n")
			replies++
			rtt := time.Since(startTime).Seconds()
			rttStr := ""
			if rtt >= 1.0 {
				rttStr = fmt.Sprintf("%.3f seconds", rtt)
			} else {
				rttStr = fmt.Sprintf("%.3f milliseconds", rtt*1000)
			}

			hops := ts.GetPathEntry(destHash).Hops
			ms := "s"
			if hops == 1 {
				ms = ""
			}

			fmt.Printf("Valid reply from <%x>\nRound-trip time is %v over %v hop%v\n", destHash, rttStr, hops, ms)
		} else {
			fmt.Printf("\r%v\rProbe timed out\n", strings.Repeat(" ", 64))
		}
	}

	loss := (1.0 - float64(replies)/float64(sent)) * 100.0
	fmt.Printf("Sent %v, received %v, packet loss %.2f%%\n", sent, replies, loss)
	if loss > 0 {
		os.Exit(2)
	}
}
