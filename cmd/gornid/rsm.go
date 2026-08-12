// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"github.com/gmlewis/go-reticulum/rsg"
)

// RSM validation error strings, aliased from the shared rsg package so
// existing gornid callers keep their lower-case names. These match
// Python's check_release_rsm_structure exactly (rnid.py lines 588-600).
var (
	errRSMNoMeta        = rsg.ErrRSMNoMeta
	errRSMIncompletePkg = rsg.ErrRSMIncompletePkg
	errRSMIncompleteOrg = rsg.ErrRSMIncompleteOrg
	errRSMInvalidData   = rsg.ErrRSMInvalidData
	errRSMInvalidLen    = rsg.ErrRSMInvalidLen
	errRSMInvalidOrigin = rsg.ErrRSMInvalidOrigin
)

// checkReleaseRSMStructure validates a signed RSM envelope against the
// canonical release-structure rules. It is a thin wrapper over
// rsg.CheckReleaseRSMStructure so gornid shares a single implementation
// with gorngit and gorngcs.
func checkReleaseRSMStructure(signedData map[any]any) error {
	return rsg.CheckReleaseRSMStructure(signedData)
}
