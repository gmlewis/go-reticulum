// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the KJV book canon and the fuzzy reference parser the kjv
// command uses to turn free-form input like "jn3:16", "Psalm 23:1-6", "ps23",
// or "1 jn 2 1" into a concrete book, chapter, and verse.
//
// The tables and the parser are a Go port of the reference implementation in
// kmjv-ref: src/utils/bibleBooks.ts (the canon and chapter counts),
// src/data/kjv-bible.ts (the text-file abbreviation map and the Psalm spelling
// alias), and src/utils/bibleRefSearch.ts (the parse-and-rank algorithm). The
// port keeps the reference's parse priority order and its scoring, so the same
// input resolves to the same verse.

package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// kjvBook is one book of the Bible.
type kjvBook struct {
	// Name is the canonical full name, which is what every reply is labeled
	// with ("John 3:16", "Psalms 23:1").
	Name string
	// Abbr is the canonical abbreviation used in search input ("Gen", "1Cor").
	Abbr string
	// Testament is "old" or "new".
	Testament string
	// Chapters is the number of chapters in the book, used to reject an
	// impossible reference before it reaches the data.
	Chapters int
}

// kjvBooks is the 66-book King James canon in order, with the chapter counts the
// reference implementation validates against.
var kjvBooks = []kjvBook{
	{Name: "Genesis", Abbr: "Gen", Testament: "old", Chapters: 50},
	{Name: "Exodus", Abbr: "Ex", Testament: "old", Chapters: 40},
	{Name: "Leviticus", Abbr: "Lev", Testament: "old", Chapters: 27},
	{Name: "Numbers", Abbr: "Num", Testament: "old", Chapters: 36},
	{Name: "Deuteronomy", Abbr: "Deut", Testament: "old", Chapters: 34},
	{Name: "Joshua", Abbr: "Josh", Testament: "old", Chapters: 24},
	{Name: "Judges", Abbr: "Judg", Testament: "old", Chapters: 21},
	{Name: "Ruth", Abbr: "Ruth", Testament: "old", Chapters: 4},
	{Name: "1 Samuel", Abbr: "1Sam", Testament: "old", Chapters: 31},
	{Name: "2 Samuel", Abbr: "2Sam", Testament: "old", Chapters: 24},
	{Name: "1 Kings", Abbr: "1Kgs", Testament: "old", Chapters: 22},
	{Name: "2 Kings", Abbr: "2Kgs", Testament: "old", Chapters: 25},
	{Name: "1 Chronicles", Abbr: "1Chr", Testament: "old", Chapters: 29},
	{Name: "2 Chronicles", Abbr: "2Chr", Testament: "old", Chapters: 36},
	{Name: "Ezra", Abbr: "Ezra", Testament: "old", Chapters: 10},
	{Name: "Nehemiah", Abbr: "Neh", Testament: "old", Chapters: 13},
	{Name: "Esther", Abbr: "Esth", Testament: "old", Chapters: 10},
	{Name: "Job", Abbr: "Job", Testament: "old", Chapters: 42},
	{Name: "Psalms", Abbr: "Ps", Testament: "old", Chapters: 150},
	{Name: "Proverbs", Abbr: "Prov", Testament: "old", Chapters: 31},
	{Name: "Ecclesiastes", Abbr: "Eccl", Testament: "old", Chapters: 12},
	{Name: "Song of Solomon", Abbr: "Song", Testament: "old", Chapters: 8},
	{Name: "Isaiah", Abbr: "Isa", Testament: "old", Chapters: 66},
	{Name: "Jeremiah", Abbr: "Jer", Testament: "old", Chapters: 52},
	{Name: "Lamentations", Abbr: "Lam", Testament: "old", Chapters: 5},
	{Name: "Ezekiel", Abbr: "Ezek", Testament: "old", Chapters: 48},
	{Name: "Daniel", Abbr: "Dan", Testament: "old", Chapters: 12},
	{Name: "Hosea", Abbr: "Hos", Testament: "old", Chapters: 14},
	{Name: "Joel", Abbr: "Joel", Testament: "old", Chapters: 3},
	{Name: "Amos", Abbr: "Amos", Testament: "old", Chapters: 9},
	{Name: "Obadiah", Abbr: "Obad", Testament: "old", Chapters: 1},
	{Name: "Jonah", Abbr: "Jonah", Testament: "old", Chapters: 4},
	{Name: "Micah", Abbr: "Mic", Testament: "old", Chapters: 7},
	{Name: "Nahum", Abbr: "Nah", Testament: "old", Chapters: 3},
	{Name: "Habakkuk", Abbr: "Hab", Testament: "old", Chapters: 3},
	{Name: "Zephaniah", Abbr: "Zeph", Testament: "old", Chapters: 3},
	{Name: "Haggai", Abbr: "Hag", Testament: "old", Chapters: 2},
	{Name: "Zechariah", Abbr: "Zech", Testament: "old", Chapters: 14},
	{Name: "Malachi", Abbr: "Mal", Testament: "old", Chapters: 4},
	{Name: "Matthew", Abbr: "Matt", Testament: "new", Chapters: 28},
	{Name: "Mark", Abbr: "Mark", Testament: "new", Chapters: 16},
	{Name: "Luke", Abbr: "Luke", Testament: "new", Chapters: 24},
	{Name: "John", Abbr: "John", Testament: "new", Chapters: 21},
	{Name: "Acts", Abbr: "Acts", Testament: "new", Chapters: 28},
	{Name: "Romans", Abbr: "Rom", Testament: "new", Chapters: 16},
	{Name: "1 Corinthians", Abbr: "1Cor", Testament: "new", Chapters: 16},
	{Name: "2 Corinthians", Abbr: "2Cor", Testament: "new", Chapters: 13},
	{Name: "Galatians", Abbr: "Gal", Testament: "new", Chapters: 6},
	{Name: "Ephesians", Abbr: "Eph", Testament: "new", Chapters: 6},
	{Name: "Philippians", Abbr: "Phil", Testament: "new", Chapters: 4},
	{Name: "Colossians", Abbr: "Col", Testament: "new", Chapters: 4},
	{Name: "1 Thessalonians", Abbr: "1Thes", Testament: "new", Chapters: 5},
	{Name: "2 Thessalonians", Abbr: "2Thes", Testament: "new", Chapters: 3},
	{Name: "1 Timothy", Abbr: "1Tim", Testament: "new", Chapters: 6},
	{Name: "2 Timothy", Abbr: "2Tim", Testament: "new", Chapters: 4},
	{Name: "Titus", Abbr: "Titus", Testament: "new", Chapters: 3},
	{Name: "Philemon", Abbr: "Phlm", Testament: "new", Chapters: 1},
	{Name: "Hebrews", Abbr: "Heb", Testament: "new", Chapters: 13},
	{Name: "James", Abbr: "Jas", Testament: "new", Chapters: 5},
	{Name: "1 Peter", Abbr: "1Pet", Testament: "new", Chapters: 5},
	{Name: "2 Peter", Abbr: "2Pet", Testament: "new", Chapters: 3},
	{Name: "1 John", Abbr: "1John", Testament: "new", Chapters: 5},
	{Name: "2 John", Abbr: "2John", Testament: "new", Chapters: 1},
	{Name: "3 John", Abbr: "3John", Testament: "new", Chapters: 1},
	{Name: "Jude", Abbr: "Jude", Testament: "new", Chapters: 1},
	{Name: "Revelation", Abbr: "Rev", Testament: "new", Chapters: 22},
}

// kjvAbbr is one abbreviation from the KJV text file's own vocabulary. Those
// abbreviations differ from the canonical ones ("Exo" rather than "Ex", "1Sm"
// rather than "1Sam"), so they are carried as a second table.
type kjvAbbr struct {
	// Abbr is the abbreviation the text file writes.
	Abbr string
	// Name is the canonical book name the abbreviation means.
	Name string
}

// kjvTextAbbrs is the text-file abbreviation table, in its own order.
var kjvTextAbbrs = []kjvAbbr{
	{"Ge", "Genesis"}, {"Exo", "Exodus"}, {"Lev", "Leviticus"}, {"Num", "Numbers"},
	{"Deu", "Deuteronomy"}, {"Josh", "Joshua"}, {"Jdgs", "Judges"}, {"Ruth", "Ruth"},
	{"1Sm", "1 Samuel"}, {"2Sm", "2 Samuel"}, {"1Ki", "1 Kings"}, {"2Ki", "2 Kings"},
	{"1Chr", "1 Chronicles"}, {"2Chr", "2 Chronicles"}, {"Ezra", "Ezra"},
	{"Neh", "Nehemiah"}, {"Est", "Esther"}, {"Job", "Job"}, {"Psa", "Psalms"},
	{"Prv", "Proverbs"}, {"Eccl", "Ecclesiastes"}, {"SSol", "Song of Solomon"},
	{"Isa", "Isaiah"}, {"Jer", "Jeremiah"}, {"Lam", "Lamentations"},
	{"Eze", "Ezekiel"}, {"Dan", "Daniel"}, {"Hos", "Hosea"}, {"Joel", "Joel"},
	{"Amos", "Amos"}, {"Obad", "Obadiah"}, {"Jonah", "Jonah"}, {"Mic", "Micah"},
	{"Nahum", "Nahum"}, {"Hab", "Habakkuk"}, {"Zep", "Zephaniah"},
	{"Hag", "Haggai"}, {"Zec", "Zechariah"}, {"Mal", "Malachi"},
	{"Mat", "Matthew"}, {"Mark", "Mark"}, {"Luke", "Luke"}, {"John", "John"},
	{"Acts", "Acts"}, {"Rom", "Romans"}, {"1Cor", "1 Corinthians"},
	{"2Cor", "2 Corinthians"}, {"Gal", "Galatians"}, {"Eph", "Ephesians"},
	{"Phi", "Philippians"}, {"Col", "Colossians"}, {"1Th", "1 Thessalonians"},
	{"2Th", "2 Thessalonians"}, {"1Tim", "1 Timothy"}, {"2Tim", "2 Timothy"},
	{"Titus", "Titus"}, {"Phmn", "Philemon"}, {"Heb", "Hebrews"}, {"Jas", "James"},
	{"1Pet", "1 Peter"}, {"2Pet", "2 Peter"}, {"1Jn", "1 John"}, {"2Jn", "2 John"},
	{"3Jn", "3 John"}, {"Jude", "Jude"}, {"Rev", "Revelation"},
}

// kjvBookByAbbr maps a text-file abbreviation to its canonical book name.
var kjvBookByAbbr = func() map[string]string {
	m := make(map[string]string, len(kjvTextAbbrs))
	for _, entry := range kjvTextAbbrs {
		m[entry.Abbr] = entry.Name
	}
	return m
}()

// kjvBookChapters maps a canonical book name to its chapter count.
var kjvBookChapters = func() map[string]int {
	m := make(map[string]int, len(kjvBooks))
	for _, b := range kjvBooks {
		m[b.Name] = b.Chapters
	}
	return m
}()

// kjvExtraAliases are the short forms a human types that are not already a book
// name, a canonical abbreviation, or a text-file abbreviation. The list matches
// the reference implementation's, because those are the aliases its tests pinned.
var kjvExtraAliases = [][2]string{
	{"jn", "John"}, {"jnh", "Jonah"}, {"jon", "Jonah"},
	{"ps", "Psalms"}, {"psa", "Psalms"}, {"pslm", "Psalms"},
	{"pr", "Proverbs"}, {"prv", "Proverbs"}, {"prov", "Proverbs"},
	{"ec", "Ecclesiastes"}, {"ecc", "Ecclesiastes"},
	{"ss", "Song of Solomon"}, {"sg", "Song of Solomon"}, {"sos", "Song of Solomon"},
	{"sn", "Song of Solomon"},
	{"1jn", "1 John"}, {"2jn", "2 John"}, {"3jn", "3 John"},
	{"1jo", "1 John"}, {"2jo", "2 John"}, {"3jo", "3 John"},
	{"1j", "1 John"}, {"2j", "2 John"}, {"3j", "3 John"},
	{"1pet", "1 Peter"}, {"2pet", "2 Peter"},
	{"1pe", "1 Peter"}, {"2pe", "2 Peter"},
	{"1p", "1 Peter"}, {"2p", "2 Peter"},
	{"1th", "1 Thessalonians"}, {"2th", "2 Thessalonians"},
	{"1thes", "1 Thessalonians"}, {"2thes", "2 Thessalonians"},
	{"1tim", "1 Timothy"}, {"2tim", "2 Timothy"},
	{"1ti", "1 Timothy"}, {"2ti", "2 Timothy"},
	{"1cor", "1 Corinthians"}, {"2cor", "2 Corinthians"},
	{"1co", "1 Corinthians"}, {"2co", "2 Corinthians"},
	{"1c", "1 Corinthians"}, {"2c", "2 Corinthians"},
	{"1sam", "1 Samuel"}, {"2sam", "2 Samuel"},
	{"1sa", "1 Samuel"}, {"2sa", "2 Samuel"},
	{"1s", "1 Samuel"}, {"2s", "2 Samuel"},
	{"1ki", "1 Kings"}, {"2ki", "2 Kings"},
	{"1kg", "1 Kings"}, {"2kg", "2 Kings"},
	{"1k", "1 Kings"}, {"2k", "2 Kings"},
	{"1chr", "1 Chronicles"}, {"2chr", "2 Chronicles"},
	{"1ch", "1 Chronicles"}, {"2ch", "2 Chronicles"},
	{"1cr", "1 Chronicles"}, {"2cr", "2 Chronicles"},
	{"mt", "Matthew"}, {"mk", "Mark"}, {"lk", "Luke"},
	{"ro", "Romans"}, {"ac", "Acts"}, {"rv", "Revelation"},
	{"rev", "Revelation"}, {"re", "Revelation"},
	{"jam", "James"}, {"jm", "James"}, {"ja", "James"},
	{"heb", "Hebrews"},
	{"ge", "Genesis"}, {"ex", "Exodus"}, {"lv", "Leviticus"},
	{"nm", "Numbers"}, {"dt", "Deuteronomy"},
	{"jos", "Joshua"}, {"jdg", "Judges"},
	{"ru", "Ruth"}, {"jb", "Job"},
	{"is", "Isaiah"}, {"je", "Jeremiah"},
	{"ez", "Ezekiel"}, {"dn", "Daniel"},
	{"hs", "Hosea"}, {"jl", "Joel"},
	{"am", "Amos"}, {"ob", "Obadiah"},
	{"mi", "Micah"}, {"na", "Nahum"},
	{"hk", "Habakkuk"}, {"hb", "Habakkuk"},
	{"zp", "Zephaniah"}, {"hg", "Haggai"},
	{"zc", "Zechariah"}, {"ml", "Malachi"},
	{"jd", "Jude"},
	{"neh", "Nehemiah"}, {"est", "Esther"}, {"esth", "Esther"},
	{"lam", "Lamentations"}, {"la", "Lamentations"},
	{"ga", "Galatians"}, {"gal", "Galatians"},
	{"eph", "Ephesians"}, {"ep", "Ephesians"},
	{"php", "Philippians"}, {"ph", "Philippians"},
	{"col", "Colossians"}, {"co", "Colossians"},
	{"tit", "Titus"}, {"phm", "Philemon"},
	{"phlm", "Philemon"}, {"phn", "Philemon"},
}

// kjvBookVariants maps a normalized variant (lowercase, no spaces) to the
// canonical book name, and kjvVariantOrder preserves the insertion order so
// fuzzy prefix matching is deterministic rather than map-order random.
var (
	kjvBookVariants map[string]string
	kjvVariantOrder []string
)

// kjvBuildBookVariants fills the variant table. It runs once at start-up, in the
// same order as the reference implementation: full names and canonical
// abbreviations, then the text-file abbreviations, then the extra aliases.
func kjvBuildBookVariants() {
	kjvBookVariants = make(map[string]string, 256)
	add := func(variant, name string) {
		key := kjvNormalizeBookKey(variant)
		if key == "" {
			return
		}
		if _, exists := kjvBookVariants[key]; exists {
			return
		}
		kjvBookVariants[key] = name
		kjvVariantOrder = append(kjvVariantOrder, key)
	}
	for _, b := range kjvBooks {
		add(b.Name, b.Name)
		add(b.Abbr, b.Name)
	}
	for _, entry := range kjvTextAbbrs {
		add(entry.Abbr, entry.Name)
	}
	for _, alias := range kjvExtraAliases {
		add(alias[0], alias[1])
	}
}

func init() {
	kjvBuildBookVariants()
}

// kjvNormalizeBookKey lowercases a book spelling and removes its whitespace, so
// "1 John", "1john", and "1 JOHN" share one key.
func kjvNormalizeBookKey(book string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, book)
}

// kjvBookNameFromAbbr resolves a text-file abbreviation to a canonical name.
func kjvBookNameFromAbbr(abbr string) (string, bool) {
	name, ok := kjvBookByAbbr[abbr]
	return name, ok
}

// kjvRefSource records which parse produced a match, which in turn decides its
// score: a reference written with a space outranks the compact form.
type kjvRefSource int

const (
	kjvSourceSpaced kjvRefSource = iota
	kjvSourceCompact
)

// kjvParsedRef is one candidate parse of a reference query.
type kjvParsedRef struct {
	bookQuery string
	chapter   int
	verse     int
	verseEnd  int
	source    kjvRefSource
}

// kjvRefMatch is one validated reference, ready to be looked up in the text.
type kjvRefMatch struct {
	// Book is the canonical book name.
	Book string
	// Chapter and Verse are the start of the reference.
	Chapter int
	Verse   int
	// VerseEnd is the last verse of a range, or 0 for a single verse.
	VerseEnd int
	// Score ranks a match against the other parses of the same query.
	Score int
}

// Reference renders the match the way a reply labels it: "John 3:16" for a
// single verse, "Psalms 23:1-6" for a range.
func (m kjvRefMatch) Reference() string {
	return kjvBuildRefString(m.Book, m.Chapter, m.Verse, m.VerseEnd)
}

// kjvBuildRefString renders a canonical reference.
func kjvBuildRefString(book string, chapter, verse, verseEnd int) string {
	if verseEnd != 0 && verseEnd != verse {
		return book + " " + strconv.Itoa(chapter) + ":" + strconv.Itoa(verse) + "-" + strconv.Itoa(verseEnd)
	}
	return book + " " + strconv.Itoa(chapter) + ":" + strconv.Itoa(verse)
}

// The reference shapes, tried in priority order. The non-greedy book group lets
// the chapter and verse digits be split off the end of a compact reference.
var (
	kjvReSpacedRange      = regexp.MustCompile(`^(.+?)\s+(\d+):(\d+)-(\d+)$`)
	kjvReSpacedVerse      = regexp.MustCompile(`^(.+?)\s+(\d+):(\d+)$`)
	kjvReSpacedSpaceRange = regexp.MustCompile(`^(.+?)\s+(\d+)\s+(\d+)-(\d+)$`)
	kjvReSpacedTwoNums    = regexp.MustCompile(`^(.+?)\s+(\d+)\s+(\d+)$`)
	kjvReSpacedChapter    = regexp.MustCompile(`^(.+?)\s+(\d+)$`)
	kjvReCompactRange     = regexp.MustCompile(`^(.+?)(\d+):(\d+)-(\d+)$`)
	kjvReCompactVerse     = regexp.MustCompile(`^(.+?)(\d+):(\d+)$`)
	kjvReCompactChapter   = regexp.MustCompile(`^(.+?)(\d+)$`)
)

// kjvTryParses returns every candidate parse of a reference query, in priority
// order. The bare-book parse is always appended last, so a query that is only a
// book name resolves to that book's first verse.
func kjvTryParses(input string) []kjvParsedRef {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return nil
	}
	var parses []kjvParsedRef
	add := func(m []string, verseEndIdx int, source kjvRefSource) {
		if m == nil {
			return
		}
		chapter, ok := kjvAtoi(m[2])
		if !ok {
			return
		}
		verse, ok := kjvAtoi(m[3])
		if !ok {
			return
		}
		parse := kjvParsedRef{bookQuery: m[1], chapter: chapter, verse: verse, source: source}
		if verseEndIdx > 0 {
			end, ok := kjvAtoi(m[verseEndIdx])
			if !ok {
				return
			}
			parse.verseEnd = end
		}
		parses = append(parses, parse)
	}
	// Spaced forms.
	add(kjvReSpacedRange.FindStringSubmatch(s), 4, kjvSourceSpaced)
	add(kjvReSpacedVerse.FindStringSubmatch(s), 0, kjvSourceSpaced)
	add(kjvReSpacedSpaceRange.FindStringSubmatch(s), 4, kjvSourceSpaced)
	add(kjvReSpacedTwoNums.FindStringSubmatch(s), 0, kjvSourceSpaced)
	if m := kjvReSpacedChapter.FindStringSubmatch(s); m != nil {
		if chapter, ok := kjvAtoi(m[2]); ok {
			parses = append(parses, kjvParsedRef{bookQuery: m[1], chapter: chapter, verse: 1, source: kjvSourceSpaced})
		}
	}
	// A query that is nothing but a book name resolves to its first verse.
	parses = append(parses, kjvParsedRef{bookQuery: s, chapter: 1, verse: 1, source: kjvSourceSpaced})

	// Compact forms, tried on the same string with its whitespace removed.
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	add(kjvReCompactRange.FindStringSubmatch(compact), 4, kjvSourceCompact)
	add(kjvReCompactVerse.FindStringSubmatch(compact), 0, kjvSourceCompact)
	if m := kjvReCompactChapter.FindStringSubmatch(compact); m != nil {
		if chapter, ok := kjvAtoi(m[2]); ok {
			parses = append(parses, kjvParsedRef{bookQuery: m[1], chapter: chapter, verse: 1, source: kjvSourceCompact})
		}
	}
	return parses
}

// kjvValidRef reports whether a reference is possible at all: a known book and
// a chapter inside that book. The verse itself is validated against the data,
// which is the only place that knows how many verses a chapter has.
func kjvValidRef(book string, chapter, verse, verseEnd int) bool {
	maxChapters, ok := kjvBookChapters[book]
	if !ok {
		return false
	}
	if chapter < 1 || chapter > maxChapters {
		return false
	}
	if verse < 1 {
		return false
	}
	if verseEnd != 0 && verseEnd < verse {
		return false
	}
	return true
}

// searchBibleReferences parses a free-form query and returns the ranked
// references it could mean, at most maxResults of them. An exact match (the
// query names the book) outranks a fuzzy prefix, and a spaced reference
// outranks the compact one.
func searchBibleReferences(query string, maxResults int) []kjvRefMatch {
	if maxResults <= 0 {
		return nil
	}
	parses := kjvTryParses(query)
	if len(parses) == 0 {
		return nil
	}

	var results []kjvRefMatch
	seen := map[string]bool{}
	addMatch := func(book string, parse kjvParsedRef, exact bool) {
		if !kjvValidRef(book, parse.chapter, parse.verse, parse.verseEnd) {
			return
		}
		ref := kjvBuildRefString(book, parse.chapter, parse.verse, parse.verseEnd)
		if seen[ref] {
			return
		}
		seen[ref] = true
		score := 60
		if exact {
			score = 90
		}
		if parse.source == kjvSourceSpaced {
			score += 10
		}
		results = append(results, kjvRefMatch{
			Book:     book,
			Chapter:  parse.chapter,
			Verse:    parse.verse,
			VerseEnd: parse.verseEnd,
			Score:    score,
		})
	}

	for _, parse := range parses {
		normalized := kjvNormalizeBookKey(parse.bookQuery)
		if normalized == "" {
			continue
		}
		if book, ok := kjvBookVariants[normalized]; ok {
			addMatch(book, parse, true)
		}
		if len(normalized) < 2 {
			continue
		}
		for _, variant := range kjvVariantOrder {
			if variant == normalized || !strings.HasPrefix(variant, normalized) {
				continue
			}
			addMatch(kjvBookVariants[variant], parse, false)
		}
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return results
}

// kjvQueryHasDigit reports whether a query names a chapter or verse. A query
// without a digit is only treated as a reference when it is a book's full name;
// otherwise "job", "mark", "am", and "is" would stop being searchable words.
func kjvQueryHasDigit(query string) bool {
	for i := range len(query) {
		if query[i] >= '0' && query[i] <= '9' {
			return true
		}
	}
	return false
}

// kjvReferenceQuery reports whether query should be treated as a reference for
// the purpose of the command. A query with a number is always a reference; a
// query without one is a reference only when it spells out a book's full name.
func kjvReferenceQuery(query string) bool {
	if kjvQueryHasDigit(query) {
		return true
	}
	normalized := kjvNormalizeBookKey(query)
	if normalized == "" {
		return false
	}
	for _, b := range kjvBooks {
		if kjvNormalizeBookKey(b.Name) == normalized {
			return true
		}
	}
	return false
}

// kjvAtoi parses a run of digits from a reference, reporting an overflow rather
// than wrapping to a different verse.
func kjvAtoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
