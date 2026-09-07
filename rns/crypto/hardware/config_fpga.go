// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build reticulum_fpga || fpga

package hardware

const currentTarget = TargetFPGA

var currentProfile = Profile{
	Target:               TargetFPGA,
	Name:                 "Tang Primer 25K FPGA (FpgaTop)",
	MaxClockMHz:          80,
	NumTokenEngines:      4,
	Sha256RoundsPerStage: 2,
	HasHardwareCrypto:    true,
}
