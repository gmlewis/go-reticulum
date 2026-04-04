// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func runDeviceSigning(out io.Writer, port string) (err error) {
	return newRuntime().runDeviceSigning(out, port)
}

func (rt cliRuntime) runDeviceSigning(out io.Writer, port string) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	eepromState, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		return err
	}
	if !eepromState.provisioned {
		return errors.New("This device has not been provisioned yet, cannot create device signature")
	}

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		return err
	}
	if len(snapshot.deviceHash) == 0 {
		if _, err := fmt.Fprintln(out, "No device hash present, skipping device signing"); err != nil {
			return err
		}
		return nil
	}

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}
	deviceSigner, err := rns.FromFile(filepath.Join(configDir, "firmware", "device.key"))
	if err != nil {
		if _, writeErr := fmt.Fprintln(out, "Could not load device signing key (did you run \"gornodeconf --key\"?)"); writeErr != nil {
			return writeErr
		}
		return exitCodeError{code: 78, err: fmt.Errorf("No device signer loaded, cannot sign device: %w", err)}
	}

	if deviceSigner == nil {
		if _, err := fmt.Fprintln(out, "No device signer loaded, cannot sign device"); err != nil {
			return err
		}
		return exitCodeError{code: 78, err: errors.New("No device signer loaded, cannot sign device")}
	}

	signature, err := deviceSigner.Sign(snapshot.deviceHash)
	if err != nil {
		return err
	}

	state := &signatureSetterState{name: "rnode", signature: signature, writer: serial}
	if err := state.storeSignature(); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Device signed"); err != nil {
		return err
	}
	return nil
}
