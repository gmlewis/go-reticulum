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

type wifiChannelWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *wifiChannelWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetWiFiChannelWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &wifiChannelWriter{}
	state := &wifiChannelSetterState{name: "rnode", channel: 11, writer: writer}
	if err := state.setWiFiChannel(); err != nil {
		t.Fatalf("setWiFiChannel returned error: %v", err)
	}

	want := []byte{0xc0, 0x6e, 0x0b, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("wifi channel frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetWiFiChannelRejectsInvalidChannel(t *testing.T) {
	t.Parallel()

	state := &wifiChannelSetterState{name: "rnode", channel: 0, writer: &wifiChannelWriter{}}
	err := state.setWiFiChannel()
	if err == nil {
		t.Fatalf("expected invalid channel error")
	}
	want := "Invalid WiFi channel"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiChannelReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &wifiChannelWriter{shortWrite: true}
	state := &wifiChannelSetterState{name: "rnode", channel: 11, writer: writer}
	err := state.setWiFiChannel()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending wifi channel to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiChannelReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &wifiChannelWriter{err: wantErr}
	state := &wifiChannelSetterState{name: "rnode", channel: 11, writer: writer}
	if err := state.setWiFiChannel(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
