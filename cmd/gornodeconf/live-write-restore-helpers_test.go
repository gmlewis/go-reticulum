// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

type liveWriteAction string

const (
	liveWriteActionFirmwareHashSet liveWriteAction = "firmware-hash"
	liveWriteActionDeviceSigning   liveWriteAction = "sign"
	liveWriteActionEEPROMBootstrap liveWriteAction = "rom"
	liveWriteActionEEPROMWipe      liveWriteAction = "eeprom-wipe"
)

type liveWriteGate string

const (
	liveWriteGateWrites      liveWriteGate = "writes"
	liveWriteGateDestructive liveWriteGate = "destructive"
)

type liveHardwareRestoreStrategy string

const (
	liveHardwareRestoreNone            liveHardwareRestoreStrategy = "none"
	liveHardwareRestoreFirmwareHash    liveHardwareRestoreStrategy = "firmware-hash"
	liveHardwareRestoreDeviceSignature liveHardwareRestoreStrategy = "device-signature"
	liveHardwareRestoreEEPROMImage     liveHardwareRestoreStrategy = "eeprom-image"
)

type liveHardwareBaseline struct {
	eeprom *eepromDownloaderState
	hashes *rnodeHashSnapshot
}

type liveTimeSleeper struct{}

func (liveTimeSleeper) Sleep(duration time.Duration) {
	time.Sleep(duration)
}

type liveHardwareRestorePlan struct {
	action          liveWriteAction
	safety          liveSerialSafety
	captureEEPROM   bool
	captureHashes   bool
	restoreStrategy liveHardwareRestoreStrategy
	reason          string
}

func planLiveHardwareRestore(action liveWriteAction, baseline liveHardwareBaseline) liveHardwareRestorePlan {
	plan := liveHardwareRestorePlan{
		action:          action,
		safety:          liveSerialSafetyDestructive,
		captureEEPROM:   true,
		captureHashes:   true,
		restoreStrategy: liveHardwareRestoreNone,
	}

	switch action {
	case liveWriteActionFirmwareHashSet:
		if baseline.eeprom == nil || !baseline.eeprom.provisioned {
			plan.reason = "captured baseline EEPROM must be provisioned before firmware-hash writes can be safely restored"
			return plan
		}
		if baseline.hashes == nil || len(baseline.hashes.firmwareHashTarget) != 32 {
			plan.reason = "captured baseline target firmware hash is required to restore firmware-hash writes"
			return plan
		}
		plan.safety = liveSerialSafetyReversibleWrite
		plan.restoreStrategy = liveHardwareRestoreFirmwareHash
		return plan

	case liveWriteActionDeviceSigning:
		if baseline.eeprom == nil || !baseline.eeprom.provisioned {
			plan.reason = "captured baseline EEPROM must be provisioned before device signing can be safely restored"
			return plan
		}
		if len(baseline.eeprom.signature) == 0 {
			plan.reason = "captured baseline device signature is required to restore device signing"
			return plan
		}
		if baseline.hashes == nil || len(baseline.hashes.deviceHash) != 32 {
			plan.reason = "captured baseline device hash is required to validate and restore device signing"
			return plan
		}
		plan.safety = liveSerialSafetyReversibleWrite
		plan.restoreStrategy = liveHardwareRestoreDeviceSignature
		return plan

	case liveWriteActionEEPROMBootstrap, liveWriteActionEEPROMWipe:
		if baseline.eeprom == nil || !baseline.eeprom.provisioned || len(baseline.eeprom.eeprom) == 0 {
			plan.reason = fmt.Sprintf("captured baseline EEPROM image is required to restore %v", action)
			return plan
		}
		plan.safety = liveSerialSafetyReversibleWrite
		plan.restoreStrategy = liveHardwareRestoreEEPROMImage
		return plan

	default:
		plan.reason = fmt.Sprintf("unsupported live write action %q", action)
		return plan
	}
}

func liveWriteGateRequirements(plan liveHardwareRestorePlan) liveWriteGate {
	if plan.safety == liveSerialSafetyDestructive {
		return liveWriteGateDestructive
	}
	return liveWriteGateWrites
}

func skipUnlessLiveWritePlanAllowed(t *testing.T, plan liveHardwareRestorePlan) {
	t.Helper()

	switch liveWriteGateRequirements(plan) {
	case liveWriteGateDestructive:
		skipUnlessLiveDestructiveAllowed(t)
	default:
		skipUnlessLiveWriteAllowed(t)
	}
}

func captureLiveHardwareBaseline(port string, serial serialPort, plan liveHardwareRestorePlan) (liveHardwareBaseline, error) {
	var baseline liveHardwareBaseline
	if plan.captureEEPROM {
		state, err := captureRnodeEEPROM(port, serial, 5*time.Second)
		if err != nil {
			return liveHardwareBaseline{}, err
		}
		baseline.eeprom = state
	}
	if plan.captureHashes {
		snapshot, err := captureRnodeHashes(serial, 5*time.Second)
		if err != nil {
			return liveHardwareBaseline{}, err
		}
		baseline.hashes = &snapshot
	}
	return baseline, nil
}

func prepareLiveWriteTest(t *testing.T, action liveWriteAction) (string, liveHardwareBaseline, liveHardwareRestorePlan) {
	t.Helper()

	port := requireLiveHardwarePort(t, defaultLiveWriteSafety(action))
	switch defaultLiveWriteSafety(action) {
	case liveSerialSafetyDestructive:
		skipUnlessLiveDestructiveAllowed(t)
	default:
		skipUnlessLiveWriteAllowed(t)
	}

	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Skipf("live RNode serial unavailable before test start: %v", err)
	}
	defer func() {
		_ = serial.Close()
	}()

	baseline, err := captureLiveHardwareBaseline(port, serial, liveHardwareRestorePlan{
		captureEEPROM: true,
		captureHashes: true,
	})
	if err != nil {
		t.Fatalf("captureLiveHardwareBaseline() error: %v", err)
	}
	plan := planLiveHardwareRestore(action, baseline)
	skipUnlessLiveWritePlanAllowed(t, plan)
	logLiveHardwareTest(t, port, plan.safety)
	return port, baseline, plan
}

func restoreLiveHardwareBaseline(t *testing.T, port string, plan liveHardwareRestorePlan, baseline liveHardwareBaseline) {
	t.Helper()

	switch plan.restoreStrategy {
	case liveHardwareRestoreNone:
		return
	case liveHardwareRestoreFirmwareHash:
		if baseline.hashes == nil {
			t.Fatal("missing baseline hashes for firmware-hash restore")
		}
		restoreFirmwareHash(t, port, baseline.hashes.firmwareHashTarget)
	case liveHardwareRestoreDeviceSignature:
		if baseline.eeprom == nil {
			t.Fatal("missing baseline EEPROM for signature restore")
		}
		restoreDeviceSignature(t, port, baseline.eeprom.signature)
	case liveHardwareRestoreEEPROMImage:
		if baseline.eeprom == nil {
			t.Fatal("missing baseline EEPROM for EEPROM restore")
		}
		restoreEEPROMImage(t, port, baseline.eeprom.eeprom)
	default:
		t.Fatalf("unsupported restore strategy %q", plan.restoreStrategy)
	}
}

func defaultLiveWriteSafety(action liveWriteAction) liveSerialSafety {
	switch action {
	case liveWriteActionEEPROMBootstrap, liveWriteActionEEPROMWipe:
		return liveSerialSafetyDestructive
	default:
		return liveSerialSafetyReversibleWrite
	}
}

func restoreFirmwareHash(t *testing.T, port string, hash []byte) {
	t.Helper()

	if len(hash) != 32 {
		t.Fatalf("restore firmware hash requires 32-byte target hash, got %d", len(hash))
	}
	out, err := runGornodeconfWithEnv(nil, "--firmware-hash", hex.EncodeToString(hash), port)
	if err != nil {
		t.Fatalf("restore firmware hash failed: %v\n%v", err, out)
	}
}

func restoreDeviceSignature(t *testing.T, port string, signature []byte) {
	t.Helper()

	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Fatalf("preflightRnodeSerial(%q): %v", port, err)
	}
	defer func() {
		_ = serial.Close()
	}()
	if err := storeSignature(serial, signature); err != nil {
		t.Fatalf("restore device signature failed: %v", err)
	}
}

func restoreEEPROMImage(t *testing.T, port string, image []byte) {
	t.Helper()

	if len(image) == 0 {
		t.Fatal("restore EEPROM image requires captured bytes")
	}
	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Fatalf("preflightRnodeSerial(%q): %v", port, err)
	}
	defer func() {
		_ = serial.Close()
	}()
	for addr, value := range image {
		if err := writeEEPROMByte(serial, byte(addr), value); err != nil {
			t.Fatalf("restore EEPROM byte %d failed: %v", addr, err)
		}
	}
}

func captureLiveEEPROMState(t *testing.T, port string) *eepromDownloaderState {
	t.Helper()

	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Fatalf("preflightRnodeSerial(%q): %v", port, err)
	}
	defer func() {
		_ = serial.Close()
	}()
	state, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		t.Fatalf("captureRnodeEEPROM(%q): %v", port, err)
	}
	return state
}

func requireSignatureBytes(t *testing.T, label string, signature []byte) {
	t.Helper()

	if len(signature) == 0 {
		t.Fatalf("missing %v signature bytes", label)
	}
}

func requireSignatureEquals(t *testing.T, label string, got, want []byte) {
	t.Helper()

	if !bytes.Equal(got, want) {
		t.Fatalf("%v signature mismatch:\n got: %x\nwant: %x", label, got, want)
	}
}

func captureRawLiveEEPROMBytes(t *testing.T, port string) []byte {
	t.Helper()

	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Fatalf("preflightRnodeSerial(%q): %v", port, err)
	}
	defer func() {
		_ = serial.Close()
	}()
	state := &eepromDownloaderState{
		name:    "rnode",
		writer:  serial,
		sleeper: liveTimeSleeper{},
	}
	if err := state.downloadEEPROM(); err != nil {
		t.Fatalf("downloadEEPROM(%q): %v", port, err)
	}
	return append([]byte(nil), state.eeprom...)
}
