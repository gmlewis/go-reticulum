// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the distress-signal guide: the ground-to-air markings a
// search aircraft looks for, the whistle and horn cadences that carry farther
// than a voice, and the light and mirror signals that work at night or across a
// valley.
//
// The content is embedded and static. A signal card is worth nothing if it
// needs to be looked up, so the command answers in one line per section and the
// full guide is three lines: short enough that a person with a whistle in one
// hand can read it off a screen.

package bot

import (
	"fmt"
	"strings"
)

// Distress-signal wordings.
const (
	// signalUsage is the usage line for the command.
	signalUsage = "signal [air|sound|light]"
	// signalUnknownLine is the answer to an unrecognised section.
	signalUnknownLine = "no distress signal section %q — try: air, sound, or light"
)

// signalSection is one part of the guide.
type signalSection struct {
	// Name is the section word.
	Name string
	// Summary is the one clause used when the whole guide is listed.
	Summary string
	// Text is the full card, one line.
	Text string
}

// signalSections is the embedded guide, in the order a person reaches for it:
// what to lay on the ground for an aircraft, then sound, then light.
var signalSections = []signalSection{
	{
		Name:    "air",
		Summary: "ground-to-air markings for search aircraft",
		Text: "[GROUND-TO-AIR] V: need assistance | X: need medical help | ->: heading this way | " +
			"Y: yes | N: no. Lay markings in a clearing, as large and high-contrast as possible.",
	},
	{
		Name:    "sound",
		Summary: "whistle and horn cadences",
		Text: "[SOUND] 3 blasts on a whistle or horn, repeated every minute, is the universal distress " +
			"signal. 2 blasts is the reply: heard, and coming.",
	},
	{
		Name:    "light",
		Summary: "mirror and torch signals",
		Text: "[LIGHT] 3 flashes of a mirror or torch, then a one-minute pause, is the distress signal. " +
			"SOS in Morse is ... --- ...",
	},
}

// signalSectionFor finds a section by its name.
func signalSectionFor(name string) (signalSection, bool) {
	word := strings.ToLower(strings.TrimSpace(name))
	for _, section := range signalSections {
		if section.Name == word {
			return section, true
		}
	}
	return signalSection{}, false
}

// runSignal answers with one section, or with every section when asked for
// nothing in particular.
func (c *commandContext) runSignal() []string {
	arg := strings.TrimSpace(c.Args)
	if arg == "" {
		lines := make([]string, 0, len(signalSections))
		for _, section := range signalSections {
			lines = append(lines, section.Text)
		}
		return lines
	}
	section, ok := signalSectionFor(arg)
	if !ok {
		return []string{fmt.Sprintf(signalUnknownLine, safeEcho(arg, maxEchoNickBytes))}
	}
	return []string{section.Text}
}
