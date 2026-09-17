// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"strings"
	"testing"
	"time"
)

// sitrepBase is the fixed clock the situation-report tests run on.
var sitrepBase = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

// sitrepPoint is the coordinate the situation-report tests query around.
var sitrepPoint = LatLng{Lat: 37.4220625, Lng: -122.0840625}

// pinStoreClock fixes every store in a fixture to the same clock the requests
// run on, so the seven-day expiry never sees the reports as long past.
func pinStoreClock(session *hubSession, at time.Time) {
	session.bot.sitreps.now = func() time.Time { return at }
	session.bot.sos.now = func() time.Time { return at }
	session.bot.checkins.now = func() time.Time { return at }
}

// TestSitrepStoreRecordsNearAndRecent asserts a report is recorded with a
// position and answered by proximity and by recency.
func TestSitrepStoreRecordsNearAndRecent(t *testing.T) {
	t.Parallel()

	store := newSitrepStore(tempDir(t))
	store.now = func() time.Time { return sitrepBase }
	near := ProjectWaypoint(sitrepPoint, 45, 1200)
	far := ProjectWaypoint(sitrepPoint, 225, 4800)
	first, err := store.add(SitrepReport{Category: "HAZARD", LatLng: near, Text: "Bridge out"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.add(SitrepReport{Category: "RESOURCE", LatLng: far, Text: "Clean water"}); err != nil {
		t.Fatalf("second add: %v", err)
	}
	if first.ID != 1 {
		t.Errorf("first report id = %v, want 1", first.ID)
	}

	found := store.near(sitrepPoint, 10000)
	if len(found) != 2 {
		t.Fatalf("near found %v reports, want 2", len(found))
	}
	if found[0].Meters > found[1].Meters {
		t.Errorf("near is not sorted closest first: %v then %v", found[0].Meters, found[1].Meters)
	}
	if found[0].Report.Category != "HAZARD" {
		t.Errorf("closest report = %v, want the hazard", found[0].Report.Category)
	}
	if close := store.near(sitrepPoint, 2000); len(close) != 1 {
		t.Errorf("a two-kilometer search found %v reports, want 1", len(close))
	}

	recent := store.recent(5)
	if len(recent) != 2 || recent[0].ID != 2 {
		t.Errorf("recent = %+v, want newest first", recent)
	}
}

// TestSitrepStoreExpiresOldReports asserts a report past its week is gone, and
// that one inside the week survives.
func TestSitrepStoreExpiresOldReports(t *testing.T) {
	t.Parallel()

	store := newSitrepStore(tempDir(t))
	store.now = func() time.Time { return sitrepBase }
	if _, err := store.add(SitrepReport{Category: "ROAD", LatLng: sitrepPoint, Text: "Washout"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Inside the week the report is still there.
	store.now = func() time.Time { return sitrepBase.Add(6 * 24 * time.Hour) }
	if got := len(store.all()); got != 1 {
		t.Fatalf("six days later the board holds %v reports, want 1", got)
	}
	// Past it, the report is gone.
	store.now = func() time.Time { return sitrepBase.Add(sitrepTTL + time.Minute) }
	if got := len(store.all()); got != 0 {
		t.Errorf("a week later the board holds %v reports, want none", got)
	}
}

// TestSitrepStoreCapsTheBoard asserts the board never grows past its bound and
// keeps the newest reports.
func TestSitrepStoreCapsTheBoard(t *testing.T) {
	t.Parallel()

	// The bound is exercised in memory: the on-disk round trip is its own test.
	store := newSitrepStore("")
	store.now = func() time.Time { return sitrepBase }
	for i := range maxSitrepReports + 3 {
		if _, err := store.add(SitrepReport{Category: "INFO", LatLng: sitrepPoint,
			Text: "report", Timestamp: sitrepBase.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatalf("add %v: %v", i, err)
		}
	}
	all := store.all()
	if len(all) != maxSitrepReports {
		t.Fatalf("board holds %v reports, want %v", len(all), maxSitrepReports)
	}
	if all[0].ID != 4 {
		t.Errorf("oldest surviving report = %v, want 4 (the first three were trimmed)", all[0].ID)
	}
}

// TestSitrepStoreRoundTripsThroughDisk asserts the board survives a restart.
func TestSitrepStoreRoundTripsThroughDisk(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	first := newSitrepStore(dir)
	first.now = func() time.Time { return sitrepBase }
	if _, err := first.add(SitrepReport{Category: "SHELTER", Sender: "Claire",
		LatLng: sitrepPoint, Location: "849VCWC8+R9", Text: "School gym open"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	second := newSitrepStore(dir)
	second.now = func() time.Time { return sitrepBase }
	all := second.all()
	if len(all) != 1 || all[0].Text != "School gym open" || all[0].Category != "SHELTER" {
		t.Fatalf("reloaded board = %+v, want the recorded report", all)
	}
	// The next report continues the numbering.
	next, err := second.add(SitrepReport{Category: "INFO", LatLng: sitrepPoint, Text: "more"})
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if next.ID != 2 {
		t.Errorf("second report id = %v, want 2", next.ID)
	}
}

// TestSitrepCommandAddsAReport asserts the confirmation names the number, the
// category, the position, and the reporter.
func TestSitrepCommandAddsAReport(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	pinStoreClock(session, sitrepBase)

	lines := runLinesAt(t, reg, session, "sitrep add 849VCWC8+R9 HAZARD Bridge out on Route 4, live wire down", sitrepBase)
	if len(lines) != 1 {
		t.Fatalf("sitrep add returned %v lines, want 1: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "SitRep #1 recorded [HAZARD @ 849") {
		t.Errorf("sitrep add = %q, want the recorded line with the category and position", lines[0])
	}
	if !strings.HasSuffix(lines[0], "] by @Alice") {
		t.Errorf("sitrep add = %q, want the reporter named", lines[0])
	}
}

// TestSitrepCommandNearSortsByDistance asserts a proximity answer is sorted
// closest first and labels each row with the distance and the direction.
func TestSitrepCommandNearSortsByDistance(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	pinStoreClock(session, sitrepBase)

	near := encodeOrFatal(t, ProjectWaypoint(sitrepPoint, 45, 1200))
	far := encodeOrFatal(t, ProjectWaypoint(sitrepPoint, 225, 4800))
	runLinesAt(t, reg, session, "sitrep add "+far+" RESOURCE Clean well water and solar charging", sitrepBase)
	runLinesAt(t, reg, session, "sitrep add "+near+" HAZARD Bridge out, live wire down", sitrepBase.Add(-25*time.Minute))

	lines := runLinesAt(t, reg, session, "sitrep near 849VCWC8+R9 10km", sitrepBase)
	if len(lines) != 3 {
		t.Fatalf("sitrep near = %v, want a header and two rows", lines)
	}
	if lines[0] != "SitReps near 849VCWC8+R9 (within 10 km):" {
		t.Errorf("sitrep near header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "[1.2km NE] HAZARD @ ") {
		t.Errorf("nearest row = %q, want the hazard 1.2 km northeast", lines[1])
	}
	if !strings.HasSuffix(lines[1], "(25m ago): Bridge out, live wire down") {
		t.Errorf("nearest row = %q, want its age and text", lines[1])
	}
	if !strings.HasPrefix(lines[2], "[4.8km SW] RESOURCE @ ") {
		t.Errorf("farthest row = %q, want the resource 4.8 km southwest", lines[2])
	}

	// A radius that reaches nothing says so rather than answering an empty
	// header.
	empty := runLinesAt(t, reg, session, "sitrep near 849VCWC8+R9 1km", sitrepBase)
	if len(empty) != 1 || !strings.Contains(empty[0], "no SitReps within 1 km") {
		t.Errorf("sitrep near with a short radius = %v, want the empty line", empty)
	}
}

// TestSitrepCommandRecent asserts the recency listing is newest first and
// bounded by the number asked for.
func TestSitrepCommandRecent(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	pinStoreClock(session, sitrepBase)
	runLinesAt(t, reg, session, "sitrep add 849VCWC8+R9 HAZARD Bridge out", sitrepBase.Add(-2*time.Hour))
	runLinesAt(t, reg, session, "sitrep add CM87uk SHELTER Gym open, power on", sitrepBase)

	lines := runLinesAt(t, reg, session, "sitrep recent 2", sitrepBase)
	if len(lines) != 3 {
		t.Fatalf("sitrep recent = %v, want a header and two rows", lines)
	}
	if !strings.Contains(lines[0], "Most recent SitReps: 2 reports") {
		t.Errorf("sitrep recent header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "#2 [SHELTER @ ") || !strings.Contains(lines[1], "(0s ago) by @Alice: Gym open, power on") {
		t.Errorf("newest row = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "#1 [HAZARD @ ") || !strings.Contains(lines[2], "(2h ago)") {
		t.Errorf("older row = %q", lines[2])
	}

	// Asking for one keeps only the newest.
	one := runLinesAt(t, reg, session, "sitrep recent 1", sitrepBase)
	if len(one) != 2 || !strings.HasPrefix(one[1], "#2 ") {
		t.Errorf("sitrep recent 1 = %v, want only the newest report", one)
	}

	// An empty board says so.
	emptyReg, emptySession, _, _ := storedFixture(t, nil)
	empty := runLinesAt(t, emptyReg, emptySession, "sitrep recent", sitrepBase)
	if len(empty) != 1 || empty[0] != sitrepEmptyBoardLine {
		t.Errorf("sitrep recent on an empty board = %v, want %q", empty, sitrepEmptyBoardLine)
	}
}

// TestSitrepCommandRejectsBadRequests asserts a malformed report gets the usage
// lines rather than being half-recorded.
func TestSitrepCommandRejectsBadRequests(t *testing.T) {
	t.Parallel()

	reg, session, _, _ := storedFixture(t, nil)
	for _, line := range []string{
		"sitrep",
		"sitrep bogus",
		"sitrep add",
		"sitrep add 849VCWC8+R9",
		"sitrep add 849VCWC8+R9 HAZARD",
		"sitrep add 849VCWC8+R9 WEATHER cloudy",
	} {
		lines := runLinesAt(t, reg, session, line, sitrepBase)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: sitrep") {
			t.Errorf("%q = %v, want a usage line", line, lines)
		}
	}
	unplaced := runLinesAt(t, reg, session, "sitrep add nowhere HAZARD bridge out", sitrepBase)
	if len(unplaced) == 0 || !strings.Contains(unplaced[0], "no location found") {
		t.Errorf("sitrep add with an unplaceable location = %v, want the notation help", unplaced)
	}
}

// TestFormatRadiusAndNearDistance asserts the two renderings a proximity answer
// uses.
func TestFormatRadiusAndNearDistance(t *testing.T) {
	t.Parallel()

	radii := []struct {
		meters float64
		want   string
	}{
		{25000, "25 km"},
		{10000, "10 km"},
		{1500, "1.5 km"},
		{750, "750 m"},
	}
	for _, tc := range radii {
		if got := formatRadius(tc.meters); got != tc.want {
			t.Errorf("formatRadius(%v) = %q, want %q", tc.meters, got, tc.want)
		}
	}
	distances := []struct {
		meters float64
		want   string
	}{
		{1200, "1.2km"},
		{4800, "4.8km"},
		{850, "850m"},
	}
	for _, tc := range distances {
		if got := formatNearDistance(tc.meters); got != tc.want {
			t.Errorf("formatNearDistance(%v) = %q, want %q", tc.meters, got, tc.want)
		}
	}
}

// encodeOrFatal encodes a coordinate at the reporting precision, or fails the
// test.
func encodeOrFatal(t *testing.T, point LatLng) string {
	t.Helper()
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		t.Fatalf("EncodeOLC(%v, %v): %v", point.Lat, point.Lng, err)
	}
	return code
}
