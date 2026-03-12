// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"testing"
)

func TestHKDF(t *testing.T) {
	t.Parallel()
	deriveFrom := []byte("key material")
	salt := []byte("salt")
	context := []byte("context")
	length := 64

	got, err := HKDF(length, deriveFrom, salt, context)
	if err != nil {
		t.Fatal(err)
	}

	want := []byte{242, 87, 219, 5, 164, 56, 107, 16,
		243, 252, 76, 33, 233, 192, 68, 47,
		84, 162, 125, 251, 92, 72, 75, 104,
		144, 20, 137, 28, 19, 75, 45, 118,
		117, 105, 254, 40, 118, 244, 62, 27,
		96, 118, 21, 69, 208, 119, 29, 5,
		17, 56, 255, 209, 75, 67, 225, 38,
		69, 210, 18, 200, 59, 102, 18, 102}
	if !bytes.Equal(got, want) {
		t.Errorf("HKDF = %v, want %v", got, want)
	}
}
