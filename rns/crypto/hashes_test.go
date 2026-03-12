// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"testing"
)

func TestSHA256(t *testing.T) {
	t.Parallel()
	data := []byte("hello world")
	got := SHA256(data)
	want := []byte{185, 77, 39, 185, 147, 77, 62, 8,
		165, 46, 82, 215, 218, 125, 171, 250,
		196, 132, 239, 227, 122, 83, 128, 238,
		144, 136, 247, 172, 226, 239, 205, 233}
	if !bytes.Equal(got, want) {
		t.Errorf("SHA256 = %v, want %v", got, want)
	}
}

func TestSHA512(t *testing.T) {
	t.Parallel()
	data := []byte("hello world")
	got := SHA512(data)
	want := []byte{48, 158, 204, 72, 156, 18, 214, 235,
		76, 196, 15, 80, 201, 2, 242, 180,
		208, 237, 119, 238, 81, 26, 124, 122,
		155, 205, 60, 168, 109, 76, 216, 111,
		152, 157, 211, 91, 197, 255, 73, 150,
		112, 218, 52, 37, 91, 69, 176, 207,
		216, 48, 232, 31, 96, 93, 207, 125,
		197, 84, 46, 147, 174, 156, 215, 111}
	if !bytes.Equal(got, want) {
		t.Errorf("SHA512 = %v, want %v", got, want)
	}
}
