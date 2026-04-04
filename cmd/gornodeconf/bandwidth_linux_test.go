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

type bandwidthWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *bandwidthWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetBandwidthWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &bandwidthWriter{}
	state := &bandwidthSetterState{name: "rnode", bandwidth: 125000, writer: writer}
	if err := state.setBandwidth(); err != nil {
		t.Fatalf("setBandwidth returned error: %v", err)
	}

	want := []byte{0xc0, 0x02, 0x00, 0x01, 0xe8, 0x48, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("bandwidth frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetBandwidthReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &bandwidthWriter{shortWrite: true}
	state := &bandwidthSetterState{name: "rnode", bandwidth: 125000, writer: writer}
	err := state.setBandwidth()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while configuring bandwidth for rnode"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetBandwidthReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &bandwidthWriter{err: wantErr}
	state := &bandwidthSetterState{name: "rnode", bandwidth: 125000, writer: writer}
	if err := state.setBandwidth(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
