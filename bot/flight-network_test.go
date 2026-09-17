// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build network

// This file checks the flight command against the real providers, which is the one
// thing the offline tests cannot do: they prove the parsing, the wording and the
// degradation against captured answers, while this proves that the two keyless
// endpoints still answer in the shape that parsing expects, and that the default
// templates in the README are the ones that work.
//
// It is behind the "network" build tag, so no default test run touches the
// network: go test -tags network ./cmd/gorrcbot/.

package bot

import (
	"os"
	"strings"
	"testing"
	"time"
)

// flightLiveTemplates are the documented defaults, so this test fails if the
// README's advice stops working.
const (
	flightLiveTemplate  = "https://api.adsb.lol/v2/callsign/{flight}"
	flightRouteTemplate = "https://api.adsbdb.com/v0/callsign/{flight}"
)

// TestFlightAgainstTheRealProviders asks the real providers about a flight number
// that has been published for years, and asserts the bot turns whatever they
// answer into a reply that names the flight and says something true about it.
func TestFlightAgainstTheRealProviders(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.FlightURL = flightLiveTemplate
	cfg.FlightRouteURL = flightRouteTemplate
	reg, session, _ := commandFixture(t, cfg)

	lines := runFlightLine(t, reg, session, time.Now(), "flight BA123")
	t.Logf("answer to flight BA123: %q", lines)
	if len(lines) == 0 {
		t.Fatal("the flight command answered nothing")
	}
	joined := strings.Join(lines, " ")

	// The route provider resolves both spellings of the number, so the answer has
	// to name the airline and the callsign the live feed uses.
	if !strings.Contains(joined, "British Airways") {
		t.Errorf("answer = %q, want the airline the route provider reports", joined)
	}
	if !strings.Contains(joined, "BAW123") {
		t.Errorf("answer = %q, want the radio callsign BAW123 that BA123 resolves to", joined)
	}
	// Either the aircraft is in the air and its position is reported with its
	// age, or the answer says plainly that nothing is transmitting the callsign.
	// Both are correct; an empty or evasive answer is not.
	switch {
	case strings.Contains(joined, "position heard"):
		if !strings.Contains(joined, "squawk") {
			t.Errorf("answer = %q, want a live state with its age and squawk", joined)
		}
	case strings.Contains(joined, flightNoAircraftSnippet):
	default:
		t.Errorf("answer = %q, want either a live state or the honest no-aircraft line", joined)
	}
}

// flightNoAircraftSnippet is the part of the no-aircraft line this test looks for,
// spelled once so the assertion and the command cannot drift apart silently.
const flightNoAircraftSnippet = "no aircraft is transmitting"

// TestFlightAgainstTheRealProvidersRefusesAnUnknownNumber asserts the real route
// provider's "unknown callsign" answer (a 404) is reported as an unknown number
// rather than as a provider failure, which is the difference between "check the
// number" and "try again later".
func TestFlightAgainstTheRealProvidersRefusesAnUnknownNumber(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.FlightURL = flightLiveTemplate
	cfg.FlightRouteURL = flightRouteTemplate
	reg, session, _ := commandFixture(t, cfg)

	lines := runFlightLine(t, reg, session, time.Now(), "flight ZZ9999")
	t.Logf("answer to flight ZZ9999: %q", lines)
	assertLines(t, lines, []string{"no flight number ZZ9999 is published: check the number and try again"})
}

// TestFlightAgainstAChosenLiveCallsign renders one real flight that is in the air
// right now, which is the only way to check the live-state line against real data
// rather than a captured answer. It is skipped unless FLIGHT_TEST_CALLSIGN names
// one, and the callsign is looked up as typed, so it also covers the path where no
// route is published for a callsign the live feed does know.
//
//	FLIGHT_TEST_CALLSIGN=ACA840 go test -tags network -run TestFlightAgainstAChosenLiveCallsign ./cmd/gorrcbot/
func TestFlightAgainstAChosenLiveCallsign(t *testing.T) {
	callsign := strings.TrimSpace(os.Getenv("FLIGHT_TEST_CALLSIGN"))
	if callsign == "" {
		t.Skip("set FLIGHT_TEST_CALLSIGN to a flight that is in the air")
	}
	cfg := defaultTestConfig()
	cfg.FlightURL = flightLiveTemplate
	cfg.FlightRouteURL = flightRouteTemplate
	reg, session, _ := commandFixture(t, cfg)

	lines := runFlightLine(t, reg, session, time.Now(), "flight "+callsign)
	t.Logf("answer to flight %v: %q", callsign, lines)
	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "position heard") {
		t.Fatalf("answer = %q, want a live position for a flight that is in the air", joined)
	}
	if !strings.Contains(joined, "squawk") {
		t.Errorf("answer = %q, want the transponder line", joined)
	}
}
