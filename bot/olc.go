// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file implements Open Location Codes (Plus Codes) in pure Go, with no
// dependency outside the standard library, so a bot on a mesh link can turn a
// coordinate into a short, speakable, error-resistant string and back without
// any network service at all. That property is the whole point in the field:
// a Plus Code survives being read out over a 1 kbps voice or packet link, and
// it decodes to an exact area with no map, no lookup table, and no server.
//
// The algorithm is the published Open Location Code specification: a base-20
// alphabet excluding vowels and easily confused glyphs, five latitude/longitude
// pairs followed by an optional 5x4 grid refinement. The integer arithmetic
// mirrors the reference implementation exactly, so a code this bot produces is
// byte-for-byte the code every other implementation produces for the same
// coordinate, and vice versa.

package bot

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// Open Location Code constants, named as the specification names them.
const (
	// olcSeparator splits a code into its two halves.
	olcSeparator = '+'
	// olcSeparatorStr is the separator as a string, for the string functions.
	olcSeparatorStr = "+"
	// olcPadding pads a code shorter than eight digits.
	olcPadding = '0'
	// olcAlphabet is the base-20 character set: the digits 2-9 and the
	// consonants that remain once vowels and the easily confused L, B, S and D
	// are removed.
	olcAlphabet = "23456789CFGHJMPQRVWX"
	// olcEncodingBase is the length of the alphabet.
	olcEncodingBase = 20
	// olcLatMax and olcLngMax are the latitude and longitude bounds.
	olcLatMax = 90.0
	olcLngMax = 180.0
	// olcMinDigits and olcMaxDigits bound a code's significant characters.
	olcMinDigits = 2
	olcMaxDigits = 15
	// olcPairDigits is the number of characters the latitude/longitude pair
	// section uses; a code with more than this many digits carries grid
	// refinements.
	olcPairDigits = 10
	// olcSeparatorPos is where the separator sits in a full code.
	olcSeparatorPos = 8
	// olcGridRows and olcGridCols are the refinement grid's shape: five rows of
	// latitude by four columns of longitude, which is what makes each grid
	// digit a single character.
	olcGridRows = 5
	olcGridCols = 4
	// olcGridDigits is how many refinement digits a full-length code may carry.
	olcGridDigits = olcMaxDigits - olcPairDigits
	// olcPairPrecision is the scale of the pair section's integer form: 20^3.
	olcPairPrecision = 8000
	// olcFinalLatPrecision and olcFinalLngPrecision scale a coordinate to the
	// finest grid cell: 8000 * 5^5 and 8000 * 4^5.
	olcFinalLatPrecision = 25000000
	olcFinalLngPrecision = 8192000
	// olcPairFirstPlaceValue is the place value of the first pair: 20^4.
	olcPairFirstPlaceValue = 160000
	// olcGridLatFirstPlaceValue and olcGridLngFirstPlaceValue are the place
	// values of the first grid digit: 5^4 and 4^4.
	olcGridLatFirstPlaceValue = 625
	olcGridLngFirstPlaceValue = 256
	// olcPairShiftLat and olcPairShiftLng drop the finest five place values
	// when a code has no grid section: 5^5 and 4^5.
	olcPairShiftLat = 3125
	olcPairShiftLng = 1024
	// olcRoundDigits is the number of decimal places decoded corners are
	// rounded to, which removes floating-point representation noise.
	olcRoundDigits = 14
)

// LatLng is a WGS-84 coordinate in signed decimal degrees. It is the currency
// of every navigation command: Plus Codes, Maidenhead grids, DMS text, and the
// geodesy functions all meet here.
type LatLng struct {
	// Lat is the latitude in degrees, -90 (south) to 90 (north).
	Lat float64
	// Lng is the longitude in degrees, -180 (west) to 180 (east).
	Lng float64
}

// CodeArea is the rectangle an Open Location Code identifies, plus the number of
// significant characters the code carried. A code is an area, never a point: a
// ten-character code is about 13.9 m by 13.9 m at the equator, and reporting a
// center without the extent would overstate what the code knows.
type CodeArea struct {
	// South and West are the coordinates of the south-west corner.
	South float64
	West  float64
	// North and East are the coordinates of the north-east corner.
	North float64
	East  float64
	// Len is the number of significant characters, separator excluded.
	Len int
}

// Center returns the coordinate at the middle of the area, clamped to the legal
// latitude and longitude range.
func (c CodeArea) Center() LatLng {
	return LatLng{
		Lat: math.Min(c.South+(c.North-c.South)/2, olcLatMax),
		Lng: math.Min(c.West+(c.East-c.West)/2, olcLngMax),
	}
}

// Errors the Open Location Code functions report. They are sentinels so a
// caller can tell a malformed code from a coordinate out of range.
var (
	// ErrOLCInvalidLength reports a code length the specification does not
	// define: an odd length below the pair section, or one outside 2..15.
	ErrOLCInvalidLength = errors.New("open location code: invalid code length")
	// ErrOLCInvalid reports a string that is not a well-formed code at all.
	ErrOLCInvalid = errors.New("open location code: not a valid code")
	// ErrOLCNotFull reports a short code where a full one is required.
	ErrOLCNotFull = errors.New("open location code: not a valid full code")
	// ErrOLCNotShort reports a full or malformed code where a short one is
	// required.
	ErrOLCNotShort = errors.New("open location code: not a valid short code")
)

// EncodeOLC encodes a coordinate into an Open Location Code of codeLen
// significant characters. Latitudes outside -90..90 are clipped and longitudes
// are normalized into -180..180, exactly as the specification prescribes, so a
// coordinate read off a map edge still yields a usable code. A codeLen the
// specification does not define is rejected rather than silently rounded.
func EncodeOLC(lat, lng float64, codeLen int) (string, error) {
	latVal, lngVal := olcIntegers(lat, lng)
	return encodeOLCIntegers(latVal, lngVal, codeLen)
}

// encodeOLCIntegers is the encoder's core, working in the specification's
// integer domain. Keeping it separate from the degree conversion is what makes
// the exact reference test data testable: the integer form is where the
// specification's arithmetic is defined, and the float path adds nothing but
// the conversion above it.
func encodeOLCIntegers(latVal, lngVal int64, codeLen int) (string, error) {
	if codeLen < olcMinDigits || (codeLen < olcPairDigits && codeLen%2 == 1) {
		return "", fmt.Errorf("%w: %v", ErrOLCInvalidLength, codeLen)
	}
	if codeLen > olcMaxDigits {
		codeLen = olcMaxDigits
	}

	// The digits are produced least significant first, so each one is pushed
	// onto the front of the result. Fifteen characters is the maximum, which
	// keeps the prepend cheap.
	code := make([]byte, 0, olcMaxDigits)
	if codeLen > olcPairDigits {
		for range olcGridDigits {
			latDigit := latVal % olcGridRows
			lngDigit := lngVal % olcGridCols
			code = append([]byte{olcAlphabet[latDigit*olcGridCols+lngDigit]}, code...)
			latVal /= olcGridRows
			lngVal /= olcGridCols
		}
	} else {
		latVal /= olcPairShiftLat
		lngVal /= olcPairShiftLng
	}
	for range olcPairDigits / 2 {
		code = append([]byte{olcAlphabet[lngVal%olcEncodingBase]}, code...)
		code = append([]byte{olcAlphabet[latVal%olcEncodingBase]}, code...)
		latVal /= olcEncodingBase
		lngVal /= olcEncodingBase
	}

	text := string(code[:olcSeparatorPos]) + olcSeparatorStr + string(code[olcSeparatorPos:])
	if codeLen >= olcSeparatorPos {
		return text[:codeLen+1], nil
	}
	// A code shorter than the separator position is padded out to it, so the
	// separator always lands in the same place a reader expects.
	return text[:codeLen] + strings.Repeat(string(olcPadding), olcSeparatorPos-codeLen) + olcSeparatorStr, nil
}

// DecodeOLC decodes a full Open Location Code into the area it identifies.
// Short codes are rejected: a short code is only meaningful next to a reference
// location, and RecoverNearestOLC is what supplies one.
func DecodeOLC(code string) (CodeArea, error) {
	if !IsFullOLC(code) {
		return CodeArea{}, fmt.Errorf("%w: %q", ErrOLCNotFull, code)
	}
	cleaned := olcClean(code)
	if len(cleaned) > olcMaxDigits {
		cleaned = cleaned[:olcMaxDigits]
	}

	// Work in the specification's integer domain so the arithmetic is exact and
	// two implementations cannot drift apart by a rounding step.
	normalLat := int64(-olcLatMax * olcPairPrecision)
	normalLng := int64(-olcLngMax * olcPairPrecision)
	pv := int64(olcPairFirstPlaceValue)
	pairs := min(len(cleaned), olcPairDigits)
	for i := 0; i < pairs; i += 2 {
		normalLat += int64(strings.IndexByte(olcAlphabet, cleaned[i])) * pv
		normalLng += int64(strings.IndexByte(olcAlphabet, cleaned[i+1])) * pv
		if i < pairs-2 {
			pv /= olcEncodingBase
		}
	}
	latPrecision := float64(pv) / olcPairPrecision
	lngPrecision := latPrecision

	var gridLat, gridLng int64
	if len(cleaned) > olcPairDigits {
		rowpv := int64(olcGridLatFirstPlaceValue)
		colpv := int64(olcGridLngFirstPlaceValue)
		digits := min(len(cleaned), olcMaxDigits)
		for i := olcPairDigits; i < digits; i++ {
			digitVal := int64(strings.IndexByte(olcAlphabet, cleaned[i]))
			row := digitVal / olcGridCols
			col := digitVal % olcGridCols
			gridLat += row * rowpv
			gridLng += col * colpv
			if i < digits-1 {
				rowpv /= olcGridRows
				colpv /= olcGridCols
			}
		}
		latPrecision = float64(rowpv) / olcFinalLatPrecision
		lngPrecision = float64(colpv) / olcFinalLngPrecision
	}

	lat := float64(normalLat)/olcPairPrecision + float64(gridLat)/olcFinalLatPrecision
	lng := float64(normalLng)/olcPairPrecision + float64(gridLng)/olcFinalLngPrecision
	return CodeArea{
		South: olcRound(lat),
		West:  olcRound(lng),
		North: olcRound(lat + latPrecision),
		East:  olcRound(lng + lngPrecision),
		Len:   len(cleaned),
	}, nil
}

// IsValidOLC reports whether a string is a well-formed Open Location Code: at
// most one separator, in an even position up to the eighth character, with
// padding only in front of it and the alphabet's characters everywhere else.
func IsValidOLC(code string) bool {
	if strings.Count(code, olcSeparatorStr) > 1 {
		return false
	}
	if len(code) == 1 {
		return false
	}
	sep := strings.IndexByte(code, olcSeparator)
	if sep == -1 || sep > olcSeparatorPos || sep%2 == 1 {
		return false
	}
	if pad := strings.IndexByte(code, olcPadding); pad != -1 {
		// Padding only ever appears in front of the separator, never first, and
		// always as one even-length run that runs up to the separator.
		if sep < olcSeparatorPos || pad == 0 {
			return false
		}
		pads := code[pad : strings.LastIndexByte(code, olcPadding)+1]
		if len(pads)%2 == 1 || strings.Count(pads, string(olcPadding)) != len(pads) {
			return false
		}
		if !strings.HasSuffix(code, olcSeparatorStr) {
			return false
		}
	}
	// A single character after the separator is not a legal code: it would give
	// the area a 20:1 aspect ratio.
	if len(code)-sep-1 == 1 {
		return false
	}
	for _, r := range code {
		if r == olcSeparator || r == olcPadding {
			continue
		}
		if !strings.ContainsRune(olcAlphabet, unicode.ToUpper(r)) {
			return false
		}
	}
	return true
}

// IsShortOLC reports whether a valid code is a short one, meaning the digits
// that identify the region have been removed and a reference location is needed
// to recover them.
func IsShortOLC(code string) bool {
	if !IsValidOLC(code) {
		return false
	}
	sep := strings.IndexByte(code, olcSeparator)
	return sep >= 0 && sep < olcSeparatorPos
}

// IsFullOLC reports whether a valid code is self-contained: it names a region,
// so it decodes without any reference location. A full code's first character
// can never encode a latitude of 90 degrees or more, nor its second a longitude
// of 180 or more, which is what rejects the few alphabet combinations that
// would otherwise decode off the map.
func IsFullOLC(code string) bool {
	if !IsValidOLC(code) || IsShortOLC(code) {
		return false
	}
	firstLat := strings.IndexByte(olcAlphabet, byte(unicode.ToUpper(rune(code[0]))))
	if firstLat*olcEncodingBase >= olcLatMax*2 {
		return false
	}
	if len(code) > 1 {
		firstLng := strings.IndexByte(olcAlphabet, byte(unicode.ToUpper(rune(code[1]))))
		if firstLng*olcEncodingBase >= int(olcLngMax)*2 {
			return false
		}
	}
	return true
}

// RecoverNearestOLC restores the missing region digits of a short code using a
// reference location, returning the nearest full code. A full code is returned
// upper-cased and otherwise unchanged, so a caller that is unsure which kind it
// holds can pass either.
func RecoverNearestOLC(shortCode string, reference LatLng) (string, error) {
	if IsFullOLC(shortCode) {
		return strings.ToUpper(shortCode), nil
	}
	if !IsShortOLC(shortCode) {
		return "", fmt.Errorf("%w: %q", ErrOLCNotShort, shortCode)
	}
	refLat := clipLatitude(reference.Lat)
	refLng := normalizeLongitude(reference.Lng)
	code := strings.ToUpper(shortCode)

	// The digits that were removed are exactly those in front of the separator;
	// the reference location supplies them, and the result is decoded and then
	// nudged by one cell when it landed on the far side of the reference.
	padding := olcSeparatorPos - strings.IndexByte(code, olcSeparator)
	resolution := math.Pow(olcEncodingBase, 2-float64(padding)/2)
	half := resolution / 2

	full, err := EncodeOLC(refLat, refLng, olcPairDigits)
	if err != nil {
		return "", err
	}
	area, err := DecodeOLC(full[:padding] + code)
	if err != nil {
		return "", err
	}
	center := area.Center()
	switch {
	case refLat+half < center.Lat && center.Lat-resolution >= -olcLatMax:
		center.Lat -= resolution
	case refLat-half > center.Lat && center.Lat+resolution <= olcLatMax:
		center.Lat += resolution
	}
	switch {
	case refLng+half < center.Lng:
		center.Lng -= resolution
	case refLng-half > center.Lng:
		center.Lng += resolution
	}
	return EncodeOLC(center.Lat, center.Lng, area.Len)
}

// olcIntegers converts a coordinate into the specification's integer form: the
// latitude and longitude offset by their maxima and scaled to the finest grid
// cell, with the latitude clamped and the longitude wrapped.
func olcIntegers(lat, lng float64) (int64, int64) {
	latVal := int64(math.Floor(lat*olcFinalLatPrecision)) + int64(olcLatMax*olcFinalLatPrecision)
	latLimit := int64(2 * olcLatMax * olcFinalLatPrecision)
	switch {
	case latVal < 0:
		latVal = 0
	case latVal >= latLimit:
		latVal = latLimit - 1
	}

	lngVal := int64(math.Floor(lng*olcFinalLngPrecision)) + int64(olcLngMax*olcFinalLngPrecision)
	lngSpan := int64(2 * olcLngMax * olcFinalLngPrecision)
	lngVal = ((lngVal % lngSpan) + lngSpan) % lngSpan
	return latVal, lngVal
}

// olcClean strips the separator and padding from a code and upper-cases the
// remaining significant characters.
func olcClean(code string) string {
	var b strings.Builder
	b.Grow(len(code))
	for _, r := range code {
		if r == olcSeparator || r == olcPadding {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// olcRound rounds a decoded corner to the specification's working precision,
// which removes floating-point representation noise without moving the corner.
func olcRound(v float64) float64 {
	scale := math.Pow(10, olcRoundDigits)
	return math.Round(v*scale) / scale
}

// clipLatitude clamps a latitude into the legal -90..90 range.
func clipLatitude(lat float64) float64 {
	return math.Min(olcLatMax, math.Max(-olcLatMax, lat))
}

// normalizeLongitude wraps a longitude into -180..180, excluding 180.
func normalizeLongitude(lng float64) float64 {
	lng = math.Mod(lng+olcLngMax, 2*olcLngMax)
	if lng < 0 {
		lng += 2 * olcLngMax
	}
	return lng - olcLngMax
}
