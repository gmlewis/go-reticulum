// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

// sanRef validates a single git ref, mirroring RNS.Utilities.rngit.util.san_ref
// (rngit v1.4.2). It returns the ref unchanged when valid, or the empty string
// when invalid (a valid ref is never empty, so this mirrors Python's None).
//
// The rules, in Python's order:
//   - reject a leading '-', leading '/', trailing '/', or trailing '.'
//   - reject any space, missing '/', '..', '/.', '//', or backslash
//   - reject any component ending in ".lock"
//   - reject any byte with code < 40 (control chars and punctuation below '(')
//   - reject ASCII DEL (0x7f), '~', '^', ':', '?', '*', '[', the "@{" sequence,
//     and a ref that is exactly "@"
func sanRef(ref string) string {
	if strings.HasPrefix(ref, "-") {
		return ""
	}
	if strings.HasPrefix(ref, "/") {
		return ""
	}
	if strings.HasSuffix(ref, "/") {
		return ""
	}
	if strings.HasSuffix(ref, ".") {
		return ""
	}
	if strings.Contains(ref, " ") {
		return ""
	}
	if !strings.Contains(ref, "/") {
		return ""
	}
	if strings.Contains(ref, "..") {
		return ""
	}
	if strings.Contains(ref, "/.") {
		return ""
	}
	if strings.Contains(ref, "//") {
		return ""
	}
	if strings.Contains(ref, `\`) {
		return ""
	}
	for comp := range strings.SplitSeq(ref, "/") {
		if strings.HasSuffix(comp, ".lock") {
			return ""
		}
	}
	for _, c := range ref {
		if c < 40 {
			return ""
		}
	}
	if strings.ContainsRune(ref, 0x7f) {
		return ""
	}
	if strings.ContainsRune(ref, '~') {
		return ""
	}
	if strings.ContainsRune(ref, '^') {
		return ""
	}
	if strings.ContainsRune(ref, ':') {
		return ""
	}
	if strings.ContainsRune(ref, '?') {
		return ""
	}
	if strings.ContainsRune(ref, '*') {
		return ""
	}
	if strings.ContainsRune(ref, '[') {
		return ""
	}
	if strings.Contains(ref, "@{") {
		return ""
	}
	if ref == "@" {
		return ""
	}
	return ref
}

// sanRefs validates a slice of refs, mirroring san_refs. It returns the slice
// when every element is a valid ref, or nil when the input is nil or any
// element is invalid.
func sanRefs(refs []string) []string {
	if refs == nil {
		return nil
	}
	for _, ref := range refs {
		if sanRef(ref) == "" {
			return nil
		}
	}
	return refs
}

// sanSHA validates a git SHA-1/object hash, mirroring san_sha. It requires at
// least 40 hex characters and returns the hash unchanged when valid, or the
// empty string when invalid.
func sanSHA(sha string) string {
	if len(sha) < 40 {
		return ""
	}
	if _, err := rns.HexToBytes(sha); err != nil {
		return ""
	}
	return sha
}

// destHexLen is the required hex length of a destination hash
// (TruncatedHashLength/8*2 = 128/8*2 = 32).
const destHexLen = rns.TruncatedHashLength / 8 * 2

// parseRemoteURL parses an rns://<hash>/<group>/<repo> repository URL,
// mirroring RNS.Utilities.rngit.server.parse_remote_url. The scheme match is
// case-insensitive, matching Python's remote.lower().startswith(PROTO_SPEC).
func parseRemoteURL(remote string) ([]byte, string, string, error) {
	if !strings.HasPrefix(strings.ToLower(remote), protoSpec) {
		return nil, "", "", fmt.Errorf("Invalid protocol in remote URL")
	}
	components := strings.Split(remote[len(protoSpec):], "/")
	if len(components) != 3 {
		return nil, "", "", fmt.Errorf("Invalid number of URL components")
	}
	destHash, err := parseDestHash(components[0])
	if err != nil {
		return nil, "", "", err
	}
	return destHash, components[1], components[2], nil
}

// parseRemoteGroupURL parses an rns://<hash>/<group> group URL, mirroring
// parse_remote_group_url.
func parseRemoteGroupURL(remote string) ([]byte, string, error) {
	if !strings.HasPrefix(strings.ToLower(remote), protoSpec) {
		return nil, "", fmt.Errorf("Invalid protocol in remote URL")
	}
	components := strings.Split(remote[len(protoSpec):], "/")
	if len(components) != 2 {
		return nil, "", fmt.Errorf("Invalid number of URL components")
	}
	destHash, err := parseDestHash(components[0])
	if err != nil {
		return nil, "", err
	}
	return destHash, components[1], nil
}

// parseRemoteDestinationURL parses an rns://<hash> destination URL, mirroring
// parse_remote_destination_url.
func parseRemoteDestinationURL(remote string) ([]byte, error) {
	if !strings.HasPrefix(strings.ToLower(remote), protoSpec) {
		return nil, fmt.Errorf("Invalid protocol in remote URL")
	}
	components := strings.Split(remote[len(protoSpec):], "/")
	if len(components) == 0 {
		return nil, fmt.Errorf("Invalid number of URL components")
	}
	return parseDestHash(components[0])
}

// parseDestHash decodes and length-validates a destination hash component,
// mirroring the shared logic in parse_remote_url and friends.
func parseDestHash(hexHash string) ([]byte, error) {
	if len(hexHash) != destHexLen {
		return nil, fmt.Errorf("Invalid destination hash length")
	}
	dest, err := rns.HexToBytes(hexHash)
	if err != nil {
		return nil, fmt.Errorf("Invalid destination hash: %w", err)
	}
	return dest, nil
}
