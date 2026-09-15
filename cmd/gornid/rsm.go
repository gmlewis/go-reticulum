// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"github.com/gmlewis/go-reticulum/rsg"
)

// checkReleaseRSMStructure validates a signed RSM envelope against the
// canonical release-structure rules. It is a thin wrapper over
// rsg.CheckReleaseRSMStructure so gornid shares a single implementation
// with gorngit and gorngcs.
func checkReleaseRSMStructure(signedData map[any]any) error {
	return rsg.CheckReleaseRSMStructure(signedData)
}
