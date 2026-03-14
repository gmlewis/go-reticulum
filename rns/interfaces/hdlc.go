// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// HDLCFlag strictly defines the standard High-Level Data Link Control frame boundary marker byte.
// It acts as the immutable synchronization primitive used to assert the start and conclusion of discrete packets over raw serial interfaces.
const HDLCFlag = 0x7E

// HDLCEsc specifies the requisite escape character deployed when escaping reserved bytes within a frame's data payload.
// It is mathematically applied to guarantee that embedded flag markers are not improperly interpreted as structural boundaries.
const HDLCEsc = 0x7D

// HDLCEscMask provides the XOR modifier utilized to cryptographically mangle escaped bytes within the HDLC stream.
// It ensures robust character obfuscation, allowing the transparent delivery of arbitrary binary payloads.
const HDLCEscMask = 0x20

// HDLCEscape mechanically scans and reformats an arbitrary binary payload to comply rigorously with HDLC framing constraints.
// It surgically replaces internal instances of the flag or escape characters, returning a physically safe byte slice ready for hardware transmission.
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

// HDLCUnescape efficiently parses an incoming raw byte stream, stripping away escape markers and restoring the mangled bytes to their pristine state.
// It structurally reverses the obfuscation applied prior to transmission, yielding the original, uncorrupted payload.
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
