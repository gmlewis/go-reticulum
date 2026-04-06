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
package main

import (
	"crypto/rand"
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
	log.SetFlags(0)
	app, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == errHelp {
			return
		}
		log.Fatal(err)
	}

	if app.version {
		fmt.Printf("gornprobe %v\n", rns.VERSION)
		return
	}

	if len(app.args) < 2 {
		fmt.Println("")
		app.usage(os.Stdout)
		fmt.Println("")
		return
	}

	fullName := app.args[0]
	destHex := app.args[1]

	targetLogLevel := rns.LogNotice
	if app.verbose {
		targetLogLevel = rns.LogInfo
	}
	rns.SetLogLevel(targetLogLevel)

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, app.configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	destHash, err := parseProbeDestinationHash(destHex)
	if err != nil {
		fmt.Println(err)
		return
	}
	firstHopTimeout, err := ret.FirstHopTimeout(destHash)
	if err != nil {
		log.Fatalf("Could not determine first hop timeout: %v\n", err)
	}
	probeTimeout := probeTimeoutSeconds(app.timeout, firstHopTimeout)

	if !ts.HasPath(destHash) {
		fmt.Printf("Path to <%x> requested  ", destHash)
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to <%x>: %v\n", destHash, err)
		}
	}

	i := 0
	syms := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
	deadline := time.Now().Add(time.Duration(probeTimeout * float64(time.Second)))
	for !ts.HasPath(destHash) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		fmt.Printf("\b\b%v ", syms[i])
		i = (i + 1) % len(syms)
	}

	if !ts.HasPath(destHash) {
		log.Fatalf("\r%v\rPath request timed out\n", strings.Repeat(" ", 60))
	}

	remoteID := rns.RecallIdentity(ts, destHash)
	appName, aspects := splitProbeFullName(fullName)

	remoteDest, err := rns.NewDestination(ts, remoteID, rns.DestinationOut, rns.DestinationSingle, appName, aspects...)
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	sent := 0
	replies := 0
	for count := 0; count < app.probes; count++ {
		if sent > 0 {
			time.Sleep(time.Duration(app.wait * float64(time.Second)))
		}

		payload := make([]byte, app.size)
		rand.Read(payload)

		p := rns.NewPacket(remoteDest, payload)
		if err := p.Pack(); err != nil {
			log.Fatalf("Error: Probe packet size of %v bytes exceed MTU of %v bytes\n", len(p.Raw), rns.MTU)
		}

		fmt.Printf("\rSent probe %v (%v bytes) to <%x>  ", sent+1, app.size, destHash)

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
