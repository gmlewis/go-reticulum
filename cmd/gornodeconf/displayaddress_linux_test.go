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

type displayAddressWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *displayAddressWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetDisplayAddressWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &displayAddressWriter{}
	state := &displayAddressSetterState{name: "rnode", address: 300, writer: writer}
	if err := state.setDisplayAddress(); err != nil {
		t.Fatalf("setDisplayAddress returned error: %v", err)
	}

	want := []byte{0xc0, 0x63, 0x2c, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("display address frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetDisplayAddressReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &displayAddressWriter{shortWrite: true}
	state := &displayAddressSetterState{name: "rnode", address: 300, writer: writer}
	err := state.setDisplayAddress()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending display address command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetDisplayAddressReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &displayAddressWriter{err: wantErr}
	state := &displayAddressSetterState{name: "rnode", address: 300, writer: writer}
	if err := state.setDisplayAddress(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
