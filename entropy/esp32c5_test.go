// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"bytes"
	"errors"
	"testing"
)

func TestESP32C5Source_Name(t *testing.T) {
	t.Parallel()

	s := NewESP32C5Source(func() (uint32, error) { return 0, nil })
	if got := s.Name(); got != "esp32c5-sar-adc" {
		t.Errorf("Name() = %q, want %q", got, "esp32c5-sar-adc")
	}
}

func TestESP32C5Source_Read(t *testing.T) {
	t.Parallel()

	var counter uint32
	words := []uint32{0x04030201, 0x08070605, 0x0c0b0a09}
	reader := func() (uint32, error) {
		if int(counter) >= len(words) {
			return 0, errors.New("out of words")
		}
		w := words[counter]
		counter++
		return w, nil
	}

	s := NewESP32C5Source(reader)

	// Read 5 bytes (unaligned across 32-bit words).
	buf1 := make([]byte, 5)
	n1, err := s.Read(buf1)
	if err != nil {
		t.Fatalf("Read(5) error = %v", err)
	}
	if n1 != 5 {
		t.Errorf("Read(5) n = %v, want 5", n1)
	}
	want1 := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	if !bytes.Equal(buf1, want1) {
		t.Errorf("Read(5) got %x, want %x", buf1, want1)
	}

	// Read next 3 bytes (should drain remaining 3 bytes of word 2).
	buf2 := make([]byte, 3)
	n2, err := s.Read(buf2)
	if err != nil {
		t.Fatalf("Read(3) error = %v", err)
	}
	if n2 != 3 {
		t.Errorf("Read(3) n = %v, want 3", n2)
	}
	want2 := []byte{0x06, 0x07, 0x08}
	if !bytes.Equal(buf2, want2) {
		t.Errorf("Read(3) got %x, want %x", buf2, want2)
	}
}

func TestESP32C5Source_ErrorPropagation(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("rng peripheral bus error")
	s := NewESP32C5Source(func() (uint32, error) {
		return 0, wantErr
	})

	buf := make([]byte, 16)
	_, err := s.Read(buf)
	if !errors.Is(err, wantErr) {
		t.Errorf("Read() error = %v, want %v", err, wantErr)
	}
}

func TestESP32C5Source_GateIntegration(t *testing.T) {
	t.Parallel()

	var state uint32 = 0x12345678
	// Simple LCG providing pseudo-random non-constant words for test
	lcg := func() (uint32, error) {
		state = state*1664525 + 1013904223
		return state, nil
	}

	s := NewESP32C5Source(lcg)
	gate, err := NewReader(s, Options{Salt: []byte("device-test")})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}

	out := make([]byte, 32)
	n, err := gate.Read(out)
	if err != nil {
		t.Fatalf("gate.Read() error = %v", err)
	}
	if n != 32 {
		t.Errorf("gate.Read() n = %v, want 32", n)
	}
}

func TestESP32C5Source_FailsClosedOnStuckRegister(t *testing.T) {
	t.Parallel()

	stuck := NewESP32C5Source(func() (uint32, error) {
		return 0xAAAAAAAA, nil
	})

	gate, err := NewReader(stuck, Options{Salt: []byte("device-test")})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}

	out := make([]byte, 32)
	_, err = gate.Read(out)
	if !errors.Is(err, ErrHealthTestFailed) {
		t.Errorf("gate.Read() with stuck register error = %v, want ErrHealthTestFailed", err)
	}
}
