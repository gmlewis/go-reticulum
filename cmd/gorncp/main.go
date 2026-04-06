// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gorncp is a Reticulum-based file transfer utility.
//
// The Python source-of-truth for rncp relies on callback completion in a few
// places where the Go port intentionally uses bounded waits to avoid an
// indefinite CLI hang. Those safety timeouts are documented in the transfer
// helpers and are the primary behavioral difference from the Python utility.
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
	"fmt"
	"log"
	"os"
	"path/filepath"

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

// prepareIdentity loads an identity from the specified path, or creates a new one if it doesn't exist.
// Matches Python's prepare_identity behavior.
func prepareIdentity(identityPath string) *rns.Identity {
	if identityPath == "" {
		home, _ := os.UserHomeDir()
		identityPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}

	var id *rns.Identity
	if _, err := os.Stat(identityPath); err == nil {
		var err error
		id, err = rns.FromFile(identityPath)
		if err != nil {
			rns.Logf("Could not load identity for rncp. The identity file at \"%v\" may be corrupt or unreadable.", rns.LogError, false, identityPath)
			os.Exit(2)
		}
	}

	if id == nil {
		rns.Log("No valid saved identity found, creating new...", rns.LogInfo, false)
		// Create directory first (matches Python behavior)
		identityDir := filepath.Dir(identityPath)
		if err := os.MkdirAll(identityDir, 0o700); err != nil {
			log.Fatalf("Could not create identity directory: %v\n", err)
		}

		var err error
		id, err = rns.NewIdentity(true)
		if err != nil {
			log.Fatalf("Could not create new identity: %v\n", err)
		}
		if err := id.ToFile(identityPath); err != nil {
			log.Fatalf("Could not persist identity %q: %v\n", identityPath, err)
		}
	}
	return id
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

func main() {
	log.SetFlags(0)
	app, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == errHelp {
			return
		}
		log.Fatal(err)
	}

	if err := app.validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if app.verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if app.quiet {
		rns.SetLogLevel(rns.LogWarning)
	}

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

	if app.version {
		fmt.Printf("gorncp %v\n", rns.VERSION)
		return
	}

	if app.listenMode {
		doListen(ts, app.identityPath, app.noCompress, app.silent, app.allowFetch, app.jail, app.savePath, app.overwrite, app.announceInterval, app.allowed, app.noAuth, app.printIdentity)
		os.Exit(0)
	} else if app.fetchMode {
		if len(app.args) < 2 {
			app.usage(os.Stderr)
			log.Fatal("destination and file must be specified")
		}
		destHashHex := app.args[0]
		fileName := app.args[1]
		doFetch(ts, app.identityPath, destHashHex, fileName, app.noCompress, app.silent, app.savePath, app.overwrite, app.phyRates, app.timeoutSec)
	} else {
		if len(app.args) < 2 {
			app.usage(os.Stderr)
			log.Fatal("destination and file must be specified")
		}
		destHashHex := app.args[0]
		filePath := app.args[1]
		doSend(ts, app.identityPath, destHashHex, filePath, app.noCompress, app.silent, app.phyRates, app.timeoutSec)
	}
}
