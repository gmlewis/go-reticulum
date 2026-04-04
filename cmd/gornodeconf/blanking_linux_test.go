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

type displayBlankingWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *displayBlankingWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetDisplayBlankingWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &displayBlankingWriter{}
	state := &displayBlankingSetterState{name: "rnode", blankingTimeout: 260, writer: writer}
	if err := state.setDisplayBlanking(); err != nil {
		t.Fatalf("setDisplayBlanking returned error: %v", err)
	}

	want := []byte{0xc0, 0x64, 0x04, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("display blanking frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetDisplayBlankingReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &displayBlankingWriter{shortWrite: true}
	state := &displayBlankingSetterState{name: "rnode", blankingTimeout: 260, writer: writer}
	err := state.setDisplayBlanking()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending display blanking timeout command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetDisplayBlankingReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &displayBlankingWriter{err: wantErr}
	state := &displayBlankingSetterState{name: "rnode", blankingTimeout: 260, writer: writer}
	if err := state.setDisplayBlanking(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
