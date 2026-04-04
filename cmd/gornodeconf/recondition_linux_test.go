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

type displayReconditionWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *displayReconditionWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeReconditionDisplayWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &displayReconditionWriter{}
	state := &displayReconditionSetterState{name: "rnode", writer: writer}
	if err := state.reconditionDisplay(); err != nil {
		t.Fatalf("reconditionDisplay returned error: %v", err)
	}

	want := []byte{0xc0, 0x68, 0x01, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("display recondition frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeReconditionDisplayReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &displayReconditionWriter{shortWrite: true}
	state := &displayReconditionSetterState{name: "rnode", writer: writer}
	err := state.reconditionDisplay()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending display recondition command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeReconditionDisplayReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &displayReconditionWriter{err: wantErr}
	state := &displayReconditionSetterState{name: "rnode", writer: writer}
	if err := state.reconditionDisplay(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
