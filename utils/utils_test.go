// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package utils

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func normalizeOutput(text string) string {
	text = strings.NewReplacer("\r", " ", "\b", "").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

func TestErrHelp(t *testing.T) {
	t.Parallel()

	if ErrHelp == nil {
		t.Fatal("ErrHelp is nil")
	}
	if !errors.Is(ErrHelp, ErrHelp) {
		t.Fatal("ErrHelp does not compare equal to itself")
	}
	if got, want := ErrHelp.Error(), "help requested"; got != want {
		t.Fatalf("ErrHelp.Error() = %q, want %q", got, want)
	}
}

func TestPrintVersionLine(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	PrintVersion(&out, "gornprobe", "0.1.0")
	if got, want := out.String(), "gornprobe 0.1.0\n"; got != want {
		t.Fatalf("PrintVersion output = %q, want %q", got, want)
	}
}

func TestNormalizeOutput(t *testing.T) {
	t.Parallel()

	got := normalizeOutput("gornprobe\r\n\b  hello\nworld\t  42")
	if got != "gornprobe hello world 42" {
		t.Fatalf("NormalizeOutput = %q", got)
	}
}

func TestNewFlagSetInvokesUsageOnHelp(t *testing.T) {
	t.Parallel()

	var called bool
	fs := NewFlagSet("gornprobe", func() {
		called = true
	})
	if err := fs.Parse([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("Parse returned %v, want flag.ErrHelp", err)
	}
	if !called {
		t.Fatal("usage function was not called")
	}
}

func TestNormalizeOutputTrimsWhitespace(t *testing.T) {
	t.Parallel()

	if got := normalizeOutput("   multiple    spaces\n\n  preserved   "); got != "multiple spaces preserved" {
		t.Fatalf("NormalizeOutput = %q", got)
	}
}

func TestWriteText(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	WriteText(&out, strings.Repeat("x", 3))
	if got, want := out.String(), "xxx"; got != want {
		t.Fatalf("WriteText output = %q, want %q", got, want)
	}
}
