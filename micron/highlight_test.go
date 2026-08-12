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
			got, err := h.Highlight(tc.content, "", "")
			if err != nil {
				t.Fatalf("Highlight error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Highlight plain fallback mismatch:\ncontent: %q\nwant:   %q\ngot:    %q",
					tc.content, tc.want, got)
			}
		})
	}
}

// TestHighlightCodePlainFallback mirrors the package-level convenience
// function against the same plain-fallback fixtures.
func TestHighlightCodePlainFallback(t *testing.T) {
	for _, tc := range goldenPlain {
		t.Run(tc.name, func(t *testing.T) {
			got := HighlightCode(tc.content, "", "")
			if got != tc.want {
				t.Fatalf("HighlightCode mismatch:\ncontent: %q\nwant:   %q\ngot:    %q",
					tc.content, tc.want, got)
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
// the same micron colour codes that Pygments emits for each token kind in
// the reference fixtures. Equality is structural (the expected `FT{color}`
// sequence must be present in the Go output), not byte-for-byte, because
// hand tokenisation does not replicate Pygments' exact token boundaries.
func TestHighlightColoredColors(t *testing.T) {
	h := NewHighlighter()
	type expect struct {
		language string
		content  string
		colors   []string // each must appear as `FT{color}` in the output
	}
	cases := []expect{
		{"python", "# a comment\n", []string{"`FT8b949e"}},
		{"python", "x = \"hello\"\n", []string{"`FTa5d6ff", "`FTff7b72", "`FTe6edf3"}},
		{"python", "def foo(x):\n    return x\n", []string{"`FTff7b72", "`FTd2a8ff", "`FTb4b4b4", "`FTe6edf3"}},
		{"python", "if True:\n    pass\n", []string{"`FTff7b72"}},
		{"go", "func foo(x int) int {\n    return x\n}\n", []string{"`FTff7b72", "`FTffa657", "`FTb4b4b4", "`FTe6edf3"}},
		{"go", "s := \"hello\"\n", []string{"`FTa5d6ff", "`FTff7b72", "`FTe6edf3"}},
		{"bash", "# comment\n", []string{"`FT8b949e"}},
		{"bash", "export FOO=bar\n", []string{"`FTffa657", "`FTff7b72", "`FTe6edf3"}},
		{"env", "echo hi\n", []string{"`FTffa657"}},
	}
	for _, tc := range cases {
		t.Run(tc.language+":"+firstLine(tc.content), func(t *testing.T) {
			got, err := h.Highlight(tc.content, "", tc.language)
			if err != nil {
				t.Fatalf("Highlight error: %v", err)
			}
			for _, c := range tc.colors {
				if !strings.Contains(got, c) {
					t.Fatalf("Highlight(%q, lang=%q) = %q; missing color %q",
						tc.content, tc.language, got, c)
				}
			}
		})
	}
}

// TestHighlightColoredRefStructure compares the Go hand-tokenised output
// against the Pygments reference fixtures. It asserts that every colour
// code appearing in the Pygments reference also appears in the Go output
// (no missing colour kinds), while tolerating differences in token
// boundaries and span grouping.
func TestHighlightColoredRefStructure(t *testing.T) {
	h := NewHighlighter()
	for _, tc := range goldenColoredRef {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.Highlight(tc.content, "", tc.language)
			if err != nil {
				t.Fatalf("Highlight error: %v", err)
			}
			refColors := extractColors(tc.ref)
			gotColors := extractColors(got)
			for c := range refColors {
				if !gotColors[c] {
					t.Fatalf("Highlight(%q, lang=%q) = %q; missing reference color %q\nref: %q",
						tc.content, tc.language, got, c, tc.ref)
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
