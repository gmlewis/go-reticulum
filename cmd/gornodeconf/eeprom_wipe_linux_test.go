// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin || linux

package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

type wipeSleepRecorder struct {
	calls []time.Duration
}

func (s *wipeSleepRecorder) Sleep(duration time.Duration) {
	s.calls = append(s.calls, duration)
}

func TestRunEEPROMWipeUsesPlatformQueryAndResetForAVR(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: []byte{kissFend, rnodeKISSCommandPlatform, romPlatformAVR, kissFend}}
	recorder := &wipeSleepRecorder{}
	rt := cliRuntime{
		openSerial: func(settings serialSettings) (serialPort, error) {
			return serial, nil
		},
		sleep: recorder.Sleep,
	}

	var out bytes.Buffer
	if err := rt.runEEPROMWipe(&out, "ttyUSB0"); err != nil {
		t.Fatalf("runEEPROMWipe returned error: %v", err)
	}
	if !strings.Contains(out.String(), "WARNING: EEPROM is being wiped!") {
		t.Fatalf("missing warning output: %q", out.String())
	}
	wantWrites := [][]byte{
		{kissFend, rnodeKISSCommandPlatform, 0x00, kissFend},
		{kissFend, 0x59, 0xf8, kissFend},
		{kissFend, 0x55, 0xf8, kissFend},
	}
	if !reflect.DeepEqual(serial.writes, wantWrites) {
		t.Fatalf("wipe command sequence mismatch:\n got: %#v\nwant: %#v", serial.writes, wantWrites)
	}
	wantSleeps := []time.Duration{13 * time.Second, 2 * time.Second}
	if !reflect.DeepEqual(recorder.calls, wantSleeps) {
		t.Fatalf("wipe sleep mismatch: got %v want %v", recorder.calls, wantSleeps)
	}
}

func TestRunEEPROMWipeSkipsResetForNRF52(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: []byte{kissFend, rnodeKISSCommandPlatform, romPlatformNRF52, kissFend}}
	recorder := &wipeSleepRecorder{}
	rt := cliRuntime{
		openSerial: func(settings serialSettings) (serialPort, error) {
			return serial, nil
		},
		sleep: recorder.Sleep,
	}

	var out bytes.Buffer
	if err := rt.runEEPROMWipe(&out, "ttyUSB0"); err != nil {
		t.Fatalf("runEEPROMWipe returned error: %v", err)
	}
	wantWrites := [][]byte{
		{kissFend, rnodeKISSCommandPlatform, 0x00, kissFend},
		{kissFend, 0x59, 0xf8, kissFend},
	}
	if !reflect.DeepEqual(serial.writes, wantWrites) {
		t.Fatalf("wipe command sequence mismatch:\n got: %#v\nwant: %#v", serial.writes, wantWrites)
	}
	wantSleeps := []time.Duration{13 * time.Second, 10 * time.Second}
	if !reflect.DeepEqual(recorder.calls, wantSleeps) {
		t.Fatalf("wipe sleep mismatch: got %v want %v", recorder.calls, wantSleeps)
	}
}
