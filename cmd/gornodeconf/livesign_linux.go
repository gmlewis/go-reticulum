// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func runDeviceSigning(out io.Writer, port string) error {
	serial, err := rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer serial.Close()

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		return err
	}
	if len(snapshot.deviceHash) == 0 {
		fmt.Fprintln(out, "No device hash present, skipping device signing")
		return nil
	}

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}
	deviceSigner, err := rns.FromFile(filepath.Join(configDir, "firmware", "device.key"))
	if err != nil {
		fmt.Fprintln(out, "Could not load device signing key")
		return err
	}

	signature, err := deviceSigner.Sign(snapshot.deviceHash)
	if err != nil {
		return err
	}

	state := &signatureSetterState{name: "rnode", signature: signature, writer: serial}
	if err := state.storeSignature(); err != nil {
		return err
	}

	fmt.Fprintln(out, "Device signed")
	return nil
}
