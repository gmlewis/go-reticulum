// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"log"
	"reflect"
	"strings"
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

type debugSerialStub struct {
	reads  []byte
	writes [][]byte
	closed bool
}

func (s *debugSerialStub) Read(data []byte) (int, error) {
	if len(s.reads) == 0 {
		return 0, io.EOF
	}
	data[0] = s.reads[0]
	s.reads = s.reads[1:]
	return 1, nil
}

func (s *debugSerialStub) Write(data []byte) (int, error) {
	s.writes = append(s.writes, append([]byte(nil), data...))
	return len(data), nil
}

func (s *debugSerialStub) Close() error {
	s.closed = true
	return nil
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

func TestDebugSerialLogsTraffic(t *testing.T) {
	var logOutput bytes.Buffer
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(&logOutput)
	t.Cleanup(func() {
		log.SetOutput(io.Discard)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})

	serial := &debugSerialStub{reads: []byte{0x11, 0x22}}
	rt := cliRuntime{
		debug: true,
		openSerial: func(settings serialSettings) (serialPort, error) {
			return serial, nil
		},
	}

	wrapped, err := rt.rnodeOpenSerial("ttyUSB0")
	if err != nil {
		t.Fatalf("rnodeOpenSerial returned error: %v", err)
	}
	if _, err := wrapped.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	buf := make([]byte, 2)
	if _, err := wrapped.Read(buf); err != nil && err != io.EOF {
		t.Fatalf("read failed: %v", err)
	}
	if err := wrapped.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	got := logOutput.String()
	for _, want := range []string{
		"gornodeconf debug open ttyUSB0",
		"gornodeconf debug write ttyUSB0 [1 2 3]",
		"gornodeconf debug read ttyUSB0 [17]",
		"gornodeconf debug close ttyUSB0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug log missing %q in %q", want, got)
		}
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
