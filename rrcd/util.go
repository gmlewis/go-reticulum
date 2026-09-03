// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ExpandPath expands $VARs first, then ~, matching Python's
// os.path.expanduser(os.path.expandvars(p)): $name and ${name} expand when
// the variable exists and stay literal otherwise.
func ExpandPath(p string) string {
	expanded := expandVars(p)
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return expanded
		}
		if expanded == "~" {
			return home
		}
		return home + expanded[1:]
	}
	return expanded
}

// expandVars expands $name and ${name} references, leaving malformed or
// non-existent references unchanged like Python's os.path.expandvars.
func expandVars(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '$' {
			sb.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			sb.WriteByte('$')
			break
		}
		if s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				sb.WriteByte('$')
				i++
				continue
			}
			name := s[i+2 : i+2+end]
			if v, ok := os.LookupEnv(name); ok {
				sb.WriteString(v)
			} else {
				sb.WriteString(s[i : i+2+end+1])
			}
			i += 2 + end + 1
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == '_' ||
			(s[j] >= 'a' && s[j] <= 'z') ||
			(s[j] >= 'A' && s[j] <= 'Z') ||
			(s[j] >= '0' && s[j] <= '9' && j > i+1)) {
			j++
		}
		if j > i+1 {
			name := s[i+1 : j]
			if v, ok := os.LookupEnv(name); ok {
				sb.WriteString(v)
			} else {
				sb.WriteString(s[i:j])
			}
			i = j
			continue
		}
		sb.WriteByte('$')
		i++
	}
	return sb.String()
}

// NormalizeNick validates and normalizes a nickname, mirroring Python
// normalize_nick: Unicode-trim, empty → invalid ("" mirrors Python's None),
// strict UTF-8, UTF-8 byte-length limit (limit <= 0 means unlimited), and
// rejection of \n, \r, or NUL.
func NormalizeNick(value string, maxBytes int) string {
	s := strings.TrimFunc(value, isUnicodeSpace)
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) {
		return ""
	}
	if maxBytes > 0 && len(s) > maxBytes {
		return ""
	}
	if strings.ContainsAny(s, "\n\r\x00") {
		return ""
	}
	return s
}

// isUnicodeSpace matches the characters Python's str.strip() removes:
// ASCII whitespace, the file/group/record/unit separators 0x1c-0x1f, and
// Unicode's White_Space property (which covers 0x85, 0xA0, and the Zs/Zl/Zp
// categories).
func isUnicodeSpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

// ParseIdentityHash parses a text identity hash the way Python's
// HubService._parse_identity_hash does: strip, lower, optional 0x prefix,
// all Unicode whitespace removed anywhere, then bytes.fromhex with a
// minimum length of 4 bytes. Errors read "invalid identity hash {text!r}:
// {e}" and "identity hash too short: {text!r}" with Python repr quoting.
func ParseIdentityHash(text string) ([]byte, error) {
	s := strings.ToLower(strings.TrimFunc(text, isUnicodeSpace))
	s = strings.TrimPrefix(s, "0x")
	// Python removes every whitespace codepoint anywhere in the string.
	s = strings.Map(func(r rune) rune {
		if isUnicodeSpace(r) {
			return -1
		}
		return r
	}, s)
	b, err := fromHexPython(s)
	if err != nil {
		return nil, fmt.Errorf("invalid identity hash %v: %v", pythonQuote(text), err)
	}
	if len(b) < 4 {
		return nil, fmt.Errorf("identity hash too short: %v", pythonQuote(text))
	}
	return b, nil
}

// fromHexPython decodes s the way Python 3.13's bytes.fromhex does: ASCII
// whitespace is skipped only while no hex nibble is pending; anything else
// that is not a hex digit (including underscores and non-ASCII whitespace)
// produces the "non-hexadecimal number found in fromhex() arg at position
// {i}" error, as does an odd number of nibbles.
func fromHexPython(s string) ([]byte, error) {
	const msg = "non-hexadecimal number found in fromhex() arg at position %v"
	out := make([]byte, 0, (len(s)+1)/2)
	nibbles := 0
	i := 0
	for i < len(s) {
		c := s[i]
		if isASCIISpaceByte(c) {
			if nibbles%2 != 0 {
				return nil, fmt.Errorf(msg, i)
			}
			for i < len(s) && isASCIISpaceByte(s[i]) {
				i++
			}
			continue
		}
		v, ok := hexNibbleByte(c)
		if !ok {
			return nil, fmt.Errorf(msg, i)
		}
		if nibbles%2 == 0 {
			out = append(out, v<<4)
		} else {
			out[len(out)-1] |= v
		}
		nibbles++
		i++
	}
	if nibbles%2 != 0 {
		return nil, fmt.Errorf(msg, i)
	}
	return out, nil
}

func isASCIISpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func hexNibbleByte(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// pythonQuote renders a string the way Python's repr does: single quotes
// unless the value contains a single quote and no double quote, with
// backslash escapes for control characters.
func pythonQuote(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = "\""
	}
	var sb strings.Builder
	sb.WriteString(quote)
	for _, r := range s {
		switch {
		case r == '\\':
			sb.WriteString("\\\\")
		case r == '\n':
			sb.WriteString("\\n")
		case r == '\r':
			sb.WriteString("\\r")
		case r == '\t':
			sb.WriteString("\\t")
		case r < 0x20:
			sb.WriteString(fmt.Sprintf("\\x%02x", r))
		case r == '\'' && quote == "'":
			sb.WriteString("\\'")
		case r == '"' && quote == "\"":
			sb.WriteString("\\\"")
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteString(quote)
	return sb.String()
}
