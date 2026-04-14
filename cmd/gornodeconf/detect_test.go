// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"testing"
)

type captureDetectWriter struct {
	data       []byte
	writeCount int
	shortWrite bool
	err        error
}

func (w *captureDetectWriter) Write(data []byte) (int, error) {
	w.writeCount++
	w.data = append([]byte(nil), data...)
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeDetectWritesPythonCommandSequence(t *testing.T) {
	t.Parallel()

	writer := &captureDetectWriter{}
	if err := rnodeDetect(writer, "rnode"); err != nil {
		t.Fatalf("rnodeDetect returned error: %v", err)
	}

	want := rnodeDetectCommand()
	if !bytes.Equal(writer.data, want) {
		t.Fatalf("detect command mismatch:\n got: %x\nwant: %x", writer.data, want)
	}
	if writer.writeCount != 1 {
		t.Fatalf("expected one write, got %v", writer.writeCount)
	}
}

func TestRNodeDetectReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &captureDetectWriter{shortWrite: true}
	err := rnodeDetect(writer, "rnode")
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	if err.Error() != "An IO error occurred while detecting hardware for rnode" {
		t.Fatalf("error mismatch: got %q", err.Error())
	}
}

func TestRNodeDetectReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &captureDetectWriter{err: wantErr}
	err := rnodeDetect(writer, "rnode")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error to be returned, got %v", err)
	}
}
