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

type displayIntensityWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *displayIntensityWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetDisplayIntensityWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &displayIntensityWriter{}
	state := &displayIntensitySetterState{name: "rnode", intensity: 300, writer: writer}
	if err := state.setDisplayIntensity(); err != nil {
		t.Fatalf("setDisplayIntensity returned error: %v", err)
	}

	want := []byte{0xc0, 0x45, 0x2c, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("display intensity frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetDisplayIntensityReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &displayIntensityWriter{shortWrite: true}
	state := &displayIntensitySetterState{name: "rnode", intensity: 300, writer: writer}
	err := state.setDisplayIntensity()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending display intensity command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetDisplayIntensityReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &displayIntensityWriter{err: wantErr}
	state := &displayIntensitySetterState{name: "rnode", intensity: 300, writer: writer}
	if err := state.setDisplayIntensity(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
