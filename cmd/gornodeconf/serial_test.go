// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"reflect"
	"testing"
	"time"
)

type stubSerial struct {
	closed bool
}

func (s *stubSerial) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (s *stubSerial) Write(data []byte) (int, error) {
	return len(data), nil
}

func (s *stubSerial) Close() error {
	s.closed = true
	return nil
}

type stubLeaver struct {
	called bool
}

func (s *stubLeaver) Leave() {
	s.called = true
}

func TestRnodeOpenSerialUsesPythonSettings(t *testing.T) {
	var got serialSettings
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		got = settings
		return &stubSerial{}, nil
	}}

	port := tempSerialPort(t)
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		t.Fatalf("rnodeOpenSerial returned error: %v", err)
	}
	if serial == nil {
		t.Fatalf("rnodeOpenSerial returned nil serial port")
	}

	want := serialSettings{
		Port:     port,
		BaudRate: rnodeBaudRate,
		ByteSize: 8,
		Parity:   "N",
		StopBits: 1,
		XonXoff:  false,
		RTSCTS:   false,
		Timeout:  0,
		DSRDTR:   false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("serial settings mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	if got.InterByteTimeout != nil {
		t.Fatalf("inter-byte timeout should be nil, got %v", got.InterByteTimeout)
	}
	if got.WriteTimeout != nil {
		t.Fatalf("write timeout should be nil, got %v", got.WriteTimeout)
	}
}

func TestGracefulExitLeavesRNodeBeforeExiting(t *testing.T) {
	controller := newExitController()
	leaver := &stubLeaver{}
	serial := &stubSerial{}
	controller.activeRNode = leaver
	controller.activeSerial = serial
	slept := false
	controller.sleep = func(time.Duration) { slept = true }
	exited := 0
	controller.exit = func(code int) { exited = code }

	controller.gracefulExit(17)

	if !leaver.called {
		t.Fatalf("expected Leave to be called")
	}
	if serial.closed {
		t.Fatalf("expected raw serial to remain open when a live RNode handles shutdown")
	}
	if slept {
		t.Fatalf("did not expect sleep before leaving a live RNode")
	}
	if exited != 17 {
		t.Fatalf("exit code mismatch: got %v want %v", exited, 17)
	}
}

func TestGracefulExitClosesRawSerialWhenNoRNode(t *testing.T) {
	controller := newExitController()
	serial := &stubSerial{}
	controller.activeRNode = nil
	controller.activeSerial = serial
	slept := false
	controller.sleep = func(time.Duration) { slept = true }
	exited := 0
	controller.exit = func(code int) { exited = code }

	controller.gracefulExit(23)

	if !slept {
		t.Fatalf("expected sleep before closing raw serial")
	}
	if !serial.closed {
		t.Fatalf("expected raw serial to be closed")
	}
	if exited != 23 {
		t.Fatalf("exit code mismatch: got %v want %v", exited, 23)
	}
}
