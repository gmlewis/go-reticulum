// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"strings"
	"testing"
)

// TestSignalSectionsAreTheDocumentedContent asserts each section carries the
// documented signals, word for word, because these are the markings a search
// aircraft is looking for and a paraphrase could be misread.
func TestSignalSectionsAreTheDocumentedContent(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"air": "[GROUND-TO-AIR] V: need assistance | X: need medical help | ->: heading this way | " +
			"Y: yes | N: no. Lay markings in a clearing, as large and high-contrast as possible.",
		"sound": "[SOUND] 3 blasts on a whistle or horn, repeated every minute, is the universal distress " +
			"signal. 2 blasts is the reply: heard, and coming.",
		"light": "[LIGHT] 3 flashes of a mirror or torch, then a one-minute pause, is the distress signal. " +
			"SOS in Morse is ... --- ...",
	}
	if len(signalSections) != len(want) {
		t.Fatalf("there are %v sections, want %v", len(signalSections), len(want))
	}
	for _, section := range signalSections {
		expected, ok := want[section.Name]
		if !ok {
			t.Errorf("unexpected section %q", section.Name)
			continue
		}
		if section.Text != expected {
			t.Errorf("section %q =\n  %v\nwant\n  %v", section.Name, section.Text, expected)
		}
		if section.Summary == "" {
			t.Errorf("section %q has no summary for the listing", section.Name)
		}
	}
}

// TestSignalCommandAnswersEachSection asserts a named section answers with its
// card, and that asking for nothing prints the whole guide.
func TestSignalCommandAnswersEachSection(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, section := range signalSections {
		lines := runLines(t, reg, session, "signal "+section.Name)
		if len(lines) != 1 || lines[0] != section.Text {
			t.Errorf("signal %v = %v, want the card", section.Name, lines)
		}
	}
	all := runLines(t, reg, session, "signal")
	if len(all) != len(signalSections) {
		t.Fatalf("signal = %v, want every section", all)
	}
	for i, section := range signalSections {
		if all[i] != section.Text {
			t.Errorf("signal line %v = %q, want %q", i, all[i], section.Text)
		}
	}
}

// TestSignalCommandRejectsAnUnknownSection asserts an unknown section names
// what it did not find and what it does have.
func TestSignalCommandRejectsAnUnknownSection(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "signal smoke")
	if len(lines) != 1 || !strings.Contains(lines[0], `no distress signal section "smoke"`) {
		t.Fatalf("signal smoke = %v, want the unknown-section line", lines)
	}
	if !strings.Contains(lines[0], "air") || !strings.Contains(lines[0], "light") {
		t.Errorf("answer = %q, want the sections that do exist", lines[0])
	}
}

// TestSignalSectionsFitOneEnvelopeEach asserts no card needs to be split, since
// a signal guide read out of order is worse than useless.
func TestSignalSectionsFitOneEnvelopeEach(t *testing.T) {
	t.Parallel()

	for _, section := range signalSections {
		fits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", section.Text)
		if err != nil {
			t.Fatalf("noticeFits(%v): %v", section.Name, err)
		}
		if !fits {
			t.Errorf("section %q does not fit one envelope", section.Name)
		}
	}
}

// TestSignalIsRegistered asserts the command exists and is documented.
func TestSignalIsRegistered(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	cmd, ok := reg.byName["signal"]
	if !ok {
		t.Fatal("signal is not registered")
	}
	if cmd.summary == "" || cmd.usage == "" || len(cmd.detail) == 0 {
		t.Errorf("signal is not documented: %+v", cmd)
	}
}
