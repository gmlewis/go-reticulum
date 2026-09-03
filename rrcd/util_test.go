// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"os"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestNormalizeNick(t *testing.T) {
	t.Parallel()
	// Vectors captured from Python normalize_nick (rrcd/util.py).
	for _, tc := range []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{
		{"trim", "  alice  ", 32, "alice"},
		{"33 ASCII bytes rejected", "a23456789012345678901234567890123", 32, ""},
		{"32 ASCII bytes kept", "a2345678901234567890123456789012", 32,
			"a2345678901234567890123456789012"},
		{"17 é bytes rejected (34 bytes)", "ééééééééééééééééé", 32, ""},
		{"16 é bytes kept (32 bytes)", "éééééééééééééééé", 32, "éééééééééééééééé"},
		{"max_bytes 3 rejects 4-byte nick", "abcd", 3, ""},
		{"max_bytes 3 keeps 3 bytes", "abc", 3, "abc"},
		{"max_bytes 0 unlimited", "0123456789012345678901234567890123", 0,
			"0123456789012345678901234567890123"},
		{"negative limit unlimited", "0123456789012345678901234567890123", -1,
			"0123456789012345678901234567890123"},
		{"newline rejected", "ali\nce", 32, ""},
		{"carriage return rejected", "ali\rce", 32, ""},
		{"NUL rejected", "ali\x00ce", 32, ""},
		{"empty invalid", "", 32, ""},
		{"whitespace-only invalid", "   ", 32, ""},
		{"control char in middle kept if not CR/LF/NUL", "al\tce", 32, "al\tce"},
	} {
		if got := NormalizeNick(tc.in, tc.maxBytes); got != tc.want {
			t.Errorf("%v: NormalizeNick(%q, %v) = %q, want %q", tc.name, tc.in, tc.maxBytes, got, tc.want)
		}
	}
}

func TestNormalizeNickInvalidUTF8(t *testing.T) {
	t.Parallel()
	// A lone 0x80 byte is not valid UTF-8; Python raises UnicodeError in
	// strict encoding and returns None.
	if got := NormalizeNick("\x80", 32); got != "" {
		t.Errorf("NormalizeNick invalid UTF-8 = %q, want empty", got)
	}
}

func TestExpandPath(t *testing.T) {
	t.Setenv("RRCD_TEST_VAR", "/opt/var")
	home := testutils.TempDir(t, "expand-home-")
	t.Setenv("HOME", home)

	for _, tc := range []struct {
		in   string
		want string
	}{
		{"$RRCD_TEST_VAR/sub", "/opt/var/sub"},
		{"~", home},
		{"~/rrcd", home + "/rrcd"},
		{"~/x", home + "/x"},
		{"/plain/path", "/plain/path"},
		{"plain", "plain"},
		// Python's expandvars matches the longest name; an undefined
		// reference (name VARb) stays literal.
		{"a$RRCD_TEST_VARb", "a$RRCD_TEST_VARb"},
		{"a${RRCD_TEST_VAR}b", "a/opt/varb"},
	} {
		if got := ExpandPath(tc.in); got != tc.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExpandPathMissingVarStays(t *testing.T) {
	// Python's expandvars leaves an undefined $VAR literal in place.
	os.Unsetenv("RRCD_TEST_UNDEF")
	if got, want := ExpandPath("x$RRCD_TEST_UNDEFINEDb/y"), "x$RRCD_TEST_UNDEFINEDb/y"; got != want {
		t.Errorf("ExpandPath = %q, want %q", got, want)
	}
}
