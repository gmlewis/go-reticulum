// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file forwards the Open Location Code engine to package geo. The
// implementation moved there so that consumers outside the bot — such as the
// `` `L `` Micron extension in gonomadnet — can decode and render a Plus Code
// without importing the bot engine, its station catalogs, or the Reticulum
// stack. The names below are the bot's existing public surface and behave
// exactly as they did when the code lived here.

package bot

import "github.com/gmlewis/go-reticulum/geo"

// LatLng is a WGS-84 coordinate in signed decimal degrees. It is the currency
// every location notation in this package is converted to and from.
type LatLng = geo.LatLng

// CodeArea is the latitude/longitude bounding box an Open Location Code names.
type CodeArea = geo.CodeArea

// Errors the Open Location Code functions report. They are sentinels so a
// caller can tell a malformed code from a coordinate out of range.
var (
	// ErrOLCInvalidLength reports a code length the specification does not
	// define: an odd length below the pair section, or one outside 2..15.
	ErrOLCInvalidLength = geo.ErrOLCInvalidLength
	// ErrOLCInvalid reports a string that is not a well-formed code at all.
	ErrOLCInvalid = geo.ErrOLCInvalid
	// ErrOLCNotFull reports a short code where a full one is required.
	ErrOLCNotFull = geo.ErrOLCNotFull
	// ErrOLCNotShort reports a full or malformed code where a short one is
	// required.
	ErrOLCNotShort = geo.ErrOLCNotShort
)

// EncodeOLC encodes a coordinate into an Open Location Code of codeLen
// significant characters.
func EncodeOLC(lat, lng float64, codeLen int) (string, error) {
	return geo.EncodeOLC(lat, lng, codeLen)
}

// DecodeOLC decodes an Open Location Code into the area it names.
func DecodeOLC(code string) (CodeArea, error) { return geo.DecodeOLC(code) }

// IsValidOLC reports whether code is a well-formed Open Location Code.
func IsValidOLC(code string) bool { return geo.IsValidOLC(code) }

// IsShortOLC reports whether code is a valid shortened Open Location Code.
func IsShortOLC(code string) bool { return geo.IsShortOLC(code) }

// IsFullOLC reports whether code is a valid full Open Location Code.
func IsFullOLC(code string) bool { return geo.IsFullOLC(code) }

// RecoverNearestOLC expands a short code against a reference coordinate.
func RecoverNearestOLC(shortCode string, reference LatLng) (string, error) {
	return geo.RecoverNearestOLC(shortCode, reference)
}
