// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gorncp is a Reticulum-based file transfer utility.
//
// It provides three main modes of operation:
//   - Listen: Waits for incoming file transfer requests from other nodes.
//   - Send: Transmits a file to a remote node that is in listen mode.
//   - Fetch: Requests and retrieves a file from a remote node.
//
// Usage:
//
//	Listen mode:
//	  gorncp -l [-i <identity_file>] [-v] [-q] [--config <config_dir>]
//
//	Send mode:
//	  gorncp <destination_hash> <file_path> [-i <identity_file>] [-v] [-q] [--config <config_dir>]
//
//	Fetch mode:
//	  gorncp -f <destination_hash> <file_name> [-i <identity_file>] [-v] [-q] [--config <config_dir>]
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-i string
//	      path to identity to use
//	-l    listen for incoming transfer requests
//	-f    fetch file from remote listener instead of sending
//	-C    disable automatic compression
//	-v    increase verbosity
//	-q    decrease verbosity
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// AppName is the name of the application used for identity generation.
const AppName = "rncp"

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	identityPath := flag.String("i", "", "path to identity to use")
	verbose := flag.Bool("v", false, "increase verbosity")
	quiet := flag.Bool("q", false, "decrease verbosity")
	listenMode := flag.Bool("l", false, "listen for incoming transfer requests")
	fetchMode := flag.Bool("f", false, "fetch file from remote listener instead of sending")
	noCompressShort := flag.Bool("C", false, "disable automatic compression")
	noCompressLong := flag.Bool("no-compress", false, "disable automatic compression")
	// Add other flags as needed
	log.SetFlags(0)
	flag.Parse()
	noCompress := *noCompressShort || *noCompressLong

	if *verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if *quiet {
		rns.SetLogLevel(rns.LogWarning)
	}

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, *configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	defer func() { _ = ret.Close() }()

	if *listenMode {
		doListen(ts, *identityPath, noCompress)
	} else if *fetchMode {
		if flag.NArg() < 2 {
			flag.Usage()
			log.Fatal("destination and file must be specified")
		}
		destHashHex := flag.Arg(0)
		fileName := flag.Arg(1)
		doFetch(*identityPath, destHashHex, fileName, noCompress)
	} else {
		if flag.NArg() < 2 {
			flag.Usage()
			log.Fatal("destination and file must be specified")
		}
		destHashHex := flag.Arg(0)
		filePath := flag.Arg(1)
		doSend(*identityPath, destHashHex, filePath, noCompress)
	}
}

func doListen(ts rns.Transport, idPath string, noCompress bool) {
	if idPath == "" {
		home, _ := os.UserHomeDir()
		idPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}

	var id *rns.Identity
	if _, err := os.Stat(idPath); err == nil {
		id, err = rns.FromFile(idPath)
		if err != nil {
			rns.Logf("Could not load identity for rncp. The identity file at \"%v\" may be corrupt or unreadable.", rns.LogError, false, idPath)
			os.Exit(2)
		}
	} else {
		rns.Log("No valid saved identity found, creating new...", rns.LogInfo, false)
		id, _ = rns.NewIdentity(true)
		if err := id.ToFile(idPath); err != nil {
			log.Fatalf("Could not persist identity %q: %v\n", idPath, err)
		}
	}

	dest, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, AppName, "receive")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	dest.RegisterRequestHandler("fetch_file", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
		fileName := string(data)
		rns.Logf("Fetch request for %v", rns.LogVerbose, false, fileName)
		return true
	}, rns.AllowAll, nil, !noCompress)

	dest.SetLinkEstablishedCallback(func(l *rns.Link) {
		rns.Log("Incoming link established", rns.LogVerbose, false)
		l.SetResourceCallback(func(adv *rns.ResourceAdvertisement) bool {
			rns.Logf("Incoming resource: %x", rns.LogVerbose, false, adv.H)
			return true
		})
		l.SetResourceConcludedCallback(func(res *rns.Resource) {
			rns.Logf("Resource concluded: %x", rns.LogInfo, false, res.Hash())
		})
	})

	fmt.Printf("Listening on : <%x>\n", dest.Hash)

	// Keep alive
	select {}
}

func doSend(idPath string, destHashHex string, filePath string, noCompress bool) {
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
	syms := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
	select {
	case <-established:
		fmt.Printf("\b\b \n")
	case <-time.After(10 * time.Second):
		log.Fatalf("\r%v\rLink establishment timed out\n", strings.Repeat(" ", 60))
	default:
		for {
			select {
			case <-established:
				fmt.Printf("\b\b \n")
				goto established
			case <-time.After(100 * time.Millisecond):
				fmt.Printf("\b\b%v ", syms[i])
				i = (i + 1) % len(syms)
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

	fmt.Printf("Sending %v (%v bytes)...\n", filepath.Base(filePath), len(data))
	if err := res.Advertise(); err != nil {
		log.Fatalf("Could not advertise resource: %v\n", err)
	}

	start := time.Now()
	i = 0
	for {
		select {
		case <-done:
			duration := time.Since(start)
			speed := float64(len(data)) / duration.Seconds()
			fmt.Printf("\rTransfer complete  100.0%% - %v of %v in %v - %vps\n",
				rns.PrettySize(float64(len(data)), "B"),
				rns.PrettySize(float64(len(data)), "B"),
				rns.PrettyTime(duration.Seconds(), false, true),
				rns.PrettySize(speed, "bps"))
			goto sent
		case <-time.After(100 * time.Millisecond):
			// Update progress
			prg := res.GetProgress()
			percent := prg * 100.0
			ps := rns.PrettySize(prg*float64(len(data)), "B")
			ts := rns.PrettySize(float64(len(data)), "B")
			duration := time.Since(start)
			speed := (prg * float64(len(data))) / duration.Seconds()
			fmt.Printf("\rTransferring file %v %.1f%% - %v of %v - %vps  ",
				syms[i], percent, ps, ts, rns.PrettySize(speed, "bps"))
			i = (i + 1) % len(syms)
		case <-time.After(60 * time.Second):
			log.Fatalf("\nFile transfer timed out")
		}
	}
sent:

	link.Teardown()
	time.Sleep(100 * time.Millisecond)
}

func doFetch(idPath string, destHashHex string, fileName string, noCompress bool) {
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
	syms := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
	for {
		select {
		case <-established:
			fmt.Printf("\b\b \n")
			goto established
		case <-time.After(100 * time.Millisecond):
			fmt.Printf("\b\b%v ", syms[i])
			i = (i + 1) % len(syms)
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

	fmt.Printf("Requesting %v from remote...  ", fileName)
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
			fmt.Printf("\b\b \n")
			goto requested
		case <-time.After(100 * time.Millisecond):
			fmt.Printf("\b\b%v ", syms[i])
			i = (i + 1) % len(syms)
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

		fmt.Printf("Downloading %v...  ", fileName)
		start := time.Now()
		i = 0
		for {
			select {
			case <-done:
				duration := time.Since(start)
				speed := float64(res.TotalSize()) / duration.Seconds()
				fmt.Printf("\rTransfer complete  100.0%% - %v of %v in %v - %vps\n",
					rns.PrettySize(float64(res.TotalSize()), "B"),
					rns.PrettySize(float64(res.TotalSize()), "B"),
					rns.PrettyTime(duration.Seconds(), false, true),
					rns.PrettySize(speed, "bps"))
				goto fetched
			case <-time.After(100 * time.Millisecond):
				prg := res.GetProgress()
				percent := prg * 100.0
				ps := rns.PrettySize(prg*float64(res.TotalSize()), "B")
				ts := rns.PrettySize(float64(res.TotalSize()), "B")
				duration := time.Since(start)
				speed := (prg * float64(res.TotalSize())) / duration.Seconds()
				fmt.Printf("\rTransferring file %v %.1f%% - %v of %v - %vps  ",
					syms[i], percent, ps, ts, rns.PrettySize(speed, "bps"))
				i = (i + 1) % len(syms)
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
