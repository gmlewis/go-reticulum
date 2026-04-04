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

type displayRotationWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *displayRotationWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetDisplayRotationWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &displayRotationWriter{}
	state := &displayRotationSetterState{name: "rnode", rotation: 5, writer: writer}
	if err := state.setDisplayRotation(); err != nil {
		t.Fatalf("setDisplayRotation returned error: %v", err)
	}

	want := []byte{0xc0, 0x67, 0x05, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("display rotation frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetDisplayRotationReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &displayRotationWriter{shortWrite: true}
	state := &displayRotationSetterState{name: "rnode", rotation: 5, writer: writer}
	err := state.setDisplayRotation()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending display rotation command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetDisplayRotationReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &displayRotationWriter{err: wantErr}
	state := &displayRotationSetterState{name: "rnode", rotation: 5, writer: writer}
	if err := state.setDisplayRotation(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
