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

type wifiPSKWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *wifiPSKWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetWiFiPSKWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &wifiPSKWriter{}
	state := &wifiPSKSetterState{name: "rnode", psk: "password", writer: writer}
	if err := state.setWiFiPSK(); err != nil {
		t.Fatalf("setWiFiPSK returned error: %v", err)
	}

	want := []byte{0xc0, 0x6c, 0x70, 0x61, 0x73, 0x73, 0x77, 0x6f, 0x72, 0x64, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi psk frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiPSKHandlesNoneAsDelete(t *testing.T) {
	t.Parallel()

	writer := &wifiPSKWriter{}
	state := &wifiPSKSetterState{name: "rnode", psk: nil, writer: writer}
	if err := state.setWiFiPSK(); err != nil {
		t.Fatalf("setWiFiPSK returned error: %v", err)
	}

	want := []byte{0xc0, 0x6c, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi psk nil frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiPSKRejectsInvalidLength(t *testing.T) {
	t.Parallel()

	state := &wifiPSKSetterState{name: "rnode", psk: "123456", writer: &wifiPSKWriter{}}
	err := state.setWiFiPSK()
	if err == nil {
		t.Fatalf("expected invalid psk length error")
	}
	want := "Invalid psk length"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiPSKReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &wifiPSKWriter{shortWrite: true}
	state := &wifiPSKSetterState{name: "rnode", psk: "password", writer: writer}
	err := state.setWiFiPSK()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending wifi PSK to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiPSKReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &wifiPSKWriter{err: wantErr}
	state := &wifiPSKSetterState{name: "rnode", psk: "password", writer: writer}
	if err := state.setWiFiPSK(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
