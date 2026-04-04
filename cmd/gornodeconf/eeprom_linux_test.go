// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
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
	called bool
	duration time.Duration
}

func (s *eepromSleeper) Sleep(duration time.Duration) {
	s.called = true
	s.duration = duration
}

func TestRNodeDownloadEEPROMWritesPythonFrame(t *testing.T) {
	t.Parallel()

	recorder := &eepromWriteRecorder{}
	sleeper := &eepromSleeper{}
	parsed := false
	state := &eepromDownloaderState{
		name:   "rnode",
		writer: recorder,
		sleeper: sleeper,
		parse: func() error {
			parsed = true
			return nil
		},
		eeprom: []byte{0x01},
	}

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
