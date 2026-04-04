// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

type leaveWriter struct {
	data       []byte
	shortWrite bool
	err        error
}

func (w *leaveWriter) Write(data []byte) (int, error) {
	w.data = append([]byte(nil), data...)
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

type leaveSleeper struct {
	calls []time.Duration
}

func (s *leaveSleeper) Sleep(d time.Duration) {
	s.calls = append(s.calls, d)
}

func TestRNodeLeaveWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &leaveWriter{}
	sleeper := &leaveSleeper{}
	if err := rnodeLeave(writer, sleeper); err != nil {
		t.Fatalf("rnodeLeave returned error: %v", err)
	}

	want := []byte{0xc0, 0x0a, 0xff, 0xc0}
	if !bytes.Equal(writer.data, want) {
		t.Fatalf("leave frame mismatch:\n got: %x\nwant: %x", writer.data, want)
	}
	wantSleeps := []time.Duration{time.Second}
	if !reflect.DeepEqual(sleeper.calls, wantSleeps) {
		t.Fatalf("sleep calls mismatch: got %v want %v", sleeper.calls, wantSleeps)
	}
}

func TestRNodeLeaveReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &leaveWriter{shortWrite: true}
	sleeper := &leaveSleeper{}
	err := rnodeLeave(writer, sleeper)
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	if err.Error() != "An IO error occurred while sending host left command to device" {
		t.Fatalf("error mismatch: got %q", err.Error())
	}
	if len(sleeper.calls) != 0 {
		t.Fatalf("sleep should not be called on write failure: %v", sleeper.calls)
	}
}

func TestRNodeLeaveReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &leaveWriter{err: wantErr}
	sleeper := &leaveSleeper{}
	err := rnodeLeave(writer, sleeper)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
	if len(sleeper.calls) != 0 {
		t.Fatalf("sleep should not be called on write failure: %v", sleeper.calls)
	}
}
