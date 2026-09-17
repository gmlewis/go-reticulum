// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"slices"
	"testing"
)

// TestKJVWildcardToRegexp ports the reference wildcard converter: * becomes .*,
// ? becomes ., character classes are preserved, and everything else is escaped.
func TestKJVWildcardToRegexp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern string
		match   []string
		nomatch []string
	}{
		{
			pattern: "lov*",
			match:   []string{"love", "loved", "loving", "lov", "lovedx"},
			nomatch: []string{"hate"},
		},
		{
			pattern: "l?ve",
			match:   []string{"love", "live", "luve"},
			nomatch: []string{"lve", "loove"},
		},
		{
			pattern: "l[ai]ve",
			match:   []string{"live", "lave"},
			nomatch: []string{"love"},
		},
		{
			pattern: "l[a-e]ve",
			match:   []string{"lave", "leve"},
			nomatch: []string{"lfve"},
		},
		{
			pattern: "a.b+c",
			match:   []string{"a.b+c"},
			nomatch: []string{"axbyc"},
		},
		{
			pattern: "LOVE",
			match:   []string{"love", "Love", "LOVE"},
		},
		{
			pattern: "l?ve*",
			match:   []string{"loved", "lives"},
			nomatch: []string{"lo"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()
			re, err := kjvWildcardToRegexp(tt.pattern)
			if err != nil {
				t.Fatalf("kjvWildcardToRegexp(%q): %v", tt.pattern, err)
			}
			for _, word := range tt.match {
				if !re.MatchString(word) {
					t.Errorf("%q should match %q", tt.pattern, word)
				}
			}
			for _, word := range tt.nomatch {
				if re.MatchString(word) {
					t.Errorf("%q should not match %q", tt.pattern, word)
				}
			}
		})
	}
	if _, err := kjvWildcardToRegexp("love["); err == nil {
		t.Error("an unterminated character class compiled, want an error")
	}
}

// TestKJVHasWildcardChars asserts exactly the four wildcard characters the query
// syntax understands are detected.
func TestKJVHasWildcardChars(t *testing.T) {
	t.Parallel()

	for _, word := range []string{"lov*", "l?ve", "l[ai]ve", "l[a]ve"} {
		if !kjvHasWildcardChars(word) {
			t.Errorf("kjvHasWildcardChars(%q) = false, want true", word)
		}
	}
	for _, word := range []string{"love", "well-beloved", ""} {
		if kjvHasWildcardChars(word) {
			t.Errorf("kjvHasWildcardChars(%q) = true, want false", word)
		}
	}
}

// TestKJVMatchWord asserts a plain word matches only the whole word, while a
// wildcard and the any-word node are broader.
func TestKJVMatchWord(t *testing.T) {
	t.Parallel()

	word := kjvQueryNode{kind: kjvNodeWord, value: "love"}
	for _, in := range []string{"LOVE", "Love", "love"} {
		if !kjvMatchWord(in, word) {
			t.Errorf("%q should match the word node", in)
		}
	}
	for _, in := range []string{"loved", "glove", "hate"} {
		if kjvMatchWord(in, word) {
			t.Errorf("%q should NOT match the word node", in)
		}
	}
	re, err := kjvWildcardToRegexp("lov*")
	if err != nil {
		t.Fatal(err)
	}
	wild := kjvQueryNode{kind: kjvNodeWildcard, pattern: "lov*", regexp: re}
	if !kjvMatchWord("loved", wild) || kjvMatchWord("hate", wild) {
		t.Error("the wildcard node did not match as expected")
	}
	if !kjvMatchWord("anything", kjvQueryNode{kind: kjvNodeAnyWord}) {
		t.Error("the any-word node should match anything")
	}
}

// TestKJVVerseWords asserts verse text is split into lowercase alphanumeric
// words, so punctuation and hyphens never stop a word from matching.
func TestKJVVerseWords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text string
		want []string
	}{
		{"In the beginning God created", []string{"in", "the", "beginning", "god", "created"}},
		{"The LORD is my shepherd; I shall not want.", []string{"the", "lord", "is", "my", "shepherd", "i", "shall", "not", "want"}},
		{"well-beloved", []string{"well", "beloved"}},
		{"", nil},
	}
	for _, tt := range cases {
		if got := kjvVerseWords(tt.text); !slices.Equal(got, tt.want) {
			t.Errorf("kjvVerseWords(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// TestKJVParseQueryWordsPhrasesAndOr ports the reference parser: a word, several
// words AND'd, a quoted phrase, a one-word quoted phrase, and the OR operator.
func TestKJVParseQueryWordsPhrasesAndOr(t *testing.T) {
	t.Parallel()

	q := kjvParseQuery("love")
	if len(q.include) != 1 || q.include[0].kind != kjvNodeWord || q.include[0].value != "love" {
		t.Errorf("parse love = %+v", q.include)
	}
	if len(kjvParseQuery("LOVE").include) != 1 || kjvParseQuery("LOVE").include[0].value != "love" {
		t.Error("parse did not lowercase the word")
	}
	if got := kjvParseQuery("'love'").include; len(got) != 1 || got[0].value != "love" {
		t.Errorf("parse did not strip surrounding punctuation: %+v", got)
	}
	if got := kjvParseQuery("love god").include; len(got) != 2 ||
		got[0].value != "love" || got[1].value != "god" {
		t.Errorf("parse love god = %+v", got)
	}

	q = kjvParseQuery(`"the love of God"`)
	if len(q.include) != 1 || q.include[0].kind != kjvNodePhrase {
		t.Fatalf("parse quoted phrase = %+v", q.include)
	}
	phrase := q.include[0].words
	if len(phrase) != 4 || phrase[0].value != "the" || phrase[1].value != "love" ||
		phrase[2].value != "of" || phrase[3].value != "god" {
		t.Errorf("quoted phrase words = %+v", phrase)
	}
	if got := kjvParseQuery(`"love"`).include; len(got) != 1 || got[0].kind != kjvNodeWord {
		t.Errorf("a one-word quoted phrase should be a plain word: %+v", got)
	}

	q = kjvParseQuery("love|charity")
	if len(q.include) != 1 || q.include[0].kind != kjvNodeOr || len(q.include[0].alternatives) != 2 {
		t.Fatalf("parse OR = %+v", q.include)
	}
	if got := kjvParseQuery("love|charity|hope").include[0]; len(got.alternatives) != 3 {
		t.Errorf("three-way OR = %+v", got.alternatives)
	}
	q = kjvParseQuery(`"the Lord"|God`)
	if len(q.include) != 1 || q.include[0].kind != kjvNodeOr || len(q.include[0].alternatives) != 2 {
		t.Fatalf("OR with a phrase = %+v", q.include)
	}
	if q.include[0].alternatives[0].kind != kjvNodePhrase ||
		q.include[0].alternatives[1].kind != kjvNodeWord {
		t.Errorf("OR alternatives = %+v", q.include[0].alternatives)
	}
}

// TestKJVParseQueryWildcardsAndAnyWord asserts *, ?, and character classes
// become wildcard nodes, and a standalone * inside a phrase is an any-word slot.
func TestKJVParseQueryWildcardsAndAnyWord(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"lov*", "l?ve", "l[ai]ve"} {
		q := kjvParseQuery(query)
		if len(q.include) != 1 || q.include[0].kind != kjvNodeWildcard {
			t.Errorf("parse %q = %+v, want one wildcard node", query, q.include)
			continue
		}
		if q.include[0].pattern != query {
			t.Errorf("parse %q kept pattern %q", query, q.include[0].pattern)
		}
	}
	if q := kjvParseQuery("lov*"); !q.include[0].regexp.MatchString("loved") {
		t.Error("the wildcard node did not compile a working regexp")
	}

	q := kjvParseQuery(`"the * of God"`)
	if len(q.include) != 1 || q.include[0].kind != kjvNodePhrase {
		t.Fatalf("parse any-word phrase = %+v", q.include)
	}
	if q.include[0].words[1].kind != kjvNodeAnyWord {
		t.Errorf("the * slot is %+v, want anyWord", q.include[0].words[1])
	}
	if got := kjvParseQuery("the * of God").include; len(got) < 1 {
		t.Errorf("a standalone * between words produced %+v", got)
	}
}

// TestKJVParseQueryExclude asserts -word and -"phrase" become exclude terms.
func TestKJVParseQueryExclude(t *testing.T) {
	t.Parallel()

	q := kjvParseQuery("love -Satan")
	if len(q.include) != 1 || q.include[0].value != "love" {
		t.Errorf("include = %+v", q.include)
	}
	if len(q.exclude) != 1 || q.exclude[0].value != "satan" {
		t.Errorf("exclude = %+v", q.exclude)
	}
	q = kjvParseQuery(`love -"the devil"`)
	if len(q.exclude) != 1 || q.exclude[0].kind != kjvNodePhrase {
		t.Errorf("exclude phrase = %+v", q.exclude)
	}
	q = kjvParseQuery("-Satan")
	if len(q.include) != 0 || len(q.exclude) != 1 {
		t.Errorf("exclude without include = %+v / %+v", q.include, q.exclude)
	}
}

// TestKJVParseQueryCombined asserts the reference's combined expression, and the
// edge cases: empty input, stray pipes, and repeated spaces.
func TestKJVParseQueryCombined(t *testing.T) {
	t.Parallel()

	q := kjvParseQuery(`"the love of God" -hate love|charity`)
	if len(q.include) != 2 || q.include[0].kind != kjvNodePhrase || q.include[1].kind != kjvNodeOr {
		t.Errorf("include = %+v", q.include)
	}
	if len(q.exclude) != 1 || q.exclude[0].value != "hate" {
		t.Errorf("exclude = %+v", q.exclude)
	}
	q = kjvParseQuery(`lov* "of God"`)
	if len(q.include) != 2 || q.include[0].kind != kjvNodeWildcard || q.include[1].kind != kjvNodePhrase {
		t.Errorf("wildcard plus phrase = %+v", q.include)
	}
	for _, query := range []string{"", "   "} {
		q := kjvParseQuery(query)
		if len(q.include) != 0 || len(q.exclude) != 0 {
			t.Errorf("parse %q = %+v / %+v, want empty", query, q.include, q.exclude)
		}
	}
	if got := kjvParseQuery("|love").include; len(got) != 1 || got[0].value != "love" {
		t.Errorf("a leading pipe produced %+v", got)
	}
	if got := kjvParseQuery("love|").include; len(got) != 1 || got[0].value != "love" {
		t.Errorf("a trailing pipe produced %+v", got)
	}
	if got := kjvParseQuery("love    god").include; len(got) != 2 {
		t.Errorf("repeated spaces produced %+v", got)
	}
}

// TestKJVParseQueryHyphenatedWord asserts a hyphenated word is matched the way
// the verse text is split: "well-beloved" is the consecutive words "well" and
// "beloved", not one unmatchable token.
func TestKJVParseQueryHyphenatedWord(t *testing.T) {
	t.Parallel()

	q := kjvParseQuery("well-beloved")
	if len(q.include) != 1 || q.include[0].kind != kjvNodePhrase {
		t.Fatalf("parse well-beloved = %+v, want one phrase node", q.include)
	}
	if got := q.include[0].words; len(got) != 2 || got[0].value != "well" || got[1].value != "beloved" {
		t.Errorf("well-beloved words = %+v", got)
	}
	words := kjvVerseWords("the well-beloved")
	if !kjvVerseMatchesQuery(words, q) {
		t.Error("the hyphenated query did not match the verse text")
	}
}

// TestKJVEvaluator asserts the evaluator's semantics: every include term must
// match, no exclude term may match, phrases are consecutive, and an any-word
// slot consumes exactly one word.
func TestKJVEvaluator(t *testing.T) {
	t.Parallel()

	words := kjvVerseWords("For God so loved the world, that he gave his only begotten Son")

	cases := []struct {
		name  string
		query string
		match bool
	}{
		{name: "single word", query: "loved", match: true},
		{name: "single word absent", query: "hate", match: false},
		{name: "AND both present", query: "god world", match: true},
		{name: "AND one absent", query: "god hate", match: false},
		{name: "phrase consecutive", query: `"God so loved"`, match: true},
		{name: "phrase not consecutive", query: `"loved God"`, match: false},
		{name: "OR first", query: "hate|loved", match: true},
		{name: "OR second", query: "hate|charity", match: false},
		{name: "exclude absent", query: "loved -hate", match: true},
		{name: "exclude present", query: "loved -world", match: false},
		{name: "wildcard", query: "lov*", match: true},
		{name: "wildcard miss", query: "hat*", match: false},
		{name: "any-word slot", query: `"God * loved"`, match: true},
		{name: "any-word needs one word", query: `"God loved"`, match: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := kjvVerseMatchesQuery(words, kjvParseQuery(tt.query)); got != tt.match {
				t.Errorf("kjvVerseMatchesQuery(%q) = %v, want %v", tt.query, got, tt.match)
			}
		})
	}
}

// TestKJVHasSpecialSyntax ports the reference detection: quotes, pipes,
// wildcards, brackets, and the exclude operator are special; a mid-word hyphen
// is not.
func TestKJVHasSpecialSyntax(t *testing.T) {
	t.Parallel()

	special := []string{`"love"`, "love|charity", "lov*", "l?ve", "l[ai]ve", "love -hate",
		"-hate", "well-beloved -hate"}
	plain := []string{"love", "love god", "well-beloved", "well-known", "two-edged"}
	for _, query := range special {
		if !kjvHasSpecialSyntax(query) {
			t.Errorf("kjvHasSpecialSyntax(%q) = false, want true", query)
		}
	}
	for _, query := range plain {
		if kjvHasSpecialSyntax(query) {
			t.Errorf("kjvHasSpecialSyntax(%q) = true, want false", query)
		}
	}
}

// TestKJVLooksLikeRegexp asserts the detector the command uses to route a query
// like "love.*life" to the raw-regexp path, while the KJVCanOpener wildcards
// stay on the word-query path.
func TestKJVLooksLikeRegexp(t *testing.T) {
	t.Parallel()

	regexish := []string{"love.*life", "^In the beginning", "(love|charity)", "a+b", "x{2}", `\blove`}
	plain := []string{"love", "love of god", "lov*", "l?ve", "l[ai]ve", "love|charity", "", "love -hate"}
	for _, query := range regexish {
		if !kjvLooksLikeRegexp(query) {
			t.Errorf("kjvLooksLikeRegexp(%q) = false, want true", query)
		}
	}
	for _, query := range plain {
		if kjvLooksLikeRegexp(query) {
			t.Errorf("kjvLooksLikeRegexp(%q) = true, want false", query)
		}
	}
}
