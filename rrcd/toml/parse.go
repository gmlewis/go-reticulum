// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package toml

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Parse reads a TOML document, keeping each line's raw text so Dump can
// reproduce it byte-for-byte. The supported subset covers the rrcd state
// files: [table] and [table."quoted key"] headers (dotted paths), bare and
// quoted keys, basic and literal strings, ints, floats, bools, string arrays,
// inline tables, comments, and blank lines.
func Parse(src string) (*Doc, error) {
	doc := &Doc{root: &Table{}}
	cur := doc.root
	var pending []string // comment/blank lines not yet attributed
	flushPending := func() {
		for _, p := range pending {
			cur.Keys = append(cur.Keys, KeyVal{
				Key:     "#comment",
				KeyRaw:  p,
				RawLine: p,
				IsRaw:   true,
			})
		}
		pending = nil
	}
	lines := splitLines(src)
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			pending = append(pending, raw)
		case strings.HasPrefix(trimmed, "["):
			hdr, err := parseHeader(trimmed)
			if err != nil {
				return nil, err
			}
			table := &Table{HeaderRaw: raw, Path: hdr, Prefix: pending}
			pending = nil
			// Intermediate super-tables are implicit in TOML.
			parent := ensureTable(doc.root, hdr[:len(hdr)-1])
			parent.Tables = append(parent.Tables, table)
			cur = table
		default:
			flushPending()
			kv, err := parseKeyValLine(raw)
			if err != nil {
				return nil, err
			}
			cur.Keys = append(cur.Keys, *kv)
		}
	}
	flushPending()
	return doc, nil
}

// splitLines splits src into lines, keeping line terminators.
func splitLines(src string) []string {
	if src == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			lines = append(lines, src[start:i+1])
			start = i + 1
		}
	}
	if start < len(src) {
		lines = append(lines, src[start:])
	}
	return lines
}

func endsWithNewlineLine(raw string) bool { return strings.HasSuffix(raw, "\n") }

// stripLineEnd removes the trailing newline and returns the comment part.
func parseHeader(trimmed string) ([]string, error) {
	// Trim a trailing comment: a '#' inside the brackets is part of a
	// quoted key, so only cut at a '#' outside quotes.
	body := trimmed
	if idx := commentIndexOutsideQuotes(body); idx >= 0 {
		body = strings.TrimSpace(body[:idx])
	}
	if !strings.HasPrefix(body, "[") || !strings.HasSuffix(body, "]") {
		return nil, fmt.Errorf("invalid table header: %v", trimmed)
	}
	inner := body[1 : len(body)-1]
	return splitKeyPath(inner)
}

// splitKeyPath splits a dotted key path, honoring quoted segments.
func splitKeyPath(inner string) ([]string, error) {
	var parts []string
	i := 0
	for {
		i = skipSpace(inner, i)
		if i >= len(inner) {
			return nil, fmt.Errorf("empty key segment")
		}
		if inner[i] == '"' || inner[i] == '\'' {
			s, next, err := parseQuotedSegment(inner, i)
			if err != nil {
				return nil, err
			}
			parts = append(parts, s)
			i = skipSpace(inner, next)
		} else {
			start := i
			for i < len(inner) && inner[i] != '.' && inner[i] != ' ' {
				i++
			}
			parts = append(parts, inner[start:i])
			i = skipSpace(inner, i)
		}
		if i >= len(inner) {
			break
		}
		if inner[i] != '.' {
			return nil, fmt.Errorf("invalid key path near %q", inner[i:])
		}
		i++ // consume the dot
	}
	return parts, nil
}

// parseQuotedSegment parses one quoted key segment starting at i, returning
// the value and the index just past the closing quote.
func parseQuotedSegment(s string, i int) (string, int, error) {
	quote := s[i]
	i++
	var sb strings.Builder
	for i < len(s) {
		c := s[i]
		if quote == '"' && c == '\\' && i+1 < len(s) {
			esc, n, err := decodeEscape(s[i+1:])
			if err != nil {
				return "", 0, err
			}
			sb.WriteRune(esc)
			i += 1 + n
			continue
		}
		if c == quote {
			return sb.String(), i + 1, nil
		}
		sb.WriteByte(c)
		i++
	}
	return "", 0, fmt.Errorf("unterminated quoted key")
}

func decodeEscape(s string) (rune, int, error) {
	if len(s) == 0 {
		return 0, 0, fmt.Errorf("dangling escape")
	}
	switch s[0] {
	case 'n':
		return '\n', 1, nil
	case 'r':
		return '\r', 1, nil
	case 't':
		return '\t', 1, nil
	case '"':
		return '"', 1, nil
	case '\\':
		return '\\', 1, nil
	case 'b':
		return '\b', 1, nil
	case 'f':
		return '\f', 1, nil
	case 'u':
		if len(s) >= 5 {
			r, ok := parseHex4(s[1:5])
			if ok {
				return r, 4, nil
			}
		}
	case 'U':
		if len(s) >= 9 {
			r, ok := parseHex8(s[1:9])
			if ok {
				return r, 9, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("invalid escape")
}

func parseHex4(s string) (rune, bool) {
	var v rune
	for i := 0; i < 4; i++ {
		d, ok := hexDigit(s[i])
		if !ok {
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

func parseHex8(s string) (rune, bool) {
	var v rune
	for i := 0; i < 8; i++ {
		d, ok := hexDigit(s[i])
		if !ok {
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

func hexDigit(c byte) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0'), true
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return rune(c-'A') + 10, true
	}
	return 0, false
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// commentIndexOutsideQuotes finds the first '#' outside quoted segments.
func commentIndexOutsideQuotes(s string) int {
	inBasic, inLiteral := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inBasic:
			if c == '\\' {
				i++
			} else if c == '"' {
				inBasic = false
			}
		case inLiteral:
			if c == '\'' {
				inLiteral = false
			}
		case c == '"':
			inBasic = true
		case c == '\'':
			inLiteral = true
		case c == '#':
			return i
		}
	}
	return -1
}

// parseKeyValLine parses one key = value line, returning the entry with raw
// text preserved.
func parseKeyValLine(raw string) (*KeyVal, error) {
	line := raw
	if endsWithNewlineLine(line) {
		line = line[:len(line)-1]
	}
	trimmed := strings.TrimSpace(line)
	comment := ""
	if idx := commentIndexOutsideQuotes(trimmed); idx >= 0 {
		comment = strings.TrimSpace(trimmed[idx:])
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	eq := indexEqualsOutsideQuotes(trimmed)
	if eq < 0 {
		return nil, fmt.Errorf("invalid key/value line: %v", raw)
	}
	keyPart := strings.TrimSpace(trimmed[:eq])
	valPart := strings.TrimSpace(trimmed[eq+1:])

	kv := &KeyVal{KeyRaw: keyPart, Comment: comment, RawLine: raw}
	keyPath, err := splitKeyPath(keyPart)
	if err != nil {
		return nil, err
	}
	if len(keyPath) != 1 {
		// Dotted key-values are accepted for parsing but keep their raw
		// form; the persistence paths only rewrite simple keys.
		kv.Key = strings.Join(keyPath, ".")
	} else {
		kv.Key = keyPath[0]
	}
	v, err := parseValue(valPart)
	if err != nil {
		return nil, fmt.Errorf("%w (line: %v)", err, raw)
	}
	kv.Value = v
	return kv, nil
}

// indexEqualsOutsideQuotes finds the key/value separator '='.
func indexEqualsOutsideQuotes(s string) int {
	inBasic, inLiteral := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inBasic:
			if c == '\\' {
				i++
			} else if c == '"' {
				inBasic = false
			}
		case inLiteral:
			if c == '\'' {
				inLiteral = false
			}
		case c == '"':
			inBasic = true
		case c == '\'':
			inLiteral = true
		case c == '=':
			return i
		}
	}
	return -1
}

// parseValue parses one TOML value from text.
func parseValue(text string) (Value, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Value{}, fmt.Errorf("empty value")
	}
	switch text[0] {
	case '"':
		s, err := parseBasicString(text)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindString, Str: s, Raw: text}, nil
	case '\'':
		s, err := parseLiteralString(text)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindString, Str: s, Raw: text, SingleQuoted: true}, nil
	case 't':
		if text == "true" {
			return Value{Kind: KindBool, Bool: true, Raw: text}, nil
		}
	case 'f':
		if text == "false" {
			return Value{Kind: KindBool, Bool: false, Raw: text}, nil
		}
	case '[':
		return parseArray(text)
	case '{':
		return parseInlineTable(text)
	}
	if n, err := parseIntValue(text); err == nil {
		return n, nil
	}
	if f, ok := parseFloatValue(text); ok {
		return f, nil
	}
	return Value{}, fmt.Errorf("unsupported value: %v", text)
}

// parseBasicString decodes a double-quoted (possibly multi-segment) string.
func parseBasicString(text string) (string, error) {
	if len(text) < 2 || text[0] != '"' {
		return "", fmt.Errorf("invalid basic string")
	}
	var sb strings.Builder
	i := 1
	for i < len(text) {
		c := text[i]
		if c == '"' {
			rest := strings.TrimSpace(text[i+1:])
			if rest != "" && rest[0] != '#' {
				return "", fmt.Errorf("garbage after basic string: %v", rest)
			}
			return sb.String(), nil
		}
		if c == '\\' && i+1 < len(text) {
			r, n, err := decodeEscape(text[i+1:])
			if err != nil {
				return "", err
			}
			sb.WriteRune(r)
			i += 1 + n
			continue
		}
		sb.WriteByte(c)
		i++
	}
	return "", fmt.Errorf("unterminated basic string")
}

// parseLiteralString parses a single-quoted literal string (no escapes).
func parseLiteralString(text string) (string, error) {
	if len(text) < 2 || text[0] != '\'' {
		return "", fmt.Errorf("invalid literal string")
	}
	end := strings.IndexByte(text[1:], '\'')
	if end < 0 {
		return "", fmt.Errorf("unterminated literal string")
	}
	rest := strings.TrimSpace(text[end+2:])
	if rest != "" && rest[0] != '#' {
		return "", fmt.Errorf("garbage after literal string: %v", rest)
	}
	return text[1 : end+1], nil
}

func parseIntValue(text string) (Value, error) {
	t := text
	neg := false
	if strings.HasPrefix(t, "+") {
		t = t[1:]
	} else if strings.HasPrefix(t, "-") {
		neg = true
		t = t[1:]
	}
	if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0o") || strings.HasPrefix(t, "0b") {
		var v int64
		var err error
		switch t[1] {
		case 'o':
			v, err = strconv.ParseInt(t[2:], 8, 64)
		case 'b':
			v, err = strconv.ParseInt(t[2:], 2, 64)
		default:
			v, err = strconv.ParseInt(t[2:], 16, 64)
		}
		if err != nil {
			return Value{}, fmt.Errorf("invalid integer: %v", text)
		}
		if neg {
			v = -v
		}
		return Value{Kind: KindInt, Int: v, Raw: text}, nil
	}
	t = strings.ReplaceAll(t, "_", "")
	v, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("invalid integer: %v", text)
	}
	if neg {
		v = -v
	}
	return Value{Kind: KindInt, Int: v, Raw: text}, nil
}

func parseFloatValue(text string) (Value, bool) {
	t := strings.ReplaceAll(text, "_", "")
	// TOML floats require a fractional/exponent part or inf/nan.
	lower := strings.ToLower(t)
	switch lower {
	case "inf", "+inf":
		return Value{Kind: KindFloat, Flt: math.Inf(1), Raw: text}, true
	case "-inf":
		return Value{Kind: KindFloat, Flt: math.Inf(-1), Raw: text}, true
	case "nan", "+nan", "-nan":
		return Value{Kind: KindFloat, Flt: math.NaN(), Raw: text}, true
	}
	if !strings.ContainsAny(t, ".eE") {
		return Value{}, false
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return Value{}, false
	}
	return Value{Kind: KindFloat, Flt: f, Raw: text}, true
}

// parseArray parses a single-line TOML array. Multi-line arrays are outside
// the supported subset (the rrcd files always write single-line arrays).
func parseArray(text string) (Value, error) {
	if !strings.HasSuffix(text, "]") {
		return Value{}, fmt.Errorf("unterminated array")
	}
	inner := text[1 : len(text)-1]
	var items []Value
	for {
		inner = strings.TrimLeft(inner, " \t")
		if inner == "" {
			break
		}
		if inner[0] == ',' || inner[0] == ']' {
			return Value{}, fmt.Errorf("malformed array")
		}
		item, rest, err := parseArrayItem(inner)
		if err != nil {
			return Value{}, err
		}
		items = append(items, item)
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			// The outer closing bracket was stripped, so an empty
			// remainder ends the array.
			break
		}
		if rest[0] == ',' {
			inner = rest[1:]
			continue
		}
		if rest[0] == ']' {
			rest = rest[1:]
		}
		if strings.TrimSpace(rest) != "" {
			return Value{}, fmt.Errorf("garbage after array item: %v", rest)
		}
		break
	}
	return Value{Kind: KindArray, Arr: items, Raw: text}, nil
}

// parseArrayItem parses one array element, returning the remainder of the
// array text after it.
func parseArrayItem(text string) (Value, string, error) {
	if text[0] == '[' {
		depth := 0
		for i := 0; i < len(text); i++ {
			switch text[i] {
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					v, err := parseArray(text[:i+1])
					return v, text[i+1:], err
				}
			}
		}
		return Value{}, "", fmt.Errorf("unterminated nested array")
	}
	if text[0] == '{' {
		depth := 0
		for i := 0; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					v, err := parseInlineTable(text[:i+1])
					return v, text[i+1:], err
				}
			}
		}
		return Value{}, "", fmt.Errorf("unterminated inline table")
	}
	if text[0] == '"' || text[0] == '\'' {
		if text[0] == '"' {
			i := 1
			for i < len(text) {
				if text[i] == '\\' {
					i += 2
					continue
				}
				if text[i] == '"' {
					break
				}
				i++
			}
			if i >= len(text) {
				return Value{}, "", fmt.Errorf("unterminated string in array")
			}
			v, err := parseValue(text[:i+1])
			return v, text[i+1:], err
		}
		end := strings.IndexByte(text[1:], '\'')
		if end < 0 {
			return Value{}, "", fmt.Errorf("unterminated literal string in array")
		}
		v, err := parseValue(text[:end+2])
		return v, text[end+2:], err
	}
	// Bare token: int, float, bool — up to a comma or closing bracket.
	end := 0
	for end < len(text) && text[end] != ',' && text[end] != ']' {
		end++
	}
	tok := strings.TrimSpace(text[:end])
	v, err := parseValue(tok)
	if err != nil {
		return Value{}, "", err
	}
	return v, text[end:], nil
}

// parseInlineTable parses a single-line inline table.
func parseInlineTable(text string) (Value, error) {
	if !strings.HasSuffix(text, "}") {
		return Value{}, fmt.Errorf("unterminated inline table")
	}
	inner := text[1 : len(text)-1]
	var entries []KeyVal
	for {
		inner = strings.TrimLeft(inner, " \t")
		if inner == "" {
			break
		}
		eq := indexEqualsOutsideQuotes(inner)
		if eq < 0 {
			return Value{}, fmt.Errorf("malformed inline table entry: %v", inner)
		}
		keyPart := strings.TrimSpace(inner[:eq])
		keyPath, err := splitKeyPath(keyPart)
		if err != nil {
			return Value{}, err
		}
		rest := strings.TrimLeft(inner[eq+1:], " \t")
		v, after, err := parseArrayItem(rest)
		if err != nil {
			return Value{}, err
		}
		key := keyPath[0]
		if len(keyPath) > 1 {
			key = strings.Join(keyPath, ".")
		}
		entries = append(entries, KeyVal{Key: key, Value: v})
		after = strings.TrimLeft(after, " \t")
		if after == "" {
			// The outer closing brace was stripped; empty remainder ends
			// the inline table.
			break
		}
		if after[0] == ',' {
			inner = after[1:]
			continue
		}
		if after[0] == '}' {
			after = after[1:]
		}
		if strings.TrimSpace(after) != "" {
			return Value{}, fmt.Errorf("garbage after inline table: %v", after)
		}
		break
	}
	return Value{Kind: KindInlineTable, Tbl: entries, Raw: text}, nil
}

// ensureTable walks the path, creating missing implicit super-tables.
func ensureTable(root *Table, path []string) *Table {
	cur := root
	for _, seg := range path {
		var next *Table
		for _, sub := range cur.Tables {
			if len(sub.Path) > 0 && sub.Path[len(sub.Path)-1] == seg {
				next = sub
				break
			}
		}
		if next == nil {
			next = &Table{Path: append(append([]string{}, path[:0]...), path...)}
			// Build the exact path prefix for the new super-table.
			next.Path = nil
			for _, s2 := range path {
				next.Path = append(next.Path, s2)
				if s2 == seg {
					break
				}
			}
			cur.Tables = append(cur.Tables, next)
		}
		cur = next
	}
	return cur
}

// findTable locates the table at the given path (creating nothing), scanning
// the tree in document order.
func findTable(root *Table, path []string) *Table {
	cur := root
	for _, seg := range path {
		var next *Table
		for _, sub := range cur.Tables {
			if len(sub.Path) > 0 && sub.Path[len(sub.Path)-1] == seg {
				next = sub
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}
