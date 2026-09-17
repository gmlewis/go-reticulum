// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the /whereami subsystem: the operational location card a
// field operator reads before moving, and the geodetic synthesis behind it.
//
// The card is deliberately assembled entirely from in-tree, offline engines —
// the Open Location Code encoder, the Maidenhead grid converter, the GCJ-02
// datum conversion, and the solar ephemeris — so a node in a slot canyon, under
// tree canopy, or after a disaster still answers instantly with no radio hop,
// no airtime, and no RF emission. That is the Autonomous Local Intelligence
// Rule the Go Reticulum Lifesaver is built around: the one question that must
// never depend on a link is "where am I?".
//
// Every notation on the card exists because somebody else needs it: a Plus Code
// is what a rescue dispatcher can paste into a map, a Maidenhead square is what
// an amateur radio operator logs, decimal degrees are what a GPS unit takes, and
// a GCJ-02 pair is the only form a Chinese domestic map app accepts. The card
// prints all of them at once rather than making the operator choose.

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// whereamiUsage is the usage line for the command.
const whereamiUsage = "whereami [pluscode|coords|grid]"

// whereamiCardRule is the rule that brackets the card, matched on both sides so
// the card is recognizable in a scrollback that holds several replies.
const whereamiCardRule = "------------------------------------------------------------"

// whereamiNoFixLine is the answer when there is no receiver, no fix, and no
// argument: the command says what it is waiting for rather than guessing.
const whereamiNoFixLine = "/whereami: no GNSS fix yet — acquiring; " +
	"give a location to answer immediately (" + whereamiUsage + ")"

// discoveryNoFixHint is the second line a zero-argument proximity request gets
// when there is no fix to fall back on. It is printed instead of a bare usage
// line because the useful answer to "I do not know where you are" is what to
// type instead.
const discoveryNoFixHint = "no GNSS fix yet — name a location instead " +
	"(coordinates, a plus code, a grid, or a place name)"

// gnssContext returns the live GNSS fix's position together with the label a
// discovery header shows for it, and reports false when there is no usable fix.
// Every zero-argument location fallback goes through here, so one receiver
// feeds every command at once.
func (c *commandContext) gnssContext() (LatLng, string, bool) {
	fix, ok := c.reg.currentFix()
	if !ok {
		return LatLng{}, "", false
	}
	point := fix.Position()
	return point, gnssLabel(point), true
}

// gnssLabel names a position the receiver reported, in the notation an operator
// can read back over a voice link and a dispatcher can paste into a map. A
// coordinate the encoder cannot turn into a code — which cannot happen for a
// validated fix — falls back to a plain description rather than to an empty
// header.
func gnssLabel(point LatLng) string {
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		return "current position"
	}
	return code
}

// The two sources a position on the card can have.
const (
	// whereamiSourceGNSS reports that the position came from the receiver.
	whereamiSourceGNSS = "gnss"
	// whereamiSourceManual reports that the position came from the argument,
	// which also means no altitude and no fix quality are known.
	whereamiSourceManual = "manual position"
)

// Geodetic constants the card needs.
const (
	// feetPerMeter converts the receiver's metric altitude to the feet a
	// North American operator thinks in.
	feetPerMeter = 3.280839895013123
	// whereamiPlusPrecision is the card's Plus Code precision: ten significant
	// characters, about 14 m by 14 m.
	whereamiPlusPrecision = 10
	// whereamiPlusHighPrecision is the extra-precise code the JSON dashboard
	// carries: eleven significant characters, about 3 m by 3 m.
	whereamiPlusHighPrecision = 11
	// degreesPerSolarZone is how many degrees of longitude one hour of local
	// solar time is worth.
	degreesPerSolarZone = 15
	// whereamiMinSolarZone and whereamiMaxSolarZone bound the solar offset in
	// hours, so an extreme longitude cannot produce a zone no clock uses.
	whereamiMinSolarZone = -12
	whereamiMaxSolarZone = 14
	// whereamiMetersPerDegreeLat is the length of one degree of latitude, which
	// is close enough to constant for the area estimate the card prints.
	whereamiMetersPerDegreeLat = 111320.0
)

// whereAmI is one fully synthesized operational location: the fix, the position,
// every notation the card prints, and the almanac it was computed against.
type whereAmI struct {
	// Fix is the GNSS fix behind the card, zero when the position is manual.
	Fix GPSFix
	// Point is the position the card describes.
	Point LatLng
	// Source is where the position came from: the GNSS receiver or the
	// operator's argument.
	Source string
	// Now is the instant the card was rendered for.
	Now time.Time
	// PlusCode is the ten-character Open Location Code.
	PlusCode string
	// PlusCode11 is the eleven-character code, which the JSON dashboard
	// carries but the card does not print.
	PlusCode11 string
	// Area is the rectangle the ten-character code identifies.
	Area CodeArea
	// AreaLatMeters and AreaLngMeters are the area's extents in meters at the
	// equator, which is the precision figure the Open Location Code
	// specification publishes: about 14 m by 14 m for ten characters. The real
	// ground size narrows with the cosine of the latitude, but the published
	// figure is the one every other tool quotes, so it is the one an operator
	// comparing codes should see.
	AreaLatMeters float64
	AreaLngMeters float64
	// Maidenhead is the six-character grid locator.
	Maidenhead string
	// GCJLat, GCJLng, and GCJ02 are the China datum conversion, empty outside
	// China, where the two datums are identical.
	GCJLat float64
	GCJLng float64
	GCJ02  string
	// Almanac is the day's solar ephemeris at the position.
	Almanac SolarDay
	// Zone and ZoneLabel are the local solar time zone the longitude defines.
	Zone      *time.Location
	ZoneLabel string
	// FixStatus is the one-line receiver status.
	FixStatus string
}

// buildWhereAmI synthesizes the card's content for one position. A manual
// position carries no altitude and no fix quality, and the card says so instead
// of printing the zero values as if a receiver had reported them.
func buildWhereAmI(fix GPSFix, point LatLng, source string, now time.Time) (whereAmI, error) {
	code, err := EncodeOLC(point.Lat, point.Lng, whereamiPlusPrecision)
	if err != nil {
		return whereAmI{}, fmt.Errorf("whereami: could not build a Plus Code: %w", err)
	}
	highCode, err := EncodeOLC(point.Lat, point.Lng, whereamiPlusHighPrecision)
	if err != nil {
		return whereAmI{}, fmt.Errorf("whereami: could not build a precise Plus Code: %w", err)
	}
	area, err := DecodeOLC(code)
	if err != nil {
		return whereAmI{}, fmt.Errorf("whereami: could not measure the Plus Code area: %w", err)
	}
	zone, label := solarZone(point.Lng)
	// The almanac is asked for the operator's local calendar day, because
	// "how much daylight is left today" is a question about the day the
	// operator is standing in, not about the UTC day.
	local := now.In(zone)
	localDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	result := whereAmI{
		Fix:           fix,
		Point:         point,
		Source:        source,
		Now:           now,
		PlusCode:      code,
		PlusCode11:    highCode,
		Area:          area,
		AreaLatMeters: (area.North - area.South) * whereamiMetersPerDegreeLat,
		AreaLngMeters: (area.East - area.West) * whereamiMetersPerDegreeLat,
		Maidenhead:    LatLngToMaidenhead(point.Lat, point.Lng),
		Almanac:       SolarAlmanac(point.Lat, point.Lng, localDay),
		Zone:          zone,
		ZoneLabel:     label,
	}
	if gcj := FormatGCJ02(point.Lat, point.Lng); gcj != "" {
		result.GCJLat, result.GCJLng = WGS84ToGCJ02(point.Lat, point.Lng)
		result.GCJ02 = "GCJ-02 (Amap/Gaode/WeChat): " + gcj
	}
	result.FixStatus = whereamiFixStatus(result)
	return result, nil
}

// solarZone returns the local solar time zone a longitude defines and its
// label. A node with no timezone database cannot know an operator's civil zone
// — or whether summer time is in force — so it reports the zone the sun keeps,
// which is derived from the position alone and is therefore reproducible
// anywhere.
func solarZone(lng float64) (*time.Location, string) {
	offset := int(math.Round(lng / degreesPerSolarZone))
	offset = max(offset, whereamiMinSolarZone)
	offset = min(offset, whereamiMaxSolarZone)
	label := fmt.Sprintf("UTC%+d", offset)
	return time.FixedZone(label, offset*3600), label
}

// whereamiFixStatus renders the receiver line: the fix kind, how many
// satellites are behind it, the dilution of precision, and — for a solution
// better than a plain fix — the quality word a receiver manual uses.
func whereamiFixStatus(w whereAmI) string {
	if w.Source != whereamiSourceGNSS {
		return "manual position — no GNSS fix or altitude"
	}
	if !w.Fix.Valid {
		return "no fix — acquiring"
	}
	status := fmt.Sprintf("3D Fix (%v satellites, HDOP %v)", w.Fix.Satellites, w.Fix.HDOP)
	switch w.Fix.FixQuality {
	case 2:
		return status + " (DGPS)"
	case 4:
		return status + " (RTK fixed)"
	case 5:
		return status + " (RTK float)"
	}
	return status
}

// renderWhereAmICard renders the operational card itself.
func renderWhereAmICard(w whereAmI) []string {
	lines := []string{"/whereami", whereamiCardRule}
	lines = append(lines, fmt.Sprintf("Plus Code (OLC)   : %v (Area: ~%vm x %vm)",
		w.PlusCode, math.Round(w.AreaLatMeters), math.Round(w.AreaLngMeters)))
	lines = append(lines, fmt.Sprintf("Coordinates       : %v", formatCardCoordinates(w.Point)))
	lines = append(lines, fmt.Sprintf("Maidenhead Grid   : %v (Amateur Radio QTH)", w.Maidenhead))
	lines = append(lines, "Elevation         : "+whereamiElevation(w))
	lines = append(lines, "GNSS Fix Status   : "+w.FixStatus)
	lines = append(lines, fmt.Sprintf("Local Solar Time  : %v %v (Solar noon: %v)",
		w.Now.In(w.Zone).Format("15:04"), w.ZoneLabel, w.Almanac.Noon.In(w.Zone).Format("15:04")))
	lines = append(lines, whereamiSunsetLine(w))
	if w.GCJ02 != "" {
		lines = append(lines, w.GCJ02)
	}
	return append(lines, whereamiCardRule)
}

// whereamiElevation renders the altitude line, or says it is unknown when the
// position came from an argument rather than a receiver, or from a fix that
// never carried a measured height.
func whereamiElevation(w whereAmI) string {
	if w.Source != whereamiSourceGNSS || !w.Fix.Valid || !w.Fix.HasAltitude {
		return "unknown (no GNSS altitude)"
	}
	return fmt.Sprintf("%v m (%v ft) MSL",
		math.Round(w.Fix.AltitudeM), math.Round(w.Fix.AltitudeM*feetPerMeter))
}

// whereamiSunsetLine renders the daylight the operator has left: a countdown
// while the sun is up, a plain statement once it is down, and the almanac's own
// words at the latitudes where the sun does not rise or does not set.
func whereamiSunsetLine(w whereAmI) string {
	return "Sunset Countdown  : " + whereamiSunsetText(w)
}

// whereamiSunsetText is the sunset statement without the card's label, so the
// captive portal's JSON carries exactly the words the card prints.
func whereamiSunsetText(w whereAmI) string {
	switch {
	case w.Almanac.MidnightSun:
		return "midnight sun — the sun does not set today"
	case w.Almanac.PolarNight:
		return "polar night — the sun does not rise today"
	case w.Almanac.Sunset.IsZero():
		return "no sunset reported for this position today"
	}
	sunset := w.Almanac.Sunset.In(w.Zone)
	if remaining := w.Almanac.Sunset.Sub(w.Now); remaining > 0 {
		return fmt.Sprintf("Sunset at %v (%v daylight remaining)", sunset.Format("15:04"),
			formatDaylightRemaining(remaining))
	}
	return fmt.Sprintf("dark — the sun set at %v", sunset.Format("15:04"))
}

// formatCardCoordinates renders a position the way the card and the dashboard
// both show it: five decimal places, hemispheres named, and no negative signs to
// mistake for a typo read aloud.
func formatCardCoordinates(point LatLng) string {
	return fmt.Sprintf("%.5f° %v, %.5f° %v",
		math.Abs(point.Lat), hemisphereLetter(point.Lat, "N", "S"),
		math.Abs(point.Lng), hemisphereLetter(point.Lng, "E", "W"))
}

// formatDaylightRemaining renders a positive daylight span as hours and
// minutes, rounded to the minute so a countdown does not jitter every read.
func formatDaylightRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Minute).Minutes())
	return fmt.Sprintf("%vh %vm", total/60, total%60)
}

// runWhereami answers one whereami request: an explicit position when the
// operator gave one, and otherwise the live GNSS fix.
func (c *commandContext) runWhereami() []string {
	if args := strings.TrimSpace(c.Args); args != "" {
		point, err := ParseLocation(args)
		if err != nil {
			return []string{"whereami: no location found in " + safeEcho(args, maxDiscoveryEchoBytes),
				locationNotationHelp}
		}
		return c.renderWhereami(point, whereamiSourceManual)
	}
	fix, ok := c.reg.currentFix()
	if !ok {
		return []string{whereamiNoFixLine}
	}
	return c.renderWhereami(fix.Position(), whereamiSourceGNSS)
}

// renderWhereami builds and renders one card, downgrading an unexpected
// geodetic failure to a single readable line rather than a panic on a link.
func (c *commandContext) renderWhereami(point LatLng, source string) []string {
	card, err := buildWhereAmI(currentFixFor(c.reg, point, source), point, source, c.now())
	if err != nil {
		logf("whereami: %v", err)
		return []string{"whereami: " + err.Error()}
	}
	return renderWhereAmICard(card)
}

// currentFixFor returns the fix a card is built from: the live one for a GNSS
// position, and the zero fix for a manual one, so a manual card can never
// accidentally print the altitude of a fix at another place.
func currentFixFor(reg *registry, point LatLng, source string) GPSFix {
	if source == whereamiSourceGNSS && reg != nil && reg.gps != nil {
		return reg.gps.LastFix()
	}
	return GPSFix{}
}
