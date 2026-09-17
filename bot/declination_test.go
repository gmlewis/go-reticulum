// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// wmmReferenceVector is one line of the World Magnetic Model's own published
// test values for the 2025.0 epoch: a date, a height above the WGS-84
// ellipsoid, a geodetic position, and the north, east, and downward field
// components the model must produce there in nanotesla.
//
// These are the model's acceptance vectors, reproduced from the WMM2025 test
// values the US National Centers for Environmental Information publishes
// alongside the coefficient table. Pinning them is what makes the port a port
// of the World Magnetic Model rather than an approximation that happens to
// point roughly north: a declination engine that is one degree off is a
// directional antenna aimed one degree off, and at fourteen kilometers that is
// a quarter of a kilometer of error before the operator has even started
// walking.
type wmmReferenceVector struct {
	year  float64
	altKm float64
	lat   float64
	lng   float64
	north float64
	east  float64
	down  float64
}

// wmmReferenceVectors is the WMM2025 test set.
var wmmReferenceVectors = []wmmReferenceVector{
	{2025.0, 0.0, 80.0, 0.0, 6521.6, 145.9, 54791.5},
	{2025.0, 0.0, 0.0, 120.0, 39677.8, -109.6, -10580.2},
	{2025.0, 0.0, -80.0, 240.0, 6117.5, 15751.9, -52022.5},
	{2025.0, 100.0, 80.0, 0.0, 6216.0, 92.4, 52598.8},
	{2025.0, 100.0, 0.0, 120.0, 37688.6, -96.2, -10152.1},
	{2025.0, 100.0, -80.0, 240.0, 5907.6, 14780.3, -49540.7},
	{2027.5, 0.0, 80.0, 0.0, 6500.8, 294.5, 54869.4},
	{2027.5, 0.0, 0.0, 120.0, 39701.6, -167.4, -10381.8},
	{2027.5, 0.0, -80.0, 240.0, 6200.7, 15730.3, -51783.7},
	{2027.5, 100.0, 80.0, 0.0, 6196.7, 233.8, 52670.5},
	{2027.5, 100.0, 0.0, 120.0, 37711.5, -148.7, -9969.8},
	{2027.5, 100.0, -80.0, 240.0, 5984.0, 14760.1, -49317.7},
}

// TestDeclinationMatchesTheModelTestVectors asserts the spherical harmonic
// engine reproduces every one of the World Magnetic Model's own published test
// vectors. The published values are rounded to a tenth of a nanotesla and a
// hundredth of a degree, so the tolerance is that rounding and nothing more: a
// real disagreement shows up immediately.
func TestDeclinationMatchesTheModelTestVectors(t *testing.T) {
	t.Parallel()

	for _, vector := range wmmReferenceVectors {
		name := fmt.Sprintf("year_%v_lat_%v_lng_%v_alt_%vkm",
			vector.year, vector.lat, vector.lng, vector.altKm)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			north, east, down := magneticFieldComponents(
				vector.lat, vector.lng, vector.altKm, vector.year)
			for _, component := range []struct {
				name      string
				got       float64
				want      float64
				tolerance float64
			}{
				{"north", north, vector.north, 0.1},
				{"east", east, vector.east, 0.1},
				{"down", down, vector.down, 0.1},
			} {
				if math.Abs(component.got-component.want) > component.tolerance {
					t.Errorf("%v = %.3f nT, want %.1f nT (difference %.3f)",
						component.name, component.got, component.want,
						component.got-component.want)
				}
			}
		})
	}
}

// TestDeclinationReferenceCities asserts the declination at the published
// reference cities is what the model in force says it is, using the ranges the
// Phase 0.6 brief pins: San Francisco strongly east, Denver mildly east, Boston
// west, London near zero, and Tokyo west. A dipole approximation cannot pass
// this test, which is exactly why the engine is a full spherical harmonic one.
func TestDeclinationReferenceCities(t *testing.T) {
	t.Parallel()

	epoch := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		lat  float64
		lng  float64
		low  float64
		high float64
	}{
		{"San Francisco", 37.7553, -122.4527, 12.8, 13.2},
		{"Denver", 39.7392, -104.9903, 7.5, 8.5},
		{"Boston", 42.3601, -71.0589, -14.5, -14.0},
		{"London", 51.5074, -0.1278, 0.5, 1.2},
		{"Tokyo", 35.6762, 139.6503, -8.2, -7.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EstimateMagneticDeclination(tc.lat, tc.lng, epoch)
			if got < tc.low || got > tc.high {
				t.Errorf("declination at %v (%v,%v) = %+.3f°, want %+.1f° to %+.1f°",
					tc.name, tc.lat, tc.lng, got, tc.low, tc.high)
			}
		})
	}
}

// TestDeclinationSignFollowsEastWest asserts the sign convention the whole
// subsystem depends on: east variation is positive and is added to a magnetic
// heading to reach a true one, and west variation is negative.
func TestDeclinationSignFollowsEastWest(t *testing.T) {
	t.Parallel()

	epoch := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	if got := EstimateMagneticDeclination(37.7553, -122.4527, epoch); got <= 0 {
		t.Errorf("San Francisco declination = %+.3f°, want east (positive)", got)
	}
	if got := EstimateMagneticDeclination(42.3601, -71.0589, epoch); got >= 0 {
		t.Errorf("Boston declination = %+.3f°, want west (negative)", got)
	}
}

// TestDeclinationFollowsTheSecularVariation asserts the model's annual change is
// applied: a date later than the epoch moves the declination by the secular
// variation rather than returning the epoch value forever.
func TestDeclinationFollowsTheSecularVariation(t *testing.T) {
	t.Parallel()

	lat, lng := 37.7553, -122.4527
	epoch := EstimateMagneticDeclination(lat, lng, time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC))
	later := EstimateMagneticDeclination(lat, lng, time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC))
	// San Francisco's variation is drifting west by a little over a tenth of a
	// degree a year, and the direction of the drift is what the secular
	// variation terms carry.
	if delta := later - epoch; delta > -0.05 || delta < -1.0 {
		t.Errorf("declination moved %+.3f° over two years, want a slight westward drift", delta)
	}
}

// TestDeclinationClampsOutsideTheModelWindow asserts a date outside the model's
// published validity window is clamped to its edge instead of being
// extrapolated: a declination invented from a stale model is worse than one
// reported at the edge of the model that exists.
func TestDeclinationClampsOutsideTheModelWindow(t *testing.T) {
	t.Parallel()

	lat, lng := 51.5074, -0.1278
	before := EstimateMagneticDeclination(lat, lng, time.Date(1995, time.June, 1, 0, 0, 0, 0, time.UTC))
	atEpoch := EstimateMagneticDeclination(lat, lng, time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC))
	if math.Abs(before-atEpoch) > 1e-9 {
		t.Errorf("a pre-epoch date = %+.3f°, want the epoch value %+.3f°", before, atEpoch)
	}
	after := EstimateMagneticDeclination(lat, lng, time.Date(2095, time.June, 1, 0, 0, 0, 0, time.UTC))
	atEnd := EstimateMagneticDeclination(lat, lng, time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC))
	if math.Abs(after-atEnd) > 1e-9 {
		t.Errorf("a post-validity date = %+.3f°, want the last-model-year value %+.3f°", after, atEnd)
	}
}

// TestDeclinationIsFiniteEverywhere asserts the engine answers a number at the
// two poles and on the date line, where a naive implementation divides by the
// sine of a colatitude that has gone to zero.
func TestDeclinationIsFiniteEverywhere(t *testing.T) {
	t.Parallel()

	epoch := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct{ lat, lng float64 }{
		{90, 0}, {-90, 0}, {90, 180}, {0, 180}, {0, -180}, {-89.9999, 179.9999},
	}
	for _, tc := range cases {
		got := EstimateMagneticDeclination(tc.lat, tc.lng, epoch)
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Errorf("declination at (%v,%v) = %v, want a finite angle", tc.lat, tc.lng, got)
		}
		if got < -180 || got > 180 {
			t.Errorf("declination at (%v,%v) = %v, want a signed angle", tc.lat, tc.lng, got)
		}
	}
}

// TestDeclinationZeroTimeIsNow asserts a zero time is read as the present rather
// than as year one, which would clamp to the epoch and silently freeze the
// secular variation.
func TestDeclinationZeroTimeIsNow(t *testing.T) {
	t.Parallel()

	got := EstimateMagneticDeclination(37.7553, -122.4527, time.Time{})
	now := EstimateMagneticDeclination(37.7553, -122.4527, time.Now())
	if math.Abs(got-now) > 0.5 {
		t.Errorf("declination at the zero time = %+.3f°, want the present value %+.3f°", got, now)
	}
}
