// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"crypto/md5"
	"errors"
	"testing"
	"time"
)

type eepromWriteRecorder struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *eepromWriteRecorder) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

type eepromSleeper struct {
	called   bool
	duration time.Duration
	state    *eepromDownloaderState
	eeprom   []byte
}

func (s *eepromSleeper) Sleep(duration time.Duration) {
	s.called = true
	s.duration = duration
	if s.state != nil {
		s.state.eeprom = append([]byte(nil), s.eeprom...)
	}
}

func TestRNodeDownloadEEPROMWritesPythonFrame(t *testing.T) {
	t.Parallel()

	recorder := &eepromWriteRecorder{}
	sleeper := &eepromSleeper{eeprom: []byte{0x01}}
	parsed := false
	state := &eepromDownloaderState{
		name:    "rnode",
		writer:  recorder,
		sleeper: sleeper,
		parse: func() error {
			parsed = true
			return nil
		},
		eeprom: []byte{0x01},
	}
	sleeper.state = state

	if err := state.downloadEEPROM(); err != nil {
		t.Fatalf("downloadEEPROM returned error: %v", err)
	}

	want := []byte{0xc0, 0x51, 0x00, 0xc0}
	if len(recorder.writes) != 1 || !bytes.Equal(recorder.writes[0], want) {
		t.Fatalf("EEPROM download frame mismatch:\n got: %#v\nwant: %x", recorder.writes, want)
	}
	if !sleeper.called || sleeper.duration != 600*time.Millisecond {
		t.Fatalf("expected 600ms sleep, got called=%v duration=%v", sleeper.called, sleeper.duration)
	}
	if !parsed {
		t.Fatalf("expected parse callback to be invoked")
	}
}

func TestRNodeDownloadEEPROMReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	recorder := &eepromWriteRecorder{shortWrite: true}
	state := &eepromDownloaderState{name: "rnode", writer: recorder, sleeper: &eepromSleeper{}}
	err := state.downloadEEPROM()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while downloading EEPROM"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeDownloadEEPROMReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	recorder := &eepromWriteRecorder{err: wantErr}
	state := &eepromDownloaderState{name: "rnode", writer: recorder, sleeper: &eepromSleeper{}}
	if err := state.downloadEEPROM(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}

func TestRNodeDownloadEEPROMReturnsErrorWhenEEPROMMissing(t *testing.T) {
	t.Parallel()

	recorder := &eepromWriteRecorder{}
	sleeper := &eepromSleeper{}
	state := &eepromDownloaderState{name: "rnode", writer: recorder, sleeper: sleeper}
	err := state.downloadEEPROM()
	if err == nil {
		t.Fatalf("expected missing EEPROM error")
	}
	want := "Could not download EEPROM from device. Is a valid firmware installed?"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeDownloadCfgSectorWritesPythonFrame(t *testing.T) {
	t.Parallel()

	recorder := &eepromWriteRecorder{}
	sleeper := &eepromSleeper{}
	state := &eepromDownloaderState{name: "rnode", writer: recorder, sleeper: sleeper}
	if err := state.downloadCfgSector(); err != nil {
		t.Fatalf("downloadCfgSector returned error: %v", err)
	}

	want := []byte{0xc0, 0x6d, 0x00, 0xc0}
	if len(recorder.writes) != 1 || !bytes.Equal(recorder.writes[0], want) {
		t.Fatalf("config sector download frame mismatch:\n got: %#v\nwant: %x", recorder.writes, want)
	}
	if !sleeper.called || sleeper.duration != 600*time.Millisecond {
		t.Fatalf("expected 600ms sleep, got called=%v duration=%v", sleeper.called, sleeper.duration)
	}
}

func TestRNodeDownloadCfgSectorReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	recorder := &eepromWriteRecorder{shortWrite: true}
	state := &eepromDownloaderState{name: "rnode", writer: recorder, sleeper: &eepromSleeper{}}
	err := state.downloadCfgSector()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while downloading config sector"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeDownloadCfgSectorReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	recorder := &eepromWriteRecorder{err: wantErr}
	state := &eepromDownloaderState{name: "rnode", writer: recorder, sleeper: &eepromSleeper{}}
	if err := state.downloadCfgSector(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}

func TestRNodeParseEEPROMExtractsDeviceSummaryFields(t *testing.T) {
	t.Parallel()

	eeprom := make([]byte, 0xa8)
	eeprom[0x00] = 0x03
	eeprom[0x01] = 0xa4
	eeprom[0x02] = 0x05
	eeprom[0x03] = 0x01
	eeprom[0x04] = 0x02
	eeprom[0x05] = 0x03
	eeprom[0x06] = 0x04
	eeprom[0x07] = 0x05
	eeprom[0x08] = 0x06
	eeprom[0x09] = 0x07
	eeprom[0x0a] = 0x08
	checksum := []byte{0x30, 0x60, 0x23, 0x43, 0x25, 0x77, 0x8c, 0x41, 0x9d, 0x48, 0xbf, 0xec, 0x0e, 0x87, 0x13, 0x71}
	copy(eeprom[0x0b:0x1b], checksum)
	for i := 0; i < 128; i++ {
		eeprom[0x1b+i] = byte(i)
	}
	eeprom[0x9b] = 0x73
	eeprom[0x9c] = 0x07
	eeprom[0x9d] = 0x05
	eeprom[0x9e] = 0x11
	eeprom[0x9f] = 0x00
	eeprom[0xa0] = 0x01
	eeprom[0xa1] = 0xe8
	eeprom[0xa2] = 0x48
	eeprom[0xa3] = 0x19
	eeprom[0xa4] = 0xcf
	eeprom[0xa5] = 0xd1
	eeprom[0xa6] = 0x90
	eeprom[0xa7] = 0x73

	state := &eepromDownloaderState{name: "rnode", eeprom: eeprom}
	if err := state.parseEEPROM(); err != nil {
		t.Fatalf("parseEEPROM returned error: %v", err)
	}

	if !state.provisioned {
		t.Fatalf("expected provisioned device")
	}
	if !state.configured {
		t.Fatalf("expected configured device")
	}
	if state.product != 0x03 || state.model != 0xa4 || state.hwRev != 0x05 {
		t.Fatalf("device identifiers mismatch: product=%#x model=%#x hwrev=%#x", state.product, state.model, state.hwRev)
	}
	if !bytes.Equal(state.serialno, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("serial number mismatch: got %x", state.serialno)
	}
	if !bytes.Equal(state.made, []byte{0x05, 0x06, 0x07, 0x08}) {
		t.Fatalf("manufacture time mismatch: got %x", state.made)
	}
	if !bytes.Equal(state.checksum, checksum) {
		t.Fatalf("checksum mismatch: got %x want %x", state.checksum, checksum)
	}
	if state.minFreq != 410000000 || state.maxFreq != 525000000 || state.maxOutput != 14 {
		t.Fatalf("model capabilities mismatch: min=%v max=%v out=%v", state.minFreq, state.maxFreq, state.maxOutput)
	}
	if state.confSF != 0x07 || state.confCR != 0x05 || state.confTXPower != 0x11 {
		t.Fatalf("config registers mismatch: sf=%#x cr=%#x txp=%#x", state.confSF, state.confCR, state.confTXPower)
	}
	if state.confBandwidth != 125000 || state.confFrequency != 433050000 {
		t.Fatalf("config values mismatch: bw=%v freq=%v", state.confBandwidth, state.confFrequency)
	}
}

func TestRNodeParseEEPROMRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()

	eeprom := make([]byte, 0xa8)
	eeprom[0x00] = 0x03
	eeprom[0x01] = 0xa4
	eeprom[0x02] = 0x05
	eeprom[0x03] = 0x01
	eeprom[0x04] = 0x02
	eeprom[0x05] = 0x03
	eeprom[0x06] = 0x04
	eeprom[0x07] = 0x05
	eeprom[0x08] = 0x06
	eeprom[0x09] = 0x07
	eeprom[0x0a] = 0x08
	eeprom[0x9b] = 0x73

	state := &eepromDownloaderState{name: "rnode", eeprom: eeprom}
	err := state.parseEEPROM()
	if err == nil {
		t.Fatalf("expected checksum mismatch error")
	}
	want := "EEPROM checksum mismatch"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeParseEEPROMUsesCustomMetadataState(t *testing.T) {
	t.Parallel()

	eeprom := make([]byte, 0xa8)
	eeprom[0x00] = 0x09
	eeprom[0x01] = 0x44
	eeprom[0x02] = 0x05
	eeprom[0x03] = 0x01
	eeprom[0x04] = 0x02
	eeprom[0x05] = 0x03
	eeprom[0x06] = 0x04
	eeprom[0x07] = 0x05
	eeprom[0x08] = 0x06
	eeprom[0x09] = 0x07
	eeprom[0x0a] = 0x08
	checksumInput := []byte{0x09, 0x44, 0x05, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	checksum := md5.Sum(checksumInput)
	copy(eeprom[0x0b:0x1b], checksum[:])
	eeprom[0x9b] = 0x73
	eeprom[0xa7] = 0x00

	state := &eepromDownloaderState{
		eeprom: eeprom,
		metadata: eepromMetadataState{
			modelCapabilitiesByCode: map[byte]modelCapability{
				0x44: {minFreq: 123000000, maxFreq: 456000000, maxOutput: 9, summary: "123 - 456 MHz", modem: "TESTMODEM"},
			},
			productNames: map[byte]string{
				0x09: "CustomBoard",
			},
		},
	}

	if err := state.parseEEPROM(); err != nil {
		t.Fatalf("parseEEPROM returned error: %v", err)
	}

	if state.minFreq != 123000000 || state.maxFreq != 456000000 || state.maxOutput != 9 {
		t.Fatalf("custom capability mismatch: min=%v max=%v out=%v", state.minFreq, state.maxFreq, state.maxOutput)
	}

	lines := state.deviceInfoLines("2026-04-05")
	joined := bytes.Join(func() [][]byte {
		out := make([][]byte, 0, len(lines))
		for _, line := range lines {
			out = append(out, []byte(line))
		}
		return out
	}(), []byte("\n"))
	if !bytes.Contains(joined, []byte("CustomBoard 123 - 456 MHz (09:44)")) {
		t.Fatalf("device info did not use custom metadata: %q", joined)
	}
	if !bytes.Contains(joined, []byte("TESTMODEM")) {
		t.Fatalf("device info did not use custom modem: %q", joined)
	}
}

func TestRNodeDeviceInfoLinesFormatsPythonSummary(t *testing.T) {
	t.Parallel()

	state := &eepromDownloaderState{
		name:          "rnode",
		provisioned:   true,
		configured:    true,
		product:       0x03,
		model:         0xa4,
		hwRev:         0x05,
		serialno:      []byte{0x01, 0x02, 0x03, 0x04},
		made:          []byte{0x65, 0x53, 0xf1, 0x00},
		checksum:      []byte{0x30, 0x60, 0x23, 0x43, 0x25, 0x77, 0x8c, 0x41, 0x9d, 0x48, 0xbf, 0xec, 0x0e, 0x87, 0x13, 0x71},
		minFreq:       410000000,
		maxFreq:       525000000,
		maxOutput:     14,
		confSF:        7,
		confCR:        5,
		confTXPower:   17,
		confFrequency: 433050000,
		confBandwidth: 125000,
		version:       "2.5.0",
	}

	got := state.deviceInfoLines("2023-11-14 22:13:20")
	want := []string{
		"",
		"Device info:",
		"\tProduct            : RNode 410 - 525 MHz (03:a4)",
		"\tDevice signature   : Unverified",
		"\tFirmware version   : 2.5.0",
		"\tHardware revision  : 5",
		"\tSerial number      : 01020304",
		"\tModem chip         : SX1278",
		"\tFrequency range    : 410 MHz - 525 MHz",
		"\tMax TX power       : 14 dBm",
		"\tManufactured       : 2023-11-14 22:13:20",
		"",
		"\tDevice mode        : TNC",
		"\t  Frequency        : 433.05 MHz",
		"\t  Bandwidth        : 125 KHz",
		"\t  TX power         : 17 dBm (50.119 mW)",
		"\t  Spreading factor : 7",
		"\t  Coding rate      : 5",
		"\t  On-air bitrate   : 5.47 kbps",
	}

	if len(got) != len(want) {
		t.Fatalf("summary line count mismatch: got %v want %v\n%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("summary line %v mismatch:\n got: %q\nwant: %q", i, got[i], want[i])
		}
	}
}
