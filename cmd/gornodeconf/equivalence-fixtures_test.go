// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import "testing"

func TestDefaultEquivalenceFixturesCoverRepresentativeScenarios(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	if len(fixtures) != 5 {
		t.Fatalf("fixture count mismatch: got %v want 5", len(fixtures))
	}

	want := map[string]equivalenceFixture{
		"avr-rnode":     {name: "avr-rnode", platform: equivalencePlatformAVR, mcu: equivalenceMCU1284P, product: equivalenceProductRNODE, board: equivalenceBoardRNODE, model: 0xa4},
		"esp32-rnode":   {name: "esp32-rnode", platform: equivalencePlatformESP32, mcu: equivalenceMCUESP32, product: equivalenceProductRNODE, board: equivalenceBoardESP32, model: 0xa1},
		"esp32-tbeam":   {name: "esp32-tbeam", platform: equivalencePlatformESP32, mcu: equivalenceMCUESP32, product: equivalenceProductTBEAM, board: equivalenceBoardTBEAM, model: 0xe4},
		"nrf52-rak4631": {name: "nrf52-rak4631", platform: equivalencePlatformNRF52, mcu: equivalenceMCUNRF52, product: equivalenceProductRAK, board: equivalenceBoardRAK4631, model: 0x11},
		"manual-flash":  {name: "manual-flash", platform: equivalencePlatformESP32, mcu: equivalenceMCUESP32, product: equivalenceProductHMBRW, board: equivalenceBoardHMBRW, model: 0xff, manualFlash: true},
	}

	for _, fixture := range fixtures {
		if got, ok := want[fixture.name]; !ok {
			t.Fatalf("unexpected fixture %q", fixture.name)
		} else if got != fixture {
			t.Fatalf("fixture %q mismatch:\n got: %#v\nwant: %#v", fixture.name, fixture, got)
		}
		delete(want, fixture.name)
	}
	if len(want) != 0 {
		t.Fatalf("missing fixtures: %#v", want)
	}
}
