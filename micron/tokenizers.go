// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package micron

import "strings"

// The hand-written tokenisers below are stdlib-only reimplementations of
// the tokenisation that Pygments performs in Python's highlight.py. They
// aim to classify the common token kinds (keywords, strings, comments,
// numbers, identifiers, operators, punctuation) so that the MicronFormatter
// emits the same colour codes Pygments would for those kinds. They do NOT
// replicate Pygments' exact token boundaries, so coloured output is not
// guaranteed to be byte-identical to a Pygments-equipped Python runtime.

// pythonKeywords are Python language keywords.
var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true,
	"class": true, "continue": true, "def": true, "del": true, "elif": true,
	"else": true, "except": true, "finally": true, "for": true, "from": true,
	"global": true, "if": true, "import": true, "in": true, "is": true,
	"lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true,
	"raise": true, "return": true, "try": true, "while": true, "with": true,
	"yield": true,
}

// pythonBuiltins are commonly-used Python builtins coloured as
// function_builtin (amber).
var pythonBuiltins = map[string]bool{
	"print": true, "len": true, "range": true, "int": true, "str": true,
	"float": true, "list": true, "dict": true, "set": true, "tuple": true,
	"bool": true, "bytes": true, "open": true, "type": true, "isinstance": true,
	"hasattr": true, "getattr": true, "setattr": true, "enumerate": true,
	"zip": true, "map": true, "filter": true, "sorted": true, "reversed": true,
	"sum": true, "min": true, "max": true, "abs": true, "round": true,
	"input": true, "format": true, "super": true, "object": true,
}

// tokenizePython is a hand-written Python tokeniser.
func tokenizePython(src string) []token {
	var out []token
	i := 0
	n := len(src)
	prevSignificant := ""
	isIdentStart := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	isIdentCont := func(c byte) bool {
		return isIdentStart(c) || (c >= '0' && c <= '9')
	}
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	push := func(key, val string) {
		if val == "" {
			return
		}
		out = append(out, token{colorKey: key, value: val})
		if strings.TrimSpace(val) != "" {
			prevSignificant = val
		}
	}
	for i < n {
		c := src[i]
		switch {
		case c == '#':
			j := i
			for j < n && src[j] != '\n' {
				j++
			}
			push("comment", src[i:j])
			i = j
		case c == '"' || c == '\'':
			j := i + 1
			for j < n && src[j] != c {
				if src[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				j++
			}
			if j < n {
				j++
			}
			push("string", src[i:j])
			i = j
		case isDigit(c):
			j := i
			if c == '0' && j+1 < n && (src[j+1] == 'x' || src[j+1] == 'X') {
				j += 2
				for j < n && isHex(src[j]) {
					j++
				}
				push("number_hex", src[i:j])
			} else {
				for j < n && isDigit(src[j]) {
					j++
				}
				if j < n && src[j] == '.' {
					j++
					for j < n && isDigit(src[j]) {
						j++
					}
					push("number_float", src[i:j])
				} else {
					push("number_integer", src[i:j])
				}
			}
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < n && isIdentCont(src[j]) {
				j++
			}
			word := src[i:j]
			i = j
			switch {
			case pythonKeywords[word]:
				push("keyword", word)
			case word == "self":
				push("self", word)
			case word == "cls":
				push("cls", word)
			case prevSignificant == "def":
				push("function_call", word)
			case prevSignificant == "class":
				push("class_def", word)
			case pythonBuiltins[word]:
				push("function_builtin", word)
			default:
				push("name", word)
			}
		case c == '\n' || c == ' ' || c == '\t':
			j := i
			for j < n && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n') {
				j++
			}
			push("text", src[i:j])
			i = j
		case strings.ContainsRune("()[]{}", rune(c)):
			push("punctuation", string(c))
			i++
		case strings.ContainsRune(",;:", rune(c)):
			push("punctuation", string(c))
			i++
		case strings.ContainsRune("+-*/%=<>!&|^~", rune(c)):
			j := i
			for j < n && strings.ContainsRune("+-*/%=<>!&|^~", rune(src[j])) {
				j++
			}
			push("operator", src[i:j])
			i = j
		case c == '.':
			push("operator", ".")
			i++
		case c == '@':
			push("decorator", "@")
			i++
		default:
			push("text", string(c))
			i++
		}
	}
	return out
}

// goKeywords are Go language keywords.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true,
	"for": true, "func": true, "go": true, "goto": true, "if": true,
	"import": true, "interface": true, "map": true, "package": true,
	"range": true, "return": true, "select": true, "struct": true,
	"switch": true, "type": true, "var": true,
}

// goBuiltins are Go builtins coloured as function_builtin (amber).
var goBuiltins = map[string]bool{
	"make": true, "new": true, "len": true, "cap": true, "copy": true,
	"append": true, "panic": true, "recover": true, "print": true,
	"println": true, "complex": true, "real": true, "imag": true,
	"close": true, "delete": true, "clear": true, "max": true, "min": true,
}

// goTypes are Go built-in types coloured as type_builtin (amber).
var goTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true, "complex64": true,
	"complex128": true, "bool": true, "byte": true, "rune": true,
	"string": true, "error": true, "any": true,
}

// tokenizeGo is a hand-written Go tokeniser.
func tokenizeGo(src string) []token {
	var out []token
	i := 0
	n := len(src)
	prevSignificant := ""
	isIdentStart := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	isIdentCont := func(c byte) bool {
		return isIdentStart(c) || (c >= '0' && c <= '9')
	}
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	push := func(key, val string) {
		if val == "" {
			return
		}
		out = append(out, token{colorKey: key, value: val})
		if strings.TrimSpace(val) != "" {
			prevSignificant = val
		}
	}
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			j := i
			for j < n && src[j] != '\n' {
				j++
			}
			push("comment", src[i:j])
			i = j
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := i + 2
			for j+1 < n && !(src[j] == '*' && src[j+1] == '/') {
				j++
			}
			if j+1 < n {
				j += 2
			}
			push("comment_doc", src[i:j])
			i = j
		case c == '"' || c == '\'':
			j := i + 1
			for j < n && src[j] != c {
				if src[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if src[j] == '\n' {
					break
				}
				j++
			}
			if j < n && src[j] == c {
				j++
			}
			push("string", src[i:j])
			i = j
		case c == '`':
			j := i + 1
			for j < n && src[j] != '`' {
				j++
			}
			if j < n {
				j++
			}
			push("string", src[i:j])
			i = j
		case isDigit(c):
			j := i
			if c == '0' && j+1 < n && (src[j+1] == 'x' || src[j+1] == 'X') {
				j += 2
				for j < n && isHex(src[j]) {
					j++
				}
				push("number_hex", src[i:j])
			} else {
				for j < n && isDigit(src[j]) {
					j++
				}
				if j < n && src[j] == '.' && j+1 < n && isDigit(src[j+1]) {
					j++
					for j < n && isDigit(src[j]) {
						j++
					}
					push("number_float", src[i:j])
				} else {
					push("number_integer", src[i:j])
				}
			}
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < n && isIdentCont(src[j]) {
				j++
			}
			word := src[i:j]
			i = j
			switch {
			case goKeywords[word]:
				push("keyword", word)
			case goTypes[word]:
				push("type_builtin", word)
			case goBuiltins[word]:
				push("function_builtin", word)
			case prevSignificant == "type":
				push("class_def", word)
			default:
				// Pygments' GoLexer tokenises function names after `func`
				// as plain Name (not Name.Function), so they share the
				// default name colour here.
				push("name", word)
			}
		case c == '\n' || c == ' ' || c == '\t':
			j := i
			for j < n && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n') {
				j++
			}
			push("text", src[i:j])
			i = j
		case strings.ContainsRune("()[]{}", rune(c)):
			push("punctuation", string(c))
			i++
		case strings.ContainsRune(",;:", rune(c)):
			push("punctuation", string(c))
			i++
		case strings.ContainsRune("+-*/%=<>!&|^~", rune(c)):
			j := i
			for j < n && strings.ContainsRune("+-*/%=<>!&|^~", rune(src[j])) {
				j++
			}
			push("operator", src[i:j])
			i = j
		case c == '.':
			push("operator", ".")
			i++
		default:
			push("text", string(c))
			i++
		}
	}
	return out
}

// bashKeywords are Bash keywords coloured as keyword (coral).
var bashKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "in": true, "function": true,
	"return": true, "break": true, "continue": true, "local": true,
	"declare": true, "typeset": true,
}

// bashBuiltins are Bash builtins coloured as function_builtin (amber).
var bashBuiltins = map[string]bool{
	"echo": true, "export": true, "cd": true, "pwd": true, "exit": true,
	"set": true, "unset": true, "shift": true, "source": true, "alias": true,
	"unalias": true, "read": true, "test": true, "true": true, "false": true,
	"eval": true, "exec": true, "trap": true, "umask": true, "wait": true,
}

// tokenizeBash is a hand-written Bash tokeniser.
func tokenizeBash(src string) []token {
	var out []token
	i := 0
	n := len(src)
	isIdentStart := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	isIdentCont := func(c byte) bool {
		return isIdentStart(c) || (c >= '0' && c <= '9')
	}
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	push := func(key, val string) {
		if val == "" {
			return
		}
		out = append(out, token{colorKey: key, value: val})
	}
	for i < n {
		c := src[i]
		switch {
		case c == '#':
			j := i
			for j < n && src[j] != '\n' {
				j++
			}
			push("comment", src[i:j])
			i = j
		case c == '"' || c == '\'':
			j := i + 1
			for j < n && src[j] != c {
				if src[j] == '\\' && j+1 < n && c == '"' {
					j += 2
					continue
				}
				j++
			}
			if j < n {
				j++
			}
			push("string", src[i:j])
			i = j
		case isDigit(c):
			j := i
			for j < n && isDigit(src[j]) {
				j++
			}
			push("number_integer", src[i:j])
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < n && isIdentCont(src[j]) {
				j++
			}
			word := src[i:j]
			i = j
			switch {
			case bashKeywords[word]:
				push("keyword", word)
			case bashBuiltins[word]:
				push("function_builtin", word)
			default:
				push("name", word)
			}
		case c == '\n' || c == ' ' || c == '\t':
			j := i
			for j < n && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n') {
				j++
			}
			push("text", src[i:j])
			i = j
		case strings.ContainsRune("()[]{}", rune(c)):
			push("punctuation", string(c))
			i++
		case strings.ContainsRune(",;:", rune(c)):
			push("punctuation", string(c))
			i++
		case strings.ContainsRune("+-*/%=<>!&|^~", rune(c)):
			j := i
			for j < n && strings.ContainsRune("+-*/%=<>!&|^~", rune(src[j])) {
				j++
			}
			push("operator", src[i:j])
			i = j
		case c == '$':
			j := i + 1
			if j < n && src[j] == '{' {
				j++
				for j < n && src[j] != '}' {
					j++
				}
				if j < n {
					j++
				}
			} else {
				for j < n && isIdentCont(src[j]) {
					j++
				}
			}
			push("variable", src[i:j])
			i = j
		case c == '.':
			push("operator", ".")
			i++
		default:
			push("text", string(c))
			i++
		}
	}
	return out
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
