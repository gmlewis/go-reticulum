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

type radioStateWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *radioStateWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetRadioStateWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &radioStateWriter{}
	state := &radioStateSetterState{name: "rnode", state: 0x01, writer: writer}
	if err := state.setRadioState(); err != nil {
		t.Fatalf("setRadioState returned error: %v", err)
	}

	want := []byte{0xc0, 0x06, 0x01, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("radio state frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetRadioStateReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &radioStateWriter{shortWrite: true}
	state := &radioStateSetterState{name: "rnode", state: 0x01, writer: writer}
	err := state.setRadioState()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while configuring radio state for rnode"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetRadioStateReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &radioStateWriter{err: wantErr}
	state := &radioStateSetterState{name: "rnode", state: 0x01, writer: writer}
	if err := state.setRadioState(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
