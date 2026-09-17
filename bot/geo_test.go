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

// closeWithin reports whether two values agree to within tolerance.
func closeWithin(got, want, tolerance float64) bool {
	return math.Abs(got-want) <= tolerance
}

// TestParseLocationAcceptsFieldNotations asserts every notation an operator can
// be handed parses to the same coordinate the notation describes.
func TestParseLocationAcceptsFieldNotations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		lat  float64
		lng  float64
	}{
		{"Plus Code", "849VCWC8+R9", 37.4220625, -122.0840625},
		{"Plus Code lower case", "849vcwc8+r9", 37.4220625, -122.0840625},
		{"decimal degrees with comma", "37.42205, -122.08409", 37.42205, -122.08409},
		{"decimal degrees with space", "37.42205 -122.08409", 37.42205, -122.08409},
		{"decimal degrees with leading hemispheres", "N37.42205 W122.08409", 37.42205, -122.08409},
		{"decimal degrees with trailing hemispheres", "37.42205N 122.08409W", 37.42205, -122.08409},
		{"southern and eastern hemispheres", "S33.8568 E151.2153", -33.8568, 151.2153},
		{"DMS with symbols", `37°25'19"N 122°05'03"W`, 37.4219444444, -122.0841666667},
		{"DMS without symbols", "37 25 19 N, 122 05 03 W", 37.4219444444, -122.0841666667},
		{"DMS with trailing hemispheres and no comma", "37 25 19N 122 05 03W", 37.4219444444, -122.0841666667},
		{"DDM with symbols", `37°25.323'N 122°05.048'W`, 37.42205, -122.0841333333},
		{"DDM with leading hemispheres", "N37 25.323 W122 05.048", 37.42205, -122.0841333333},
		{"Maidenhead field and square", "CM87", 37.5, -123.0},
		{"Maidenhead with subsquare", "CM87uk", 37.4375, -122.2916666667},
		{"Maidenhead lower case", "cm87uk", 37.4375, -122.2916666667},
		{"Maidenhead Connecticut", "FN31pr", 41.7291666667, -72.7083333333},
		{"Google Maps coordinate with leading @", "@28.405832,-81.4716354", 28.405832, -81.4716354},
		{"Google Maps coordinate with leading @ and space", "@ 37.42205, -122.08409", 37.42205, -122.08409},
		{"Plus Code with leading @", "@849VCWC8+R9", 37.4220625, -122.0840625},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLocation(tc.in)
			if err != nil {
				t.Fatalf("ParseLocation(%q): %v", tc.in, err)
			}
			if !closeWithin(got.Lat, tc.lat, 1e-6) {
				t.Errorf("ParseLocation(%q).Lat = %v, want %v", tc.in, got.Lat, tc.lat)
			}
			if !closeWithin(got.Lng, tc.lng, 1e-6) {
				t.Errorf("ParseLocation(%q).Lng = %v, want %v", tc.in, got.Lng, tc.lng)
			}
		})
	}
}

// TestParseLocationRejectsUnplaceableText asserts text that is not a location,
// or that cannot be placed without more information, is refused rather than
// guessed. A wrong position is worse than no position.
func TestParseLocationRejectsUnplaceableText(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"", "   ", "@", "@   ", "hello world", "91, 0", "0, 181", "-91 0", "0 -181",
		"37.4", "1 2 3", "37.5 122.5 1 2", "8F+6X", "ZZ99", "37.5, NOPE",
		"N37.5 E122.1 S10", "0/0",
	} {
		if got, err := ParseLocation(in); err == nil {
			t.Errorf("ParseLocation(%q) = %+v, want an error", in, got)
		}
	}
}

// TestHaversineDistanceMatchesGoldens asserts the great-circle distance agrees
// with the reference formula the values were captured from, across the equator,
// across a pole, and across the antimeridian.
func TestHaversineDistanceMatchesGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		p1   LatLng
		p2   LatLng
		want float64
	}{
		{"San Francisco to Paris", LatLng{37.422, -122.0841}, LatLng{48.8583, 2.2945}, 8967031.299922155},
		{"one degree of longitude", LatLng{0, 0}, LatLng{0, 1}, 111194.92664455874},
		{"one degree of latitude", LatLng{0, 0}, LatLng{1, 0}, 111194.92664455874},
		{"London to Paris", LatLng{51.5, -0.12}, LatLng{48.8583, 2.2945}, 340316.69033927465},
		{"Sydney to San Francisco", LatLng{-33.8568, 151.2153}, LatLng{37.422, -122.0841}, 11952702.627900647},
		{"across the pole", LatLng{89, 0}, LatLng{89, 180}, 222389.8532891186},
		{"across the antimeridian", LatLng{-45, 170}, LatLng{-45, -170}, 1568520.5567985757},
		{"the same point", LatLng{37.422, -122.0841}, LatLng{37.422, -122.0841}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HaversineDistance(tc.p1, tc.p2)
			if !closeWithin(got, tc.want, 1e-6) {
				t.Errorf("HaversineDistance = %v, want %v", got, tc.want)
			}
			if back := HaversineDistance(tc.p2, tc.p1); !closeWithin(back, got, 1e-9) {
				t.Errorf("distance is not symmetric: %v vs %v", got, back)
			}
		})
	}
}

// TestInitialBearingMatchesGoldens asserts the initial bearing agrees with the
// reference formula, including the wrapping cases a naive atan2 gets wrong.
func TestInitialBearingMatchesGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		p1   LatLng
		p2   LatLng
		want float64
	}{
		{"due east along the equator", LatLng{0, 0}, LatLng{0, 1}, 90},
		{"due north along a meridian", LatLng{0, 0}, LatLng{1, 0}, 0},
		{"San Francisco to Paris", LatLng{37.422, -122.0841}, LatLng{48.8583, 2.2945}, 33.389214811319164},
		{"London to Paris", LatLng{51.5, -0.12}, LatLng{48.8583, 2.2945}, 148.72559004844635},
		{"Sydney to San Francisco", LatLng{-33.8568, 151.2153}, LatLng{37.422, -122.0841}, 56.2336495794147},
		{"across the pole", LatLng{89, 0}, LatLng{89, 180}, 0},
		{"across the antimeridian", LatLng{-45, 170}, LatLng{-45, -170}, 97.10707611044654},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := InitialBearing(tc.p1, tc.p2)
			if !closeWithin(got, tc.want, 1e-9) {
				t.Errorf("InitialBearing = %v, want %v", got, tc.want)
			}
			if got < 0 || got >= 360 {
				t.Errorf("InitialBearing = %v, want a value in 0..360", got)
			}
		})
	}
}

// TestCompassPointNamesEverySector asserts the sixteen-point rose covers the
// circle in order and that each sector's center names that sector.
func TestCompassPointNamesEverySector(t *testing.T) {
	t.Parallel()

	if len(CompassPoints) != compassPointCount {
		t.Fatalf("the rose has %v points, want %v", len(CompassPoints), compassPointCount)
	}
	for i, name := range CompassPoints {
		bearing := float64(i) * degreesPerCompassPoint
		if got := CompassPoint(bearing); got != name {
			t.Errorf("CompassPoint(%v) = %q, want %q", bearing, got, name)
		}
		// A sector extends half its width to either side of its center.
		if got := CompassPoint(bearing + degreesPerCompassPoint/2 - 1e-9); got != name {
			t.Errorf("CompassPoint(%v + half sector) = %q, want %q", bearing, got, name)
		}
		if got := CompassPoint(bearing - degreesPerCompassPoint/2 + 1e-9); got != name {
			t.Errorf("CompassPoint(%v - half sector) = %q, want %q", bearing, got, name)
		}
	}
	if got := CompassPoint(360); got != "N" {
		t.Errorf("CompassPoint(360) = %q, want N", got)
	}
	if got := CompassPoint(-10); got != "N" {
		t.Errorf("CompassPoint(-10) = %q, want N", got)
	}
}

// TestProjectWaypointMatchesGoldens asserts dead reckoning agrees with the
// reference formula, both along a meridian and on an oblique course.
func TestProjectWaypointMatchesGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		origin  LatLng
		bearing float64
		meters  float64
		lat     float64
		lng     float64
	}{
		{"due north one kilometer", LatLng{51.5, -0.12}, 0, 1000, 51.50899321605918, -0.12},
		{"due east from the equator", LatLng{0, 0}, 90, 111319.4907932736, 0, 1.0011202323025827},
		{"south west ten kilometers", LatLng{47.365590, 8.524997}, 225, 10000, 47.30196008760977, 8.431222623015515},
		{"zero distance", LatLng{37.422, -122.0841}, 48, 0, 37.422, -122.0841},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ProjectWaypoint(tc.origin, tc.bearing, tc.meters)
			if !closeWithin(got.Lat, tc.lat, 1e-9) {
				t.Errorf("ProjectWaypoint().Lat = %v, want %v", got.Lat, tc.lat)
			}
			if !closeWithin(got.Lng, tc.lng, 1e-9) {
				t.Errorf("ProjectWaypoint().Lng = %v, want %v", got.Lng, tc.lng)
			}
		})
	}
}

// TestProjectWaypointInvertsDistanceAndBearing asserts a projected waypoint is
// exactly the distance and bearing from its origin that produced it, which is
// what makes dead reckoning trustworthy.
func TestProjectWaypointInvertsDistanceAndBearing(t *testing.T) {
	t.Parallel()

	origin := LatLng{37.422, -122.0841}
	for _, bearing := range []float64{0, 45, 90, 135, 180, 225, 270, 315} {
		for _, meters := range []float64{1, 100, 5000, 100000} {
			target := ProjectWaypoint(origin, bearing, meters)
			if got := HaversineDistance(origin, target); !closeWithin(got, meters, meters*1e-9+1e-6) {
				t.Errorf("bearing %v distance %v: measured %v", bearing, meters, got)
			}
			if got := InitialBearing(origin, target); !closeWithin(got, bearing, 1e-6) {
				t.Errorf("bearing %v distance %v: measured bearing %v", bearing, meters, got)
			}
		}
	}
}

// TestMaidenheadRoundTrips asserts a locator names the square it came from, and
// that decoding a published locator's center re-encodes to that locator.
func TestMaidenheadRoundTrips(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lat  float64
		lng  float64
		grid string
		dlat float64
		dlng float64
	}{
		{"Googleplex", 37.422, -122.0841, "CM87wk", 37.4375, -122.125},
		{"Connecticut", 41.729, -72.708, "FN31pr", 41.7291666667, -72.7083333333},
		{"London", 51.5, -0.12, "IO91wm", 51.5208333333, -0.125},
		{"southern and eastern", -33.8568, 151.2153, "QF56od", -33.8541666667, 151.2083333333},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := LatLngToMaidenhead(tc.lat, tc.lng); got != tc.grid {
				t.Errorf("LatLngToMaidenhead(%v, %v) = %q, want %q", tc.lat, tc.lng, got, tc.grid)
			}
			got, err := MaidenheadToLatLng(tc.grid)
			if err != nil {
				t.Fatalf("MaidenheadToLatLng(%q): %v", tc.grid, err)
			}
			if !closeWithin(got.Lat, tc.dlat, 1e-9) {
				t.Errorf("MaidenheadToLatLng(%q).Lat = %v, want %v", tc.grid, got.Lat, tc.dlat)
			}
			if !closeWithin(got.Lng, tc.dlng, 1e-9) {
				t.Errorf("MaidenheadToLatLng(%q).Lng = %v, want %v", tc.grid, got.Lng, tc.dlng)
			}
			// The center of a locator's square re-encodes to the same locator.
			if again := LatLngToMaidenhead(got.Lat, got.Lng); again != tc.grid {
				t.Errorf("re-encoding the center of %q gave %q", tc.grid, again)
			}
		})
	}
}

// TestMaidenheadRejectsMalformedLocators asserts a locator outside the grid's
// own ranges is refused.
func TestMaidenheadRejectsMalformedLocators(t *testing.T) {
	t.Parallel()

	for _, grid := range []string{"", "C", "CM", "CM8", "CM87u", "ZZ99", "CM87u?", "CM87ukz9"} {
		if got, err := MaidenheadToLatLng(grid); err == nil {
			t.Errorf("MaidenheadToLatLng(%q) = %+v, want an error", grid, got)
		}
	}
}

// TestParseDistanceAcceptsFieldUnits asserts every unit an operator might type
// is understood, and that a bare number is meters.
func TestParseDistanceAcceptsFieldUnits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want float64
	}{
		{"1500", 1500},
		{"1500m", 1500},
		{"1.5km", 1500},
		{"3.5 km", 3500},
		{"1mi", 1609.344},
		{"2 mi", 3218.688},
		{"1nm", 1852},
		{"500ft", 152.4},
		{"300yd", 274.32},
	}
	for _, tc := range tests {
		got, err := ParseDistance(tc.in)
		if err != nil {
			t.Errorf("ParseDistance(%q): %v", tc.in, err)
			continue
		}
		if !closeWithin(got, tc.want, 1e-9) {
			t.Errorf("ParseDistance(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"", "abc", "5 furlongs", "-3km", "km"} {
		if _, err := ParseDistance(in); err == nil {
			t.Errorf("ParseDistance(%q) accepted, want an error", in)
		}
	}
}

// TestParseBearingAcceptsDegreesAndCompassPoints asserts a course can be typed
// either way, and that nonsense is refused.
func TestParseBearingAcceptsDegreesAndCompassPoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want float64
	}{
		{"048", 48},
		{"48", 48},
		{"48°", 48},
		{"0", 0},
		{"359", 359},
		{"N", 0},
		{"E", 90},
		{"S", 180},
		{"W", 270},
		{"NE", 45},
		{"nne", 22.5},
	}
	for _, tc := range tests {
		got, err := ParseBearing(tc.in)
		if err != nil {
			t.Errorf("ParseBearing(%q): %v", tc.in, err)
			continue
		}
		if !closeWithin(got, tc.want, 1e-9) {
			t.Errorf("ParseBearing(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"", "360", "-1", "up", "NN"} {
		if _, err := ParseBearing(in); err == nil {
			t.Errorf("ParseBearing(%q) accepted, want an error", in)
		}
	}
}

// TestCoordinateFormatting asserts the four renderings the loc command prints
// carry the same position in the shapes a field operator expects.
func TestCoordinateFormatting(t *testing.T) {
	t.Parallel()

	point := LatLng{Lat: 37.42205, Lng: -122.08409}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"decimal degrees", formatDD(point), "37.4220°N, 122.0841°W"},
		{"degrees decimal minutes", formatDDM(point), "37°25.32'N 122°05.05'W"},
		{"degrees minutes seconds", formatDMS(point), `37°25'19"N 122°05'03"W`},
		{"machine readable", FormatLatLng(point), "37.422050, -122.084090"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
	// A southern and eastern position keeps its hemispheres straight.
	south := LatLng{Lat: -33.8568, Lng: 151.2153}
	if got, want := formatDD(south), "33.8568°S, 151.2153°E"; got != want {
		t.Errorf("formatDD(south) = %q, want %q", got, want)
	}
}

// TestFormatDistanceAndBearing asserts the distance and bearing renderings the
// navigation commands print.
func TestFormatDistanceAndBearing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"kilometers", FormatDistance(3420), "3.42 km (2.13 mi, 1.85 nm)"},
		{"meters", FormatDistance(450), "450 m (1476 ft, 492 yd)"},
		{"small bearing pads to three digits", FormatBearing(48), "048° NE"},
		{"north", FormatBearing(0), "000° N"},
		{"south west", FormatBearing(228), "228° SW"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestParseLocationErrorIsWrapped asserts every rejection carries the sentinel,
// so a caller can tell "not a location" from a real failure.
func TestParseLocationErrorIsWrapped(t *testing.T) {
	t.Parallel()

	_, err := ParseLocation("not a place")
	if !errors.Is(err, ErrLocationUnrecognized) {
		t.Fatalf("error = %v, want ErrLocationUnrecognized", err)
	}
	if !strings.Contains(err.Error(), "unrecognized location") {
		t.Errorf("error %v does not name the problem", err)
	}
}
