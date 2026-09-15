// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

// TestColdWaterRuleIsTheDocumentedSequence asserts the no-argument answer is the
// one-minute, ten-minute, one-hour sequence, in that order, with the instruction
// each step carries.
func TestColdWaterRuleIsTheDocumentedSequence(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "coldwater")
	if len(lines) != 3 {
		t.Fatalf("coldwater = %v, want the three steps of the rule", lines)
	}
	steps := []struct {
		prefix  string
		needles []string
	}{
		{"1 minute:", []string{"cold shock", "gasp reflex", "airway clear", "DO NOT swim immediately"}},
		{"10 minutes:", []string{"incapacitation", "muscle control", "swimming failure", "lifejacket"}},
		{"1 hour:", []string{"hypothermia", "loss of consciousness", "drowning comes before"}},
	}
	if !strings.HasPrefix(lines[0], coldwaterRuleHeader) {
		t.Errorf("the rule does not start with its header: %q", lines[0])
	}
	for i, step := range steps {
		if !strings.Contains(lines[i], step.prefix) {
			t.Errorf("step %v = %q, want it to carry %q", i, lines[i], step.prefix)
		}
		for _, needle := range step.needles {
			if !strings.Contains(lines[i], needle) {
				t.Errorf("step %v = %q, want it to mention %q", i, lines[i], needle)
			}
		}
	}
}

// TestColdWaterBandsCoverThePublishedTable asserts every temperature falls in
// the published band, with that band's exhaustion and survival windows.
func TestColdWaterBandsCoverThePublishedTable(t *testing.T) {
	t.Parallel()

	// The table's boundaries are published in Fahrenheit; the Celsius values are
	// the rounded labels the same table is taught with, so each row states the
	// Fahrenheit bound it is testing.
	tests := []struct {
		name         string
		fahrenheit   float64
		wantRisk     string
		wantFailure  string
		wantSurvival string
	}{
		{"below freezing", 30, "Immediate Swim Failure", "< 15 min", "15-45 min"},
		{"freezing", 32.5, "High Risk of Swim Failure", "15-30 min", "30-90 min"},
		{"thirty-nine", 39, "High Risk of Swim Failure", "15-30 min", "30-90 min"},
		{"forty", 40, "High Risk of Swim Failure", "30-60 min", "1-3 hours"},
		{"forty-eight", 48, "High Risk of Swim Failure", "30-60 min", "1-3 hours"},
		{"fifty", 50, "Rapid Cooling", "1-2 hours", "1-6 hours"},
		{"fifty-nine", 59, "Rapid Cooling", "1-2 hours", "1-6 hours"},
		{"sixty", 60, "Gradual Cooling", "2-7 hours", "2-40 hours"},
		{"sixty-nine", 69, "Gradual Cooling", "2-7 hours", "2-40 hours"},
		{"seventy", 70, "Cold Shock Still Possible", "many hours", "indefinite"},
		{"warm", 80, "Cold Shock Still Possible", "many hours", "indefinite"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			celsius := (tc.fahrenheit - 32) * 5 / 9
			band := coldwaterBandFor(celsius)
			if !strings.Contains(band.Risk, tc.wantRisk) {
				t.Errorf("at %v°F the risk is %q, want it to mention %q", tc.fahrenheit, band.Risk, tc.wantRisk)
			}
			if band.SwimFailure != tc.wantFailure {
				t.Errorf("at %v°F swim failure = %q, want %q", tc.fahrenheit, band.SwimFailure, tc.wantFailure)
			}
			if !strings.Contains(band.Survival, tc.wantSurvival) {
				t.Errorf("at %v°F survival = %q, want it to mention %q", tc.fahrenheit, band.Survival, tc.wantSurvival)
			}
			if band.Action == "" {
				t.Errorf("at %v°F there is no action line", tc.fahrenheit)
			}
		})
	}
}

// TestColdWaterBandBoundariesAreThePublishedOnes asserts the band changes at the
// published temperatures, in Fahrenheit, so the table cannot drift.
func TestColdWaterBandBoundariesAreThePublishedOnes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    float64
		want string
	}{
		{"just below the first boundary", 32.4, "< 15 min"},
		{"at the first boundary", 32.5, "15-30 min"},
		{"at the second boundary", 40, "30-60 min"},
		{"at the third boundary", 50, "1-2 hours"},
		{"at the fourth boundary", 60, "2-7 hours"},
		{"at the fifth boundary", 70, "many hours"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			celsius := (tc.f - 32) * 5 / 9
			if got := coldwaterBandFor(celsius).SwimFailure; got != tc.want {
				t.Errorf("at %v°F swim failure = %q, want %q", tc.f, got, tc.want)
			}
		})
	}
}

// TestParseColdWaterTemperatureAcceptsBothScales asserts a temperature is read
// with its unit, and that a bare number is Fahrenheit.
func TestParseColdWaterTemperatureAcceptsBothScales(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		celsius float64
	}{
		{"48F", 8.888888},
		{"48f", 8.888888},
		{"48°F", 8.888888},
		{"48 F", 8.888888},
		{"48", 8.888888},
		{"8.9C", 8.9},
		{"8.9c", 8.9},
		{"8.9°C", 8.9},
		{"0C", 0},
		{"32F", 0},
		{"-1.1C", -1.1},
	}
	for _, tc := range tests {
		got, ok := parseColdWaterTemperature(tc.in)
		if !ok {
			t.Errorf("parseColdWaterTemperature(%q) failed", tc.in)
			continue
		}
		if !closeWithin(got, tc.celsius, 1e-6) {
			t.Errorf("parseColdWaterTemperature(%q) = %v, want %v", tc.in, got, tc.celsius)
		}
	}
	for _, in := range []string{"", "cold", "48X", "F", "1.2.3C"} {
		if got, ok := parseColdWaterTemperature(in); ok {
			t.Errorf("parseColdWaterTemperature(%q) = %v, want a failure", in, got)
		}
	}
}

// TestColdWaterCommandRendersTheWindow asserts the three-line answer for a
// temperature: the headline in both scales, the windows, and the action.
func TestColdWaterCommandRendersTheWindow(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "coldwater 48F")
	if len(lines) != 3 {
		t.Fatalf("coldwater 48F = %v, want three lines", lines)
	}
	if lines[0] != "[COLD WATER 48°F / 8.9°C] DANGER: High Risk of Swim Failure!" {
		t.Errorf("headline = %q", lines[0])
	}
	if lines[1] != "Cold Shock: 1 min gasp reflex | Swim Failure: 30-60 min | Survival: 1-3 hours" {
		t.Errorf("windows = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "Action: HUDDLE position or HELP posture") {
		t.Errorf("action = %q", lines[2])
	}

	// The same water quoted in Celsius answers identically.
	metric := runLines(t, reg, session, "coldwater 8.9C")
	if len(metric) != 3 || metric[0] != lines[0] {
		t.Errorf("coldwater 8.9C = %v, want the same answer as 48F", metric)
	}
}

// TestColdwaterAliasAndRegistration asserts the command, its alias, and the
// alias table all agree, and that both are documented.
func TestColdwaterAliasAndRegistration(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, name := range []string{"coldwater", "immersion"} {
		cmd, ok := reg.byName[name]
		if !ok {
			t.Fatalf("command %q is not registered", name)
		}
		if cmd.summary == "" || cmd.usage != coldwaterUsage || len(cmd.detail) == 0 {
			t.Errorf("command %q is not documented: %+v", name, cmd)
		}
		lines := runLines(t, reg, session, name+" 48F")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "[COLD WATER 48°F") {
			t.Errorf("%v 48F = %v, want the immersion window", name, lines)
		}
	}
	if got := reg.aliases["immersion"]; got != "coldwater" {
		t.Errorf("aliases[immersion] = %q, want coldwater", got)
	}
}

// TestColdwaterCommandRejectsUnusableArguments asserts an argument that is not
// a temperature is answered with the usage rather than a made-up window.
func TestColdwaterCommandRejectsUnusableArguments(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "coldwater freezing cold")
	if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+coldwaterUsage) {
		t.Errorf("coldwater with an unusable argument = %v, want the usage line", lines)
	}
}

// TestColdWaterLinesFitOneEnvelope asserts every line fits one NOTICE, since a
// survival window split across envelopes could be attached to the wrong step.
func TestColdWaterLinesFitOneEnvelope(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range runLines(t, reg, session, "coldwater") {
		fits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", line)
		if err != nil {
			t.Fatalf("noticeFits(%q): %v", line, err)
		}
		if !fits {
			t.Errorf("rule line %q does not fit one envelope", line)
		}
	}
	for _, temperature := range []string{"30F", "35F", "48F", "55F", "65F", "75F"} {
		for _, line := range runLines(t, reg, session, "coldwater "+temperature) {
			fits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", line)
			if err != nil {
				t.Fatalf("noticeFits(%q): %v", line, err)
			}
			if !fits {
				t.Errorf("%v line %q does not fit one envelope", temperature, line)
			}
		}
	}
}
