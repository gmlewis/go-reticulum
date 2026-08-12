// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package micron converts markdown to the micron rich-text format and
// provides syntax highlighting for code blocks. It is a Go port of the
// Python RNS.Utilities.rngit MarkdownToMicron converter (util.py) and
// SyntaxHighlighter (highlight.py), designed to produce byte-comparable
// micron output to the Python converter for the same markdown input when
// no syntax highlighter is configured.
//
// Micron is a lightweight rich-text format used by Reticulum terminals.
// It encodes formatting (bold, italic, underline), colours
// (foreground/background), alignment, links, literal blocks and box-drawn
// tables using backtick-prefixed control sequences.
//
// The package-level convenience function ConvertMarkdownToMicron mirrors
// Python's convert_markdown_to_micron. A Converter can be constructed via
// NewConverter for control over max width, the syntax highlighter and the
// URL scope used to resolve relative links.
package micron

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Micron control-byte sequences used by the converter. These mirror the
// constants defined in Python's MarkdownToMicron class (util.py).
const (
	// Bold is the micron bold toggle.
	Bold = "`!"
	// BoldEnd is the micron bold toggle (same sequence toggles off).
	BoldEnd = "`!"
	// Italic is the micron italic toggle.
	Italic = "`*"
	// ItalicEnd is the micron italic toggle (same sequence toggles off).
	ItalicEnd = "`*"
	// Underline is the micron underline toggle.
	Underline = "`_"
	// UnderlineEnd is the micron underline toggle (same sequence toggles off).
	UnderlineEnd = "`_"

	// CodeBG is the background colour sequence for fenced code blocks.
	CodeBG = "`BT282828"
	// CodeBGInline is the background colour sequence for inline code.
	CodeBGInline = "`BT383838"
	// CodeFG is the foreground colour sequence for code blocks.
	CodeFG = "`Fddd"
	// CodeReset closes foreground and background colour tags for code.
	CodeReset = "`f`b"

	// LiteralStart opens a micron literal block (raw, unformatted text).
	LiteralStart = "`="
	// LiteralEnd closes a micron literal block.
	LiteralEnd = "`="

	// Bullet is the bullet glyph used for unordered list items.
	Bullet = "•"
)

// Table box-drawing glyphs used when rendering markdown tables.
const (
	TableH  = "─"
	TableV  = "│"
	TableTL = "┌"
	TableTR = "┐"
	TableBL = "└"
	TableBR = "┘"
	TableML = "├"
	TableMR = "┤"
	TableTM = "┬"
	TableBM = "┴"
	TableMM = "┼"

	// TableMinColWidth is the minimum column width applied when rendering
	// tables.
	TableMinColWidth = 3
)

// DefaultMaxWidth is the default rendering width used by NewConverter when
// no MaxWidth option is supplied. It mirrors Python's default of 100.
const DefaultMaxWidth = 100

// DefaultURLScope is the default local URL scope used to resolve relative
// links when no URLScope option is supplied. It mirrors Python's ":/page/".
const DefaultURLScope = ":/page/"

// Regex patterns for markdown elements. These mirror the Python
// MarkdownToMicron class patterns (util.py lines ~97-114). All patterns are
// RE2-safe; Go's regexp engine handles them without deviation.
var (
	headerRE         = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	codeFenceRE      = regexp.MustCompile("^(\\s*)```(.*)$")
	horizontalRuleRE = regexp.MustCompile(`^(\s*)(---+|===+|\*\*\*+|___+)\s*$`)
	unorderedListRE  = regexp.MustCompile(`^(\s*)([-*+])\s+(.+)$`)

	tableRowRE = regexp.MustCompile(`^\s*\|?(.+?)\|?\s*$`)
	tableSepRE = regexp.MustCompile(`^\s*\|?(?:\s*:?-+:?\s*\|)+\s*$`)

	quoteRE = regexp.MustCompile(`^>\s?(.*)$`)

	linkRE       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	inlineCodeRE = regexp.MustCompile("`([^`]+)`")
	boldRE       = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	italicRE     = regexp.MustCompile(`\*(.+?)\*|_(.+?)_`)

	linkPlaceholderRE = regexp.MustCompile("\x00LINK(\\d+)\x00")
	codePlaceholderRE = regexp.MustCompile("\x00CODE(\\d+)\x00")

	// Visible-width stripping patterns (order matters: `f`b before `f).
	visFBB3 = regexp.MustCompile("`" + `[FB][0-9a-fA-F]{3}`)
	visFBT6 = regexp.MustCompile("`" + `[FB]T[0-9a-fA-F]{6}`)
	visFmt  = regexp.MustCompile("`" + `[!*_=]`)
	visFB   = regexp.MustCompile("`" + `f` + "`" + `b`)
	visF    = regexp.MustCompile("`" + `f`)
	visB    = regexp.MustCompile("`" + `b`)
)

// Converter renders markdown text to the micron rich-text format. It is a
// port of Python's MarkdownToMicron class.
type Converter struct {
	// MaxWidth is the rendering width used for wrapping quote text and
	// constraining table column widths.
	MaxWidth int
	// LocalURLScope is the scope prefixed onto relative link URLs.
	LocalURLScope string
	// Highlighter, when non-nil, is used to syntax-highlight fenced code
	// blocks that declare a language. When nil, fenced code blocks are
	// rendered as literal blocks.
	Highlighter *Highlighter

	// BoldLinks controls whether link text is rendered bold.
	BoldLinks bool
	// UnderlineLinks controls whether link text is rendered underlined.
	UnderlineLinks bool
	// LinkColor, when set to a 3- or 6-digit hex colour, applies a
	// foreground colour to link text. A 3-digit value uses the `F form
	// while a 6-digit value uses the `FT form.
	LinkColor string
}

// Option configures a Converter at construction time.
type Option func(*Converter)

// WithMaxWidth sets the rendering width.
func WithMaxWidth(w int) Option { return func(c *Converter) { c.MaxWidth = w } }

// WithURLScope sets the local URL scope used to resolve relative links.
func WithURLScope(scope string) Option { return func(c *Converter) { c.LocalURLScope = scope } }

// WithHighlighter sets the syntax highlighter used for fenced code blocks.
func WithHighlighter(h *Highlighter) Option { return func(c *Converter) { c.Highlighter = h } }

// WithLinkColor sets a foreground colour applied to link text. Pass a
// 3-digit or 6-digit hex colour string.
func WithLinkColor(color string) Option { return func(c *Converter) { c.LinkColor = color } }

// NewConverter returns a Converter configured with the given options. It
// mirrors the defaults of Python's MarkdownToMicron: max width 100, URL
// scope ":/page/", bold and underlined links, no link colour and no
// syntax highlighter.
func NewConverter(opts ...Option) *Converter {
	c := &Converter{
		MaxWidth:       DefaultMaxWidth,
		LocalURLScope:  DefaultURLScope,
		BoldLinks:      true,
		UnderlineLinks: true,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// SetURLScope overrides the local URL scope, mirroring Python's
// set_url_scope.
func (c *Converter) SetURLScope(scope string) { c.LocalURLScope = scope }

// FormatBlock converts a markdown document to micron. It is the main
// entry point and mirrors Python's MarkdownToMicron.format_block. The
// output is byte-comparable to the Python converter for the same input
// when no syntax highlighter is configured.
func (c *Converter) FormatBlock(text string) string {
	lines := strings.Split(text, "\n")
	st := &blockState{c: c}
	for _, line := range lines {
		isFence, langHint := c.detectCodeFence(line)
		if isFence {
			st.flushQuote()
			st.flushTable()
			if !st.inCodeBlock {
				st.inCodeBlock = true
				if langHint != "" {
					st.codeLang = strings.TrimSpace(langHint)
				} else {
					st.codeLang = ""
				}
				st.codeBuffer = nil
			} else {
				st.flushCode()
				st.inCodeBlock = false
				st.codeLang = ""
			}
			continue
		}
		if st.inCodeBlock {
			st.codeBuffer = append(st.codeBuffer, line)
			continue
		}
		if m := quoteRE.FindStringSubmatch(line); m != nil {
			if !st.inQuote {
				st.flushTable()
				st.inQuote = true
				st.quoteBuffer = nil
			}
			st.quoteBuffer = append(st.quoteBuffer, m[1])
			continue
		}
		if st.inQuote {
			st.flushQuote()
			if strings.TrimSpace(line) != "" {
				if c.isTableRow(line) {
					st.inTable = true
					st.tableBuffer = []string{line}
				} else {
					st.result = append(st.result, c.FormatLine(line))
				}
			} else {
				st.result = append(st.result, "")
			}
			continue
		}
		if c.isTableRow(line) {
			if !st.inTable {
				st.inTable = true
				st.tableBuffer = []string{line}
			} else {
				st.tableBuffer = append(st.tableBuffer, line)
			}
			continue
		}
		if st.inTable {
			st.flushTable()
		}
		st.result = append(st.result, c.FormatLine(line))
	}
	if st.inQuote {
		st.flushQuote()
	}
	if st.inTable {
		st.flushTable()
	}
	if st.inCodeBlock {
		st.flushCode()
	}
	return strings.Join(st.result, "\n")
}

// blockState tracks the buffered structures within a FormatBlock run.
type blockState struct {
	c           *Converter
	result      []string
	inCodeBlock bool
	codeLang    string
	codeBuffer  []string
	inTable     bool
	tableBuffer []string
	inQuote     bool
	quoteBuffer []string
}

func (st *blockState) flushQuote() {
	if len(st.quoteBuffer) == 0 {
		st.inQuote = false
		return
	}
	para := strings.Join(st.quoteBuffer, " ")
	formatted := st.c.formatInline(para)
	effective := max(st.c.MaxWidth-3, 1)
	for _, wl := range st.c.wrapText(formatted, effective) {
		st.result = append(st.result, " │ "+wl)
	}
	st.quoteBuffer = nil
	st.inQuote = false
}

func (st *blockState) flushTable() {
	if len(st.tableBuffer) == 0 {
		st.inTable = false
		return
	}
	if len(st.tableBuffer) >= 2 && st.c.isTableSeparator(st.tableBuffer[1]) {
		st.result = append(st.result, st.c.FormatTable(st.tableBuffer)...)
	} else {
		for _, line := range st.tableBuffer {
			st.result = append(st.result, st.c.FormatLine(line))
		}
	}
	st.tableBuffer = nil
	st.inTable = false
}

func (st *blockState) flushCode() {
	if len(st.codeBuffer) == 0 {
		return
	}
	codeContent := strings.Join(st.codeBuffer, "\n")
	if st.c.Highlighter != nil && st.codeLang != "" {
		if strings.ToLower(st.codeLang) == "rawmu" {
			st.result = append(st.result, codeContent)
		} else {
			highlighted, err := st.c.Highlighter.Highlight(codeContent, "", st.codeLang)
			if err != nil {
				st.result = append(st.result, CodeBG+CodeFG)
				st.result = append(st.result, LiteralStart)
				st.result = append(st.result, escapeLiterals(codeContent))
				st.result = append(st.result, LiteralEnd)
				st.result = append(st.result, CodeReset)
			} else {
				st.result = append(st.result, CodeBG+CodeFG)
				st.result = append(st.result, highlighted)
				st.result = append(st.result, CodeReset)
			}
		}
	} else {
		st.result = append(st.result, CodeBG+CodeFG)
		st.result = append(st.result, LiteralStart)
		st.result = append(st.result, escapeLiterals(codeContent))
		st.result = append(st.result, LiteralEnd)
		st.result = append(st.result, CodeReset)
	}
	st.codeBuffer = nil
}

// FormatLine formats a single markdown line into micron. It mirrors
// Python's MarkdownToMicron.format_line (called with the default mode).
func (c *Converter) FormatLine(line string) string {
	line = strings.ReplaceAll(line, "\\", "\\\\")
	if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "- ") {
		line = "\\" + line
	}
	if strings.HasPrefix(line, "<") {
		line = "\\" + line
	}
	if horizontalRuleRE.MatchString(line) {
		return c.formatHorizontalRule()
	}
	if m := headerRE.FindStringSubmatch(line); m != nil {
		return c.formatHeader(m)
	}
	if m := unorderedListRE.FindStringSubmatch(line); m != nil {
		return c.formatListItem(m)
	}
	return c.formatInline(line)
}

// formatInline processes inline markdown elements (links, inline code,
// bold, italic) in the same order as Python's _format_inline. Links are
// extracted first, then inline code, then bold, then italic; placeholders
// are restored in the reverse order (links then code).
func (c *Converter) formatInline(text string) string {
	var links [][2]string
	var codeBlocks []string

	text = linkRE.ReplaceAllStringFunc(text, func(m string) string {
		sm := linkRE.FindStringSubmatch(m)
		links = append(links, [2]string{sm[1], sm[2]})
		return placeholder("LINK", len(links)-1)
	})
	text = inlineCodeRE.ReplaceAllStringFunc(text, func(m string) string {
		sm := inlineCodeRE.FindStringSubmatch(m)
		codeBlocks = append(codeBlocks, sm[1])
		return placeholder("CODE", len(codeBlocks)-1)
	})
	text = boldRE.ReplaceAllStringFunc(text, c.boldSub)
	text = italicRE.ReplaceAllStringFunc(text, c.italicSub)

	text = linkPlaceholderRE.ReplaceAllStringFunc(text, func(m string) string {
		sm := linkPlaceholderRE.FindStringSubmatch(m)
		idx := atoi(sm[1])
		linkText, url := links[idx][0], links[idx][1]
		return c.restoreLink(linkText, url)
	})
	text = codePlaceholderRE.ReplaceAllStringFunc(text, func(m string) string {
		sm := codePlaceholderRE.FindStringSubmatch(m)
		idx := atoi(sm[1])
		content := codeBlocks[idx]
		content = strings.ReplaceAll(content, "`", "\\`")
		return CodeBGInline + CodeFG + content + CodeReset
	})
	return text
}

func (c *Converter) restoreLink(text, url string) string {
	anchorComponents := strings.Split(url, "#")
	url = anchorComponents[0]
	anchor := ""
	if len(anchorComponents) > 1 {
		anchor = anchorComponents[1]
	}
	if !strings.Contains(url, ":/") {
		url = c.LocalURLScope + url
		if anchor != "" {
			url = url + "|anchor=" + anchor
		}
	}
	undl := ""
	if c.UnderlineLinks {
		undl = Underline
	}
	bold := ""
	if c.BoldLinks {
		bold = Bold
	}
	text = strings.ReplaceAll(text, "`", "")
	link := undl + bold + "`[" + text + "`" + url + "]" + bold + undl
	if c.LinkColor != "" && len(c.LinkColor) == 3 {
		link = "`F" + c.LinkColor + link + "`f"
	}
	if c.LinkColor != "" && len(c.LinkColor) == 6 {
		link = "`FT" + c.LinkColor + link + "`f"
	}
	return link
}

func (c *Converter) boldSub(m string) string {
	sm := boldRE.FindStringSubmatch(m)
	content := sm[1]
	if content == "" {
		content = sm[2]
	}
	return Bold + content + BoldEnd
}

func (c *Converter) italicSub(m string) string {
	sm := italicRE.FindStringSubmatch(m)
	content := sm[1]
	if content == "" {
		content = sm[2]
	}
	return Italic + content + ItalicEnd
}

func (c *Converter) formatHeader(m []string) string {
	hashes := m[1]
	content := m[2]
	level := min(len(hashes), 6)
	prefix := strings.Repeat(">", level)
	return prefix + c.formatInline(content)
}

func (c *Converter) formatListItem(m []string) string {
	indent := m[1]
	content := c.formatInline(m[3])
	return indent + " " + Bullet + " " + content
}

func (c *Converter) formatHorizontalRule() string { return "-" }

func (c *Converter) detectCodeFence(line string) (bool, string) {
	m := codeFenceRE.FindStringSubmatch(line)
	if m == nil {
		return false, ""
	}
	return true, m[2]
}

func (c *Converter) isTableRow(line string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	m := tableRowRE.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	content := m[1]
	return strings.Contains(content, "|") || strings.HasPrefix(strings.TrimSpace(line), "|")
}

func (c *Converter) isTableSeparator(line string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	return tableSepRE.MatchString(line)
}

// displayWidth returns the display width of text. Python uses wcswidth
// when the wcwidth module is available, falling back to len(text). Go's
// stdlib has no wcwidth; this implementation uses the rune count, which
// equals Python's len(text) (codepoint count) and matches wcswidth for
// ASCII and width-1 glyphs (including the box-drawing characters used by
// tables). Wide CJK glyphs are therefore undercounted by this port versus
// a Python runtime with wcwidth installed; this only affects table
// column sizing for inputs containing such glyphs.
func (c *Converter) displayWidth(text string) int {
	return utf8.RuneCountInString(text)
}

func (c *Converter) visibleWidth(text string) int {
	text = visFBB3.ReplaceAllString(text, "")
	text = visFBT6.ReplaceAllString(text, "")
	text = visFmt.ReplaceAllString(text, "")
	text = visFB.ReplaceAllString(text, "")
	text = visF.ReplaceAllString(text, "")
	text = visB.ReplaceAllString(text, "")
	return c.displayWidth(text)
}

func (c *Converter) padCell(text string, width int, align string) string {
	text = c.truncateCell(text, width)
	tw := c.visibleWidth(text)
	padding := width - tw
	switch align {
	case "right":
		return strings.Repeat(" ", padding) + text
	case "center":
		left := padding / 2
		right := padding - left
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
	default:
		return text + strings.Repeat(" ", padding)
	}
}

func (c *Converter) truncateCell(text string, width int) string {
	if c.visibleWidth(text) <= width {
		return text
	}
	runes := []rune(text)
	trunc := len(runes)
	for trunc > 0 && c.visibleWidth(string(runes[:trunc])) >= width {
		trunc--
	}
	truncated := string(runes[:trunc])

	var activeTags [4]bool // index: ! * _ =
	fgActive := false
	bgActive := false
	tb := []byte(truncated)
	i := 0
	for i < len(tb) {
		if tb[i] == '`' {
			if i+1 < len(tb) {
				tag := tb[i+1]
				switch tag {
				case '!', '*', '_', '=':
					idx := tagIdx(tag)
					activeTags[idx] = !activeTags[idx]
					i += 2
					continue
				case 'f':
					fgActive = false
					i += 2
					continue
				case 'b':
					bgActive = false
					i += 2
					continue
				case 'F':
					fgActive = true
					if i+2 < len(tb) && tb[i+2] == 'T' {
						i += 8
					} else {
						i += 5
					}
					continue
				case 'B':
					bgActive = true
					if i+2 < len(tb) && tb[i+2] == 'T' {
						i += 8
					} else {
						i += 5
					}
					continue
				}
			}
		}
		i++
	}

	var closers []string
	if fgActive {
		closers = append(closers, "`f")
	}
	if bgActive {
		closers = append(closers, "`b")
	}
	for idx, active := range activeTags {
		if active {
			closers = append(closers, "`"+string(tagChar(idx)))
		}
	}
	return truncated + strings.Join(closers, "") + "…"
}

func tagIdx(c byte) int {
	switch c {
	case '!':
		return 0
	case '*':
		return 1
	case '_':
		return 2
	case '=':
		return 3
	}
	return 0
}

func tagChar(idx int) byte {
	switch idx {
	case 0:
		return '!'
	case 1:
		return '*'
	case 2:
		return '_'
	case 3:
		return '='
	}
	return '!'
}

func (c *Converter) wrapText(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	words := strings.Split(text, " ")
	var lines []string
	currentLine := ""
	currentWidth := 0
	for _, word := range words {
		if word == "" {
			continue
		}
		wordWidth := c.visibleWidth(word)
		if wordWidth > width {
			if currentLine != "" {
				lines = append(lines, currentLine)
				currentLine = ""
				currentWidth = 0
			}
			remaining := word
			for remaining != "" {
				runes := []rune(remaining)
				low, high := 1, len(runes)
				fitChars := 0
				for low <= high {
					mid := (low + high) / 2
					if c.visibleWidth(string(runes[:mid])) <= width {
						fitChars = mid
						low = mid + 1
					} else {
						high = mid - 1
					}
				}
				if fitChars == 0 {
					fitChars = 1
				}
				lines = append(lines, string(runes[:fitChars]))
				remaining = string(runes[fitChars:])
			}
			continue
		}
		spaceWidth := 0
		if currentLine != "" {
			spaceWidth = 1
		}
		if currentWidth+spaceWidth+wordWidth <= width {
			if currentLine != "" {
				currentLine += " " + word
				currentWidth += spaceWidth + wordWidth
			} else {
				currentLine = word
				currentWidth = wordWidth
			}
		} else {
			lines = append(lines, currentLine)
			currentLine = word
			currentWidth = wordWidth
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// ConvertMarkdownToMicron converts markdown text to micron using default
// converter settings (max width 100, no syntax highlighter, URL scope
// ":/page/"). It mirrors Python's convert_markdown_to_micron.
func ConvertMarkdownToMicron(text string) string {
	return NewConverter().FormatBlock(text)
}

// placeholder builds a NUL-delimited placeholder token used to stash
// extracted links/inline-code during inline formatting.
func placeholder(kind string, idx int) string {
	return "\x00" + kind + itoa(idx) + "\x00"
}

// itoa is a small non-allocating int formatter sufficient for placeholder
// indices.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// atoi parses a decimal integer from s.
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// escapeLiterals escapes backticks so they render as literal text within
// a micron literal block. It mirrors Python's _escape_literals.
func escapeLiterals(text string) string {
	return strings.ReplaceAll(text, "`", "\\`")
}
