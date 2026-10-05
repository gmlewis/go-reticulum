// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file forwards the WGS-84 <-> GCJ-02 coordinate converter to package geo;
// see olc.go in this package for why the implementation moved.

package bot

import "github.com/gmlewis/go-reticulum/geo"

// IsInChina reports whether a WGS-84 coordinate falls inside the region where
// Chinese domestic maps apply the GCJ-02 offset.
func IsInChina(lat, lng float64) bool { return geo.IsInChina(lat, lng) }

// WGS84ToGCJ02 converts a GPS coordinate to the GCJ-02 datum Chinese domestic
// map apps display.
func WGS84ToGCJ02(wgsLat, wgsLng float64) (gcjLat, gcjLng float64) {
	return geo.WGS84ToGCJ02(wgsLat, wgsLng)
}

// GCJ02ToWGS84 recovers the GPS coordinate behind a GCJ-02 coordinate.
func GCJ02ToWGS84(gcjLat, gcjLng float64) (wgsLat, wgsLng float64) {
	return geo.GCJ02ToWGS84(gcjLat, gcjLng)
}

// FormatGCJ02 renders a coordinate in the GCJ-02 datum the way the bot prints
// the "Mars coordinate" for a position inside China.
func FormatGCJ02(lat, lng float64) string { return geo.FormatGCJ02(lat, lng) }
