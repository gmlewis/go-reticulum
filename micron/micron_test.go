// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package micron

import (
	"strings"
	"testing"
)

// TestConvertMarkdownToMicronGolden asserts that ConvertMarkdownToMicron
// produces byte-identical output to Python's convert_markdown_to_micron
// for every fixture in goldenConvert. These fixtures cover headers h1-h6,
// paragraphs, ordered/unordered/nested lists, fenced code blocks (plain,
// language-tagged, rawmu, unclosed), inline code, links (relative,
// external, anchor), images, blockquotes (single, multi-line, wrapping),
// tables (simple, aligned, no-edge-pipes, long-cell), bold/italic
// (asterisk and underscore), horizontal rules, leading-dash/lt escaping,
// backslash doubling and mixed documents.
func TestConvertMarkdownToMicronGolden(t *testing.T) {
	for _, tc := range goldenConvert {
		t.Run(tc.name, func(t *testing.T) {
			got := ConvertMarkdownToMicron(tc.input)
			if got != tc.want {
				t.Fatalf("ConvertMarkdownToMicron mismatch:\ninput:  %q\nwant:   %q\ngot:    %q",
					tc.input, tc.want, got)
			}
		})
	}
}

// TestConverterFormatBlock mirrors TestConvertMarkdownToMicronGolden but
// exercises the Converter type directly with default options.
func TestConverterFormatBlock(t *testing.T) {
	c := NewConverter()
	for _, tc := range goldenConvert {
		t.Run(tc.name, func(t *testing.T) {
			got := c.FormatBlock(tc.input)
			if got != tc.want {
				t.Fatalf("FormatBlock mismatch:\ninput:  %q\nwant:   %q\ngot:    %q",
					tc.input, tc.want, got)
			}
		})
	}
}

// TestConverterOptions verifies that options propagate to the converter.
func TestConverterOptions(t *testing.T) {
	t.Run("WithURLScope", func(t *testing.T) {
		c := NewConverter(WithURLScope(":/x/"))
		got := c.FormatBlock("[t](dest)")
		want := "`_`!`[t`:/x/dest]`!`_"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("WithMaxWidth", func(t *testing.T) {
		c := NewConverter(WithMaxWidth(20))
		// Quote wrapping uses max_width-3 = 17.
		got := c.FormatBlock("> a b c d e f g h i j k l m n o p q r s t u v w x y z")
		if !strings.Contains(got, "\n │ ") {
			t.Fatalf("expected wrapped quote lines in %q", got)
		}
	})
	t.Run("WithLinkColor3", func(t *testing.T) {
		c := NewConverter(WithLinkColor("abc"))
		got := c.FormatBlock("[t](d)")
		want := "`Fabc`_`!`[t`:/page/d]`!`_`f"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("WithLinkColor6", func(t *testing.T) {
		c := NewConverter(WithLinkColor("aabbcc"))
		got := c.FormatBlock("[t](d)")
		want := "`FTaabbcc`_`!`[t`:/page/d]`!`_`f"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("BoldLinksFalse", func(t *testing.T) {
		c := NewConverter()
		c.BoldLinks = false
		got := c.FormatBlock("[t](d)")
		want := "`_`[t`:/page/d]`_"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

// TestFormatLineLeadingEscapes checks the leading-dash and leading-lt
// escaping rules in FormatLine.
func TestFormatLineLeadingEscapes(t *testing.T) {
	c := NewConverter()
	cases := []struct{ in, want string }{
		{"-not a list", "\\-not a list"},
		{"<tag>", "\\<tag>"},
		{"- list", " • list"},
		{"---", "-"},
		{"normal", "normal"},
	}
	for _, tc := range cases {
		got := c.FormatLine(tc.in)
		if got != tc.want {
			t.Fatalf("FormatLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseTableRow covers the markdown table-row splitter.
func TestParseTableRow(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"| a | b |", []string{"a", "b"}},
		{"a | b", []string{"a", "b"}},
		{"|a|b|", []string{"a", "b"}},
		{"| a \\| b | c |", []string{"a | b", "c"}},
	}
	for _, tc := range cases {
		got := parseTableRow(tc.in)
		if !sliceEq(got, tc.want) {
			t.Fatalf("parseTableRow(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestParseTableAlignments covers alignment derivation.
func TestParseTableAlignments(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"| --- | --- |", []string{"left", "left"}},
		{"| :--- | :---: | ---: |", []string{"left", "center", "right"}},
	}
	for _, tc := range cases {
		got := parseTableAlignments(tc.in)
		if !sliceEq(got, tc.want) {
			t.Fatalf("parseTableAlignments(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
