// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// KISS utilities
const (
	KISSFend    = 0xC0
	KISSFesc    = 0xDB
	KISSTfend   = 0xDC
	KISSTfesc   = 0xDD
	KISSCmdData = 0x00
)

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
