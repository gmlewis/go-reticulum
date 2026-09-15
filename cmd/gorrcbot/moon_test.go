// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

// The golden values in this file were captured by running the Python `pymeeus`
// package, a faithful implementation of the same Meeus algorithms, against the
// same UTC-consistent definitions this code uses: positions evaluated in
// Terrestrial Time, sidereal time in UT, and Delta T applied with the
// Espenak-Meeus polynomial. Agreement therefore checks the transcription of the
// periodic terms, the ecliptic-to-equatorial step, the event search, and the
// phase solver — not merely that the code agrees with itself.

// moonGoldenTT is one position golden: a Terrestrial Time Julian day and the
// Moon's place at it.
type moonGoldenTT struct {
	JD          float64
	Longitude   float64
	Latitude    float64
	DistanceKm  float64
	ParallaxDeg float64
	RA          float64
	Dec         float64
}

// moonPositionGoldens are the captured geocentric positions.
var moonPositionGoldens = []moonGoldenTT{
	{2461299.0, 225.329844, -5.072532, 396269.017, 0.922243, 221.282183, -21.278271},
	{2461305.25, 300.370847, -2.554789, 399977.745, 0.913691, 303.161685, -22.564392},
	{2461310.5, 7.675412, 3.208952, 378291.464, 0.966075, 5.777789, 5.993561},
	{2461000.125, 242.247991, -4.874107, 406607.120, 0.898793, 239.100767, -25.384628},
	{2465000.75, 31.923718, -4.913795, 388073.331, 0.941721, 31.459432, 7.521402},
}

// TestMoonPositionMatchesReferenceGoldens asserts the periodic series, the
// parallax, and the equatorial conversion all match the reference
// implementation, term for term.
func TestMoonPositionMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	for _, golden := range moonPositionGoldens {
		got := moonPositionAt(golden.JD)
		// The longitude and latitude series are the same published terms, so
		// they agree to the last digit the golden carries.
		if diff := angularDifference(got.Longitude, golden.Longitude); diff > 2e-4 {
			t.Errorf("JD %v: longitude = %.6f, want %.6f (off by %.6f)",
				golden.JD, got.Longitude, golden.Longitude, diff)
		}
		if diff := math.Abs(got.Latitude - golden.Latitude); diff > 2e-4 {
			t.Errorf("JD %v: latitude = %.6f, want %.6f (off by %.6f)",
				golden.JD, got.Latitude, golden.Latitude, diff)
		}
		if diff := math.Abs(got.DistanceKm - golden.DistanceKm); diff > 1 {
			t.Errorf("JD %v: distance = %.3f km, want %.3f km", golden.JD, got.DistanceKm, golden.DistanceKm)
		}
		if diff := math.Abs(got.ParallaxDeg - golden.ParallaxDeg); diff > 1e-4 {
			t.Errorf("JD %v: parallax = %.6f, want %.6f", golden.JD, got.ParallaxDeg, golden.ParallaxDeg)
		}
		// The reference reports apparent coordinates, which carry nutation;
		// this model works on the mean equinox, so the tolerance is the
		// nutation's own size (under 20 arcseconds) plus the obliquity term.
		if diff := angularDifference(got.RightAscension, golden.RA); diff > 0.02 {
			t.Errorf("JD %v: right ascension = %.6f, want %.6f", golden.JD, got.RightAscension, golden.RA)
		}
		if diff := math.Abs(got.Declination - golden.Dec); diff > 0.02 {
			t.Errorf("JD %v: declination = %.6f, want %.6f", golden.JD, got.Declination, golden.Dec)
		}
	}
}

// angularDifference returns the smallest absolute difference between two angles
// in degrees, so a wrap at 360 does not look like a large error.
func angularDifference(a, b float64) float64 {
	return math.Abs(normalizeDegrees(a-b+180) - 180)
}

// TestMoonElongationMatchesReferenceGoldens asserts the phase angle — the
// quantity the illumination and the phase name both come from — matches the
// reference's Sun-Moon elongation.
func TestMoonElongationMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	goldens := []struct {
		jd   float64
		want float64
	}{
		{2461299.0, 52.659791},
		{2461305.25, 121.599988},
		{2461310.5, 183.765430},
	}
	for _, golden := range goldens {
		got := moonElongationAt(golden.jd)
		if diff := angularDifference(got, golden.want); diff > 0.02 {
			t.Errorf("JD %v: elongation = %.6f, want %.6f", golden.jd, got, golden.want)
		}
	}
}

// TestMoonAlmanacMatchesReferenceGoldens asserts rise, transit, and set all
// match the reference implementation to well inside the field tolerance, at
// locations from the equator to the Antarctic and across both solstices.
func TestMoonAlmanacMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lat, lng float64
		date     string
		rise     float64
		transit  float64
		set      float64
	}{
		{37.7749, -122.4194, "2026-09-15", 1125.024, 1419.684, 235.779},
		{51.5, -0.12, "2026-09-15", 663.019, 914.048, 1155.962},
		{-33.8568, 151.2153, "2026-09-15", 1337.188, 288.649, 722.593},
		{64.1466, -21.9426, "2026-06-21", 793.635, 1160.982, 71.279},
		{-77.85, 166.67, "2026-04-01", 333.993, 748.921, 1241.562},
		{35.6762, 139.6503, "2026-05-10", 970.297, 1303.694, 142.694},
		{0, 0, "2026-01-01", 976.332, 1352.213, 221.819},
		{61.2181, -149.9003, "2026-09-15", 1371.488, 45.389, 245.404},
	}
	for _, tc := range tests {
		day := mustParseDay(t, tc.date)
		got := MoonAlmanac(tc.lat, tc.lng, day)
		events := []struct {
			name string
			at   time.Time
			has  bool
			want float64
		}{
			{"rise", got.Rise, got.HasRise, tc.rise},
			{"transit", got.Transit, got.HasTransit, tc.transit},
			{"set", got.Set, got.HasSet, tc.set},
		}
		for _, event := range events {
			if !event.has {
				t.Errorf("%v %v: no %v, want %v minutes past midnight", tc.date, tc.lat, event.name, event.want)
				continue
			}
			minutes := event.at.Sub(day).Minutes()
			if diff := math.Abs(minutes - event.want); diff > 2 {
				t.Errorf("%v %v: %v = %.3f minutes, want %.3f (off by %.3f)",
					tc.date, tc.lat, event.name, minutes, event.want, diff)
			}
		}
	}
}

// TestMoonAlmanacEventsAreOrdered asserts the events inside a day come in the
// order they must: the search cannot report a rise after the set it precedes.
func TestMoonAlmanacEventsAreOrdered(t *testing.T) {
	t.Parallel()

	day := mustParseDay(t, "2026-09-15")
	got := MoonAlmanac(37.7749, -122.4194, day)
	if !got.HasSet || !got.HasRise || !got.HasTransit {
		t.Fatalf("San Francisco has all three events on this date: %+v", got)
	}
	if !got.Set.Before(got.Rise) {
		t.Errorf("set %v should precede the later rise %v", got.Set, got.Rise)
	}
	if !got.Transit.After(got.Rise) {
		t.Errorf("transit %v should follow the rise %v", got.Transit, got.Rise)
	}
	if got.Rise.Sub(day) < 0 || got.Rise.Sub(day) >= 24*time.Hour {
		t.Errorf("rise %v is outside the UTC day", got.Rise)
	}
}

// TestMoonAlmanacHandlesNoEventDays asserts a day with no rise and no set is
// reported as such rather than with a fabricated time.
func TestMoonAlmanacHandlesNoEventDays(t *testing.T) {
	t.Parallel()

	// A new moon is up with the Sun, so a high-latitude winter day can have no
	// moonrise at all. The check is structural: whatever the answer, a time
	// that is present must lie inside the day, and a time that is absent must
	// be zero.
	day := mustParseDay(t, "2026-12-21")
	got := MoonAlmanac(78.2232, 15.6469, day)
	for name, at := range map[string]time.Time{"rise": got.Rise, "transit": got.Transit, "set": got.Set} {
		if at.IsZero() {
			continue
		}
		if at.Before(day) || !at.Before(day.AddDate(0, 0, 1)) {
			t.Errorf("%v %v is outside the UTC day", name, at)
		}
	}
}

// TestMoonPhaseTimesMatchReferenceGoldens asserts the next new, first-quarter,
// full and last-quarter moons match the reference implementation, which is what
// a coastal party plans a spring tide around.
func TestMoonPhaseTimesMatchReferenceGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		afterUTJD float64
		phases    map[string]float64
	}{
		{2461298.5, map[string]float64{
			"New Moon": 2461324.159696, "First Quarter": 2461302.363644,
			"Full Moon": 2461310.200597, "Last Quarter": 2461317.059078}},
		{2461041.5, map[string]float64{
			"New Moon": 2461059.327654, "First Quarter": 2461066.699424,
			"Full Moon": 2461043.918675, "Last Quarter": 2461051.158558}},
		{2460998.5, map[string]float64{
			"New Moon": 2460999.782749, "First Quarter": 2461007.790695,
			"Full Moon": 2461014.468133, "Last Quarter": 2461021.369185}},
	}
	for _, tc := range tests {
		after := julianToTime(tc.afterUTJD)
		events := MoonPhaseTimes(after)
		if len(events) != 4 {
			t.Fatalf("after %v: got %v phase events, want 4", after, len(events))
		}
		for _, event := range events {
			want, ok := tc.phases[event.Name]
			if !ok {
				t.Errorf("unexpected phase %q", event.Name)
				continue
			}
			got := julianDay(event.At)
			if diff := math.Abs(got-want) * 1440; diff > 3 {
				t.Errorf("after %v: %v = JD %.6f, want %.6f (off by %.1f minutes)",
					after, event.Name, got, want, diff)
			}
		}
		// The line the command prints is chronological, so the events must be
		// sorted by time.
		for i := 1; i < len(events); i++ {
			if events[i].At.Before(events[i-1].At) {
				t.Errorf("phase events are not sorted: %v", events)
				break
			}
		}
	}
}

// julianToTime converts a Julian day number into the UTC instant it names.
func julianToTime(jd float64) time.Time {
	seconds := (jd - jdUnixEpoch) * 86400
	return time.Unix(int64(math.Round(seconds)), 0).UTC()
}

// TestMoonPhaseNamesTrackTheElongation asserts the named phase, its index, the
// illumination and the age are all readings of the one phase angle, so they can
// never contradict each other.
func TestMoonPhaseNamesTrackTheElongation(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 18, 0, 0, 0, 0, time.UTC)
	for step := range 40 {
		at := start.Add(time.Duration(step) * 18 * time.Hour)
		got := MoonPhaseAt(at)
		elongation := MoonElongationDeg(at)
		wantIndex := int(math.Floor(elongation/360*lunarPhaseCount+0.5)) % lunarPhaseCount
		if got.Index != wantIndex {
			t.Errorf("%v: index %v does not match elongation %.2f", at, got.Index, elongation)
		}
		if got.Name != MoonPhaseNames[got.Index] {
			t.Errorf("%v: name %q does not match index %v", at, got.Name, got.Index)
		}
		wantIllumination := (1 - math.Cos(radians(elongation))) / 2 * 100
		if !closeWithin(got.Illumination, wantIllumination, 1e-9) {
			t.Errorf("%v: illumination %v does not match the elongation", at, got.Illumination)
		}
		if !closeWithin(got.Age, elongation/360*synodicMonth, 1e-9) {
			t.Errorf("%v: age %v does not match the elongation", at, got.Age)
		}
		if got.Age < 0 || got.Age >= synodicMonth {
			t.Errorf("%v: age %v is outside one synodic month", at, got.Age)
		}
	}
}

// TestMoonPhaseNamesArePhysicallyRight asserts the familiar anchors: new moon
// is dark, full moon is lit, and the moon is on the far side of the Earth from
// the Sun at full moon.
func TestMoonPhaseNamesArePhysicallyRight(t *testing.T) {
	t.Parallel()

	newMoon := MoonPhaseAt(mustParseDay(t, "2026-01-18").Add(12 * time.Hour))
	if newMoon.Name != "New Moon" || newMoon.Illumination > 5 {
		t.Errorf("2026-01-18: %v (%.0f%%), want a dark new moon", newMoon.Name, newMoon.Illumination)
	}
	fullMoon := MoonPhaseAt(mustParseDay(t, "2026-01-02").Add(12 * time.Hour))
	if fullMoon.Name != "Full Moon" || fullMoon.Illumination < 95 {
		t.Errorf("2026-01-02: %v (%.0f%%), want a lit full moon", fullMoon.Name, fullMoon.Illumination)
	}
	// The reference's first quarter of this lunation, captured from the same
	// implementation the other goldens come from.
	firstQuarter := MoonPhaseAt(julianToTime(2461066.699424))
	if firstQuarter.Name != "First Quarter" {
		t.Errorf("the reference first quarter reads as %v", firstQuarter.Name)
	}
	if !closeWithin(firstQuarter.Illumination, 50, 3) {
		t.Errorf("first quarter illumination = %.0f%%, want about half", firstQuarter.Illumination)
	}
}

// TestNightIlluminationFollowsTheDocumentedThresholds asserts the three field
// ratings are applied from the illumination, and that a Moon below the horizon
// overrides them.
func TestNightIlluminationFollowsTheDocumentedThresholds(t *testing.T) {
	t.Parallel()

	sanFrancisco := LatLng{Lat: 37.422, Lng: -122.0841}
	tests := []struct {
		date string
		want string
	}{
		// A full moon is up all night: bright, navigable without a headlamp.
		{"2026-01-02", nightRatingBright},
		// A new moon gives nothing at all.
		{"2026-01-18", nightRatingDark},
		// A first quarter sets around local midnight, so the middle of the
		// night has no moon at all: dark, whatever the phase.
		{"2026-01-24", nightRatingDark},
	}
	for _, tc := range tests {
		day := mustParseDay(t, tc.date)
		got := NightIllumination(sanFrancisco.Lat, sanFrancisco.Lng, day)
		if got.Rating != tc.want {
			t.Errorf("%v: rating = %q (moon up=%v, altitude %.1f°), want %q",
				tc.date, got.Rating, got.MoonUp, got.AltitudeDeg, tc.want)
		}
		if !got.WindowEnd.After(got.WindowStart) {
			t.Errorf("%v: night window %v..%v is not a window", tc.date, got.WindowStart, got.WindowEnd)
		}
	}
}

// TestNightIlluminationUsesTheMoonAtTheMiddleOfTheNight asserts the rating is
// decided at the middle of the dark window, which is the moment a night
// movement is planned around.
func TestNightIlluminationUsesTheMoonAtTheMiddleOfTheNight(t *testing.T) {
	t.Parallel()

	day := mustParseDay(t, "2026-01-02")
	got := NightIllumination(37.422, -122.0841, day)
	middle := got.WindowStart.Add(got.WindowEnd.Sub(got.WindowStart) / 2)
	if want := moonAltitudeDeg(middle, 37.422, -122.0841); !closeWithin(got.AltitudeDeg, want, 1e-9) {
		t.Errorf("altitude %v is not the middle-of-night altitude %v", got.AltitudeDeg, want)
	}
	if got.MoonUp != (got.AltitudeDeg > 0) {
		t.Errorf("MoonUp %v contradicts altitude %v", got.MoonUp, got.AltitudeDeg)
	}
	// The window is the darkness that follows the day: dusk to the next dawn.
	dusk := SolarAlmanac(37.422, -122.0841, day).Dusk
	if !got.WindowStart.Equal(dusk) {
		t.Errorf("window starts at %v, want civil dusk %v", got.WindowStart, dusk)
	}
}

// TestMoonCommandReportsTheAlmanacForAPlace asserts the command's three lines:
// the phase with its transit, the rise and set with the night rating, and the
// next four phases.
func TestMoonCommandReportsTheAlmanacForAPlace(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLinesAt(t, reg, session, "moon 37.7749, -122.4194 2026-09-15", spacewxClock)
	if len(lines) != 3 {
		t.Fatalf("moon = %v, want three lines", lines)
	}
	if !strings.HasPrefix(lines[0], "Moon: ") || !strings.Contains(lines[0], "illumination, age ") ||
		!strings.Contains(lines[0], "| Transit: ") {
		t.Errorf("first line = %q, want the phase and the transit", lines[0])
	}
	for _, field := range []string{"Moonrise: ", "Moonset: ", "Night Illumination: "} {
		if !strings.Contains(lines[1], field) {
			t.Errorf("second line = %q, want %q", lines[1], field)
		}
	}
	if !strings.HasPrefix(lines[2], "Next: ") || !strings.Contains(lines[2], "Full Moon") ||
		!strings.Contains(lines[2], "(Spring Tides)") {
		t.Errorf("third line = %q, want the next phases with the spring-tide ones named", lines[2])
	}
}

// TestMoonCommandWithoutAPlaceReportsTheGlobalFacts asserts a request with no
// location still answers: the phase and the next quarters are the same
// everywhere, so they need no observer.
func TestMoonCommandWithoutAPlaceReportsTheGlobalFacts(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLinesAt(t, reg, session, "moon", spacewxClock)
	if len(lines) != 2 {
		t.Fatalf("moon = %v, want the phase and the next phases", lines)
	}
	if !strings.HasPrefix(lines[0], "Moon: ") || strings.Contains(lines[0], "Transit") {
		t.Errorf("first line = %q, want the phase without a transit", lines[0])
	}
	if !strings.HasPrefix(lines[1], "Next: ") {
		t.Errorf("second line = %q, want the next phases", lines[1])
	}
	// A date may be given without a place.
	dated := runLinesAt(t, reg, session, "moon 2026-06-21", spacewxClock)
	if len(dated) != 2 || !strings.HasPrefix(dated[0], "Moon: ") {
		t.Errorf("moon with a date only = %v, want the phase for that date", dated)
	}
}

// TestMoonCommandRejectsUnusableArguments asserts a bad location or date is
// answered with the usage rather than a wrong almanac.
func TestMoonCommandRejectsUnusableArguments(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLinesAt(t, reg, session, "moon nowhere in particular", spacewxClock)
	if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+moonUsage) {
		t.Errorf("moon with an unusable argument = %v, want the usage line", lines)
	}
}

// TestMoonCommandHandlesPolarLatitudes asserts a place where the Moon may not
// rise or set on a given day is reported honestly.
func TestMoonCommandHandlesPolarLatitudes(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLinesAt(t, reg, session, "moon 78.2232, 15.6469 2026-12-21", spacewxClock)
	if len(lines) != 3 {
		t.Fatalf("moon at Svalbard = %v, want three lines", lines)
	}
	if !strings.Contains(lines[1], "Moonrise: ") {
		t.Errorf("second line = %q, want the rise and set reported", lines[1])
	}
}

// TestMoonCommandLinesFitOneEnvelope asserts the three lines the command
// produces each fit one NOTICE, since a phase name split across two envelopes
// would be read in the wrong order.
func TestMoonCommandLinesFitOneEnvelope(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range runLinesAt(t, reg, session, "moon 37.7749, -122.4194 2026-09-15", spacewxClock) {
		fits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", line)
		if err != nil {
			t.Fatalf("noticeFits(%q): %v", line, err)
		}
		if !fits {
			t.Errorf("line %q does not fit one envelope", line)
		}
	}
}

// TestFormatMoonClockAndMoment asserts the two renderings the command prints.
func TestFormatMoonClockAndMoment(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 18, 21, 14, 0, 0, time.UTC)
	if got, want := FormatMoonClock(at), "21:14 UTC"; got != want {
		t.Errorf("FormatMoonClock = %q, want %q", got, want)
	}
	if got, want := FormatMoonClock(time.Time{}), "—"; got != want {
		t.Errorf("FormatMoonClock(zero) = %q, want %q", got, want)
	}
	if got, want := FormatMoonPhaseMoment(at), "Sep 18"; got != want {
		t.Errorf("FormatMoonPhaseMoment = %q, want %q", got, want)
	}
}

// TestDeltaTIsPlausible asserts the Terrestrial Time offset is the right order
// of magnitude for this century, since every event time depends on it.
func TestDeltaTIsPlausible(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		year float64
		min  float64
		max  float64
	}{
		{2026, 60, 90},
		{2050, 80, 180},
		{2100, 100, 300},
	} {
		got := deltaTSeconds(tc.year)
		if got < tc.min || got > tc.max {
			t.Errorf("deltaTSeconds(%v) = %.1f s, want between %v and %v", tc.year, got, tc.min, tc.max)
		}
	}
	// Delta T is never negative in the modern era and grows with the year.
	if deltaTSeconds(2100) <= deltaTSeconds(2026) {
		t.Error("Delta T should grow with the year")
	}
}

// TestMoonIsRegisteredAndDocumented asserts the command exists in the registry
// and carries help.
func TestMoonIsRegisteredAndDocumented(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	cmd, ok := reg.byName["moon"]
	if !ok {
		t.Fatal("moon is not registered")
	}
	if cmd.summary == "" || cmd.usage != moonUsage || len(cmd.detail) == 0 {
		t.Errorf("moon is not documented: %+v", cmd)
	}
	lines := runLines(t, reg, session, "help moon")
	if len(lines) != helpLineCount(cmd, defaultTestConfig()) {
		t.Errorf("help moon returned %v lines, want %v", len(lines), helpLineCount(cmd, defaultTestConfig()))
	}
}
