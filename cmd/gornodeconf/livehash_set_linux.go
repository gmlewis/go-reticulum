// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

func runFirmwareHashSet(out io.Writer, port, hashHex string) error {
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil || len(hashBytes) != 32 {
		return errors.New("The provided value was not a valid SHA256 hash")
	}

	serial, err := rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer serial.Close()

	state := &firmwareHashSetterState{name: "rnode", hashBytes: hashBytes, writer: serial}
	if err := state.setFirmwareHash(); err != nil {
		return err
	}

	fmt.Fprintln(out, "Firmware hash set")
	return nil
}
