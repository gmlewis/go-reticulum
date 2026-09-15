// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// historyRow builds one conversation row as the client would persist it.
func historyRow(room, nick, text string, ts int64) *rrc.RRCMessage {
	return &rrc.RRCMessage{
		Kind: "msg",
		Room: room,
		Src:  peerHashFor(0x22),
		Nick: nick,
		Text: text,
		Ts:   ts,
		ID:   hex.EncodeToString([]byte{byte(ts), 0x01}),
	}
}

// historySuffix is the eight-hex-character room digest the client puts in a
// history file name.
func historySuffix(room string) string {
	sum := sha256.Sum256([]byte(normalizeRoom(room)))
	return hex.EncodeToString(sum[:4])
}

// writeHistoryDir writes rows into one hub's history directory, exactly in the
// client's layout, and returns the directory.
func writeHistoryDir(t *testing.T, storageDir, hubDir string, entries map[string][]*rrc.RRCMessage) string {
	t.Helper()
	dir := filepath.Join(storageDir, historyDirName, hubDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%v): %v", dir, err)
	}
	for room, rows := range entries {
		path := filepath.Join(dir, normalizeRoom(room)+"_"+historySuffix(room)+".log")
		var buf []byte
		for _, row := range rows {
			buf = append(buf, cbor.Encode(row.HistoryEntry())...)
		}
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatalf("WriteFile(%v): %v", path, err)
		}
	}
	return dir
}

// TestHistoryStoreReadsBackWhatTheClientWrote asserts the reader understands the
// real on-disk layout: the per-hub directory, the room file name, the CBOR entry
// stream, and the fields the digest needs.
func TestHistoryStoreReadsBackWhatTheClientWrote(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	writeHistoryDir(t, storage, fakeHubOne, map[string][]*rrc.RRCMessage{
		"general": {
			historyRow("general", "Alice", "first", 1_700_000_000_000),
			historyRow("general", "Bob", "second", 1_700_000_060_000),
		},
	})

	rows := newHistoryStore(storage, fakeHubOne).newest("general", 10)
	if len(rows) != 2 {
		t.Fatalf("newest = %v rows, want 2", len(rows))
	}
	if rows[0].Text != "first" || rows[1].Text != "second" {
		t.Errorf("texts = %q, %q, want first, second (append order)", rows[0].Text, rows[1].Text)
	}
	if rows[0].Nick != "Alice" || rows[0].Ts != 1_700_000_000_000 {
		t.Errorf("row = %+v, want Alice at the written timestamp", rows[0])
	}
	if rows[0].Room != "general" {
		t.Errorf("Room = %q, want the file's room filled in", rows[0].Room)
	}
	if got := hexString(rows[0].Src); got != hexString(peerHashFor(0x22)) {
		t.Errorf("Src = %v, want the written sender hash", got)
	}
}

// TestHistoryStoreFindsTheRoomRegardlessOfCaseAndSanitizing asserts lookup is by
// the room digest, not by the file name's readable prefix: the client lowercases
// the room and sanitizes the name it prefixes, and may sanitize it away entirely.
func TestHistoryStoreFindsTheRoomRegardlessOfCaseAndSanitizing(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	dir := filepath.Join(storage, historyDirName, fakeHubOne)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A file whose readable prefix is gone, as happens when a room name
	// sanitizes to nothing.
	sum := sha256.Sum256([]byte("lounge"))
	path := filepath.Join(dir, hex.EncodeToString(sum[:4])+".log")
	row := historyRow("lounge", "Alice", "hello", 1_700_000_000_000)
	if err := os.WriteFile(path, cbor.Encode(row.HistoryEntry()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := newHistoryStore(storage, fakeHubOne)
	if rows := store.newest("lounge", 10); len(rows) != 1 {
		t.Errorf("newest(lounge) = %v rows, want 1", len(rows))
	}
	if rows := store.newest("LOUNGE", 10); len(rows) != 1 {
		t.Errorf("newest(LOUNGE) = %v rows, want 1 (rooms are case-insensitive)", len(rows))
	}
}

// TestHistoryStoreMatchesADestNameSuffixedHubDirectory asserts the fallback that
// finds a hub directory the client suffixed with a destination-name hash.
func TestHistoryStoreMatchesADestNameSuffixedHubDirectory(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	writeHistoryDir(t, storage, fakeHubOne+"__a1b2c3d4", map[string][]*rrc.RRCMessage{
		"general": {historyRow("general", "Alice", "hello", 1_700_000_000_000)},
	})

	rows := newHistoryStore(storage, fakeHubOne).newest("general", 10)
	if len(rows) != 1 {
		t.Errorf("newest = %v rows, want 1 from the suffixed hub directory", len(rows))
	}
}

// TestHistoryStoreNeverLeaksBetweenHubsOrRooms asserts a miss stays a miss: a
// hub with no history, a room with no history, and a bot with no storage
// directory all read nothing rather than somebody else's rows.
func TestHistoryStoreNeverLeaksBetweenHubsOrRooms(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	writeHistoryDir(t, storage, fakeHubOne, map[string][]*rrc.RRCMessage{
		"general": {historyRow("general", "Alice", "hello", 1_700_000_000_000)},
	})

	store := newHistoryStore(storage, fakeHubTwo)
	if rows := store.newest("general", 10); len(rows) != 0 {
		t.Errorf("newest on a hub with no history = %v rows, want none", len(rows))
	}
	other := newHistoryStore(storage, fakeHubOne)
	if rows := other.newest("lounge", 10); len(rows) != 0 {
		t.Errorf("newest on a room with no history = %v rows, want none", len(rows))
	}
	if rows := newHistoryStore("", fakeHubOne).newest("general", 10); len(rows) != 0 {
		t.Errorf("newest without a storage directory = %v rows, want none", len(rows))
	}
	if rows := newHistoryStore(storage, "").newest("general", 10); len(rows) != 0 {
		t.Errorf("newest without a hub hash = %v rows, want none", len(rows))
	}
	if rows := other.newest("general", 0); len(rows) != 0 {
		t.Errorf("newest with a zero limit = %v rows, want none", len(rows))
	}
	if rows := other.newest("", 10); len(rows) != 0 {
		t.Errorf("newest of the empty room = %v rows, want none", len(rows))
	}
	if rows := other.newest("*", 10); len(rows) != 0 {
		t.Errorf("newest of the catch-all room = %v rows, want none", len(rows))
	}
}

// TestHistoryStoreKeepsOnlyTheNewestRows asserts the bounded window: a command
// that asks for the last n rows gets the last n, not the first n.
func TestHistoryStoreKeepsOnlyTheNewestRows(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	var rows []*rrc.RRCMessage
	for i := range 5 {
		rows = append(rows, historyRow("general", "Alice", string(rune('a'+i)), int64(1_700_000_000_000+i)))
	}
	writeHistoryDir(t, storage, fakeHubOne, map[string][]*rrc.RRCMessage{"general": rows})

	got := newHistoryStore(storage, fakeHubOne).newest("general", 3)
	if len(got) != 3 {
		t.Fatalf("newest(3) = %v rows, want 3", len(got))
	}
	if got[0].Text != "c" || got[2].Text != "e" {
		t.Errorf("texts = %q..%q, want c..e", got[0].Text, got[2].Text)
	}
}

// TestHistoryStoreStopsAtTheFirstDecodeError asserts a truncated or corrupt tail
// costs only the tail: the valid prefix is still readable, which is what the
// client's own history loading does.
func TestHistoryStoreStopsAtTheFirstDecodeError(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	dir := filepath.Join(storage, historyDirName, fakeHubOne)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	good := historyRow("general", "Alice", "readable", 1_700_000_000_000)
	body := append(cbor.Encode(good.HistoryEntry()), 0xff, 0xff, 0xff)
	path := filepath.Join(dir, "general_"+historySuffix("general")+".log")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rows := newHistoryStore(storage, fakeHubOne).newest("general", 10)
	if len(rows) != 1 || rows[0].Text != "readable" {
		t.Errorf("newest = %v rows, want the one readable row before the garbage", rows)
	}
}

// TestHistoryStoreSkipsValuesThatAreNotEntries asserts a stray non-map value in
// the stream is skipped rather than ending the read.
func TestHistoryStoreSkipsValuesThatAreNotEntries(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	dir := filepath.Join(storage, historyDirName, fakeHubOne)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	row := historyRow("general", "Alice", "after the stray value", 1_700_000_000_000)
	body := append(cbor.Encode("not an entry"), cbor.Encode(row.HistoryEntry())...)
	path := filepath.Join(dir, "general_"+historySuffix("general")+".log")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rows := newHistoryStore(storage, fakeHubOne).newest("general", 10)
	if len(rows) != 1 || rows[0].Text != "after the stray value" {
		t.Errorf("newest = %v rows, want the row after the stray value", rows)
	}
}

// TestHistoryEntryMapNormalizesEveryDecodedShape asserts the three shapes the
// decoder can hand back all become the string-keyed map the entry decoder wants,
// and that anything else is refused.
func TestHistoryEntryMapNormalizesEveryDecodedShape(t *testing.T) {
	t.Parallel()

	row := historyRow("general", "Alice", "hello", 1_700_000_000_000)
	want := row.HistoryEntry()

	decode := func(t *testing.T, body []byte) any {
		t.Helper()
		dec := cbor.NewDecoder(bytes.NewReader(body))
		val, err := dec.Decode()
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		return val
	}

	for _, tt := range []struct {
		name string
		val  any
	}{
		{name: "decoded map", val: decode(t, cbor.Encode(want))},
		{name: "plain map", val: want},
		{name: "any-keyed map", val: map[any]any{"k": "msg"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if entry := historyEntryMap(tt.val); entry == nil {
				t.Errorf("historyEntryMap(%v) = nil, want a map", tt.name)
			}
		})
	}

	for _, val := range []any{nil, "string", 42, []any{1}} {
		if entry := historyEntryMap(val); entry != nil {
			t.Errorf("historyEntryMap(%v) = %v, want nil", val, entry)
		}
	}
}

// TestPersistableRoomName asserts only a real named room reads history.
func TestPersistableRoomName(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		room string
		want bool
	}{
		{room: "general", want: true},
		{room: "", want: false},
		{room: "*", want: false},
	} {
		if got := persistableRoomName(tt.room); got != tt.want {
			t.Errorf("persistableRoomName(%q) = %v, want %v", tt.room, got, tt.want)
		}
	}
}

// TestHistoryStoreReadsARealClientFile asserts the reader against bytes written
// by the client's own encoder for a full conversation row, direct-notice keys
// included, so a format change on either side fails here.
func TestHistoryStoreReadsARealClientFile(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	direct := &rrc.RRCMessage{
		Kind:   "notice",
		Src:    peerHashFor(0x33),
		Nick:   "gorrcbot",
		Text:   "private from gorrbot",
		Ts:     1_700_000_120_000,
		Direct: true,
		Dst:    peerHashFor(0x44),
	}
	writeHistoryDir(t, storage, fakeHubOne, map[string][]*rrc.RRCMessage{"general": {direct}})

	rows := newHistoryStore(storage, fakeHubOne).newest("general", 10)
	if len(rows) != 1 {
		t.Fatalf("newest = %v rows, want 1", len(rows))
	}
	if !rows[0].Direct || hexString(rows[0].Dst) != hexString(peerHashFor(0x44)) {
		t.Errorf("row = %+v, want the direct-notice keys preserved", rows[0])
	}
	if rows[0].Nick != "gorrcbot" {
		t.Errorf("Nick = %q, want gorrbot", rows[0].Nick)
	}
}

// TestHistoryStoreReadsRowsInTimeOrder asserts the reader preserves the client's
// append order, which the digest relies on when it trims to the newest rows.
func TestHistoryStoreReadsRowsInTimeOrder(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	base := time.Date(2026, 9, 14, 19, 0, 0, 0, time.UTC).UnixMilli()
	writeHistoryDir(t, storage, fakeHubOne, map[string][]*rrc.RRCMessage{
		"general": {
			historyRow("general", "Alice", "older", base),
			historyRow("general", "Bob", "newer", base+60_000),
		},
	})

	rows := newHistoryStore(storage, fakeHubOne).newest("general", 10)
	if len(rows) != 2 {
		t.Fatalf("newest = %v rows, want 2", len(rows))
	}
	if rows[0].Ts >= rows[1].Ts {
		t.Errorf("timestamps = %v, %v, want ascending append order", rows[0].Ts, rows[1].Ts)
	}
}
