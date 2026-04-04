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

type interferenceAvoidanceWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *interferenceAvoidanceWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeSetInterferenceAvoidanceWritesPythonFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		disabled bool
		want     []byte
	}{
		{name: "enabled", disabled: false, want: []byte{0xc0, 0x69, 0x00, 0xc0}},
		{name: "disabled", disabled: true, want: []byte{0xc0, 0x69, 0x01, 0xc0}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			writer := &interferenceAvoidanceWriter{}
			state := &interferenceAvoidanceSetterState{name: "rnode", disabled: test.disabled, writer: writer}
			if err := state.setDisableInterferenceAvoidance(); err != nil {
				t.Fatalf("setDisableInterferenceAvoidance returned error: %v", err)
			}

			if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], test.want) {
				t.Fatalf("interference avoidance frame mismatch:\n got: %#v\nwant: %x", writer.writes, test.want)
			}
		})
	}
}

func TestRNodeSetInterferenceAvoidanceReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &interferenceAvoidanceWriter{shortWrite: true}
	state := &interferenceAvoidanceSetterState{name: "rnode", disabled: true, writer: writer}
	err := state.setDisableInterferenceAvoidance()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending interference avoidance configuration command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeSetInterferenceAvoidanceReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &interferenceAvoidanceWriter{err: wantErr}
	state := &interferenceAvoidanceSetterState{name: "rnode", disabled: true, writer: writer}
	if err := state.setDisableInterferenceAvoidance(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
