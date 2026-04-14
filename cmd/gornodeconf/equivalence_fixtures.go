// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

const (
	equivalencePlatformAVR   = 0x90
	equivalencePlatformESP32 = 0x80
	equivalencePlatformNRF52 = 0x70

	equivalenceMCU1284P = 0x91
	equivalenceMCU2560  = 0x92
	equivalenceMCUESP32 = 0x81
	equivalenceMCUNRF52 = 0x71

	equivalenceProductRNODE = 0x03
	equivalenceProductTBEAM = 0xe0
	equivalenceProductRAK   = 0x10
	equivalenceProductHMBRW = 0xf0

	equivalenceBoardRNODE   = 0x31
	equivalenceBoardESP32   = 0x35
	equivalenceBoardTBEAM   = 0x33
	equivalenceBoardRAK4631 = 0x51
	equivalenceBoardHMBRW   = 0x32
)

// equivalenceFixture describes one representative board/model scenario for the
// behavioral comparison harness.
type equivalenceFixture struct {
	name        string
	platform    byte
	mcu         byte
	product     byte
	board       byte
	model       byte
	manualFlash bool
}

func defaultEquivalenceFixtures() []equivalenceFixture {
	return []equivalenceFixture{
		{
			name:     "avr-rnode",
			platform: equivalencePlatformAVR,
			mcu:      equivalenceMCU1284P,
			product:  equivalenceProductRNODE,
			board:    equivalenceBoardRNODE,
			model:    0xa4,
		},
		{
			name:     "esp32-rnode",
			platform: equivalencePlatformESP32,
			mcu:      equivalenceMCUESP32,
			product:  equivalenceProductRNODE,
			board:    equivalenceBoardESP32,
			model:    0xa1,
		},
		{
			name:     "esp32-tbeam",
			platform: equivalencePlatformESP32,
			mcu:      equivalenceMCUESP32,
			product:  equivalenceProductTBEAM,
			board:    equivalenceBoardTBEAM,
			model:    0xe4,
		},
		{
			name:     "nrf52-rak4631",
			platform: equivalencePlatformNRF52,
			mcu:      equivalenceMCUNRF52,
			product:  equivalenceProductRAK,
			board:    equivalenceBoardRAK4631,
			model:    0x11,
		},
		{
			name:        "manual-flash",
			platform:    equivalencePlatformESP32,
			mcu:         equivalenceMCUESP32,
			product:     equivalenceProductHMBRW,
			board:       equivalenceBoardHMBRW,
			model:       0xff,
			manualFlash: true,
		},
	}
}
