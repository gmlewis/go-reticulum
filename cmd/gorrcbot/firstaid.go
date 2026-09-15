// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the offline wilderness-medicine action cards: the firstaid
// command and its embedded content. The cards are compiled into the binary and
// make no network call of any kind, because the moment somebody needs them is
// exactly the moment no network exists.
//
// Each card is one line, deliberately: a single line fits one RRC envelope, so
// a card can never be truncated into a half-instruction, and it can be read out
// over a voice link between two people without either of them having to page
// through anything. The wording is the standard field protocol — direct
// pressure, tourniquet time, hypothermia handling, START triage — because that
// is what a responder has already been trained on and will recognize under
// stress.
//
// This is decision support, not a substitute for training. The operator-facing
// help says so; the cards themselves stay terse because a terse card is one
// that gets read.

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// firstaidUsage is the usage line for the command.
const firstaidUsage = "firstaid <topic>"

// firstAidCard is one action card: the primary topic name, the words that also
// reach it, and the text itself.
type firstAidCard struct {
	// Name is the primary topic word.
	Name string
	// Aliases are other words that reach the same card.
	Aliases []string
	// Text is the card, one line, ready to send.
	Text string
}

// firstAidCards is the embedded first-aid reference. It is ordered the way a
// responder reaches for it: bleeding and airway first, environmental injuries
// after.
var firstAidCards = []firstAidCard{
	{
		Name:    "bleed",
		Aliases: []string{"tourniquet", "bleeding"},
		Text: "[BLEEDING] Direct pressure -> wound packing. Tourniquet: 2-3 in above wound " +
			`(not over joint). Tighten until bleeding stops. Mark time: "TK 14:32". NEVER loosen.`,
	},
	{
		Name:    "cpr",
		Aliases: []string{"arrest", "compressions"},
		Text: "[CPR] Rate: 100-120 bpm (Stayin' Alive). Depth: 2-2.4 in (5-6 cm). " +
			"Ratio: 30 compressions to 2 breaths. Minimize interruptions. AED: apply pads immediately.",
	},
	{
		Name:    "triage",
		Aliases: []string{"start"},
		Text: "[START TRIAGE] Walk? -> Green (Minor). Breathing? No -> open airway; Still no -> " +
			"Black (Expectant); Yes -> Red (Immediate). Resp >30 or Cap Refill >2s or No command follow -> " +
			"Red. Else -> Yellow (Delayed).",
	},
	{
		Name:    "shock",
		Aliases: []string{"hypovolemic"},
		Text: "[SHOCK] Signs: rapid weak pulse, pale/cold skin, confusion. Lie flat, elevate feet 12 in " +
			"(unless head/chest injury). Keep warm with blanket. Do NOT give food or water.",
	},
	{
		Name:    "hypo",
		Aliases: []string{"hypothermia", "cold"},
		Text: "[HYPOTHERMIA] Mild: shivering, alert. Severe: shivering stops, apathy, confusion. " +
			"GENTLE handling (prevent V-fib!). Strip wet clothes, vapor wrap, warm sweet drinks if alert.",
	},
	{
		Name:    "heat",
		Aliases: []string{"heatstroke", "hyperthermia"},
		Text: "[HEAT ILLNESS] Exhaustion: heavy sweat, pale, nausea -> rest, shade, sip fluids. " +
			"Stroke: HOT, red, dry/altered mental status = EMERGENCY! Rapid active cooling: douse, " +
			"ice in armpits/groin.",
	},
	{
		Name:    "burns",
		Aliases: []string{"burn", "scald"},
		Text: "[BURNS] Cool immediately with clean water 10-20 min. DO NOT use ice or ointments. " +
			"Remove jewelry before swelling. Cover loosely with sterile dry/non-stick dressing.",
	},
	{
		Name:    "water",
		Aliases: []string{"purify", "disinfect"},
		Text: "[WATER DISINFECTION] Boil: Rolling boil 1 min (<2000m) or 3 min (>2000m). " +
			"Bleach (5-6% unscented): 2 drops/L (8 drops/gal) clear water; 4 drops/L cloudy. Wait 30 min.",
	},
	{
		Name:    "snake",
		Aliases: []string{"snakebite", "envenomation"},
		Text: "[SNAKEBITE] Keep calm, immobilize limb AT or BELOW heart level. Remove jewelry/rings. " +
			"DO NOT cut, suction, ice, or apply tourniquet. Mark swelling edge with pen + time.",
	},
}

// firstAidTopicNames returns every topic word the command accepts, sorted, which
// is what the listing and the unknown-topic answer print.
func firstAidTopicNames() []string {
	var names []string
	for _, card := range firstAidCards {
		names = append(names, card.Name)
	}
	sort.Strings(names)
	return names
}

// firstAidCardFor finds the card a topic word reaches, by its name or any of
// its aliases.
func firstAidCardFor(topic string) (firstAidCard, bool) {
	word := strings.ToLower(strings.TrimSpace(topic))
	for _, card := range firstAidCards {
		if word == card.Name || slices.Contains(card.Aliases, word) {
			return card, true
		}
	}
	return firstAidCard{}, false
}

// runFirstAid answers with one action card, or lists the topics when asked for
// nothing in particular.
func (c *commandContext) runFirstAid() []string {
	topic := strings.TrimSpace(c.Args)
	if topic == "" {
		return []string{"First aid topics: " + strings.Join(firstAidTopicNames(), ", ")}
	}
	card, ok := firstAidCardFor(topic)
	if !ok {
		// The topic is echoed back only after the same hygiene as any other
		// text this bot repeats.
		return []string{fmt.Sprintf("no first aid card for %q — try: %v",
			safeEcho(topic, maxEchoNickBytes), strings.Join(firstAidTopicNames(), ", "))}
	}
	return []string{card.Text}
}
