// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

type scriptedSerial struct {
	mu           sync.Mutex
	reads        []byte
	closed       bool
	writes       [][]byte
	readErr      error
	blockOnEmpty bool
	wait         chan struct{}
	once         sync.Once
}

func (s *scriptedSerial) Read(data []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.EOF
	}
	if len(s.reads) == 0 {
		if s.blockOnEmpty {
			wait := s.wait
			s.mu.Unlock()
			<-wait
			return 0, io.EOF
		}
		if s.readErr != nil {
			err := s.readErr
			s.mu.Unlock()
			return 0, err
		}
		s.mu.Unlock()
		return 0, io.EOF
	}
	data[0] = s.reads[0]
	s.reads = s.reads[1:]
	s.mu.Unlock()
	return 1, nil
}

func (s *scriptedSerial) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, append([]byte(nil), data...))
	return len(data), nil
}

func (s *scriptedSerial) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.once.Do(func() {
		if s.wait != nil {
			close(s.wait)
		}
	})
	return nil
}

func TestCaptureRnodeHashesReadsPythonFrames(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{
		reads: append([]byte(nil), []byte{
			kissFend, rnodeKISSCommandFWVersion, 0x02, 0x05, kissFend,
			kissFend, rnodeKISSCommandDevHash,
			0x01, 0x02, 0x03, 0x04,
			0x05, 0x06, 0x07, 0x08,
			0x09, 0x0a, 0x0b, 0x0c,
			0x0d, 0x0e, 0x0f, 0x10,
			0x11, 0x12, 0x13, 0x14,
			0x15, 0x16, 0x17, 0x18,
			0x19, 0x1a, 0x1b, 0x1c,
			0x1d, 0x1e, 0x1f, 0x20, kissFend,
			kissFend, rnodeKISSCommandHashes,
			0x01,
			0xa1, 0xa2, 0xa3, 0xa4,
			0xa5, 0xa6, 0xa7, 0xa8,
			0xa9, 0xaa, 0xab, 0xac,
			0xad, 0xae, 0xaf, 0xb0,
			0xb1, 0xb2, 0xb3, 0xb4,
			0xb5, 0xb6, 0xb7, 0xb8,
			0xb9, 0xba, 0xbb, 0xbc,
			0xbd, 0xbe, 0xbf, kissFesc, kissTfend, kissFend,
			kissFend, rnodeKISSCommandHashes,
			0x02,
			0xc1, 0xc2, 0xc3, 0xc4,
			0xc5, 0xc6, 0xc7, 0xc8,
			0xc9, 0xca, 0xcb, 0xcc,
			0xcd, 0xce, 0xcf, 0xd0,
			0xd1, 0xd2, 0xd3, 0xd4,
			0xd5, 0xd6, 0xd7, 0xd8,
			0xd9, 0xda, kissFesc, kissTfesc, 0xdc, 0xdd, 0xde, 0xdf, 0xe0, kissFend,
		}...),
	}

	snapshot, err := captureRnodeHashes(serial, time.Second)
	if err != nil {
		t.Fatalf("captureRnodeHashes returned error: %v", err)
	}

	if len(serial.writes) != 1 {
		t.Fatalf("expected one detect write, got %v", len(serial.writes))
	}
	if !bytes.Equal(serial.writes[0], rnodeDetectCommand()) {
		t.Fatalf("detect command mismatch:\n got: %x\nwant: %x", serial.writes[0], rnodeDetectCommand())
	}

	if !bytes.Equal(snapshot.deviceHash, []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20}) {
		t.Fatalf("device hash mismatch: %x", snapshot.deviceHash)
	}
	if !bytes.Equal(snapshot.firmwareHashTarget, []byte{0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf, 0xc0}) {
		t.Fatalf("firmware target hash mismatch: %x", snapshot.firmwareHashTarget)
	}
	if !bytes.Equal(snapshot.firmwareHash, []byte{0xc1, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf, 0xd0, 0xd1, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf, 0xe0}) {
		t.Fatalf("firmware hash mismatch: %x", snapshot.firmwareHash)
	}
}

func TestCaptureRnodeHashesTimesOut(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{blockOnEmpty: true, wait: make(chan struct{})}
	_, err := captureRnodeHashes(serial, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err.Error() != "timed out while reading device hashes" {
		t.Fatalf("unexpected error: %v", err)
	}
}
