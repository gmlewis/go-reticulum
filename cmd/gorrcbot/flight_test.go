// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The bodies below are trimmed copies of what the two providers really answer,
// captured live: adsbdb's route object for BAW123, its "invalid callsign" answer
// for a number it does not publish, and adsb.lol's live state for an aircraft
// that is in the air. Every test that parses a provider answer parses one of
// these, so the shapes stay the provider's and not the test's invention.
const (
	flightRouteBody = `{"response":{"flightroute":{"callsign":"BAW123","callsign_icao":"BAW123",
      "callsign_iata":"BA123","airline":{"name":"British Airways","icao":"BAW","iata":"BA","country":"United Kingdom"},
      "origin":{"iata_code":"LHR","icao_code":"EGLL","municipality":"London","name":"London Heathrow Airport"},
      "destination":{"iata_code":"DOH","icao_code":"OTHH","municipality":"Doha","name":"Hamad International Airport"}}}}`

	flightRouteUnknownBody = `{"response":"invalid callsign: BA999"}`

	flightRouteEmptyBody = `{"response":{}}`

	flightLiveBody = `{"ac":[{"flight":"BAW123  ","hex":"400a1b","t":"A333","alt_baro":39000,
      "baro_rate":0,"gs":543.3,"track":79.39,"lat":50.618296,"lon":-3.673145,"squawk":"1142",
      "seen_pos":0.23,"category":"A3","messages":8123}],"msg":"No error","total":1}`

	flightLiveEmptyBody = `{"ac":[],"msg":"No error","total":0}`
)

// flightAnswer is one canned provider answer: the body to serve, or the error to
// fail with. A URL with no answer configured fails, which is how a provider that
// cannot be reached is simulated.
type flightAnswer struct {
	body string
	err  error
}

// flightAnswers maps a fragment of a provider URL onto the answer for it, so one
// fixture can serve the route provider and the live provider differently.
type flightAnswers map[string]flightAnswer

// flightProbe records what the flight command asked the providers for.
type flightProbe struct {
	calls int
	urls  []string
}

// urlsContaining reports whether any fetched URL contains fragment.
func (p *flightProbe) urlsContaining(fragment string) []string {
	var out []string
	for _, url := range p.urls {
		if strings.Contains(url, fragment) {
			out = append(out, url)
		}
	}
	return out
}

// flightFixture builds a registry whose two providers serve the given answers. A
// nil cfg gets both templates configured; a caller-supplied cfg is used exactly as
// it is, so a test can leave one provider out or leave both out.
func flightFixture(t *testing.T, cfg *BotConfig, answers flightAnswers) (*registry, *hubSession, *flightProbe) {
	t.Helper()
	if cfg == nil {
		cfg = defaultTestConfig()
		cfg.FlightURL = "https://api.adsb.invalid/v2/callsign/{flight}"
		cfg.FlightRouteURL = "https://api.adsbdb.invalid/v0/callsign/{flight}"
	}
	reg, session, _ := commandFixture(t, cfg)
	probe := &flightProbe{}
	reg.fetch = func(url string) (string, error) {
		probe.calls++
		probe.urls = append(probe.urls, url)
		for fragment, answer := range answers {
			if strings.Contains(url, fragment) {
				return answer.body, answer.err
			}
		}
		return "", errors.New("no provider is configured at that address")
	}
	return reg, session, probe
}

// runFlightLine runs one flight command at the given clock.
func runFlightLine(t *testing.T, reg *registry, session *hubSession, now time.Time, line string) []string {
	t.Helper()
	return reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot "+line, peerHashFor(0x11)),
		Room:    "general",
		Command: line,
		Nick:    "gorrcbot",
		Now:     now,
	})
}

// flightHappyLines is the answer for a flight that is in the air, with its route
// published: the route first, then where the aircraft is, then its transponder.
var flightHappyLines = []string{
	"British Airways BA123 (BAW123): London (LHR) → Doha (DOH)",
	"BAW123: 39000 ft | level | 543 kt | track 079° | 50.618N 3.673W | A333",
	"squawk 1142 | position heard 0s ago",
}

// TestFlightReportsRouteAndLiveState asserts the whole answer for a flight in the
// air: the route a passenger recognizes, where the aircraft actually is, and how
// old that position is.
func TestFlightReportsRouteAndLiveState(t *testing.T) {
	t.Parallel()

	reg, session, probe := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid":   {body: flightLiveBody},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), flightHappyLines)

	// The number a passenger knows must reach the live feed as the callsign the
	// radio carries, which is the reason the route is looked up first.
	if got := probe.urlsContaining("BAW123"); len(got) != 1 {
		t.Errorf("live provider URLs naming BAW123 = %v, want exactly one", got)
	}
	if got := probe.urlsContaining("/callsign/BA123"); len(got) != 1 {
		t.Errorf("route provider URLs naming BA123 = %v, want exactly one", got)
	}
	if probe.calls != 2 {
		t.Errorf("provider calls = %v, want one per provider", probe.calls)
	}
}

// TestFlightNormalizesTheNumberTyped asserts the ways a person writes a flight
// number all reach the providers the same way: the spaces and hyphens go, the
// letters are upper-cased, and the route still resolves.
func TestFlightNormalizesTheNumberTyped(t *testing.T) {
	t.Parallel()

	for _, typed := range []string{"flight ba123", "flight BA 123", "flight ba-123", "flight BA123"} {
		t.Run(typed, func(t *testing.T) {
			t.Parallel()
			reg, session, probe := flightFixture(t, nil, flightAnswers{
				"adsbdb.invalid": {body: flightRouteBody},
				"adsb.invalid":   {body: flightLiveBody},
			})
			assertLines(t, runFlightLine(t, reg, session, catchupBase, typed), flightHappyLines)
			if got := probe.urlsContaining("/callsign/BA123"); len(got) != 1 {
				t.Errorf("route provider URLs for %q = %v, want the normalized BA123", typed, got)
			}
		})
	}
}

// TestFlightReportsLiveStateWithoutARouteProvider asserts the live state stands on
// its own when the operator configured no route provider: the answer is still
// self-identifying, because the state line carries the callsign.
func TestFlightReportsLiveStateWithoutARouteProvider(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.FlightURL = "https://api.adsb.invalid/v2/callsign/{flight}"
	reg, session, probe := flightFixture(t, cfg, flightAnswers{
		"adsb.invalid": {body: flightLiveBody},
	})

	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), flightHappyLines[1:])
	if probe.calls != 1 {
		t.Errorf("provider calls = %v, want the live provider alone", probe.calls)
	}
	if got := probe.urlsContaining("/callsign/BA123"); len(got) != 1 {
		t.Errorf("live provider URLs = %v, want the typed number as the callsign", got)
	}
}

// TestFlightSaysWhenNothingIsInTheAir asserts an empty aircraft list is reported
// as the answer it is, with the route still shown: a flight that is between legs
// is not a failure, and the reader still wants to know where it should be.
func TestFlightSaysWhenNothingIsInTheAir(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid":   {body: flightLiveEmptyBody},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
		flightHappyLines[0],
		"no aircraft is transmitting BAW123 right now: it may be between flights, or beyond receiver range",
	})
}

// TestFlightRefusesANumberNoProviderPublishes asserts a number that no route
// provider publishes and no aircraft is transmitting is answered as an unknown
// number, which is a statement about the number and not about the network.
func TestFlightRefusesANumberNoProviderPublishes(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteUnknownBody},
		"adsb.invalid":   {body: flightLiveEmptyBody},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA999"), []string{
		"no flight number BA999 is published: check the number and try again",
	})
}

// TestFlightReportsAnUnpublishedRouteForAnAircraftThatIsFlying asserts the two
// providers are not allowed to veto each other: a route database that has never
// heard of a charter flight must not hide the aircraft that is transmitting it.
func TestFlightReportsAnUnpublishedRouteForAnAircraftThatIsFlying(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteEmptyBody},
		"adsb.invalid":   {body: flightLiveBody},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
		flightHappyLines[1],
		flightHappyLines[2],
		"note: no route is published for this number",
	})
}

// TestFlightDegradesOneProviderAtATime asserts each provider can fail on its own
// without taking the other's answer with it, and that both failing is said
// plainly instead of being dressed up as an answer about the flight.
func TestFlightDegradesOneProviderAtATime(t *testing.T) {
	t.Parallel()

	t.Run("the route provider is down", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {err: errors.New("connection refused")},
			"adsb.invalid":   {body: flightLiveBody},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[1],
			flightHappyLines[2],
			"note: the route provider did not answer",
		})
	})

	t.Run("the route provider says the number is unknown", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {err: &providerStatusError{code: http.StatusNotFound, status: "404 Not Found"}},
			"adsb.invalid":   {body: flightLiveEmptyBody},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA999"), []string{
			"no flight number BA999 is published: check the number and try again",
		})
	})

	t.Run("the route provider answers a status", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {err: &providerStatusError{code: 500, status: "500 Internal Server Error"}},
			"adsb.invalid":   {body: flightLiveBody},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[1],
			flightHappyLines[2],
			"note: the route provider did not answer",
		})
	})

	t.Run("the route provider answers something unreadable", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {body: "<html>maintenance</html>"},
			"adsb.invalid":   {body: flightLiveBody},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[1],
			flightHappyLines[2],
			"note: the route provider's answer could not be read",
		})
	})

	t.Run("the live provider is down", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {body: flightRouteBody},
			"adsb.invalid":   {err: errors.New("connection refused")},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[0],
			"note: the live provider did not answer",
		})
	})

	t.Run("the live provider answers something unreadable", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {body: flightRouteBody},
			"adsb.invalid":   {body: "not json at all"},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[0],
			"note: the live provider's answer could not be read",
		})
	})

	t.Run("the live provider answers a status", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {body: flightRouteBody},
			"adsb.invalid":   {err: &providerStatusError{code: 429, status: "429 Too Many Requests"}},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[0],
			"note: the live provider did not answer",
		})
	})

	t.Run("both providers are down", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"),
			[]string{flightFailedLine})
	})
}

// TestFlightNeverLeaksAProviderURL asserts a configured template never reaches a
// room. A keyed provider is a legitimate configuration, so a template may carry a
// key, and the failure paths are exactly where it would escape.
func TestFlightNeverLeaksAProviderURL(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.FlightURL = "https://api.adsb.invalid/v2/callsign/{flight}?key=SECRETKEY123"
	cfg.FlightRouteURL = "https://api.adsbdb.invalid/v0/callsign/{flight}?key=SECRETKEY456"
	reg, session, _ := flightFixture(t, cfg, flightAnswers{})

	joined := strings.Join(runFlightLine(t, reg, session, catchupBase, "flight BA123"), " ")
	for _, secret := range []string{"SECRETKEY123", "SECRETKEY456", "example.invalid", "adsb.invalid"} {
		if strings.Contains(joined, secret) {
			t.Errorf("the answer %q repeats %q, which may carry a private host or a key", joined, secret)
		}
	}
}

// TestFlightRefusesAnythingThatIsNotAFlightNumber asserts the argument is
// validated before any URL exists, and that a refusal never repeats the text that
// caused it.
func TestFlightRefusesAnythingThatIsNotAFlightNumber(t *testing.T) {
	t.Parallel()

	for _, args := range []string{
		"../../etc/passwd",
		"BA123?x=1",
		"BA123&key=1",
		"BA 123/../..",
		"BA123%00",
		"BA",
		"123",
		"ABCDEFGHI",
		strings.Repeat("9", 200),
		"BA123\nBA456",
		"\x1b[2JBA123",
	} {
		t.Run(args, func(t *testing.T) {
			t.Parallel()
			reg, session, probe := flightFixture(t, nil, flightAnswers{
				"adsbdb.invalid": {body: flightRouteBody},
				"adsb.invalid":   {body: flightLiveBody},
			})
			lines := runFlightLine(t, reg, session, catchupBase, "flight "+args)
			assertLines(t, lines, []string{flightRejectedLine})
			if probe.calls != 0 {
				t.Errorf("provider calls = %v, want none for a rejected argument", probe.calls)
			}
			if joined := strings.Join(lines, " "); strings.Contains(joined, "etc") ||
				strings.Contains(joined, "passwd") {
				t.Errorf("the refusal %q repeats the argument it refused", joined)
			}
		})
	}
}

// TestFlightAnswersUsageAndConfiguration asserts the three ways the command has
// nothing to do: no argument, no configured provider, and a template the bot
// refuses to use. None of them reaches the network.
func TestFlightAnswersUsageAndConfiguration(t *testing.T) {
	t.Parallel()

	t.Run("no argument", func(t *testing.T) {
		t.Parallel()
		reg, session, probe := flightFixture(t, nil, flightAnswers{})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight"), []string{"Usage: " + flightUsage})
		if probe.calls != 0 {
			t.Errorf("provider calls = %v, want none", probe.calls)
		}
	})

	t.Run("no provider is configured", func(t *testing.T) {
		t.Parallel()
		// No template at all: the command says so and never reaches a provider.
		cfg := defaultTestConfig()
		reg, session, probe := flightFixture(t, cfg, flightAnswers{})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"),
			[]string{flightNotConfiguredLine})
		if probe.calls != 0 {
			t.Errorf("provider calls = %v, want none", probe.calls)
		}
	})

	t.Run("the template carries no placeholder", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		cfg.FlightURL = "https://api.adsb.invalid/v2/callsign"
		reg, session, probe := flightFixture(t, cfg, flightAnswers{})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"),
			[]string{flightMisconfiguredLine})
		if probe.calls != 0 {
			t.Errorf("provider calls = %v, want none", probe.calls)
		}
	})

	t.Run("the route template carries no placeholder", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		cfg.FlightURL = "https://api.adsb.invalid/v2/callsign/{flight}"
		cfg.FlightRouteURL = "https://api.adsbdb.invalid/v0/callsign"
		reg, session, _ := flightFixture(t, cfg, flightAnswers{
			"adsb.invalid": {body: flightLiveBody},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[1],
			flightHappyLines[2],
			"note: the route provider is misconfigured; the operator must fix flight_route_url",
		})
	})
}

// TestFlightKeepsACachedPositionHonest asserts the cache never turns into a lie:
// the second ask inside the cache lifetime reaches no provider, and the age it
// reports has grown by exactly how long the answer was held.
func TestFlightKeepsACachedPositionHonest(t *testing.T) {
	t.Parallel()

	reg, session, probe := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid":   {body: flightLiveBody},
	})
	first := runFlightLine(t, reg, session, catchupBase, "flight BA123")
	assertLines(t, first, flightHappyLines)
	if probe.calls != 2 {
		t.Fatalf("provider calls = %v, want one per provider", probe.calls)
	}

	// Half a minute later the position is still the provider's, and the answer
	// must say so: a cached line that kept claiming "heard 0s ago" would tell the
	// reader the aircraft is somewhere it has already left.
	later := runFlightLine(t, reg, session, catchupBase.Add(30*time.Second), "flight BA123")
	assertLines(t, later, []string{
		flightHappyLines[0],
		flightHappyLines[1],
		"squawk 1142 | position heard 30s ago",
	})
	if probe.calls != 2 {
		t.Errorf("provider calls = %v, want the cached answers to be reused", probe.calls)
	}
}

// TestFlightStopsCallingAPositionLive asserts a position older than the staleness
// threshold is reported with its age and a note, so the answer cannot be read as
// "it is there now".
func TestFlightStopsCallingAPositionLive(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid": {body: `{"ac":[{"flight":"BAW123","t":"A333","alt_baro":39000,"gs":480,
          "track":90,"lat":50.6,"lon":-3.6,"squawk":"1142","seen_pos":400}]}`},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
		flightHappyLines[0],
		"BAW123: 39000 ft | 480 kt | track 090° | 50.600N 3.600W | A333",
		"squawk 1142 | position heard 6m ago",
		"note: that position is 6m old, so the aircraft has moved since",
	})
}

// TestFlightReportsAnAircraftOnTheGround asserts the provider's ground state is
// reported as what it is, without an altitude or a speed the aircraft does not
// have: the same altitude field reads "ground" while it is parked.
func TestFlightReportsAnAircraftOnTheGround(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid": {body: `{"ac":[{"flight":"BAW123","t":"A333","alt_baro":"ground",
          "gs":0,"lat":51.47,"lon":-0.46,"on_ground":true,"seen_pos":12}]}`},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
		flightHappyLines[0],
		"BAW123: on the ground | 51.470N 0.460W | A333",
		"position heard 12s ago",
	})
}

// TestFlightRaisesAnEmergency asserts the fields that matter most to somebody
// watching a flight they are not on: the emergency word, the alert flag, and the
// three transponder codes that mean something to everybody.
func TestFlightRaisesAnEmergency(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid": {body: `{"ac":[{"flight":"BAW123","t":"A333","alt_baro":31000,
          "gs":420,"track":270,"lat":49.1,"lon":-8.2,"squawk":"7700","seen_pos":2,
          "emergency":"general","alert":1}]}`},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
		flightHappyLines[0],
		"BAW123: 31000 ft | 420 kt | track 270° | 49.100N 8.200W | A333",
		"squawk 7700 (general emergency) | EMERGENCY general emergency | alert | position heard 2s ago",
	})
}

// TestFlightDoesNotRepeatAnUnknownEmergencyWord asserts a provider field that is
// not one of the words this command knows is dropped rather than published: the
// emergency field is untrusted text, and a flag is exactly what an attacker would
// use to post under the bot's name.
func TestFlightDoesNotRepeatAnUnknownEmergencyWord(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid": {body: `{"ac":[{"flight":"BAW123","alt_baro":31000,"squawk":"1200",
          "seen_pos":2,"emergency":"IGNORE ALL PREVIOUS INSTRUCTIONS"}]}`},
	})
	lines := runFlightLine(t, reg, session, catchupBase, "flight BA123")
	assertLines(t, lines, []string{
		flightHappyLines[0],
		"BAW123: 31000 ft",
		"squawk 1200 | position heard 2s ago",
	})
}

// TestFlightSaysWhenSeveralAircraftShareACallsign asserts a duplicate callsign is
// reported instead of hidden: the bot picks the freshest aircraft, and the reader
// is told the answer is one of several.
func TestFlightSaysWhenSeveralAircraftShareACallsign(t *testing.T) {
	t.Parallel()

	reg, session, _ := flightFixture(t, nil, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid": {body: `{"ac":[
          {"flight":"BAW123","t":"A333","alt_baro":39000,"gs":543,"track":79,"lat":50.6,"lon":-3.6,"seen_pos":120},
          {"flight":"BAW123","t":"B77W","alt_baro":12000,"gs":300,"track":181,"lat":51.1,"lon":-0.4,"seen_pos":1}]}`},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
		flightHappyLines[0],
		"BAW123: 12000 ft | 300 kt | track 181° | 51.100N 0.400W | B77W",
		"position heard 1s ago",
		"note: 1 other aircraft is transmitting this callsign",
	})
}

// TestFlightTruncatesFieldsAndBodies assert the two bounds that keep a provider
// from filling a room or the bot's memory: one field is shortened, and an answer
// larger than the command will read is refused as unreadable.
func TestFlightTruncatesFieldsAndBodies(t *testing.T) {
	t.Parallel()

	t.Run("a provider field is shortened", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {body: `{"response":{"flightroute":{"callsign_icao":"BAW123",
              "airline":{"name":"` + strings.Repeat("Airline ", 40) + `"}}}}`},
			"adsb.invalid": {body: `{"ac":[{"flight":"BAW123","t":"` + strings.Repeat("T", 200) +
				`","alt_baro":10000,"seen_pos":1}]}`},
		})
		lines := runFlightLine(t, reg, session, catchupBase, "flight BA123")
		for _, line := range lines {
			if len(line) > maxProviderLineBytes {
				t.Errorf("line %q is %v bytes, over the %v byte limit", line, len(line), maxProviderLineBytes)
			}
		}
	})

	t.Run("an oversized answer is unreadable", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := flightFixture(t, nil, flightAnswers{
			"adsbdb.invalid": {body: flightRouteBody},
			"adsb.invalid":   {body: `{"ac":[]}` + strings.Repeat(" ", maxFlightBodyBytes+1)},
		})
		assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
			flightHappyLines[0],
			"note: the live provider's answer could not be read",
		})
	})
}

// TestFlightRespectsTheReplyBudget asserts a small budget shortens the answer and
// says how many lines it dropped rather than dropping them silently.
func TestFlightRespectsTheReplyBudget(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.MaxReplyLines = 2
	cfg.FlightURL = "https://api.adsb.invalid/v2/callsign/{flight}"
	cfg.FlightRouteURL = "https://api.adsbdb.invalid/v0/callsign/{flight}"
	reg, session, _ := flightFixture(t, cfg, flightAnswers{
		"adsbdb.invalid": {body: flightRouteBody},
		"adsb.invalid":   {body: flightLiveBody},
	})
	assertLines(t, runFlightLine(t, reg, session, catchupBase, "flight BA123"), []string{
		flightHappyLines[0],
		"… 2 not shown",
	})
}
