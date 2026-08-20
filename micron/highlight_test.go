// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package micron

import (
	"strings"
	"testing"
)

// TestHighlightPlainFallback asserts that the Go highlighter's plain-text
// fallback path is byte-identical to Python's SyntaxHighlighter with
// Pygments unavailable. These cases have no language and no filename, so
// both runtimes take the plain fallback.
func TestHighlightPlainFallback(t *testing.T) {
	h := NewHighlighter()
	for _, tc := range goldenPlain {
		t.Run(tc.name, func(t *testing.T) {
			want := pythonHighlightPlain(t, tc.content, "", "")
			got, err := h.Highlight(tc.content, "", "")
			if err != nil {
				t.Fatalf("Highlight error: %v", err)
			}
			if got != want {
				t.Fatalf("Highlight plain fallback mismatch with live Python (Pygments off):\ncontent: %q\nwant:   %q\ngot:    %q",
					tc.content, want, got)
			}
		})
	}
}

// TestHighlightCodePlainFallback mirrors the package-level convenience
// function against the same plain-fallback fixtures.
func TestHighlightCodePlainFallback(t *testing.T) {
	for _, tc := range goldenPlain {
		t.Run(tc.name, func(t *testing.T) {
			want := pythonHighlightPlain(t, tc.content, "", "")
			got := HighlightCode(tc.content, "", "")
			if got != want {
				t.Fatalf("HighlightCode mismatch with live Python (Pygments off):\ncontent: %q\nwant:   %q\ngot:    %q",
					tc.content, want, got)
			}
		})
	}
}

// TestHighlightEmptyContent verifies the empty-content branch, which
// returns _plain_text without backslash doubling (mirroring Python's
// `if not content` guard).
func TestHighlightEmptyContent(t *testing.T) {
	h := NewHighlighter()
	got, err := h.Highlight("", "", "")
	if err != nil {
		t.Fatalf("Highlight error: %v", err)
	}
	if got != "`=\n\n`=" {
		t.Fatalf("got %q, want `=\\n\\n`=", got)
	}
}

// TestHighlightColoredColors asserts that the hand-written tokenisers emit
// every micron colour code that a live Pygments run emits for each fixture.
// The expected colour set is captured fresh from Python highlight_code (gated
// on Pygments availability); equality is structural (each `FT{color}` from the
// Pygments reference must appear in the Go output), not byte-for-byte, because
// hand tokenisation does not replicate Pygments' exact token boundaries.
func TestHighlightColoredColors(t *testing.T) {
	skipIfNoPygments(t)
	h := NewHighlighter()
	type expect struct {
		language string
		content  string
	}
	cases := []expect{
		{"python", "# a comment\n"},
		{"python", "x = \"hello\"\n"},
		{"python", "def foo(x):\n    return x\n"},
		{"python", "if True:\n    pass\n"},
		{"go", "func foo(x int) int {\n    return x\n}\n"},
		{"go", "s := \"hello\"\n"},
		{"bash", "# comment\n"},
		{"bash", "export FOO=bar\n"},
		{"env", "echo hi\n"},
	}
	for _, tc := range cases {
		t.Run(tc.language+":"+firstLine(tc.content), func(t *testing.T) {
			ref := pythonHighlightColored(t, tc.content, "", tc.language)
			wantColors := extractColors(ref)
			got, err := h.Highlight(tc.content, "", tc.language)
			if err != nil {
				t.Fatalf("Highlight error: %v", err)
			}
			if len(wantColors) == 0 {
				t.Fatalf("Pygments reference for %q produced no colours; expected a non-empty colour set", tc.content)
			}
			for c := range wantColors {
				if !strings.Contains(got, c) {
					t.Fatalf("Highlight(%q, lang=%q) = %q; missing live-Pygments color %q\nref: %q",
						tc.content, tc.language, got, c, ref)
				}
			}
		})
	}
}

// TestHighlightColoredRefStructure compares the Go hand-tokenised output
// against a live Pygments reference capture. It asserts that every colour
// code appearing in the Pygments reference also appears in the Go output
// (no missing colour kinds), while tolerating differences in token
// boundaries and span grouping.
func TestHighlightColoredRefStructure(t *testing.T) {
	skipIfNoPygments(t)
	h := NewHighlighter()
	for _, tc := range goldenColoredRef {
		t.Run(tc.name, func(t *testing.T) {
			ref := pythonHighlightColored(t, tc.content, "", tc.language)
			got, err := h.Highlight(tc.content, "", tc.language)
			if err != nil {
				t.Fatalf("Highlight error: %v", err)
			}
			refColors := extractColors(ref)
			gotColors := extractColors(got)
			for c := range refColors {
				if !gotColors[c] {
					t.Fatalf("Highlight(%q, lang=%q) = %q; missing live-Pygments color %q\nref: %q",
						tc.content, tc.language, got, c, ref)
				}
			}
		})
	}
}

// TestHighlightFilenameInference verifies that a recognised filename
// extension routes to the matching tokeniser (coloured output), while an
// unrecognised extension falls back to plain text.
func TestHighlightFilenameInference(t *testing.T) {
	h := NewHighlighter()
	t.Run("py_file", func(t *testing.T) {
		got, err := h.Highlight("# c\n", "foo.py", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(got, "`FT8b949e") {
			t.Fatalf("expected python comment color in %q", got)
		}
	})
	t.Run("unknown_file", func(t *testing.T) {
		got, err := h.Highlight("plain text\n", "foo.txt", "")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		want := "`=\nplain text\n\n`="
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

// TestHighlightRawmuPassthrough verifies that the "rawmu" language is
// passed through verbatim when a highlighter is configured on a
// converter. With a highlighter present, the rawmu branch appends the raw
// code content without any micron wrapping (mirroring Python's
// `result_lines.append(code_content)`).
func TestHighlightRawmuPassthrough(t *testing.T) {
	c := NewConverter(WithHighlighter(NewHighlighter()))
	got := c.FormatBlock("```rawmu\n`craw\n```")
	want := "`craw"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestHighlightWithHighlighter verifies that a converter with a
// highlighter produces coloured output for a fenced Go block, and that the
// result is wrapped in the code background/foreground sequences.
func TestHighlightWithHighlighter(t *testing.T) {
	c := NewConverter(WithHighlighter(NewHighlighter()))
	got := c.FormatBlock("```go\nfunc main() {}\n```")
	if !strings.HasPrefix(got, "`BT282828`Fddd\n") {
		t.Fatalf("missing code bg/fg prefix in %q", got)
	}
	if !strings.HasSuffix(got, "\n`f`b") {
		t.Fatalf("missing code reset suffix in %q", got)
	}
	if !strings.Contains(got, "`FTff7b72func`f") {
		t.Fatalf("missing highlighted keyword in %q", got)
	}
}

// extractColors returns the set of `FT{color}` sequences in s. All theme
// colours are 6-digit hex, so exactly six hex digits are read after `FT.
func extractColors(s string) map[string]bool {
	out := map[string]bool{}
	i := 0
	for {
		j := strings.Index(s[i:], "`FT")
		if j < 0 {
			break
		}
		start := i + j + 3
		end := start + 6
		if end <= len(s) {
			allHex := true
			for k := start; k < end; k++ {
				if !isHexByte(s[k]) {
					allHex = false
					break
				}
			}
			if allHex {
				out["`FT"+s[start:end]] = true
			}
		}
		i = start
	}
	return out
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}
