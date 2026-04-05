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

func TestPromptManualFlashEntry(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := promptManualFlashEntry(&out, strings.NewReader("\n")); err != nil {
		t.Fatalf("promptManualFlashEntry returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Please put the board into flashing mode now, by holding the BOOT or PRG button,",
		"while momentarily pressing the RESET button. Then release the BOOT or PRG button.",
		"Hit enter when this is done.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing prompt text %q in %q", want, got)
		}
	}
}

func TestPromptManualFlashExit(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := promptManualFlashExit(&out, strings.NewReader("\n")); err != nil {
		t.Fatalf("promptManualFlashExit returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Please take the board out of flashing mode by momentarily pressing the RESET button.",
		"Hit enter when this is done.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing prompt text %q in %q", want, got)
		}
	}
}
