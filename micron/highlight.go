// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package micron

import (
	"strings"
)

// Highlighter applies syntax highlighting to source code, emitting micron
// colour sequences. It is a Go port of Python's SyntaxHighlighter
// (highlight.py). Unlike the Python implementation, which delegates
// tokenisation to the third-party Pygments library, this port uses
// hand-written, stdlib-only tokenisers for a small set of languages.
//
// The plain-text fallback path (used when no language is supplied or the
// language is not supported by a hand-written tokeniser) is
// byte-comparable to Python's SyntaxHighlighter with Pygments unavailable.
// The coloured paths are a stdlib reimplementation: they emit the same
// micron colour codes as Python's MicronFormatter for the same token
// kinds, but token boundaries may differ from Pygments, so coloured
// output is not guaranteed to be byte-identical to a Pygments-equipped
// Python runtime.
type Highlighter struct {
	Theme map[string]string
}

// HighlightOption configures a Highlighter at construction time.
type HighlightOption func(*Highlighter)

// WithTheme replaces the default colour theme.
func WithTheme(theme map[string]string) HighlightOption {
	return func(h *Highlighter) { h.Theme = theme }
}

// NewHighlighter returns a Highlighter with the default theme, mirroring
// Python's SyntaxHighlighter default theme (highlight.py).
func NewHighlighter(opts ...HighlightOption) *Highlighter {
	h := &Highlighter{Theme: defaultTheme()}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Highlight returns micron-highlighted code. When language is empty and
// filename does not map to a supported language, or the language is not
// supported, the plain-text fallback is used. The fallback is
// byte-comparable to Python's SyntaxHighlighter._plain_text fallback
// (including the backslash doubling applied on the non-empty fallback
// path).
func (h *Highlighter) Highlight(content, filename, language string) (string, error) {
	if content == "" {
		return plainText(content), nil
	}
	lang := resolveLanguage(language, filename)
	tok := tokenizerFor(lang)
	if tok == nil {
		return h.plainFallback(content), nil
	}
	tokens := tok(content)
	return h.formatTokens(tokens), nil
}

// HighlightCode is a convenience function mirroring Python's
// highlight_code. It constructs a Highlighter with the default theme and
// returns highlighted micron. On any internal error it falls back to the
// plain-text rendering.
func HighlightCode(content, filename, language string) string {
	h := NewHighlighter()
	out, err := h.Highlight(content, filename, language)
	if err != nil {
		return h.plainFallback(content)
	}
	return out
}

// plainText mirrors Python's SyntaxHighlighter._plain_text: it wraps
// content in a micron literal block and escapes backticks (only). It does
// NOT double backslashes.
func plainText(content string) string {
	escaped := strings.ReplaceAll(content, "`", "\\`")
	return "`=\n" + escaped + "\n`="
}

// plainFallback mirrors Python's non-empty fallback path:
// _plain_text(content).replace("\\", "\\\\") — it doubles every backslash
// in the literal-block-wrapped result (including backslashes introduced by
// backtick escaping).
func (h *Highlighter) plainFallback(content string) string {
	return strings.ReplaceAll(plainText(content), "\\", "\\\\")
}

// resolveLanguage maps a language hint or filename to a canonical
// tokeniser language name. It mirrors the aliasing in Python's
// _highlight_pygments (env/environment -> bash) and adds filename
// inference for a few extensions.
func resolveLanguage(language, filename string) string {
	lang := strings.ToLower(strings.TrimSpace(language))
	switch lang {
	case "env", "environment":
		return "bash"
	case "sh", "shell":
		return "bash"
	case "py", "python":
		return "python"
	case "go", "golang":
		return "go"
	case "":
		if filename != "" {
			switch lowerExt(filename) {
			case ".py":
				return "python"
			case ".go":
				return "go"
			case ".sh", ".bash":
				return "bash"
			}
		}
		return ""
	default:
		return lang
	}
}

func lowerExt(filename string) string {
	i := strings.LastIndex(filename, ".")
	if i < 0 {
		return ""
	}
	return strings.ToLower(filename[i:])
}

// color returns the hex colour for a theme key, or "" when the key maps
// to None (no colour) or is absent.
func (h *Highlighter) color(key string) string {
	if key == "" {
		return ""
	}
	if v, ok := h.Theme[key]; ok {
		return v
	}
	return ""
}

// token is a single tokenised span with a theme colour key and raw value.
type token struct {
	colorKey string
	value    string
}

// formatTokens renders tokens to micron, mirroring the colour-emission
// logic of Python's MicronFormatter.format. Coloured tokens are wrapped in
// `FT{color}...`f with leading/trailing newlines extracted outside the
// colour span; uncoloured tokens are emitted raw (escaped). A name token
// immediately following a "." operator is coloured as an attribute call.
func (h *Highlighter) formatTokens(tokens []token) string {
	var b strings.Builder
	prevDot := false
	lastEndedBreak := true
	for _, t := range tokens {
		isDot := t.colorKey == "operator" && t.value == "."
		endsBreak := strings.HasSuffix(t.value, "\n")

		color := ""
		if prevDot && strings.HasPrefix(t.colorKey, "name") && t.value != "" {
			color = h.color("attribute_call")
		} else {
			color = h.color(t.colorKey)
		}

		if color != "" && t.value != "" {
			escaped := escapeValue(t.value)
			ilb := ""
			if strings.HasPrefix(escaped, "\n") {
				ilb = "\n"
				escaped = escaped[1:]
			}
			tlb := ""
			if strings.HasSuffix(escaped, "\n") {
				tlb = "\n"
				escaped = escaped[:len(escaped)-1]
			}
			if len(escaped) > 0 {
				b.WriteString(ilb)
				b.WriteString("`FT")
				b.WriteString(color)
				b.WriteString(escaped)
				b.WriteString("`f")
				b.WriteString(tlb)
			} else {
				b.WriteString(ilb)
				b.WriteString(tlb)
			}
		} else {
			escaped := escapeValue(t.value)
			b.WriteString(escapePlainSpan(escaped, lastEndedBreak))
		}
		prevDot = isDot
		lastEndedBreak = endsBreak
	}
	return b.String()
}

// escapeValue mirrors Python's MicronFormatter._escape_value: double
// backslashes, then escape backticks.
func escapeValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "`", "\\`")
	return value
}

// escapePlainSpan mirrors the plain-token branch of MicronFormatter.format:
// when a value spans newlines, lines beginning with '-', '>' or '<' are
// backslash-escaped; when the previous token ended with a line break and
// the value begins with one of those characters, it is also escaped.
func escapePlainSpan(escaped string, lastEndedBreak bool) string {
	if strings.Contains(escaped, "\n") {
		lines := strings.Split(escaped, "\n")
		if len(lines) > 1 {
			for i, line := range lines {
				if strings.HasPrefix(line, "-") || strings.HasPrefix(line, ">") || strings.HasPrefix(line, "<") {
					lines[i] = "\\" + line
				}
			}
			joined := strings.Join(lines, "\n")
			if strings.HasSuffix(escaped, "\n") {
				return joined + "\n"
			}
			return joined
		}
	} else if lastEndedBreak {
		if strings.HasPrefix(escaped, "-") || strings.HasPrefix(escaped, ">") || strings.HasPrefix(escaped, "<") {
			return "\\" + escaped
		}
	}
	return escaped
}

// tokenizerFor returns the hand-written tokeniser for lang, or nil when
// unsupported.
func tokenizerFor(lang string) func(string) []token {
	switch lang {
	case "python":
		return tokenizePython
	case "go":
		return tokenizeGo
	case "bash":
		return tokenizeBash
	default:
		return nil
	}
}

// defaultTheme returns the default colour theme, mirroring Python's
// SyntaxHighlighter._get_default_theme. Keys with no colour (None in
// Python) are omitted from the map so that color() returns "".
func defaultTheme() map[string]string {
	return map[string]string{
		"keyword":             "ff7b72",
		"keyword_constant":    "ff7b72",
		"keyword_control":     "ff7b72",
		"keyword_declaration": "ff7b72",
		"function_def":        "79c0ff",
		"function_magic":      "ff7b72",
		"function_call":       "d2a8ff",
		"function_builtin":    "ffa657",
		"class_def":           "7ee787",
		"class_ref":           "56d364",
		"self":                "ff9bce",
		"cls":                 "ff9bce",
		"string":              "a5d6ff",
		"string_quoted":       "a5d6ff",
		"string_doc":          "8b949e",
		"string_interpol":     "ffd700",
		"string_escape":       "ffea00",
		"number":              "79c0ff",
		"number_float":        "79c0ff",
		"number_integer":      "79c0ff",
		"number_hex":          "79c0ff",
		"comment":             "8b949e",
		"comment_doc":         "8b949e",
		"comment_preproc":     "ff7b72",
		"operator":            "ff7b72",
		"operator_arithmetic": "ff7b72",
		"operator_comparison": "ff7b72",
		"operator_assignment": "ff7b72",
		"operator_word":       "ff7b72",
		"operator_dot":        "c9d1d9",
		"punctuation":         "b4b4b4",
		"punctuation_brace":   "b4b4b4",
		"punctuation_paren":   "b4b4b4",
		"punctuation_colon":   "b4b4b4",
		"punctuation_comma":   "8b949e",
		"decorator":           "f0883e",
		"constant":            "ff7b72",
		"constant_builtin":    "ff7b72",
		"type_hint":           "ffa657",
		"type_builtin":        "ffa657",
		"exception":           "f85149",
		"exception_builtin":   "f85149",
		"name":                "e6edf3",
		"attribute":           "e6edf3",
		"attribute_call":      "d2a8ff",
		"variable":            "e6edf3",
		"parameter":           "e6edf3",
		"namespace":           "7ee787",
		"module":              "a5d6ff",
		"generic_heading":     "c9d1d9",
		"generic_subheading":  "c9d1d9",
		"generic_prompt":      "8b949e",
		"generic_error":       "f85149",
		"generic_deleted":     "f85149",
		"generic_inserted":    "7ee787",
		"generic_output":      "e6edf3",
	}
}
