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

type wifiNetmaskWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *wifiNetmaskWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetWiFiNetmaskWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &wifiNetmaskWriter{}
	state := &wifiNetmaskSetterState{name: "rnode", nm: "10.1.2.3", writer: writer}
	if err := state.setWiFiNM(); err != nil {
		t.Fatalf("setWiFiNM returned error: %v", err)
	}

	want := []byte{0xc0, 0x85, 0x0a, 0x01, 0x02, 0x03, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi netmask frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiNetmaskHandlesNoneAsZeroAddress(t *testing.T) {
	t.Parallel()

	writer := &wifiNetmaskWriter{}
	state := &wifiNetmaskSetterState{name: "rnode", nm: nil, writer: writer}
	if err := state.setWiFiNM(); err != nil {
		t.Fatalf("setWiFiNM returned error: %v", err)
	}

	want := []byte{0xc0, 0x85, 0x00, 0x00, 0x00, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi netmask nil frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiNetmaskRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	state := &wifiNetmaskSetterState{name: "rnode", nm: 123, writer: &wifiNetmaskWriter{}}
	err := state.setWiFiNM()
	if err == nil {
		t.Fatalf("expected invalid IP error")
	}
	want := "Invalid IP address"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiNetmaskReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &wifiNetmaskWriter{shortWrite: true}
	state := &wifiNetmaskSetterState{name: "rnode", nm: "10.1.2.3", writer: writer}
	err := state.setWiFiNM()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending wifi netmask to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiNetmaskReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &wifiNetmaskWriter{err: wantErr}
	state := &wifiNetmaskSetterState{name: "rnode", nm: "10.1.2.3", writer: writer}
	if err := state.setWiFiNM(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
