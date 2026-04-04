// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func TestVerifyFirmwareHashMatches(t *testing.T) {
	t.Parallel()

	contents := []byte("abc")
	if err := verifyFirmwareHash(contents, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"); err != nil {
		t.Fatalf("verifyFirmwareHash returned error: %v", err)
	}
}

func TestVerifyFirmwareHashMismatch(t *testing.T) {
	t.Parallel()

	err := verifyFirmwareHash([]byte("abc"), "000000")
	if err == nil {
		t.Fatal("expected verifyFirmwareHash to fail")
	}
	want := "Firmware hash"
	if got := err.Error(); len(got) == 0 || got[:len(want)] != want {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := err.Error(); !containsString(got, "Firmware corrupt. Try clearing the local firmware cache with: rnodeconf --clear-cache") {
		t.Fatalf("missing cache-clear hint: %v", err)
	}
}

func containsString(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexString(s, substr) >= 0)
}

func indexString(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
