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

type spreadingFactorWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *spreadingFactorWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetSpreadingFactorWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &spreadingFactorWriter{}
	state := &spreadingFactorSetterState{name: "rnode", spreadingFactor: 7, writer: writer}
	if err := state.setSpreadingFactor(); err != nil {
		t.Fatalf("setSpreadingFactor returned error: %v", err)
	}

	want := []byte{0xc0, 0x04, 0x07, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("spreading factor frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetSpreadingFactorReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &spreadingFactorWriter{shortWrite: true}
	state := &spreadingFactorSetterState{name: "rnode", spreadingFactor: 7, writer: writer}
	err := state.setSpreadingFactor()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while configuring spreading factor for rnode"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetSpreadingFactorReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &spreadingFactorWriter{err: wantErr}
	state := &spreadingFactorSetterState{name: "rnode", spreadingFactor: 7, writer: writer}
	if err := state.setSpreadingFactor(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
