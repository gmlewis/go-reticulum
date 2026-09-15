// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

// storedFixture builds a connected session whose bot keeps its state in a fresh
// temporary directory, so the persistent stores really have somewhere to write.
// The caller also gets the directory, so a test can reload the store from it.
func storedFixture(t *testing.T, cfg *BotConfig, rooms ...string) (*registry, *hubSession, *fakeHub, string) {
	t.Helper()
	if cfg == nil {
		cfg = defaultTestConfig()
	}
	session, fake := newReplySession(t, cfg, rooms...)
	dir := tempDir(t)
	session.bot.cfg.StorageDir = dir
	session.bot.sos = newSOSStore(dir)
	session.bot.sitreps = newSitrepStore(dir)
	session.bot.checkins = newCheckinStore(dir)
	session.bot.checkinWatch = newCheckinWatchdog(session.bot.checkins)
	session.bot.checkinWatch.deliver = session.bot.deliverOverdue
	// The engine registers its sessions in addHub, which this fixture bypasses;
	// the watchdog and the LXMF outcome path both walk that list.
	session.bot.sessions = append(session.bot.sessions, session)
	return newRegistry(session.bot), session, fake, dir
}

// noticeTexts returns the notice texts the fake hub was asked to send, in
// order.
func noticeTexts(fake *fakeHub) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	out := make([]string, 0, len(fake.notices))
	for _, notice := range fake.notices {
		out = append(out, notice.Text)
	}
	return out
}

// directTexts returns the direct-notice texts the fake hub was asked to send.
func directTexts(fake *fakeHub) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.direct...)
}

// TestSOSStoreRoundTripsThroughDisk asserts a beacon survives a restart: a new
// store over the same directory finds it, with the same id and details.
func TestSOSStoreRoundTripsThroughDisk(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	first := newSOSStore(dir)
	at := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	record, err := first.add(SOSRecord{
		Sender: "Alice (abc123def456)", SenderHash: "abc123def456",
		Location: "849VCWC8+R9", LatLng: LatLng{Lat: 37.4220625, Lng: -122.0840625},
		Triage: "RED", Details: "2 hikers, 1 leg fracture", Timestamp: at,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if record.ID != 1 {
		t.Errorf("first beacon id = %v, want 1", record.ID)
	}

	second := newSOSStore(dir)
	active := second.active()
	if len(active) != 1 {
		t.Fatalf("reloaded store has %v active beacons, want 1", len(active))
	}
	if active[0].ID != record.ID || active[0].Details != record.Details {
		t.Errorf("reloaded beacon = %+v, want %+v", active[0], record)
	}
	if !active[0].Timestamp.Equal(at) {
		t.Errorf("reloaded timestamp = %v, want %v", active[0].Timestamp, at)
	}
	// The next beacon continues the numbering rather than reusing an id.
	next, err := second.add(SOSRecord{SenderHash: "other", Triage: "GREEN", Details: "ok"})
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if next.ID != 2 {
		t.Errorf("second beacon id = %v, want 2", next.ID)
	}
}

// TestSOSStoreCapsTheRegistry asserts the registry never grows past its bound
// and that the newest beacons are the ones kept.
func TestSOSStoreCapsTheRegistry(t *testing.T) {
	t.Parallel()

	// The bound is exercised in memory: the on-disk round trip is its own test.
	store := newSOSStore("")
	for i := range maxSOSRecords + 5 {
		if _, err := store.add(SOSRecord{SenderHash: "hash", Triage: "INFO",
			Details: "report", Timestamp: time.Unix(int64(i), 0)}); err != nil {
			t.Fatalf("add %v: %v", i, err)
		}
	}
	active := store.active()
	if len(active) != maxSOSRecords {
		t.Fatalf("registry holds %v beacons, want %v", len(active), maxSOSRecords)
	}
	if got := active[len(active)-1].Details; got != "report" {
		t.Errorf("newest beacon = %q, want the last one added", got)
	}
	if active[0].ID != 6 {
		t.Errorf("oldest surviving id = %v, want 6 (the first five were trimmed)", active[0].ID)
	}
}

// TestSOSStoreClearRequiresTheSender asserts only the identity that raised a
// beacon can stand it down, and that clearing is recorded.
func TestSOSStoreClearRequiresTheSender(t *testing.T) {
	t.Parallel()

	store := newSOSStore(tempDir(t))
	record, err := store.add(SOSRecord{SenderHash: "alicehash", Triage: "RED", Details: "help"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.clear(record.ID, "malloryhash"); err == nil {
		t.Error("a stranger cleared somebody else's beacon")
	}
	if _, err := store.clear(record.ID+99, "alicehash"); err == nil {
		t.Error("clearing an unknown id succeeded")
	}
	cleared, err := store.clear(record.ID, "alicehash")
	if err != nil {
		t.Fatalf("clear by the sender: %v", err)
	}
	if !cleared.Resolved {
		t.Error("the cleared beacon is not marked resolved")
	}
	if active := store.active(); len(active) != 0 {
		t.Errorf("%v beacons still active after clearing, want 0", len(active))
	}
}

// TestSOSStoreReportsACorruptFile asserts a damaged state file is reported
// rather than silently replaced with an empty registry.
func TestSOSStoreReportsACorruptFile(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, sosFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing the corrupt file: %v", err)
	}
	store := newSOSStore(dir)
	if active := store.active(); len(active) != 0 {
		t.Errorf("a corrupt file yielded %v beacons, want none", len(active))
	}
	if store.loadErr == nil {
		t.Error("a corrupt state file was not reported")
	}
}

// TestSOSCommandRaisesABeacon asserts one raise records the beacon, alerts the
// room, confirms to the sender by direct NOTICE, and answers in the documented
// shape.
func TestSOSCommandRaisesABeacon(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	fake.setCapability(rrc.CapDirectNotice, true)
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "sos 849VCWC8+R9 RED 2 hikers, 1 leg fracture", now)
	if len(lines) != 1 {
		t.Fatalf("sos returned %v lines, want 1: %v", len(lines), lines)
	}
	want := "[SOS #1 RECORDED] RED @ 849VCWC8+R9 by @Alice: 2 hikers, 1 leg fracture | Alerted room"
	if lines[0] != want {
		t.Errorf("sos =\n  %v\nwant\n  %v", lines[0], want)
	}

	alerts := noticeTexts(fake)
	foundAlert := false
	for _, text := range alerts {
		if strings.Contains(text, "[SOS ALERT #1] RED at 849VCWC8+R9 by @Alice: 2 hikers, 1 leg fracture") {
			foundAlert = true
		}
	}
	if !foundAlert {
		t.Errorf("the room notices were %v, want a high-priority alert among them", alerts)
	}
	direct := directTexts(fake)
	if len(direct) != 1 || !strings.Contains(direct[0], "[SOS #1] RED recorded at 849VCWC8+R9") {
		t.Errorf("direct notices = %v, want the sender's confirmation", direct)
	}
}

// TestSOSCommandAlertsEveryJoinedRoom asserts the alert is broadcast to every
// room the bot has joined, which is what makes it reach whoever is listening.
func TestSOSCommandAlertsEveryJoinedRoom(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil, "general", "emergency")
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "sos 849VCWC8+R9 YELLOW lost party of three", now)
	if len(lines) != 1 || !strings.Contains(lines[0], "| Alerted 2 rooms") {
		t.Fatalf("sos = %v, want a confirmation naming two alerted rooms", lines)
	}
	var rooms []string
	fake.mu.Lock()
	for _, notice := range fake.notices {
		if strings.Contains(notice.Text, "[SOS ALERT") {
			rooms = append(rooms, notice.Room)
		}
	}
	fake.mu.Unlock()
	slices.Sort(rooms)
	if !slices.Equal(rooms, []string{"emergency", "general"}) {
		t.Errorf("alerted rooms = %v, want both joined rooms", rooms)
	}
}

// TestSOSCommandQueuesLXMFDispatch asserts a configured dispatch destination
// gets the beacon over LXMF, which is the copy that survives the local link.
func TestSOSCommandQueuesLXMFDispatch(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.LXMFEnabled = true
	cfg.EmergencyLXMFDestination = "4643602e6f3b1c0d8a9b7e5f4d3c2b1a"
	cfg.EmergencyLXMFDestinationHash = mustHex(cfg.EmergencyLXMFDestination)
	reg, session, fake, _ := storedFixture(t, cfg)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	sender := newFakeLXMF()
	reg.lxmf = sender
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "sos 849VCWC8+R9 RED trapped by rising water", now)
	if len(lines) != 1 || !strings.Contains(lines[0], sosLXMFQueued) {
		t.Fatalf("sos = %v, want a confirmation naming the LXMF dispatch queue", lines)
	}
	if sender.sendCount() != 1 {
		t.Fatalf("LXMF sends = %v, want 1", sender.sendCount())
	}
	text := sender.texts()[0]
	if !strings.Contains(text, "[SOS #1] RED at 849VCWC8+R9") || !strings.Contains(text, "trapped by rising water") {
		t.Errorf("dispatch payload = %q, want the beacon", text)
	}
	if got := hexString(sender.last().PeerHash); got != cfg.EmergencyLXMFDestination {
		t.Errorf("dispatch went to %v, want the configured destination %v", got, cfg.EmergencyLXMFDestination)
	}
}

// TestSOSListAndClear asserts the listing shows the active beacons with their
// age and that clearing is reflected immediately.
func TestSOSListAndClear(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	raised := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	runLinesAt(t, reg, session, "sos 849VCWC8+R9 RED 2 hikers, 1 leg fracture", raised)

	lines := runLinesAt(t, reg, session, "sos list", raised.Add(3*time.Hour))
	if len(lines) != 2 {
		t.Fatalf("sos list = %v, want a header and one row", lines)
	}
	if !strings.Contains(lines[0], "Active SOS beacons: 1 beacon") {
		t.Errorf("sos list header = %q", lines[0])
	}
	want := "#1 [RED] 849VCWC8+R9 (3h ago) by @Alice: 2 hikers, 1 leg fracture"
	if lines[1] != want {
		t.Errorf("sos list row =\n  %v\nwant\n  %v", lines[1], want)
	}

	cleared := runLinesAt(t, reg, session, "sos clear 1", raised.Add(4*time.Hour))
	if len(cleared) != 1 || cleared[0] != "[SOS #1 CLEARED]" {
		t.Errorf("sos clear = %v, want the cleared line", cleared)
	}
	empty := runLinesAt(t, reg, session, "sos list", raised.Add(4*time.Hour))
	if len(empty) != 1 || empty[0] != sosNoBeaconsLine {
		t.Errorf("sos list after clearing = %v, want %q", empty, sosNoBeaconsLine)
	}
}

// TestSOSCommandRejectsBadRequests asserts a malformed beacon gets the usage
// lines instead of a half-recorded beacon.
func TestSOSCommandRejectsBadRequests(t *testing.T) {
	t.Parallel()

	reg, session, _, _ := storedFixture(t, nil)
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	for _, line := range []string{
		"sos",
		"sos 849VCWC8+R9",
		"sos 849VCWC8+R9 RED",
		"sos 849VCWC8+R9 PURPLE help me",
	} {
		lines := runLinesAt(t, reg, session, line, now)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+sosUsage) {
			t.Errorf("%q = %v, want the usage line", line, lines)
		}
	}
	// A location that cannot be placed is refused with the notation help rather
	// than being recorded half-way.
	unplaced := runLinesAt(t, reg, session, "sos nowhere RED help me", now)
	if len(unplaced) == 0 || !strings.Contains(unplaced[0], "no location found") {
		t.Errorf("sos with an unplaceable location = %v, want the notation help", unplaced)
	}
}

// TestSOSStoreKeepsWorkingWhenTheDiskFails asserts a beacon that cannot be
// persisted is still alerted on: a full disk must never cost somebody their
// call for help.
func TestSOSStoreKeepsWorkingWhenTheDiskFails(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	// Make the state path a directory, so every write fails.
	if err := os.MkdirAll(filepath.Join(dir, sosFileName), 0o700); err != nil {
		t.Fatalf("creating the blocking directory: %v", err)
	}
	reg, session, fake, _ := storedFixture(t, nil)
	reg.bot.cfg.StorageDir = dir
	reg.bot.sos = newSOSStore(dir)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "sos 849VCWC8+R9 RED generator failed, no heat", now)
	if len(lines) != 1 || !strings.Contains(lines[0], "[SOS #1 RECORDED] RED") {
		t.Fatalf("sos with an unwritable store = %v, want the beacon recorded in memory", lines)
	}
	if len(noticeTexts(fake)) == 0 {
		t.Error("the room was not alerted when the store could not be written")
	}
}
