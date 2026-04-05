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

type bootstrapROMWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *bootstrapROMWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestBootstrapEEPROMWritesExpectedFrames(t *testing.T) {
	t.Parallel()

	writer := &bootstrapROMWriter{}
	if err := bootstrapEEPROM(writer, 0x03, 0xa4, 0x35, 0x01020304, 0x05060708, []byte{0x10, 0x20, 0x30}); err != nil {
		t.Fatalf("bootstrapEEPROM returned error: %v", err)
	}

	want := [][]byte{
		{kissFend, rnodeKISSCommandROMWrite, 0x00, 0x03, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x01, 0xa4, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x02, 0x35, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x03, 0x01, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x04, 0x02, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x05, 0x03, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x06, 0x04, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x07, 0x05, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x08, 0x06, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x09, 0x07, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x0a, 0x08, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x0b, 0xd8, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x0c, 0x15, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x0d, 0xcd, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x0e, 0xb2, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x0f, 0x39, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x10, 0x02, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x11, 0x4a, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x12, 0x6b, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x13, 0x77, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x14, 0x9c, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x15, 0xc7, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x16, 0x07, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x17, 0xc9, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x18, 0xb0, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x19, 0xae, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x1a, 0xe4, kissFend},
		{kissFend, rnodeKISSCommandDeviceSignature, 0x10, 0x20, 0x30, kissFend},
		{kissFend, rnodeKISSCommandROMWrite, 0x9b, 0x73, kissFend},
	}

	if len(writer.writes) != len(want) {
		t.Fatalf("unexpected frame count: got %d want %d", len(writer.writes), len(want))
	}
	for i := range want {
		if !bytes.Equal(writer.writes[i], want[i]) {
			t.Fatalf("frame %d mismatch:\n got: %x\nwant: %x", i, writer.writes[i], want[i])
		}
	}
}

func TestBootstrapEEPROMReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &bootstrapROMWriter{err: wantErr}
	if err := bootstrapEEPROM(writer, 0x03, 0xa4, 0x35, 0x01020304, 0x05060708, []byte{0x10}); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
