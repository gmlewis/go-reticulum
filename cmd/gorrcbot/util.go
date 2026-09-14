// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the small shared helpers the bot's policy code needs.

package main

import (
	"encoding/hex"
	"strings"
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
