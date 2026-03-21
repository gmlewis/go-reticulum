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

	"github.com/gmlewis/go-reticulum/rns"
)

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
				return fmt.Sprintf("%.0f %s%s", num, unit, suffix)
			}
			return fmt.Sprintf("%.2f %s%s", num, unit, suffix)
		}
		num /= 1000.0
	}

	return fmt.Sprintf("%.2f%s%s", num, lastUnit, suffix)
}

func printUsage() {
	fmt.Printf("usage: gorncp [-h] [--config path] [-v] [-q] [-S] [-l] [-C] [-F] [-f] [-j path]\n")
	fmt.Printf("          [-s path] [-O] [-b seconds] [-a allowed_hash] [-n] [-p]\n")
	fmt.Printf("          [-i identity] [-w seconds] [-P] [--version]\n")
	fmt.Printf("          [file] [destination]\n")
	fmt.Printf("\n")
	fmt.Printf("Reticulum File Transfer Utility\n")
	fmt.Printf("\n")
	fmt.Printf("positional arguments:\n")
	fmt.Printf("  file                  file to be transferred\n")
	fmt.Printf("  destination           hexadecimal hash of the receiver\n")
	fmt.Printf("\n")
	fmt.Printf("options:\n")
	fmt.Printf("  -h, --help            show this help message and exit\n")
	fmt.Printf("  --config path         path to alternative Reticulum config directory\n")
	fmt.Printf("  -v, --verbose         increase verbosity\n")
	fmt.Printf("  -q, --quiet           decrease verbosity\n")
	fmt.Printf("  -S, --silent          disable transfer progress output\n")
	fmt.Printf("  -l, --listen          listen for incoming transfer requests\n")
	fmt.Printf("  -C, --no-compress     disable automatic compression\n")
	fmt.Printf("  -F, --allow-fetch     allow authenticated clients to fetch files\n")
	fmt.Printf("  -f, --fetch           fetch file from remote listener instead of sending\n")
	fmt.Printf("  -j path, --jail path  restrict fetch requests to specified path\n")
	fmt.Printf("  -s path, --save path  save received files in specified path\n")
	fmt.Printf("  -O, --overwrite       Allow overwriting received files, instead of adding\n")
	fmt.Printf("                        postfix\n")
	fmt.Printf("  -b seconds            announce interval, 0 to only announce at startup\n")
	fmt.Printf("  -a allowed_hash       allow this identity (or add in\n")
	fmt.Printf("                        ~/.rncp/allowed_identities)\n")
	fmt.Printf("  -n, --no-auth         accept requests from anyone\n")
	fmt.Printf("  -p, --print-identity  print identity and destination info and exit\n")
	fmt.Printf("  -i identity           path to identity to use\n")
	fmt.Printf("  -w seconds            sender timeout before giving up\n")
	fmt.Printf("  -P, --phy-rates       display physical layer transfer rates\n")
	fmt.Printf("  --version             show program's version number and exit\n")
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

	flag.Usage = printUsage
	flag.Parse()

	help := flag.Bool("h", false, "show this help message and exit")
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

	if *help {
		printUsage()
		os.Exit(0)
	}

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
	defer func() {
		if err := ret.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	if showVersion {
		fmt.Printf("gorncp %v\n", rns.VERSION)
		return
	}

	if *listenMode {
		doListen(ts, *identityPath, noCompress, silent, allowFetch, jail, savePath, overwrite, announceInterval, allowed, noAuth, printIdentity)
		os.Exit(0)
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
