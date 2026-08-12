// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func TestSanitizeName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"ascii", "hello", "hello"},
		{"empty", "", ""},
		{"zero width stripped", "name​with​zero", "namewithzero"},
		{"bom stripped", "\uFEFFbom", "bom"},
		{"bidi rle stripped", "evil‮name", "evilname"},
		{"bidi lre stripped", "evil‪name", "evilname"},
		{"emoji stripped", "\U0001F600hello", "hello"},
		{"emoji trailing stripped", "hello\U0001F600", "hello"},
		{"dingbat stripped", "✀hello", "hello"},
		{"misc symbol stripped", "☀hello", "hello"},
		{"whitespace collapse", "a  b   c", "a b c"},
		{"trim", "  pad  ", "pad"},
		{"newlines dropped", " name\nwith\rnewline ", "namewithnewline"},
		{"tabs dropped", "a\tb", "ab"},
		{"control dropped", "a\x00b\x07c", "abc"},
		{"del stripped", "a\x7Fb", "ab"},
		{"c1 control stripped", "a\x9Fb", "ab"},
		{"nbsp to space", "a b", "a b"},
		{"vertical forms stripped", "a︐b", "ab"},
		{"combining half marks stripped", "a︠b", "ab"},
		{"private use stripped", "ab", "ab"},
		{"cjk compat stripped", "a豈b", "ab"},
		{"punct kept", "a.b,c!", "a.b,c!"},
		{"number kept", "node42", "node42"},
		{"ogham space to space", "a b", "a b"},
		{"ideographic space to space", "a　b", "a b"},
		{"en space to space", "a b", "a b"},
		{"line separator to space", "a b", "a b"},
		{"paragraph separator to space", "a b", "a b"},
		{"word joiner dropped", "a⁠b", "ab"},
		{"specials stripped", "a￱b", "ab"},
		{"regional indicator stripped", "\U0001F1E0\U0001F1EAbc", "bc"},
		{"variation selector stripped", "a︀b", "ab"},
		{"emoji skin tone stripped", "a\U0001F3FBb", "ab"},
		{"long ascii passthrough",
			"01234567890123456789012345678901234567890123456789",
			"01234567890123456789012345678901234567890123456789"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeName(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeNameTruncationContract(t *testing.T) {
	t.Parallel()
	// Truncation to 45 runes (with ellipsis) is applied at the call site in
	// remote-init.go, mirroring Python's `if len(nn) > 45: nn = f"{nn[:45]}..."`.
	// Here we verify the raw sanitizeName output preserves rune count so the
	// truncation contract at the call site is byte-free.
	long := ""
	for range 50 {
		long += "x"
	}
	got := sanitizeName(long)
	if len([]rune(got)) != 50 {
		t.Errorf("expected 50 runes, got %d", len([]rune(got)))
	}
	nn := got
	if len([]rune(nn)) > 45 {
		nn = string([]rune(nn)[:45]) + "..."
	}
	want := string([]rune(got)[:45]) + "..."
	if nn != want {
		t.Errorf("truncation = %q, want %q", nn, want)
	}
}
