// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package toml

import (
	"strings"
	"testing"
)

func TestParseMultiLineStringArray(t *testing.T) {
	t.Parallel()
	src := `[hub]
trusted_identities = [
  "0a8b370a62de4c5464b7ef7f56ff33c8",
  "96018dd4df4c2037c8ed603573efc746",
]
banned_identities = []
`
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	hub := doc.TablePath("hub")
	if hub == nil {
		t.Fatal("missing [hub]")
	}
	v, ok := hub.Get("trusted_identities")
	if !ok {
		t.Fatal("missing trusted_identities")
	}
	if v.Kind != KindArray || len(v.Arr) != 2 {
		t.Fatalf("arr = %+v, want 2 strings", v.Arr)
	}
	if v.Arr[0].Str != "0a8b370a62de4c5464b7ef7f56ff33c8" {
		t.Errorf("arr[0] = %q", v.Arr[0].Str)
	}
	if v.Arr[1].Str != "96018dd4df4c2037c8ed603573efc746" {
		t.Errorf("arr[1] = %q", v.Arr[1].Str)
	}
	out := doc.Dump()
	if !strings.Contains(out, "trusted_identities = [\n") {
		t.Errorf("Dump lost multi-line form:\n%v", out)
	}
}

func TestParseSingleLineArrayStillWorks(t *testing.T) {
	t.Parallel()
	doc, err := Parse("a = [\"x\", \"y\"]\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v, ok := doc.Root().Get("a")
	if !ok || len(v.Arr) != 2 {
		t.Fatalf("a = %+v ok=%v", v, ok)
	}
}

// TestParseMultiLineArrayTrailingComma is valid TOML (the comma may sit on
// the next line); the old single-line-only parser rejected it as garbage.
func TestParseMultiLineArrayTrailingComma(t *testing.T) {
	t.Parallel()
	doc, err := Parse("arr = [1, 2\n, 3]\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v, ok := doc.Root().Get("arr")
	if !ok || len(v.Arr) != 3 {
		t.Fatalf("arr = %+v ok=%v", v, ok)
	}
}
