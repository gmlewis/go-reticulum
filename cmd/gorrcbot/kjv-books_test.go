// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
)

// TestKJVBookTablesAreComplete asserts the canon is exactly the 66 books, in
// order, and that every KJV text-file abbreviation resolves to a canonical name.
func TestKJVBookTablesAreComplete(t *testing.T) {
	t.Parallel()

	if len(kjvBooks) != 66 {
		t.Fatalf("kjvBooks has %v entries, want 66", len(kjvBooks))
	}
	seen := map[string]bool{}
	for i, b := range kjvBooks {
		if b.Name == "" || b.Abbr == "" || b.Chapters < 1 {
			t.Errorf("book %v = %+v, want a name, an abbreviation, and chapters", i, b)
		}
		if b.Testament != "old" && b.Testament != "new" {
			t.Errorf("book %q testament = %q, want old or new", b.Name, b.Testament)
		}
		if seen[b.Name] {
			t.Errorf("book %q appears twice", b.Name)
		}
		seen[b.Name] = true
		if got := kjvBookChapters[b.Name]; got != b.Chapters {
			t.Errorf("kjvBookChapters[%q] = %v, want %v", b.Name, got, b.Chapters)
		}
		// Both the full name and the canonical abbreviation are searchable.
		for _, variant := range []string{b.Name, b.Abbr} {
			key := kjvNormalizeBookKey(variant)
			if got, ok := kjvBookVariants[key]; !ok || got != b.Name {
				t.Errorf("kjvBookVariants[%q] = %q, %v; want %q", key, got, ok, b.Name)
			}
		}
	}
	// The 39 Old Testament books come before the 27 New Testament books.
	if kjvBooks[0].Name != "Genesis" || kjvBooks[38].Name != "Malachi" {
		t.Errorf("the Old Testament books are out of order: %v, %v", kjvBooks[0].Name, kjvBooks[38].Name)
	}
	if kjvBooks[39].Name != "Matthew" || kjvBooks[65].Name != "Revelation" {
		t.Errorf("the New Testament books are out of order: %v, %v", kjvBooks[39].Name, kjvBooks[65].Name)
	}
	// The text-file abbreviation table is the other 66 abbreviations, and every
	// one of them names a canonical book.
	if len(kjvTextAbbrs) != 66 {
		t.Fatalf("kjvTextAbbrs has %v entries, want 66", len(kjvTextAbbrs))
	}
	if len(kjvBookByAbbr) != 66 {
		t.Errorf("kjvBookByAbbr has %v entries, want 66", len(kjvBookByAbbr))
	}
	for _, entry := range kjvTextAbbrs {
		if got := kjvBookChapters[entry.Name]; got < 1 {
			t.Errorf("text abbreviation %q names unknown book %q", entry.Abbr, entry.Name)
		}
		if got, ok := kjvBookByAbbr[entry.Abbr]; !ok || got != entry.Name {
			t.Errorf("kjvBookByAbbr[%q] = %q, %v; want %q", entry.Abbr, got, ok, entry.Name)
		}
		// The text abbreviations are searchable aliases too.
		if got, ok := kjvBookVariants[kjvNormalizeBookKey(entry.Abbr)]; !ok || got != entry.Name {
			t.Errorf("kjvBookVariants[%q] = %q, %v; want %q",
				kjvNormalizeBookKey(entry.Abbr), got, ok, entry.Name)
		}
	}
}

// TestKJVBookNameFromAbbr asserts the text-file abbreviations the Bible data
// uses all resolve, and an unknown abbreviation does not.
func TestKJVBookNameFromAbbr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		abbr string
		want string
	}{
		{"Ge", "Genesis"},
		{"1Jn", "1 John"},
		{"3Jn", "3 John"},
		{"Psa", "Psalms"},
		{"SSol", "Song of Solomon"},
		{"Phmn", "Philemon"},
		{"Rev", "Revelation"},
		{"Zzz", ""},
	}
	for _, tt := range cases {
		got, ok := kjvBookNameFromAbbr(tt.abbr)
		if tt.want == "" {
			if ok {
				t.Errorf("kjvBookNameFromAbbr(%q) = %q, true; want not found", tt.abbr, got)
			}
			continue
		}
		if !ok || got != tt.want {
			t.Errorf("kjvBookNameFromAbbr(%q) = %q, %v; want %q", tt.abbr, got, ok, tt.want)
		}
	}
}

// TestSearchBibleReferencesBasicParsing ports the reference parser's basic
// cases: a spaced reference, a compact one, a numbered book, a book and chapter
// with no verse, and a bare book name.
func TestSearchBibleReferencesBasicParsing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		query        string
		wantBook     string
		wantChapter  int
		wantVerse    int
		wantVerseEnd int
		wantRef      string
	}{
		{query: "John 3:16", wantBook: "John", wantChapter: 3, wantVerse: 16, wantRef: "John 3:16"},
		{query: "jn3:16", wantBook: "John", wantChapter: 3, wantVerse: 16, wantRef: "John 3:16"},
		{query: "1 jn 2 1", wantBook: "1 John", wantChapter: 2, wantVerse: 1},
		{query: "1jn2:1", wantBook: "1 John", wantChapter: 2, wantVerse: 1},
		{query: "ps23", wantBook: "Psalms", wantChapter: 23, wantVerse: 1},
		{query: "ps 23", wantBook: "Psalms", wantChapter: 23, wantVerse: 1},
		{query: "1 john", wantBook: "1 John", wantChapter: 1, wantVerse: 1},
		{query: "Genesis 1:1", wantBook: "Genesis", wantChapter: 1, wantVerse: 1},
		{query: "gen1:1", wantBook: "Genesis", wantChapter: 1, wantVerse: 1},
		{query: "Revelation", wantBook: "Revelation", wantChapter: 1, wantVerse: 1},
		{query: "song of solomon 1:1", wantBook: "Song of Solomon", wantChapter: 1, wantVerse: 1},
		{query: "sos1:1", wantBook: "Song of Solomon", wantChapter: 1, wantVerse: 1},
		{query: "JOHN 3:16", wantBook: "John", wantChapter: 3, wantVerse: 16},
		{query: "John 3 16", wantBook: "John", wantChapter: 3, wantVerse: 16},
	}
	for _, tt := range cases {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()
			got := searchBibleReferences(tt.query, 5)
			if len(got) == 0 {
				t.Fatalf("searchBibleReferences(%q) returned nothing", tt.query)
			}
			first := got[0]
			if first.Book != tt.wantBook || first.Chapter != tt.wantChapter || first.Verse != tt.wantVerse {
				t.Errorf("searchBibleReferences(%q)[0] = %v %v:%v, want %v %v:%v",
					tt.query, first.Book, first.Chapter, first.Verse, tt.wantBook, tt.wantChapter, tt.wantVerse)
			}
			if first.VerseEnd != tt.wantVerseEnd {
				t.Errorf("searchBibleReferences(%q)[0].VerseEnd = %v, want %v",
					tt.query, first.VerseEnd, tt.wantVerseEnd)
			}
			if tt.wantRef != "" && first.Reference() != tt.wantRef {
				t.Errorf("searchBibleReferences(%q)[0].Reference() = %q, want %q",
					tt.query, first.Reference(), tt.wantRef)
			}
		})
	}
}

// TestSearchBibleReferencesNumberedAndMultiWordBooks asserts every numbered
// book and the multi-word book names parse, which is what the "1 jn 2 1" shape
// depends on.
func TestSearchBibleReferencesNumberedAndMultiWordBooks(t *testing.T) {
	t.Parallel()

	books := []string{
		"1 Samuel", "2 Samuel", "1 Kings", "2 Kings", "1 Chronicles", "2 Chronicles",
		"1 Corinthians", "2 Corinthians", "1 Thessalonians", "2 Thessalonians",
		"1 Timothy", "2 Timothy", "1 Peter", "2 Peter", "1 John", "2 John", "3 John",
		"Song of Solomon",
	}
	for _, book := range books {
		t.Run(book, func(t *testing.T) {
			t.Parallel()
			got := searchBibleReferences(book+" 1:1", 5)
			if len(got) == 0 {
				t.Fatalf("searchBibleReferences(%q) returned nothing", book+" 1:1")
			}
			if got[0].Book != book {
				t.Errorf("searchBibleReferences(%q)[0].Book = %q, want %q", book+" 1:1", got[0].Book, book)
			}
		})
	}
}

// TestSearchBibleReferencesAliases asserts the short aliases a human actually
// types resolve to their canonical books, and that "1 john" never degrades to
// "John".
func TestSearchBibleReferencesAliases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		abbr string
		want string
	}{
		{"jn", "John"}, {"jnh", "Jonah"}, {"jon", "Jonah"},
		{"ps", "Psalms"}, {"psa", "Psalms"}, {"pslm", "Psalms"},
		{"pr", "Proverbs"}, {"prv", "Proverbs"}, {"prov", "Proverbs"},
		{"ec", "Ecclesiastes"}, {"ecc", "Ecclesiastes"},
		{"ss", "Song of Solomon"}, {"sg", "Song of Solomon"}, {"sos", "Song of Solomon"},
		{"1jn", "1 John"}, {"2jn", "2 John"}, {"3jn", "3 John"},
		{"1j", "1 John"}, {"2j", "2 John"}, {"3j", "3 John"},
		{"1pet", "1 Peter"}, {"1pe", "1 Peter"}, {"1p", "1 Peter"},
		{"1th", "1 Thessalonians"}, {"1thes", "1 Thessalonians"},
		{"1tim", "1 Timothy"}, {"1ti", "1 Timothy"},
		{"1cor", "1 Corinthians"}, {"1co", "1 Corinthians"}, {"1c", "1 Corinthians"},
		{"1sam", "1 Samuel"}, {"1sa", "1 Samuel"}, {"1s", "1 Samuel"},
		{"1ki", "1 Kings"}, {"1kg", "1 Kings"}, {"1k", "1 Kings"},
		{"1chr", "1 Chronicles"}, {"1ch", "1 Chronicles"}, {"1cr", "1 Chronicles"},
		{"mt", "Matthew"}, {"mk", "Mark"}, {"lk", "Luke"},
		{"ro", "Romans"}, {"ac", "Acts"}, {"rv", "Revelation"}, {"rev", "Revelation"},
		{"jam", "James"}, {"jm", "James"}, {"ja", "James"},
		{"ge", "Genesis"}, {"ex", "Exodus"}, {"lv", "Leviticus"},
		{"nm", "Numbers"}, {"dt", "Deuteronomy"}, {"jos", "Joshua"}, {"jdg", "Judges"},
		{"ru", "Ruth"}, {"jb", "Job"}, {"is", "Isaiah"}, {"je", "Jeremiah"},
		{"ez", "Ezekiel"}, {"dn", "Daniel"}, {"hs", "Hosea"}, {"jl", "Joel"},
		{"am", "Amos"}, {"ob", "Obadiah"}, {"mi", "Micah"}, {"na", "Nahum"},
		{"hk", "Habakkuk"}, {"hb", "Habakkuk"}, {"zp", "Zephaniah"}, {"hg", "Haggai"},
		{"zc", "Zechariah"}, {"ml", "Malachi"}, {"jd", "Jude"},
		{"neh", "Nehemiah"}, {"est", "Esther"}, {"esth", "Esther"},
		{"lam", "Lamentations"}, {"la", "Lamentations"},
		{"ga", "Galatians"}, {"gal", "Galatians"}, {"eph", "Ephesians"}, {"ep", "Ephesians"},
		{"php", "Philippians"}, {"ph", "Philippians"}, {"col", "Colossians"}, {"co", "Colossians"},
		{"tit", "Titus"}, {"phm", "Philemon"}, {"phlm", "Philemon"}, {"phn", "Philemon"},
		// The text-file abbreviations are aliases too.
		{"Ge", "Genesis"}, {"Exo", "Exodus"}, {"Jdgs", "Judges"},
		{"1Sm", "1 Samuel"}, {"1Ki", "1 Kings"}, {"Est", "Esther"},
		{"Psa", "Psalms"}, {"Prv", "Proverbs"}, {"SSol", "Song of Solomon"},
		{"Eze", "Ezekiel"}, {"Mat", "Matthew"}, {"Phi", "Philippians"},
		{"Phmn", "Philemon"}, {"2Jn", "2 John"}, {"3Jn", "3 John"},
	}
	for _, tt := range cases {
		t.Run(tt.abbr, func(t *testing.T) {
			t.Parallel()
			got := searchBibleReferences(tt.abbr+" 1:1", 5)
			if len(got) == 0 {
				t.Fatalf("searchBibleReferences(%q) returned nothing", tt.abbr+" 1:1")
			}
			if got[0].Book != tt.want {
				t.Errorf("searchBibleReferences(%q)[0].Book = %q, want %q", tt.abbr+" 1:1", got[0].Book, tt.want)
			}
		})
	}

	if got := searchBibleReferences("1 john", 5); got[0].Book != "1 John" {
		t.Errorf("searchBibleReferences(\"1 john\")[0].Book = %q, want \"1 John\"", got[0].Book)
	}
}

// TestSearchBibleReferencesFuzzyPrefix asserts a prefix of a book name resolves
// to every book it could be, which is the fuzzy half of the parser.
func TestSearchBibleReferencesFuzzyPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		query string
		want  []string
	}{
		{query: "gene1:1", want: []string{"Genesis"}},
		{query: "rev22:21", want: []string{"Revelation"}},
		{query: "phil1:1", want: []string{"Philippians", "Philemon"}},
		{query: "co1:1", want: []string{"Colossians"}},
		{query: "1 c 1:1", want: []string{"1 Corinthians", "1 Chronicles"}},
	}
	for _, tt := range cases {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()
			got := searchBibleReferences(tt.query, 20)
			books := make([]string, 0, len(got))
			for _, m := range got {
				books = append(books, m.Book)
			}
			for _, want := range tt.want {
				if !containsString(books, want) {
					t.Errorf("searchBibleReferences(%q) books = %v, want %q among them", tt.query, books, want)
				}
			}
		})
	}
}

// TestSearchBibleReferencesValidation asserts an impossible chapter or verse is
// never returned, which keeps the command from answering with a verse that does
// not exist.
func TestSearchBibleReferencesValidation(t *testing.T) {
	t.Parallel()

	cases := []string{"John 0:1", "John 99:1", "John 1:0", "Revelation 23:1"}
	for _, query := range cases {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			for _, m := range searchBibleReferences(query, 20) {
				if m.Book == "John" && (m.Chapter == 0 || m.Chapter == 99 || m.Verse == 0) {
					t.Errorf("searchBibleReferences(%q) returned the impossible %v", query, m.Reference())
				}
				if m.Book == "Revelation" && m.Chapter == 23 {
					t.Errorf("searchBibleReferences(%q) returned the impossible %v", query, m.Reference())
				}
			}
		})
	}
	if got := searchBibleReferences("Revelation 22:21", 5); len(got) == 0 || got[0].Reference() != "Revelation 22:21" {
		t.Errorf("searchBibleReferences(\"Revelation 22:21\") = %v, want the last verse", got)
	}
}

// TestSearchBibleReferencesRanges asserts a verse range is parsed, including a
// single-verse range, and a reversed range is rejected.
func TestSearchBibleReferencesRanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		query        string
		wantBook     string
		wantChapter  int
		wantVerse    int
		wantVerseEnd int
		wantRef      string
	}{
		{query: "ps23:1-6", wantBook: "Psalms", wantChapter: 23, wantVerse: 1, wantVerseEnd: 6, wantRef: "Psalms 23:1-6"},
		{query: "Psalms 23:1-6", wantBook: "Psalms", wantChapter: 23, wantVerse: 1, wantVerseEnd: 6, wantRef: "Psalms 23:1-6"},
		{query: "ps 23 1-6", wantBook: "Psalms", wantChapter: 23, wantVerse: 1, wantVerseEnd: 6},
		{query: "John 3:1-3", wantBook: "John", wantChapter: 3, wantVerse: 1, wantVerseEnd: 3, wantRef: "John 3:1-3"},
		{query: "jn3:16-17", wantBook: "John", wantChapter: 3, wantVerse: 16, wantVerseEnd: 17},
		{query: "1cor13:1-13", wantBook: "1 Corinthians", wantChapter: 13, wantVerse: 1, wantVerseEnd: 13, wantRef: "1 Corinthians 13:1-13"},
		{query: "John 3:16-16", wantBook: "John", wantChapter: 3, wantVerse: 16, wantVerseEnd: 16, wantRef: "John 3:16"},
		{query: "song of solomon 1:1-4", wantBook: "Song of Solomon", wantChapter: 1, wantVerse: 1, wantVerseEnd: 4},
		{query: "psal 23:1-6", wantBook: "Psalms", wantChapter: 23, wantVerse: 1, wantVerseEnd: 6},
	}
	for _, tt := range cases {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()
			got := searchBibleReferences(tt.query, 5)
			if len(got) == 0 {
				t.Fatalf("searchBibleReferences(%q) returned nothing", tt.query)
			}
			first := got[0]
			if first.Book != tt.wantBook || first.Chapter != tt.wantChapter ||
				first.Verse != tt.wantVerse || first.VerseEnd != tt.wantVerseEnd {
				t.Errorf("searchBibleReferences(%q)[0] = %v %v:%v-%v, want %v %v:%v-%v",
					tt.query, first.Book, first.Chapter, first.Verse, first.VerseEnd,
					tt.wantBook, tt.wantChapter, tt.wantVerse, tt.wantVerseEnd)
			}
			if tt.wantRef != "" && first.Reference() != tt.wantRef {
				t.Errorf("searchBibleReferences(%q)[0].Reference() = %q, want %q",
					tt.query, first.Reference(), tt.wantRef)
			}
		})
	}

	for _, m := range searchBibleReferences("John 3:6-1", 20) {
		if m.VerseEnd != 0 && m.VerseEnd < m.Verse {
			t.Errorf("a reversed range produced %v", m.Reference())
		}
	}
}

// TestSearchBibleReferencesRanking asserts an exact match outranks a fuzzy one,
// a spaced reference outranks a compact one, and duplicates never appear.
func TestSearchBibleReferencesRanking(t *testing.T) {
	t.Parallel()

	if got := searchBibleReferences("John 3:16", 5); got[0].Book != "John" {
		t.Errorf("searchBibleReferences(\"John 3:16\")[0].Book = %q, want John", got[0].Book)
	}
	if got := searchBibleReferences("John 3:16", 5); got[0].Score < 90 {
		t.Errorf("a spaced reference scored %v, want at least 90", got[0].Score)
	}
	got := searchBibleReferences("phil1:1", 20)
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("scores are not descending: %v then %v", got[i-1].Score, got[i].Score)
		}
	}
	refs := map[string]bool{}
	for _, m := range searchBibleReferences("John 3:16", 20) {
		if refs[m.Reference()] {
			t.Errorf("duplicate reference %q", m.Reference())
		}
		refs[m.Reference()] = true
	}
}

// TestSearchBibleReferencesEdgeCases asserts nonsense and empty input produce
// nothing, and the result cap is honored.
func TestSearchBibleReferencesEdgeCases(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"", "   ", "123", "xyzzy", "zzzqqqxxx"} {
		if got := searchBibleReferences(query, 5); len(got) != 0 {
			t.Errorf("searchBibleReferences(%q) = %v, want nothing", query, got)
		}
	}
	if got := searchBibleReferences("1 1:1", 3); len(got) > 3 {
		t.Errorf("searchBibleReferences(\"1 1:1\", 3) returned %v matches, want at most 3", len(got))
	}
	if got := searchBibleReferences("John 3:16", 0); got != nil {
		t.Errorf("searchBibleReferences with maxResults 0 returned %v, want nil", got)
	}
}

// TestKJVQueryHasDigit asserts the helper the command uses to tell a reference
// that names a chapter from a bare word that happens to be a book name.
func TestKJVQueryHasDigit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		query string
		want  bool
	}{
		{"jn3:16", true}, {"Psalm 23:1-6", true}, {"psalms23:3", true},
		{"1 john", true}, {"john", false}, {"love of god", false}, {"", false},
	}
	for _, tt := range cases {
		if got := kjvQueryHasDigit(tt.query); got != tt.want {
			t.Errorf("kjvQueryHasDigit(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

// TestKJVNormalizeBookKey asserts the whitespace-and-case normalization every
// book lookup and fuzzy match shares.
func TestKJVNormalizeBookKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		book string
		want string
	}{
		{"Song of Solomon", "songofsolomon"},
		{"1 JOHN", "1john"},
		{"  1 john  ", "1john"},
		{"Psalms", "psalms"},
	}
	for _, tt := range cases {
		if got := kjvNormalizeBookKey(tt.book); got != tt.want {
			t.Errorf("kjvNormalizeBookKey(%q) = %q, want %q", tt.book, got, tt.want)
		}
	}
}
