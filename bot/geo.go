// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file forwards the geodesy engine to package geo; see olc.go in this
// package for why the implementation moved. It also re-exposes the small angle
// and coordinate-formatting helpers that other files in this package call, so
// none of the bot's own code had to change when the engine moved.
//
// It is also where a location becomes a position the bot can use: parseLocation
// supplies the one thing the engine cannot supply for itself, which is where the
// reader is standing.

package bot

import (
	"errors"
	"slices"
	"strings"

	"github.com/gmlewis/go-reticulum/geo"
)

// ErrLocationUnrecognized reports text that is not a location in any notation
// this engine accepts.
var ErrLocationUnrecognized = geo.ErrLocationUnrecognized

// ErrShortLocationNeedsFix reports a shortened Plus Code handed to a node with no
// live GNSS fix. The code itself is valid; what is missing is the position that
// says which region it names, so the reply carrying this error says that instead
// of calling a real Plus Code unreadable.
var ErrShortLocationNeedsFix = errors.New("a shortened Plus Code needs a live GNSS fix to say which region it is in")

// shortCodeNoFixLine is the reply line those errors become. It names what is
// missing and what to send instead, because the operator holding a code from a
// phone that showed it shortened has no other way to tell the bot what was meant.
var shortCodeNoFixLine = ErrShortLocationNeedsFix.Error() + " — send the full code or a coordinate instead"

// CompassPoints is the sixteen-point compass rose, in clockwise order from
// north. The names are the standard three-letter abbreviations.
var CompassPoints = geo.CompassPoints

// ParseLocation turns a location a human typed into a coordinate, accepting a
// full Plus Code, decimal degrees, degrees/minutes/seconds, degrees and decimal
// minutes, and a Maidenhead grid locator.
func ParseLocation(input string) (LatLng, error) { return geo.ParseLocation(input) }

// ParseLocationWithReference turns a location a human typed into a coordinate,
// completing a shortened Plus Code against reference. Every notation
// ParseLocation accepts resolves identically; the short code is the one that
// needs the position of the reader.
func ParseLocationWithReference(input string, reference LatLng) (LatLng, error) {
	return geo.ParseLocationWithReference(input, reference)
}

// NeedsReferenceLocation reports whether input is a location that cannot be
// placed without one — a shortened Plus Code.
func NeedsReferenceLocation(input string) bool { return geo.NeedsReferenceLocation(input) }

// parseLocation resolves a location a human typed against the live GNSS fix. A
// full Plus Code, a coordinate pair, and a Maidenhead grid name their own place
// and are resolved by notation alone; a shortened Plus Code — the form a phone
// shows and a person reads out, such as "CG4J+32P" — names its region only
// relative to somewhere already known, so the node's own fix completes it. That
// is the search-and-rescue case the field commands exist for: a code read off
// somebody else's screen is placed as the matching cell nearest the reader, which
// is the cell the sender was in while the two are within about half a degree of
// each other. Beyond that the short code cannot say which region was meant, and
// the answer names the full code so a reader can see the region it picked.
//
// Text that is not a location at all comes back exactly as ParseLocation reports
// it, and a short code with no fix to place it comes back as
// ErrShortLocationNeedsFix, so a command can tell a typo from a missing receiver.
func (r *registry) parseLocation(text string) (LatLng, error) {
	fix, haveFix := r.currentFix()
	if haveFix {
		return ParseLocationWithReference(text, fix.Position())
	}
	point, err := ParseLocation(text)
	if err != nil && NeedsReferenceLocation(text) {
		return LatLng{}, ErrShortLocationNeedsFix
	}
	return point, err
}

// locationFailureText renders the reason a command could not place the single
// location it was handed. A shortened Plus Code the node had no fix to complete
// gets its own sentence: it is a valid code, and the notation help would send the
// operator looking for a spelling mistake that is not there.
func locationFailureText(err error) string {
	if errors.Is(err, ErrShortLocationNeedsFix) {
		return shortCodeNoFixLine
	}
	return "no location found — " + locationNotationHelp
}

// helpOrShortCodeReason returns the second line of a usage reply: the command's
// own help, or the reason a shortened Plus Code in the argument could not be
// placed. The reason wins when the node has no fix to complete the code with,
// because in that case the help would blame a notation that is in fact correct.
func (c *commandContext) helpOrShortCodeReason(args, help string) string {
	if _, haveFix := c.reg.currentFix(); haveFix {
		return help
	}
	if slices.ContainsFunc(strings.Fields(args), NeedsReferenceLocation) {
		return shortCodeNoFixLine
	}
	return help
}

// HaversineDistance returns the great-circle distance between two coordinates
// in meters.
func HaversineDistance(p1, p2 LatLng) float64 { return geo.HaversineDistance(p1, p2) }

// InitialBearing returns the initial great-circle bearing from p1 to p2 in
// degrees, in the range 0 to 360 exclusive.
func InitialBearing(p1, p2 LatLng) float64 { return geo.InitialBearing(p1, p2) }

// CompassPoint names the sixteen-point compass sector a bearing falls in.
func CompassPoint(bearing float64) string { return geo.CompassPoint(bearing) }

// ProjectWaypoint returns the coordinate reached by travelling distMeters from
// origin along the great circle that leaves origin on bearingDeg.
func ProjectWaypoint(origin LatLng, bearingDeg, distMeters float64) LatLng {
	return geo.ProjectWaypoint(origin, bearingDeg, distMeters)
}

// LatLngToMaidenhead renders a coordinate as a six-character Maidenhead grid
// locator.
func LatLngToMaidenhead(lat, lng float64) string { return geo.LatLngToMaidenhead(lat, lng) }

// MaidenheadToLatLng converts a Maidenhead grid locator to a coordinate.
func MaidenheadToLatLng(grid string) (LatLng, error) { return geo.MaidenheadToLatLng(grid) }

// ParseDistance parses a distance a human typed (e.g. "15km", "3 miles") into
// meters.
func ParseDistance(text string) (float64, error) { return geo.ParseDistance(text) }

// ParseBearing parses a bearing a human typed (e.g. "045", "NE") into degrees.
func ParseBearing(text string) (float64, error) { return geo.ParseBearing(text) }

// FormatDistance renders a distance in meters the way the navigation commands
// print it: meters below 1 km, one decimal place below 1,000 km, whole
// kilometers above that.
func FormatDistance(meters float64) string { return geo.FormatDistance(meters) }

// FormatBearing renders a bearing as three digits plus a compass point.
func FormatBearing(bearing float64) string { return geo.FormatBearing(bearing) }

// FormatLatLng renders a coordinate pair as signed decimal degrees, the
// machine-readable form used when a coordinate must round-trip exactly.
func FormatLatLng(p LatLng) string { return geo.FormatLatLng(p) }

// The unexported helpers below are used by other files in this package. They
// forward to the engine so the moved code stays the single implementation.

// radians converts degrees to radians.
func radians(deg float64) float64 { return geo.Radians(deg) }

// degrees converts radians to degrees.
func degrees(rad float64) float64 { return geo.Degrees(rad) }

// normalizeDegrees wraps a degree value into 0..360.
func normalizeDegrees(value float64) float64 { return geo.NormalizeDegrees(value) }

// normalizeLongitude wraps a longitude into -180..180, excluding 180.
func normalizeLongitude(lng float64) float64 { return geo.NormalizeLongitude(lng) }

// formatDD renders decimal degrees with hemisphere letters.
func formatDD(p LatLng) string { return geo.FormatDD(p) }

// formatDDM renders degrees and decimal minutes.
func formatDDM(p LatLng) string { return geo.FormatDDM(p) }

// hemisphereLetter picks the hemisphere letter for a signed coordinate.
func hemisphereLetter(value float64, positive, negative string) string {
	return geo.HemisphereLetter(value, positive, negative)
}
