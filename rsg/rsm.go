// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rsg

import (
	"errors"
	"strings"
)

// RSM validation error strings, matching Python's
// check_release_rsm_structure exactly (rnid.py lines 588-600). These are
// user-facing messages and must match Python verbatim.
var (
	ErrRSMNoMeta        = errors.New("No release metadata in manifest")
	ErrRSMIncompletePkg = errors.New("Incomplete package data in manifest")
	ErrRSMIncompleteOrg = errors.New("Incomplete release origin data in manifest")
	ErrRSMInvalidData   = errors.New("Invalid data in release manifest")
	ErrRSMInvalidLen    = errors.New("Invalid origin hash length in manifest")
	ErrRSMInvalidOrigin = errors.New("Invalid origin hash in manifest")
)

// CheckReleaseRSMStructure validates a signed RSM envelope against the
// canonical release-structure rules defined by Python's
// check_release_rsm_structure (rnid.py). signedData is the unpacked
// envelope map (map[any]any with string keys; meta is map[any]any). It
// returns nil for a valid manifest, or the exact Python error.
//
// The validation order mirrors Python:
//  1. meta must be present and a map (else ErrRSMNoMeta)
//  2. name and version must be non-falsy (else ErrRSMIncompletePkg)
//  3. origin and path must be non-falsy (else ErrRSMIncompleteOrg)
//  4. name and version must not contain "/" (else ErrRSMInvalidData)
//  5. origin must be 16 bytes long (else ErrRSMInvalidLen)
//  6. origin must be bytes (else ErrRSMInvalidOrigin)
func CheckReleaseRSMStructure(signedData map[any]any) error {
	metaVal, ok := signedData["meta"]
	if !ok || metaVal == nil {
		return ErrRSMNoMeta
	}
	meta, ok := metaVal.(map[any]any)
	if !ok || meta == nil {
		return ErrRSMNoMeta
	}

	name := meta["name"]
	version := meta["version"]
	origin := meta["origin"]
	originPath := meta["path"]

	if isFalsy(name) || isFalsy(version) {
		return ErrRSMIncompletePkg
	}
	if isFalsy(origin) || isFalsy(originPath) {
		return ErrRSMIncompleteOrg
	}

	nameStr, _ := name.(string)
	versionStr, _ := version.(string)
	if strings.Contains(nameStr, "/") || strings.Contains(versionStr, "/") {
		return ErrRSMInvalidData
	}

	if !validOriginLength(origin, 16) {
		return ErrRSMInvalidLen
	}
	if !isBytes(origin) {
		return ErrRSMInvalidOrigin
	}
	return nil
}

// isFalsy reports whether v is Python-falsy for the RSM check purposes:
// nil, empty string, or empty []byte. Other zero values (0, false) are
// also falsy in Python, but name/version/path/origin are expected to be
// strings or bytes; treat them as falsy when empty.
func isFalsy(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []byte:
		return len(x) == 0
	default:
		return false
	}
}

// isBytes reports whether v is a []byte.
func isBytes(v any) bool {
	_, ok := v.([]byte)
	return ok
}

// validOriginLength reports whether origin has the expected byte length
// for the given type ([]byte or string). Non-collection types return
// true so the length check is skipped and the subsequent type check
// (#6) reports ErrRSMInvalidOrigin, matching Python's check order for
// non-bytes origins.
func validOriginLength(v any, want int) bool {
	switch x := v.(type) {
	case []byte:
		return len(x) == want
	case string:
		return len(x) == want
	default:
		return true
	}
}
