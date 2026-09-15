// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the small shared helpers the bot's policy code needs.

package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

// hexString renders a byte slice as lowercase hexadecimal. The RRC client uses
// the same rendering for peer hashes in every display and message-id context.
func hexString(b []byte) string {
	return hex.EncodeToString(b)
}

// maxHashPrefixBytes is one more than the longest hash prefix a human would
// type: hex of the 16-byte identity hash.
const maxHashPrefixBytes = 2 * 16

// isHexPrefix reports whether token is a lowercase-hexadecimal prefix of hash,
// accepting upper case input.
func isHexPrefix(token, hash string) bool {
	if token == "" || len(token) > maxHashPrefixBytes || len(token) > len(hash) {
		return false
	}
	for i := range len(token) {
		if !isHexDigit(token[i]) {
			return false
		}
	}
	return strings.HasPrefix(strings.ToLower(hash), strings.ToLower(token))
}

// isHexDigit reports whether c is an ASCII hexadecimal digit.
func isHexDigit(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'f':
		return true
	case c >= 'A' && c <= 'F':
		return true
	default:
		return false
	}
}

// isHexString reports whether s is entirely ASCII hexadecimal.
func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isHexDigit(s[i]) {
			return false
		}
	}
	return true
}

// mustHex decodes a hexadecimal string, panicking on a malformed one. It exists
// for tests and for the few compile-time constants the bot decodes.
func mustHex(s string) []byte {
	raw, err := hex.DecodeString(s)
	if err != nil {
		panic("invalid hexadecimal literal " + s + ": " + err.Error())
	}
	return raw
}

// normalizeRoom lowercases and trims a room name, mirroring the client's own
// normalization so room names from the configuration and from the wire agree.
func normalizeRoom(room string) string {
	return strings.ToLower(strings.TrimSpace(room))
}

// maxEchoNickBytes bounds a nick the bot repeats back, since a nick is chosen by
// its owner and can be arbitrarily long.
const maxEchoNickBytes = 32

// safeEcho makes text this bot did not write safe to repeat in a NOTICE: the
// terminal escapes and control characters are removed with the same hygiene a
// provider answer gets, the result is shortened to limit bytes, and text that
// sanitizes away entirely becomes empty, which callers treat as "nothing to
// show". A NOTICE is rendered by other people's terminals, so an escape sequence
// in one would run there.
func safeEcho(text string, limit int) string {
	clean, err := sanitizeProviderLine(text)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(truncateUTF8Bytes(clean, limit))
}

// safeTarget renders a resolved peer for a reply: "nick (hash-prefix)", the shape
// the hub's own notices use, with the nick stripped of terminal escapes. A nick
// is chosen by its owner and the bot publishes it under its own name, so it gets
// the same hygiene as any other echoed text.
func safeTarget(target rrc.PeerTarget) string {
	nick := safeEcho(target.Nick, maxEchoNickBytes)
	if nick == "" {
		nick = shortHash(target.HashHex)
	}
	if target.HashHex == "" {
		return nick
	}
	return fmt.Sprintf("%v (%v)", nick, shortHash(target.HashHex))
}

// pluralCount renders a count with its singular or plural noun. The plural is
// given explicitly: English is not regular enough to append an "s" ("match" is
// not "matchs"), and a bot that spells its own replies wrong looks broken.
func pluralCount(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%v %v", n, singular)
	}
	return fmt.Sprintf("%v %v", n, plural)
}

// formatHorizon renders when something expires relative to now, as a whole
// phrase: "expires in 6d" for a future expiry, "expired 5m ago" for one that has
// already passed. The unit is as coarse as formatAge's, so a path answer reads
// like the rest of the bot's answers rather than like a timestamp.
func formatHorizon(d time.Duration) string {
	if d < 0 {
		return "expired " + formatAge(-d) + " ago"
	}
	return "expires in " + formatAge(d)
}

// hexToBytes decodes a hex string, reporting the decode failure rather than
// returning half a hash.
func hexToBytes(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("decoding hex %q: %w", s, err)
	}
	return b, nil
}
