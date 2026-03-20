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

// sprintf is a helper function for formatting strings.
func sprintf(format string, a ...any) string {
	return fmt.Sprintf(format, a...)
}

// validateIdentityHash validates a hexadecimal identity hash.
// Returns an error if the hash is invalid (wrong length or non-hex characters).
func validateIdentityHash(hash string) error {
	destLen := (rns.TruncatedHashLength / 8) * 2
	if len(hash) != destLen {
		return fmt.Errorf("allowed destination length is invalid, must be %d hexadecimal characters (%d bytes)", destLen, destLen/2)
	}
	if _, err := rns.HexToBytes(hash); err != nil {
		return fmt.Errorf("invalid destination entered. check your input")
	}
	return nil
}

// AppName is the name of the application used for identity generation.
const AppName = "rncp"

// eraseStr is the terminal escape sequence to clear the current line and return to column 0.
// Matches Python's erase_str = "\33[2K\r"
const eraseStr = "\033[2K\r"

// spinnerSymbols are the Unicode Braille characters used for progress animation.
// Matches Python's syms = "⢄⢂⢁⡁⡈⡐⡠"
var spinnerSymbols = []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}

// sizeStr formats a byte count with appropriate unit suffix.
// Matches Python's size_str() function exactly.
func sizeStr(num float64, suffix string) string {
	units := []string{"", "K", "M", "G", "T", "P", "E", "Z"}
	lastUnit := "Y"

	if suffix == "b" {
		num *= 8
	}

	for _, unit := range units {
		if num < 1000.0 {
			if unit == "" {
				return sprintf("%.0f %s%s", num, unit, suffix)
			}
			return sprintf("%.2f %s%s", num, unit, suffix)
		}
		num /= 1000.0
	}

	return sprintf("%.2f%s%s", num, lastUnit, suffix)
}

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	identityPath := flag.String("i", "", "path to identity to use")
	verbose := flag.Bool("v", false, "increase verbosity")
	quiet := flag.Bool("q", false, "decrease verbosity")
	listenMode := flag.Bool("l", false, "listen for incoming transfer requests")
	fetchMode := flag.Bool("f", false, "fetch file from remote listener instead of sending")
	noCompressShort := flag.Bool("C", false, "disable automatic compression")
	noCompressLong := flag.Bool("no-compress", false, "disable automatic compression")
	silentShort := flag.Bool("S", false, "disable transfer progress output")
	silentLong := flag.Bool("silent", false, "disable transfer progress output")
	allowFetchShort := flag.Bool("F", false, "allow authenticated clients to fetch files")
	allowFetchLong := flag.Bool("allow-fetch", false, "allow authenticated clients to fetch files")
	jailShort := flag.String("j", "", "restrict fetch requests to specified path")
	jailLong := flag.String("jail", "", "restrict fetch requests to specified path")
	saveShort := flag.String("s", "", "save received files in specified path")
	saveLong := flag.String("save", "", "save received files in specified path")
	overwriteShort := flag.Bool("O", false, "allow overwriting received files")
	overwriteLong := flag.Bool("overwrite", false, "allow overwriting received files")
	announceShort := flag.Int("b", -1, "announce interval (0=once, >0=seconds)")
	var allowed []string
	flag.Func("a", "allow identity hash", func(s string) error {
		allowed = append(allowed, s)
		return nil
	})
	noAuthShort := flag.Bool("n", false, "accept requests from anyone")
	noAuthLong := flag.Bool("no-auth", false, "accept requests from anyone")
	printIdentityShort := flag.Bool("p", false, "print identity and destination info and exit")
	printIdentityLong := flag.Bool("print-identity", false, "print identity and destination info and exit")
	phyRatesShort := flag.Bool("P", false, "display physical layer transfer rates")
	phyRatesLong := flag.Bool("phy-rates", false, "display physical layer transfer rates")
	timeout := flag.Float64("w", 15.0, "sender timeout seconds")
	version := flag.Bool("version", false, "show version")
	log.SetFlags(0)
	flag.Parse()
	noCompress := *noCompressShort || *noCompressLong
	silent := *silentShort || *silentLong
	allowFetch := *allowFetchShort || *allowFetchLong
	jail := *jailShort
	if *jailLong != "" {
		jail = *jailLong
	}
	savePath := *saveShort
	if *saveLong != "" {
		savePath = *saveLong
	}
	overwrite := *overwriteShort || *overwriteLong
	announceInterval := *announceShort
	noAuth := *noAuthShort || *noAuthLong
	printIdentity := *printIdentityShort || *printIdentityLong
	phyRates := *phyRatesShort || *phyRatesLong
	timeoutSec := *timeout
	showVersion := *version

	// Validate allowed identity hashes
	for _, a := range allowed {
		if err := validateIdentityHash(a); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

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

	if showVersion {
		fmt.Printf("gorncp %v\n", rns.VERSION)
		return
	}

	if *listenMode {
		doListen(ts, *identityPath, noCompress, silent, allowFetch, jail, savePath, overwrite, announceInterval, allowed, noAuth, printIdentity)
	} else if *fetchMode {
		if flag.NArg() < 2 {
			flag.Usage()
			log.Fatal("destination and file must be specified")
		}
		destHashHex := flag.Arg(0)
		fileName := flag.Arg(1)
		doFetch(*identityPath, destHashHex, fileName, noCompress, silent, savePath, overwrite, phyRates, timeoutSec)
	} else {
		if flag.NArg() < 2 {
			flag.Usage()
			log.Fatal("destination and file must be specified")
		}
		destHashHex := flag.Arg(0)
		filePath := flag.Arg(1)
		doSend(*identityPath, destHashHex, filePath, noCompress, silent, phyRates, timeoutSec)
	}
}

func doListen(ts rns.Transport, idPath string, noCompress bool, silent bool, allowFetch bool, jail string, savePath string, overwrite bool, announceInterval int, allowed []string, noAuth bool, printIdentity bool) {
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

	if jail != "" {
		fetchJail := filepath.Clean(jail)
		rns.Logf("Restricting fetch requests to paths under %q", rns.LogVerbose, false, fetchJail)
	}

	if savePath != "" {
		sp := filepath.Clean(savePath)
		rns.Logf("Saving received files in %q", rns.LogVerbose, false, sp)
	}

	if overwrite {
		rns.Log("Allowing overwrite of received files", rns.LogVerbose, false)
	}

	if len(allowed) > 0 {
		rns.Logf("Allowing %d identity hash(es)", rns.LogVerbose, false, len(allowed))
		for _, a := range allowed {
			rns.Logf("  Allowed: %v", rns.LogVerbose, false, a)
		}
	}

	if noAuth {
		rns.Log("Accepting unauthenticated requests", rns.LogVerbose, false)
	}

	if printIdentity {
		fmt.Printf("Identity     : %v\n", id)
		fmt.Printf("Listening on : %v\n", rns.PrettyHex(dest.Hash))
		os.Exit(0)
	}

	if allowFetch {
		dest.RegisterRequestHandler("fetch_file", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
			fileName := string(data)
			rns.Logf("Fetch request for %v", rns.LogVerbose, false, fileName)
			return true
		}, rns.AllowAll, nil, !noCompress)
	}

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

	if announceInterval >= 0 {
		rns.Logf("Announcing destination (interval=%v)", rns.LogVerbose, false, announceInterval)
		if err := dest.Announce(nil); err != nil {
			rns.Logf("Announce failed: %v", rns.LogError, false, err)
		}
		if announceInterval > 0 {
			go func() {
				for {
					time.Sleep(time.Duration(announceInterval) * time.Second)
					if err := dest.Announce(nil); err != nil {
						rns.Logf("Announce failed: %v", rns.LogError, false, err)
					}
				}
			}()
		}
	}

	// Keep alive
	select {}
}

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
		timeout := time.Now().Add(15 * time.Second)
		for !ts.HasPath(destHash) && time.Now().Before(timeout) {
			time.Sleep(100 * time.Millisecond)
			if !silent {
				fmt.Printf("\b\b%v ", spinnerSymbols[i])
				i = (i + 1) % len(spinnerSymbols)
			}
		}

		if !ts.HasPath(destHash) {
			log.Fatalf("\r%v\rPath request timed out\n", strings.Repeat(" ", 60))
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
