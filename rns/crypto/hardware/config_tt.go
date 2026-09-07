// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build reticulum_asic || asic

package hardware

const currentTarget = TargetASIC

var currentProfile = Profile{
	Target:               TargetASIC,
	Name:                 "Tiny Tapeout ASIC (tt_um_gmlewis_reticulum)",
	MaxClockMHz:          50,
	NumTokenEngines:      1,
	Sha256RoundsPerStage: 1,
	HasHardwareCrypto:    true,
}
