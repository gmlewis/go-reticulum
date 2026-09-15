// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
	"time"
)

// buoyFixture is a real National Data Buoy Center feed, trimmed to a few
// observations. The units are the feed's own: wind in meters per second, wave
// height in meters, temperatures in Celsius, pressure in hectopascals.
const buoyFixture = `#YY  MM DD hh mm WDIR WSPD GST  WVHT   DPD   APD MWD   PRES  ATMP  WTMP  DEWP  VIS PTDY  TIDE
#yr  mo dy hr mn degT m/s  m/s     m   sec   sec degT   hPa  degC  degC  degC  nmi  hPa    ft
2026 09 15 21 50 310  6.0  8.0   1.9    14   7.2 290 1016.2    MM  12.3    MM   MM   MM    MM
2026 09 15 21 40 310  5.5  7.5   1.8    13   7.0 290 1016.0    MM  12.3    MM   MM   MM    MM
2026 09 15 21 30 310  5.0  7.0   1.8    12   6.8 285 1015.6    MM  12.4    MM   MM   MM    MM
2026 09 15 21 20 300  5.0  6.0   1.7    12   6.6 285 1015.4    MM  12.4    MM   MM   MM    MM
2026 09 15 17 50 290  4.0  6.0   1.5    12   6.5 285 1015.0    MM  12.4    MM   MM   MM    MM
`

// buoyConfig is a configuration with the documented provider template.
func buoyConfig() *BotConfig {
	cfg := defaultTestConfig()
	cfg.BuoyURL = "https://www.ndbc.noaa.gov/data/realtime2/{place}.txt"
	return cfg
}

// TestParseBuoyRealtimeReadsTheFeed asserts the fixed-column feed decodes into
// the newest observation with every unit converted.
func TestParseBuoyRealtimeReadsTheFeed(t *testing.T) {
	t.Parallel()

	report, err := ParseBuoyRealtime("46026", buoyFixture)
	if err != nil {
		t.Fatalf("ParseBuoyRealtime: %v", err)
	}
	if report.Rows != 5 {
		t.Errorf("read %v observations, want 5", report.Rows)
	}
	got := report.Observation
	if want := time.Date(2026, 9, 15, 21, 50, 0, 0, time.UTC); !got.At.Equal(want) {
		t.Errorf("newest observation at %v, want %v", got.At, want)
	}
	// 6.0 m/s is 11.66 knots, which reads as 12.
	if !got.HasWind || !closeWithin(got.WindSpeedKt, 11.663, 0.01) {
		t.Errorf("wind speed = %v kt, want 11.66", got.WindSpeedKt)
	}
	if !got.HasGust || !closeWithin(got.WindGustKt, 15.551, 0.01) {
		t.Errorf("gust = %v kt, want 15.55", got.WindGustKt)
	}
	if !got.HasWindDirection || got.WindDirectionDeg != 310 {
		t.Errorf("wind direction = %v, want 310", got.WindDirectionDeg)
	}
	// 1.9 m is 6.23 feet.
	if !got.HasWave || !closeWithin(got.WaveHeightFt, 6.234, 0.01) || !closeWithin(got.WaveHeightM, 1.9, 1e-9) {
		t.Errorf("wave height = %v ft (%v m), want 6.23 ft", got.WaveHeightFt, got.WaveHeightM)
	}
	if !got.HasPeriod || got.DominantPeriodSec != 14 {
		t.Errorf("dominant period = %v, want 14", got.DominantPeriodSec)
	}
	if !got.HasPressure || !closeWithin(got.PressureHPa, 1016.2, 1e-9) {
		t.Errorf("pressure = %v, want 1016.2", got.PressureHPa)
	}
	if !got.HasWaterTemp || !closeWithin(got.WaterTempF, 54.14, 0.01) || !closeWithin(got.WaterTempC, 12.3, 1e-9) {
		t.Errorf("water temperature = %v F (%v C), want 54.14", got.WaterTempF, got.WaterTempC)
	}
	if got.HasAirTemp {
		t.Errorf("air temperature is reported as present, but the feed says MM")
	}
	// The oldest row is what the trend is measured against.
	if report.Oldest == nil || !report.Oldest.At.Equal(time.Date(2026, 9, 15, 17, 50, 0, 0, time.UTC)) {
		t.Errorf("oldest observation = %+v, want the 17:50 row", report.Oldest)
	}
}

// TestParseBuoyRealtimeHandlesMissingSensors asserts a sensor publishing "MM"
// is reported as absent rather than as a zero: a wave height of zero and no
// wave height at all are very different sea states.
func TestParseBuoyRealtimeHandlesMissingSensors(t *testing.T) {
	t.Parallel()

	feed := `#YY  MM DD hh mm WDIR WSPD GST  WVHT   DPD   APD MWD   PRES  ATMP  WTMP  DEWP  VIS PTDY  TIDE
2026 09 15 22 00  MM   MM   MM    MM    MM    MM  MM 1012.8    MM  15.9    MM   MM -0.6    MM
`
	report, err := ParseBuoyRealtime("46026", feed)
	if err != nil {
		t.Fatalf("ParseBuoyRealtime: %v", err)
	}
	got := report.Observation
	if got.HasWind || got.HasGust || got.HasWindDirection {
		t.Errorf("wind reported as present from a row of MM values: %+v", got)
	}
	if got.HasWave || got.HasPeriod || got.HasWaveDirection {
		t.Errorf("waves reported as present from a row of MM values: %+v", got)
	}
	if !got.HasPressure || !got.HasWaterTemp {
		t.Errorf("the sensors that did report are missing: %+v", got)
	}
	lines := renderBuoyAnswer(report, got.At)
	if !strings.Contains(lines[0], "Wave: no data") || !strings.Contains(lines[0], "Wind: no data") {
		t.Errorf("sea line = %q, want the missing sensors named", lines[0])
	}
}

// TestParseBuoyRealtimeRejectsUnusableFeeds asserts a feed that carries no
// observation is refused rather than reported as calm water.
func TestParseBuoyRealtimeRejectsUnusableFeeds(t *testing.T) {
	t.Parallel()

	for _, feed := range []string{
		"",
		"not a feed at all",
		"#YY  MM DD hh mm WDIR WSPD GST  WVHT   DPD   APD MWD   PRES  ATMP  WTMP  DEWP  VIS PTDY  TIDE\n",
		"#YY  MM DD\n2026 09 15\n",
	} {
		if report, err := ParseBuoyRealtime("46026", feed); err == nil {
			t.Errorf("ParseBuoyRealtime(%q) = %+v, want an error", feed, report)
		}
	}
}

// TestBuoySwellRatingNamesTheSea asserts the period, not the height, decides
// whether a sea is groundswell or chop.
func TestBuoySwellRatingNamesTheSea(t *testing.T) {
	t.Parallel()

	tests := []struct {
		period float64
		want   string
	}{
		{4, "choppy wind swell"},
		{5.9, "choppy wind swell"},
		{6, "wind swell"},
		{9.9, "wind swell"},
		{10, "groundswell"},
		{13.9, "groundswell"},
		{14, "heavy groundswell"},
		{18, "heavy groundswell"},
	}
	for _, tc := range tests {
		if got := BuoySwellRating(tc.period); got != tc.want {
			t.Errorf("BuoySwellRating(%v) = %q, want %q", tc.period, got, tc.want)
		}
	}
}

// TestBuoyPressureTrendReportsTheChange asserts the trend across the feed is
// named, with its size and span, and that a feed too short in time says nothing
// rather than guessing.
func TestBuoyPressureTrendReportsTheChange(t *testing.T) {
	t.Parallel()

	report, err := ParseBuoyRealtime("46026", buoyFixture)
	if err != nil {
		t.Fatalf("ParseBuoyRealtime: %v", err)
	}
	if got := BuoyPressureTrend(report); got != "rising, +1.2 hPa over 4h" {
		t.Errorf("trend = %q, want a rise of 1.2 hPa over four hours", got)
	}

	// A single row has no trend to report.
	single := `#YY  MM DD hh mm WDIR WSPD GST  WVHT   DPD   APD MWD   PRES  ATMP  WTMP  DEWP  VIS PTDY  TIDE
2026 09 15 21 50 310  6.0  8.0   1.9    14   7.2 290 1016.2    MM  12.3    MM   MM   MM    MM
`
	one, err := ParseBuoyRealtime("46026", single)
	if err != nil {
		t.Fatalf("ParseBuoyRealtime: %v", err)
	}
	if got := BuoyPressureTrend(one); got != "" {
		t.Errorf("a single observation reported the trend %q", got)
	}

	// A change under half a hectopascal reads as steady.
	steady := strings.Replace(buoyFixture, "1015.0", "1016.0", 1)
	flat, err := ParseBuoyRealtime("46026", steady)
	if err != nil {
		t.Fatalf("ParseBuoyRealtime: %v", err)
	}
	if got := BuoyPressureTrend(flat); got != "steady" {
		t.Errorf("trend = %q, want steady", got)
	}
}

// TestBuoyCommandRendersTheSeaState asserts the two-line answer matches the
// documented shape, with every unit converted for a mariner.
func TestBuoyCommandRendersTheSeaState(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, buoyConfig())
	reg.fetch = func(url string) (string, error) {
		if !strings.HasSuffix(url, "/46026.txt") {
			t.Errorf("the provider was asked for %q, want the lowercased station", url)
		}
		return buoyFixture, nil
	}
	now := time.Date(2026, 9, 15, 22, 18, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "buoy 46026", now)
	if len(lines) != 2 {
		t.Fatalf("buoy = %v, want two lines", lines)
	}
	want := "Buoy 46026 (28m ago): Wave 6.2 ft @ 14s WNW (heavy groundswell) | Wind 12 kt G 16 kt NW"
	if lines[0] != want {
		t.Errorf("sea line =\n  %v\nwant\n  %v", lines[0], want)
	}
	if !strings.HasPrefix(lines[1], "Water Temp: 54.1°F (12.3°C) | Pressure: 1016.2 hPa (rising, +1.2 hPa over 4h)") {
		t.Errorf("conditions line = %q, want the temperature, the pressure, and its trend", lines[1])
	}
}

// TestBuoyCommandReportsItsConfiguration asserts the command explains rather
// than guesses when it cannot answer.
func TestBuoyCommandReportsItsConfiguration(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	if lines := runLinesAt(t, reg, session, "buoy 46026", spacewxClock); len(lines) != 1 ||
		lines[0] != buoyNotConfiguredLine {
		t.Errorf("buoy with no provider = %v, want %q", lines, buoyNotConfiguredLine)
	}

	reg, session, _ = commandFixture(t, buoyConfig())
	for _, line := range []string{"buoy", "buoy 12", "buoy 46026/../etc", "buoy 4602612"} {
		lines := runLinesAt(t, reg, session, line, spacewxClock)
		if len(lines) != 1 || lines[0] != buoyStationRejectedLine {
			t.Errorf("%q = %v, want the station line", line, lines)
		}
	}

	cfg := buoyConfig()
	cfg.BuoyURL = "file:///etc/passwd/{place}"
	reg, session, _ = commandFixture(t, cfg)
	if lines := runLinesAt(t, reg, session, "buoy 46026", spacewxClock); len(lines) != 1 ||
		lines[0] != buoyMisconfiguredLine {
		t.Errorf("buoy with a bad template = %v, want %q", lines, buoyMisconfiguredLine)
	}

	reg, session, _ = commandFixture(t, buoyConfig())
	reg.fetch = func(url string) (string, error) { return "no feed here", nil }
	if lines := runLinesAt(t, reg, session, "buoy 46026", spacewxClock); len(lines) != 1 ||
		lines[0] != buoyFailedLine {
		t.Errorf("buoy with a feed that does not decode = %v, want %q", lines, buoyFailedLine)
	}
}

// TestBuoyCommandLinesFitOneEnvelope asserts both lines fit one NOTICE, since a
// sea state split across envelopes could be read as two different seas.
func TestBuoyCommandLinesFitOneEnvelope(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, buoyConfig())
	reg.fetch = func(url string) (string, error) { return buoyFixture, nil }
	for _, line := range runLinesAt(t, reg, session, "buoy 46026",
		time.Date(2026, 9, 15, 22, 18, 0, 0, time.UTC)) {
		fits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", line)
		if err != nil {
			t.Fatalf("noticeFits(%q): %v", line, err)
		}
		if !fits {
			t.Errorf("line %q does not fit one envelope", line)
		}
	}
}
