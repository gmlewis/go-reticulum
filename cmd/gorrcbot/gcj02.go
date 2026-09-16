// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the WGS-84 <-> GCJ-02 coordinate converter, the datum shift a
// Chinese domestic map applies to every position it displays.
//
// China mandates that domestic maps publish coordinates in GCJ-02, the datum
// behind Gaode/Amap, Tencent Maps, and Baidu's BD-09 base. GCJ-02 is GPS with a
// deliberately non-linear offset applied — several hundred meters, varying by
// location — so a coordinate copied out of a Chinese map app and fed to a GPS
// receiver lands in the wrong street, and a GPS coordinate dropped into Amap
// lands several hundred meters away. There is no published inverse; the offset
// function is only defined in the forward direction, so the inverse here is
// recovered by fixed-point refinement of the forward transform.
//
// The two directions are the only thing this file does. It is pure arithmetic on
// the Krassovsky 1940 ellipsoid, the ellipsoid the offset is defined against, so
// it needs no map data and no network — an off-grid operator converting the
// coordinate of a relay copied from a Chinese map has exactly the same answer as
// one with a live connection.
//
// The formulas are the widely published closed form (the "eviltransform"
// reference implementation and the tens of ports derived from it). Reproducing
// them exactly matters: any deviation puts a coordinate in a different place
// than every Chinese map app does.

package main

import (
	"math"
	"strings"
)

// The Krassovsky 1940 ellipsoid, which is the ellipsoid the GCJ-02 offset
// equations are stated against.
const (
	// gcjSemiMajorAxisMeters is the ellipsoid's semi-major axis, a.
	gcjSemiMajorAxisMeters = 6378245.0
	// gcjEccentricitySquared is the ellipsoid's first eccentricity squared,
	// ee = 2f - f^2.
	gcjEccentricitySquared = 0.00669342162296594323
)

// The bounding box China's mandate covers. A coordinate outside it is never
// offset, which is what keeps Hong Kong's offshore waters and every foreign
// position bit-for-bit unchanged.
const (
	gcjChinaMinLat = 0.8293
	gcjChinaMaxLat = 55.8271
	gcjChinaMinLng = 72.004
	gcjChinaMaxLng = 137.8347
)

// GCJ-02 refinement bounds. The forward offset function is smooth and nearly
// identity, so the fixed-point iteration below converges to better than the
// hundredth of a meter within a couple of steps; the ceiling exists only so a
// pathological input cannot spin.
const (
	// gcjRefineIterations bounds one inverse conversion.
	gcjRefineIterations = 16
	// gcjRefineToleranceDegrees is the residual at which the inverse stops.
	// One ten-millionth of a degree is about a centimeter.
	gcjRefineToleranceDegrees = 1e-10
)

// gcjPrefixes are the notation markers that tell the location parser a
// coordinate is a Mars coordinate rather than GPS.
var gcjPrefixes = []string{"gcj02:", "gcj:"}

// IsInChina reports whether a coordinate falls inside the area China's GCJ-02
// mandate covers. It is a plain bounding box, which is what the reference
// implementations use: it deliberately includes some offshore and border
// territory, because a coordinate is only ever offset by the map app that
// produced it, and the cost of one extra offset is far smaller than the cost of
// leaving a real one out.
func IsInChina(lat, lng float64) bool {
	return lat >= gcjChinaMinLat && lat <= gcjChinaMaxLat &&
		lng >= gcjChinaMinLng && lng <= gcjChinaMaxLng
}

// WGS84ToGCJ02 converts a GPS coordinate to the GCJ-02 "Mars coordinate" a
// Chinese domestic map displays. A coordinate outside China is returned
// unchanged, because the offset is not defined there.
func WGS84ToGCJ02(wgsLat, wgsLng float64) (gcjLat, gcjLng float64) {
	if !IsInChina(wgsLat, wgsLng) {
		return wgsLat, wgsLng
	}
	deltaLat, deltaLng := gcjOffset(wgsLat, wgsLng)
	return wgsLat + deltaLat, wgsLng + deltaLng
}

// GCJ02ToWGS84 converts a GCJ-02 Mars coordinate back to the GPS position
// behind it. The offset function is only published in the forward direction, so
// the inverse is recovered by refinement: the input is treated as a first guess
// at the GPS coordinate, the forward transform is applied to that guess, and the
// residual is fed back. Because the offset changes slowly, three or four steps
// land within a centimeter; the iteration stops as soon as it stops moving.
func GCJ02ToWGS84(gcjLat, gcjLng float64) (wgsLat, wgsLng float64) {
	if !IsInChina(gcjLat, gcjLng) {
		return gcjLat, gcjLng
	}
	wgsLat, wgsLng = gcjLat, gcjLng
	for range gcjRefineIterations {
		gotLat, gotLng := WGS84ToGCJ02(wgsLat, wgsLng)
		residualLat := gcjLat - gotLat
		residualLng := gcjLng - gotLng
		if math.Abs(residualLat) < gcjRefineToleranceDegrees &&
			math.Abs(residualLng) < gcjRefineToleranceDegrees {
			break
		}
		wgsLat += residualLat
		wgsLng += residualLng
	}
	return wgsLat, wgsLng
}

// gcjOffset returns the latitude and longitude displacement GCJ-02 applies at a
// position, in degrees. The latitude and longitude offsets run on the same
// polynomial-plus-trigonometric shape, evaluated at the position's distance from
// the (105E, 35N) origin the offset is defined about, and then divided by the
// local meridional and prime-vertical radii of curvature on the Krassovsky
// ellipsoid to turn a distance in meters into a change in degrees.
func gcjOffset(lat, lng float64) (deltaLat, deltaLng float64) {
	x := lng - 105.0
	y := lat - 35.0

	deltaLat = gcjLatitudeShift(x, y)
	deltaLng = gcjLongitudeShift(x, y)

	radLat := radians(lat)
	magic := math.Sin(radLat)
	magic = 1 - gcjEccentricitySquared*magic*magic
	sqrtMagic := math.Sqrt(magic)

	// The denominators are the ellipsoid's radii of curvature scaled by the
	// degrees-to-radians factor, which is what makes the result a delta in
	// degrees rather than in meters.
	deltaLat = (deltaLat * 180.0) /
		((gcjSemiMajorAxisMeters * (1 - gcjEccentricitySquared)) / (magic * sqrtMagic) * math.Pi)
	deltaLng = (deltaLng * 180.0) /
		(gcjSemiMajorAxisMeters / sqrtMagic * math.Cos(radLat) * math.Pi)
	return deltaLat, deltaLng
}

// gcjLatitudeShift is the raw latitude displacement polynomial, before it is
// divided by the ellipsoid's radius of curvature.
func gcjLatitudeShift(x, y float64) float64 {
	shift := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	shift += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	shift += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	shift += (160.0*math.Sin(y/12.0*math.Pi) + 320.0*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return shift
}

// gcjLongitudeShift is the raw longitude displacement polynomial, before it is
// divided by the ellipsoid's radius of curvature.
func gcjLongitudeShift(x, y float64) float64 {
	shift := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	shift += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	shift += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	shift += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return shift
}

// trimGCJPrefix reports whether text carries a Mars-coordinate marker and
// returns the coordinate text behind it. Both "gcj:" and "gcj02:" are accepted,
// in any case, with or without a space after the colon.
func trimGCJPrefix(text string) (string, bool) {
	lower := strings.ToLower(text)
	for _, prefix := range gcjPrefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		return strings.TrimSpace(text[len(prefix):]), true
	}
	return "", false
}

// FormatGCJ02 renders a WGS-84 coordinate as the GCJ-02 pair a Chinese map app
// accepts, or as an empty string when the position is outside China and the two
// datums are the same. It is what a tower answer prints when a station is in
// China, so the operator can paste it straight into Amap or WeChat.
func FormatGCJ02(lat, lng float64) string {
	if !IsInChina(lat, lng) {
		return ""
	}
	gcjLat, gcjLng := WGS84ToGCJ02(lat, lng)
	return FormatLatLng(LatLng{Lat: gcjLat, Lng: gcjLng})
}
