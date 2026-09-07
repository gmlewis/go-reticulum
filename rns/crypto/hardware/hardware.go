// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package hardware provides configuration parameters, capability detection,
// and hardware profiles for Reticulum cryptographic hardware accelerators
// (Tiny Tapeout ASIC macro tt_um_gmlewis_reticulum and Tang Primer 25K FPGA prototype).
package hardware

// Target identifies the active cryptographic target (software fallback or hardware accelerator).
type Target int

const (
	// TargetSoftware indicates pure Go standard-library cryptographic implementation.
	TargetSoftware Target = iota
	// TargetASIC indicates the Tiny Tapeout ASIC macro (tt_um_gmlewis_reticulum).
	TargetASIC
	// TargetFPGA indicates the Tang Primer 25K FPGA prototype (FpgaTop).
	TargetFPGA
)

// String returns the human-readable identifier of the target.
func (t Target) String() string {
	switch t {
	case TargetASIC:
		return "reticulum_asic"
	case TargetFPGA:
		return "reticulum_fpga"
	default:
		return "software"
	}
}

// Profile describes the hardware specifications, pipeline stages, and clock
// constraints of the active cryptographic execution environment.
type Profile struct {
	// Target identifies the active accelerator target.
	Target Target
	// Name provides a human-readable description of the hardware or software target.
	Name string
	// MaxClockMHz is the maximum safe operating clock frequency in MHz.
	MaxClockMHz int
	// NumTokenEngines is the number of parallel TokenEngine hardware blocks.
	NumTokenEngines int
	// Sha256RoundsPerStage is the number of SHA-256 calculation rounds per pipeline stage.
	Sha256RoundsPerStage int
	// HasHardwareCrypto reports whether hardware-accelerated cryptography is active.
	HasHardwareCrypto bool
}

// GetTarget returns the Target selected by build tags at compile time.
func GetTarget() Target {
	return currentTarget
}

// CurrentProfile returns the Profile selected by build tags at compile time.
func CurrentProfile() Profile {
	return currentProfile
}
