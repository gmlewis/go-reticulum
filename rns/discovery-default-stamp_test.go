// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

// TestDiscoveryDefaultStampValueIs16 verifies that the default discovery
// stamp cost is 16 (Python InterfaceAnnouncer.DEFAULT_STAMP_VALUE,
// Discovery.py:35), up from the prior 14. A handler constructed with a zero
// required value must adopt 16.
func TestDiscoveryDefaultStampValueIs16(t *testing.T) {
	t.Parallel()
	h := NewInterfaceAnnounceHandler(&Reticulum{logger: NewLogger()}, 0, func(map[string]any) {})
	if h.requiredValue != 16 {
		t.Fatalf("default requiredValue=%v, want 16", h.requiredValue)
	}
	if discoveryDefaultStampValue != 16 {
		t.Fatalf("discoveryDefaultStampValue=%v, want 16", discoveryDefaultStampValue)
	}
}
