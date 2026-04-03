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

func fetchResponseCode(response any) (int, bool) {
	switch code := response.(type) {
	case int:
		return code, true
	case int8:
		return int(code), true
	case int16:
		return int(code), true
	case int32:
		return int(code), true
	case int64:
		return int(code), true
	case uint:
		return int(code), true
	case uint8:
		return int(code), true
	case uint16:
		return int(code), true
	case uint32:
		return int(code), true
	case uint64:
		if code > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(code), true
	default:
		return 0, false
	}
}

func waitForDownloadCompletion(done <-chan struct{}, timeout time.Duration, onTick func()) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			if onTick != nil {
				onTick()
			}
		case <-timer.C:
			return fmt.Errorf("File download timed out")
		}
	}
}

func doFetch(ts rns.Transport, idPath string, destHashHex string, fileName string, noCompress bool, silent bool, savePath string, overwrite bool, phyRates bool, timeoutSec float64) {
	_ = noCompress
	_ = phyRates
	id := prepareIdentity(idPath)

	destHash, err := parseDestinationHash(destHashHex)
	if err != nil {
		log.Fatalf("%v\n", err)
	}

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
			log.Fatalf("\r%v\rPath %q not found\n", strings.Repeat(" ", 60), destHashHex)
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
			if rr.Response == false {
				if !silent {
					fmt.Printf("\r%v\r", strings.Repeat(" ", 60))
				}
				fmt.Printf("Fetch request failed, the file %v was not found on the remote\n", fileName)
				link.Teardown()
				sleepAfterFetchFailure(time.Sleep)
				os.Exit(0)
			} else if rr.Response == nil {
				if !silent {
					fmt.Printf("\r%v\r", strings.Repeat(" ", 60))
				}
				fmt.Printf("Fetch request failed due to an error on the remote system\n")
				link.Teardown()
				sleepAfterFetchFailure(time.Sleep)
				os.Exit(0)
			} else if code, ok := fetchResponseCode(rr.Response); ok && code == int(rns.ReqFetchNotAllowed) {
				if !silent {
					fmt.Printf("\r%v\r", strings.Repeat(" ", 60))
				}
				fmt.Printf("Fetch request failed, fetching the file %v was not allowed by the remote\n", fileName)
				link.Teardown()
				sleepAfterFetchFailure(time.Sleep)
				os.Exit(0)
			}
			requestResolved <- true
		} else {
			if !silent {
				fmt.Printf("\r%v\r", strings.Repeat(" ", 60))
			}
			fmt.Printf("Fetch request failed due to an unknown error (probably not authorised)\n")
			link.Teardown()
			sleepAfterFetchFailure(time.Sleep)
			os.Exit(0)
		}
	}, nil, nil, 0)

	if err != nil {
		log.Fatalf("Request failed: %v\n", err)
	}

	i = 0
	requestTicker := time.NewTicker(100 * time.Millisecond)
	defer requestTicker.Stop()
	requestTimeout := time.NewTimer(10 * time.Second)
	defer requestTimeout.Stop()
	for {
		select {
		case <-requestResolved:
			if !silent {
				fmt.Printf("\b\b \n")
			}
			goto requested
		case <-requestTicker.C:
			if !silent {
				fmt.Printf("\b\b%v ", spinnerSymbols[i])
				i = (i + 1) % len(spinnerSymbols)
			}
		case <-requestTimeout.C:
			log.Fatalf("\r%v\rFetch request timed out\n", strings.Repeat(" ", 60))
		}
	}

requested:

	select {
	case res := <-resourceStarted:
		done := make(chan struct{}, 1)
		res.SetCallback(func(r *rns.Resource) {
			select {
			case done <- struct{}{}:
			default:
			}
		})
		res.SetProgressCallback(func(r *rns.Resource) {
			// Progress callback for tracking transfer progress
		})

		if !silent {
			fmt.Printf("Downloading %v...  ", fileName)
		}
		start := time.Now()
		i = 0
		if err := waitForDownloadCompletion(done, 60*time.Second, func() {
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
		}); err != nil {
			log.Fatalf("\n%v", err)
		}

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
	case <-time.After(10 * time.Second):
		log.Fatalf("Timed out waiting for resource transfer to start")
	}

	link.Teardown()
	sleepAfterFetchCompletion(time.Sleep)
}
