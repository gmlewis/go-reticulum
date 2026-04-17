// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && (linux || darwin)

package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestLiveFirmwareHashSetRoundTrip(t *testing.T) {
	port, baseline, plan := prepareLiveWriteTest(t, liveWriteActionFirmwareHashSet)
	if baseline.hashes == nil {
		t.Fatal("missing baseline hashes")
	}
	t.Cleanup(func() {
		restoreLiveHardwareBaseline(t, port, plan, baseline)
	})

	original := append([]byte(nil), baseline.hashes.firmwareHashTarget...)
	updated := invertHash(original)
	if strings.EqualFold(hex.EncodeToString(updated), hex.EncodeToString(original)) {
		t.Fatal("updated firmware hash must differ from baseline")
	}

	out, err := runGornodeconf("--firmware-hash", hex.EncodeToString(updated), port)
	if err != nil {
		t.Fatalf("runGornodeconf firmware hash set failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Firmware hash set") {
		t.Fatalf("missing firmware hash set output:\n%v", out)
	}

	readback, err := runGornodeconf("--get-target-firmware-hash", port)
	if err != nil {
		t.Fatalf("runGornodeconf target hash readback failed: %v\n%v", err, readback)
	}
	wantLine := "The target firmware hash is: " + hex.EncodeToString(updated)
	if !strings.Contains(readback, wantLine) {
		t.Fatalf("missing updated target hash output %q:\n%v", wantLine, readback)
	}
}

func TestLiveDeviceSigning(t *testing.T) {
	port, baseline, plan := prepareLiveWriteTest(t, liveWriteActionDeviceSigning)
	if baseline.eeprom == nil {
		t.Fatal("missing baseline EEPROM")
	}
	if baseline.hashes == nil {
		t.Fatal("missing baseline hashes")
	}
	requireSignatureBytes(t, "baseline", baseline.eeprom.signature)

	restored := false
	t.Cleanup(func() {
		if restored {
			return
		}
		restoreLiveHardwareBaseline(t, port, plan, baseline)
	})

	home := tempTrustKeyHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "-k")
	if err != nil {
		t.Fatalf("runGornodeconf --key failed: %v\n%v", err, out)
	}

	deviceSigner, err := rns.FromFile(filepath.Join(home, ".config", "rnodeconf", "firmware", "device.key"), nil)
	if err != nil {
		t.Fatalf("load generated device signing key: %v", err)
	}
	expectedSignature, err := deviceSigner.Sign(baseline.hashes.deviceHash)
	if err != nil {
		t.Fatalf("sign baseline device hash: %v", err)
	}

	signOut, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--sign", port)
	if err != nil {
		t.Fatalf("runGornodeconf --sign failed: %v\n%v", err, signOut)
	}
	if !strings.Contains(signOut, "Device signed") {
		t.Fatalf("missing device signed output:\n%v", signOut)
	}

	signedState := captureLiveEEPROMState(t, port)
	if !signedState.provisioned {
		t.Fatal("device became unprovisioned after signing")
	}
	requireSignatureBytes(t, "signed", signedState.signature)
	requireSignatureEquals(t, "signed", signedState.signature, expectedSignature)

	restoreLiveHardwareBaseline(t, port, plan, baseline)
	restored = true

	restoredState := captureLiveEEPROMState(t, port)
	requireSignatureBytes(t, "restored", restoredState.signature)
	requireSignatureEquals(t, "restored", restoredState.signature, baseline.eeprom.signature)
}

func TestLiveEEPROMBootstrapRoundTrip(t *testing.T) {
	port, baseline, plan := prepareLiveWriteTest(t, liveWriteActionEEPROMBootstrap)
	if baseline.eeprom == nil {
		t.Fatal("missing baseline EEPROM")
	}

	restored := false
	t.Cleanup(func() {
		if restored {
			return
		}
		restoreLiveHardwareBaseline(t, port, plan, baseline)
	})

	home := tempTrustKeyHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "-k")
	if err != nil {
		t.Fatalf("runGornodeconf --key failed: %v\n%v", err, out)
	}

	bootstrapOut, err := runGornodeconfWithEnv(
		map[string]string{"HOME": home},
		"--rom", "--autoinstall",
		"--product", hex.EncodeToString([]byte{baseline.eeprom.product}),
		"--model", hex.EncodeToString([]byte{baseline.eeprom.model}),
		"--hwrev", strconv.Itoa(int(baseline.eeprom.hwRev)),
		port,
	)
	if err != nil {
		t.Fatalf("runGornodeconf --rom failed: %v\n%v", err, bootstrapOut)
	}
	if !strings.Contains(bootstrapOut, "Bootstrapping device EEPROM...") {
		t.Fatalf("missing bootstrap start output:\n%v", bootstrapOut)
	}
	if !strings.Contains(bootstrapOut, "EEPROM Bootstrapping successful!") {
		t.Fatalf("missing bootstrap success output:\n%v", bootstrapOut)
	}
	if !strings.Contains(bootstrapOut, "Saved device identity") {
		t.Fatalf("missing bootstrap backup output:\n%v", bootstrapOut)
	}

	backupPath := filepath.Join(home, ".config", "rnodeconf", "firmware", "device_db", "00000001")
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read bootstrap backup: %v", err)
	}

	bootstrappedState := captureLiveEEPROMState(t, port)
	if !bootstrappedState.provisioned {
		t.Fatal("device became unprovisioned after bootstrap")
	}
	if len(bootstrappedState.eeprom) < len(backup) {
		t.Fatalf("bootstrapped EEPROM shorter than backup: got %d want at least %d", len(bootstrappedState.eeprom), len(backup))
	}
	if !bytes.Equal(bootstrappedState.eeprom[:len(backup)], backup) {
		t.Fatalf("bootstrapped EEPROM prefix does not match saved backup")
	}

	restoreLiveHardwareBaseline(t, port, plan, baseline)
	restored = true

	restoredState := captureLiveEEPROMState(t, port)
	if !bytes.Equal(restoredState.eeprom, baseline.eeprom.eeprom) {
		t.Fatal("restored EEPROM image mismatch")
	}
}

func TestLiveEEPROMWipeRoundTrip(t *testing.T) {
	port, baseline, plan := prepareLiveWriteTest(t, liveWriteActionEEPROMWipe)
	if baseline.eeprom == nil {
		t.Fatal("missing baseline EEPROM")
	}

	restored := false
	t.Cleanup(func() {
		if restored {
			return
		}
		restoreLiveHardwareBaseline(t, port, plan, baseline)
	})

	out, err := runGornodeconf("--eeprom-wipe", port)
	if err != nil {
		t.Fatalf("runGornodeconf --eeprom-wipe failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "WARNING: EEPROM is being wiped!") {
		t.Fatalf("missing wipe warning output:\n%v", out)
	}

	wipedEEPROM := captureRawLiveEEPROMBytes(t, port)
	if len(wipedEEPROM) <= 0x9b {
		t.Fatalf("wiped EEPROM shorter than expected: %d", len(wipedEEPROM))
	}
	if wipedEEPROM[0x9b] == 0x73 {
		t.Fatal("wiped EEPROM still reports provisioned marker")
	}

	restoreLiveHardwareBaseline(t, port, plan, baseline)
	restored = true

	restoredEEPROM := captureRawLiveEEPROMBytes(t, port)
	if !bytes.Equal(restoredEEPROM, baseline.eeprom.eeprom) {
		t.Fatal("restored EEPROM image mismatch")
	}
}

func invertHash(hash []byte) []byte {
	out := append([]byte(nil), hash...)
	for i := range out {
		out[i] ^= 0xff
	}
	return out
}
