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

type signatureStoreWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *signatureStoreWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestStoreSignatureWritesEscapedPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &signatureStoreWriter{}
	signature := []byte{0x01, kissFend, 0x02, kissFesc, 0x03}
	if err := storeSignature(writer, signature); err != nil {
		t.Fatalf("storeSignature returned error: %v", err)
	}
	want := []byte{kissFend, rnodeKISSCommandDeviceSignature, 0x01, kissFesc, kissTfend, 0x02, kissFesc, kissTfesc, 0x03, kissFend}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("signature frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestStoreSignatureReturnsShortWriteError(t *testing.T) {
	t.Parallel()

	writer := &signatureStoreWriter{shortWrite: true}
	if err := storeSignature(writer, []byte{0x01}); err == nil {
		t.Fatal("expected short write error")
	}
}

func TestStoreSignatureReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &signatureStoreWriter{err: wantErr}
	if err := storeSignature(writer, []byte{0x01}); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
