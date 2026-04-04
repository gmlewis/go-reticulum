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
)

type firmwareUpdateWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *firmwareUpdateWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeIndicateFirmwareUpdateWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &firmwareUpdateWriter{}
	state := &firmwareUpdateIndicatorState{name: "rnode", writer: writer}
	if err := state.indicateFirmwareUpdate(); err != nil {
		t.Fatalf("indicateFirmwareUpdate returned error: %v", err)
	}

	want := []byte{0xc0, 0x61, 0x01, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("firmware update frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeIndicateFirmwareUpdateReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &firmwareUpdateWriter{shortWrite: true}
	state := &firmwareUpdateIndicatorState{name: "rnode", writer: writer}
	err := state.indicateFirmwareUpdate()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending firmware update command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeIndicateFirmwareUpdateReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &firmwareUpdateWriter{err: wantErr}
	state := &firmwareUpdateIndicatorState{name: "rnode", writer: writer}
	if err := state.indicateFirmwareUpdate(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
