// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"testing"
)

func TestChecksumInfoHashMatchesPythonBootstrapDigest(t *testing.T) {
	t.Parallel()

	got := checksumInfoHash(0x03, 0xa4, 0x35, 0x01020304, 0x05060708)
	want := []byte{0xd8, 0x15, 0xcd, 0xb2, 0x39, 0x02, 0x4a, 0x6b, 0x77, 0x9c, 0xc7, 0x07, 0xc9, 0xb0, 0xae, 0xe4}
	if !bytes.Equal(got, want) {
		t.Fatalf("checksum mismatch: got %x want %x", got, want)
	}
}
