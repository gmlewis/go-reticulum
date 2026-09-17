// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"strings"
	"testing"
)

// TestEncodeMorseMatchesTheStandardTable asserts the encoding of every letter
// and digit is the ITU code, since a wrong code is a wrong message.
func TestEncodeMorseMatchesTheStandardTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		code string
	}{
		{"SOS", "... --- ..."},
		{"SOS MAYDAY", "... --- ... / -- .- -.-- -.. .- -.--"},
		{"E", "."},
		{"T", "-"},
		{"A B", ".- / -..."},
		{"1234567890", ".---- ..--- ...-- ....- ..... -.... --... ---.. ----. -----"},
		{"HELLO WORLD", ".... . .-.. .-.. --- / .-- --- .-. .-.. -.."},
		{"WT? OK!", ".-- - ..--.. / --- -.- -.-.--"},
		{"sos", "... --- ..."},
	}
	for _, tc := range tests {
		got, err := EncodeMorse(tc.text)
		if err != nil {
			t.Errorf("EncodeMorse(%q): %v", tc.text, err)
			continue
		}
		if got != tc.code {
			t.Errorf("EncodeMorse(%q) = %q, want %q", tc.text, got, tc.code)
		}
	}
}

// TestDecodeMorseMatchesTheStandardTable asserts the decoding of every code is
// the character it stands for.
func TestDecodeMorseMatchesTheStandardTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		text string
	}{
		{"... --- ...", "SOS"},
		{"... --- ... / -- .- -.-- -.. .- -.--", "SOS MAYDAY"},
		{".", "E"},
		{"-", "T"},
		{".---- ..--- ...--", "123"},
		{".... . .-.. .-.. ---", "HELLO"},
		// Three spaces are how a person writing a code down marks a word break.
		{".... . .-.. .-.. ---   .-- --- .-. .-.. -..", "HELLO WORLD"},
	}
	for _, tc := range tests {
		got, err := DecodeMorse(tc.code)
		if err != nil {
			t.Errorf("DecodeMorse(%q): %v", tc.code, err)
			continue
		}
		if got != tc.text {
			t.Errorf("DecodeMorse(%q) = %q, want %q", tc.code, got, tc.text)
		}
	}
}

// TestMorseRoundTrips asserts encode then decode returns the message, which is
// the property that makes the translator trustworthy in both directions.
func TestMorseRoundTrips(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"SOS", "HELLO WORLD", "NEED WATER AT GRID CM87WK", "1234567890"} {
		code, err := EncodeMorse(text)
		if err != nil {
			t.Fatalf("EncodeMorse(%q): %v", text, err)
		}
		back, err := DecodeMorse(code)
		if err != nil {
			t.Fatalf("DecodeMorse(%q): %v", code, err)
		}
		if back != text {
			t.Errorf("round trip of %q gave %q (code %q)", text, back, code)
		}
	}
}

// TestMorseAlphabetAndReverseTableAgree asserts the two directions can never
// drift apart.
func TestMorseAlphabetAndReverseTableAgree(t *testing.T) {
	t.Parallel()

	if len(morseAlphabet) != len(morseCodes) {
		t.Fatalf("the alphabet has %v entries and the reverse table %v",
			len(morseAlphabet), len(morseCodes))
	}
	for letter, code := range morseAlphabet {
		if back, ok := morseCodes[code]; !ok || back != letter {
			t.Errorf("code %q maps back to %q, want %q", code, back, letter)
		}
	}
}

// TestMorseRefusesWhatItCannotTranslate asserts an unsupported character or
// code is named rather than silently dropped, which would corrupt the message.
func TestMorseRefusesWhatItCannotTranslate(t *testing.T) {
	t.Parallel()

	if _, err := EncodeMorse("SOS ~"); err == nil {
		t.Error("encoding an unsupported character succeeded")
	} else if !strings.Contains(err.Error(), "~") {
		t.Errorf("error %v does not name the character", err)
	}
	if _, err := EncodeMorse("   "); err == nil {
		t.Error("encoding nothing succeeded")
	}
	if _, err := DecodeMorse("... ...-.- ..."); err == nil {
		t.Error("decoding an unknown code succeeded")
	} else if !strings.Contains(err.Error(), "...-.-") {
		t.Errorf("error %v does not name the code", err)
	}
	if _, err := DecodeMorse(""); err == nil {
		t.Error("decoding nothing succeeded")
	}
}

// TestMorseCommandTranslatesBothWays asserts the command's two directions, and
// that its usage is explained.
func TestMorseCommandTranslatesBothWays(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	encode := runLines(t, reg, session, "morse SOS MAYDAY")
	if len(encode) != 1 || encode[0] != "... --- ... / -- .- -.-- -.. .- -.--" {
		t.Errorf("morse SOS MAYDAY = %v, want the documented code", encode)
	}
	decode := runLines(t, reg, session, "morse -d ... --- ...")
	if len(decode) != 1 || decode[0] != "SOS" {
		t.Errorf("morse -d ... --- ... = %v, want SOS", decode)
	}
	for _, line := range []string{"morse", "morse -d", "morse -x hello"} {
		lines := runLines(t, reg, session, line)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+morseUsage) {
			t.Errorf("%q = %v, want the usage line", line, lines)
		}
	}
	tooLong := runLines(t, reg, session, "morse "+strings.Repeat("A", maxMorseTextBytes+1))
	if len(tooLong) != 1 || !strings.Contains(tooLong[0], "limited to") {
		t.Errorf("an over-long message = %v, want the length line", tooLong)
	}
}

// TestMorseOutputFitsOneEnvelopeForFieldMessages asserts a realistic SOS
// message renders into one envelope, which is what the command is for.
func TestMorseOutputFitsOneEnvelopeForFieldMessages(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"SOS", "SOS MAYDAY", "NEED HELP AT CM87WK", "INJURED NEED MEDICAL"} {
		code, err := EncodeMorse(text)
		if err != nil {
			t.Fatalf("EncodeMorse(%q): %v", text, err)
		}
		fits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", code)
		if err != nil {
			t.Fatalf("noticeFits(%q): %v", code, err)
		}
		if !fits {
			t.Errorf("%q (%q) does not fit one envelope", text, code)
		}
	}
}
