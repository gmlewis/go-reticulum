// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rrc"
)

// kjvFixtureText is a small King James extract in the same shape as the real
// kjv.txt: one verse per line, the text-file book abbreviation first, then
// chapter:verse and the verse text. It covers the shapes the command must
// handle — a header line that is not a verse, a numbered book, a long verse, and
// a psalm with gaps — without reading the 4 MB original.
const kjvFixtureText = `Holy Bible, Authorized (King James) Version, Textfile 930105.
Ge1:1 In the beginning God created the heaven and the earth.
Ge1:2 And the earth was without form, and void; and darkness was upon the face of the deep. And the Spirit of God moved upon the face of the waters.
Ge1:3 And God said, Let there be light: and there was light.
John3:16 For God so loved the world, that he gave his only begotten Son, that whosoever believeth in him should not perish, but have everlasting life.
John3:17 For God sent not his Son into the world to condemn the world; but that the world through him might be saved.
Psa23:1 The LORD is my shepherd; I shall not want.
Psa23:2 He maketh me to lie down in green pastures: he leadeth me beside the still waters.
Psa23:6 Surely goodness and mercy shall follow me all the days of my life: and I will dwell in the house of the LORD for ever.
1Jn3:16 Hereby perceive we the love of God, because he laid down his life for us: and we ought to lay down our lives for the brethren.
Isa1:1 The vision of Isaiah the son of Amoz, which he saw concerning Judah and Jerusalem in the days of Uzziah, Jotham, Ahaz, and Hezekiah, kings of Judah.
Rev1:1 The Revelation of Jesus Christ, which God gave unto him, to shew unto his servants things which must shortly come to pass.
Rev22:21 The grace of our Lord Jesus Christ be with you all. Amen.
`

// writeKJVFixture writes the fixture into a fresh temp directory and returns its
// path.
func writeKJVFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(tempDir(t), "kjv.txt")
	if err := os.WriteFile(path, []byte(kjvFixtureText), 0o644); err != nil {
		t.Fatalf("writing the KJV fixture: %v", err)
	}
	return path
}

// TestParseKJVLine asserts the line reader accepts the real file's shape and
// rejects the header and malformed lines rather than inventing verses.
func TestParseKJVLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line    string
		ok      bool
		book    string
		chapter int
		verse   int
	}{
		{line: "Ge1:1 In the beginning God created", ok: true, book: "Genesis", chapter: 1, verse: 1},
		{line: "John3:16 For God so loved the world", ok: true, book: "John", chapter: 3, verse: 16},
		{line: "1Jn3:16 Hereby perceive we the love of God", ok: true, book: "1 John", chapter: 3, verse: 16},
		{line: "Psa23:1 The LORD is my shepherd", ok: true, book: "Psalms", chapter: 23, verse: 1},
		{line: "Rev22:21 The grace of our Lord", ok: true, book: "Revelation", chapter: 22, verse: 21},
		{line: "  Ge1:1   In the beginning  ", ok: true, book: "Genesis", chapter: 1, verse: 1},
		{line: "Holy Bible, Authorized (King James) Version, Textfile 930105."},
		{line: ""},
		{line: "Zz9:9 Not a real book"},
		{line: "John 3:16 no colon form"},
		{line: "John3:16"},
	}
	for _, tt := range cases {
		t.Run(tt.line, func(t *testing.T) {
			t.Parallel()
			verse, ok := parseKJVLine(tt.line)
			if ok != tt.ok {
				t.Fatalf("parseKJVLine(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			}
			if !ok {
				return
			}
			if verse.Book != tt.book || verse.Chapter != tt.chapter || verse.Verse != tt.verse {
				t.Errorf("parseKJVLine(%q) = %v %v:%v, want %v %v:%v",
					tt.line, verse.Book, verse.Chapter, verse.Verse, tt.book, tt.chapter, tt.verse)
			}
			if strings.TrimSpace(verse.Text) == "" {
				t.Errorf("parseKJVLine(%q) produced no text", tt.line)
			}
		})
	}
}

// TestLoadKJVStore asserts the whole fixture loads, the canonical names are
// applied, and the chapter index keeps verses in order.
func TestLoadKJVStore(t *testing.T) {
	t.Parallel()

	store, err := loadKJV(writeKJVFixture(t))
	if err != nil {
		t.Fatalf("loadKJV: %v", err)
	}
	if got := len(store.verses); got != 12 {
		t.Errorf("loaded %v verses, want 12", got)
	}
	verse, ok := store.verse("John", 3, 16)
	if !ok {
		t.Fatal("John 3:16 is missing from the store")
	}
	if !strings.Contains(verse.Text, "For God so loved the world") {
		t.Errorf("John 3:16 text = %q", verse.Text)
	}
	if verse.Reference() != "John 3:16" {
		t.Errorf("John 3:16 reference = %q", verse.Reference())
	}
	if _, ok := store.verse("John", 3, 99); ok {
		t.Error("John 3:99 was found, want no such verse")
	}
	chapter := store.chapterVerses("Psalms", 23)
	if len(chapter) != 3 {
		t.Fatalf("Psalms 23 has %v verses, want 3", len(chapter))
	}
	for i := 1; i < len(chapter); i++ {
		if chapter[i-1].Verse >= chapter[i].Verse {
			t.Errorf("Psalms 23 verses are out of order: %v then %v", chapter[i-1].Verse, chapter[i].Verse)
		}
	}
}

// TestKJVStoreLookupRefs asserts a single reference, a range, and a range whose
// gaps are simply skipped.
func TestKJVStoreLookupRefs(t *testing.T) {
	t.Parallel()

	store, err := loadKJV(writeKJVFixture(t))
	if err != nil {
		t.Fatalf("loadKJV: %v", err)
	}

	single := store.lookupRefs([]kjvRefMatch{{Book: "John", Chapter: 3, Verse: 16}})
	if len(single) != 1 || single[0].Reference() != "John 3:16" {
		t.Errorf("single lookup = %v", single)
	}

	rng := store.lookupRefs([]kjvRefMatch{{Book: "Psalms", Chapter: 23, Verse: 1, VerseEnd: 6}})
	if len(rng) != 3 {
		t.Fatalf("Psalms 23:1-6 returned %v verses, want 3", len(rng))
	}
	want := []string{"Psalms 23:1", "Psalms 23:2", "Psalms 23:6"}
	for i, v := range rng {
		if v.Reference() != want[i] {
			t.Errorf("range verse %v = %q, want %q", i, v.Reference(), want[i])
		}
	}

	missing := store.lookupRefs([]kjvRefMatch{{Book: "John", Chapter: 3, Verse: 99}})
	if len(missing) != 0 {
		t.Errorf("a missing verse returned %v", missing)
	}
}

// TestKJVSearch asserts the three search paths: words (AND, phrase, OR,
// exclude, wildcard, prefix fallback) and a raw regular expression.
func TestKJVSearch(t *testing.T) {
	t.Parallel()

	store, err := loadKJV(writeKJVFixture(t))
	if err != nil {
		t.Fatalf("loadKJV: %v", err)
	}

	cases := []struct {
		query string
		want  []string
		// wantFirst, when set, must be the first result: ranking matters.
		wantFirst string
	}{
		{query: "shepherd", want: []string{"Psalms 23:1"}, wantFirst: "Psalms 23:1"},
		{query: "shepher", want: []string{"Psalms 23:1"}},
		{query: `"In the beginning"`, want: []string{"Genesis 1:1"}},
		{query: `"God so loved"`, want: []string{"John 3:16"}},
		{query: "love of god", want: []string{"1 John 3:16"}},
		{query: `"the * of God"`, want: []string{"1 John 3:16"}},
		{query: "shepherd|grace", want: []string{"Psalms 23:1", "Revelation 22:21"}},
		{query: "loved -brethren", want: []string{"John 3:16"}},
		{query: "lov*", want: []string{"John 3:16", "1 John 3:16"}},
		{query: "love.*life", want: []string{"John 3:16", "1 John 3:16"}},
		{query: "^In the beginning", want: []string{"Genesis 1:1"}},
		{query: "l?ve", want: []string{"1 John 3:16"}},
		{query: "well-beloved", want: nil},
		{query: "zzzqqqxxx", want: nil},
		{query: "", want: nil},
		{query: "   ", want: nil},
	}
	for _, tt := range cases {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()
			got := searchKJVVerses(store, tt.query)
			refs := make([]string, 0, len(got))
			for _, v := range got {
				refs = append(refs, v.Reference())
			}
			for _, want := range tt.want {
				if !containsString(refs, want) {
					t.Errorf("search(%q) = %v, want %q among them", tt.query, refs, want)
				}
			}
			if tt.wantFirst != "" && (len(refs) == 0 || refs[0] != tt.wantFirst) {
				t.Errorf("search(%q)[0] = %v, want %q", tt.query, refs, tt.wantFirst)
			}
			if tt.want == nil && len(refs) != 0 {
				t.Errorf("search(%q) = %v, want nothing", tt.query, refs)
			}
		})
	}

	// A bare abbreviation that is not a word in the text resolves to its book's
	// first verse, while a word that does occur is searched for.
	if got := searchKJVVerses(store, "gen"); len(got) != 1 || got[0].Reference() != "Genesis 1:1" {
		t.Errorf("searchKJVVerses(\"gen\") = %v, want Genesis 1:1", got)
	}
	if got := searchKJVVerses(store, "isa"); len(got) != 1 || got[0].Reference() != "Isaiah 1:1" {
		t.Errorf("searchKJVVerses(\"isa\") = %v, want Isaiah 1:1", got)
	}
	if got := searchKJVVerses(store, "is"); !containsKJVReference(got, "Psalms 23:1") {
		t.Errorf("searchKJVVerses(\"is\") = %v, want the word search to win over Isaiah", got)
	}
	if got := searchKJVVerses(store, "god"); len(got) < 3 {
		t.Errorf("searchKJVVerses(\"god\") = %v, want every verse containing it", got)
	}
}

// TestLoadKJVErrors asserts a missing path, a directory, and a file with no
// verses each produce an error instead of an empty store.
func TestLoadKJVErrors(t *testing.T) {
	t.Parallel()

	if _, err := loadKJV(filepath.Join(tempDir(t), "absent.txt")); err == nil {
		t.Error("a missing file loaded without an error")
	}
	if _, err := loadKJV(tempDir(t)); err == nil {
		t.Error("a directory loaded without an error")
	}
	empty := filepath.Join(tempDir(t), "empty.txt")
	if err := os.WriteFile(empty, []byte("no verses here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadKJV(empty); err == nil {
		t.Error("a file with no verses loaded without an error")
	}
}

// TestKJVCacheLoadsOnce asserts the expensive parse happens once, and a failure
// is remembered rather than retried on every command.
func TestKJVCacheLoadsOnce(t *testing.T) {
	t.Parallel()

	path := writeKJVFixture(t)
	cache := newKJVCache(path)
	first, err := cache.get()
	if err != nil {
		t.Fatalf("cache.get: %v", err)
	}
	second, err := cache.get()
	if err != nil {
		t.Fatalf("cache.get again: %v", err)
	}
	if first != second {
		t.Error("the cache loaded the file twice")
	}

	missing := newKJVCache(filepath.Join(tempDir(t), "absent.txt"))
	if _, err := missing.get(); err == nil {
		t.Fatal("a missing file reported no error")
	}
	if _, err := missing.get(); err == nil {
		t.Error("the cached failure was forgotten")
	}
}

// TestKJVVerseLabel asserts the reply labels a verse with its full canonical
// name, which is what makes a search answer readable.
func TestKJVVerseLabel(t *testing.T) {
	t.Parallel()

	v := kjvVerse{Book: "John", Chapter: 3, Verse: 16, Text: "For God so loved the world."}
	if got := kjvVerseLabel(v); got != "John 3:16: For God so loved the world." {
		t.Errorf("kjvVerseLabel = %q", got)
	}
}

// TestKJVVerseLines asserts the reply is bounded by the line budget and says
// honestly how many matches were not shown.
func TestKJVVerseLines(t *testing.T) {
	t.Parallel()

	verses := []kjvVerse{
		{Book: "John", Chapter: 3, Verse: 16, Text: "a"},
		{Book: "John", Chapter: 3, Verse: 17, Text: "b"},
		{Book: "John", Chapter: 3, Verse: 18, Text: "c"},
	}
	if got := kjvVerseLines(verses, 12); len(got) != 3 {
		t.Errorf("a reply under budget = %v, want 3 lines", got)
	}
	got := kjvVerseLines(verses, 2)
	if len(got) != 2 {
		t.Fatalf("a bounded reply = %v, want 2 lines", got)
	}
	if !strings.Contains(got[1], "more matching verses") {
		t.Errorf("the trailer = %q, want it to say how many were omitted", got[1])
	}
	if got := kjvVerseLines(verses, 1); len(got) != 1 || !strings.HasPrefix(got[0], "John 3:16:") {
		t.Errorf("a budget of 1 = %v, want the first verse alone", got)
	}
	if got := kjvVerseLines(nil, 4); len(got) != 0 {
		t.Errorf("no verses = %v, want no lines", got)
	}
}

// kjvCommandFixture builds a registry whose bot has the KJV fixture configured.
func kjvCommandFixture(t *testing.T) (*registry, *hubSession) {
	t.Helper()
	cfg := defaultTestConfig()
	cfg.KJVTxtFile = writeKJVFixture(t)
	reg, session, _ := commandFixture(t, cfg)
	return reg, session
}

// TestKJVCommandNotConfigured asserts the command says how to turn itself on
// instead of guessing, which is what every other optional command does.
func TestKJVCommandNotConfigured(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	assertLines(t, runLines(t, reg, session, "kjv jn3:16"), []string{kjvNotConfiguredLine})
}

// TestKJVCommandUsage asserts empty and oversized argument lists answer with the
// usage line rather than an empty reply.
func TestKJVCommandUsage(t *testing.T) {
	t.Parallel()

	reg, session := kjvCommandFixture(t)
	assertLines(t, runLines(t, reg, session, "kjv"), []string{"Usage: " + kjvUsage})
	assertLines(t, runLines(t, reg, session, "kjv    "), []string{"Usage: " + kjvUsage})
	long := strings.Repeat("x", maxKJVQueryBytes+1)
	assertLines(t, runLines(t, reg, session, "kjv "+long), []string{"Usage: " + kjvUsage})
}

// TestKJVCommandLooksUpAReference asserts the reference forms a human types all
// resolve to the labeled verse, ranging from "jn3:16" to "Psalm 23:1-6".
func TestKJVCommandLooksUpAReference(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args string
		want []string
	}{
		{args: "jn3:16", want: []string{"John 3:16: For God so loved the world"}},
		{args: "John 3:16", want: []string{"John 3:16: For God so loved the world"}},
		{args: "1jn3:16", want: []string{"1 John 3:16: Hereby perceive we the love of God"}},
		{args: "ps23:3", want: []string{"no such verse"}},
		{args: "Psalm 23:1-6", want: []string{"Psalms 23:1: The LORD is my shepherd", "Psalms 23:2:", "Psalms 23:6:"}},
		{args: "psalms23:3", want: []string{"no such verse"}},
		{args: "gen1:1", want: []string{"Genesis 1:1: In the beginning God created"}},
	}
	for _, tt := range cases {
		t.Run(tt.args, func(t *testing.T) {
			t.Parallel()
			reg, session := kjvCommandFixture(t)
			lines := runLines(t, reg, session, "kjv "+tt.args)
			if len(lines) == 0 {
				t.Fatalf("kjv %v returned nothing", tt.args)
			}
			for _, want := range tt.want {
				if !strings.Contains(strings.Join(lines, "\n"), want) {
					t.Errorf("kjv %v = %q, want it to contain %q", tt.args, lines, want)
				}
			}
		})
	}
}

// TestKJVCommandSearchesWordsAndRegex asserts bare words search for matching
// verses, and a regexp-looking argument is attempted as a pattern.
func TestKJVCommandSearchesWordsAndRegex(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args string
		want []string
	}{
		{args: "shepherd", want: []string{"Psalms 23:1: The LORD is my shepherd"}},
		{args: "love of god", want: []string{"1 John 3:16"}},
		{args: `"In the beginning"`, want: []string{"Genesis 1:1"}},
		{args: "love.*life", want: []string{"John 3:16", "1 John 3:16"}},
		{args: "shepherd|grace", want: []string{"Psalms 23:1", "Revelation 22:21"}},
		{args: "gen", want: []string{"Genesis 1:1: In the beginning God created"}},
		{args: "isa", want: []string{"Isaiah 1:1: The vision of Isaiah"}},
		{args: "zzzqqqxxx", want: []string{"no verses match"}},
	}
	for _, tt := range cases {
		t.Run(tt.args, func(t *testing.T) {
			t.Parallel()
			reg, session := kjvCommandFixture(t)
			lines := runLines(t, reg, session, "kjv "+tt.args)
			joined := strings.Join(lines, "\n")
			for _, want := range tt.want {
				if !strings.Contains(joined, want) {
					t.Errorf("kjv %v = %q, want it to contain %q", tt.args, lines, want)
				}
			}
		})
	}

	// A word that really occurs in the text is searched for, even though its
	// two letters also spell the abbreviation of a book: "is" must find
	// Psalms 23:1, not Isaiah 1:1.
	reg, session := kjvCommandFixture(t)
	joined := strings.Join(runLines(t, reg, session, "kjv is"), "\n")
	if !strings.Contains(joined, "Psalms 23:1") {
		t.Errorf("kjv is = %q, want the word search for \"is\"", joined)
	}
	if strings.Contains(joined, "Isaiah 1:1") {
		t.Errorf("kjv is = %q, want Isaiah to stay out of a plain word search", joined)
	}
}

// TestKJVCommandBoundsItsReply asserts a broad search is cut to the operator's
// line budget and says so, so one command cannot flood a room.
func TestKJVCommandBoundsItsReply(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.KJVTxtFile = writeKJVFixture(t)
	cfg.MaxReplyLines = 3
	reg, session, _ := commandFixture(t, cfg)
	lines := runLines(t, reg, session, "kjv god")
	if len(lines) != 3 {
		t.Fatalf("a bounded search returned %v lines, want 3: %v", len(lines), lines)
	}
	if !strings.Contains(lines[len(lines)-1], "more matching verses") {
		t.Errorf("the last line = %q, want the truncation trailer", lines[len(lines)-1])
	}
}

// TestKJVCommandBoundsAVerseRange asserts a long verse range is bounded the same
// way a search is: the reply says how many verses of the range were not shown, so
// a truncated reference never looks like the whole passage.
func TestKJVCommandBoundsAVerseRange(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.KJVTxtFile = writeKJVFixture(t)
	cfg.MaxReplyLines = 2
	reg, session, _ := commandFixture(t, cfg)
	lines := runLines(t, reg, session, "kjv Psalm 23:1-6")
	if len(lines) != 2 {
		t.Fatalf("a bounded range returned %v lines, want 2: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "Psalms 23:1: ") {
		t.Errorf("the first line = %q, want the first verse of the range", lines[0])
	}
	if !strings.Contains(lines[1], "more matching verses") {
		t.Errorf("the trailer = %q, want it to say how many verses were not shown", lines[1])
	}
}

// TestKJVCommandIsPrivateWhenAskedPrivately asserts the answer follows the reply
// policy: a direct request is answered with a direct NOTICE.
func TestKJVCommandIsPrivateWhenAskedPrivately(t *testing.T) {
	t.Parallel()

	requester := peerHashFor(0x31)
	cfg := defaultTestConfig()
	cfg.KJVTxtFile = writeKJVFixture(t)
	session, fake := newReplySession(t, cfg)
	fake.setCapability(rrc.CapDirectNotice, true)
	fake.setKnownPeer(hexString(requester), "Alice")
	reg := newRegistry(session.bot)
	r := newResponder(cfg, mustHex(replyOwnHash), reg.Run)
	r.handle(session, directMessageFrom("kjv jn3:16", requester))

	direct := fake.directList()
	if len(direct) != 1 {
		t.Fatalf("direct notices = %v, want exactly 1 for a private request", direct)
	}
	if !strings.Contains(direct[0], "John 3:16: For God so loved the world") {
		t.Errorf("the direct notice = %q, want the labeled verse", direct[0])
	}
	if got := fake.noticeList(); len(got) != 0 {
		t.Errorf("room notices = %v, want none for a private request", got)
	}
}

// TestKJVIsRegisteredAndDocumented asserts the command is in the table, that help
// lists it, and that "help kjv" gives real guidance with examples.
func TestKJVIsRegisteredAndDocumented(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	cmd, ok := reg.byName["kjv"]
	if !ok {
		t.Fatalf("kjv is not registered; names = %v", reg.names())
	}
	if cmd.usage != kjvUsage {
		t.Errorf("usage = %q, want %q", cmd.usage, kjvUsage)
	}
	// help kjv must explain all three input shapes, with examples.
	lines := runLines(t, reg, session, "help kjv")
	joined := strings.Join(lines, "\n")
	for _, want := range []string{kjvUsage, "jn3:16", "love", "regex", "John 3:16:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("help kjv = %q, want it to mention %q", lines, want)
		}
	}
	if want := helpLineCount(cmd, reg.config()); len(lines) != want {
		t.Errorf("help kjv returned %v lines, want the summary line plus %v detail lines",
			len(lines), len(cmd.detail))
	}
	if got := runLines(t, reg, session, "help"); !strings.Contains(got[0], "kjv") {
		t.Errorf("the command listing = %q, want it to include kjv", got[0])
	}
}

// TestKJVRealReferenceFile loads the full King James text from the reference
// checkout when it is present, so the parser and search are exercised against
// the real data. It skips cleanly when the checkout is absent.
func TestKJVRealReferenceFile(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve the home directory: %v", err)
	}
	path := filepath.Join(home, "src", "github.com", "gmlewis", "kjv-ref", "kjv.txt")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("the reference kjv.txt is not available at %v", path)
	}
	store, err := loadKJV(path)
	if err != nil {
		t.Fatalf("loadKJV(%v): %v", path, err)
	}
	if got := len(store.verses); got != 31102 {
		t.Errorf("loaded %v verses, want 31102", got)
	}
	verse, ok := store.verse("John", 3, 16)
	if !ok || !strings.HasPrefix(verse.Text, "For God so loved the world") {
		t.Errorf("John 3:16 = %+v, %v", verse, ok)
	}
	if got := searchKJVVerses(store, "shepherd"); len(got) == 0 {
		t.Error("searching the real text for \"shepherd\" found nothing")
	}
	psalm, ok := store.verse("Psalms", 23, 1)
	if !ok || !strings.Contains(psalm.Text, "shepherd") {
		t.Errorf("Psalms 23:1 = %+v, %v", psalm, ok)
	}
	if got := store.lookupRefs([]kjvRefMatch{{Book: "Psalms", Chapter: 23, Verse: 1, VerseEnd: 6}}); len(got) != 6 {
		t.Errorf("Psalms 23:1-6 returned %v verses, want 6", len(got))
	}
	shortest, ok := store.verse("John", 11, 35)
	if !ok || shortest.Text != "Jesus wept." {
		t.Errorf("John 11:35 = %+v, %v; want \"Jesus wept.\"", shortest, ok)
	}

	// The shapes the kjv command is asked for most often must all resolve
	// against the real text.
	fuzzy := []struct {
		query string
		want  []string
	}{
		{query: "jn3:16", want: []string{"John 3:16"}},
		{query: "John 3:16", want: []string{"John 3:16"}},
		{query: "Psalm 23:1-6", want: []string{"Psalms 23:1", "Psalms 23:2", "Psalms 23:6"}},
		{query: "psalms23:3", want: []string{"Psalms 23:3"}},
		{query: "ps23", want: []string{"Psalms 23:1"}},
		{query: "rom8:28", want: []string{"Romans 8:28"}},
		{query: "1cor13:4", want: []string{"1 Corinthians 13:4"}},
		{query: "john3:16", want: []string{"John 3:16"}},
		{query: "phil1:1", want: []string{"Philippians 1:1", "Philemon 1:1"}},
	}
	for _, tt := range fuzzy {
		refs := searchBibleReferences(tt.query, 4)
		got := store.lookupRefs(refs)
		refsOut := make([]string, 0, len(got))
		for _, v := range got {
			refsOut = append(refsOut, v.Reference())
		}
		for _, want := range tt.want {
			if !containsString(refsOut, want) {
				t.Errorf("lookup %q = %v, want %q among them", tt.query, refsOut, want)
			}
		}
	}

	// A word search, a phrase search, and a raw regexp over the whole text.
	for _, query := range []string{"shepherd", `"In the beginning"`, "love.*life", "^In the beginning"} {
		if got := searchKJVVerses(store, query); len(got) == 0 {
			t.Errorf("searching the real text for %q found nothing", query)
		}
	}
	if got := searchKJVVerses(store, "shepherd"); !containsKJVReference(got, "Psalms 23:1") {
		t.Error("the real text search for \"shepherd\" never reaches Psalms 23:1")
	}
	// A bare abbreviation resolves to its book, because the exact word search
	// for "gen" finds no verse in the text.
	if got := searchKJVVerses(store, "gen"); len(got) == 0 || got[0].Reference() != "Genesis 1:1" {
		t.Errorf("searchKJVVerses(\"gen\") = %v, want Genesis 1:1", got)
	}

	// The command layer, over the real text.
	cfg := defaultTestConfig()
	cfg.KJVTxtFile = path
	reg, session, _ := commandFixture(t, cfg)
	lines := runLines(t, reg, session, "kjv jn3:16")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "John 3:16: For God so loved the world") {
		t.Errorf("kjv jn3:16 over the real text = %q", lines)
	}
	lines = runLines(t, reg, session, "kjv Psalm 23:1-6")
	if len(lines) != 6 || !strings.HasPrefix(lines[0], "Psalms 23:1: The LORD is my shepherd") {
		t.Errorf("kjv Psalm 23:1-6 over the real text = %q", lines)
	}
}

// containsKJVReference reports whether any verse in the slice has the reference.
func containsKJVReference(verses []kjvVerse, reference string) bool {
	for _, v := range verses {
		if v.Reference() == reference {
			return true
		}
	}
	return false
}
