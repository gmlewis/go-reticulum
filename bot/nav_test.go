// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"strings"
	"testing"
)

// TestLocCommandRendersEveryNotation asserts the loc answer carries the same
// position in all four notations, and that a Plus Code round-trips through
// every one of them unchanged.
func TestLocCommandRendersEveryNotation(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "loc 37.42205, -122.08409")
	if len(lines) != 1 {
		t.Fatalf("loc returned %v lines, want 1: %v", len(lines), lines)
	}
	want := "DD: 37.4220°N, 122.0841°W | DDM: 37°25.32'N 122°05.05'W | Grid: CM87wk | OLC: 849VCWC8+R9"
	if lines[0] != want {
		t.Errorf("loc =\n  %v\nwant\n  %v", lines[0], want)
	}

	// The same position typed as a Plus Code renders the identical line.
	fromCode := runLines(t, reg, session, "loc 849VCWC8+R9")
	if len(fromCode) != 1 {
		t.Fatalf("loc <plus code> returned %v lines, want 1", len(fromCode))
	}
	if !strings.Contains(fromCode[0], "OLC: 849VCWC8+R9") {
		t.Errorf("loc <plus code> = %q, want it to name the same Plus Code", fromCode[0])
	}
}

// TestLocCommandAcceptsLeadingAt asserts that coordinates prefixed with an '@'
// (such as copied from Google Maps URLs) are accepted seamlessly.
func TestLocCommandAcceptsLeadingAt(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "loc @28.405832,-81.4716354")
	if len(lines) != 1 {
		t.Fatalf("loc returned %v lines, want 1: %v", len(lines), lines)
	}
	want := "DD: 28.4058°N, 81.4716°W | DDM: 28°24.35'N 81°28.30'W | Grid: EL98gj | OLC: 76WWCG4H+88"
	if lines[0] != want {
		t.Errorf("loc =\n  %v\nwant\n  %v", lines[0], want)
	}
}

// TestLocCommandUsage asserts an empty or unusable loc request explains what a
// location may look like instead of guessing.
func TestLocCommandUsage(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "loc")
	if len(lines) != 2 || !strings.Contains(lines[0], "Usage: "+locUsage) {
		t.Errorf("loc with no argument = %v, want a usage line and the notation help", lines)
	}
	lines = runLines(t, reg, session, "loc somewhere over there")
	if len(lines) != 1 || !strings.Contains(lines[0], locationNotationHelp) {
		t.Errorf("loc <nonsense> = %v, want the notation help", lines)
	}
}

// withFix installs a live GNSS fix on a registry, which is what makes a
// shortened Plus Code placeable. The reader is closed with the test, so no scan
// goroutine outlives the case.
func withFix(t *testing.T, reg *registry, fix GPSFix) {
	t.Helper()
	reader := NewGPSReader(nil)
	reader.SetFix(fix)
	t.Cleanup(func() { _ = reader.Close() })
	reg.gps = reader
}

// TestLocCommandCompletesShortPlusCodes asserts the one notation that names its
// region only relative to somewhere already known is completed against the live
// GNSS fix. This is the search-and-rescue case: a position read off the missing
// party's phone still lands where they were standing.
func TestLocCommandCompletesShortPlusCodes(t *testing.T) {
	t.Parallel()

	// The fix is in San Francisco; the code is the local form of the full code
	// one line up, so both must answer identically.
	reg, session := whereamiFixture(t, sfFix())
	long := runLines(t, reg, session, "loc 849VCWC8+R9")
	lines := runLines(t, reg, session, "loc CWC8+R9")
	if len(lines) != 1 || len(long) != 1 {
		t.Fatalf("loc returned %v lines for the short code and %v for the full one, want 1 each", len(lines), len(long))
	}
	if lines[0] != long[0] {
		t.Errorf("loc <short code> =\n  %v\nwant the full code's answer\n  %v", lines[0], long[0])
	}
	if !strings.Contains(lines[0], "OLC: 849VCWC8+R9") {
		t.Errorf("loc <short code> = %q, want it placed in the 849V region", lines[0])
	}

	// The same code resolves from a fix anywhere in its region, and a code the
	// sender's phone showed with eleven digits is completed to the same place.
	orlando := GPSFix{Valid: true, Lat: 28.405, Lng: -81.47}
	away, awaySession := whereamiFixture(t, orlando)
	lines = runLines(t, away, awaySession, "loc CG4J+32P")
	if len(lines) != 1 || !strings.Contains(lines[0], "OLC: 76WWCG4J+32") {
		t.Errorf("loc CG4J+32P = %v, want it completed in the 76WW region", lines)
	}
}

// TestLocCommandExplainsAShortCodeWithNoFix asserts a shortened Plus Code handed
// to a node with no receiver is answered with the reason it cannot be placed: the
// code is real, and what is missing is the position that says which region it is
// in. Calling a real Plus Code unreadable would blame the wrong thing.
func TestLocCommandExplainsAShortCodeWithNoFix(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "loc CWC8+R9")
	if len(lines) != 1 {
		t.Fatalf("loc <short code> with no fix returned %v lines, want 1: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "no location found") {
		t.Errorf("loc <short code> with no fix = %q, want it not to blame the notation", lines[0])
	}
	if !strings.Contains(lines[0], "GNSS fix") {
		t.Errorf("loc <short code> with no fix = %q, want it to name the missing fix", lines[0])
	}
}

// TestShortPlusCodesResolveInEveryLocationCommand asserts the reference is
// supplied wherever a command accepts a location, not only in loc: a search team
// types the same short code whichever question it asks.
func TestShortPlusCodesResolveInEveryLocationCommand(t *testing.T) {
	t.Parallel()

	nav, navSession := whereamiFixture(t, sfFix())
	stored, storedSession, _, _ := storedFixture(t, nil)
	withFix(t, stored, sfFix())
	tests := []struct {
		name    string
		reg     *registry
		session *hubSession
		line    string
		want    string
	}{
		{"loc", nav, navSession, "loc CWC8+R9", "OLC: 849VCWC8+R9"},
		{"dist", nav, navSession, "dist CWC8+R9 to 8FVC9G8F+6X", "Heading 031° (NNE)"},
		{"proj", nav, navSession, "proj CWC8+R9 048 3.5km", "Target: 849VCWVW+65"},
		{"sun", nav, navSession, "sun CWC8+R9 2026-06-21", "(849VCWC8+R9)"},
		{"whereami", nav, navSession, "whereami CWC8+R9", "849VCWC8+R9"},
		{"checkin", stored, storedSession, "checkin CWC8+R9 overdue 4h at the trailhead", "849VCWC8+R9"},
		{"sitrep", stored, storedSession, "sitrep add CWC8+R9 HAZARD bridge out", "849VCWC8+R9"},
		{"sos", stored, storedSession, "sos CWC8+R9 RED two hikers", "849VCWC8+R9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := runLines(t, tc.reg, tc.session, tc.line)
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("%q = %v, want it to carry %q", tc.line, lines, tc.want)
			}
			if strings.Contains(joined, "no location found") || strings.Contains(joined, "Usage:") {
				t.Errorf("%q = %v, want the short code placed", tc.line, lines)
			}
		})
	}
}

// TestDistCommandReportsCourseAndReturn asserts the dist answer gives the
// distance plus both headings, which is what a navigator plots.
func TestDistCommandReportsCourseAndReturn(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "dist 849VCWC8+R9 to 8FVC9G8F+6X")
	if len(lines) != 1 {
		t.Fatalf("dist returned %v lines, want 1: %v", len(lines), lines)
	}
	want := "9388.96 km (5834.03 mi, 5069.63 nm) | Heading 031° (NNE) | Return 323° (NW)"
	if lines[0] != want {
		t.Errorf("dist =\n  %v\nwant\n  %v", lines[0], want)
	}

	// A pair of bare coordinates without a "to" separator splits on the only
	// boundary where both halves parse.
	bare := runLines(t, reg, session, "dist 37.42205 -122.08409 47.365590 8.524997")
	if len(bare) != 1 || !strings.Contains(bare[0], "Heading 031° (NNE)") {
		t.Errorf("dist <bare pair> = %v, want the same course", bare)
	}
}

// TestDistCommandUsage asserts an unusable dist request is answered with its
// usage rather than a wrong distance.
func TestDistCommandUsage(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{"dist", "dist 849VCWC8+R9", "dist nothing at all"} {
		lines := runLines(t, reg, session, line)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+distUsage) {
			t.Errorf("%q = %v, want the usage line", line, lines)
		}
	}
}

// TestProjCommandProjectsAWaypoint asserts dead reckoning renders as a Plus
// Code, a coordinate, a grid locator, and the course that produced it.
func TestProjCommandProjectsAWaypoint(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "proj 849VCWC8+R9 048 3.5km")
	if len(lines) != 1 {
		t.Fatalf("proj returned %v lines, want 1: %v", len(lines), lines)
	}
	want := "Target: 849VCWVW+65 | 37.4431°N, 122.0546°W | Grid: CM87xk (dist: 3.50 km, bearing: 048° NE)"
	if lines[0] != want {
		t.Errorf("proj =\n  %v\nwant\n  %v", lines[0], want)
	}

	// A compass point and a statute-mile distance are accepted too.
	miles := runLines(t, reg, session, "proj 849VCWC8+R9 NE 2mi")
	if len(miles) != 1 || !strings.Contains(miles[0], "(dist: 3.22 km, bearing: 045° NE)") {
		t.Errorf("proj with NE and miles = %v, want a 3.22 km northeast target", miles)
	}
}

// TestProjCommandUsage asserts an unusable projection is answered with its
// usage, including what a bearing and a distance may look like.
func TestProjCommandUsage(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{"proj", "proj 849VCWC8+R9", "proj 849VCWC8+R9 048", "proj nowhere 048 1km"} {
		lines := runLines(t, reg, session, line)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+projUsage) {
			t.Errorf("%q = %v, want the usage line", line, lines)
		}
	}
}

// TestSunCommandReportsTheAlmanac asserts the sun answer names the date, the
// position, the day's light, and the moon, with the times in UTC.
func TestSunCommandReportsTheAlmanac(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "sun 849VCWC8+R9 2026-06-21")
	if len(lines) != 3 {
		t.Fatalf("sun returned %v lines, want 3: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "Almanac for 2026-06-21 at ") {
		t.Errorf("sun first line = %q, want the date and position", lines[0])
	}
	if !strings.Contains(lines[0], "(849VCWC8+R9)") {
		t.Errorf("sun first line = %q, want the Plus Code", lines[0])
	}
	for _, field := range []string{"Dawn: ", "Sunrise: ", "Noon: ", "Sunset: ", "Dusk: ", "Day: ", "(UTC)"} {
		if !strings.Contains(lines[1], field) {
			t.Errorf("sun almanac line = %q, want it to carry %q", lines[1], field)
		}
	}
	if !strings.HasPrefix(lines[2], "Moon: ") || !strings.Contains(lines[2], "% illuminated)") {
		t.Errorf("sun moon line = %q, want the phase and its illumination", lines[2])
	}
}

// TestSunCommandHandlesPolarLatitudes asserts a latitude where the sun does not
// rise, or does not set, is reported as such instead of printing a nonsense
// time.
func TestSunCommandHandlesPolarLatitudes(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	night := runLines(t, reg, session, "sun 78.2232, 15.6469 2026-12-21")
	if len(night) < 2 || !strings.Contains(night[1], "polar night") {
		t.Errorf("Svalbard in December = %v, want a polar night line", night)
	}
	day := runLines(t, reg, session, "sun 78.2232, 15.6469 2026-06-21")
	if len(day) < 2 || !strings.Contains(day[1], "midnight sun") {
		t.Errorf("Svalbard in June = %v, want a midnight sun line", day)
	}
}

// TestSunCommandUsage asserts an unusable sun request is answered with its
// usage.
func TestSunCommandUsage(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{"sun", "sun nowhere in particular"} {
		lines := runLines(t, reg, session, line)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+sunUsage) {
			t.Errorf("%q = %v, want the usage line", line, lines)
		}
	}
}

// TestNavigationCommandsAreDocumented asserts help explains every navigation
// command, so the commands are discoverable from the bot itself.
func TestNavigationCommandsAreDocumented(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, name := range []string{"loc", "dist", "proj", "sun"} {
		cmd, ok := reg.byName[name]
		if !ok {
			t.Errorf("help has no entry for %q", name)
			continue
		}
		if cmd.summary == "" || cmd.usage == "" || len(cmd.detail) == 0 {
			t.Errorf("command %q is not documented (summary %q, usage %q, %v detail lines)",
				name, cmd.summary, cmd.usage, len(cmd.detail))
		}
		lines := runLines(t, reg, session, "help "+name)
		if len(lines) != helpLineCount(cmd, defaultTestConfig()) {
			t.Errorf("help %v returned %v lines, want %v", name, len(lines), helpLineCount(cmd, defaultTestConfig()))
		}
	}
}
