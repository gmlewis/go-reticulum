// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// HDLC utilities
const (
	HDLCFlag    = 0x7E
	HDLCEsc     = 0x7D
	HDLCEscMask = 0x20
)

func HDLCEscape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case HDLCEsc:
			out = append(out, HDLCEsc, HDLCEsc^HDLCEscMask)
		case HDLCFlag:
			out = append(out, HDLCEsc, HDLCFlag^HDLCEscMask)
		default:
			out = append(out, b)
		}
	}
	return out
}

func HDLCUnescape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	escape := false
	for _, b := range data {
		if b == HDLCEsc && !escape {
			escape = true
			continue
		}
		if escape {
			out = append(out, b^HDLCEscMask)
			escape = false
		} else {
			out = append(out, b)
		}
	}
	return out
}
