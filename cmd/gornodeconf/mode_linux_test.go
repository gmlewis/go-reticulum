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

type modeWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *modeWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

type modeSleeper struct {
	calls []time.Duration
}

func (s *modeSleeper) Sleep(d time.Duration) {
	s.calls = append(s.calls, d)
}

func TestRNodeSetNormalModeWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &modeWriter{}
	if err := rnodeSetNormalMode(writer); err != nil {
		t.Fatalf("rnodeSetNormalMode returned error: %v", err)
	}

	want := []byte{0xc0, 0x54, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("normal mode frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeSetTNCModeWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &modeWriter{}
	sleeper := &modeSleeper{}
	state := &modeSwitchState{platform: 0x00, writer: writer, sleeper: sleeper}
	if err := state.setTNCMode(); err != nil {
		t.Fatalf("setTNCMode returned error: %v", err)
	}

	wantWrites := [][]byte{{0xc0, 0x53, 0x00, 0xc0}}
	if !reflect.DeepEqual(writer.writes, wantWrites) {
		t.Fatalf("TNC mode frame mismatch:\n got: %#v\nwant: %#v", writer.writes, wantWrites)
	}
	if len(sleeper.calls) != 0 {
		t.Fatalf("did not expect hard reset sleep for non-ESP32 platform: %v", sleeper.calls)
	}
}

func TestRNodeSetTNCModeHardResetsESP32(t *testing.T) {
	t.Parallel()

	writer := &modeWriter{}
	sleeper := &modeSleeper{}
	state := &modeSwitchState{platform: romPlatformESP32, writer: writer, sleeper: sleeper}
	if err := state.setTNCMode(); err != nil {
		t.Fatalf("setTNCMode returned error: %v", err)
	}

	wantWrites := [][]byte{{0xc0, 0x53, 0x00, 0xc0}, {0xc0, 0x55, 0xf8, 0xc0}}
	if !reflect.DeepEqual(writer.writes, wantWrites) {
		t.Fatalf("TNC mode frame mismatch:\n got: %#v\nwant: %#v", writer.writes, wantWrites)
	}
	wantSleeps := []time.Duration{2 * time.Second}
	if !reflect.DeepEqual(sleeper.calls, wantSleeps) {
		t.Fatalf("hard reset sleep mismatch: got %v want %v", sleeper.calls, wantSleeps)
	}
}

func TestRNodeSetNormalModeReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &modeWriter{shortWrite: true}
	if err := rnodeSetNormalMode(writer); err == nil {
		t.Fatalf("expected error on short write")
	} else if err.Error() != "An IO error occurred while configuring device mode" {
		t.Fatalf("error mismatch: got %q", err.Error())
	}
}

func TestRNodeSetTNCModeReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &modeWriter{err: wantErr}
	state := &modeSwitchState{platform: romPlatformESP32, writer: writer, sleeper: &modeSleeper{}}
	if err := state.setTNCMode(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
