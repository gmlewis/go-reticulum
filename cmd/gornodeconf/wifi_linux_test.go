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

type wifiModeWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *wifiModeWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetWiFiModeWritesPythonFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode byte
		want []byte
	}{
		{name: "off", mode: 0x00, want: []byte{0xc0, 0x6a, 0x00, 0xc0}},
		{name: "ap", mode: 0x01, want: []byte{0xc0, 0x6a, 0x01, 0xc0}},
		{name: "station", mode: 0x02, want: []byte{0xc0, 0x6a, 0x02, 0xc0}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			writer := &wifiModeWriter{}
			state := &wifiModeSetterState{name: "rnode", mode: test.mode, writer: writer}
			if err := state.setWiFiMode(); err != nil {
				t.Fatalf("setWiFiMode returned error: %v", err)
			}

			if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], test.want) {
				t.Fatalf("wifi mode frame mismatch:\n got: %#v\nwant: %x", writer.writes, test.want)
			}
		})
	}
}

func TestRNodeSetWiFiModeReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &wifiModeWriter{shortWrite: true}
	state := &wifiModeSetterState{name: "rnode", mode: 0x01, writer: writer}
	err := state.setWiFiMode()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending wifi mode command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetWiFiModeReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &wifiModeWriter{err: wantErr}
	state := &wifiModeSetterState{name: "rnode", mode: 0x01, writer: writer}
	if err := state.setWiFiMode(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
