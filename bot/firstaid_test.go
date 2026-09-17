// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"slices"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestFirstAidCardsAreExactlyTheFieldText asserts every card is the documented
// protocol text, word for word. A medical card that drifts from the standard
// wording is worse than no card, so the expectation is exact rather than a
// substring.
func TestFirstAidCardsAreExactlyTheFieldText(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"bleed": `[BLEEDING] Direct pressure -> wound packing. Tourniquet: 2-3 in above wound (not over joint). Tighten until bleeding stops. Mark time: "TK 14:32". NEVER loosen.`,
		"cpr":   `[CPR] Rate: 100-120 bpm (Stayin' Alive). Depth: 2-2.4 in (5-6 cm). Ratio: 30 compressions to 2 breaths. Minimize interruptions. AED: apply pads immediately.`,
		"triage": `[START TRIAGE] Walk? -> Green (Minor). Breathing? No -> open airway; Still no -> ` +
			`Black (Expectant); Yes -> Red (Immediate). Resp >30 or Cap Refill >2s or No command follow -> ` +
			`Red. Else -> Yellow (Delayed).`,
		"shock": `[SHOCK] Signs: rapid weak pulse, pale/cold skin, confusion. Lie flat, elevate feet 12 in (unless head/chest injury). Keep warm with blanket. Do NOT give food or water.`,
		"hypo":  `[HYPOTHERMIA] Mild: shivering, alert. Severe: shivering stops, apathy, confusion. GENTLE handling (prevent V-fib!). Strip wet clothes, vapor wrap, warm sweet drinks if alert.`,
		"heat":  `[HEAT ILLNESS] Exhaustion: heavy sweat, pale, nausea -> rest, shade, sip fluids. Stroke: HOT, red, dry/altered mental status = EMERGENCY! Rapid active cooling: douse, ice in armpits/groin.`,
		"burns": `[BURNS] Cool immediately with clean water 10-20 min. DO NOT use ice or ointments. Remove jewelry before swelling. Cover loosely with sterile dry/non-stick dressing.`,
		"water": `[WATER DISINFECTION] Boil: Rolling boil 1 min (<2000m) or 3 min (>2000m). Bleach (5-6% unscented): 2 drops/L (8 drops/gal) clear water; 4 drops/L cloudy. Wait 30 min.`,
		"snake": `[SNAKEBITE] Keep calm, immobilize limb AT or BELOW heart level. Remove jewelry/rings. DO NOT cut, suction, ice, or apply tourniquet. Mark swelling edge with pen + time.`,
	}
	if len(firstAidCards) != len(want) {
		t.Fatalf("there are %v cards, want %v", len(firstAidCards), len(want))
	}
	for _, card := range firstAidCards {
		expected, ok := want[card.Name]
		if !ok {
			t.Errorf("card %q is not part of the documented set", card.Name)
			continue
		}
		if card.Text != expected {
			t.Errorf("card %q text =\n  %v\nwant\n  %v", card.Name, card.Text, expected)
		}
	}
}

// TestFirstAidCardsFitOneEnvelope asserts every card fits a single NOTICE
// envelope: a card split across two envelopes could be read out of order.
func TestFirstAidCardsFitOneEnvelope(t *testing.T) {
	t.Parallel()

	ownHash := mustHex(replyOwnHash)
	for _, card := range firstAidCards {
		fits, err := noticeFits(ownHash, "general", "gorrcbot", card.Text)
		if err != nil {
			t.Fatalf("noticeFits(%q): %v", card.Name, err)
		}
		if !fits {
			size, _ := noticeEnvelopeSize(ownHash, "general", "gorrcbot", card.Text)
			t.Errorf("card %q measures %v bytes, larger than the MDU %v", card.Name, size, rns.MDU)
		}
	}
}

// TestFirstAidCommandAnswersEveryTopicAndAlias asserts the command and the
// rx and triage aliases all reach every card.
func TestFirstAidCommandAnswersEveryTopicAndAlias(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, card := range firstAidCards {
		for _, command := range []string{"firstaid", "rx", "triage"} {
			lines := runLines(t, reg, session, command+" "+card.Name)
			if len(lines) != 1 || lines[0] != card.Text {
				t.Errorf("%v %v =\n  %v\nwant\n  %v", command, card.Name, lines, card.Text)
			}
		}
		for _, alias := range card.Aliases {
			lines := runLines(t, reg, session, "firstaid "+alias)
			if len(lines) != 1 || lines[0] != card.Text {
				t.Errorf("firstaid %v (alias of %v) = %v, want the card", alias, card.Name, lines)
			}
		}
	}
}

// TestFirstAidCommandListsTopics asserts an empty request lists the topics,
// which is the only way a person learns which cards exist.
func TestFirstAidCommandListsTopics(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "firstaid")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "First aid topics: ") {
		t.Fatalf("firstaid with no topic = %v, want the topic listing", lines)
	}
	listed := strings.Split(strings.TrimPrefix(lines[0], "First aid topics: "), ", ")
	sorted := append([]string(nil), listed...)
	slices.Sort(sorted)
	if !slices.Equal(listed, sorted) {
		t.Errorf("topic listing %q is not sorted", lines[0])
	}
	for _, card := range firstAidCards {
		if !slices.Contains(listed, card.Name) {
			t.Errorf("topic listing %q is missing %q", lines[0], card.Name)
		}
	}
}

// TestFirstAidCommandRejectsAnUnknownTopic asserts an unknown topic names what
// it did not find and what it does have, so the asker can correct themselves.
func TestFirstAidCommandRejectsAnUnknownTopic(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "firstaid ligma")
	if len(lines) != 1 {
		t.Fatalf("firstaid with an unknown topic = %v, want one line", lines)
	}
	if !strings.Contains(lines[0], `no first aid card for "ligma"`) {
		t.Errorf("answer = %q, want it to name the unknown topic", lines[0])
	}
	if !strings.Contains(lines[0], "bleed") {
		t.Errorf("answer = %q, want it to list the topics that do exist", lines[0])
	}
}

// TestFirstAidTopicsAreDistinct asserts no topic word reaches two cards, which
// would make the answer depend on table order.
func TestFirstAidTopicsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}
	for _, card := range firstAidCards {
		for _, word := range append([]string{card.Name}, card.Aliases...) {
			if other, ok := seen[word]; ok {
				t.Errorf("topic %q reaches both %q and %q", word, other, card.Name)
			}
			seen[word] = card.Name
		}
	}
}

// TestFirstAidIsRegisteredWithItsAliases asserts the command, its two aliases,
// and the alias table all agree.
func TestFirstAidIsRegisteredWithItsAliases(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	for _, name := range []string{"firstaid", "rx", "triage"} {
		cmd, ok := reg.byName[name]
		if !ok {
			t.Fatalf("command %q is not registered", name)
		}
		if len(cmd.detail) == 0 || cmd.summary == "" {
			t.Errorf("command %q is not documented", name)
		}
	}
	for alias, target := range map[string]string{"rx": "firstaid", "triage": "firstaid"} {
		if got := reg.aliases[alias]; got != target {
			t.Errorf("alias %q maps to %q, want %q", alias, got, target)
		}
	}
}
