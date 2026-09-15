// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// launchBody is a trimmed Launch Library 2 answer with two launches, in the shape
// the live provider sends (verified against 2.3.0: count + results[], with net,
// status.abbrev, launch_service_provider.name and pad.name).
const launchBody = `{
  "count": 2,
  "results": [
    {
      "name": "Vega-C | Sentinel-3C & FLEX",
      "net": "2026-09-15T01:21:00Z",
      "status": {"abbrev": "Go"},
      "launch_service_provider": {"name": "Arianespace"},
      "pad": {"name": "Kourou ELV"}
    },
    {
      "name": "Falcon 9 | Starlink Group 12-3",
      "net": "2026-09-14T22:40:00Z",
      "status": {"abbrev": "TBD"},
      "launch_service_provider": {"name": "SpaceX"},
      "pad": {"name": "SLC-40"}
    }
  ]
}`

// launchProbe records what the launch commands asked the provider for.
type launchProbe struct {
	calls int
	urls  []string
}

// last is the URL of the most recent fetch.
func (p *launchProbe) last() string {
	if len(p.urls) == 0 {
		return ""
	}
	return p.urls[len(p.urls)-1]
}

// launchPastBody is a previous-window answer: two launches that really have flown,
// newest first, so the "ago" wording and the ordering of a past list are both
// exercised.
const launchPastBody = `{
  "count": 2,
  "results": [
    {
      "name": "Vega-C | Sentinel-3C & FLEX",
      "net": "2026-09-14T20:21:00Z",
      "status": {"abbrev": "Go"},
      "launch_service_provider": {"name": "Arianespace"},
      "pad": {"name": "Kourou ELV"}
    },
    {
      "name": "Falcon 9 | Starlink Group 12-3",
      "net": "2026-09-14T19:00:00Z",
      "status": {"abbrev": "Success"},
      "launch_service_provider": {"name": "SpaceX"},
      "pad": {"name": "SLC-40"}
    }
  ]
}`

// launchesFixture builds a registry whose provider returns body, and records the
// fetches it served.
func launchesFixture(t *testing.T, cfg *BotConfig, body string) (*registry, *hubSession, *launchProbe) {
	t.Helper()
	if cfg == nil {
		cfg = defaultTestConfig()
	}
	cfg.LaunchURL = "https://ll.example.invalid/2.3.0/launches/{mode}/?limit={limit}&mode=list"
	reg, session, _ := commandFixture(t, cfg)
	probe := &launchProbe{}
	reg.fetch = func(url string) (string, error) {
		probe.calls++
		probe.urls = append(probe.urls, url)
		return body, nil
	}
	return reg, session, probe
}

// runLaunchLine runs one command line at the fixed base clock.
func runLaunchLine(t *testing.T, reg *registry, session *hubSession, line string) []string {
	t.Helper()
	return reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot "+line, peerHashFor(0x11)),
		Room:    "general",
		Command: line,
		Nick:    "gorrcbot",
		Now:     catchupBase,
	})
}

// TestLaunchesReportsUpcomingLaunches asserts the default window lists the next
// three launches, soonest first, with the countdown, vehicle, status, provider and
// pad on one line each.
func TestLaunchesReportsUpcomingLaunches(t *testing.T) {
	t.Parallel()

	reg, session, probe := launchesFixture(t, nil, launchBody)
	lines := runLaunchLine(t, reg, session, "launches")

	// The provider's order is not trusted, and this window is its weakest point:
	// it keeps a launch that has already flown in "upcoming" until it updates the
	// window. So the launch still ahead leads, and the one that flew 20 minutes
	// ago follows it with its age, rather than the reader being shown the past
	// first in an answer about what is next.
	assertLines(t, lines, []string{
		"upcoming launches (2):",
		"2026-09-15 01:21Z in 2h21m | Vega-C | Sentinel-3C & FLEX | Go | Arianespace @ Kourou ELV",
		"2026-09-14 22:40Z 20m ago | Falcon 9 | Starlink Group 12-3 | TBD | SpaceX @ SLC-40",
	})
	if probe.calls != 1 {
		t.Errorf("fetches = %v, want 1", probe.calls)
	}
	if got := probe.last(); !strings.Contains(got, "/launches/upcoming/") || !strings.Contains(got, "limit=3") {
		t.Errorf("fetched %q, want the upcoming window with limit=3", got)
	}
}

// TestLaunchesHonoursModeAndCount asserts "past" asks for the previous window, an
// explicit count is passed to the provider and respected, and the countdown reads
// as an age for a past launch.
func TestLaunchesHonoursModeAndCount(t *testing.T) {
	t.Parallel()

	reg, session, probe := launchesFixture(t, nil, launchPastBody)
	lines := runLaunchLine(t, reg, session, "launches past 1")
	assertLines(t, lines, []string{
		"past launches (1):",
		"2026-09-14 20:21Z 2h39m ago | Vega-C | Sentinel-3C & FLEX | Go | Arianespace @ Kourou ELV",
	})
	if got := probe.last(); !strings.Contains(got, "/launches/previous/") || !strings.Contains(got, "limit=1") {
		t.Errorf("fetched %q, want the previous window with limit=1", got)
	}
	// The most recent past launch comes first, and the count is passed through.
	assertLines(t, runLaunchLine(t, reg, session, "launches past 2"), []string{
		"past launches (2):",
		"2026-09-14 20:21Z 2h39m ago | Vega-C | Sentinel-3C & FLEX | Go | Arianespace @ Kourou ELV",
		"2026-09-14 19:00Z 4h ago | Falcon 9 | Starlink Group 12-3 | Success | SpaceX @ SLC-40",
	})
	if got := probe.last(); !strings.Contains(got, "/launches/previous/") || !strings.Contains(got, "limit=2") {
		t.Errorf("fetched %q, want the previous window with limit=2", got)
	}
}

// TestLaunchesCachesPerWindow asserts two asks inside the cache window cost one
// fetch, because the provider allows only 15 anonymous calls per hour per IP.
func TestLaunchesCachesPerWindow(t *testing.T) {
	t.Parallel()

	reg, session, probe := launchesFixture(t, nil, launchBody)
	runLaunchLine(t, reg, session, "launches")
	runLaunchLine(t, reg, session, "launches")
	if probe.calls != 1 {
		t.Errorf("fetches for two identical asks = %v, want 1", probe.calls)
	}
	// A different window is a different answer, so it is fetched.
	runLaunchLine(t, reg, session, "launches past")
	if probe.calls != 2 {
		t.Errorf("fetches after asking for the past window = %v, want 2", probe.calls)
	}
}

// TestLaunchesRejectsUnusableArguments asserts every bad argument gets the usage
// line, and that a rejected argument never reaches the provider.
func TestLaunchesRejectsUnusableArguments(t *testing.T) {
	t.Parallel()

	reg, session, probe := launchesFixture(t, nil, launchBody)
	for _, line := range []string{"launches soon", "launches upcoming 99", "launches past 0", "launches upcoming 2 extra"} {
		assertLines(t, runLaunchLine(t, reg, session, line), []string{"Usage: " + launchesUsage})
	}
	if probe.calls != 0 {
		t.Errorf("fetches for rejected arguments = %v, want 0", probe.calls)
	}
}

// TestLaunchesAnswersAProviderFailureHonestly asserts a failure or an empty
// window is reported as such, never as an empty list.
func TestLaunchesAnswersAProviderFailureHonestly(t *testing.T) {
	t.Parallel()

	reg, session, _ := launchesFixture(t, nil, launchBody)
	reg.fetch = func(string) (string, error) { return "", errors.New("connection refused") }
	assertLines(t, runLaunchLine(t, reg, session, "launches"), []string{"launches: provider unreachable, try again later"})

	empty, emptySession, _ := launchesFixture(t, nil, `{"count": 0, "results": []}`)
	assertLines(t, runLaunchLine(t, empty, emptySession, "launches"), []string{"no launches reported for that window"})

	broken, brokenSession, _ := launchesFixture(t, nil, `{"count": 1, "results": [`)
	assertLines(t, runLaunchLine(t, broken, brokenSession, "launches"), []string{"launches: could not read the provider's answer"})
}

// TestLaunchesWithoutAProviderSaysSo asserts the commands are disabled honestly
// when no launch_url is configured.
func TestLaunchesWithoutAProviderSaysSo(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.LaunchURL = ""
	reg, session, fake := commandFixture(t, cfg)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), testAskerNick)

	assertLines(t, runLaunchLine(t, reg, session, "launches"), []string{launchesNotConfiguredLine})
	assertLines(t, runLaunchLine(t, reg, session, "launches past"), []string{launchesNotConfiguredLine})
}

// TestLaunchesStripsHostileProviderText asserts a provider answer is sanitized,
// shortened and truncated before it is posted: the body is somebody else's text.
func TestLaunchesStripsHostileProviderText(t *testing.T) {
	t.Parallel()

	// The body is marshalled so the control bytes are real, as a hostile provider
	// would send them.
	raw, err := json.Marshal(map[string]any{
		"count": 1,
		"results": []any{map[string]any{
			"name":                    "Vega\x1b[31m-C\x1b]0;pwned\x07",
			"net":                     "2026-09-15T01:21:00Z",
			"status":                  map[string]any{"abbrev": "Go\x07"},
			"launch_service_provider": map[string]any{"name": strings.Repeat("P", 200)},
			"pad":                     map[string]any{"name": "Kourou ELV"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _ := launchesFixture(t, nil, string(raw))

	lines := runLaunchLine(t, reg, session, "launches")
	for _, line := range lines {
		if strings.ContainsAny(line, "\x1b\x07") {
			t.Errorf("line %q carries a terminal escape", line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want a header and one launch", lines)
	}
	if !strings.Contains(lines[1], "Vega-C") {
		t.Errorf("line = %q, want the escape-stripped vehicle name", lines[1])
	}
	if !strings.Contains(lines[1], "…") {
		t.Errorf("line = %q, want an over-long provider field truncated with a marker", lines[1])
	}
}

// TestLaunchLinesSurviveAZeroBudget asserts a reply budget of zero cannot slice
// the list backwards: the header is still the answer, and nothing panics.
func TestLaunchLinesSurviveAZeroBudget(t *testing.T) {
	t.Parallel()

	launches := []launch{{at: catchupBase.Add(time.Hour), name: "Vega-C"}}
	assertLines(t, launchLines(launchRequest{mode: "upcoming", label: "upcoming", count: 3},
		launches, catchupBase, 0), []string{"upcoming launches (1):", "… 1 not shown"})
	assertLines(t, launchLines(launchRequest{mode: "upcoming", label: "upcoming", count: 3},
		launches, catchupBase, -5), []string{"upcoming launches (1):", "… 1 not shown"})
}

// TestLaunchesIsRegistered asserts the command is in the table, so help lists it
// and the dispatcher can reach it.
func TestLaunchesIsRegistered(t *testing.T) {
	t.Parallel()

	reg, session, _ := launchesFixture(t, nil, launchBody)
	cmd, ok := reg.byName["launches"]
	if !ok {
		t.Fatalf("launches is not registered; names = %v", reg.names())
	}
	if cmd.usage != launchesUsage {
		t.Errorf("usage = %q, want %q", cmd.usage, launchesUsage)
	}
	assertLines(t, reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot help launches", peerHashFor(0x11)),
		Room:    "general",
		Command: "help launches",
		Nick:    "gorrcbot",
		Now:     catchupBase,
	}), []string{
		"launches — " + cmd.summary + ". Usage: " + launchesUsage,
	})
}

// TestJSONFieldReadsTheShapesAProviderUses asserts the tiny field reader the
// launch parser relies on: dotted paths, array indexes, first-element and
// wildcard collection, missing fields, and a type that is not a string.
func TestJSONFieldReadsTheShapesAProviderUses(t *testing.T) {
	t.Parallel()

	body := map[string]any{
		"count": float64(2),
		"results": []any{
			map[string]any{"name": "first", "status": map[string]any{"abbrev": "Go"}},
			map[string]any{"name": "second"},
		},
		"flag": true,
	}
	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{name: "top-level string", path: "count", want: "2"},
		{name: "dotted path", path: "results[0].name", want: "first"},
		{name: "second element", path: "results[1].name", want: "second"},
		{name: "nested dotted path", path: "results[0].status.abbrev", want: "Go"},
		{name: "first element", path: "results[].name", want: "first"},
		{name: "missing element", path: "results[9].name", want: ""},
		{name: "missing key", path: "results[0].nope", want: ""},
		{name: "boolean renders as text", path: "flag", want: "true"},
		{name: "an object is not a field", path: "results[0].status", want: ""},
		{name: "empty path", path: "", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := jsonField(body, tt.path); got != tt.want {
				t.Errorf("jsonField(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestParseLaunchTimeAcceptsTheProviderFormats asserts the RFC3339 timestamps the
// provider sends parse, and anything else is reported as unparsed rather than as
// an epoch date.
func TestParseLaunchTimeAcceptsTheProviderFormats(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{in: "2026-09-15T01:21:00Z", want: time.Date(2026, 9, 15, 1, 21, 0, 0, time.UTC), ok: true},
		{in: "2026-09-15T01:21:00.000Z", want: time.Date(2026, 9, 15, 1, 21, 0, 0, time.UTC), ok: true},
		{in: "2026-09-15T01:21:00+00:00", want: time.Date(2026, 9, 15, 1, 21, 0, 0, time.UTC), ok: true},
		{in: ""},
		{in: "tomorrow"},
	} {
		got, ok := parseLaunchTime(tt.in)
		if ok != tt.ok || !got.Equal(tt.want) {
			t.Errorf("parseLaunchTime(%q) = %v, %v, want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// TestLaunchesUpcomingLeadsWithLaunchesStillAhead asserts the ordering rule the
// live provider made necessary: in the upcoming window the launches still ahead
// come first, soonest first, and the ones that have already flown follow, most
// recent first — a reader asking what is next is not asking what just went up.
func TestLaunchesUpcomingLeadsWithLaunchesStillAhead(t *testing.T) {
	t.Parallel()

	const body = `{
      "count": 4,
      "results": [
        {"name": "flew recently", "net": "2026-09-14T22:40:00Z", "status": {"abbrev": "Success"}},
        {"name": "flies later", "net": "2026-09-15T06:00:00Z", "status": {"abbrev": "Go"}},
        {"name": "flew long ago", "net": "2026-09-14T02:00:00Z", "status": {"abbrev": "Success"}},
        {"name": "flies soonest", "net": "2026-09-15T01:21:00Z", "status": {"abbrev": "Go"}}
      ]
    }`
	reg, session, _ := launchesFixture(t, nil, body)
	lines := runLaunchLine(t, reg, session, "launches upcoming 4")
	assertLines(t, lines, []string{
		"upcoming launches (4):",
		"2026-09-15 01:21Z in 2h21m | flies soonest | Go",
		"2026-09-15 06:00Z in 7h | flies later | Go",
		"2026-09-14 22:40Z 20m ago | flew recently | Success",
		"2026-09-14 02:00Z 21h ago | flew long ago | Success",
	})
}
