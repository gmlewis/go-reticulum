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

// wxalertConfig is a configuration with a severe-weather provider set.
func wxalertConfig() *BotConfig {
	cfg := defaultTestConfig()
	cfg.WeatherAlertURL = "https://api.weather.gov/alerts/active?area={place}"
	return cfg
}

// nwsAlertsBody is an answer shaped like the National Weather Service's active
// alerts API: a features array whose properties carry the CAP fields.
const nwsAlertsBody = `{"type":"FeatureCollection","features":[` +
	`{"properties":{"event":"Tornado Warning","severity":"Extreme","areaDesc":"Cleveland County, OK",` +
	`"ends":"2026-03-15T18:00:00-05:00"}},` +
	`{"properties":{"event":"Flash Flood Warning","severity":"Severe","areaDesc":"Pottawatomie County, OK",` +
	`"ends":"2026-03-15T20:00:00-05:00"}}]}`

// TestRenderAlertAnswerDecodesTheNWSShape asserts an NWS answer becomes one
// line per alert, most severe first.
func TestRenderAlertAnswerDecodesTheNWSShape(t *testing.T) {
	t.Parallel()

	lines, err := renderAlertAnswer(nwsAlertsBody, "OK")
	if err != nil {
		t.Fatalf("renderAlertAnswer: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("renderAlertAnswer = %v, want a header and two alerts", lines)
	}
	if lines[0] != "Active weather alerts for OK: 2 alerts" {
		t.Errorf("header = %q", lines[0])
	}
	want := "[Extreme] Tornado Warning | Cleveland County, OK | until 2026-03-15T18:00:00-05:00"
	if lines[1] != want {
		t.Errorf("first alert =\n  %v\nwant\n  %v", lines[1], want)
	}
	if !strings.Contains(lines[2], "[Severe] Flash Flood Warning") {
		t.Errorf("second alert = %q, want the less severe one second", lines[2])
	}
}

// TestRenderAlertAnswerReportsNoActiveWarnings asserts an empty collection is
// reported as such rather than as a failure.
func TestRenderAlertAnswerReportsNoActiveWarnings(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{"features":[]}`, `[]`, `{"@graph":[]}`, `{"alerts":[]}`} {
		lines, err := renderAlertAnswer(body, "OK")
		if err != nil {
			t.Errorf("renderAlertAnswer(%q): %v", body, err)
			continue
		}
		if len(lines) != 1 || lines[0] != "No active weather alerts for OK." {
			t.Errorf("renderAlertAnswer(%q) = %v, want the no-alerts line", body, lines)
		}
	}
}

// TestRenderAlertAnswerFallsBackToText asserts a provider that answers with a
// headline rather than JSON still produces a usable line.
func TestRenderAlertAnswerFallsBackToText(t *testing.T) {
	t.Parallel()

	lines, err := renderAlertAnswer("Tornado Warning in effect until 18:00 CST", "OK")
	if err != nil {
		t.Fatalf("renderAlertAnswer: %v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "Tornado Warning in effect") {
		t.Errorf("renderAlertAnswer = %v, want the provider text", lines)
	}
	// An HTML error page is a broken provider, not an alert.
	if _, err := renderAlertAnswer("<html><body>Service unavailable</body></html>", "OK"); err == nil {
		t.Error("an HTML error page was treated as a warning")
	}
}

// TestRenderAlertAnswerRejectsAnEmptyBody asserts nothing usable is an error
// rather than an empty reply.
func TestRenderAlertAnswerRejectsAnEmptyBody(t *testing.T) {
	t.Parallel()

	if _, err := renderAlertAnswer("   ", "OK"); err == nil {
		t.Error("an empty body was accepted")
	}
}

// TestWxalertCommandListsWarnings asserts the command fetches through the
// injected provider and answers with the decoded alerts.
func TestWxalertCommandListsWarnings(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, wxalertConfig())
	fetches := 0
	reg.fetch = func(url string) (string, error) {
		fetches++
		if !strings.Contains(url, "area=OK") {
			t.Errorf("the provider was asked for %q, want the requested area", url)
		}
		return nwsAlertsBody, nil
	}

	lines := runLinesAt(t, reg, session, "wxalert OK", spacewxClock)
	if len(lines) != 3 || !strings.Contains(lines[0], "Active weather alerts for OK: 2 alerts") {
		t.Fatalf("wxalert = %v, want a header and two alerts", lines)
	}
	// The answer is reused well inside the fifteen-minute window.
	runLinesAt(t, reg, session, "wxalert OK", spacewxClock.Add(10*time.Minute))
	if fetches != 1 {
		t.Errorf("fetches = %v, want the answer reused from the cache", fetches)
	}
	// And refreshed once it expires.
	runLinesAt(t, reg, session, "wxalert OK", spacewxClock.Add(20*time.Minute))
	if fetches != 2 {
		t.Errorf("fetches = %v after the cache expired, want 2", fetches)
	}
}

// TestWxalertCommandReportsItsConfiguration asserts the command explains its
// configuration state rather than guessing.
func TestWxalertCommandReportsItsConfiguration(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	if lines := runLinesAt(t, reg, session, "wxalert OK", spacewxClock); len(lines) != 1 ||
		lines[0] != wxalertNotConfiguredLine {
		t.Errorf("wxalert with no provider = %v, want %q", lines, wxalertNotConfiguredLine)
	}

	reg, session, _ = commandFixture(t, wxalertConfig())
	if lines := runLinesAt(t, reg, session, "wxalert", spacewxClock); len(lines) != 1 ||
		!strings.Contains(lines[0], "Usage: "+wxalertUsage) {
		t.Errorf("wxalert with no place = %v, want the usage line", lines)
	}
	if lines := runLinesAt(t, reg, session, "wxalert <script>", spacewxClock); len(lines) != 1 ||
		lines[0] != wxalertPlaceRejectedLine {
		t.Errorf("wxalert with a rejected place = %v, want the place line", lines)
	}

	reg.fetch = func(url string) (string, error) { return "", errors.New("no route to host") }
	if lines := runLinesAt(t, reg, session, "wxalert OK", spacewxClock); len(lines) != 1 ||
		lines[0] != wxalertFailedLine {
		t.Errorf("wxalert with a failing provider = %v, want %q", lines, wxalertFailedLine)
	}
}

// TestWxAlertLinesAreBounded asserts a continent-wide feed cannot produce an
// unbounded reply, which the line budget would truncate mid-alert.
func TestWxAlertLinesAreBounded(t *testing.T) {
	t.Parallel()

	alerts := make([]wxAlert, 0, maxWxAlertRows*2)
	for i := range maxWxAlertRows * 2 {
		alerts = append(alerts, wxAlert{Event: "Warning", Severity: "Moderate", Area: string(rune('A' + i))})
	}
	lines := wxAlertLines("OK", alerts)
	if len(lines) != maxWxAlertRows+1 {
		t.Errorf("rendered %v lines, want a header and %v alerts", len(lines), maxWxAlertRows)
	}
}

// TestWxSeverityRankOrdersTheCapScale asserts the severities sort most severe
// first, and that an unknown severity lands in the middle rather than first.
func TestWxSeverityRankOrdersTheCapScale(t *testing.T) {
	t.Parallel()

	ordered := []string{"Extreme", "Severe", "Moderate", "Minor", "Whatever", ""}
	for i := 1; i < len(ordered); i++ {
		if wxSeverityRank(ordered[i-1]) >= wxSeverityRank(ordered[i]) {
			t.Errorf("wxSeverityRank(%q) is not before wxSeverityRank(%q)",
				ordered[i-1], ordered[i])
		}
	}
}
