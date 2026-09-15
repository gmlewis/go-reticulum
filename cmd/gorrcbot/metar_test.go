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

// metarConfig is a configuration with a METAR provider set.
func metarConfig() *BotConfig {
	cfg := defaultTestConfig()
	cfg.MetarURL = "https://aviationweather.gov/api/data/metar?ids={place}&format=raw"
	return cfg
}

// TestParseMETARDecodesTheStandardReport asserts every group of a real report
// decodes into the fields the answer prints.
func TestParseMETARDecodesTheStandardReport(t *testing.T) {
	t.Parallel()

	raw := "KDEN 152053Z 34012KT 10SM FEW250 18/02 A3012 RMK AO2 SLP123 T01780017"
	got, err := ParseMETAR(raw)
	if err != nil {
		t.Fatalf("ParseMETAR: %v", err)
	}
	if got.Station != "KDEN" {
		t.Errorf("Station = %q, want KDEN", got.Station)
	}
	if got.Time != "152053Z" {
		t.Errorf("Time = %q, want 152053Z", got.Time)
	}
	if got.Wind != "340° 12kt" {
		t.Errorf("Wind = %q, want 340° 12kt", got.Wind)
	}
	if got.Visibility != "10SM" {
		t.Errorf("Visibility = %q, want 10SM", got.Visibility)
	}
	if !got.HasTemperature || got.TemperatureC != 18 || got.DewpointC != 2 {
		t.Errorf("temperature = %v/%v (present %v), want 18/2", got.TemperatureC, got.DewpointC, got.HasTemperature)
	}
	if !got.HasAltimeter || !closeWithin(got.AltimeterInHg, 30.12, 1e-9) || got.AltimeterHPa != 1020 {
		t.Errorf("altimeter = %v inHg / %v hPa, want 30.12 / 1020", got.AltimeterInHg, got.AltimeterHPa)
	}
	if got.Sky != "FEW250" {
		t.Errorf("Sky = %q, want FEW250", got.Sky)
	}
}

// TestMETARLineMatchesTheDocumentedShape asserts the rendered line is exactly
// the documented shape for the documented report.
func TestMETARLineMatchesTheDocumentedShape(t *testing.T) {
	t.Parallel()

	report, err := ParseMETAR("KDEN 152053Z 34012KT 10SM FEW250 18/02 A3012 RMK AO2")
	if err != nil {
		t.Fatalf("ParseMETAR: %v", err)
	}
	want := "KDEN 152053Z: Wind 340° 12kt | Vis 10SM | Temp 18°C (64°F) / DP 2°C | " +
		"Altimeter 30.12 inHg (1020 hPa) | Sky FEW250"
	if got := report.Line(); got != want {
		t.Errorf("Line =\n  %v\nwant\n  %v", got, want)
	}
}

// TestParseMETARHandlesFieldVariants asserts the groups that differ from the
// simplest case — sub-zero temperatures, gusts, variable wind, calm, statute
// and metric visibility, and a Q altimeter — all decode.
func TestParseMETARHandlesFieldVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		raw    string
		wind   string
		vis    string
		tempC  float64
		dewC   float64
		altHg  float64
		altHPa float64
	}{
		{"gusts", "KJFK 152051Z 27018G28KT 10SM SCT050 21/12 A2992", "270° 18kt gusting 28kt", "10SM", 21, 12, 29.92, 1013},
		{"variable wind", "EGLL 152050Z VRB03KT 9999 FEW030 12/09 Q1018", "variable 3kt", "9999", 12, 9, 30.06, 1018},
		{"calm", "KLAX 152053Z 00000KT 6SM BR OVC008 16/14 A3005", "calm", "6SM", 16, 14, 30.05, 1018},
		{"sub zero", "CYYZ 152100Z 32008KT 15SM SKC M05/M10 A3021", "320° 8kt", "15SM", -5, -10, 30.21, 1023},
		{"metric wind", "EGKK 152050Z 20010MPS 9999 BKN020 11/08 Q1015", "200° 10mps", "9999", 11, 8, 29.97, 1015},
		{"fractional visibility", "KSEA 152053Z 18005KT 1/2SM FG VV002 09/09 A3020", "180° 5kt", "1/2SM", 9, 9, 30.20, 1023},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseMETAR(tc.raw)
			if err != nil {
				t.Fatalf("ParseMETAR: %v", err)
			}
			if got.Wind != tc.wind {
				t.Errorf("Wind = %q, want %q", got.Wind, tc.wind)
			}
			if got.Visibility != tc.vis {
				t.Errorf("Visibility = %q, want %q", got.Visibility, tc.vis)
			}
			if got.TemperatureC != tc.tempC || got.DewpointC != tc.dewC {
				t.Errorf("temperature = %v/%v, want %v/%v", got.TemperatureC, got.DewpointC, tc.tempC, tc.dewC)
			}
			if !closeWithin(got.AltimeterInHg, tc.altHg, 0.01) || got.AltimeterHPa != tc.altHPa {
				t.Errorf("altimeter = %v inHg / %v hPa, want %v / %v",
					got.AltimeterInHg, got.AltimeterHPa, tc.altHg, tc.altHPa)
			}
		})
	}
}

// TestParseMETARRejectsUnusableReports asserts a body that carries no decodable
// group is refused, so the command falls back to the raw text instead of
// printing an empty line.
func TestParseMETARRejectsUnusableReports(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "   ", "not a report at all", "<html>error</html>"} {
		if got, err := ParseMETAR(raw); err == nil {
			t.Errorf("ParseMETAR(%q) = %+v, want an error", raw, got)
		}
	}
}

// TestMetarStationAcceptsIcaoCodes asserts a station code is normalized and
// that anything else is refused before it reaches a URL.
func TestMetarStationAcceptsIcaoCodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"kden", "KDEN"},
		{" EGLL ", "EGLL"},
		{"KJFK", "KJFK"},
		{"K1A2", "K1A2"},
	} {
		got, err := metarStation(tc.in)
		if err != nil {
			t.Errorf("metarStation(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("metarStation(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"", "K", "KDENX", "KD EN", "KDEN/../etc", "ÜDEN"} {
		if got, err := metarStation(in); err == nil {
			t.Errorf("metarStation(%q) = %q, want an error", in, got)
		}
	}
}

// TestMetarCommandAnswersTheDecodedReport asserts the command fetches through
// the injected provider and answers with the decoded line, and that the answer
// is reused while the cache is warm.
func TestMetarCommandAnswersTheDecodedReport(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, metarConfig())
	fetches := 0
	reg.fetch = func(url string) (string, error) {
		fetches++
		if !strings.Contains(url, "kden") {
			t.Errorf("the provider was asked for %q, want the lowercased station", url)
		}
		return "KDEN 152053Z 34012KT 10SM FEW250 18/02 A3012", nil
	}

	lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock)
	if len(lines) != 1 {
		t.Fatalf("metar = %v, want one line", lines)
	}
	if !strings.HasPrefix(lines[0], "KDEN 152053Z: Wind 340° 12kt") {
		t.Errorf("metar = %q, want the decoded report", lines[0])
	}
	runLinesAt(t, reg, session, "metar KDEN", spacewxClock.Add(time.Minute))
	if fetches != 1 {
		t.Errorf("fetches = %v, want the answer reused from the cache", fetches)
	}
}

// TestMetarCommandFallsBackToTheRawReport asserts an undecodable answer is
// still reported, because a raw METAR is better than nothing.
func TestMetarCommandFallsBackToTheRawReport(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, metarConfig())
	reg.fetch = func(url string) (string, error) {
		return "KDEN RMK AO2 SOMETHING UNUSUAL", nil
	}
	lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock)
	if len(lines) != 1 || !strings.Contains(lines[0], "(raw):") {
		t.Fatalf("metar with an undecodable report = %v, want the raw fallback", lines)
	}
}

// TestMetarCommandReportsItsConfiguration asserts the command explains its
// configuration state rather than guessing.
func TestMetarCommandReportsItsConfiguration(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	if lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock); len(lines) != 1 ||
		lines[0] != metarNotConfiguredLine {
		t.Errorf("metar with no provider = %v, want %q", lines, metarNotConfiguredLine)
	}

	reg, session, _ = commandFixture(t, metarConfig())
	if lines := runLinesAt(t, reg, session, "metar", spacewxClock); len(lines) != 1 ||
		!strings.Contains(lines[0], "Usage: "+metarUsage) {
		t.Errorf("metar with no station = %v, want the usage line", lines)
	}
	if lines := runLinesAt(t, reg, session, "metar KDENX", spacewxClock); len(lines) != 1 ||
		lines[0] != metarPlaceRejectedLine {
		t.Errorf("metar with a bad station = %v, want the station line", lines)
	}

	reg.fetch = func(url string) (string, error) { return "", errors.New("no route to host") }
	if lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock); len(lines) != 1 ||
		lines[0] != metarFailedLine {
		t.Errorf("metar with a failing provider = %v, want %q", lines, metarFailedLine)
	}
}
