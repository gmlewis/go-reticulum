// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// olcTolerance is how far apart two decoded degrees may be while still counting
// as the same corner. The codec works in integers and rounds to fourteen
// decimal places, so the only difference a correct implementation can show is
// floating-point representation noise.
const olcTolerance = 1e-9

// closeTo reports whether two degrees agree to within olcTolerance.
func closeTo(got, want float64) bool {
	return math.Abs(got-want) <= olcTolerance
}

// TestEncodeOLCMatchesReferenceGoldens asserts encoding reproduces the official
// reference implementation exactly, for every case that implementation ships.
//
// Two comparisons are made. The integer form is compared against the reference
// test data's own expected code, which is exact. The degree form is compared
// against the code the reference implementation produces from the same degrees,
// which pins the float path — including its floating-point rounding — to the
// reference rather than to an approximation of it.
func TestEncodeOLCMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	if len(olcEncodingGoldens) == 0 {
		t.Fatal("no encoding goldens")
	}
	for _, tc := range olcEncodingGoldens {
		got, err := encodeOLCIntegers(tc.LatInt, tc.LngInt, tc.Length)
		if err != nil {
			t.Errorf("encodeOLCIntegers(%v, %v, %v): %v", tc.LatInt, tc.LngInt, tc.Length, err)
			continue
		}
		if got != tc.Code {
			t.Errorf("encodeOLCIntegers(%v, %v, %v) = %q, want %q",
				tc.LatInt, tc.LngInt, tc.Length, got, tc.Code)
		}

		fromDegrees, err := EncodeOLC(tc.Lat, tc.Lng, tc.Length)
		if err != nil {
			t.Errorf("EncodeOLC(%v, %v, %v): %v", tc.Lat, tc.Lng, tc.Length, err)
			continue
		}
		if fromDegrees != tc.FloatCode {
			t.Errorf("EncodeOLC(%v, %v, %v) = %q, the reference produces %q",
				tc.Lat, tc.Lng, tc.Length, fromDegrees, tc.FloatCode)
		}
	}
}

// TestDecodeOLCMatchesReferenceGoldens asserts the decoder returns exactly the
// four corners the reference test data requires for every code it ships.
func TestDecodeOLCMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	if len(olcDecodingGoldens) == 0 {
		t.Fatal("no decoding goldens")
	}
	for _, tc := range olcDecodingGoldens {
		area, err := DecodeOLC(tc.Code)
		if err != nil {
			t.Errorf("DecodeOLC(%q): %v", tc.Code, err)
			continue
		}
		if !closeTo(area.South, tc.South) || !closeTo(area.West, tc.West) ||
			!closeTo(area.North, tc.North) || !closeTo(area.East, tc.East) {
			t.Errorf("DecodeOLC(%q) = [%v, %v, %v, %v], want [%v, %v, %v, %v]",
				tc.Code, area.South, area.West, area.North, area.East,
				tc.South, tc.West, tc.North, tc.East)
		}
	}
}

// TestOLCValidityMatchesReferenceGoldens asserts the three validity predicates
// agree with the reference for every case it ships, including the malformed
// codes that must be rejected.
func TestOLCValidityMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	if len(olcValidityGoldens) == 0 {
		t.Fatal("no validity goldens")
	}
	for _, tc := range olcValidityGoldens {
		if got := IsValidOLC(tc.Code); got != tc.Valid {
			t.Errorf("IsValidOLC(%q) = %v, want %v", tc.Code, got, tc.Valid)
		}
		if got := IsShortOLC(tc.Code); got != tc.Short {
			t.Errorf("IsShortOLC(%q) = %v, want %v", tc.Code, got, tc.Short)
		}
		if got := IsFullOLC(tc.Code); got != tc.Full {
			t.Errorf("IsFullOLC(%q) = %v, want %v", tc.Code, got, tc.Full)
		}
	}
}

// TestRecoverNearestOLCMatchesReferenceGoldens asserts a short code is restored
// to the exact full code the reference restores from the same reference point.
func TestRecoverNearestOLCMatchesReferenceGoldens(t *testing.T) {
	t.Parallel()

	if len(olcShortGoldens) == 0 {
		t.Fatal("no short-code goldens")
	}
	for _, tc := range olcShortGoldens {
		if tc.Short == tc.Full {
			// The reference data marks such a row as "shorten only"; there is
			// nothing to recover.
			continue
		}
		got, err := RecoverNearestOLC(tc.Short, LatLng{Lat: tc.Lat, Lng: tc.Lng})
		if err != nil {
			t.Errorf("RecoverNearestOLC(%q, %v): %v", tc.Short, tc.Lat, err)
			continue
		}
		if got != tc.Full {
			t.Errorf("RecoverNearestOLC(%q, %v, %v) = %q, want %q", tc.Short, tc.Lat, tc.Lng, got, tc.Full)
		}
	}
}

// TestEncodeOLCKnownPoints asserts the codes the Open Location Code
// documentation publishes for well-known places, so the codec is anchored to
// more than its own reference data file.
func TestEncodeOLCKnownPoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		lat    float64
		lng    float64
		length int
		want   string
	}{
		{"Googleplex", 37.422, -122.0841, 10, "849VCWC8+Q9"},
		{"Googleplex refined", 37.422, -122.0841, 11, "849VCWC8+Q9R"},
		{"Sydney Opera House", -33.8568, 151.2153, 10, "4RRH46V8+74"},
		{"Eiffel Tower", 48.8583, 2.2945, 10, "8FW4V75V+8R"},
		{"Mount Everest", 27.988056, 86.925278, 10, "7MV8XWQG+64"},
		{"Prime meridian and equator", 0, 0, 10, "6FG22222+22"},
		{"South pole", -90, 0, 10, "2F222222+22"},
		{"North pole at the antimeridian", 90, 180, 10, "C2X2X2X2+X2"},
		{"Zurich", 47.365590, 8.524997, 10, "8FVC9G8F+6X"},
		{"Zurich short", 47.365590, 8.524997, 6, "8FVC9G00+"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EncodeOLC(tc.lat, tc.lng, tc.length)
			if err != nil {
				t.Fatalf("EncodeOLC: %v", err)
			}
			if got != tc.want {
				t.Errorf("EncodeOLC(%v, %v, %v) = %q, want %q", tc.lat, tc.lng, tc.length, got, tc.want)
			}
		})
	}
}

// TestDecodeOLCContainsItsOwnEncoding asserts the defining property of the
// codec: the coordinate a code was produced from lies inside the area that code
// decodes to. A codec that encoded and decoded through different arithmetic
// would drift out of its own cell.
func TestDecodeOLCContainsItsOwnEncoding(t *testing.T) {
	t.Parallel()

	for _, tc := range olcEncodingGoldens {
		area, err := DecodeOLC(tc.FloatCode)
		if err != nil {
			t.Errorf("DecodeOLC(%q) from (%v, %v): %v", tc.FloatCode, tc.Lat, tc.Lng, err)
			continue
		}
		// A length past the maximum is clamped before encoding, so the code that
		// comes back carries at most olcMaxDigits significant characters.
		if want := min(tc.Length, olcMaxDigits); area.Len != want {
			t.Errorf("DecodeOLC(%q).Len = %v, want %v", tc.FloatCode, area.Len, want)
		}
		// The codec clips a latitude and wraps a longitude before encoding, so
		// the coordinate the area must contain is the normalized one.
		lat := clipLatitude(tc.Lat)
		lng := normalizeLongitude(tc.Lng)
		if lat < area.South-olcTolerance || lat > area.North+olcTolerance {
			t.Errorf("DecodeOLC(%q) latitude %.10f outside [%.10f, %.10f]",
				tc.FloatCode, lat, area.South, area.North)
		}
		if lng < area.West-olcTolerance || lng > area.East+olcTolerance {
			t.Errorf("DecodeOLC(%q) longitude %.10f outside [%.10f, %.10f]",
				tc.FloatCode, lng, area.West, area.East)
		}
	}
}

// TestDecodeOLCPrecision asserts the cell sizes the specification promises, so
// a truncated or mis-scaled codec cannot pass the containment test by decoding
// every code to a single oversized cell.
func TestDecodeOLCPrecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code      string
		latSpan   float64
		lngSpan   float64
		south     float64
		west      float64
		centerLat float64
		centerLng float64
	}{
		{
			code: "849VCWC8+Q9", latSpan: 0.000125, lngSpan: 0.000125,
			south: 37.421875, west: -122.084125,
			centerLat: 37.4219375, centerLng: -122.0840625,
		},
		{
			code: "849VCWC8+Q9R", latSpan: 0.000025, lngSpan: 0.00003125,
			south: 37.421975, west: -122.084125,
			centerLat: 37.4219875, centerLng: -122.084109375,
		},
	}
	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			area, err := DecodeOLC(tc.code)
			if err != nil {
				t.Fatalf("DecodeOLC: %v", err)
			}
			if !closeTo(area.North-area.South, tc.latSpan) {
				t.Errorf("latitude span = %v, want %v", area.North-area.South, tc.latSpan)
			}
			if !closeTo(area.East-area.West, tc.lngSpan) {
				t.Errorf("longitude span = %v, want %v", area.East-area.West, tc.lngSpan)
			}
			if !closeTo(area.South, tc.south) {
				t.Errorf("South = %.10f, want %.10f", area.South, tc.south)
			}
			if !closeTo(area.West, tc.west) {
				t.Errorf("West = %.10f, want %.10f", area.West, tc.west)
			}
			center := area.Center()
			if !closeTo(center.Lat, tc.centerLat) {
				t.Errorf("Center().Lat = %.10f, want %.10f", center.Lat, tc.centerLat)
			}
			if !closeTo(center.Lng, tc.centerLng) {
				t.Errorf("Center().Lng = %.10f, want %.10f", center.Lng, tc.centerLng)
			}
		})
	}
}

// TestEncodeOLCLengthValidation asserts the lengths the specification defines
// are accepted and that an undefined odd length below the pair section is
// refused rather than silently rounded.
func TestEncodeOLCLengthValidation(t *testing.T) {
	t.Parallel()

	for _, length := range []int{2, 4, 6, 8, 10, 11, 12, 13, 14, 15} {
		code, err := EncodeOLC(47.365590, 8.524997, length)
		if err != nil {
			t.Errorf("EncodeOLC(length %v): %v", length, err)
			continue
		}
		if !IsValidOLC(code) {
			t.Errorf("EncodeOLC(length %v) = %q, which is not a valid code", length, code)
		}
		if got := strings.Count(code, string(olcSeparator)); got != 1 {
			t.Errorf("EncodeOLC(length %v) = %q, carrying %v separators", length, code, got)
		}
	}
	for _, length := range []int{-1, 0, 1, 3, 5, 7, 9} {
		if _, err := EncodeOLC(0, 0, length); !errors.Is(err, ErrOLCInvalidLength) {
			t.Errorf("EncodeOLC(length %v) error = %v, want ErrOLCInvalidLength", length, err)
		}
	}
	// A length past the maximum is clamped, not refused: the specification's own
	// encoder does the same.
	code, err := EncodeOLC(47.365590, 8.524997, 40)
	if err != nil {
		t.Fatalf("EncodeOLC(40): %v", err)
	}
	if got := len(olcClean(code)); got != olcMaxDigits {
		t.Errorf("EncodeOLC(40) has %v significant characters, want %v", got, olcMaxDigits)
	}
}

// TestEncodeOLCRoundTrips asserts every defined length survives encode then
// decode with the same significant-character count.
func TestEncodeOLCRoundTrips(t *testing.T) {
	t.Parallel()

	for _, length := range []int{2, 4, 6, 8, 10, 11, 12, 13, 14, 15} {
		code, err := EncodeOLC(-33.8568, 151.2153, length)
		if err != nil {
			t.Fatalf("EncodeOLC(length %v): %v", length, err)
		}
		area, err := DecodeOLC(code)
		if err != nil {
			t.Fatalf("DecodeOLC(%q): %v", code, err)
		}
		if area.Len != length {
			t.Errorf("length %v round-tripped to Len %v (%q)", length, area.Len, code)
		}
		// The area must contain the original coordinate for every length.
		if -33.8568 < area.South-olcTolerance || -33.8568 > area.North+olcTolerance {
			t.Errorf("length %v: latitude outside %q", length, code)
		}
		if 151.2153 < area.West-olcTolerance || 151.2153 > area.East+olcTolerance {
			t.Errorf("length %v: longitude outside %q", length, code)
		}
	}
}

// TestDecodeOLCRejectsNonFullCodes asserts a short or malformed code is refused
// by DecodeOLC: without a reference location a short code is meaningless, and
// silently guessing would place the answer in the wrong hemisphere.
func TestDecodeOLCRejectsNonFullCodes(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"", "INVALID", "8F+6X", "9G8F+6X", "849VCWC8+R", "+", "0"} {
		if _, err := DecodeOLC(code); !errors.Is(err, ErrOLCNotFull) {
			t.Errorf("DecodeOLC(%q) error = %v, want ErrOLCNotFull", code, err)
		}
	}
	// An eight-digit code carries no padding and no grid refinement, so it is a
	// full code and decodes to the coarser area it names.
	area, err := DecodeOLC("849VCWC8+")
	if err != nil {
		t.Fatalf("DecodeOLC(8-digit): %v", err)
	}
	if area.Len != 8 {
		t.Errorf("DecodeOLC(8-digit).Len = %v, want 8", area.Len)
	}
	if !closeTo(area.North-area.South, 0.0025) {
		t.Errorf("8-digit latitude span = %v, want 0.0025", area.North-area.South)
	}
}

// TestRecoverNearestOLCRefusesInvalidInput asserts only a real short code is
// recovered, and that a full code is passed through unchanged.
func TestRecoverNearestOLCRefusesInvalidInput(t *testing.T) {
	t.Parallel()

	got, err := RecoverNearestOLC("8fvc9g8f+6x", LatLng{Lat: 47.4, Lng: 8.5})
	if err != nil {
		t.Fatalf("RecoverNearestOLC(full): %v", err)
	}
	if got != "8FVC9G8F+6X" {
		t.Errorf("RecoverNearestOLC(full) = %q, want the code upper-cased", got)
	}
	for _, code := range []string{"", "NOTACODE", "8fvc9g8f+6x"} {
		if code == "8fvc9g8f+6x" {
			continue
		}
		if _, err := RecoverNearestOLC(code, LatLng{Lat: 47.4, Lng: 8.5}); !errors.Is(err, ErrOLCNotShort) {
			t.Errorf("RecoverNearestOLC(%q) error = %v, want ErrOLCNotShort", code, err)
		}
	}
}

// TestCodeAreaCenterIsTheMiddleOfTheArea asserts Center is the geometric middle,
// which is what a user is shown when the bot reports a code as a place.
func TestCodeAreaCenterIsTheMiddleOfTheArea(t *testing.T) {
	t.Parallel()

	area := CodeArea{South: 10, West: 20, North: 11, East: 22, Len: 4}
	center := area.Center()
	if !closeTo(center.Lat, 10.5) {
		t.Errorf("Center().Lat = %v, want 10.5", center.Lat)
	}
	if !closeTo(center.Lng, 21) {
		t.Errorf("Center().Lng = %v, want 21", center.Lng)
	}
}
