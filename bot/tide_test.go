// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// tideFixture is a real provider answer, captured from the National Ocean
// Service's high/low predictions product for San Francisco (9414290) on
// 2026-09-15. Every expectation below is read off it.
const tideFixture = `{ "predictions" : [
{"t":"2026-09-15 03:34", "v":"0.563", "type":"L"},{"t":"2026-09-15 10:19", "v":"4.548", "type":"H"},` +
	`{"t":"2026-09-15 15:17", "v":"2.462", "type":"L"},{"t":"2026-09-15 21:40", "v":"5.767", "type":"H"},` +
	`{"t":"2026-09-16 04:23", "v":"0.636", "type":"L"},{"t":"2026-09-16 11:26", "v":"4.241", "type":"H"},` +
	`{"t":"2026-09-16 15:59", "v":"3.005", "type":"L"},{"t":"2026-09-16 22:16", "v":"5.588", "type":"H"}
]}`

// tideConfig is a configuration with the documented provider template.
func tideConfig() *BotConfig {
	cfg := defaultTestConfig()
	cfg.TideURL = "https://api.tidesandcurrents.noaa.gov/api/prod/datagetter?product=predictions" +
		"&datum=MLLW&time_zone=gmt&units=english&interval=hilo&format=json&station={place}" +
		"&begin_date={date}&range=48"
	return cfg
}

// TestParseTidePredictionsReadsTheProviderAnswer asserts the fixed provider
// format decodes into the events it names, in time order.
func TestParseTidePredictionsReadsTheProviderAnswer(t *testing.T) {
	t.Parallel()

	events, err := parseTidePredictions(tideFixture)
	if err != nil {
		t.Fatalf("parseTidePredictions: %v", err)
	}
	if len(events) != 8 {
		t.Fatalf("decoded %v events, want 8", len(events))
	}
	first := events[0]
	if first.High {
		t.Error("the first event is a low water")
	}
	if !closeWithin(first.Height, 0.563, 1e-9) {
		t.Errorf("first height = %v, want 0.563", first.Height)
	}
	if want := time.Date(2026, 9, 15, 3, 34, 0, 0, time.UTC); !first.At.Equal(want) {
		t.Errorf("first event at %v, want %v", first.At, want)
	}
	if got := events[1]; !got.High || !got.At.Equal(time.Date(2026, 9, 15, 10, 19, 0, 0, time.UTC)) {
		t.Errorf("second event = %+v, want the 10:19 high water", got)
	}
	for i := 1; i < len(events); i++ {
		if events[i].At.Before(events[i-1].At) {
			t.Fatalf("events are not in time order: %+v", events)
		}
	}
}

// TestParseTidePredictionsRejectsUnusableAnswers asserts the provider's own
// error form, and anything else that carries no predictions, is refused rather
// than reported as a day with no tide.
func TestParseTidePredictionsRejectsUnusableAnswers(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"",
		"not json",
		`{"predictions":[]}`,
		`{"error":{"message":"No Predictions data was found. Please make sure the Datum input is valid."}}`,
		`{"predictions":[{"t":"not a time","v":"1.0","type":"H"}]}`,
		`{"predictions":[{"t":"2026-09-15 03:34","v":"MM","type":"L"}]}`,
	} {
		if events, err := parseTidePredictions(body); err == nil {
			t.Errorf("parseTidePredictions(%q) = %v, want an error", body, events)
		}
	}
}

// TestTideInterpolateFollowsTheRuleOfTwelfths asserts the classic rule: a
// half-cycle of six hours moves 1, 2, 3, 3, 2, 1 twelfths of the range.
func TestTideInterpolateFollowsTheRuleOfTwelfths(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	previous := TideEvent{At: start, Height: 0}
	next := TideEvent{At: start.Add(6 * time.Hour), Height: 12, High: true}

	tests := []struct {
		hours      float64
		wantHeight float64
		wantPct    float64
	}{
		{0, 0, 0},
		{1, 1, 100.0 / 12},
		{2, 3, 300.0 / 12},
		{3, 6, 600.0 / 12},
		{4, 9, 900.0 / 12},
		{5, 11, 1100.0 / 12},
		{6, 12, 100},
	}
	for _, tc := range tests {
		at := start.Add(time.Duration(tc.hours * float64(time.Hour)))
		height, percent := tideInterpolate(previous, next, at)
		if !closeWithin(height, tc.wantHeight, 1e-9) {
			t.Errorf("after %v hours: height = %v, want %v", tc.hours, height, tc.wantHeight)
		}
		if !closeWithin(percent, tc.wantPct, 1e-9) {
			t.Errorf("after %v hours: percent = %v, want %v", tc.hours, percent, tc.wantPct)
		}
	}

	// A half-cycle that is not exactly six hours still runs from one event to
	// the other: the rule is scaled to the interval.
	short := TideEvent{At: start.Add(3 * time.Hour), Height: 12, High: true}
	height, percent := tideInterpolate(previous, short, start.Add(90*time.Minute))
	if !closeWithin(height, 6, 1e-9) || !closeWithin(percent, 50, 1e-9) {
		t.Errorf("half way through a short cycle: height %v percent %v, want 6 and 50", height, percent)
	}

	// An event pair that does not advance cannot divide by anything.
	height, percent = tideInterpolate(previous, previous, start)
	if height != previous.Height || percent != 0 {
		t.Errorf("a zero-length cycle gave height %v percent %v", height, percent)
	}
}

// TestTideStateAtReportsFloodAndEbb asserts the direction of the tide and the
// event it is heading for, which is what a crossing decision turns on.
func TestTideStateAtReportsFloodAndEbb(t *testing.T) {
	t.Parallel()

	events, err := parseTidePredictions(tideFixture)
	if err != nil {
		t.Fatalf("parseTidePredictions: %v", err)
	}
	tests := []struct {
		name     string
		at       time.Time
		flooding bool
		nextAt   time.Time
	}{
		{"rising after the early low", time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC), true,
			time.Date(2026, 9, 15, 10, 19, 0, 0, time.UTC)},
		{"falling after the morning high", time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), false,
			time.Date(2026, 9, 15, 15, 17, 0, 0, time.UTC)},
		{"rising into the evening high", time.Date(2026, 9, 15, 18, 0, 0, 0, time.UTC), true,
			time.Date(2026, 9, 15, 21, 40, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state, ok := tideStateAt(events, tc.at)
			if !ok {
				t.Fatalf("no state at %v", tc.at)
			}
			if state.Flooding != tc.flooding {
				t.Errorf("flooding = %v, want %v", state.Flooding, tc.flooding)
			}
			if !state.Next.At.Equal(tc.nextAt) {
				t.Errorf("next event at %v, want %v", state.Next.At, tc.nextAt)
			}
			if state.PercentToNext <= 0 || state.PercentToNext >= 100 {
				t.Errorf("percent to the next event = %v, want between 0 and 100", state.PercentToNext)
			}
			// The estimate always lies between the two bracketing events.
			low, high := state.Previous.Height, state.Next.Height
			if low > high {
				low, high = high, low
			}
			if state.Height < low-1e-9 || state.Height > high+1e-9 {
				t.Errorf("estimated height %v is outside [%v, %v]", state.Height, low, high)
			}
			// The change is signed by the direction.
			if state.Flooding != (state.Change >= 0) {
				t.Errorf("change %v contradicts flooding %v", state.Change, state.Flooding)
			}
		})
	}

	// A moment outside the window has no state at all.
	if _, ok := tideStateAt(events, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)); ok {
		t.Error("a moment before the first event reported a state")
	}
}

// TestSpringNeapAssessmentFollowsTheMoon asserts the assessment is read from the
// Moon's elongation, so it needs no provider and cannot disagree with the moon
// command: new and full are spring, the quarters are neap.
func TestSpringNeapAssessmentFollowsTheMoon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		utJD float64
		want string
	}{
		{"new moon", 2461059.327654, "Spring Tide (extreme tidal range)"},
		{"full moon", 2461073.423108, "Spring Tide (extreme tidal range)"},
		{"first quarter", 2461066.699424, "Neap Tide (minimal tidal range)"},
		{"last quarter", 2461081.029884, "Neap Tide (minimal tidal range)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SpringNeapAssessment(julianToTime(tc.utJD)); got != tc.want {
				t.Errorf("SpringNeapAssessment(%v) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}

	// Halfway between a new moon and a full moon the range is on its way to
	// being extreme, and the wording says so.
	rising := julianToTime(2461063.013539)
	if got := SpringNeapAssessment(rising); !strings.Contains(got, "Approaching") {
		t.Errorf("between new and full the assessment = %q, want an approaching line", got)
	}
}

// TestFindTideStationResolvesIdsNamesAndPorts asserts the reference table
// answers an id, a name, and a partial name.
func TestFindTideStationResolvesIdsNamesAndPorts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query string
		id    string
	}{
		{"9414290", "9414290"},
		{" 9414290 ", "9414290"},
		{"san francisco", "9414290"},
		{"SAN FRANCISCO", "9414290"},
		{"boston", "8443970"},
		{"Golden Gate", "9414290"},
	}
	for _, tc := range tests {
		station, ok := findTideStation(tc.query)
		if !ok {
			t.Errorf("findTideStation(%q) found nothing", tc.query)
			continue
		}
		if station.ID != tc.id {
			t.Errorf("findTideStation(%q) = %v (%v), want %v", tc.query, station.ID, station.Name, tc.id)
		}
	}
	for _, query := range []string{"", "   ", "nowhere at all"} {
		if station, ok := findTideStation(query); ok {
			t.Errorf("findTideStation(%q) = %v, want nothing", query, station)
		}
	}
}

// TestNearestTideStationResolvesPositions asserts a position resolves to the
// station that shares its water, and that a position far from any station is
// still reported with how far away the nearest one is.
func TestNearestTideStationResolvesPositions(t *testing.T) {
	t.Parallel()

	station, meters, ok := nearestTideStation(LatLng{Lat: 37.806, Lng: -122.466})
	if !ok {
		t.Fatal("no station found for the Golden Gate")
	}
	if station.ID != "9414290" {
		t.Errorf("nearest station to the Golden Gate = %v (%v), want 9414290", station.ID, station.Name)
	}
	if meters > 5000 {
		t.Errorf("the Golden Gate is %v m from its own station", meters)
	}

	// The whole table is in the provider's coverage, which is US waters, so a
	// position in the middle of the Pacific is a long way from all of it.
	if _, meters, ok := nearestTideStation(LatLng{Lat: 0, Lng: -140}); !ok || meters < tideNearLimitKm*1000 {
		t.Errorf("a mid-Pacific position is %v m from a station, want more than %v", meters, tideNearLimitKm*1000)
	}
}

// TestTideCommandRendersTheDay asserts the four-line answer: the station, the
// day's events, the state of the tide now, and the spring/neap assessment.
func TestTideCommandRendersTheDay(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, tideConfig())
	reg.fetch = func(url string) (string, error) {
		if !strings.Contains(url, "station=9414290") || !strings.Contains(url, "begin_date=20260915") {
			t.Errorf("the provider was asked for %q, want the station and the date", url)
		}
		return tideFixture, nil
	}
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "tide 9414290 2026-09-15", now)
	if len(lines) != 4 {
		t.Fatalf("tide = %v, want four lines", lines)
	}
	want := "Low  03:34 (0.6 ft) | High 10:19 (4.5 ft) | Low  15:17 (2.5 ft) | High 21:40 (5.8 ft)"
	if lines[1] != want {
		t.Errorf("events line =\n  %v\nwant\n  %v", lines[1], want)
	}
	// The name in the header is the provider's own published one, so the test
	// checks the id and the rest of the line rather than the center's capitals.
	if !strings.HasPrefix(lines[0], "Tides for ") ||
		!strings.Contains(strings.ToUpper(lines[0]), "SAN FRANCISCO (GOLDEN GATE)") ||
		!strings.Contains(lines[0], "(9414290) on Sep 15 (MLLW UTC):") {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], "Current: FLOODING (+") || !strings.Contains(lines[2], "% to high)") ||
		!strings.Contains(lines[2], "(in 2h19m)") {
		t.Errorf("current line = %q, want a flooding estimate two hours from the high", lines[2])
	}
	if !strings.HasPrefix(lines[3], "Spring/Neap: ") {
		t.Errorf("assessment line = %q", lines[3])
	}
}

// TestTideCommandResolvesNamesAndPositions asserts a port name and a position
// both reach the same station.
func TestTideCommandResolvesNamesAndPositions(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"san francisco 2026-09-15", "37.806, -122.466 2026-09-15", "9414290 2026-09-15"} {
		reg, session, _ := commandFixture(t, tideConfig())
		var asked string
		reg.fetch = func(url string) (string, error) {
			asked = url
			return tideFixture, nil
		}
		lines := runLinesAt(t, reg, session, "tide "+query, time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC))
		if len(lines) == 0 {
			t.Fatalf("tide %v answered nothing", query)
		}
		if !strings.Contains(asked, "station=9414290") {
			t.Errorf("tide %v asked for %q, want station 9414290", query, asked)
		}
		if !strings.Contains(asked, "begin_date=20260915") {
			t.Errorf("tide %v asked for %q, want the requested date", query, asked)
		}
	}
}

// TestTideCommandOmitsTheCurrentLineForAnotherDay asserts a request for a date
// that is not today reports the events without pretending to know the present
// state.
func TestTideCommandOmitsTheCurrentLineForAnotherDay(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, tideConfig())
	reg.fetch = func(url string) (string, error) { return tideFixture, nil }
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "tide 9414290 2026-09-16", now)
	if len(lines) != 3 {
		t.Fatalf("tide for another day = %v, want the header, the events, and the assessment", lines)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "Current:") {
			t.Errorf("a request for another day reported the current state: %q", line)
		}
	}
}

// TestTideCommandReportsItsConfiguration asserts the command explains rather
// than guesses when it cannot answer.
func TestTideCommandReportsItsConfiguration(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	if lines := runLinesAt(t, reg, session, "tide 9414290", spacewxClock); len(lines) != 1 ||
		lines[0] != tideNotConfiguredLine {
		t.Errorf("tide with no provider = %v, want %q", lines, tideNotConfiguredLine)
	}

	reg, session, _ = commandFixture(t, tideConfig())
	for _, line := range []string{"tide", "tide nowhere at all", "tide 0, 0"} {
		lines := runLinesAt(t, reg, session, line, spacewxClock)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+tideUsage) {
			t.Errorf("%q = %v, want the usage line", line, lines)
		}
	}

	cfg := tideConfig()
	cfg.TideURL = "file:///etc/passwd?station={place}&date={date}"
	reg, session, _ = commandFixture(t, cfg)
	if lines := runLinesAt(t, reg, session, "tide 9414290", spacewxClock); len(lines) != 1 ||
		lines[0] != tideMisconfiguredLine {
		t.Errorf("tide with a bad template = %v, want %q", lines, tideMisconfiguredLine)
	}
}

// TestTideCommandFallsBackToTheFailureLine asserts a provider failure is
// reported as one, not as a day with no tide.
func TestTideCommandFallsBackToTheFailureLine(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, tideConfig())
	reg.fetch = func(url string) (string, error) {
		return `{"error":{"message":"No Predictions data was found."}}`, nil
	}
	lines := runLinesAt(t, reg, session, "tide 9414290 2026-09-15", spacewxClock)
	if len(lines) != 1 || lines[0] != tideFailedLine {
		t.Fatalf("tide with a failing provider = %v, want %q", lines, tideFailedLine)
	}
	if strings.Contains(lines[0], "Predictions data") {
		t.Errorf("the answer repeated the provider's own message: %q", lines[0])
	}
}

// TestTideEventsOnFiltersToTheDay asserts only the requested day's events are
// listed, so a 48-hour window does not produce a second day's table.
func TestTideEventsOnFiltersToTheDay(t *testing.T) {
	t.Parallel()

	events, err := parseTidePredictions(tideFixture)
	if err != nil {
		t.Fatalf("parseTidePredictions: %v", err)
	}
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	on := tideEventsOn(events, day)
	if len(on) != 4 {
		t.Fatalf("the day has %v events, want 4", len(on))
	}
	for _, event := range on {
		if event.At.Before(day) || !event.At.Before(day.AddDate(0, 0, 1)) {
			t.Errorf("event %v is outside the day", event.At)
		}
	}
	if next := tideEventsOn(events, day.AddDate(0, 0, 1)); len(next) != 4 {
		t.Errorf("the next day has %v events, want 4", len(next))
	}
}

// TestTideStationTableIsCoherent asserts the reference table has no duplicate
// ids and that every entry carries a usable position.
func TestTideStationTableIsCoherent(t *testing.T) {
	t.Parallel()

	if len(tideStations) < 100 {
		t.Fatalf("the reference table holds %v stations, want at least 100", len(tideStations))
	}
	seen := map[string]bool{}
	for _, station := range tideStations {
		if seen[station.ID] {
			t.Errorf("station %v appears twice", station.ID)
		}
		seen[station.ID] = true
		if len(station.ID) != 7 {
			t.Errorf("station id %q is not seven digits", station.ID)
		}
		if station.Name == "" {
			t.Errorf("station %v has no name", station.ID)
		}
		if math.Abs(station.Lat) > 90 || math.Abs(station.Lng) > 180 {
			t.Errorf("station %v is at an impossible position: %v, %v", station.ID, station.Lat, station.Lng)
		}
	}
}

// TestTideSearchFindsAStationByNameAndID asserts the two things an asker can
// type without knowing anything: the name they would say, and the id they
// half-remember.
func TestTideSearchFindsAStationByNameAndID(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)

	byName := runLines(t, reg, session, "tide search san francisco")
	if len(byName) == 0 || !strings.Contains(byName[0], `Tide stations matching "san francisco" (Page 1 of 1):`) {
		t.Fatalf("tide search san francisco = %v, want one page of matches", byName)
	}
	if !strings.Contains(byName[1], "  9414290: ") ||
		!strings.Contains(strings.ToUpper(byName[1]), "SAN FRANCISCO") {
		t.Errorf("first match = %q, want the Golden Gate station", byName[1])
	}

	byID := runLines(t, reg, session, "tide search 9414")
	if len(byID) < 2 || !strings.Contains(byID[1], "9414") {
		t.Errorf("tide search 9414 = %v, want the stations whose id starts 9414", byID)
	}

	byState := runLines(t, reg, session, "tide search Oregon")
	if len(byState) < 2 {
		t.Fatalf("tide search Oregon = %v, want the Oregon stations", byState)
	}
	for _, line := range byState[1:] {
		if strings.HasPrefix(line, "[") {
			continue
		}
		if !strings.Contains(line, ", OR") {
			t.Errorf("tide search Oregon listed a station outside Oregon: %q", line)
		}
	}
}

// TestTideSearchPaginates asserts a query with more hits than one page carries
// is cut into pages that say so, and that the page after the first is reachable
// by its explicit command.
func TestTideSearchPaginates(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	first := runLines(t, reg, session, "tide search 94")
	if len(first) == 0 || !strings.Contains(first[0], `Tide stations matching "94" (Page 1 of `) {
		t.Fatalf("tide search 94 = %v, want a paged answer", first)
	}
	footer := first[len(first)-1]
	if !strings.HasPrefix(footer, "[Page 1 of ") ||
		!strings.Contains(footer, `ask "tide search 94 2" or "more" for next]`) {
		t.Errorf("footer = %q, want the explicit next-page command", footer)
	}
	second := runLines(t, reg, session, "tide search 94 2")
	if len(second) == 0 || !strings.Contains(second[0], "(Page 2 of ") {
		t.Fatalf("tide search 94 2 = %v, want page 2", second)
	}
	if second[1] == first[1] {
		t.Error("page 2 repeated page 1's rows")
	}
}

// TestTideSearchExplainsAnEmptyAnswer asserts a query that matches nothing is
// answered with the ways into the catalog rather than with silence.
func TestTideSearchExplainsAnEmptyAnswer(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "tide search zzyzx")
	if len(lines) != 2 || !strings.Contains(lines[0], `No Tide stations match "zzyzx"`) {
		t.Fatalf("tide search zzyzx = %v, want a no-match line and a hint", lines)
	}
	if !strings.Contains(lines[1], "tide list CA") || !strings.Contains(lines[1], "tide near") {
		t.Errorf("the hint = %q, want the other two ways into the catalog", lines[1])
	}
	for _, line := range []string{"tide search", "tide search   ", "tide search ???"} {
		if got := runLines(t, reg, session, line); len(got) == 0 || !strings.Contains(got[0], "Usage: tide search") {
			t.Errorf("%q = %v, want the search usage line", line, got)
		}
	}
	// The catalog carries every station the provider publishes, so how many
	// pages California has is the provider's data: ask for a page far past the
	// end and check that the answer names the total it really has.
	if got := runLines(t, reg, session, "tide list CA 9999"); len(got) != 1 ||
		!strings.Contains(got[0], "page 9999 not found (total ") ||
		!strings.Contains(got[0], " pages)") {
		t.Errorf("a page past the end = %v, want the totals", got)
	}
}

// TestTideListFiltersByStateOrName asserts the list form takes a state code or
// the state's own name, and that the unfiltered form lists everything.
func TestTideListFiltersByStateOrName(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	// Oregon's stations are the provider's, all of them, so the answer is a
	// region filter that pages: every row must be in OR, and paging through must
	// reach the stations a person names.
	byCode := runLines(t, reg, session, "tide list OR")
	if len(byCode) < 3 || !strings.Contains(byCode[0], "Tide stations in OR (Page 1 of ") {
		t.Fatalf("tide list OR = %v, want Oregon's stations paged", byCode)
	}
	for _, line := range byCode[1 : len(byCode)-1] {
		if !strings.Contains(line, ", OR") {
			t.Errorf("tide list OR listed a station outside Oregon: %q", line)
		}
	}
	found := map[string]bool{}
	for page := 1; page <= 64; page++ {
		lines := runLines(t, reg, session, fmt.Sprintf("tide list OR %v", page))
		if len(lines) < 3 {
			break
		}
		for _, want := range []string{"9432845", "9435308", "9439040", "9439221"} {
			if strings.Contains(strings.Join(lines, "\n"), want) {
				found[want] = true
			}
		}
		if strings.Contains(lines[len(lines)-1], "end of results") {
			break
		}
	}
	for _, want := range []string{"9432845", "9435308", "9439040", "9439221"} {
		if !found[want] {
			t.Errorf("tide list OR never reached station %v", want)
		}
	}

	byName := runLines(t, reg, session, "tide list oregon")
	if len(byName) == 0 || !strings.Contains(byName[0], "Tide stations in OREGON (Page 1 of ") ||
		byName[1] != byCode[1] {
		t.Errorf("tide list oregon = %v, want the same first page as tide list OR", byName)
	}

	all := runLines(t, reg, session, "tide list")
	if len(all) == 0 || !strings.Contains(all[0], "Tide stations (Page 1 of ") {
		t.Fatalf("tide list = %v, want the whole catalog paged", all)
	}
	pageTwo := runLines(t, reg, session, "tide list 2")
	if len(pageTwo) == 0 || !strings.Contains(pageTwo[0], "Tide stations (Page 2 of ") {
		t.Errorf("tide list 2 = %v, want page 2 of the whole catalog", pageTwo)
	}
	if got := runLines(t, reg, session, "tide list ZZ"); len(got) != 2 ||
		!strings.Contains(got[0], "No Tide stations in ZZ") {
		t.Errorf("tide list ZZ = %v, want a no-region line and a hint", got)
	}
}

// TestTideNearNamesTheClosestStations asserts a position resolves to the
// stations around it, nearest first, with the distance and the bearing.
func TestTideNearNamesTheClosestStations(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "tide near 37.8,-122.4")
	if len(lines) != 5 {
		t.Fatalf("tide near 37.8,-122.4 = %v, want a header, three stations, and a footer", lines)
	}
	if !strings.Contains(lines[0], "Tide stations near 37.8,-122.4 (Page 1 of 1):") {
		t.Errorf("header = %q, want the position that was asked for", lines[0])
	}
	for i, line := range lines[1:4] {
		if !strings.Contains(line, " (") || !strings.Contains(line, " nmi ") ||
			!strings.Contains(line, "): ") {
			t.Errorf("station %v of the answer = %q, want a distance, a bearing, and the name", i+1, line)
		}
	}
	if !closerFirst(lines[1:4]) {
		t.Errorf("tide near 37.8,-122.4 = %v, want the nearest station first", lines[1:4])
	}
	if want := "[Page 1 of 1: end of results]"; lines[4] != want {
		t.Errorf("footer = %q, want %q", lines[4], want)
	}

	byName := runLines(t, reg, session, "tide near golden gate")
	if len(byName) < 2 || !strings.Contains(byName[1], "9414290") {
		t.Errorf("tide near golden gate = %v, want the station named for it", byName)
	}
	byCity := runLines(t, reg, session, "tide near Orlando, fl")
	if len(byCity) != 5 {
		t.Fatalf("tide near Orlando, fl = %v, want a header, three stations, and a footer", byCity)
	}
	if !strings.Contains(byCity[0], "Tide stations near Orlando, fl") {
		t.Errorf("header = %q, want the city that was asked for", byCity[0])
	}
	// Which station is nearest to Orlando is the provider's data: the command
	// promises the nearest first, with its distance and bearing.
	if !strings.Contains(byCity[1], " nmi ") || !strings.Contains(byCity[1], "): ") {
		t.Errorf("nearest tide station to Orlando = %q, want a distance and a name", byCity[1])
	}
	if !closerFirst(byCity[1:4]) {
		t.Errorf("tide near Orlando, fl = %v, want the nearest station first", byCity[1:4])
	}
	if got := runLines(t, reg, session, "tide near nowhere at all"); len(got) != 2 ||
		!strings.Contains(got[0], "not a place, a coordinate, or a plus code") {
		t.Errorf("tide near nowhere = %v, want an explanation and a hint", got)
	}
	if got := runLines(t, reg, session, "tide near"); len(got) == 0 ||
		!strings.Contains(got[0], "Usage: tide near") {
		t.Errorf("tide near with no place = %v, want the usage line", got)
	}
}

// TestTideDiscoveryNeedsNoProvider asserts finding a station is an offline
// question: it answers on a bot with no tide_url at all, which is what makes the
// opaque ids discoverable before any provider is configured.
func TestTideDiscoveryNeedsNoProvider(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	reg.fetch = func(url string) (string, error) {
		t.Errorf("discovery reached the network: %v", url)
		return "", nil
	}
	for _, line := range []string{"tide search san francisco", "tide list CA", "tide near 37.8,-122.4"} {
		if lines := runLines(t, reg, session, line); len(lines) < 2 {
			t.Errorf("%v = %v, want an offline answer", line, lines)
		}
	}
	if lines := runLines(t, reg, session, "tide 9414290"); len(lines) != 1 || lines[0] != tideNotConfiguredLine {
		t.Errorf("tide 9414290 = %v, want %q", lines, tideNotConfiguredLine)
	}
}
