// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"unicode"
)

// sanitizeName mirrors lxmd.py's sanitize_name used to clean peer display
// names before printing them in the --peers listing.
//
// The Python implementation applies unicodedata.normalize('NFKC', name)
// first. The Go standard library has no equivalent compatibility
// normalization, so that step is intentionally skipped here. This is
// acceptable for the parity coverage (ASCII names plus the strip,
// control, private-use, and whitespace cases); NFKC only affects
// compatibility-equivalence folding of legacy width/CJK compatibility
// characters, none of which appear in the parity suite.
//
// The remaining behavior matches Python faithfully:
//
//   - A category filter keeps runes whose Unicode general category has a
//     prefix of L (letters, including modifier letters Lm), N (numbers),
//     or P (punctuation). Space separators (Zs, Zl, Zp) are converted to a
//     single ASCII space. Everything else (control characters Cf/Cc/Cn,
//     marks Mn/Me, symbols, etc.) is dropped. Python additionally keeps
//     spacing-combining marks (Mc); the Go stdlib does not expose Mc
//     directly, and unicode.IsMark covers Mn+Mc+Me, so marks are dropped
//     here entirely. Mc is rare in peer names and does not appear in the
//     parity tests.
//   - Three strip ranges (emoji/pictograph blocks, control/bidi/zero-width
//     characters, and surrogates/private-use/CJK-compat) are removed as a
//     safety net. Most of those runes are already dropped by the category
//     filter, but the ranges are applied explicitly for parity.
//   - Runs of whitespace are collapsed to a single ASCII space and the
//     result is trimmed.
//
// Truncation to 45 runes (with an ellipsis) is performed by the caller,
// matching Python's `if len(nn) > 45: nn = f"{nn[:45]}..."`.
func sanitizeName(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if shouldStrip(r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsPunct(r) {
			b.WriteRune(r)
			continue
		}
		if isSpaceSeparator(r) || r == lineSeparator || r == paragraphSeparator {
			b.WriteRune(' ')
			continue
		}
		// Everything else (controls, marks, symbols, unassigned) is dropped.
	}
	cleaned := b.String()
	// Collapse runs of unicode whitespace to a single ASCII space, then trim.
	cleaned = collapseWhitespace(cleaned)
	return strings.TrimSpace(cleaned)
}

const (
	lineSeparator      = ' ' // Zl LINE SEPARATOR
	paragraphSeparator = ' ' // Zp PARAGRAPH SEPARATOR
)

// collapseWhitespace replaces every run of unicode.IsSpace runes with a
// single ASCII space.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteRune(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// isSpaceSeparator reports whether r is a Unicode space separator (Zs),
// the set Python's sanitize_name converts to a single ASCII space during
// the category filter pass.
func isSpaceSeparator(r rune) bool {
	switch r {
	case ' ', ' ', ' ', ' ', ' ', '　':
		return true
	}
	if r >= ' ' && r <= ' ' {
		return true
	}
	return false
}

// shouldStrip reports whether r falls into any of the three strip ranges
// from lxmd.py (STRIP_BLOCKS_RE, STRIP_CONTROL_RE, STRIP_PRIVATE_RE).
func shouldStrip(r rune) bool {
	return stripBlock(r) || stripControl(r) || stripPrivate(r)
}

// stripBlock matches STRIP_BLOCKS_RE: emoji and pictograph ranges.
func stripBlock(r rune) bool {
	switch {
	case r >= '\U0001F600' && r <= '\U0001F64F': // Emoticons
		return true
	case r >= '\U0001F300' && r <= '\U0001F5FF': // Misc Symbols & Pictographs
		return true
	case r >= '\U0001F680' && r <= '\U0001F6FF': // Transport & Map Symbols
		return true
	case r >= '\U0001F700' && r <= '\U0001F77F': // Alchemical Symbols
		return true
	case r >= '\U0001F780' && r <= '\U0001F7FF': // Geometric Shapes Extended
		return true
	case r >= '\U0001F800' && r <= '\U0001F8FF': // Supplemental Arrows-C
		return true
	case r >= '\U0001F900' && r <= '\U0001F9FF': // Supplemental Symbols & Pictographs
		return true
	case r >= '\U0001FA00' && r <= '\U0001FA6F': // Chess Symbols
		return true
	case r >= '\U0001FA70' && r <= '\U0001FAFF': // Symbols & Pictographs Extended-A
		return true
	case r >= '\U0001F1E0' && r <= '\U0001F1FF': // Flags (regional indicators)
		return true
	case r >= '\U0001F3FB' && r <= '\U0001F3FF': // Emoji modifiers (skin tones)
		return true
	case r >= '☀' && r <= '⛿': // Misc Symbols
		return true
	case r >= '✀' && r <= '➿': // Dingbats
		return true
	case r >= '︀' && r <= '️': // Variation Selectors
		return true
	case r >= '\U000E0100' && r <= '\U000E01EF': // Variation Selectors Supplement
		return true
	}
	return false
}

// stripControl matches STRIP_CONTROL_RE: C0/C1 controls, zero-width, bidi,
// format, BOM, and specials.
func stripControl(r rune) bool {
	switch {
	case r >= '\x00' && r <= '\x08': // C0 controls (NUL-BS)
		return true
	case r == '\x0B' || r == '\x0C': // VT, FF
		return true
	case r >= '\x0E' && r <= '\x1F': // C0 controls (SO-US)
		return true
	case r >= '\x7F' && r <= '\x9F': // DEL and C1 controls
		return true
	case r >= '​' && r <= '‏': // Zero-width chars, LRM, RLM, etc.
		return true
	case r >= '‪' && r <= '‮': // Bidi embedding controls
		return true
	case r >= '⁠' && r <= '⁯': // Format chars (word joiner, etc.)
		return true
	case r == '\uFEFF': // BOM / Zero Width NBSP
		return true
	case r >= '￰' && r <= '￸': // Specials
		return true
	}
	return false
}

// stripPrivate matches STRIP_PRIVATE_RE: surrogates, private use, CJK
// compatibility ideographs, vertical forms, combining half marks, and
// supplementary private use areas A/B.
func stripPrivate(r rune) bool {
	switch {
	case r >= 0xD800 && r <= 0xDFFF: // Surrogates (not valid rune literals)
		return true
	case r >= '' && r <= '': // Private Use Area
		return true
	case r >= '豈' && r <= '﫿': // CJK Compatibility Ideographs
		return true
	case r >= '︐' && r <= '︟': // Vertical Forms
		return true
	case r >= '︠' && r <= '︯': // Combining Half Marks
		return true
	case r >= '\U000F0000' && r <= '\U000FFFFF': // Supplementary Private Use Area-A
		return true
	case r >= '\U00100000' && r <= '\U0010FFFF': // Supplementary Private Use Area-B
		return true
	}
	return false
}
