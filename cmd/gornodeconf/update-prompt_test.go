// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptUseExtractedFirmware(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := promptUseExtractedFirmware(&out, strings.NewReader("\n")); err != nil {
		t.Fatalf("promptUseExtractedFirmware returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Please note that this *only* works if you are") {
		t.Fatalf("missing extracted-firmware warning: %q", out.String())
	}
	if !strings.Contains(out.String(), "Hit enter to continue.") {
		t.Fatalf("missing continuation prompt: %q", out.String())
	}
}
