// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

func TestParseHashAcceptsValidHex(t *testing.T) {
	t.Parallel()

	got, err := parseHash("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("parseHash returned error: %v", err)
	}
	want := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	if !bytes.Equal(got, want) {
		t.Fatalf("hash mismatch: got %x want %x", got, want)
	}
}

func TestParseHashAcceptsUppercaseHex(t *testing.T) {
	t.Parallel()

	got, err := parseHash("00112233445566778899AABBCCDDEEFF")
	if err != nil {
		t.Fatalf("parseHash returned error: %v", err)
	}
	want := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	if !bytes.Equal(got, want) {
		t.Fatalf("hash mismatch: got %x want %x", got, want)
	}
}

func TestParseHashRejectsWrongLength(t *testing.T) {
	t.Parallel()

	if _, err := parseHash("0011"); err == nil || err.Error() != "Hash length is invalid, must be 32 hexadecimal characters (16 bytes)." {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseHashRejectsInvalidHex(t *testing.T) {
	t.Parallel()

	if _, err := parseHash("00112233445566778899aabbccddeefg"); err == nil || err.Error() != "Invalid hash entered. Check your input." {
		t.Fatalf("unexpected error: %v", err)
	}
}
