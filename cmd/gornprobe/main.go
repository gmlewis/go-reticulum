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
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	// DefaultProbeSize is the default size of the probe packet payload in bytes.
	DefaultProbeSize = 16
	// DefaultTimeout is the default timeout in seconds before giving up on a probe.
	DefaultTimeout = 12.0
)

type runtimeT struct {
	app    *appT
	logger *rns.Logger
}

func newRuntime(app *appT) *runtimeT {
	if app == nil {
		app = &appT{}
	}
	return &runtimeT{app: app, logger: rns.NewLogger()}
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
	newRuntime(app).run()
}

func (rt *runtimeT) run() {
	if rt == nil || rt.app == nil {
		return
	}
	app := rt.app
	logger := rt.logger
	app.logger = logger

	if app.version {
		fmt.Printf("gornprobe %v\n", rns.VERSION)
		return
	}

	cleanup := func() {}
	setupSignalHandler(func() {
		cleanup()
	})

	if len(app.args) < 2 {
		fmt.Println("")
		app.usage(os.Stdout)
		fmt.Println("")
		return
	}

	fullName := app.args[0]
	destHex := app.args[1]
	destHash, err := parseProbeDestinationHash(destHex)
	if err != nil {
		fmt.Println(err)
		return
	}

	targetLogLevel := rns.LogNotice
	if app.verbose {
		targetLogLevel = rns.LogInfo
	}
	logger.SetLogLevel(targetLogLevel)

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, app.configDir, logger)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	cleanup = func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum properly: %v", err)
		}
	}
	defer func() {
		cleanup()
	}()

	firstHopTimeout, err := ret.FirstHopTimeout(destHash)
	if err != nil {
		log.Fatalf("Could not determine first hop timeout: %v\n", err)
	}
	probeTimeout := probeTimeoutSeconds(app.timeout, firstHopTimeout)

	if err := waitForProbePath(os.Stdout, ts, destHash, probeTimeout); err != nil {
		if err == errPathRequestTimedOut {
			os.Exit(1)
		}
		log.Fatal(err)
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
		sleepBetweenProbes(sent, app.wait, time.Sleep)

		payload := make([]byte, app.size)
		rand.Read(payload)

		p := rns.NewPacket(remoteDest, payload)
		if err := p.Pack(); err != nil {
			fmt.Println(formatProbeMTUError(len(p.Raw)))
			os.Exit(3)
		}

		more := ""
		if app.verbose {
			entry := ts.GetPathEntry(destHash)
			if entry != nil {
				ifName := ""
				if entry.Interface != nil {
					ifName = entry.Interface.Name()
				}
				more = formatProbeVerboseMore(entry.NextHop, ifName)
			}
		}
		fmt.Print(formatProbeSentLine(sent+1, app.size, destHash, more))

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

		if waitForProbeReceiptAt(os.Stdout, receipt, probeTimeout, time.Now, time.Sleep) {
			fmt.Printf("\b\b \n")
			replies++
			hops := ts.GetPathEntry(destHash).Hops
			receptionStats := ""
			if app.verbose {
				var rssi, snr, q *float64
				if value, err := ret.PacketRSSI(receipt.Hash); err == nil {
					if typed, ok := value.(float64); ok {
						rssi = &typed
					}
				}
				if value, err := ret.PacketSNR(receipt.Hash); err == nil {
					if typed, ok := value.(float64); ok {
						snr = &typed
					}
				}
				if value, err := ret.PacketQ(receipt.Hash); err == nil {
					if typed, ok := value.(float64); ok {
						q = &typed
					}
				}
				receptionStats = formatProbeReceptionStats(rssi, snr, q)
			}
			fmt.Print(formatProbeReplyLine(destHash, time.Since(startTime).Seconds(), hops, receptionStats))
		}
	}

	if summary, exitCode := formatProbeLossSummary(sent, replies); true {
		fmt.Println(summary)
		if exitCode > 0 {
			os.Exit(exitCode)
		}
	}
}
