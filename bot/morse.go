// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the bidirectional Morse translator. Morse is the one
// signalling mode that survives a dead battery charger and a broken antenna: a
// torch, a whistle, or a straight key and any piece of wire will do, and every
// operator on the band already knows it.
//
// Both directions are supported, because both are needed. A person about to
// signal wants the code for their message; a person who just heard a signal
// wants to know what it said, and reading a code off a screen is far more
// reliable than counting dashes in the dark.
//
// The alphabet is the full ITU set — letters, digits, and the punctuation that
// appears in real traffic — so a message is never silently mangled: a character
// the table does not carry is refused by name rather than dropped.

package bot

import (
	"fmt"
	"strings"
)

// Morse command wordings and bounds.
const (
	// morseUsage is the usage line for the command.
	morseUsage = "morse <text> | morse -d <code...>"
	// morseLetterSeparator separates the codes of one word.
	morseLetterSeparator = " "
	// morseWordSeparator separates the words of a message.
	morseWordSeparator = "/"
	// maxMorseTextBytes bounds how much text one request may carry, so a long
	// paste cannot produce a reply longer than the line budget can deliver.
	maxMorseTextBytes = 200
	// morseUnsupportedLine names a character the table does not carry.
	morseUnsupportedLine = "morse: %q is not in the Morse table"
	// morseUnknownCodeLine names a code the table does not carry.
	morseUnknownCodeLine = "morse: %q is not a Morse code"
)

// morseAlphabet maps a character to its code, in the standard ITU order.
var morseAlphabet = map[rune]string{
	'A': ".-", 'B': "-...", 'C': "-.-.", 'D': "-..", 'E': ".", 'F': "..-.",
	'G': "--.", 'H': "....", 'I': "..", 'J': ".---", 'K': "-.-", 'L': ".-..",
	'M': "--", 'N': "-.", 'O': "---", 'P': ".--.", 'Q': "--.-", 'R': ".-.",
	'S': "...", 'T': "-", 'U': "..-", 'V': "...-", 'W': ".--", 'X': "-..-",
	'Y': "-.--", 'Z': "--..",
	'0': "-----", '1': ".----", '2': "..---", '3': "...--", '4': "....-",
	'5': ".....", '6': "-....", '7': "--...", '8': "---..", '9': "----.",
	'.': ".-.-.-", ',': "--..--", '?': "..--..", '\'': ".----.", '!': "-.-.--",
	'/': "-..-.", '(': "-.--.", ')': "-.--.-", '&': ".-...", ':': "---...",
	';': "-.-.-.", '=': "-...-", '+': ".-.-.", '-': "-....-", '_': "..--.-",
	'"': ".-..-.", '$': "...-..-", '@': ".--.-.",
}

// morseCodes is the reverse table, built once from the alphabet so the two can
// never disagree.
var morseCodes = func() map[string]rune {
	codes := make(map[string]rune, len(morseAlphabet))
	for letter, code := range morseAlphabet {
		codes[code] = letter
	}
	return codes
}()

// EncodeMorse translates text into Morse. Letters of a word are separated by a
// space and words by a slash, which is the conventional rendering. Case is
// ignored, because Morse has only one case, and a character the table does not
// carry is reported rather than dropped.
func EncodeMorse(text string) (string, error) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return "", fmt.Errorf("morse: nothing to translate")
	}
	encoded := make([]string, 0, len(words))
	for _, word := range words {
		letters := make([]string, 0, len(word))
		for _, r := range strings.ToUpper(word) {
			code, ok := morseAlphabet[r]
			if !ok {
				return "", fmt.Errorf(morseUnsupportedLine, string(r))
			}
			letters = append(letters, code)
		}
		encoded = append(encoded, strings.Join(letters, morseLetterSeparator))
	}
	return strings.Join(encoded, morseLetterSeparator+morseWordSeparator+morseLetterSeparator), nil
}

// DecodeMorse translates Morse back into text. A slash separates words; without
// one, a run of three spaces is treated as a word break too, because that is
// how a person reading a code off the air tends to write it down.
func DecodeMorse(code string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(code), "   ", " "+morseWordSeparator+" ")
	words := strings.Split(normalized, morseWordSeparator)
	decoded := make([]string, 0, len(words))
	for _, word := range words {
		letters := strings.Fields(word)
		if len(letters) == 0 {
			continue
		}
		var b strings.Builder
		for _, letter := range letters {
			r, ok := morseCodes[letter]
			if !ok {
				return "", fmt.Errorf(morseUnknownCodeLine, letter)
			}
			b.WriteRune(r)
		}
		decoded = append(decoded, b.String())
	}
	if len(decoded) == 0 {
		return "", fmt.Errorf("morse: nothing to translate")
	}
	return strings.Join(decoded, " "), nil
}

// runMorse translates in the direction the request names.
func (c *commandContext) runMorse() []string {
	args := strings.TrimSpace(c.Args)
	if args == "" {
		return []string{"Usage: " + morseUsage}
	}
	if decode := strings.HasPrefix(args, "-d "); decode || args == "-d" {
		code := strings.TrimSpace(strings.TrimPrefix(args, "-d"))
		if code == "" {
			return []string{"Usage: " + morseUsage}
		}
		text, err := DecodeMorse(code)
		if err != nil {
			return []string{err.Error()}
		}
		return []string{text}
	}
	if strings.HasPrefix(args, "-") {
		return []string{"Usage: " + morseUsage}
	}
	if len(args) > maxMorseTextBytes {
		return []string{fmt.Sprintf("morse: text is limited to %v characters", maxMorseTextBytes)}
	}
	code, err := EncodeMorse(args)
	if err != nil {
		return []string{err.Error()}
	}
	return []string{code}
}
