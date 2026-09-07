// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package hardware

import (
	"testing"
)

func TestHardwareProfileConsistency(t *testing.T) {
	t.Parallel()

	target := GetTarget()
	profile := CurrentProfile()

	if profile.Target != target {
		t.Fatalf("profile.Target = %v, want %v", profile.Target, target)
	}

	if target.String() == "" {
		t.Errorf("target.String() should not be empty")
	}

	switch target {
	case TargetSoftware:
		if profile.HasHardwareCrypto {
			t.Errorf("software target should have HasHardwareCrypto=false")
		}
		if profile.MaxClockMHz != 0 {
			t.Errorf("software target should have MaxClockMHz=0, got %v", profile.MaxClockMHz)
		}
	case TargetASIC:
		if !profile.HasHardwareCrypto {
			t.Errorf("ASIC target should have HasHardwareCrypto=true")
		}
		if profile.MaxClockMHz != 50 {
			t.Errorf("ASIC target MaxClockMHz = %v, want 50", profile.MaxClockMHz)
		}
		if profile.NumTokenEngines != 1 {
			t.Errorf("ASIC target NumTokenEngines = %v, want 1", profile.NumTokenEngines)
		}
	case TargetFPGA:
		if !profile.HasHardwareCrypto {
			t.Errorf("FPGA target should have HasHardwareCrypto=true")
		}
		if profile.MaxClockMHz != 80 {
			t.Errorf("FPGA target MaxClockMHz = %v, want 80", profile.MaxClockMHz)
		}
		if profile.NumTokenEngines != 4 {
			t.Errorf("FPGA target NumTokenEngines = %v, want 4", profile.NumTokenEngines)
		}
	}
}
