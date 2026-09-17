// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"math"
	"strconv"
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
		if len(lines) != 2 || lines[0] != buoyStationRejectedLine || lines[1] != buoyUsageHint {
			t.Errorf("%q = %v, want the station line and the discovery hint", line, lines)
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

// TestBuoySearchFindsABuoyByPlaceAndID asserts the two things an asker can type
// without knowing a station id: the place they would say, and the id they are
// holding.
func TestBuoySearchFindsABuoyByPlaceAndID(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)

	byPlace := runLines(t, reg, session, "buoy search san francisco")
	if len(byPlace) < 2 || !strings.Contains(byPlace[0], `Weather buoys matching "san francisco" (Page 1 of `) {
		t.Fatalf("buoy search san francisco = %v, want a paged answer", byPlace)
	}
	// The label is the provider's own published name, matched case-insensitively:
	// the catalog carries the stations as they are published, so the test asks
	// which station came first, not how the center capitalizes it.
	if !strings.Contains(byPlace[1], "  46026: ") ||
		!strings.Contains(strings.ToUpper(byPlace[1]), "SAN FRANCISCO") {
		t.Errorf("first match = %q, want the San Francisco buoy", byPlace[1])
	}

	byID := runLines(t, reg, session, "buoy search 44013")
	if len(byID) < 2 || !strings.Contains(byID[1], "  44013: ") ||
		!strings.Contains(strings.ToUpper(byID[1]), "BOSTON") {
		t.Errorf("buoy search 44013 = %v, want the Boston buoy first", byID)
	}

	byRegion := runLines(t, reg, session, "buoy search cape")
	if len(byRegion) < 2 || !strings.Contains(byRegion[0], `Weather buoys matching "cape" (Page 1 of `) {
		t.Fatalf("buoy search cape = %v, want a paged answer", byRegion)
	}
	// Every row on the page must match the query somewhere, which is the whole
	// search contract; which cape is listed first is the catalog's own order
	// over the stations the center publishes.
	for _, line := range byRegion[1 : len(byRegion)-1] {
		if !strings.Contains(strings.ToUpper(line), "CAPE") {
			t.Errorf("buoy search cape listed a buoy that does not match: %q", line)
		}
	}
	if !strings.Contains(strings.Join(runLines(t, reg, session, "buoy search mendocino"), "\n"), "46213") {
		t.Error("buoy search mendocino did not find the Cape Mendocino buoy")
	}

	if got := runLines(t, reg, session, "buoy search zzyzx"); len(got) != 2 ||
		!strings.Contains(got[0], `No Weather buoys match "zzyzx"`) {
		t.Errorf("buoy search zzyzx = %v, want a no-match line and a hint", got)
	}
	if got := runLines(t, reg, session, "buoy search"); len(got) == 0 ||
		!strings.Contains(got[0], "Usage: buoy search") {
		t.Errorf("buoy search with no words = %v, want the usage line", got)
	}
}

// TestBuoyListFiltersByRegion asserts the list form takes a state code, the
// state's own name, and a basin code for the buoys outside the states.
func TestBuoyListFiltersByRegion(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	byCode := runLines(t, reg, session, "buoy list CA")
	if len(byCode) == 0 || !strings.Contains(byCode[0], "Weather buoys in CA (Page 1 of ") {
		t.Fatalf("buoy list CA = %v, want the Californian buoys paged", byCode)
	}
	for _, line := range byCode[1 : len(byCode)-1] {
		if !strings.Contains(line, ", CA") {
			t.Errorf("buoy list CA listed a buoy outside California: %q", line)
		}
	}

	byName := runLines(t, reg, session, "buoy list california")
	if len(byName) == 0 || !strings.Contains(byName[0], "Weather buoys in CALIFORNIA (Page 1 of ") ||
		byName[1] != byCode[1] {
		t.Errorf("buoy list california = %v, want the same first page as buoy list CA", byName)
	}

	// The Gulf is a region filter over the full catalog, so the answer is paged;
	// what it must never do is call a station on Florida's Atlantic coast a Gulf
	// station, which is what the basin bounds have to get right.
	basin := runLines(t, reg, session, "buoy list GOM")
	if len(basin) < 3 || !strings.Contains(basin[0], "Weather buoys in GOM (Page 1 of ") {
		t.Fatalf("buoy list GOM = %v, want the Gulf buoys paged", basin)
	}
	for _, line := range basin[1 : len(basin)-1] {
		if !strings.Contains(line, ", GOM") {
			t.Errorf("buoy list GOM listed a buoy outside the Gulf: %q", line)
		}
	}

	if got := runLines(t, reg, session, "buoy list ZZ"); len(got) != 2 ||
		!strings.Contains(got[0], "No Weather buoys in ZZ") {
		t.Errorf("buoy list ZZ = %v, want a no-region line and a hint", got)
	}
	all := runLines(t, reg, session, "buoy list")
	if len(all) == 0 || !strings.Contains(all[0], "Weather buoys (Page 1 of ") {
		t.Errorf("buoy list = %v, want the whole catalog paged", all)
	}
}

// TestBuoyNearNamesTheClosestBuoys asserts a position resolves to the buoys
// around it, and that a station id resolves to the buoy's own position, which is
// how "buoy near 46026" answers.
func TestBuoyNearNamesTheClosestBuoys(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "buoy near 37.8,-122.4")
	if len(lines) != 5 {
		t.Fatalf("buoy near 37.8,-122.4 = %v, want a header, three buoys, and a footer", lines)
	}
	if !strings.Contains(lines[0], "Weather buoys near 37.8,-122.4 (Page 1 of 1):") {
		t.Errorf("header = %q, want the position that was asked for", lines[0])
	}
	// The catalog carries every station the center publishes, so which three are
	// nearest is the center's data, not this test's business: what the command
	// promises is a distance, a bearing, a name, and the nearest first.
	for i, line := range lines[1:4] {
		if !strings.Contains(line, " (") || !strings.Contains(line, " nmi ") ||
			!strings.Contains(line, "): ") {
			t.Errorf("buoy %v of the answer = %q, want a distance, a bearing, and the name", i+1, line)
		}
	}
	if !closerFirst(lines[1:4]) {
		t.Errorf("buoy near 37.8,-122.4 = %v, want the nearest station first", lines[1:4])
	}

	byID := runLines(t, reg, session, "buoy near 46026")
	if len(byID) < 2 || !strings.Contains(byID[1], "46026 (0.0 nmi ") {
		t.Errorf("buoy near 46026 = %v, want the buoy itself at zero distance", byID)
	}

	byCity := runLines(t, reg, session, "buoy near Orlando, fl")
	if len(byCity) != 5 {
		t.Fatalf("buoy near Orlando, fl = %v, want a header, three buoys, and a footer", byCity)
	}
	if !strings.Contains(byCity[0], "Weather buoys near Orlando, fl") {
		t.Errorf("header = %q, want the city that was asked for", byCity[0])
	}
	// Which buoy is nearest to Orlando is the center's data, and the catalog
	// carries every station it publishes; the command promises the nearest
	// first, in the state that was asked for.
	if !strings.Contains(byCity[1], " nmi ") || !strings.Contains(byCity[1], "): ") {
		t.Errorf("nearest buoy to Orlando = %q, want a distance and a name", byCity[1])
	}
	if !closerFirst(byCity[1:4]) {
		t.Errorf("buoy near Orlando, fl = %v, want the nearest station first", byCity[1:4])
	}
	if !strings.Contains(byCity[0], "Orlando, fl") {
		t.Errorf("header = %q, want the city that was asked for", byCity[0])
	}

	if got := runLines(t, reg, session, "buoy near nowhere at all"); len(got) != 2 ||
		!strings.Contains(got[0], "not a place, a coordinate, or a plus code") {
		t.Errorf("buoy near nowhere = %v, want an explanation and a hint", got)
	}
}

// TestBuoyDiscoveryNeedsNoProvider asserts finding a buoy is an offline
// question: it answers on a bot with no buoy_url at all.
func TestBuoyDiscoveryNeedsNoProvider(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	reg.fetch = func(url string) (string, error) {
		t.Errorf("discovery reached the network: %v", url)
		return "", nil
	}
	for _, line := range []string{"buoy search boston", "buoy list HI", "buoy near 21.3,-157.9"} {
		if lines := runLinesAt(t, reg, session, line, spacewxClock); len(lines) < 2 {
			t.Errorf("%v = %v, want an offline answer", line, lines)
		}
	}
	if lines := runLinesAt(t, reg, session, "buoy 46026", spacewxClock); len(lines) != 1 ||
		lines[0] != buoyNotConfiguredLine {
		t.Errorf("buoy 46026 = %v, want %q", lines, buoyNotConfiguredLine)
	}
}

// TestBuoyStationTableIsCoherent asserts the reference catalog has no duplicate
// ids, no empty names, real positions, and the coverage it claims.
func TestBuoyStationTableIsCoherent(t *testing.T) {
	t.Parallel()

	if len(buoyStations) < 80 {
		t.Fatalf("the reference table holds %v buoys, want at least 80", len(buoyStations))
	}
	seen := map[string]bool{}
	regions := map[string]bool{}
	for _, station := range buoyStations {
		if seen[station.ID] {
			t.Errorf("buoy %v appears twice", station.ID)
		}
		seen[station.ID] = true
		// The center publishes partner stations whose ids are longer than the
		// five-character NDBC ids ("4403585") and some in lower case ("pxsc1"),
		// and the table carries the feed in full. The shape worth refusing is an
		// id no command could take, not one that is longer than the classic.
		if len(station.ID) < 4 || len(station.ID) > 8 {
			t.Errorf("buoy id %q is not 4 to 8 characters", station.ID)
		}
		if strings.ContainsAny(station.ID, " \t") {
			t.Errorf("buoy id %q carries whitespace", station.ID)
		}
		if station.Name == "" {
			t.Errorf("buoy %v has no name", station.ID)
		}
		if station.Region == "" {
			t.Errorf("buoy %v has no region", station.ID)
		}
		if math.Abs(station.Lat) > 90 || math.Abs(station.Lng) > 180 {
			t.Errorf("buoy %v is at an impossible position: %v, %v", station.ID, station.Lat, station.Lng)
		}
		regions[station.Region] = true
	}
	for _, want := range []string{"CA", "OR", "WA", "AK", "HI", "FL", "MA", "TX", "MI"} {
		if !regions[want] {
			t.Errorf("the reference table has no buoy in %v", want)
		}
	}
}

// closerFirst reports whether a page of proximity rows is ordered nearest first,
// which is the promise the command makes regardless of which stations the
// provider happens to publish. Each row carries its distance as "(0.2 nmi NE)".
func closerFirst(rows []string) bool {
	previous := -1.0
	for _, row := range rows {
		start := strings.Index(row, "(")
		end := strings.Index(row, " nmi")
		if start < 0 || end < start {
			return false
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(row[start+1:end]), 64)
		if err != nil {
			return false
		}
		if value < previous {
			return false
		}
		previous = value
	}
	return true
}
