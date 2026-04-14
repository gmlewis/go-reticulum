// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

const (
	kissFend  = 0xc0
	kissFesc  = 0xdb
	kissTfend = 0xdc
	kissTfesc = 0xdd

	rnodeKISSCommandUnknown         = 0xfe
	rnodeKISSCommandFrequency       = 0x01
	rnodeKISSCommandBandwidth       = 0x02
	rnodeKISSCommandPlatform        = 0x48
	rnodeKISSCommandFWVersion       = 0x50
	rnodeKISSCommandROMRead         = 0x51
	rnodeKISSCommandDevHash         = 0x56
	rnodeKISSCommandDeviceSignature = 0x57
	rnodeKISSCommandHashes          = 0x60
	rnodeKISSCommandCFGRead         = 0x6d
	rnodeKISSCommandData            = 0x00
)

func kissEscape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case kissFesc:
			out = append(out, kissFesc, kissTfesc)
		case kissFend:
			out = append(out, kissFesc, kissTfend)
		default:
			out = append(out, b)
		}
	}
	return out
}
