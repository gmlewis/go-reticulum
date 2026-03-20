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

func doFetch(idPath string, destHashHex string, fileName string, noCompress bool, silent bool, savePath string, overwrite bool, phyRates bool, timeoutSec float64) {
	_ = noCompress
	if idPath == "" {
		home, _ := os.UserHomeDir()
		idPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}
	id, err := rns.FromFile(idPath)
	if err != nil {
		log.Fatalf("Could not load identity: %v\n", err)
	}

	destHash, err := rns.HexToBytes(destHashHex)
	if err != nil {
		log.Fatalf("Invalid destination hash: %v\n", err)
	}

	ts := rns.NewTransportSystem()
	remoteID := rns.RecallIdentity(ts, destHash)
	if remoteID == nil {
		fmt.Printf("Path to <%x> requested  ", destHash)
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to <%x>: %v\n", destHash, err)
		}

		i := 0
		syms := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
		timeout := time.Now().Add(15 * time.Second)
		for !ts.HasPath(destHash) && time.Now().Before(timeout) {
			time.Sleep(100 * time.Millisecond)
			fmt.Printf("\b\b%v ", syms[i])
			i = (i + 1) % len(syms)
		}

		if !ts.HasPath(destHash) {
			log.Fatalf("\r%v\rPath request timed out\n", strings.Repeat(" ", 60))
		}
		fmt.Printf("\b\b \n")
		remoteID = rns.RecallIdentity(ts, destHash)
	}
	remoteDest, err := rns.NewDestination(ts, remoteID, rns.DestinationOut, rns.DestinationSingle, AppName, "receive")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	fmt.Printf("Establishing link with remote transport instance...  ")
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
		case <-time.After(10 * time.Second):
			log.Fatalf("\r%v\rLink establishment timed out\n", strings.Repeat(" ", 60))
		}
	}
established:

	if err := link.Identify(id); err != nil {
		log.Fatalf("Could not identify local node on link: %v\n", err)
	}

	requestResolved := make(chan bool, 1)
	resourceStarted := make(chan *rns.Resource, 1)

	link.SetResourceCallback(func(adv *rns.ResourceAdvertisement) bool {
		return true
	})

	link.SetResourceStartedCallback(func(res *rns.Resource) {
		resourceStarted <- res
	})

	if !silent {
		fmt.Printf("Requesting %v from remote...  ", fileName)
	}
	_, err = link.Request("fetch_file", []byte(fileName), func(rr *rns.RequestReceipt) {
		if rr.Status == rns.RequestReady {
			requestResolved <- true
		} else {
			log.Fatalf("\r%v\rRequest failed with status %v\n", strings.Repeat(" ", 60), rr.Status)
		}
	}, nil, nil, 0)

	if err != nil {
		log.Fatalf("Request failed: %v\n", err)
	}

	i = 0
	for {
		select {
		case <-requestResolved:
			if !silent {
				fmt.Printf("\b\b \n")
			}
			goto requested
		case <-time.After(100 * time.Millisecond):
			if !silent {
				fmt.Printf("\b\b%v ", spinnerSymbols[i])
				i = (i + 1) % len(spinnerSymbols)
			}
		case <-time.After(10 * time.Second):
			log.Fatalf("\r%v\rFetch request timed out\n", strings.Repeat(" ", 60))
		}
	}
requested:

	select {
	case res := <-resourceStarted:
		done := make(chan bool, 1)
		res.SetCallback(func(r *rns.Resource) {
			done <- true
		})

		if !silent {
			fmt.Printf("Downloading %v...  ", fileName)
		}
		start := time.Now()
		i = 0
		for {
			select {
			case <-done:
				if !silent {
					duration := time.Since(start)
					speed := float64(res.TotalSize()) / duration.Seconds()
					fmt.Printf("\rTransfer complete  100.0%% - %v of %v in %v - %vps\n",
						rns.PrettySize(float64(res.TotalSize()), "B"),
						rns.PrettySize(float64(res.TotalSize()), "B"),
						rns.PrettyTime(duration.Seconds(), false, true),
						rns.PrettySize(speed, "bps"))
				}
				goto fetched
			case <-time.After(100 * time.Millisecond):
				if !silent {
					prg := res.GetProgress()
					percent := prg * 100.0
					ps := rns.PrettySize(prg*float64(res.TotalSize()), "B")
					ts := rns.PrettySize(float64(res.TotalSize()), "B")
					duration := time.Since(start)
					speed := (prg * float64(res.TotalSize())) / duration.Seconds()
					fmt.Printf("\rTransferring file %v %.1f%% - %v of %v - %vps  ",
						spinnerSymbols[i], percent, ps, ts, rns.PrettySize(speed, "bps"))
					i = (i + 1) % len(spinnerSymbols)
				}
			case <-time.After(60 * time.Second):
				log.Fatalf("\nFile download timed out")
			}
		}
	case <-time.After(10 * time.Second):
		log.Fatalf("Timed out waiting for resource transfer to start")
	}
fetched:

	link.Teardown()
	time.Sleep(100 * time.Millisecond)
}
