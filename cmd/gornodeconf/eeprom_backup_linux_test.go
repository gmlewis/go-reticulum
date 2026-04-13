// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestWriteEEPROMBackupWritesTimestampedFile(t *testing.T) {
	t.Parallel()

	dir := tempEEPROMBackupHome(t)
	timestamp := time.Date(2026, time.April, 4, 13, 14, 15, 0, time.UTC)
	path, err := writeEEPROMBackup(dir, timestamp, []byte{0x01, 0x02, 0x03})
	if err != nil {
		t.Fatalf("writeEEPROMBackup returned error: %v", err)
	}
	want := filepath.Join(dir, "eeprom", "2026-04-04_13-14-15.eeprom")
	if path != want {
		t.Fatalf("unexpected backup path: got %q want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if !bytes.Equal(data, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("backup contents mismatch: %x", data)
	}
}

func TestRunEEPROMBackupWritesExpectedMessage(t *testing.T) {
	home := tempEEPROMBackupHome(t)
	t.Setenv("HOME", home)
	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	rt := cliRuntime{
		openSerial: func(settings serialSettings) (serialPort, error) {
			return serial, nil
		},
		now: func() time.Time {
			return time.Date(2026, time.April, 4, 13, 14, 15, 0, time.UTC)
		},
	}

	var out bytes.Buffer
	if err := rt.runEEPROMBackup(&out, "ttyUSB0"); err != nil {
		t.Fatalf("runEEPROMBackup returned error: %v", err)
	}
	if !strings.Contains(out.String(), "EEPROM backup written to:") {
		t.Fatalf("missing backup message: %q", out.String())
	}
	want := filepath.Join(home, ".config", "rnodeconf", "eeprom", "2026-04-04_13-14-15.eeprom")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected backup file %v: %v", want, err)
	}
}

func tempEEPROMBackupHome(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-eeprom-backup-*")
	t.Cleanup(cleanup)
	return dir
}
