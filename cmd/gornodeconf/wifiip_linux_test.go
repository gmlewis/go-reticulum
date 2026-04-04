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

type wifiIPWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *wifiIPWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetWiFiIPWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &wifiIPWriter{}
	state := &wifiIPSetterState{name: "rnode", ip: "10.1.2.3", writer: writer}
	if err := state.setWiFiIP(); err != nil {
		t.Fatalf("setWiFiIP returned error: %v", err)
	}

	want := []byte{0xc0, 0x84, 0x0a, 0x01, 0x02, 0x03, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi ip frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiIPHandlesNoneAsZeroAddress(t *testing.T) {
	t.Parallel()

	writer := &wifiIPWriter{}
	state := &wifiIPSetterState{name: "rnode", ip: nil, writer: writer}
	if err := state.setWiFiIP(); err != nil {
		t.Fatalf("setWiFiIP returned error: %v", err)
	}

	want := []byte{0xc0, 0x84, 0x00, 0x00, 0x00, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi ip nil frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiIPRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	state := &wifiIPSetterState{name: "rnode", ip: 123, writer: &wifiIPWriter{}}
	err := state.setWiFiIP()
	if err == nil {
		t.Fatalf("expected invalid IP error")
	}
	want := "Invalid IP address"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiIPReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &wifiIPWriter{shortWrite: true}
	state := &wifiIPSetterState{name: "rnode", ip: "10.1.2.3", writer: writer}
	err := state.setWiFiIP()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending wifi IP address to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiIPReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &wifiIPWriter{err: wantErr}
	state := &wifiIPSetterState{name: "rnode", ip: "10.1.2.3", writer: writer}
	if err := state.setWiFiIP(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
