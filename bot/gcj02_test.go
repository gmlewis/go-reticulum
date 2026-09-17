// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"fmt"
	"math"
	"testing"
)

// gcjReferenceTolerance is how close a converted coordinate must come to the
// published reference pair. A degree of latitude is about 111 km, so 1e-6
// degrees is roughly 0.1 m — far below the precision of any coordinate a person
// types, and far below the 300-500 m offset the conversion exists to remove.
const gcjReferenceTolerance = 1e-6

// TestIsInChinaCoversTheMandatedBoundingBox asserts the bounding box China
// mandates GCJ-02 within, including its edges, and that four well-known places
// outside it are refused.
func TestIsInChinaCoversTheMandatedBoundingBox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lat  float64
		lng  float64
		want bool
	}{
		{name: "Beijing", lat: 39.9042, lng: 116.4074, want: true},
		{name: "Shanghai", lat: 31.2335, lng: 121.5015, want: true},
		{name: "Lhasa", lat: 29.6520, lng: 91.1721, want: true},
		{name: "Urumqi", lat: 43.8256, lng: 87.6168, want: true},
		{name: "southwest corner", lat: 0.8293, lng: 72.004, want: true},
		{name: "northeast corner", lat: 55.8271, lng: 137.8347, want: true},
		{name: "just south of the box", lat: 0.8292, lng: 100.0, want: false},
		{name: "just west of the box", lat: 30.0, lng: 72.0039, want: false},
		{name: "New York", lat: 40.7128, lng: -74.0060, want: false},
		{name: "London", lat: 51.5074, lng: -0.1278, want: false},
		{name: "Tokyo", lat: 35.6762, lng: 139.6503, want: false},
		{name: "San Francisco", lat: 37.7553, lng: -122.4527, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsInChina(tt.lat, tt.lng); got != tt.want {
				t.Errorf("IsInChina(%v, %v) = %v, want %v", tt.lat, tt.lng, got, tt.want)
			}
		})
	}
}

// TestWGS84ToGCJ02MatchesPublishedReferences asserts the forward transform
// against coordinates published for three well-known sites, to the hundredth of
// a meter.
func TestWGS84ToGCJ02MatchesPublishedReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		wgsLat, wgsLng float64
		gcjLat, gcjLng float64
	}{
		{
			// Tiananmen Square, the reference pair every description of the
			// offset uses. GPS: 39.9055N 116.3976E.
			name: "Tiananmen Square", wgsLat: 39.9055, wgsLng: 116.3976,
			gcjLat: 39.9069033815, gcjLng: 116.4038431840,
		},
		{
			// Shanghai Tower, the tallest building in China.
			name: "Shanghai Tower", wgsLat: 31.2335, wgsLng: 121.5015,
			gcjLat: 31.2314780608, gcjLng: 121.5059260802,
		},
		{
			// Central Beijing: the coordinate a Gaode/Amap pin is placed at for
			// the city center.
			name: "Beijing city center", wgsLat: 39.9042, wgsLng: 116.4074,
			gcjLat: 39.9056033432, gcjLng: 116.4136422538,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotLat, gotLng := WGS84ToGCJ02(tt.wgsLat, tt.wgsLng)
			if math.Abs(gotLat-tt.gcjLat) > gcjReferenceTolerance {
				t.Errorf("WGS84ToGCJ02(%v, %v) lat = %v, want %v±%v",
					tt.wgsLat, tt.wgsLng, gotLat, tt.gcjLat, gcjReferenceTolerance)
			}
			if math.Abs(gotLng-tt.gcjLng) > gcjReferenceTolerance {
				t.Errorf("WGS84ToGCJ02(%v, %v) lng = %v, want %v±%v",
					tt.wgsLat, tt.wgsLng, gotLng, tt.gcjLng, gcjReferenceTolerance)
			}
		})
	}
}

// TestWGS84ToGCJ02OutsideChinaIsIdentity asserts the conversion never moves a
// coordinate that China's mandate does not cover, which is what keeps every
// position outside the country bit-for-bit unchanged.
func TestWGS84ToGCJ02OutsideChinaIsIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lat, lng float64
	}{
		{name: "New York", lat: 40.7128, lng: -74.0060},
		{name: "London", lat: 51.5074, lng: -0.1278},
		{name: "Tokyo", lat: 35.6762, lng: 139.6503},
		{name: "San Francisco", lat: 37.7553, lng: -122.4527},
		{name: "Sydney", lat: -33.8688, lng: 151.2093},
		{name: "just outside the south edge", lat: 0.8292, lng: 100.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotLat, gotLng := WGS84ToGCJ02(tt.lat, tt.lng)
			if gotLat != tt.lat || gotLng != tt.lng {
				t.Errorf("WGS84ToGCJ02(%v, %v) = (%v, %v), want the input unchanged",
					tt.lat, tt.lng, gotLat, gotLng)
			}
		})
	}
}

// TestGCJ02ToWGS84MatchesPublishedReferences asserts the inverse transform
// recovers the GPS coordinate behind a published GCJ-02 pin.
func TestGCJ02ToWGS84MatchesPublishedReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		gcjLat, gcjLng float64
		wgsLat, wgsLng float64
	}{
		{
			name: "Tiananmen Square", gcjLat: 39.9069033815, gcjLng: 116.4038431840,
			wgsLat: 39.9055, wgsLng: 116.3976,
		},
		{
			name: "Shanghai Tower", gcjLat: 31.2314780608, gcjLng: 121.5059260802,
			wgsLat: 31.2335, wgsLng: 121.5015,
		},
		{
			name: "Chengdu", gcjLat: 30.5703461411, gcjLng: 104.0693054772,
			wgsLat: 30.5728, wgsLng: 104.0668,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotLat, gotLng := GCJ02ToWGS84(tt.gcjLat, tt.gcjLng)
			if math.Abs(gotLat-tt.wgsLat) > gcjReferenceTolerance {
				t.Errorf("GCJ02ToWGS84(%v, %v) lat = %v, want %v±%v",
					tt.gcjLat, tt.gcjLng, gotLat, tt.wgsLat, gcjReferenceTolerance)
			}
			if math.Abs(gotLng-tt.wgsLng) > gcjReferenceTolerance {
				t.Errorf("GCJ02ToWGS84(%v, %v) lng = %v, want %v±%v",
					tt.gcjLat, tt.gcjLng, gotLng, tt.wgsLng, gcjReferenceTolerance)
			}
		})
	}
}

// TestGCJ02ToWGS84OutsideChinaIsIdentity asserts the inverse is also a no-op
// outside the mandated area.
func TestGCJ02ToWGS84OutsideChinaIsIdentity(t *testing.T) {
	t.Parallel()

	for _, point := range []LatLng{
		{Lat: 40.7128, Lng: -74.0060},
		{Lat: 51.5074, Lng: -0.1278},
		{Lat: -33.8688, Lng: 151.2093},
	} {
		gotLat, gotLng := GCJ02ToWGS84(point.Lat, point.Lng)
		if gotLat != point.Lat || gotLng != point.Lng {
			t.Errorf("GCJ02ToWGS84(%v, %v) = (%v, %v), want the input unchanged",
				point.Lat, point.Lng, gotLat, gotLng)
		}
	}
}

// TestGCJ02RoundTripIsSubMillimeter asserts the two transforms are true
// inverses across China and near its edges, so a coordinate may be converted in
// either direction without accumulating drift.
func TestGCJ02RoundTripIsSubMillimeter(t *testing.T) {
	t.Parallel()

	points := []LatLng{
		{Lat: 39.9055, Lng: 116.3976}, // Beijing
		{Lat: 31.2335, Lng: 121.5015}, // Shanghai
		{Lat: 23.1291, Lng: 113.2644}, // Guangzhou
		{Lat: 22.5431, Lng: 114.0579}, // Shenzhen
		{Lat: 30.5728, Lng: 104.0668}, // Chengdu
		{Lat: 34.3416, Lng: 108.9398}, // Xi'an
		{Lat: 24.8801, Lng: 102.8329}, // Kunming
		{Lat: 29.6520, Lng: 91.1721},  // Lhasa
		{Lat: 43.8256, Lng: 87.6168},  // Urumqi
		{Lat: 36.6171, Lng: 101.7782}, // Xining
		{Lat: 1.3521, Lng: 103.8198},  // Singapore: outside, must stay exact
		{Lat: 0.9000, Lng: 100.0000},  // near the southern edge
	}
	for _, point := range points {
		gcjLat, gcjLng := WGS84ToGCJ02(point.Lat, point.Lng)
		backLat, backLng := GCJ02ToWGS84(gcjLat, gcjLng)
		if math.Abs(backLat-point.Lat) > gcjReferenceTolerance {
			t.Errorf("roundtrip lat for %+v = %v, want %v±%v",
				point, backLat, point.Lat, gcjReferenceTolerance)
		}
		if math.Abs(backLng-point.Lng) > gcjReferenceTolerance {
			t.Errorf("roundtrip lng for %+v = %v, want %v±%v",
				point, backLng, point.Lng, gcjReferenceTolerance)
		}
	}
}

// TestParseLocationAcceptsTheGCJPrefix asserts the notation an operator uses for
// a coordinate copied out of a Chinese map app: the gcj: (or gcj02:) prefix
// marks the value as a Mars coordinate, and the parser returns the GPS position
// behind it.
func TestParseLocationAcceptsTheGCJPrefix(t *testing.T) {
	t.Parallel()

	gcjLat, gcjLng := WGS84ToGCJ02(39.9055, 116.3976)
	tests := []struct {
		name string
		text string
	}{
		{name: "gcj prefix", text: "gcj:39.9069033815,116.4038431840"},
		{name: "gcj02 prefix", text: "gcj02:39.9069033815,116.4038431840"},
		{name: "upper case", text: "GCJ:39.9069033815 116.4038431840"},
		{name: "spaced", text: "  gcj: 39.9069033815, 116.4038431840  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLocation(tt.text)
			if err != nil {
				t.Fatalf("ParseLocation(%q): %v", tt.text, err)
			}
			if math.Abs(got.Lat-39.9055) > gcjReferenceTolerance ||
				math.Abs(got.Lng-116.3976) > gcjReferenceTolerance {
				t.Errorf("ParseLocation(%q) = %+v, want the GPS coordinate 39.9055, 116.3976",
					tt.text, got)
			}
			// The pair the test feeds in must really be the Mars coordinate of
			// the GPS one, or the assertion above would pass for the wrong
			// reason.
			if math.Abs(gcjLat-39.9069033815) > gcjReferenceTolerance ||
				math.Abs(gcjLng-116.4038431840) > gcjReferenceTolerance {
				t.Fatalf("WGS84ToGCJ02(39.9055, 116.3976) = %v, %v", gcjLat, gcjLng)
			}
		})
	}
}

// TestParseLocationRejectsABareGCJPrefix asserts the prefix is a modifier, not a
// location: without a coordinate behind it there is nothing to convert.
func TestParseLocationRejectsABareGCJPrefix(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"gcj:", "gcj02:  ", "gcj:not-a-place"} {
		if got, err := ParseLocation(text); err == nil {
			t.Errorf("ParseLocation(%q) = %+v, want an error", text, got)
		}
	}
}

// TestGCJ02ConversionIsReversibleOutsideTheBox asserts a Mars coordinate that
// happens to fall outside the bounding box (a mistyped or foreign value) passes
// through untouched rather than being shifted by an offset that does not exist
// there.
func TestGCJ02ConversionIsReversibleOutsideTheBox(t *testing.T) {
	t.Parallel()

	point := LatLng{Lat: 35.6762, Lng: 139.6503} // Tokyo
	converted, err := ParseLocation("gcj:35.6762,139.6503")
	if err != nil {
		t.Fatalf("ParseLocation: %v", err)
	}
	if converted != point {
		t.Errorf("ParseLocation(gcj:...) = %+v, want %+v untouched", converted, point)
	}
}

// TestLocPrintsTheMarsCoordinateInChina asserts the automatic prompt guidance the
// converter exists for: a location command that resolves inside China prints the
// GCJ-02 pair beside the GPS notations, so a coordinate can be carried between
// this bot and a Chinese map app in either direction, while a position outside
// China prints nothing extra.
func TestLocPrintsTheMarsCoordinateInChina(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, defaultTestConfig())

	china := runLines(t, reg, session, "loc 39.9055,116.3976")
	if len(china) != 2 {
		t.Fatalf("loc in China = %v, want the notations and a GCJ-02 line", china)
	}
	lat, lng := WGS84ToGCJ02(39.9055, 116.3976)
	if want := fmt.Sprintf("GCJ-02 (Amap/Gaode/WeChat): %v", FormatLatLng(LatLng{Lat: lat, Lng: lng})); china[1] != want {
		t.Errorf("loc line 2 = %q, want %q", china[1], want)
	}

	us := runLines(t, reg, session, "loc 37.7553,-122.4527")
	if len(us) != 1 {
		t.Errorf("loc outside China = %v, want only the notations", us)
	}
}
