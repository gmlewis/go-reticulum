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
	"strconv"
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
		timeout := time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))
		for !ts.HasPath(destHash) && time.Now().Before(timeout) {
			time.Sleep(100 * time.Millisecond)
			fmt.Printf("\b\b%v ", syms[i])
			i = (i + 1) % len(syms)
		}

		if !ts.HasPath(destHash) {
			log.Fatalf("\r%v\rPath not found\n", strings.Repeat(" ", 60))
		}
		fmt.Printf("\b\b \n")
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

	requestResolved := make(chan bool, 1)
	resourceStarted := make(chan *rns.Resource, 1)

	link.SetResourceCallback(func(adv *rns.ResourceAdvertisement) bool {
		return true
	})

	link.SetResourceStartedCallback(func(res *rns.Resource) {
		activeResource = res
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
		res.SetProgressCallback(func(r *rns.Resource) {
			// Progress callback for tracking transfer progress
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
				if res.Status() == rns.ResourceStatusComplete {
					metadata := res.Metadata()
					if metadata == nil {
						log.Fatalf("Invalid data received, ignoring resource")
					}

					nameBytes, ok := metadata["name"]
					if !ok {
						log.Fatalf("Invalid data received, ignoring resource")
					}

					filename := filepath.Base(string(nameBytes))
					counter := 0
					var savedFilename string

					if savePath != "" {
						savedFilename = filepath.Clean(filepath.Join(savePath, filename))
						if !strings.HasPrefix(savedFilename, savePath+"/") {
							log.Fatalf("Invalid save path %v, ignoring", savedFilename)
						}
					} else {
						savedFilename = filename
					}

					fullSavePath := savedFilename
					if overwrite {
						if _, err := os.Stat(fullSavePath); err == nil {
							if err := os.Remove(fullSavePath); err != nil {
								rns.Logf("Could not overwrite existing file %v, renaming instead", rns.LogError, false, fullSavePath)
							}
						}
					}

					for {
						if _, err := os.Stat(fullSavePath); os.IsNotExist(err) {
							break
						}
						counter++
						fullSavePath = savedFilename + "." + strconv.Itoa(counter)
					}

					if err := os.WriteFile(fullSavePath, res.Data(), 0o644); err != nil {
						log.Fatalf("An error occurred while saving received resource: %v", err)
					}

					if !silent {
						fmt.Printf("\n%v fetched from %v\n", fileName, rns.PrettyHex(destHash))
					}
				} else {
					if !silent {
						fmt.Printf("\nThe transfer failed\n")
					}
					os.Exit(1)
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
