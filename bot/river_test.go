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

// riverFixture is a real stream-gauge answer, trimmed to the points the trend
// and the report need. The structure and every field name are the service's
// own; only the number of points is reduced.
const riverFixture = `{"value":{"timeSeries":[
 {"sourceInfo":{"siteName":"POTOMAC RIVER NEAR WASH, DC LITTLE FALLS PUMP STA",
   "siteCode":[{"value":"01646500","network":"NWIS","agencyCode":"USGS"}]},
  "variable":{"variableCode":[{"value":"00065"}]},
  "values":[{"value":[
    {"value":"6.42","qualifiers":["P"],"dateTime":"2026-09-15T14:45:00.000-04:00"},
    {"value":"6.68","qualifiers":["P"],"dateTime":"2026-09-15T15:45:00.000-04:00"},
    {"value":"","qualifiers":["P"],"dateTime":"2026-09-15T16:45:00.000-04:00"},
    {"value":"6.82","qualifiers":["P"],"dateTime":"2026-09-15T17:45:00.000-04:00"}]}]},
 {"sourceInfo":{"siteName":"POTOMAC RIVER NEAR WASH, DC LITTLE FALLS PUMP STA",
   "siteCode":[{"value":"01646500","network":"NWIS","agencyCode":"USGS"}]},
  "variable":{"variableCode":[{"value":"00060"}]},
  "values":[{"value":[
    {"value":"13900","qualifiers":["P"],"dateTime":"2026-09-15T14:45:00.000-04:00"},
    {"value":"14200","qualifiers":["P"],"dateTime":"2026-09-15T17:45:00.000-04:00"}]}]}
]}}`

// riverFloodFixture is a real river-forecast-center answer for the same gauge,
// trimmed to the fields the command reads.
const riverFloodFixture = `{"lid":"BRKM2","usgsId":"01646500",
 "name":"Potomac River near Washington DC (Little Falls)",
 "flood":{"stageUnits":"ft","flowUnits":"cfs","categories":{
   "major":{"stage":14,"flow":-9999},"moderate":{"stage":12,"flow":-9999},
   "minor":{"stage":10,"flow":-9999},"action":{"stage":5,"flow":22000}}}}`

// riverConfig is a configuration with both providers set.
func riverConfig() *BotConfig {
	cfg := defaultTestConfig()
	cfg.RiverURL = "https://waterservices.usgs.gov/nwis/iv/?sites={place}&format=json&parameterCd=00065,00060&period=P1D"
	cfg.RiverFloodURL = "https://api.water.noaa.gov/nwps/v1/gauges/{place}"
	return cfg
}

// TestParseRiverGaugeReadsTheService asserts the instantaneous-values answer
// decodes into the latest stage and flow, with the point the trend is measured
// against.
func TestParseRiverGaugeReadsTheService(t *testing.T) {
	t.Parallel()

	reading, err := ParseRiverGauge(riverFixture)
	if err != nil {
		t.Fatalf("ParseRiverGauge: %v", err)
	}
	if reading.SiteID != "01646500" {
		t.Errorf("site id = %q, want 01646500", reading.SiteID)
	}
	if !strings.HasPrefix(reading.SiteName, "POTOMAC RIVER") {
		t.Errorf("site name = %q", reading.SiteName)
	}
	if !reading.HasStage || !closeWithin(reading.StageFt, 6.82, 1e-9) {
		t.Errorf("stage = %v, want 6.82", reading.StageFt)
	}
	// The service stamps its times with the gauge's own offset; the reading is
	// kept in UTC.
	if want := time.Date(2026, 9, 15, 21, 45, 0, 0, time.UTC); !reading.StageAt.Equal(want) {
		t.Errorf("stage measured at %v, want %v", reading.StageAt, want)
	}
	if !reading.HasFlow || !closeWithin(reading.FlowCFS, 14200, 1e-9) {
		t.Errorf("flow = %v, want 14200", reading.FlowCFS)
	}
	// Three hours back from 17:45 is 14:45, which is where the 6.42 point is.
	if !reading.HasPrevious || !closeWithin(reading.PreviousFt, 6.42, 1e-9) {
		t.Errorf("previous stage = %v, want 6.42", reading.PreviousFt)
	}
	if want := time.Date(2026, 9, 15, 18, 45, 0, 0, time.UTC); !reading.PreviousAt.Equal(want) {
		t.Errorf("previous stage measured at %v, want %v", reading.PreviousAt, want)
	}
}

// TestParseRiverGaugeHandlesPartialAnswers asserts a gauge that publishes only
// one parameter, or no history at all, is still read for what it has.
func TestParseRiverGaugeHandlesPartialAnswers(t *testing.T) {
	t.Parallel()

	stageOnly := `{"value":{"timeSeries":[
	 {"sourceInfo":{"siteName":"A RIVER","siteCode":[{"value":"01234567"}]},
	  "variable":{"variableCode":[{"value":"00065"}]},
	  "values":[{"value":[{"value":"3.10","dateTime":"2026-09-15T12:00:00Z"}]}]}]}}`
	reading, err := ParseRiverGauge(stageOnly)
	if err != nil {
		t.Fatalf("ParseRiverGauge: %v", err)
	}
	if !reading.HasStage || reading.HasFlow {
		t.Errorf("reading = %+v, want the stage alone", reading)
	}
	if reading.HasPrevious {
		t.Error("a single point produced a previous reading")
	}

	// A blank value, which is what an out-of-service gauge publishes, is not a
	// reading of zero.
	blank := `{"value":{"timeSeries":[
	 {"sourceInfo":{"siteName":"A RIVER","siteCode":[{"value":"01234567"}]},
	  "variable":{"variableCode":[{"value":"00065"}]},
	  "values":[{"value":[{"value":"","dateTime":"2026-09-15T12:00:00Z"}]}]}]}}`
	if got, err := ParseRiverGauge(blank); err == nil {
		t.Errorf("a blank value decoded to %+v, want an error", got)
	}
}

// TestParseRiverGaugeRejectsUnusableAnswers asserts an answer that carries no
// gauge data is refused rather than reported as a dry river.
func TestParseRiverGaugeRejectsUnusableAnswers(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"",
		"not json",
		`{}`,
		`{"value":{"timeSeries":[]}}`,
		`{"value":{"timeSeries":[{"sourceInfo":{"siteName":"A RIVER"},"variable":{"variableCode":[{"value":"00065"}]}}]}}`,
	} {
		if reading, err := ParseRiverGauge(body); err == nil {
			t.Errorf("ParseRiverGauge(%q) = %+v, want an error", body, reading)
		}
	}
}

// TestParseRiverFloodThresholdsReadsTheCategories asserts the forecast center's
// categories decode, and that a category it does not define is left absent
// rather than treated as zero.
func TestParseRiverFloodThresholdsReadsTheCategories(t *testing.T) {
	t.Parallel()

	thresholds, err := ParseRiverFloodThresholds(riverFloodFixture)
	if err != nil {
		t.Fatalf("ParseRiverFloodThresholds: %v", err)
	}
	if thresholds.Name != "Potomac River near Washington DC (Little Falls)" {
		t.Errorf("name = %q", thresholds.Name)
	}
	if !thresholds.HasAction || !closeWithin(thresholds.Action, 5, 1e-9) {
		t.Errorf("action stage = %v, want 5", thresholds.Action)
	}
	if !thresholds.HasMinor || !closeWithin(thresholds.Minor, 10, 1e-9) {
		t.Errorf("flood stage = %v, want 10", thresholds.Minor)
	}
	if !thresholds.HasModerate || !closeWithin(thresholds.Moderate, 12, 1e-9) {
		t.Errorf("moderate stage = %v, want 12", thresholds.Moderate)
	}
	if !thresholds.HasMajor || !closeWithin(thresholds.Major, 14, 1e-9) {
		t.Errorf("major stage = %v, want 14", thresholds.Major)
	}

	// A center that defines only some of the categories leaves the rest
	// undefined, which the status must not read as zero.
	partial := `{"name":"A RIVER","flood":{"categories":{
		"action":{"stage":4},"minor":{"stage":9},"moderate":{"stage":-9999},"major":{"stage":-9999}}}}`
	got, err := ParseRiverFloodThresholds(partial)
	if err != nil {
		t.Fatalf("ParseRiverFloodThresholds: %v", err)
	}
	if got.HasModerate || got.HasMajor {
		t.Errorf("undefined categories were read as stages: %+v", got)
	}
	if !got.HasAction || !got.HasMinor {
		t.Errorf("defined categories are missing: %+v", got)
	}
}

// TestParseRiverFloodThresholdsRejectsUnusableAnswers asserts an answer with no
// categories is refused, so the command reports the stage without a comparison
// rather than inventing one.
func TestParseRiverFloodThresholdsRejectsUnusableAnswers(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"", "not json", `{}`, `{"flood":null}`, `{"flood":{"categories":{}}}`} {
		if thresholds, err := ParseRiverFloodThresholds(body); err == nil {
			t.Errorf("ParseRiverFloodThresholds(%q) = %+v, want an error", body, thresholds)
		}
	}
}

// TestRiverFloodStatusNamesEveryCategory asserts the status follows the
// published categories, and that an undefined category cannot be crossed.
func TestRiverFloodStatusNamesEveryCategory(t *testing.T) {
	t.Parallel()

	full := RiverThresholds{Action: 5, Minor: 10, Moderate: 12, Major: 14,
		HasAction: true, HasMinor: true, HasModerate: true, HasMajor: true}
	tests := []struct {
		stage float64
		want  string
	}{
		{2.77, "NORMAL / NO FLOOD"},
		{4.99, "NORMAL / NO FLOOD"},
		{5, "ACTION STAGE"},
		{9.99, "ACTION STAGE"},
		{10, "FLOOD"},
		{11.99, "FLOOD"},
		{12, "MODERATE FLOOD"},
		{13.99, "MODERATE FLOOD"},
		{14, "MAJOR FLOOD"},
		{20, "MAJOR FLOOD"},
	}
	for _, tc := range tests {
		if got := RiverFloodStatus(tc.stage, full); got != tc.want {
			t.Errorf("RiverFloodStatus(%v) = %q, want %q", tc.stage, got, tc.want)
		}
	}

	// With only an action stage defined, a flood-level river is still only at
	// action stage: the categories that are not published cannot be guessed.
	actionOnly := RiverThresholds{Action: 5, HasAction: true}
	if got := RiverFloodStatus(20, actionOnly); got != "ACTION STAGE" {
		t.Errorf("with only an action stage defined, a high river = %q", got)
	}
}

// TestRiverCommandRendersTheGaugeReport asserts the three-line answer matches
// the documented shape: the gauge, the stage, the flow and the trend, and the
// flood status.
func TestRiverCommandRendersTheGaugeReport(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, riverConfig())
	reg.fetch = func(url string) (string, error) {
		switch {
		case strings.Contains(url, "waterservices.usgs.gov"):
			if !strings.Contains(url, "sites=01646500") {
				t.Errorf("the gauge was asked for as %q", url)
			}
			return riverFixture, nil
		case strings.Contains(url, "nwps"):
			if !strings.HasSuffix(url, "/01646500") {
				t.Errorf("the threshold provider was asked for %q", url)
			}
			return riverFloodFixture, nil
		default:
			return "", nil
		}
	}
	now := time.Date(2026, 9, 15, 22, 0, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "river 01646500", now)
	if len(lines) != 3 {
		t.Fatalf("river = %v, want three lines", lines)
	}
	wantHeader := "Potomac River near Washington DC (Little Falls) (01646500) 15m ago:"
	if lines[0] != wantHeader {
		t.Errorf("header =\n  %v\nwant\n  %v", lines[0], wantHeader)
	}
	wantBody := "Stage: 6.82 ft (Flood Stage: 10.0 ft) | Flow: 14,200 cfs | Trend: Rising (+0.40 ft/3h)"
	if lines[1] != wantBody {
		t.Errorf("body =\n  %v\nwant\n  %v", lines[1], wantBody)
	}
	// 6.82 ft is above the center's 5 ft action stage, so the status names it.
	if lines[2] != "Status: ACTION STAGE (Action Stage at 5.0 ft)" {
		t.Errorf("status = %q", lines[2])
	}
}

// TestRiverCommandWithoutThresholdsSaysSo asserts a gauge with no comparison
// configured is reported as such, rather than as a river that is not flooding.
func TestRiverCommandWithoutThresholdsSaysSo(t *testing.T) {
	t.Parallel()

	cfg := riverConfig()
	cfg.RiverFloodURL = ""
	reg, session, _ := commandFixture(t, cfg)
	reg.fetch = func(url string) (string, error) { return riverFixture, nil }

	lines := runLinesAt(t, reg, session, "river 01646500", time.Date(2026, 9, 15, 22, 0, 0, 0, time.UTC))
	if len(lines) != 3 {
		t.Fatalf("river = %v, want three lines", lines)
	}
	if !strings.HasPrefix(lines[2], "Status: unknown") || !strings.Contains(lines[2], "river_flood_url") {
		t.Errorf("status = %q, want it to name what is missing", lines[2])
	}
	if strings.Contains(lines[1], "Flood Stage") {
		t.Errorf("body = %q, want no flood stage without thresholds", lines[1])
	}
}

// TestRiverCommandReportsItsConfiguration asserts the command explains rather
// than guesses when it cannot answer.
func TestRiverCommandReportsItsConfiguration(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	if lines := runLinesAt(t, reg, session, "river 01646500", spacewxClock); len(lines) != 1 ||
		lines[0] != riverNotConfiguredLine {
		t.Errorf("river with no provider = %v, want %q", lines, riverNotConfiguredLine)
	}

	reg, session, _ = commandFixture(t, riverConfig())
	for _, line := range []string{"river", "river 1234", "river 0164650a", "river 01646500/../etc"} {
		lines := runLinesAt(t, reg, session, line, spacewxClock)
		if len(lines) != 1 || lines[0] != riverGaugeRejectedLine {
			t.Errorf("%q = %v, want the gauge line", line, lines)
		}
	}

	cfg := riverConfig()
	cfg.RiverURL = "file:///etc/passwd?sites={place}"
	reg, session, _ = commandFixture(t, cfg)
	if lines := runLinesAt(t, reg, session, "river 01646500", spacewxClock); len(lines) != 1 ||
		lines[0] != riverMisconfiguredLine {
		t.Errorf("river with a bad template = %v, want %q", lines, riverMisconfiguredLine)
	}

	reg, session, _ = commandFixture(t, riverConfig())
	reg.fetch = func(url string) (string, error) { return "no gauge here", nil }
	if lines := runLinesAt(t, reg, session, "river 01646500", spacewxClock); len(lines) != 1 ||
		lines[0] != riverFailedLine {
		t.Errorf("river with an answer that does not decode = %v, want %q", lines, riverFailedLine)
	}
}

// TestRiverTrendNamesTheDirection asserts the trend is signed, named, and
// measured over the window it says it is.
func TestRiverTrendNamesTheDirection(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 15, 21, 45, 0, 0, time.UTC)
	previous := at.Add(-3 * time.Hour)
	tests := []struct {
		name     string
		from, to float64
		want     string
	}{
		{"rising", 6.42, 6.82, "Rising (+0.40 ft/3h)"},
		{"falling", 6.82, 6.42, "Falling (-0.40 ft/3h)"},
		{"steady", 6.80, 6.82, "Steady (3h)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reading := RiverReading{StageFt: tc.to, StageAt: at, HasStage: true,
				PreviousFt: tc.from, PreviousAt: previous, HasPrevious: true}
			if got := riverTrend(reading); got != tc.want {
				t.Errorf("riverTrend = %q, want %q", got, tc.want)
			}
		})
	}
	// With no history the trend says so instead of implying the river is
	// steady.
	if got := riverTrend(RiverReading{StageFt: 6.82, HasStage: true}); !strings.HasPrefix(got, "unknown") {
		t.Errorf("trend with no history = %q, want unknown", got)
	}
}

// TestFormatThousandsSeparatesTheGroups asserts a discharge is rendered the way
// a gauge report writes it.
func TestFormatThousandsSeparatesTheGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{14200, "14,200"},
		{1234567, "1,234,567"},
		{-2500, "-2,500"},
		{14200.4, "14,200"},
	}
	for _, tc := range tests {
		if got := formatThousands(tc.value); got != tc.want {
			t.Errorf("formatThousands(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestTitleizeSiteNameKeepsAbbreviations asserts a gauge name published in
// capitals becomes readable without turning "DC" into "Dc".
func TestTitleizeSiteNameKeepsAbbreviations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"POTOMAC RIVER NEAR WASH, DC LITTLE FALLS PUMP STA", "Potomac River Near Wash, DC Little Falls Pump Sta"},
		{"MISSISSIPPI RIVER AT ST. LOUIS, MO", "Mississippi River At St. Louis, MO"},
		{"Gauge USGS 01646500", "Gauge USGS 01646500"},
		{"Potomac River near Washington DC (Little Falls)", "Potomac River near Washington DC (Little Falls)"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := titleizeSiteName(tc.in); got != tc.want {
			t.Errorf("titleizeSiteName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRiverCommandLinesFitOneEnvelope asserts all three lines fit one NOTICE.
func TestRiverCommandLinesFitOneEnvelope(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, riverConfig())
	reg.fetch = func(url string) (string, error) {
		if strings.Contains(url, "nwps") {
			return riverFloodFixture, nil
		}
		return riverFixture, nil
	}
	for _, line := range runLinesAt(t, reg, session, "river 01646500",
		time.Date(2026, 9, 15, 22, 0, 0, 0, time.UTC)) {
		fits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", line)
		if err != nil {
			t.Fatalf("noticeFits(%q): %v", line, err)
		}
		if !fits {
			t.Errorf("line %q does not fit one envelope", line)
		}
	}
}
