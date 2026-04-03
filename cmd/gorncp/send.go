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
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type statsEntry struct {
	Time   time.Time
	Got    float64
	PhyGot float64
}

func doSend(ts rns.Transport, idPath string, destHashHex string, filePath string, noCompress bool, silent bool, phyRates bool, timeoutSec float64) {
	id := prepareIdentity(idPath)

	destHash, err := rns.HexToBytes(destHashHex)
	if err != nil {
		log.Fatalf("Invalid destination hash: %v\n", err)
	}

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
			log.Fatalf("\r%v\rPath %q not found\n", strings.Repeat(" ", 60), destHashHex)
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
		fmt.Printf("Establishing link with %v  ", rns.PrettyHex(destHash))
	}
	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		log.Fatalf("Could not create link: %v\n", err)
	}

	var activeResource *rns.Resource
	activeLink := link
	setupSignalHandler(&activeResource, &activeLink)

	established := make(chan bool, 1)
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		established <- true
	})

	if err := link.Establish(); err != nil {
		log.Fatalf("Could not establish link: %v\n", err)
	}

	i := 0
	linkTimeout := time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))
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
			if time.Now().After(linkTimeout) {
				log.Fatalf("\r%v\rLink establishment timed out\n", strings.Repeat(" ", 60))
			}
		}
	}
established:

	if err := link.Identify(id); err != nil {
		log.Fatalf("Could not identify local node on link: %v\n", err)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Fatalf("File not found\n")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Could not read file: %v\n", err)
	}

	metadata := map[string][]byte{
		"name": []byte(filepath.Base(filePath)),
	}

	res, err := rns.NewResourceWithOptions(data, link, rns.ResourceOptions{
		AutoCompress: !noCompress,
		Metadata:     metadata,
	})
	if err != nil {
		log.Fatalf("Could not create resource: %v\n", err)
	}
	activeResource = res

	statsMax := 32
	stats := make([]statsEntry, 0)
	var statsMu sync.Mutex
	var speed, phySpeed float64

	done := make(chan bool, 1)
	res.SetCallback(func(r *rns.Resource) {
		done <- true
	})
	res.SetProgressCallback(func(r *rns.Resource) {
		now := time.Now()
		got := r.GetProgress() * float64(len(data))
		phyGot := r.GetSegmentProgress() * float64(r.TotalSize())

		statsMu.Lock()
		defer statsMu.Unlock()

		entry := statsEntry{
			Time:   now,
			Got:    got,
			PhyGot: phyGot,
		}
		stats = append(stats, entry)

		for len(stats) > statsMax {
			stats = stats[1:]
		}

		span := now.Sub(stats[0].Time).Seconds()
		if span == 0 {
			speed = 0
			phySpeed = 0
		} else {
			diff := got - stats[0].Got
			speed = diff / span

			phyDiff := phyGot - stats[0].PhyGot
			if phyDiff > 0 {
				phySpeed = phyDiff / span
			}
		}
	})

	if !silent {
		fmt.Printf("Advertising file resource  ")
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
				statsMu.Lock()
				s := speed
				ps := phySpeed
				statsMu.Unlock()
				if s == 0 {
					s = float64(len(data)) / duration.Seconds()
				}
				if ps == 0 {
					ps = float64(res.TotalSize()) / duration.Seconds()
				}

				phyStr := ""
				if phyRates {
					phyStr = " (" + rns.PrettySize(ps, "bps") + " at physical layer)"
				}
				fmt.Printf("\rTransfer complete  100.0%% - %v of %v in %v - %vps%v\n",
					rns.PrettySize(float64(len(data)), "B"),
					rns.PrettySize(float64(len(data)), "B"),
					rns.PrettyTime(duration.Seconds(), false, true),
					rns.PrettySize(s, "bps"),
					phyStr)
			}
			goto sent
		case <-time.After(100 * time.Millisecond):
			if !silent {
				// Update progress
				prg := res.GetProgress()
				segPrg := res.GetSegmentProgress()
				percent := prg * 100.0
				ps := rns.PrettySize(prg*float64(len(data)), "B")
				ts := rns.PrettySize(float64(len(data)), "B")
				duration := time.Since(start)

				statsMu.Lock()
				s := speed
				psRate := phySpeed
				statsMu.Unlock()

				if s == 0 {
					s = (prg * float64(len(data))) / duration.Seconds()
				}
				if psRate == 0 {
					psRate = (segPrg * float64(res.TotalSize())) / duration.Seconds()
				}

				phyStr := ""
				if phyRates {
					phyStr = " (" + rns.PrettySize(psRate, "bps") + " at physical layer)"
				}
				fmt.Printf("\rTransferring file %v %.1f%% - %v of %v - %vps%v  ",
					spinnerSymbols[i], percent, ps, ts, rns.PrettySize(s, "bps"), phyStr)
				i = (i + 1) % len(spinnerSymbols)
			}
		case <-time.After(60 * time.Second):
			log.Fatalf("\nFile transfer timed out")
		}
	}
sent:

	if res.Status() == rns.ResourceStatusComplete {
		if !silent {
			fmt.Printf("\n%v copied to %v\n", filePath, rns.PrettyHex(destHash))
		}
	} else {
		if !silent {
			fmt.Printf("\nThe transfer failed\n")
		}
		os.Exit(1)
	}

	link.Teardown()
	time.Sleep(250 * time.Millisecond)
}
