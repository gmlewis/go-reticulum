// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package toml

import "testing"

// The expected outputs below are captured from live tomlkit 0.15.1 running
// the exact mutation sequences the rrcd persistence paths perform.

func TestEditExistingValueRerendersInPlace(t *testing.T) {
	t.Parallel()
	// tomlkit: replaced values re-render with default (double-quote) style
	// at their original position; untouched lines stay verbatim.
	doc := mustParse(t, "[hub]\nname = 'old'\ncount = 1\n")
	hub := doc.TablePath("hub")
	hub.Set("name", StringValue("new"))
	want := "[hub]\nname = \"new\"\ncount = 1\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditNewKeyAppendsAfterLastKey(t *testing.T) {
	t.Parallel()
	// tomlkit appends the key after the last key of the table, before the
	// blank line that precedes the next table.
	doc := mustParse(t, "[hub]\na = 1\n\n[logging]\nb = 2\n")
	doc.TablePath("hub").Set("banned_identities", StringArrayValue([]string{"aa", "bb"}))
	want := "[hub]\na = 1\nbanned_identities = [\"aa\", \"bb\"]\n\n[logging]\nb = 2\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditArrayReplace(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[hub]\nbanned_identities = ['0xAABB', 'cc']\n\n[logging]\nlevel = \"INFO\"\n")
	doc.TablePath("hub").Set("banned_identities", StringArrayValue([]string{"aabb", "cc"}))
	want := "[hub]\nbanned_identities = [\"aabb\", \"cc\"]\n\n[logging]\nlevel = \"INFO\"\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditEmptyArrayReplace(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[hub]\nresource_expectation_ttl_s = 30.0\n\n[logging]\nlevel = \"INFO\"\n")
	doc.TablePath("hub").Set("banned_identities", StringArrayValue(nil))
	want := "[hub]\nresource_expectation_ttl_s = 30.0\nbanned_identities = []\n\n[logging]\nlevel = \"INFO\"\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditFloatReplace(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[rooms]\nlast_used_ts = 900.0\n")
	doc.TablePath("rooms").Set("last_used_ts", FloatValue(1730000000.123456))
	want := "[rooms]\nlast_used_ts = 1730000000.123456\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditKeyDeletePreservesRest(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[hub]\na = 1\nb = 2\nc = 3\n")
	if !doc.TablePath("hub").Delete("b") {
		t.Fatal("Delete(b) = false")
	}
	want := "[hub]\na = 1\nc = 3\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditTableDelete(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[rooms]\n\n[rooms.x]\na = 1\n\n[rooms.y]\nb = 2\n")
	if !doc.TablePath("rooms").DeleteTable("x") {
		t.Fatal("DeleteTable(x) = false")
	}
	want := "[rooms]\n\n[rooms.y]\nb = 2\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditNewSubTableAfterNonEmptyBody(t *testing.T) {
	t.Parallel()
	// tomlkit inserts one blank line before a new table appended after
	// non-empty content.
	doc := mustParse(t, "[rooms]\na = 1\n")
	doc.TablePath("rooms", "new")
	want := "[rooms]\na = 1\n\n[rooms.new]\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditNewSubTableAfterEmptyParent(t *testing.T) {
	t.Parallel()
	// tomlkit appends the first room of an empty-bodied [rooms] directly
	// after its header (the template shape).
	doc := mustParse(t, "[rooms]\n")
	doc.TablePath("rooms", "new")
	want := "[rooms]\n[rooms.new]\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditQuotedRoomHeader(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[rooms]\n")
	doc.TablePath("rooms", "my room")
	doc.TablePath("rooms", "my room").Set("founder", StringValue("abc"))
	want := "[rooms]\n[rooms.\"my room\"]\nfounder = \"abc\"\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditNewTopLevelTableGetsBlankLine(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[hub]\nx = 1\n# tail comment\n")
	doc.TablePath("rooms")
	want := "[hub]\nx = 1\n# tail comment\n\n[rooms]\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditTopLevelTableAfterHeaderStillGetsBlankLine(t *testing.T) {
	t.Parallel()
	// tomlkit inserts a blank line even when the previous line is a table
	// header with an empty body, as long as earlier content exists.
	doc := mustParse(t, "[hub]\nx = 1\n[other]\n")
	doc.TablePath("rooms")
	want := "[hub]\nx = 1\n[other]\n\n[rooms]\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditCreatedRoomsRegistryShape(t *testing.T) {
	t.Parallel()
	// Reproducing the persist flow from a template-like file: implicit
	// parent elision and consecutive room tables with blank separators.
	doc := mustParse(t, "top = 1\n")
	doc.TablePath("rooms")
	doc.TablePath("rooms", "one")
	doc.TablePath("rooms", "one").Set("founder", StringValue("aa"))
	doc.TablePath("rooms", "two")
	doc.TablePath("rooms", "two").Set("founder", StringValue("bb"))
	want := "top = 1\n\n[rooms.one]\nfounder = \"aa\"\n\n[rooms.two]\nfounder = \"bb\"\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditSetTableReplacesContent(t *testing.T) {
	t.Parallel()
	// Re-assigning a dict over an existing sub-table replaces its content
	// (tomlkit re-assign semantics) and keeps the table in place.
	doc := mustParse(t, "[rooms.x]\n\n[rooms.x.invited]\naa = 1.0\n")
	doc.TablePath("rooms", "x").SetTable("invited").Set("bb", FloatValue(2.0))
	want := "[rooms.x]\n\n[rooms.x.invited]\nbb = 2.0\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditSetTableConvertsInlineValue(t *testing.T) {
	t.Parallel()
	// A hand-edited inline invited value converts to a sub-table; the Go
	// port emits it without the extra blank line tomlkit leaves behind
	// (documented mechanical divergence for hand-edited inline tables).
	doc := mustParse(t, "[rooms.x]\ninvited = { aa = 1.0 }\n")
	doc.TablePath("rooms", "x").SetTable("invited").Set("bb", FloatValue(2.0))
	want := "[rooms.x]\n[rooms.x.invited]\nbb = 2.0\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditSetTableEmpty(t *testing.T) {
	t.Parallel()
	// Assigning an empty dict to a missing invited key on a table with an
	// otherwise empty body renders the sub-table header directly after
	// (captured from live tomlkit).
	doc := mustParse(t, "[rooms.x]\n")
	doc.TablePath("rooms", "x").SetTable("invited")
	want := "[rooms.x]\n[rooms.x.invited]\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditDocWithoutTrailingNewline(t *testing.T) {
	t.Parallel()
	// tomlkit completes the unterminated last line, then appends the new
	// table header without a blank separator.
	doc := mustParse(t, "[rooms]\na = 1")
	doc.TablePath("rooms", "new")
	want := "[rooms]\na = 1\n[rooms.new]\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditTrailingBlanksSuppressSeparator(t *testing.T) {
	t.Parallel()
	// Content already ending with a blank line gets no extra blank.
	doc := mustParse(t, "[rooms]\n\n\n")
	doc.TablePath("rooms", "new")
	want := "[rooms]\n\n\n[rooms.new]\n"
	if got := doc.Dump(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEditGetOrCreateReturnsExisting(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, "[rooms.x]\na = 1\n")
	tbl := doc.TablePath("rooms", "x")
	if v, ok := tbl.Get("a"); !ok || v.Int != 1 {
		t.Fatalf("existing table contents lost: %+v", v)
	}
	if len(tbl.Keys) != 1 || tbl.Keys[0].Key != "a" {
		t.Fatalf("keys = %+v", tbl.Keys)
	}
}
