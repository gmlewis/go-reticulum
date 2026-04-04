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
