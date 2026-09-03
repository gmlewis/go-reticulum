// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package toml implements a minimal format-preserving TOML reader and writer
// for the rrcd state files. Parsing keeps the raw text of every line so
// documents round-trip byte-identically through Parse/Dump, while the edit
// API mutates values in place (existing keys keep their position; values
// re-render in tomlkit's default style) and appends new tables with the same
// blank-line separators tomlkit emits.
package toml

import (
	"strconv"
	"strings"
)

// Kind identifies the type of a parsed TOML value.
type Kind int

// Value kinds.
const (
	KindInt Kind = iota
	KindFloat
	KindString
	KindBool
	KindArray
	KindInlineTable
)

// Value is a parsed TOML value.
type Value struct {
	Kind Kind
	Int  int64
	Flt  float64
	Str  string
	Bool bool
	Arr  []Value
	Tbl  []KeyVal // inline-table entries
	Raw  string   // original raw text of the value

	SingleQuoted bool // string was written as a literal 'string'
}

// KeyVal is one key/value assignment line, preserving its raw text and any
// trailing comment for format-preserving rewrites.
type KeyVal struct {
	Key     string // logical (unquoted) key name
	KeyRaw  string // raw key text as written
	Value   Value
	Comment string // trailing comment text on the line, including its '#'
	RawLine string // the complete original line text, verbatim
	IsRaw   bool   // comment or blank line; RawLine is emitted verbatim
	Dirty   bool   // value changed by an edit; re-render on Dump
}

// Table is a TOML table: leading trivia, a header line, and the items
// inside it. An empty HeaderRaw is the implicit root table.
type Table struct {
	// Prefix holds the comment/blank lines that immediately precede this
	// table's header; like tomlkit, they belong to the table.
	Prefix []string
	// HeaderRaw is the raw header line text, empty for the root.
	HeaderRaw string
	Path      []string
	Keys      []KeyVal
	Tables    []*Table
	// Synthetic marks tables created by edits rather than parsed from the
	// file; synthetic empty parents with sub-tables are elided on dump.
	Synthetic bool
}

// Doc is a parsed TOML document.
type Doc struct {
	root *Table
}

// Root returns the document's root table.
func (d *Doc) Root() *Table { return d.root }

// Dump renders the document back to text, reusing raw text for untouched
// items and re-rendering only edited or new ones.
func (d *Doc) Dump() string {
	out := &dumper{}
	dumpTable(out, d.root)
	out.completeLine()
	return out.sb.String()
}

// dumper carries render state: the text so far and whether the last line
// still needs its line terminator.
type dumper struct {
	sb strings.Builder
	// lastUnterminated records that the last emitted line lacked its
	// terminator (only possible for the final line of the source text).
	lastUnterminated bool
}

func (d *dumper) completeLine() {
	if d.lastUnterminated {
		d.sb.WriteString("\n")
		d.lastUnterminated = false
	}
}

func (d *dumper) writeRawLine(line string) {
	d.completeLine()
	d.sb.WriteString(line)
	d.lastUnterminated = !endsWithNewline(line)
}

func dumpTable(d *dumper, t *Table) {
	if t == nil {
		return
	}
	for _, line := range t.Prefix {
		d.writeRawLine(line)
	}
	if t.HeaderRaw != "" && tableHeaderVisible(t) {
		d.writeRawLine(t.HeaderRaw)
	}
	for i := range t.Keys {
		kv := &t.Keys[i]
		switch {
		case kv.IsRaw:
			d.writeRawLine(kv.RawLine)
		case kv.Dirty:
			d.writeRenderedKeyVal(kv)
		default:
			// Untouched lines re-emit verbatim for byte-identical dumps.
			d.writeRawLine(kv.RawLine)
		}
	}
	for _, sub := range t.Tables {
		if sub.Synthetic {
			dumpSyntheticTable(d, t, sub)
			continue
		}
		dumpTable(d, sub)
	}
}

func (d *dumper) writeRenderedKeyVal(kv *KeyVal) {
	d.completeLine()
	d.sb.WriteString(renderKey(kv.Key))
	d.sb.WriteString(" = ")
	d.sb.WriteString(renderValue(kv.Value))
	if kv.Comment != "" {
		d.sb.WriteString(" ")
		d.sb.WriteString(kv.Comment)
	}
	d.sb.WriteString("\n")
}

// dumpSyntheticTable emits a table created by an edit, inserting the blank
// line tomlkit would insert before a new table header.
func dumpSyntheticTable(d *dumper, parent *Table, t *Table) {
	// A synthetic empty parent with sub-tables renders nothing for itself
	// (implicit super-table, like tomlkit).
	if len(t.Keys) == 0 && len(t.Tables) > 0 {
		for _, sub := range t.Tables {
			dumpSyntheticTable(d, t, sub)
		}
		return
	}
	d.writeSyntheticHeader(parent, t)
	for i := range t.Keys {
		kv := &t.Keys[i]
		switch {
		case kv.IsRaw:
			d.writeRawLine(kv.RawLine)
		default:
			d.writeRenderedKeyVal(kv)
		}
	}
	for _, sub := range t.Tables {
		dumpSyntheticTable(d, t, sub)
	}
}

// writeSyntheticHeader emits the header of an edited table, with the
// separator behavior captured from tomlkit: empty output → none;
// unterminated last line → complete the line; trailing blank line → none;
// first sub-table of an empty-bodied existing parent → none; otherwise one
// blank line.
func (d *dumper) writeSyntheticHeader(parent *Table, t *Table) {
	header := t.HeaderRaw
	if header == "" {
		header = renderTableHeader(t.Path)
		t.HeaderRaw = header
	}
	if syntheticHeaderSeparator(d.sb.String(), parent, t) {
		d.sb.WriteString("\n")
	}
	d.completeLine()
	d.sb.WriteString(header)
	d.sb.WriteString("\n")
}

// writeSyntheticHeader emits the header of an edited table, with the
// separator behavior captured from tomlkit: empty output → none;
// unterminated last line → complete the line; trailing blank line → none;
// first sub-table of an empty-bodied existing parent → none; otherwise one
// blank line.
func writeSyntheticHeader(sb *strings.Builder, parent *Table, t *Table) {
	header := t.HeaderRaw
	if header == "" {
		header = renderTableHeader(t.Path)
		t.HeaderRaw = header
	}
	soFar := sb.String()
	blank := syntheticHeaderSeparator(soFar, parent, t)
	if blank {
		sb.WriteString("\n")
	}
	if len(soFar) > 0 && !endsWithNewline(soFar) {
		sb.WriteString("\n")
	}
	sb.WriteString(header)
	sb.WriteString("\n")
}

// syntheticHeaderSeparator reports whether a blank line separates the new
// table header from the content rendered so far.
func syntheticHeaderSeparator(soFar string, parent *Table, t *Table) bool {
	if soFar == "" {
		return false
	}
	// An unterminated last line just gets its line terminator completed;
	// tomlkit adds no blank separator there.
	if !endsWithNewline(soFar) {
		return false
	}
	if strings.HasSuffix(soFar, "\n\n") {
		return false
	}
	// First child of an existing, empty-bodied parent follows its header
	// directly (tomlkit appends [rooms.new] right after "[rooms]\n").
	if parent != nil && !parent.Synthetic && parentBodyEmpty(parent) &&
		strings.HasSuffix(soFar, renderTableHeader(parent.Path)+"\n") {
		return false
	}
	return true
}

// tableHeaderVisible reports whether the header line renders: synthetic
// empty parents with sub-tables are elided.
func tableHeaderVisible(t *Table) bool {
	return !t.Synthetic || len(t.Keys) > 0 || len(t.Tables) == 0 || len(t.Prefix) > 0
}

// renderTableHeader renders "[path.segments]" with bare or quoted segments.
func renderTableHeader(path []string) string {
	parts := make([]string, len(path))
	for i, seg := range path {
		parts[i] = renderKey(seg)
	}
	return "[" + strings.Join(parts, ".") + "]"
}

// writeRenderedKeyVal emits an edited or newly created key/value line.
func writeRenderedKeyVal(sb *strings.Builder, kv *KeyVal) {
	sb.WriteString(renderKey(kv.Key))
	sb.WriteString(" = ")
	sb.WriteString(renderValue(kv.Value))
	if kv.Comment != "" {
		sb.WriteString(" ")
		sb.WriteString(kv.Comment)
	}
	sb.WriteString("\n")
}

// renderKey quotes key unless it is a legal bare key (digits-only keys stay
// bare, matching tomlkit).
func renderKey(key string) string {
	if bareKeyOK(key) {
		return key
	}
	return quoteBasicString(key)
}

// bareKeyOK reports whether key may be written as a TOML bare key.
func bareKeyOK(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// quoteBasicString renders s as a TOML basic (double-quoted) string.
func quoteBasicString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// renderValue renders an edited value in tomlkit's default styles: lists
// with a space after each comma, inline tables space-padded, floats in
// Python repr form, and strings double-quoted.
func renderValue(v Value) string {
	switch v.Kind {
	case KindString:
		if v.SingleQuoted && !strings.Contains(v.Str, "'") {
			return "'" + v.Str + "'"
		}
		return quoteBasicString(v.Str)
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindFloat:
		return FormatFloat(v.Flt)
	case KindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case KindArray:
		parts := make([]string, len(v.Arr))
		for i, item := range v.Arr {
			parts[i] = renderValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case KindInlineTable:
		parts := make([]string, len(v.Tbl))
		for i, e := range v.Tbl {
			parts[i] = renderKey(e.Key) + " = " + renderValue(e.Value)
		}
		if len(parts) == 0 {
			return "{}"
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return v.Raw
}

// parentBodyEmpty reports whether a table has no own key/comment/blank
// entries.
func parentBodyEmpty(t *Table) bool {
	for i := range t.Keys {
		if !t.Keys[i].IsRaw {
			return false
		}
		// A comment line inside the body counts as content.
		trimmed := strings.TrimSpace(t.Keys[i].RawLine)
		if strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}

func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}
