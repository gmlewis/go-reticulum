// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the KJV word-query language: the KJVCanOpener-style expression
// syntax the kjv command understands for a query that is not a Bible reference.
//
// The syntax is a Go port of the reference implementation in kjv-ref:
// src/utils/bibleQueryParser.ts (the tokenizer, the wildcard-to-regexp
// translation, and the AST) and src/utils/bibleQueryEval.ts (the evaluator).
// A query is one or more terms AND'd together; terms may be quoted phrases,
// wildcards (lov*, l?ve, l[ai]ve), an OR group (love|charity), an any-word slot
// (*), or an excluded term (-hate).

package main

import (
	"regexp"
	"strings"
	"unicode"
)

// kjvNodeKind is the kind of one node in a parsed query.
type kjvNodeKind int

const (
	// kjvNodeWord matches one whole word, case-insensitively.
	kjvNodeWord kjvNodeKind = iota
	// kjvNodeWildcard matches one word against a wildcard pattern.
	kjvNodeWildcard
	// kjvNodeAnyWord matches any one word, and consumes it.
	kjvNodeAnyWord
	// kjvNodePhrase matches consecutive words.
	kjvNodePhrase
	// kjvNodeOr matches any one of its alternatives.
	kjvNodeOr
)

// kjvQueryNode is one node of a parsed query.
type kjvQueryNode struct {
	// kind selects which of the fields below is meaningful.
	kind kjvNodeKind
	// value is the lowercased word for a word node.
	value string
	// pattern is the original wildcard pattern, kept for reporting.
	pattern string
	// regexp is the compiled wildcard for a wildcard node.
	regexp *regexp.Regexp
	// words are the consecutive words of a phrase node.
	words []kjvQueryNode
	// alternatives are the branches of an OR node.
	alternatives []kjvQueryNode
}

// kjvParsedQuery is a parsed word query: the terms that must match, and the
// terms that must not.
type kjvParsedQuery struct {
	include []kjvQueryNode
	exclude []kjvQueryNode
}

// kjvTokenKind is the kind of one tokenizer token.
type kjvTokenKind int

const (
	kjvTokWord kjvTokenKind = iota
	kjvTokPipe
	kjvTokStar
	kjvTokPhraseStart
	kjvTokPhraseEnd
)

// kjvToken is one tokenizer token.
type kjvToken struct {
	kind    kjvTokenKind
	value   string
	exclude bool
}

// kjvReStripPunct strips the punctuation the reference strips from the edges of a
// search term, keeping the characters that carry meaning.
var kjvReStripPunct = regexp.MustCompile(`^[^A-Za-z0-9_*?\[]+|[^A-Za-z0-9_*\]]+$`)

// kjvReExcludeHyphen matches a hyphen used as the exclude operator, which is a
// hyphen that is not part of a word. RE2 has no lookaround, so the two "not
// preceded by" and "not followed by" cases are spelled out as alternations.
var kjvReExcludeHyphen = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])-|-(?:[^A-Za-z0-9_]|$)`)

// kjvWildcardToRegexp translates a KJVCanOpener wildcard pattern into an
// anchored, case-insensitive regular expression: * becomes .*, ? becomes ., a
// character class is preserved (with a leading ! or ^ inverted), and every other
// regular-expression metacharacter is escaped so it matches literally.
func kjvWildcardToRegexp(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	inClass := false
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if inClass {
			sb.WriteRune(ch)
			if ch == ']' {
				inClass = false
			}
			continue
		}
		switch ch {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		case '[':
			sb.WriteRune('[')
			inClass = true
			if i+1 < len(runes) && (runes[i+1] == '!' || runes[i+1] == '^') {
				sb.WriteRune('^')
				i++
			}
		case '.', '+', '(', ')', '{', '}', '^', '$', '|', '\\':
			sb.WriteRune('\\')
			sb.WriteRune(ch)
		default:
			sb.WriteRune(ch)
		}
	}
	return regexp.Compile("(?i)^" + sb.String() + "$")
}

// kjvHasWildcardChars reports whether a term uses the wildcard syntax.
func kjvHasWildcardChars(word string) bool {
	return strings.ContainsAny(word, "*?[]")
}

// kjvHasSpecialSyntax reports whether a query uses any syntax beyond plain
// words. A hyphen is the exclude operator only when it is not part of a word, so
// "well-beloved" is an ordinary word.
func kjvHasSpecialSyntax(query string) bool {
	if strings.ContainsAny(query, "\"|*?[]") {
		return true
	}
	return kjvReExcludeHyphen.MatchString(query)
}

// kjvLooksLikeRegexp reports whether a query contains a regular-expression
// metacharacter the KJVCanOpener syntax does not claim, which is what routes
// "love.*life" to the raw-regexp search. The KJVCanOpener wildcards (*, ?, [],
// |) and the exclude hyphen are deliberately absent, so they stay on the word
// path.
func kjvLooksLikeRegexp(query string) bool {
	return strings.ContainsAny(query, ".+(){}^$\\")
}

// kjvVerseWords splits verse text into lowercase alphanumeric words, which is
// the vocabulary every node is matched against.
func kjvVerseWords(text string) []string {
	var words []string
	start := -1
	for i := 0; i <= len(text); i++ {
		isWord := false
		if i < len(text) {
			c := text[i]
			isWord = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		}
		if isWord {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			words = append(words, strings.ToLower(text[start:i]))
			start = -1
		}
	}
	return words
}

// kjvMakeWordNode turns one search term into a node. A term that is really
// several words once the verse-splitting rules apply — "well-beloved", say —
// becomes a phrase, because that is exactly what it is in the verse text.
func kjvMakeWordNode(value string) kjvQueryNode {
	cleaned := kjvReStripPunct.ReplaceAllString(value, "")
	if cleaned == "" {
		return kjvQueryNode{kind: kjvNodeWord, value: strings.ToLower(value)}
	}
	if kjvHasWildcardChars(cleaned) {
		if re, err := kjvWildcardToRegexp(cleaned); err == nil {
			return kjvQueryNode{kind: kjvNodeWildcard, pattern: cleaned, regexp: re}
		}
		return kjvQueryNode{kind: kjvNodeWord, value: strings.ToLower(cleaned)}
	}
	parts := kjvVerseWords(cleaned)
	switch len(parts) {
	case 0:
		return kjvQueryNode{kind: kjvNodeWord, value: strings.ToLower(cleaned)}
	case 1:
		return kjvQueryNode{kind: kjvNodeWord, value: parts[0]}
	default:
		nodes := make([]kjvQueryNode, 0, len(parts))
		for _, part := range parts {
			nodes = append(nodes, kjvQueryNode{kind: kjvNodeWord, value: part})
		}
		return kjvQueryNode{kind: kjvNodePhrase, words: nodes}
	}
}

// kjvFlattenPhrase replaces any nested phrase node with its words, so a phrase
// is always a flat run of word-like positions.
func kjvFlattenPhrase(nodes []kjvQueryNode) []kjvQueryNode {
	out := make([]kjvQueryNode, 0, len(nodes))
	for _, node := range nodes {
		if node.kind == kjvNodePhrase {
			out = append(out, node.words...)
			continue
		}
		out = append(out, node)
	}
	return out
}

// kjvTokenize splits a query into its tokens.
func kjvTokenize(input string) []kjvToken {
	runes := []rune(input)
	var tokens []kjvToken
	i, n := 0, len(runes)
	readWord := func() string {
		var word []rune
		for i < n && !unicode.IsSpace(runes[i]) && runes[i] != '|' && runes[i] != '"' {
			word = append(word, runes[i])
			i++
		}
		return string(word)
	}
	for i < n {
		ch := runes[i]
		if unicode.IsSpace(ch) {
			i++
			continue
		}
		if ch == '"' {
			tokens = append(tokens, kjvToken{kind: kjvTokPhraseStart})
			i++
			var word []rune
			for i < n && runes[i] != '"' {
				if unicode.IsSpace(runes[i]) {
					if len(word) > 0 {
						tokens = append(tokens, kjvPhraseWordToken(string(word)))
						word = word[:0]
					}
				} else {
					word = append(word, runes[i])
				}
				i++
			}
			if len(word) > 0 {
				tokens = append(tokens, kjvPhraseWordToken(string(word)))
			}
			i++
			tokens = append(tokens, kjvToken{kind: kjvTokPhraseEnd})
			continue
		}
		if ch == '|' {
			tokens = append(tokens, kjvToken{kind: kjvTokPipe})
			i++
			continue
		}
		if ch == '-' && i+1 < n && !unicode.IsSpace(runes[i+1]) && runes[i+1] != '|' {
			if runes[i+1] == '"' {
				i++
				start := len(tokens)
				tokens = append(tokens, kjvToken{kind: kjvTokPhraseStart})
				i++
				var word []rune
				for i < n && runes[i] != '"' {
					if unicode.IsSpace(runes[i]) && len(word) > 0 {
						tokens = append(tokens, kjvToken{kind: kjvTokWord, value: string(word)})
						word = word[:0]
					} else if !unicode.IsSpace(runes[i]) {
						word = append(word, runes[i])
					}
					i++
				}
				if len(word) > 0 {
					tokens = append(tokens, kjvToken{kind: kjvTokWord, value: string(word)})
				}
				i++
				tokens = append(tokens, kjvToken{kind: kjvTokPhraseEnd})
				for t := start; t < len(tokens); t++ {
					if tokens[t].kind == kjvTokWord {
						tokens[t].exclude = true
					}
				}
				continue
			}
			i++
			if word := readWord(); word != "" {
				tokens = append(tokens, kjvToken{kind: kjvTokWord, value: word, exclude: true})
			}
			continue
		}
		if ch == '*' && (i+1 >= n || unicode.IsSpace(runes[i+1]) || runes[i+1] == '|') {
			tokens = append(tokens, kjvToken{kind: kjvTokStar})
			i++
			continue
		}
		word := readWord()
		switch {
		case word == "":
		case word == "*":
			tokens = append(tokens, kjvToken{kind: kjvTokStar})
		default:
			tokens = append(tokens, kjvToken{kind: kjvTokWord, value: word})
		}
	}
	return tokens
}

// kjvPhraseWordToken turns one word inside a quoted phrase into a token, where a
// lone * is the any-word placeholder.
func kjvPhraseWordToken(value string) kjvToken {
	if value == "*" {
		return kjvToken{kind: kjvTokStar}
	}
	return kjvToken{kind: kjvTokWord, value: value}
}

// kjvParseQuery parses a query into its include and exclude terms.
func kjvParseQuery(input string) kjvParsedQuery {
	tokens := kjvTokenize(input)
	var include, exclude []kjvQueryNode
	i := 0
	for i < len(tokens) {
		if tokens[i].kind == kjvTokPipe {
			i++
			continue
		}
		var group []kjvQueryNode
		groupExclude := false
		for i < len(tokens) {
			switch tokens[i].kind {
			case kjvTokPipe:
				i++
				continue
			case kjvTokWord:
				if tokens[i].exclude {
					groupExclude = true
				}
				group = append(group, kjvMakeWordNode(tokens[i].value))
				i++
			case kjvTokStar:
				group = append(group, kjvQueryNode{kind: kjvNodeAnyWord})
				i++
			case kjvTokPhraseStart:
				i++
				var phrase []kjvQueryNode
				for i < len(tokens) && tokens[i].kind != kjvTokPhraseEnd {
					switch tokens[i].kind {
					case kjvTokWord:
						if tokens[i].exclude {
							groupExclude = true
						}
						phrase = append(phrase, kjvMakeWordNode(tokens[i].value))
					case kjvTokStar:
						phrase = append(phrase, kjvQueryNode{kind: kjvNodeAnyWord})
					}
					i++
				}
				if i < len(tokens) {
					i++
				}
				phrase = kjvFlattenPhrase(phrase)
				switch {
				case len(phrase) == 1:
					group = append(group, phrase[0])
				case len(phrase) > 1:
					group = append(group, kjvQueryNode{kind: kjvNodePhrase, words: phrase})
				}
			case kjvTokPhraseEnd:
				i++
			default:
				i++
			}
			if i >= len(tokens) || tokens[i].kind != kjvTokPipe {
				break
			}
		}
		if len(group) == 0 {
			continue
		}
		node := group[0]
		if len(group) > 1 {
			node = kjvQueryNode{kind: kjvNodeOr, alternatives: group}
		}
		if groupExclude {
			exclude = append(exclude, node)
		} else if node.kind != kjvNodeAnyWord {
			include = append(include, node)
		}
	}
	return kjvParsedQuery{include: include, exclude: exclude}
}

// kjvMatchWord reports whether one word matches one node.
func kjvMatchWord(word string, node kjvQueryNode) bool {
	switch node.kind {
	case kjvNodeWord:
		return strings.ToLower(word) == node.value
	case kjvNodeWildcard:
		return node.regexp != nil && node.regexp.MatchString(word)
	case kjvNodeAnyWord:
		return true
	default:
		return false
	}
}

// kjvPhraseMatchesAt reports whether a flat phrase matches the words starting at
// start. An any-word slot consumes exactly one word.
func kjvPhraseMatchesAt(words []string, nodes []kjvQueryNode, start int) bool {
	for k, node := range nodes {
		idx := start + k
		if idx >= len(words) {
			return false
		}
		if node.kind == kjvNodeAnyWord {
			continue
		}
		if !kjvMatchWord(words[idx], node) {
			return false
		}
	}
	return true
}

// kjvPhraseMatchesInVerse reports whether a phrase appears anywhere in a verse.
func kjvPhraseMatchesInVerse(words []string, nodes []kjvQueryNode) bool {
	if len(nodes) == 0 {
		return false
	}
	for i := 0; i+len(nodes) <= len(words); i++ {
		if kjvPhraseMatchesAt(words, nodes, i) {
			return true
		}
	}
	return false
}

// kjvNodeMatchesInVerse reports whether one node matches anywhere in a verse.
func kjvNodeMatchesInVerse(words []string, node kjvQueryNode) bool {
	switch node.kind {
	case kjvNodeWord, kjvNodeWildcard, kjvNodeAnyWord:
		for _, word := range words {
			if kjvMatchWord(word, node) {
				return true
			}
		}
		return false
	case kjvNodePhrase:
		return kjvPhraseMatchesInVerse(words, node.words)
	case kjvNodeOr:
		for _, alt := range node.alternatives {
			if kjvNodeMatchesInVerse(words, alt) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// kjvVerseMatchesQuery reports whether a verse satisfies a parsed query: every
// include term must match, and no exclude term may.
func kjvVerseMatchesQuery(words []string, query kjvParsedQuery) bool {
	for _, node := range query.include {
		if !kjvNodeMatchesInVerse(words, node) {
			return false
		}
	}
	for _, node := range query.exclude {
		if kjvNodeMatchesInVerse(words, node) {
			return false
		}
	}
	return true
}
