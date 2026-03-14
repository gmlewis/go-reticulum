// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// Constants defining the rigid structural boundaries and escape markers required by the KISS (Keep It Simple, Stupid) framing protocol.
const (
	// KISSFend designates the Frame End marker, definitively signaling the start or conclusion of a discrete data packet.
	KISSFend    = 0xC0
	// KISSFesc specifies the Frame Escape character, utilized to safely obfuscate control bytes appearing within the payload.
	KISSFesc    = 0xDB
	// KISSTfend is the Transposed Frame End byte, structurally substituted into the stream when a literal Fend appears in the data.
	KISSTfend   = 0xDC
	// KISSTfesc is the Transposed Frame Escape byte, substituted when a literal Fesc is encountered in the payload.
	KISSTfesc   = 0xDD
	// KISSCmdData instructs the TNC that the accompanying payload consists of standard, routable network data rather than control commands.
	KISSCmdData = 0x00
)

// KISSEscape aggressively scans and re-encodes a raw binary payload to strictly adhere to the KISS protocol's byte-stuffing rules.
// It safely neutralizes internal boundary markers, yielding a buffer that can be reliably transmitted over raw serial links without triggering premature frame truncation.
func KISSEscape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case KISSFesc:
			out = append(out, KISSFesc, KISSTfesc)
		case KISSFend:
			out = append(out, KISSFesc, KISSTfend)
		default:
			out = append(out, b)
		}
	}
	return out
}

// KISSUnescape carefully parses a received byte stream, stripping away KISS framing escapes to restore the original payload.
// It structurally reverses the obfuscation introduced during transmission, delivering uncorrupted data to the upper network layers.
func KISSUnescape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	escape := false
	for _, b := range data {
		if escape {
			switch b {
			case KISSTfend:
				out = append(out, KISSFend)
			case KISSTfesc:
				out = append(out, KISSFesc)
			}
			escape = false
		} else if b == KISSFesc {
			escape = true
		} else {
			out = append(out, b)
		}
	}
	return out
}
