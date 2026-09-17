// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"math"
	"testing"
	"time"
)

// minutesOfDay renders an almanac time as minutes past the UTC midnight of the
// day the almanac is for. An event that falls on the neighbouring UTC day is
// therefore negative or beyond a day, which is exactly what the reference
// values record.
func minutesOfDay(midnight, event time.Time) float64 {
	return event.Sub(midnight).Minutes()
}

// TestSolarAlmanacMatchesReferenceGoldens asserts the almanac agrees with an
// independent implementation of the same published formulas, at locations from
// the equator to the arctic and across both solstices.
func TestSolarAlmanacMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lat     float64
		lng     float64
		date    string
		dawn    float64
		sunrise float64
		noon    float64
		sunset  float64
		dusk    float64
		// tolerance is how many minutes the two implementations may differ by.
		// Both are closed-form, so they agree to well inside a minute at
		// ordinary latitudes. Near a pole the sun's path is almost tangent to
		// the horizon and a tiny difference in the declination terms moves the
		// crossing by minutes, so those rows carry a wider margin.
		tolerance float64
	}{
		{"San Francisco equinox", 37.422, -122.0841, "2026-03-15", 832.7818, 859.2817, 1217.35, 1575.5589, 1602.1039, 2},
		{"London solstice", 51.5, -0.12, "2026-06-21", 174.7776, 223.4709, 722.1833, 1221.1329, 1269.8241, 2},
		{"Sydney solstice", -33.8568, 151.2153, "2026-12-21", -348.8219, -319.1017, 112.9667, 545.1283, 574.851, 2},
		{"Equator new year", 0, 0, "2026-01-01", 337.1225, 360.0208, 723.3167, 1087.1145, 1110.005, 2},
		{"Anchorage autumn", 61.2181, -149.9003, "2026-09-15", 882.4414, 927.3568, 1315.0, 1700.4839, 1745.1391, 6},
		{"Antarctic autumn", -77.85, 166.67, "2026-04-01", -336.5688, -234.8196, 57.3, 346.313, 447.1796, 6},
		{"Tokyo spring", 35.6762, 139.6503, "2026-05-10", -287.2414, -258.7588, 157.8333, 574.9193, 603.4838, 2},
		{"Nairobi winter", -1.2921, 36.8219, "2026-08-20", 192.5379, 214.1068, 576.2333, 938.1826, 959.7377, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			day := mustParseDay(t, tc.date)
			got := SolarAlmanac(tc.lat, tc.lng, day)
			events := []struct {
				name string
				at   time.Time
				want float64
			}{
				{"dawn", got.Dawn, tc.dawn},
				{"sunrise", got.Sunrise, tc.sunrise},
				{"noon", got.Noon, tc.noon},
				{"sunset", got.Sunset, tc.sunset},
				{"dusk", got.Dusk, tc.dusk},
			}
			for _, e := range events {
				if e.at.IsZero() {
					t.Errorf("%v does not occur, want %v minutes past midnight", e.name, e.want)
					continue
				}
				if diff := math.Abs(minutesOfDay(day, e.at) - e.want); diff > tc.tolerance {
					t.Errorf("%v = %.4f minutes past midnight, want %.4f (off by %.4f)",
						e.name, minutesOfDay(day, e.at), e.want, diff)
				}
			}
		})
	}
}

// TestSolarAlmanacPolarCases asserts the two cases a plain arithmetic answer
// gets wrong: a summer night that never reaches civil twilight, and a winter
// day on which the sun never rises.
func TestSolarAlmanacPolarCases(t *testing.T) {
	t.Parallel()

	reykjavik := SolarAlmanac(64.1466, -21.9426, mustParseDay(t, "2026-06-21"))
	if reykjavik.PolarNight || reykjavik.MidnightSun {
		t.Errorf("Reykjavik in June: PolarNight=%v MidnightSun=%v, want neither",
			reykjavik.PolarNight, reykjavik.MidnightSun)
	}
	if reykjavik.Sunrise.IsZero() || reykjavik.Sunset.IsZero() {
		t.Error("Reykjavik in June has a sunrise and a sunset")
	}
	if !reykjavik.TwilightAllNight {
		t.Error("Reykjavik in June never reaches civil twilight, so TwilightAllNight must be true")
	}
	if !reykjavik.Dawn.IsZero() || !reykjavik.Dusk.IsZero() {
		t.Error("Reykjavik in June has no separate dawn or dusk")
	}
	if got := reykjavik.DayLength(); got < 20*time.Hour || got > 22*time.Hour {
		t.Errorf("Reykjavik in June day length = %v, want between 20h and 22h", got)
	}

	svalbard := SolarAlmanac(78.2232, 15.6469, mustParseDay(t, "2026-12-21"))
	if !svalbard.PolarNight {
		t.Error("Svalbard in December is polar night")
	}
	if svalbard.MidnightSun {
		t.Error("Svalbard in December is not midnight sun")
	}
	if !svalbard.Sunrise.IsZero() || !svalbard.Sunset.IsZero() {
		t.Error("Svalbard in December has no sunrise or sunset")
	}
	if got := svalbard.DayLength(); got != 0 {
		t.Errorf("Svalbard in December day length = %v, want 0", got)
	}
	if svalbard.Noon.IsZero() {
		t.Error("solar noon is computable even in polar night")
	}
}

// TestSolarAlmanacDayLengthIsSunsetMinusSunrise asserts the day length is the
// interval between the two events, including when sunset falls on the next UTC
// day, which is the case for every location west of Greenwich.
func TestSolarAlmanacDayLengthIsSunsetMinusSunrise(t *testing.T) {
	t.Parallel()

	day := mustParseDay(t, "2026-03-15")
	got := SolarAlmanac(37.422, -122.0841, day)
	if got.Sunset.Before(got.Sunrise) {
		t.Fatalf("sunset %v is before sunrise %v", got.Sunset, got.Sunrise)
	}
	if want := got.Sunset.Sub(got.Sunrise); got.DayLength() != want {
		t.Errorf("DayLength = %v, want %v", got.DayLength(), want)
	}
	// The equinox is near enough to twelve hours everywhere.
	if hours := got.DayLength().Hours(); hours < 11.9 || hours > 12.2 {
		t.Errorf("equinox day length = %v hours, want near 12", hours)
	}
}

// TestMoonPhaseMatchesReferenceGoldens asserts every phase name is reached at
// the instant an independent implementation puts that phase at. The instants
// are the reference's own phase instants, captured by solving the Sun-Moon
// elongation, so a name that drifts out of its bin is caught.
func TestMoonPhaseMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		utJD         float64
		illumination string
	}{
		{"New Moon", 2461059.327654, "dark"},
		{"Waxing Crescent", 2461063.013539, "crescent"},
		{"First Quarter", 2461066.699424, "half"},
		{"Waxing Gibbous", 2461070.061266, "gibbous"},
		{"Full Moon", 2461073.423108, "lit"},
		{"Waning Gibbous", 2461077.226496, "gibbous"},
		{"Last Quarter", 2461081.029884, "half"},
		{"Waning Crescent", 2461085.015331, "crescent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MoonPhaseAt(julianToTime(tc.utJD))
			if got.Name != tc.name {
				t.Errorf("at the reference %v instant the phase reads as %q", tc.name, got.Name)
			}
			if got.Index != indexOfPhase(t, tc.name) {
				t.Errorf("at the reference %v instant the index is %v", tc.name, got.Index)
			}
			// The illumination has to match the phase the name claims.
			switch tc.illumination {
			case "dark":
				if got.Illumination > 5 {
					t.Errorf("new moon illumination = %.0f%%", got.Illumination)
				}
			case "crescent":
				if got.Illumination <= 0 || got.Illumination >= 50 {
					t.Errorf("crescent illumination = %.0f%%, want between 0 and 50", got.Illumination)
				}
			case "half":
				if !closeWithin(got.Illumination, 50, 3) {
					t.Errorf("quarter illumination = %.0f%%, want about 50", got.Illumination)
				}
			case "gibbous":
				if got.Illumination <= 50 || got.Illumination >= 100 {
					t.Errorf("gibbous illumination = %.0f%%, want between 50 and 100", got.Illumination)
				}
			case "lit":
				if got.Illumination < 95 {
					t.Errorf("full moon illumination = %.0f%%", got.Illumination)
				}
			}
		})
	}
}

// indexOfPhase returns the position of a phase name in the table.
func indexOfPhase(t *testing.T, name string) int {
	t.Helper()
	for i, candidate := range MoonPhaseNames {
		if candidate == name {
			return i
		}
	}
	t.Fatalf("unknown phase name %q", name)
	return -1
}

// TestMoonPhaseIllumination asserts the illumination follows the age: new moon
// is dark, full moon is lit, and the fraction rises and falls through the
// month without ever contradicting the named phase.
func TestMoonPhaseIllumination(t *testing.T) {
	t.Parallel()

	newMoon := MoonPhaseAt(mustParseDay(t, "2026-01-18").Add(12 * time.Hour))
	if newMoon.Illumination > 5 {
		t.Errorf("new moon illumination = %v%%, want under 5%%", newMoon.Illumination)
	}
	fullMoon := MoonPhaseAt(mustParseDay(t, "2026-01-02").Add(12 * time.Hour))
	if fullMoon.Illumination < 95 {
		t.Errorf("full moon illumination = %v%%, want over 95%%", fullMoon.Illumination)
	}

	// Sample one lunation from a new moon and assert the illumination rises to
	// the full moon and falls away again.
	start := mustParseDay(t, "2026-01-18")
	var waxing, waning []float64
	for step := range 8 {
		at := start.Add(time.Duration(float64(step)*synodicMonth/8*24) * time.Hour)
		illumination := MoonPhaseAt(at).Illumination
		if step <= 4 {
			waxing = append(waxing, illumination)
		} else {
			waning = append(waning, illumination)
		}
	}
	for i := 1; i < len(waxing); i++ {
		if waxing[i] < waxing[i-1] {
			t.Errorf("illumination fell while waxing: %v", waxing)
			break
		}
	}
	for i := 1; i < len(waning); i++ {
		if waning[i] > waning[i-1] {
			t.Errorf("illumination rose while waning: %v", waning)
			break
		}
	}
}

// TestMoonPhaseIsConsistentWithItself asserts the illumination and the index
// are derived from the same age, so the name and the percentage can never
// disagree.
func TestMoonPhaseIsConsistentWithItself(t *testing.T) {
	t.Parallel()

	for _, at := range []time.Time{
		mustParseDay(t, "2026-01-18"), mustParseDay(t, "2026-04-15"),
		mustParseDay(t, "2025-11-20"), mustParseDay(t, "2024-02-29"),
	} {
		got := MoonPhaseAt(at)
		wantIllumination := (1 - math.Cos(2*math.Pi*got.Age/synodicMonth)) / 2 * 100
		if !closeWithin(got.Illumination, wantIllumination, 1e-9) {
			t.Errorf("illumination %v does not match age %v (want %v)",
				got.Illumination, got.Age, wantIllumination)
		}
		if got.Age < 0 || got.Age >= synodicMonth {
			t.Errorf("age %v is outside one synodic month", got.Age)
		}
		if got.Index < 0 || got.Index >= lunarPhaseCount {
			t.Errorf("phase index %v is outside the table", got.Index)
		}
		if got.Name != MoonPhaseNames[got.Index] {
			t.Errorf("name %q does not match index %v", got.Name, got.Index)
		}
	}
}

// TestParseAlmanacDayAcceptsFieldDates asserts the date shapes a person is
// likely to type, and that nonsense is refused.
func TestParseAlmanacDayAcceptsFieldDates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 15, 18, 30, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want string
	}{
		{"", "2026-03-15"},
		{"today", "2026-03-15"},
		{"TOMORROW", "2026-03-16"},
		{"yesterday", "2026-03-14"},
		{"2026-06-21", "2026-06-21"},
		{"2026/06/21", "2026-06-21"},
		{"21 Jun 2026", "2026-06-21"},
		{"Jun 21 2026", "2026-06-21"},
		{"June 21, 2026", "2026-06-21"},
	}
	for _, tc := range tests {
		got, err := ParseAlmanacDay(tc.in, now)
		if err != nil {
			t.Errorf("ParseAlmanacDay(%q): %v", tc.in, err)
			continue
		}
		if want := tc.want; got.Format("2006-01-02") != want {
			t.Errorf("ParseAlmanacDay(%q) = %v, want %v", tc.in, got.Format("2006-01-02"), want)
		}
	}
	for _, in := range []string{"not a date", "2026-13-45", "32 Jan 2026"} {
		if got, err := ParseAlmanacDay(in, now); err == nil {
			t.Errorf("ParseAlmanacDay(%q) = %v, want an error", in, got)
		}
	}
}

// TestFormatUTCClockAndDayLength asserts the two renderings the sun command
// prints.
func TestFormatUTCClockAndDayLength(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 3, 15, 6, 18, 0, 0, time.UTC)
	if got, want := FormatUTCClock(at), "06:18"; got != want {
		t.Errorf("FormatUTCClock = %q, want %q", got, want)
	}
	if got, want := FormatUTCClock(time.Time{}), "—"; got != want {
		t.Errorf("FormatUTCClock(zero) = %q, want %q", got, want)
	}
	tests := []struct {
		in   time.Duration
		want string
	}{
		{12*time.Hour + 37*time.Minute, "12h37m"},
		{0, "0h00m"},
		{59 * time.Minute, "0h59m"},
		{24 * time.Hour, "24h00m"},
	}
	for _, tc := range tests {
		if got := FormatDayLength(tc.in); got != tc.want {
			t.Errorf("FormatDayLength(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// mustParseDay parses a golden date or fails the test.
func mustParseDay(t *testing.T, date string) time.Time {
	t.Helper()
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("bad golden date %q: %v", date, err)
	}
	return utcMidnight(day)
}
