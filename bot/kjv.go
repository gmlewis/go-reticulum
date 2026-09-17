// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the kjv command: Bible verse lookup and full-text search over
// a King James Version text file the operator names with kjv_txt_file.
//
// The text file is read-only and is parsed once, lazily, on the first kjv
// command; a bot that never uses the command never touches the file. One line
// carries one verse in the shape the reference data uses:
//
//	John3:16 For God so loved the world, that he gave his only begotten Son, ...
//
// An argument list is interpreted in one of three ways, in this order:
//
//   - a Bible reference, however loosely spelled ("jn3:16", "Psalm 23:1-6",
//     "ps23", "1 jn 2 1"), which is looked up;
//   - a raw regular expression, when the text contains a metacharacter the word
//     syntax does not claim ("love.*life");
//   - bare words, which search for every verse containing them.
//
// Every answer is one complete verse per line, labeled with its full canonical
// name: "John 3:16: For God so loved the world, ...". Long lines are split by
// the reply policy, and the whole reply is bounded by max_reply_lines.

package bot

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// KJV command constants.
const (
	// kjvUsage is the usage line for the kjv command.
	kjvUsage = "kjv <reference | words | regex>"
	// kjvNotConfiguredLine is the answer when the operator has set no
	// kjv_txt_file. It names the key so the fix is obvious.
	kjvNotConfiguredLine = "kjv is not configured: set kjv_txt_file in config.toml to enable it"
	// kjvUnreadableLine is the answer when the configured file cannot be read.
	// It never repeats the path into a room; the log has the detail.
	kjvUnreadableLine = "kjv: the configured Bible text file cannot be read (the bot log says why)"
	// maxKJVQueryBytes bounds one query. It is deliberately generous: a
	// reference is short and a search is a few words, and a longer argument is
	// not a query.
	maxKJVQueryBytes = 256
	// maxKJVQueryEchoBytes bounds the query text repeated in a no-match reply.
	maxKJVQueryEchoBytes = 64
	// maxKJVFileBytes bounds the text file the bot will read, so a
	// misconfigured path cannot exhaust memory.
	maxKJVFileBytes = 64 << 20
	// maxKJVReferenceMatches bounds how many fuzzy reference candidates are
	// looked up, so a two-letter query cannot resolve to dozens of books.
	maxKJVReferenceMatches = 4
)

// kjvReLine matches one verse line of the King James text file. The book part
// allows a leading digit for the numbered books ("1Jn3:16").
var kjvReLine = regexp.MustCompile(`^(\d?[A-Za-z]+)(\d+):(\d+) (.+)$`)

// kjvVerse is one verse: its canonical book, its place, and its text.
type kjvVerse struct {
	// Book is the canonical book name, which is what every reply is labeled
	// with.
	Book string
	// Chapter and Verse locate the verse within the book.
	Chapter int
	Verse   int
	// Text is the verse text exactly as the file carries it.
	Text string
}

// Reference renders the verse's canonical reference, for example "John 3:16".
func (v kjvVerse) Reference() string {
	return kjvBuildRefString(v.Book, v.Chapter, v.Verse, 0)
}

// kjvVerseLabel renders one reply line: the full canonical reference, then the
// verse text.
func kjvVerseLabel(v kjvVerse) string {
	return v.Reference() + ": " + v.Text
}

// kjvStore is the parsed Bible: every verse in file order plus an index by book
// and chapter for lookups.
type kjvStore struct {
	// verses is every verse in file order, which is canonical order and the
	// deterministic tie-break for search results.
	verses []kjvVerse
	// byChapter indexes verses by canonical book name and chapter.
	byChapter map[string]map[int][]kjvVerse
}

// parseKJVLine reads one line of the text file. It reports false for the file's
// header and for anything that is not a verse, so a malformed line is skipped
// rather than turned into an invented verse.
func parseKJVLine(line string) (kjvVerse, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if trimmed == "" {
		return kjvVerse{}, false
	}
	m := kjvReLine.FindStringSubmatch(trimmed)
	if m == nil {
		return kjvVerse{}, false
	}
	book, ok := kjvBookNameFromAbbr(m[1])
	if !ok {
		return kjvVerse{}, false
	}
	chapter, ok := kjvAtoi(m[2])
	if !ok {
		return kjvVerse{}, false
	}
	verse, ok := kjvAtoi(m[3])
	if !ok {
		return kjvVerse{}, false
	}
	return kjvVerse{Book: book, Chapter: chapter, Verse: verse, Text: m[4]}, true
}

// loadKJV reads and parses the whole text file. It is the expensive step, and a
// file with no recognizable verses is an error rather than an empty Bible, so a
// wrong path is reported instead of returning "no verses match" to every query.
func loadKJV(path string) (*kjvStore, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading %v: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%v is a directory, not a Bible text file", path)
	}
	if info.Size() > maxKJVFileBytes {
		return nil, fmt.Errorf("%v is %v bytes, larger than the %v-byte limit", path, info.Size(), maxKJVFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %v: %w", path, err)
	}
	store := &kjvStore{byChapter: make(map[string]map[int][]kjvVerse)}
	for line := range strings.SplitSeq(string(data), "\n") {
		verse, ok := parseKJVLine(line)
		if !ok {
			continue
		}
		store.verses = append(store.verses, verse)
		chapters := store.byChapter[verse.Book]
		if chapters == nil {
			chapters = make(map[int][]kjvVerse)
			store.byChapter[verse.Book] = chapters
		}
		chapters[verse.Chapter] = append(chapters[verse.Chapter], verse)
	}
	if len(store.verses) == 0 {
		return nil, fmt.Errorf("%v holds no recognizable verses", path)
	}
	return store, nil
}

// chapterVerses returns one chapter's verses in order.
func (s *kjvStore) chapterVerses(book string, chapter int) []kjvVerse {
	if s == nil {
		return nil
	}
	return s.byChapter[book][chapter]
}

// verse returns one verse.
func (s *kjvStore) verse(book string, chapter, verse int) (kjvVerse, bool) {
	for _, v := range s.chapterVerses(book, chapter) {
		if v.Verse == verse {
			return v, true
		}
	}
	return kjvVerse{}, false
}

// lookupRefs turns parsed references into the verses they name, in reference
// order. A range contributes every verse it names that the text actually has, so
// a reference to a verse the translation omits is simply skipped. The result is
// bounded by the chapter, never by the reply, so the renderer can say honestly
// how much of a long passage it did not show.
func (s *kjvStore) lookupRefs(refs []kjvRefMatch) []kjvVerse {
	var out []kjvVerse
	seen := map[string]bool{}
	add := func(v kjvVerse) {
		ref := v.Reference()
		if seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, v)
	}
	for _, ref := range refs {
		if ref.VerseEnd > ref.Verse {
			for n := ref.Verse; n <= ref.VerseEnd; n++ {
				if v, ok := s.verse(ref.Book, ref.Chapter, n); ok {
					add(v)
				}
			}
			continue
		}
		if v, ok := s.verse(ref.Book, ref.Chapter, ref.Verse); ok {
			add(v)
		}
	}
	return out
}

// kjvScoredVerse is one search hit with the score it is ranked by.
type kjvScoredVerse struct {
	verse kjvVerse
	score float64
}

// searchKJVVerses interprets a query that is not a strict reference. The order
// matters, and is the one a reader expects:
//
//   - a query that looks like a regular expression is matched as one first;
//   - otherwise the word-query syntax is tried exactly;
//   - a query that found nothing is then tried as a fuzzy book reference, so a
//     bare abbreviation like "gen" resolves to Genesis 1:1 instead of to every
//     verse containing "generation". A word that really occurs in the text
//     ("is", "am") matched exactly and never reaches this step;
//   - a word query is then retried with each term as a prefix ("shepher");
//   - and finally a raw regular expression is tried as a last resort.
//
// Every step returns all of its matches; the reply renderer does the bounding,
// so a truncated answer can still say how much it did not show.
func searchKJVVerses(store *kjvStore, query string) []kjvVerse {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil
	}
	if kjvLooksLikeRegexp(trimmed) {
		if out := store.searchRegexp(trimmed); len(out) > 0 {
			return out
		}
	}
	if out := store.searchWords(trimmed); len(out) > 0 {
		return out
	}
	if refs := searchBibleReferences(trimmed, maxKJVReferenceMatches); len(refs) > 0 {
		if out := store.lookupRefs(refs); len(out) > 0 {
			return out
		}
	}
	if !kjvHasSpecialSyntax(trimmed) {
		if out := store.searchWords(kjvPrefixQuery(trimmed)); len(out) > 0 {
			return out
		}
	}
	if !kjvLooksLikeRegexp(trimmed) {
		if out := store.searchRegexp(trimmed); len(out) > 0 {
			return out
		}
	}
	return nil
}

// searchWords evaluates the word-query syntax against every verse, ranking a
// longer match first and then a shorter verse, which favors a precise hit over a
// verse that merely contains the same words many times.
func (s *kjvStore) searchWords(query string) []kjvVerse {
	parsed := kjvParseQuery(query)
	if len(parsed.include) == 0 && len(parsed.exclude) == 0 {
		return nil
	}
	var hits []kjvScoredVerse
	for _, verse := range s.verses {
		words := kjvVerseWords(verse.Text)
		if !kjvVerseMatchesQuery(words, parsed) {
			continue
		}
		score := float64(len(parsed.include)) + 1/float64(max(len(words), 1))
		hits = append(hits, kjvScoredVerse{verse: verse, score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]kjvVerse, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.verse)
	}
	return out
}

// searchRegexp matches a regular expression against the whole verse text, in
// canonical order. An unparsable pattern matches nothing rather than failing the
// command, because a stray bracket is a search that simply found no verse.
func (s *kjvStore) searchRegexp(pattern string) []kjvVerse {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil
	}
	var out []kjvVerse
	for _, verse := range s.verses {
		if re.MatchString(verse.Text) {
			out = append(out, verse)
		}
	}
	return out
}

// kjvPrefixQuery turns a plain word query into a prefix query, so "shepher"
// finds "shepherd". Terms that already carry wildcard syntax are left alone.
func kjvPrefixQuery(query string) string {
	fields := strings.Fields(query)
	for i, field := range fields {
		if field == "" || field == "|" || field == "-" || strings.HasPrefix(field, "-") ||
			kjvHasWildcardChars(field) {
			continue
		}
		fields[i] = field + "*"
	}
	return strings.Join(fields, " ")
}

// kjvCache loads the Bible once and remembers the outcome, including a failure,
// so an unusable path costs one read rather than one per command.
type kjvCache struct {
	// path is the configured text file; empty means the command is off.
	path string

	mu     sync.Mutex
	loaded bool
	store  *kjvStore
	err    error
}

// newKJVCache builds the lazy loader for one configured path.
func newKJVCache(path string) *kjvCache {
	return &kjvCache{path: strings.TrimSpace(path)}
}

// get returns the parsed Bible, loading it on first use.
func (c *kjvCache) get() (*kjvStore, error) {
	if c == nil {
		return nil, fmt.Errorf("no kjv cache")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.store, c.err
	}
	c.store, c.err = loadKJV(c.path)
	c.loaded = true
	return c.store, c.err
}

// kjvVerseLines renders verses for a reply, one complete labeled verse per line,
// bounded by the reply budget. When more verses matched than fit, the last line
// says how many were not shown, so a truncated answer never looks complete.
func kjvVerseLines(verses []kjvVerse, budget int) []string {
	if len(verses) == 0 {
		return nil
	}
	if budget < 1 {
		budget = 1
	}
	if len(verses) <= budget {
		lines := make([]string, 0, len(verses))
		for _, v := range verses {
			lines = append(lines, kjvVerseLabel(v))
		}
		return lines
	}
	shown := budget - 1
	if shown < 1 {
		// A one-line budget leaves no room for a trailer; the verse itself is
		// more useful than a count.
		shown = budget
	}
	lines := make([]string, 0, budget)
	for _, v := range verses[:shown] {
		lines = append(lines, kjvVerseLabel(v))
	}
	if shown < budget {
		lines = append(lines, fmt.Sprintf("… %v more matching verses not shown", len(verses)-shown))
	}
	return lines
}

// runKJV answers one kjv command.
func (c *commandContext) runKJV() []string {
	query := strings.TrimSpace(c.Args)
	if query == "" || len(query) > maxKJVQueryBytes {
		return []string{"Usage: " + kjvUsage}
	}
	if c.reg.kjv == nil || c.reg.kjv.path == "" {
		return []string{kjvNotConfiguredLine}
	}
	store, err := c.reg.kjv.get()
	if err != nil {
		logf("kjv: %v", err)
		return []string{kjvUnreadableLine}
	}
	budget := c.replyBudget()

	if kjvReferenceQuery(query) {
		refs := searchBibleReferences(query, maxKJVReferenceMatches)
		if len(refs) > 0 {
			// Every verse the reference names is collected and the renderer
			// bounds the reply, so a long range still reports how much of it
			// was not shown instead of looking complete.
			if verses := store.lookupRefs(refs); len(verses) > 0 {
				return kjvVerseLines(verses, budget)
			}
			// A bare book name that this text does not carry is worth a word
			// search; a chapter and verse that does not exist is not.
			if kjvQueryHasDigit(query) {
				return []string{kjvNoSuchVerseLine(query)}
			}
		}
	}

	verses := searchKJVVerses(store, query)
	if len(verses) == 0 {
		return []string{kjvNoMatchLine(query)}
	}
	return kjvVerseLines(verses, budget)
}

// kjvNoSuchVerseLine is the answer to a reference this text has no verse for.
func kjvNoSuchVerseLine(query string) string {
	return "no such verse: " + safeEcho(query, maxKJVQueryEchoBytes)
}

// kjvNoMatchLine is the answer to a search with no matching verse.
func kjvNoMatchLine(query string) string {
	return fmt.Sprintf("no verses match %q", safeEcho(query, maxKJVQueryEchoBytes))
}
