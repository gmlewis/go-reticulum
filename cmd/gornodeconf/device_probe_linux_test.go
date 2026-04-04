// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

type deviceProbeFixture struct {
	detected  bool
	detectErr error
	calls     []time.Duration
}

func (f *deviceProbeFixture) Detect() error {
	return f.detectErr
}

func (f *deviceProbeFixture) Detected() bool {
	return f.detected
}

func (f *deviceProbeFixture) Sleep(d time.Duration) {
	f.calls = append(f.calls, d)
}

func TestDeviceProbeSuccessSleepsAndReturnsTrue(t *testing.T) {
	t.Parallel()

	fixture := &deviceProbeFixture{detected: true}
	probe := newDeviceProbeState(fixture, fixture)
	got, err := probe.probe()
	if err != nil {
		t.Fatalf("probe returned error: %v", err)
	}
	if !got {
		t.Fatalf("expected probe success")
	}
	wantCalls := []time.Duration{deviceProbeInitialDelay, deviceProbeFinalDelay}
	if !reflect.DeepEqual(fixture.calls, wantCalls) {
		t.Fatalf("sleep calls mismatch: got %v want %v", fixture.calls, wantCalls)
	}
}

func TestDeviceProbeFailureReturnsPythonMessage(t *testing.T) {
	t.Parallel()

	fixture := &deviceProbeFixture{detected: false}
	probe := newDeviceProbeState(fixture, fixture)
	got, err := probe.probe()
	if err == nil {
		t.Fatalf("expected error when device is not detected")
	}
	if got {
		t.Fatalf("expected probe failure")
	}
	if err.Error() != "Got invalid response while detecting device" {
		t.Fatalf("error mismatch: got %q", err.Error())
	}
	wantCalls := []time.Duration{deviceProbeInitialDelay, deviceProbeFinalDelay}
	if !reflect.DeepEqual(fixture.calls, wantCalls) {
		t.Fatalf("sleep calls mismatch: got %v want %v", fixture.calls, wantCalls)
	}
}

func TestDeviceProbeDetectErrorShortCircuits(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("detect failed")
	fixture := &deviceProbeFixture{detectErr: wantErr}
	probe := newDeviceProbeState(fixture, fixture)
	got, err := probe.probe()
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected detect error, got %v", err)
	}
	if got {
		t.Fatalf("expected probe failure")
	}
	wantCalls := []time.Duration{deviceProbeInitialDelay}
	if !reflect.DeepEqual(fixture.calls, wantCalls) {
		t.Fatalf("sleep calls mismatch: got %v want %v", fixture.calls, wantCalls)
	}
}
