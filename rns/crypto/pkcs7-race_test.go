// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"sync"
	"testing"
)

// TestPKCS7PadDoesNotMutateInput pins that padding never writes into the
// caller's slice backing array. append(data, pad…) reuses spare capacity
// and races under concurrent Encrypt (CI: WARNING: DATA RACE in PKCS7Pad
// during concurrent Link.Teardown).
func TestPKCS7PadDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	// Spare capacity so a naive append would write past len(data).
	base := make([]byte, 32)
	for i := range 8 {
		base[i] = byte(i)
	}
	input := base[:8]
	orig := append([]byte(nil), input...)

	got := PKCS7Pad(input, 16)
	if !bytes.Equal(input, orig) {
		t.Errorf("PKCS7Pad mutated input: got %v, want %v", input, orig)
	}
	if len(got) != 16 {
		t.Fatalf("padded len = %v, want 16", len(got))
	}
	// The result must not alias the input's spare capacity.
	if &got[8] == &base[8] {
		t.Error("PKCS7Pad result aliases input spare capacity")
	}
}

// TestPKCS7PadConcurrent runs concurrent pads over slices that share a
// backing array with spare capacity — the race the CI detector caught.
func TestPKCS7PadConcurrent(t *testing.T) {
	t.Parallel()

	shared := make([]byte, 0, 64)
	shared = append(shared, make([]byte, 20)...)

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			// Each call must not write into shared's spare capacity.
			_ = PKCS7Pad(shared, 16)
		})
	}
	wg.Wait()
}
