// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

func TestLiveHardwareRestorePlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		action   liveWriteAction
		baseline liveHardwareBaseline
		want     liveHardwareRestorePlan
	}{
		{
			name:   "firmware hash set is reversible with baseline target hash",
			action: liveWriteActionFirmwareHashSet,
			baseline: liveHardwareBaseline{
				eeprom: &eepromDownloaderState{provisioned: true},
				hashes: &rnodeHashSnapshot{firmwareHashTarget: bytesOfLen(32)},
			},
			want: liveHardwareRestorePlan{
				action:          liveWriteActionFirmwareHashSet,
				safety:          liveSerialSafetyReversibleWrite,
				captureEEPROM:   true,
				captureHashes:   true,
				restoreStrategy: liveHardwareRestoreFirmwareHash,
			},
		},
		{
			name:   "device signing is reversible with baseline signature",
			action: liveWriteActionDeviceSigning,
			baseline: liveHardwareBaseline{
				eeprom: &eepromDownloaderState{provisioned: true, signature: bytesOfLen(128)},
				hashes: &rnodeHashSnapshot{deviceHash: bytesOfLen(32)},
			},
			want: liveHardwareRestorePlan{
				action:          liveWriteActionDeviceSigning,
				safety:          liveSerialSafetyReversibleWrite,
				captureEEPROM:   true,
				captureHashes:   true,
				restoreStrategy: liveHardwareRestoreDeviceSignature,
			},
		},
		{
			name:   "bootstrap is reversible with captured eeprom image",
			action: liveWriteActionEEPROMBootstrap,
			baseline: liveHardwareBaseline{
				eeprom: &eepromDownloaderState{provisioned: true, eeprom: bytesOfLen(0xa8)},
				hashes: &rnodeHashSnapshot{firmwareHashTarget: bytesOfLen(32)},
			},
			want: liveHardwareRestorePlan{
				action:          liveWriteActionEEPROMBootstrap,
				safety:          liveSerialSafetyReversibleWrite,
				captureEEPROM:   true,
				captureHashes:   true,
				restoreStrategy: liveHardwareRestoreEEPROMImage,
			},
		},
		{
			name:   "bootstrap without baseline image is destructive",
			action: liveWriteActionEEPROMBootstrap,
			baseline: liveHardwareBaseline{
				eeprom: &eepromDownloaderState{provisioned: false},
			},
			want: liveHardwareRestorePlan{
				action:          liveWriteActionEEPROMBootstrap,
				safety:          liveSerialSafetyDestructive,
				captureEEPROM:   true,
				captureHashes:   true,
				restoreStrategy: liveHardwareRestoreNone,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := planLiveHardwareRestore(tc.action, tc.baseline)
			if got.action != tc.want.action {
				t.Fatalf("plan action = %q, want %q", got.action, tc.want.action)
			}
			if got.safety != tc.want.safety {
				t.Fatalf("plan safety = %q, want %q", got.safety, tc.want.safety)
			}
			if got.captureEEPROM != tc.want.captureEEPROM {
				t.Fatalf("plan captureEEPROM = %v, want %v", got.captureEEPROM, tc.want.captureEEPROM)
			}
			if got.captureHashes != tc.want.captureHashes {
				t.Fatalf("plan captureHashes = %v, want %v", got.captureHashes, tc.want.captureHashes)
			}
			if got.restoreStrategy != tc.want.restoreStrategy {
				t.Fatalf("plan restoreStrategy = %q, want %q", got.restoreStrategy, tc.want.restoreStrategy)
			}
		})
	}

	t.Run("firmware hash set without captured target hash becomes destructive", func(t *testing.T) {
		t.Parallel()

		got := planLiveHardwareRestore(liveWriteActionFirmwareHashSet, liveHardwareBaseline{
			eeprom: &eepromDownloaderState{provisioned: true},
			hashes: &rnodeHashSnapshot{},
		})
		if got.safety != liveSerialSafetyDestructive {
			t.Fatalf("plan safety = %q, want %q", got.safety, liveSerialSafetyDestructive)
		}
		if got.restoreStrategy != liveHardwareRestoreNone {
			t.Fatalf("plan restoreStrategy = %q, want %q", got.restoreStrategy, liveHardwareRestoreNone)
		}
		if !strings.Contains(got.reason, "target firmware hash") {
			t.Fatalf("plan reason = %q, want mention of target firmware hash", got.reason)
		}
	})
}

func TestLiveWriteGateRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan liveHardwareRestorePlan
		want liveWriteGate
	}{
		{
			name: "reversible write uses write gate",
			plan: liveHardwareRestorePlan{
				safety: liveSerialSafetyReversibleWrite,
			},
			want: liveWriteGateWrites,
		},
		{
			name: "destructive write uses destructive gate",
			plan: liveHardwareRestorePlan{
				safety: liveSerialSafetyDestructive,
			},
			want: liveWriteGateDestructive,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := liveWriteGateRequirements(tc.plan); got != tc.want {
				t.Fatalf("liveWriteGateRequirements() = %q, want %q", got, tc.want)
			}
		})
	}
}

func bytesOfLen(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i + 1)
	}
	return out
}
