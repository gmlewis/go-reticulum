// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bootstrapSignerStub struct {
	message   []byte
	signature []byte
}

func (s *bootstrapSignerStub) Sign(message []byte) ([]byte, error) {
	s.message = append([]byte(nil), message...)
	return append([]byte(nil), s.signature...), nil
}

func TestRunEEPROMBootstrapBootstrapsDeviceAndBacksItUp(t *testing.T) {
	home := tempTrustKeyHome(t)
	t.Setenv("HOME", home)

	serial := &scriptedSerial{reads: append(validRnodeEEPROMFrame(), []byte{kissFend, rnodeKISSCommandPlatform, romPlatformAVR, kissFend}...)}
	signer := &bootstrapSignerStub{signature: []byte{0x10, 0x20, 0x30}}
	rt := cliRuntime{
		openSerial:          func(settings serialSettings) (serialPort, error) { return serial, nil },
		now:                 func() time.Time { return time.Unix(0x05060708, 0) },
		loadBootstrapSigner: func(string) (bootstrapChecksumSigner, error) { return signer, nil },
	}

	var out bytes.Buffer
	if err := rt.runEEPROMBootstrap(&out, "ttyUSB0", options{autoinstall: true, product: "03", model: "a4", hwrev: 5}); err != nil {
		t.Fatalf("runEEPROMBootstrap returned error: %v\n%v", err, out.String())
	}
	if !strings.Contains(out.String(), "Bootstrapping device EEPROM...") {
		t.Fatalf("missing bootstrap start message: %q", out.String())
	}
	if !strings.Contains(out.String(), "EEPROM Bootstrapping successful!") {
		t.Fatalf("missing bootstrap success message: %q", out.String())
	}
	if !strings.Contains(out.String(), "Saved device identity") {
		t.Fatalf("missing device identity message: %q", out.String())
	}
	if got, want := signer.message, checksumInfoHash(0x03, 0xa4, 0x05, 1, uint32(time.Unix(0x05060708, 0).Unix())); !bytes.Equal(got, want) {
		t.Fatalf("checksum passed to signer mismatch:\n got: %x\nwant: %x", got, want)
	}
	backupPath := filepath.Join(home, ".config", "rnodeconf", "firmware", "device_db", "00000001")
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read device backup: %v", err)
	}
	if !bytes.Equal(backup, bootstrapEEPROMImage(0x03, 0xa4, 0x05, 1, uint32(time.Unix(0x05060708, 0).Unix()), []byte{0x10, 0x20, 0x30})) {
		t.Fatalf("backup content mismatch")
	}
}

func TestRunEEPROMBootstrapSkipsProvisionedDeviceWithoutAutoinstall(t *testing.T) {
	home := tempTrustKeyHome(t)
	t.Setenv("HOME", home)

	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) { return serial, nil }}
	var out bytes.Buffer
	if err := rt.runEEPROMBootstrap(&out, "ttyUSB0", options{product: "03", model: "a4", hwrev: 5}); err != nil {
		t.Fatalf("runEEPROMBootstrap returned error: %v", err)
	}
	if !strings.Contains(out.String(), "valid EEPROM was already present") {
		t.Fatalf("missing skip message: %q", out.String())
	}
	if len(serial.writes) != 1 {
		t.Fatalf("expected only EEPROM read write, got %v writes", len(serial.writes))
	}
}

func TestRunEEPROMBootstrapReturnsSignerError(t *testing.T) {
	home := tempTrustKeyHome(t)
	t.Setenv("HOME", home)

	serial := &scriptedSerial{reads: append(validRnodeEEPROMFrame(), []byte{kissFend, rnodeKISSCommandPlatform, romPlatformAVR, kissFend}...)}
	wantErr := fmt.Errorf("boom")
	rt := cliRuntime{
		openSerial:          func(settings serialSettings) (serialPort, error) { return serial, nil },
		now:                 func() time.Time { return time.Unix(0x05060708, 0) },
		loadBootstrapSigner: func(string) (bootstrapChecksumSigner, error) { return nil, wantErr },
	}
	var out bytes.Buffer
	if err := rt.runEEPROMBootstrap(&out, "ttyUSB0", options{autoinstall: true, product: "03", model: "a4", hwrev: 5}); err == nil {
		t.Fatal("expected signer error")
	}
	if len(serial.writes) < 3 {
		t.Fatalf("expected EEPROM read, platform read, and wipe writes before signer failure, got %v writes", len(serial.writes))
	}
}
