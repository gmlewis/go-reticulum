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

type eepromByteWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *eepromByteWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestWriteEEPROMByteWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &eepromByteWriter{}
	if err := writeEEPROMByte(writer, 0x0b, 0x7a); err != nil {
		t.Fatalf("writeEEPROMByte returned error: %v", err)
	}
	want := []byte{kissFend, rnodeKISSCommandROMWrite, 0x0b, 0x7a, kissFend}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("EEPROM write frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestWriteEEPROMByteReturnsShortWriteError(t *testing.T) {
	t.Parallel()

	writer := &eepromByteWriter{shortWrite: true}
	if err := writeEEPROMByte(writer, 0x0b, 0x7a); err == nil {
		t.Fatal("expected short write error")
	}
}

func TestWriteEEPROMByteReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &eepromByteWriter{err: wantErr}
	if err := writeEEPROMByte(writer, 0x0b, 0x7a); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
