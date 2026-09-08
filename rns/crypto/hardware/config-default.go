// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !reticulum_asic && !asic && !reticulum_fpga && !fpga

package hardware

const currentTarget = TargetSoftware

var currentProfile = Profile{
	Target:               TargetSoftware,
	Name:                 "Software (Go stdlib)",
	MaxClockMHz:          0,
	NumTokenEngines:      0,
	Sha256RoundsPerStage: 0,
	HasHardwareCrypto:    false,
}
