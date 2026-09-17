// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the magnetic declination engine: the difference between the
// direction a magnetic compass points and true north, computed from a position
// alone.
//
// A compass, whether it is a QMC5883L on the I2C bus or a marine compass
// sentence, reports a heading relative to magnetic north. Magnetic north is not
// the north a map, a bearing, or a rescue grid uses, and the gap between them —
// the declination, or variation — ranges from a fraction of a degree to more
// than twenty depending on where on Earth the operator is standing. An operator
// who aims a Yagi at "042" while the local variation is +13 degrees is aiming
// thirteen degrees off, which at fourteen kilometers is more than three
// kilometers of error.
//
// The engine is the World Magnetic Model, the same spherical-harmonic model the
// US National Centers for Environmental Information and the British Geological
// Survey publish, evaluated from its coefficient table. It is a few hundred
// lines of arithmetic and no data files, no network, and no third-party
// library, which is the only way the Go Reticulum Lifesaver can convert a
// magnetic heading to a true one in a slot canyon with the radio off.

package main

import (
	"math"
	"time"
)

// World Magnetic Model constants.
const (
	// wmmEpoch is the decimal year the coefficient table is defined at. WMM2025
	// is the model in force from 2025.0 to 2030.0.
	wmmEpoch = 2025.0
	// wmmValidityYears is how long the model is published for. A date outside
	// the window is clamped to its edge rather than extrapolated into nonsense:
	// a declination fifteen years past the model's epoch is a guess dressed up
	// as a measurement.
	wmmValidityYears = 5.0
	// wmmMaxOrder is the degree and order the model is truncated at.
	wmmMaxOrder = 12
	// wmmReferenceRadiusKm is the geomagnetic reference radius the spherical
	// harmonic expansion is evaluated against.
	wmmReferenceRadiusKm = 6371.2
	// wmmSemiMajorAxisKm and wmmSemiMinorAxisKm are the WGS-84 ellipsoid axes,
	// which turn a geodetic position into the geocentric one the model needs.
	wmmSemiMajorAxisKm = 6378.137
	wmmSemiMinorAxisKm = 6356.7523142
	// wmmDefaultAltitudeKm is the height the declination estimate assumes. The
	// field changes by well under a hundredth of a degree over the few
	// kilometers an operator on foot can climb, so a sea-level estimate is the
	// honest default for a pocket device.
	wmmDefaultAltitudeKm = 0.0
)

// wmmCoefficient is one line of the World Magnetic Model: a degree n and order
// m, the main-field Gauss coefficients g and h in nanotesla at the epoch, and
// their annual secular variation dg and dh in nanotesla per year.
type wmmCoefficient struct {
	n  int
	m  int
	g  float64
	h  float64
	dg float64
	dh float64
}

// wmm2025Coefficients is the WMM2025 main field and secular variation, degree
// and order one through twelve. It is the published coefficient table
// verbatim; the field it describes reproduces every one of the model's own
// published test vectors to the precision they are printed at.
var wmm2025Coefficients = []wmmCoefficient{
	{n: 1, m: 0, g: -29351.8, h: 0.0, dg: 12.0, dh: 0.0},
	{n: 1, m: 1, g: -1410.8, h: 4545.4, dg: 9.7, dh: -21.5},
	{n: 2, m: 0, g: -2556.6, h: 0.0, dg: -11.6, dh: 0.0},
	{n: 2, m: 1, g: 2951.1, h: -3133.6, dg: -5.2, dh: -27.7},
	{n: 2, m: 2, g: 1649.3, h: -815.1, dg: -8.0, dh: -12.1},
	{n: 3, m: 0, g: 1361.0, h: 0.0, dg: -1.3, dh: 0.0},
	{n: 3, m: 1, g: -2404.1, h: -56.6, dg: -4.2, dh: 4.0},
	{n: 3, m: 2, g: 1243.8, h: 237.5, dg: 0.4, dh: -0.3},
	{n: 3, m: 3, g: 453.6, h: -549.5, dg: -15.6, dh: -4.1},
	{n: 4, m: 0, g: 895.0, h: 0.0, dg: -1.6, dh: 0.0},
	{n: 4, m: 1, g: 799.5, h: 278.6, dg: -2.4, dh: -1.1},
	{n: 4, m: 2, g: 55.7, h: -133.9, dg: -6.0, dh: 4.1},
	{n: 4, m: 3, g: -281.1, h: 212.0, dg: 5.6, dh: 1.6},
	{n: 4, m: 4, g: 12.1, h: -375.6, dg: -7.0, dh: -4.4},
	{n: 5, m: 0, g: -233.2, h: 0.0, dg: 0.6, dh: 0.0},
	{n: 5, m: 1, g: 368.9, h: 45.4, dg: 1.4, dh: -0.5},
	{n: 5, m: 2, g: 187.2, h: 220.2, dg: 0.0, dh: 2.2},
	{n: 5, m: 3, g: -138.7, h: -122.9, dg: 0.6, dh: 0.4},
	{n: 5, m: 4, g: -142.0, h: 43.0, dg: 2.2, dh: 1.7},
	{n: 5, m: 5, g: 20.9, h: 106.1, dg: 0.9, dh: 1.9},
	{n: 6, m: 0, g: 64.4, h: 0.0, dg: -0.2, dh: 0.0},
	{n: 6, m: 1, g: 63.8, h: -18.4, dg: -0.4, dh: 0.3},
	{n: 6, m: 2, g: 76.9, h: 16.8, dg: 0.9, dh: -1.6},
	{n: 6, m: 3, g: -115.7, h: 48.8, dg: 1.2, dh: -0.4},
	{n: 6, m: 4, g: -40.9, h: -59.8, dg: -0.9, dh: 0.9},
	{n: 6, m: 5, g: 14.9, h: 10.9, dg: 0.3, dh: 0.7},
	{n: 6, m: 6, g: -60.7, h: 72.7, dg: 0.9, dh: 0.9},
	{n: 7, m: 0, g: 79.5, h: 0.0, dg: 0.0, dh: 0.0},
	{n: 7, m: 1, g: -77.0, h: -48.9, dg: -0.1, dh: 0.6},
	{n: 7, m: 2, g: -8.8, h: -14.4, dg: -0.1, dh: 0.5},
	{n: 7, m: 3, g: 59.3, h: -1.0, dg: 0.5, dh: -0.8},
	{n: 7, m: 4, g: 15.8, h: 23.4, dg: -0.1, dh: 0.0},
	{n: 7, m: 5, g: 2.5, h: -7.4, dg: -0.8, dh: -1.0},
	{n: 7, m: 6, g: -11.1, h: -25.1, dg: -0.8, dh: 0.6},
	{n: 7, m: 7, g: 14.2, h: -2.3, dg: 0.8, dh: -0.2},
	{n: 8, m: 0, g: 23.2, h: 0.0, dg: -0.1, dh: 0.0},
	{n: 8, m: 1, g: 10.8, h: 7.1, dg: 0.2, dh: -0.2},
	{n: 8, m: 2, g: -17.5, h: -12.6, dg: 0.0, dh: 0.5},
	{n: 8, m: 3, g: 2.0, h: 11.4, dg: 0.5, dh: -0.4},
	{n: 8, m: 4, g: -21.7, h: -9.7, dg: -0.1, dh: 0.4},
	{n: 8, m: 5, g: 16.9, h: 12.7, dg: 0.3, dh: -0.5},
	{n: 8, m: 6, g: 15.0, h: 0.7, dg: 0.2, dh: -0.6},
	{n: 8, m: 7, g: -16.8, h: -5.2, dg: 0.0, dh: 0.3},
	{n: 8, m: 8, g: 0.9, h: 3.9, dg: 0.2, dh: 0.2},
	{n: 9, m: 0, g: 4.6, h: 0.0, dg: 0.0, dh: 0.0},
	{n: 9, m: 1, g: 7.8, h: -24.8, dg: -0.1, dh: -0.3},
	{n: 9, m: 2, g: 3.0, h: 12.2, dg: 0.1, dh: 0.3},
	{n: 9, m: 3, g: -0.2, h: 8.3, dg: 0.3, dh: -0.3},
	{n: 9, m: 4, g: -2.5, h: -3.3, dg: -0.3, dh: 0.3},
	{n: 9, m: 5, g: -13.1, h: -5.2, dg: 0.0, dh: 0.2},
	{n: 9, m: 6, g: 2.4, h: 7.2, dg: 0.3, dh: -0.1},
	{n: 9, m: 7, g: 8.6, h: -0.6, dg: -0.1, dh: -0.2},
	{n: 9, m: 8, g: -8.7, h: 0.8, dg: 0.1, dh: 0.4},
	{n: 9, m: 9, g: -12.9, h: 10.0, dg: -0.1, dh: 0.1},
	{n: 10, m: 0, g: -1.3, h: 0.0, dg: 0.1, dh: 0.0},
	{n: 10, m: 1, g: -6.4, h: 3.3, dg: 0.0, dh: 0.0},
	{n: 10, m: 2, g: 0.2, h: 0.0, dg: 0.1, dh: 0.0},
	{n: 10, m: 3, g: 2.0, h: 2.4, dg: 0.1, dh: -0.2},
	{n: 10, m: 4, g: -1.0, h: 5.3, dg: 0.0, dh: 0.1},
	{n: 10, m: 5, g: -0.6, h: -9.1, dg: -0.3, dh: -0.1},
	{n: 10, m: 6, g: -0.9, h: 0.4, dg: 0.0, dh: 0.1},
	{n: 10, m: 7, g: 1.5, h: -4.2, dg: -0.1, dh: 0.0},
	{n: 10, m: 8, g: 0.9, h: -3.8, dg: -0.1, dh: -0.1},
	{n: 10, m: 9, g: -2.7, h: 0.9, dg: 0.0, dh: 0.2},
	{n: 10, m: 10, g: -3.9, h: -9.1, dg: 0.0, dh: 0.0},
	{n: 11, m: 0, g: 2.9, h: 0.0, dg: 0.0, dh: 0.0},
	{n: 11, m: 1, g: -1.5, h: 0.0, dg: 0.0, dh: 0.0},
	{n: 11, m: 2, g: -2.5, h: 2.9, dg: 0.0, dh: 0.1},
	{n: 11, m: 3, g: 2.4, h: -0.6, dg: 0.0, dh: 0.0},
	{n: 11, m: 4, g: -0.6, h: 0.2, dg: 0.0, dh: 0.1},
	{n: 11, m: 5, g: -0.1, h: 0.5, dg: -0.1, dh: 0.0},
	{n: 11, m: 6, g: -0.6, h: -0.3, dg: 0.0, dh: 0.0},
	{n: 11, m: 7, g: -0.1, h: -1.2, dg: 0.0, dh: 0.1},
	{n: 11, m: 8, g: 1.1, h: -1.7, dg: -0.1, dh: 0.0},
	{n: 11, m: 9, g: -1.0, h: -2.9, dg: -0.1, dh: 0.0},
	{n: 11, m: 10, g: -0.2, h: -1.8, dg: -0.1, dh: 0.0},
	{n: 11, m: 11, g: 2.6, h: -2.3, dg: -0.1, dh: 0.0},
	{n: 12, m: 0, g: -2.0, h: 0.0, dg: 0.0, dh: 0.0},
	{n: 12, m: 1, g: -0.2, h: -1.3, dg: 0.0, dh: 0.0},
	{n: 12, m: 2, g: 0.3, h: 0.7, dg: 0.0, dh: 0.0},
	{n: 12, m: 3, g: 1.2, h: 1.0, dg: 0.0, dh: -0.1},
	{n: 12, m: 4, g: -1.3, h: -1.4, dg: 0.0, dh: 0.1},
	{n: 12, m: 5, g: 0.6, h: 0.0, dg: 0.0, dh: 0.0},
	{n: 12, m: 6, g: 0.6, h: 0.6, dg: 0.1, dh: 0.0},
	{n: 12, m: 7, g: 0.5, h: -0.1, dg: 0.0, dh: 0.0},
	{n: 12, m: 8, g: -0.1, h: 0.8, dg: 0.0, dh: 0.0},
	{n: 12, m: 9, g: -0.4, h: 0.1, dg: 0.0, dh: 0.0},
	{n: 12, m: 10, g: -0.2, h: -1.0, dg: -0.1, dh: 0.0},
	{n: 12, m: 11, g: -1.3, h: 0.1, dg: 0.0, dh: 0.0},
	{n: 12, m: 12, g: -0.7, h: 0.2, dg: -0.1, dh: -0.1},
}

// EstimateMagneticDeclination returns the magnetic declination in decimal
// degrees for a WGS-84 position and instant, positive east and negative west.
// It is the angle to add to a magnetic compass heading to obtain a true
// heading.
//
// The estimate is the World Magnetic Model evaluated at sea level. A caller
// with no position has no declination: the model is a function of place, and
// guessing one from a nearby city would be worse than reporting the magnetic
// heading as magnetic. A zero time is read as now.
func EstimateMagneticDeclination(lat, lng float64, t time.Time) float64 {
	x, y, _ := magneticFieldComponents(lat, lng, wmmDefaultAltitudeKm, wmmDecimalYear(t))
	return degrees(math.Atan2(y, x))
}

// wmmDecimalYear renders an instant as the decimal year the model is evaluated
// at, so the secular variation is applied for the fraction of the year that has
// actually passed. A zero time is read as now.
func wmmDecimalYear(t time.Time) float64 {
	if t.IsZero() {
		t = time.Now()
	}
	utc := t.UTC()
	start := time.Date(utc.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(utc.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	return float64(utc.Year()) + float64(utc.Sub(start))/float64(end.Sub(start))
}

// wmmTimeDelta is the number of years the coefficients are propagated from the
// model's epoch, clamped to the model's published validity window.
func wmmTimeDelta(year float64) float64 {
	return min(max(year-wmmEpoch, 0), wmmValidityYears)
}

// magneticFieldComponents evaluates the World Magnetic Model at a geodetic
// position and returns the north, east, and downward field components in
// nanotesla. It is the whole engine: declination, inclination, and total
// intensity are all read off this one vector.
//
// The model is defined in geocentric spherical coordinates over an ellipsoid's
// surface, so the position is first converted to a geocentric radius and
// colatitude, the harmonics are summed there, and the resulting vector is
// rotated back into the geodetic frame an operator's map and compass use.
func magneticFieldComponents(lat, lng, altitudeKm, year float64) (north, east, down float64) {
	dt := wmmTimeDelta(year)
	latGeodetic := radians(lat)
	sinLat, cosLat := math.Sin(latGeodetic), math.Cos(latGeodetic)
	semiMajor2 := wmmSemiMajorAxisKm * wmmSemiMajorAxisKm
	semiMinor2 := wmmSemiMinorAxisKm * wmmSemiMinorAxisKm
	eccentricity2 := 1 - semiMinor2/semiMajor2
	primeVertical := wmmSemiMajorAxisKm / math.Sqrt(1-eccentricity2*sinLat*sinLat)
	rho := (primeVertical + altitudeKm) * cosLat
	zAxis := (primeVertical*(1-eccentricity2) + altitudeKm) * sinLat
	radius := math.Hypot(rho, zAxis)
	latGeocentric := math.Atan2(zAxis, rho)
	colatitude := math.Pi/2 - latGeocentric

	width := wmmMaxOrder + 1
	legendre := make([]float64, width*width)
	derivative := make([]float64, width*width)
	wmmAssociatedLegendre(colatitude, wmmMaxOrder, legendre, derivative)
	sinColatitude := math.Sin(colatitude)
	longitude := radians(lng)

	for i := range wmm2025Coefficients {
		coefficient := &wmm2025Coefficients[i]
		g := coefficient.g + dt*coefficient.dg
		h := coefficient.h + dt*coefficient.dh
		index := coefficient.n*width + coefficient.m
		order := float64(coefficient.m)
		cosOrder := math.Cos(order * longitude)
		sinOrder := math.Sin(order * longitude)
		harmonic := g*cosOrder + h*sinOrder
		radial := math.Pow(wmmReferenceRadiusKm/radius, float64(coefficient.n+2))
		north += radial * harmonic * derivative[index]
		if sinColatitude != 0 {
			east += radial * order * (g*sinOrder - h*cosOrder) * legendre[index] / sinColatitude
		}
		down += -radial * float64(coefficient.n+1) * harmonic * legendre[index]
	}

	// Rotate the geocentric vector into the geodetic frame. The angle between
	// the two verticals is the difference between the geodetic and geocentric
	// latitudes, which vanishes at the poles and the equator and peaks near the
	// mid-latitudes at about a tenth of a degree on the WGS-84 ellipsoid.
	tilt := latGeodetic - latGeocentric
	cosTilt, sinTilt := math.Cos(tilt), math.Sin(tilt)
	return north*cosTilt + down*sinTilt, east, -north*sinTilt + down*cosTilt
}

// wmmAssociatedLegendre fills the Schmidt semi-normalised associated Legendre
// functions P(n,m) of the cosine of a colatitude together with their
// derivatives with respect to the colatitude. Both are written into flat
// (maxOrder+1)-wide row-major tables indexed by n*width+m.
//
// The functions are built from the unnormalised Ferrers form, which carries no
// Condon-Shortley phase because the geomagnetic convention does not use one,
// and are then Schmidt normalised with the square root of
// (2-delta(m,0))(n-m)!/(n+m)!. The derivative is carried through the same
// recursion rather than differenced afterwards, because differencing loses
// precision exactly where the high-order terms matter.
func wmmAssociatedLegendre(colatitude float64, maxOrder int, p, d []float64) {
	width := maxOrder + 1
	cosTheta, sinTheta := math.Cos(colatitude), math.Sin(colatitude)
	at := func(n, m int) int { return n*width + m }
	p[at(0, 0)] = 1
	for m := 1; m <= maxOrder; m++ {
		p[at(m, m)] = float64(2*m-1) * sinTheta * p[at(m-1, m-1)]
		d[at(m, m)] = float64(2*m-1) * (sinTheta*d[at(m-1, m-1)] + cosTheta*p[at(m-1, m-1)])
	}
	for m := 0; m <= maxOrder; m++ {
		for n := m + 1; n <= maxOrder; n++ {
			if n == m+1 {
				p[at(n, m)] = float64(2*n-1) * cosTheta * p[at(n-1, m)]
				d[at(n, m)] = float64(2*n-1) * (cosTheta*d[at(n-1, m)] - sinTheta*p[at(n-1, m)])
				continue
			}
			p[at(n, m)] = (float64(2*n-1)*cosTheta*p[at(n-1, m)] -
				float64(n+m-1)*p[at(n-2, m)]) / float64(n-m)
			d[at(n, m)] = (float64(2*n-1)*(cosTheta*d[at(n-1, m)]-sinTheta*p[at(n-1, m)]) -
				float64(n+m-1)*d[at(n-2, m)]) / float64(n-m)
		}
	}
	for n := 0; n <= maxOrder; n++ {
		for m := 0; m <= n; m++ {
			factor := 1.0
			if m > 0 {
				factor = 2.0
			}
			norm := math.Sqrt(factor * math.Gamma(float64(n-m)+1) / math.Gamma(float64(n+m)+1))
			p[at(n, m)] *= norm
			d[at(n, m)] *= norm
		}
	}
}
