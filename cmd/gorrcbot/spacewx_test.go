// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// spacewxClock is the fixed clock the space-weather tests run on.
var spacewxClock = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

// spacewxBody is the provider answer the cached-fetch tests serve.
const spacewxBody = `{"sfi":158,"ssn":112,"kp":4}`

// spacewxConfig is a configuration with a provider URL set.
func spacewxConfig() *BotConfig {
	cfg := defaultTestConfig()
	cfg.SpaceWeatherURL = "https://services.swpc.noaa.gov/products/observed-solar-flux.json"
	return cfg
}

// TestParseSpaceWeatherAcceptsProviderShapes asserts the parser reads the
// indices out of a flat object, a nested one with renamed keys, and NOAA's own
// array-of-arrays product shape.
func TestParseSpaceWeatherAcceptsProviderShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		sfi  float64
		ssn  float64
		kp   float64
	}{
		{"flat", `{"sfi":158,"ssn":112,"kp":4}`, 158, 112, 4},
		{"nested and renamed", `{"solar_flux":{"f107":150},"sunspot_number":"112","kp_index":3.33}`, 150, 112, 3.33},
		{"punctuated keys", `{"Solar Flux Index": 145, "Sunspot-Number": 90, "Kp-index": 2}`, 145, 90, 2},
		{"noaa product rows", `[["time_tag","kp_index","estimated_kp","kp"],` +
			`["2026-03-15 09:00:00",3,3.33,3],["2026-03-15 12:00:00",4,4.33,4]]`, 0, 0, 4},
		{"numeric strings", `{"sfi":"158","ssn":"112","kp":"4"}`, 158, 112, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSpaceWeather(tc.body)
			if err != nil {
				t.Fatalf("parseSpaceWeather: %v", err)
			}
			if got.SFI != tc.sfi || got.SSN != tc.ssn || got.Kp != tc.kp {
				t.Errorf("parseSpaceWeather = SFI %v SSN %v Kp %v, want %v %v %v",
					got.SFI, got.SSN, got.Kp, tc.sfi, tc.ssn, tc.kp)
			}
		})
	}
}

// TestParseSpaceWeatherRejectsUnusableAnswers asserts an answer that is not
// JSON, or that carries no index at all, is refused rather than rendered as a
// line full of zeros.
func TestParseSpaceWeatherRejectsUnusableAnswers(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"", "not json", "<html>nope</html>", `{}`, `{"foo":1}`, `[]`} {
		if got, err := parseSpaceWeather(body); err == nil {
			t.Errorf("parseSpaceWeather(%q) = %+v, want an error", body, got)
		}
	}
}

// TestSpaceWeatherLineMatchesTheDocumentedShape asserts the one-line report is
// exactly the documented shape for the documented reading.
func TestSpaceWeatherLineMatchesTheDocumentedShape(t *testing.T) {
	t.Parallel()

	reading := SpaceWeatherReading{SFI: 158, SSN: 112, Kp: 4, At: spacewxClock}
	want := "SFI: 158 | Sunspots: 112 | Kp-index: 4 (Unsettled) | Geomag: G0 Quiet | HF Bands: Fair across 20m-15m"
	if got := reading.line(spacewxClock); got != want {
		t.Errorf("line =\n  %v\nwant\n  %v", got, want)
	}
	// A manual entry says so, because an operator needs to know they are
	// reading somebody's voice-net numbers rather than a fresh measurement.
	manual := reading
	manual.Manual = true
	if got := manual.line(spacewxClock); !strings.HasSuffix(got, "(operator entry)") {
		t.Errorf("manual line = %q, want it marked as an operator entry", got)
	}
	// A reading that is not from this moment carries its age.
	old := reading
	old.At = spacewxClock.Add(-25 * time.Minute)
	if got := old.line(spacewxClock); !strings.HasSuffix(got, "(reading from 25m ago)") {
		t.Errorf("aged line = %q, want its age", got)
	}
}

// TestKpDescriptionAndGeomagneticScale asserts the two scales agree with the
// K-index and with each other at every point.
func TestKpDescriptionAndGeomagneticScale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kp     float64
		desc   string
		geomag string
	}{
		{0, "Quiet", "G0 Quiet"},
		{2, "Quiet", "G0 Quiet"},
		{3, "Unsettled", "G0 Quiet"},
		{4, "Unsettled", "G0 Quiet"},
		{5, "Minor storm", "G1 Minor"},
		{6, "Moderate storm", "G2 Moderate"},
		{7, "Strong storm", "G3 Strong"},
		{8, "Severe storm", "G4 Severe"},
		{9, "Extreme storm", "G5 Extreme"},
	}
	for _, tc := range tests {
		if got := KpDescription(tc.kp); got != tc.desc {
			t.Errorf("KpDescription(%v) = %q, want %q", tc.kp, got, tc.desc)
		}
		if got := GeomagneticScale(tc.kp); got != tc.geomag {
			t.Errorf("GeomagneticScale(%v) = %q, want %q", tc.kp, got, tc.geomag)
		}
	}
}

// TestHfBandOutlookFollowsFluxAndDisturbance asserts a storm always closes the
// high bands and that a rising flux never makes the outlook worse.
func TestHfBandOutlookFollowsFluxAndDisturbance(t *testing.T) {
	t.Parallel()

	if got := hfBandOutlook(158, 4); got != "Fair across 20m-15m" {
		t.Errorf("hfBandOutlook(158, 4) = %q, want the documented fair outlook", got)
	}
	for _, kp := range []float64{5, 6, 7, 9} {
		if got := hfBandOutlook(250, kp); !strings.HasPrefix(got, "Poor") {
			t.Errorf("hfBandOutlook(250, %v) = %q, want a storm to close the high bands", kp, got)
		}
	}
	previous := ""
	for _, sfi := range []float64{70, 95, 120, 160, 210} {
		if got := hfBandOutlook(sfi, 1); got == previous {
			t.Errorf("hfBandOutlook(%v, 1) repeats %q, want a distinct outlook", sfi, got)
		} else {
			previous = got
		}
	}
}

// TestSpacewxCommandFetchesAndCaches asserts a cold cache fetches, a warm one
// does not, and an expired one fetches again.
func TestSpacewxCommandFetchesAndCaches(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, spacewxConfig())
	fetches := 0
	reg.fetch = func(url string) (string, error) {
		fetches++
		return spacewxBody, nil
	}

	lines := runLinesAt(t, reg, session, "spacewx", spacewxClock)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "SFI: 158 | Sunspots: 112 | Kp-index: 4 (Unsettled)") {
		t.Fatalf("spacewx = %v, want the reading", lines)
	}
	if fetches != 1 {
		t.Fatalf("fetches = %v, want 1", fetches)
	}

	// Inside the hour the cached reading is reused.
	runLinesAt(t, reg, session, "spacewx", spacewxClock.Add(30*time.Minute))
	if fetches != 1 {
		t.Errorf("fetches = %v after a warm lookup, want still 1", fetches)
	}
	// Past it, the provider is asked again.
	runLinesAt(t, reg, session, "spacewx", spacewxClock.Add(2*time.Hour))
	if fetches != 2 {
		t.Errorf("fetches = %v after the cache expired, want 2", fetches)
	}
}

// TestSpacewxCommandDegradesWhenTheProviderFails asserts the last reading is
// reported, with its age, rather than a failure line, once one has been seen.
func TestSpacewxCommandDegradesWhenTheProviderFails(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, spacewxConfig())
	reg.fetch = func(url string) (string, error) { return spacewxBody, nil }
	runLinesAt(t, reg, session, "spacewx", spacewxClock)

	reg.fetch = func(url string) (string, error) { return "", errors.New("no route to host") }
	lines := runLinesAt(t, reg, session, "spacewx", spacewxClock.Add(3*time.Hour))
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "SFI: 158") {
		t.Fatalf("spacewx after a provider failure = %v, want the cached reading", lines)
	}
	if !strings.Contains(lines[0], "(reading from 3h ago)") {
		t.Errorf("cached line = %q, want its age", lines[0])
	}
}

// TestSpacewxCommandWithoutAProvider asserts an unconfigured bot says how to
// turn the command on, and reports an operator entry when it has one.
func TestSpacewxCommandWithoutAProvider(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLinesAt(t, reg, session, "spacewx", spacewxClock)
	if len(lines) != 1 || lines[0] != spacewxNotConfiguredLine {
		t.Fatalf("spacewx with no provider = %v, want %q", lines, spacewxNotConfiguredLine)
	}

	set := runLinesAt(t, reg, session, "spacewx set sfi=158 ssn=112 kp=4", spacewxClock)
	if len(set) != 1 || !strings.Contains(set[0], "SFI: 158") || !strings.HasSuffix(set[0], "(operator entry)") {
		t.Fatalf("spacewx set = %v, want the entered reading", set)
	}
	// With a reading in hand, an unconfigured bot reports it rather than the
	// configuration line.
	again := runLinesAt(t, reg, session, "spacewx", spacewxClock)
	if len(again) != 1 || !strings.HasSuffix(again[0], "(operator entry)") {
		t.Errorf("spacewx after an operator entry = %v, want the entry reported", again)
	}
}

// TestSpacewxCommandReportsAMisconfiguredProvider asserts an unusable URL is
// refused without repeating it into the room.
func TestSpacewxCommandReportsAMisconfiguredProvider(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.SpaceWeatherURL = "file:///etc/passwd"
	reg, session, _ := commandFixture(t, cfg)
	lines := runLinesAt(t, reg, session, "spacewx", spacewxClock)
	if len(lines) != 1 || lines[0] != spacewxMisconfiguredLine {
		t.Fatalf("spacewx with a bad URL = %v, want the misconfiguration line", lines)
	}
	if strings.Contains(lines[0], "passwd") {
		t.Errorf("the answer repeats the configured URL: %q", lines[0])
	}
}

// TestSpacewxSetRejectsBadInput asserts a malformed operator entry is refused
// with what the command accepts.
func TestSpacewxSetRejectsBadInput(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{
		"spacewx set",
		"spacewx set sfi",
		"spacewx set sfi=abc",
		"spacewx set kp=12",
		"spacewx set qq=1",
	} {
		lines := runLinesAt(t, reg, session, line, spacewxClock)
		if len(lines) == 0 || !strings.Contains(lines[0], "spacewx:") && !strings.Contains(lines[0], "Usage:") {
			t.Errorf("%q = %v, want the command's guidance", line, lines)
		}
	}
	if lines := runLinesAt(t, reg, session, "spacewx 158", spacewxClock); len(lines) == 0 ||
		!strings.Contains(lines[0], "Usage: "+spacewxUsage) {
		t.Errorf("spacewx with a stray argument = %v, want the usage line", lines)
	}
}
