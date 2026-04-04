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

type txPowerWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *txPowerWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetTXPowerWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &txPowerWriter{}
	state := &txPowerSetterState{name: "rnode", txpower: 17, writer: writer}
	if err := state.setTXPower(); err != nil {
		t.Fatalf("setTXPower returned error: %v", err)
	}

	want := []byte{0xc0, 0x03, 0x11, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("TX power frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetTXPowerReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &txPowerWriter{shortWrite: true}
	state := &txPowerSetterState{name: "rnode", txpower: 17, writer: writer}
	err := state.setTXPower()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while configuring TX power for rnode"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetTXPowerReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &txPowerWriter{err: wantErr}
	state := &txPowerSetterState{name: "rnode", txpower: 17, writer: writer}
	if err := state.setTXPower(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
