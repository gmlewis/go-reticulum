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

type wifiSSIDWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *wifiSSIDWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetWiFiSSIDWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &wifiSSIDWriter{}
	state := &wifiSSIDSetterState{name: "rnode", ssid: "TEST", writer: writer}
	if err := state.setWiFiSSID(); err != nil {
		t.Fatalf("setWiFiSSID returned error: %v", err)
	}

	want := []byte{0xc0, 0x6b, 0x54, 0x45, 0x53, 0x54, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi ssid frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiSSIDHandlesNoneAsDelete(t *testing.T) {
	t.Parallel()

	writer := &wifiSSIDWriter{}
	state := &wifiSSIDSetterState{name: "rnode", ssid: nil, writer: writer}
	if err := state.setWiFiSSID(); err != nil {
		t.Fatalf("setWiFiSSID returned error: %v", err)
	}

	want := []byte{0xc0, 0x6b, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi ssid nil frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiSSIDRejectsInvalidLength(t *testing.T) {
	t.Parallel()

	state := &wifiSSIDSetterState{name: "rnode", ssid: "1234567890123456789012345678901234", writer: &wifiSSIDWriter{}}
	err := state.setWiFiSSID()
	if err == nil {
		t.Fatalf("expected invalid SSID length error")
	}
	want := "Invalid SSID length"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiSSIDReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &wifiSSIDWriter{shortWrite: true}
	state := &wifiSSIDSetterState{name: "rnode", ssid: "TEST", writer: writer}
	err := state.setWiFiSSID()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending wifi SSID to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiSSIDReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &wifiSSIDWriter{err: wantErr}
	state := &wifiSSIDSetterState{name: "rnode", ssid: "TEST", writer: writer}
	if err := state.setWiFiSSID(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
