// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func doSend(idPath string, destHashHex string, filePath string, noCompress bool, silent bool, phyRates bool, timeoutSec float64) {
	if idPath == "" {
		home, _ := os.UserHomeDir()
		idPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}
	id, err := rns.FromFile(idPath)
	if err != nil {
		log.Fatalf("Could not load identity: %v\n", err)
	}
	_ = id // TODO: FIX THIS!!!

	destHash, err := rns.HexToBytes(destHashHex)
	if err != nil {
		log.Fatalf("Invalid destination hash: %v\n", err)
	}

	ts := rns.NewTransportSystem()
	remoteID := rns.RecallIdentity(ts, destHash)
	if remoteID == nil {
		if !silent {
			fmt.Printf("Path to <%x> requested  ", destHash)
		}
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to <%x>: %v\n", destHash, err)
		}

		i := 0
		timeout := time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))
		for !ts.HasPath(destHash) && time.Now().Before(timeout) {
			time.Sleep(100 * time.Millisecond)
			if !silent {
				fmt.Printf("\b\b%v ", spinnerSymbols[i])
				i = (i + 1) % len(spinnerSymbols)
			}
		}

		if !ts.HasPath(destHash) {
			log.Fatalf("\r%v\rPath not found\n", strings.Repeat(" ", 60))
		}
		if !silent {
			fmt.Printf("\b\b \n")
		}
		remoteID = rns.RecallIdentity(ts, destHash)
	}

	remoteDest, err := rns.NewDestination(ts, remoteID, rns.DestinationOut, rns.DestinationSingle, AppName, "receive")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	if !silent {
		fmt.Printf("Establishing link with remote transport instance...  ")
	}
	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		log.Fatalf("Could not create link: %v\n", err)
	}

	established := make(chan bool, 1)
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		established <- true
	})

	if err := link.Establish(); err != nil {
		log.Fatalf("Could not establish link: %v\n", err)
	}

	i := 0
	select {
	case <-established:
		if !silent {
			fmt.Printf("\b\b \n")
		}
	case <-time.After(10 * time.Second):
		log.Fatalf("\r%v\rLink establishment timed out\n", strings.Repeat(" ", 60))
	default:
		for {
			select {
			case <-established:
				if !silent {
					fmt.Printf("\b\b \n")
				}
				goto established
			case <-time.After(100 * time.Millisecond):
				if !silent {
					fmt.Printf("\b\b%v ", spinnerSymbols[i])
					i = (i + 1) % len(spinnerSymbols)
				}
			}
		}
	}
established:

	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Could not read file: %v\n", err)
	}

	res, err := rns.NewResourceWithOptions(data, link, rns.ResourceOptions{AutoCompress: !noCompress})
	if err != nil {
		log.Fatalf("Could not create resource: %v\n", err)
	}

	done := make(chan bool, 1)
	res.SetCallback(func(r *rns.Resource) {
		done <- true
	})

	if !silent {
		fmt.Printf("Sending %v (%v bytes)...\n", filepath.Base(filePath), len(data))
	}
	if err := res.Advertise(); err != nil {
		log.Fatalf("Could not advertise resource: %v\n", err)
	}

	start := time.Now()
	i = 0
	for {
		select {
		case <-done:
			if !silent {
				duration := time.Since(start)
				speed := float64(len(data)) / duration.Seconds()
				fmt.Printf("\rTransfer complete  100.0%% - %v of %v in %v - %vps\n",
					rns.PrettySize(float64(len(data)), "B"),
					rns.PrettySize(float64(len(data)), "B"),
					rns.PrettyTime(duration.Seconds(), false, true),
					rns.PrettySize(speed, "bps"))
			}
			goto sent
		case <-time.After(100 * time.Millisecond):
			if !silent {
				// Update progress
				prg := res.GetProgress()
				percent := prg * 100.0
				ps := rns.PrettySize(prg*float64(len(data)), "B")
				ts := rns.PrettySize(float64(len(data)), "B")
				duration := time.Since(start)
				speed := (prg * float64(len(data))) / duration.Seconds()
				fmt.Printf("\rTransferring file %v %.1f%% - %v of %v - %vps  ",
					spinnerSymbols[i], percent, ps, ts, rns.PrettySize(speed, "bps"))
				i = (i + 1) % len(spinnerSymbols)
			}
		case <-time.After(60 * time.Second):
			log.Fatalf("\nFile transfer timed out")
		}
	}
sent:

	link.Teardown()
	time.Sleep(100 * time.Millisecond)
}
