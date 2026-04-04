// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

type firmwareHashWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *firmwareHashWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetFirmwareHashWritesPythonFrame(t *testing.T) {
	t.Parallel()

	hashBytes, err := hex.DecodeString("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("DecodeString failed: %v", err)
	}
	writer := &firmwareHashWriter{}
	state := &firmwareHashSetterState{name: "rnode", hashBytes: hashBytes, writer: writer}
	if err := state.setFirmwareHash(); err != nil {
		t.Fatalf("setFirmwareHash returned error: %v", err)
	}

	want := []byte{0xc0, 0x58, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("firmware hash frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetFirmwareHashReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	hashBytes, err := hex.DecodeString("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("DecodeString failed: %v", err)
	}
	writer := &firmwareHashWriter{shortWrite: true}
	state := &firmwareHashSetterState{name: "rnode", hashBytes: hashBytes, writer: writer}
	err = state.setFirmwareHash()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending firmware hash to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetFirmwareHashReturnsWriterError(t *testing.T) {
	t.Parallel()

	hashBytes, err := hex.DecodeString("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("DecodeString failed: %v", err)
	}
	wantErr := errors.New("boom")
	writer := &firmwareHashWriter{err: wantErr}
	state := &firmwareHashSetterState{name: "rnode", hashBytes: hashBytes, writer: writer}
	if err := state.setFirmwareHash(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
