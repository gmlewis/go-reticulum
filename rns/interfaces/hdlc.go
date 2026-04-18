// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// HDLCFlag defines the High-Level Data Link Control (HDLC) frame boundary
// marker. It is the synchronization primitive used to assert the start and
// end of discrete packets over raw serial interfaces.
const HDLCFlag = 0x7E

// HDLCEsc escapes reserved bytes inside an HDLC payload.
// It prevents embedded flag markers from being misinterpreted
// as structural boundaries.
const HDLCEsc = 0x7D

// HDLCEscMask is XORed with escaped bytes in an HDLC frame.
// It ensures escaped bytes are transformed safely for transport.
const HDLCEscMask = 0x20

// HDLCEscape scans and reformats a binary payload to comply with HDLC framing
// constraints. It replaces flag and escape characters so the payload is safe for
// hardware transmission.
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

// HDLCUnescape parses a raw byte stream and removes escape markers to restore
// the original payload. It reverses the obfuscation applied for transmission.
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
