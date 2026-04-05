// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornodeconf is a command-line utility for configuring and managing RNode
// devices. This initial Go port focuses on the command-line surface and keeps
// the serial-port contract compatible with the Python source of truth.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gmlewis/go-reticulum/rns"
)

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		if exitErr, ok := err.(exitCodeError); ok {
			fmt.Fprintln(os.Stderr, exitErr.Error())
			os.Exit(exitErr.code)
		}
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return newRuntime().run(args)
}

func (rt cliRuntime) run(args []string) error {
	if hasHelp(args) {
		printHelp()
		return nil
	}

	opts, port, err := parseArgs(args)
	if err != nil {
		printHelp()
		return err
	}
	rt.debug = opts.debug

	if opts.version {
		fmt.Printf("gornodeconf %v\n", rns.VERSION)
		return nil
	}

	if opts.clearCache {
		fmt.Println("Clearing local firmware cache...")
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		updateDir := filepath.Join(home, ".config", "rnodeconf", "update")
		if _, err := os.Stat(updateDir); err != nil {
			return err
		}
		if err := os.RemoveAll(updateDir); err != nil {
			return err
		}
		fmt.Println("Done")
		return nil
	}

	if opts.fwVersion != "" {
		if _, err := strconv.ParseFloat(opts.fwVersion, 64); err != nil {
			fmt.Printf("Selected version %q does not appear to be a number.\n", opts.fwVersion)
			return nil
		}
	}

	if opts.trustKey != "" {
		return handleTrustKey(opts.trustKey)
	}

	if opts.public {
		return handlePublicKeys()
	}

	if opts.key {
		return handleGenerateKeys(opts.autoinstall)
	}

	if port, err = rt.resolveLivePort(port, opts); err != nil {
		return err
	}

	if opts.extract {
		return rt.runFirmwareExtract(os.Stdout, port, opts)
	}

	if opts.eepromBackup {
		return rt.runEEPROMBackup(os.Stdout, port)
	}

	if opts.eepromDump {
		return rt.runEEPROMDump(os.Stdout, port)
	}

	if opts.eepromWipe {
		return rt.runEEPROMWipe(os.Stdout, port)
	}

	if opts.update {
		return rt.runFirmwareUpdate(os.Stdout, port, opts)
	}

	if opts.getTargetFirmwareHash || opts.getFirmwareHash {
		return rt.runFirmwareHashReadbacks(os.Stdout, port, opts)
	}

	if opts.firmwareHash != "" {
		return rt.runFirmwareHashSet(os.Stdout, port, opts.firmwareHash)
	}

	if opts.sign {
		// Keep the legacy Python help text for parity, but execute the live
		// device-signing workflow after the provisioning and hash checks.
		return rt.runDeviceSigning(os.Stdout, port)
	}

	if port == "" {
		printHelp()
		return nil
	}

	_ = port
	_ = opts
	fmt.Println("gornodeconf utility started (limited functionality in this version)")
	return nil
}

type exitCodeError struct {
	code int
	err  error
}

func (e exitCodeError) Error() string {
	return e.err.Error()
}

func (e exitCodeError) Unwrap() error {
	return e.err
}
