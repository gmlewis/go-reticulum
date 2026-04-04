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

type signatureWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *signatureWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeStoreSignatureWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &signatureWriter{}
	state := &signatureSetterState{name: "rnode", signature: []byte{0xc0, 0xdb, 0x01}, writer: writer}
	if err := state.storeSignature(); err != nil {
		t.Fatalf("storeSignature returned error: %v", err)
	}

	want := []byte{0xc0, 0x57, 0xdb, 0xdc, 0xdb, 0xdd, 0x01, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("signature frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeStoreSignatureReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &signatureWriter{shortWrite: true}
	state := &signatureSetterState{name: "rnode", signature: []byte{0xc0, 0xdb, 0x01}, writer: writer}
	err := state.storeSignature()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending signature to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeStoreSignatureReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &signatureWriter{err: wantErr}
	state := &signatureSetterState{name: "rnode", signature: []byte{0xc0, 0xdb, 0x01}, writer: writer}
	if err := state.storeSignature(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
