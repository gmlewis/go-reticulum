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

type codingRateWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *codingRateWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetCodingRateWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &codingRateWriter{}
	state := &codingRateSetterState{name: "rnode", codingRate: 5, writer: writer}
	if err := state.setCodingRate(); err != nil {
		t.Fatalf("setCodingRate returned error: %v", err)
	}

	want := []byte{0xc0, 0x05, 0x05, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("coding rate frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetCodingRateReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &codingRateWriter{shortWrite: true}
	state := &codingRateSetterState{name: "rnode", codingRate: 5, writer: writer}
	err := state.setCodingRate()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while configuring coding rate for rnode"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetCodingRateReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &codingRateWriter{err: wantErr}
	state := &codingRateSetterState{name: "rnode", codingRate: 5, writer: writer}
	if err := state.setCodingRate(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
