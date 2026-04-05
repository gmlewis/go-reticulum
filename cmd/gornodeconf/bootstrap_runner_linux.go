// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"io"
	"time"
)

func runEEPROMBootstrap(out io.Writer, port string, opts options) error {
	return newRuntime().runEEPROMBootstrap(out, port, opts)
}

func (rt cliRuntime) runEEPROMBootstrap(out io.Writer, port string, opts options) (err error) {
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
	if eepromState.signatureValid || (eepromState.provisioned && !opts.autoinstall) {
		if _, err := fmt.Fprintln(out, "EEPROM bootstrap was requested, but a valid EEPROM was already present."); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "No changes are being made."); err != nil {
			return err
		}
		return nil
	}

	if opts.autoinstall && eepromState.provisioned {
		platform, err := readRnodePlatform(port, serial, 5*time.Second)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "Clearing old EEPROM, this will take about 15 seconds..."); err != nil {
			return err
		}
		state := &modeSwitchState{platform: platform, writer: serial, sleeper: rt}
		if err := state.wipeEEPROM(); err != nil {
			return err
		}
	}

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}

	identity, err := resolveBootstrapIdentity(opts)
	if err != nil {
		return err
	}

	serialno, err := nextBootstrapSerialNumber(configDir)
	if err != nil {
		return err
	}

	timestamp := uint32(rt.now().Unix())
	checksum := checksumInfoHash(identity.product, identity.model, identity.hwRev, serialno, timestamp)

	loader := rt.loadBootstrapSigner
	if loader == nil {
		loader = loadBootstrapSigner
	}
	signer, err := loader(configDir)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Bootstrapping device EEPROM..."); err != nil {
		return err
	}
	signature, err := signer.Sign(checksum)
	if err != nil {
		return err
	}
	image := bootstrapEEPROMImage(identity.product, identity.model, identity.hwRev, serialno, timestamp, signature)
	if err := bootstrapEEPROM(serial, identity.product, identity.model, identity.hwRev, serialno, timestamp, signature); err != nil {
		return err
	}
	if _, err := writeDeviceIdentityBackup(configDir, serialno, image); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "EEPROM Bootstrapping successful!"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Saved device identity"); err != nil {
		return err
	}
	return nil
}
