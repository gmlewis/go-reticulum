// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func TestParseFirmwareReleaseInfo(t *testing.T) {
	t.Parallel()

	version, hash, err := parseFirmwareReleaseInfo([]byte("2.1.0 deadbeef"))
	if err != nil {
		t.Fatalf("parseFirmwareReleaseInfo returned error: %v", err)
	}
	if version != "2.1.0" || hash != "deadbeef" {
		t.Fatalf("unexpected release info: %q %q", version, hash)
	}
}

func TestParseFirmwareReleaseInfoMalformed(t *testing.T) {
	t.Parallel()

	if _, _, err := parseFirmwareReleaseInfo([]byte("only-version")); err == nil {
		t.Fatal("expected parseFirmwareReleaseInfo to fail")
	}
}
