// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the four navigation commands: loc, dist, proj, and sun. They
// are the thin layer between the geodesy and almanac engines and the command
// registry, and their whole job is to resolve what a person typed, call the
// engine, and render the one or two lines the answer fits in.
//
// A location is always resolved offline. A place NAME cannot be: resolving one
// would need a geocoder, and a mesh node has no internet. The commands accept
// the five notations a person can read off a map, a GPS, or a radio log, and
// say plainly what they accept when given anything else.

package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Navigation command usage lines and wordings.
const (
	// locUsage is the usage line for the loc command.
	locUsage = "loc <pluscode|coords|grid>"
	// distUsage is the usage line for the dist command.
	distUsage = "dist <from> <to>"
	// projUsage is the usage line for the proj command.
	projUsage = "proj <origin> <bearing> <distance>"
	// sunUsage is the usage line for the sun command.
	sunUsage = "sun <pluscode|coords|grid> [date]"
	// locationNotationHelp names the notations the navigation commands accept.
	// It is printed instead of a bare error, because the useful answer to a
	// mistyped location is what a location may look like.
	locationNotationHelp = "a location may be a Plus Code, decimal degrees, DMS, DDM, or a Maidenhead grid"
	// projDistanceHelp is the distance vocabulary the proj command explains.
	projDistanceHelp = "a distance is a number with a unit, like 3.5km, 2mi, 1nm, 1500m, or 500ft"
	// projBearingHelp is the bearing vocabulary the proj command explains.
	projBearingHelp = "a bearing is degrees (048) or a compass point (NE)"
)

// olcCodeLength is the Plus Code precision every answer prints. Ten characters
// is about 13.9 by 13.9 meters at the equator: precise enough to name a
// building or a trail junction, short enough to read out over a voice link.
const olcCodeLength = 10

// runLoc resolves one location and renders it in every notation the bot can
// produce, so a person who has it in one form can read it in the form the
// person at the other end of the link uses.
func (c *commandContext) runLoc() []string {
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + locUsage, locationNotationHelp}
	}
	point, err := ParseLocation(c.Args)
	if err != nil {
		return []string{"loc: no location found — " + locationNotationHelp}
	}
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		return []string{"loc: could not build a Plus Code: " + err.Error()}
	}
	return []string{fmt.Sprintf("DD: %v | DDM: %v | Grid: %v | OLC: %v",
		formatDD(point), formatDDM(point), LatLngToMaidenhead(point.Lat, point.Lng), code)}
}

// runDist reports the distance and both headings between two locations, which
// is the three numbers a navigator needs to plot a course and a return.
func (c *commandContext) runDist() []string {
	from, to, ok := c.splitLocationPair(c.Args)
	if !ok {
		return []string{"Usage: " + distUsage, locationNotationHelp}
	}
	meters := HaversineDistance(from, to)
	bearing := InitialBearing(from, to)
	back := InitialBearing(to, from)
	return []string{fmt.Sprintf("%v | Heading %03.0f° (%v) | Return %03.0f° (%v)",
		FormatDistance(meters), bearing, CompassPoint(bearing), back, CompassPoint(back))}
}

// runProj projects a waypoint from an origin, a course, and a distance: the
// dead-reckoning question a search team asks when a party reports where it
// started and which way it walked.
func (c *commandContext) runProj() []string {
	origin, bearing, distance, ok := c.splitProjArgs(c.Args)
	if !ok {
		return []string{"Usage: " + projUsage, projDistanceHelp}
	}
	target := ProjectWaypoint(origin, bearing, distance)
	code, err := EncodeOLC(target.Lat, target.Lng, olcCodeLength)
	if err != nil {
		return []string{"proj: could not build a Plus Code: " + err.Error()}
	}
	return []string{fmt.Sprintf("Target: %v | %v | Grid: %v (dist: %.2f km, bearing: %v)",
		code, formatDD(target), LatLngToMaidenhead(target.Lat, target.Lng),
		distance/1000, FormatBearing(bearing))}
}

// runSun reports the day's almanac: the light a party has left, and the moon it
// will have after dark. Every time is UTC, and the answer says so, because a
// mesh node cannot know the reader's time zone.
func (c *commandContext) runSun() []string {
	point, day, ok := c.splitSunArgs(c.Args)
	if !ok {
		return []string{"Usage: " + sunUsage, locationNotationHelp}
	}
	almanac := SolarAlmanac(point.Lat, point.Lng, day)
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		return []string{"sun: could not build a Plus Code: " + err.Error()}
	}

	lines := []string{fmt.Sprintf("Almanac for %v at %v (%v):",
		day.Format("2006-01-02"), formatDD(point), code)}
	switch {
	case almanac.PolarNight:
		lines = append(lines, "Sun: polar night — the sun does not rise; Day: 0h00m (UTC)")
	case almanac.MidnightSun:
		lines = append(lines, "Sun: midnight sun — the sun does not set; Day: 24h00m (UTC)")
	default:
		lines = append(lines, fmt.Sprintf("Dawn: %v | Sunrise: %v | Noon: %v | Sunset: %v | Dusk: %v | Day: %v (UTC)",
			FormatUTCClock(almanac.Dawn), FormatUTCClock(almanac.Sunrise), FormatUTCClock(almanac.Noon),
			FormatUTCClock(almanac.Sunset), FormatUTCClock(almanac.Dusk),
			FormatDayLength(almanac.DayLength())))
		if almanac.TwilightAllNight {
			lines = append(lines, "the sun never reaches civil twilight at this latitude today: there is no true dark")
		}
	}
	moon := MoonPhaseAt(day.Add(12 * time.Hour))
	lines = append(lines, fmt.Sprintf("Moon: %v (%.0f%% illuminated)", moon.Name, moon.Illumination))
	return lines
}

// splitLocationPair splits "from to to" into its two locations. A location may
// itself contain spaces, so the split is found by trying each boundary and
// taking the first one where both halves parse. An explicit " to " separator is
// tried first, because it is unambiguous.
func (c *commandContext) splitLocationPair(args string) (LatLng, LatLng, bool) {
	text := strings.TrimSpace(args)
	if text == "" {
		return LatLng{}, LatLng{}, false
	}
	for _, separator := range []string{" to ", " | ", ";"} {
		from, to, found := strings.Cut(text, separator)
		if !found {
			continue
		}
		first, err := ParseLocation(from)
		if err != nil {
			continue
		}
		second, err := ParseLocation(to)
		if err != nil {
			continue
		}
		return first, second, true
	}
	for _, index := range splitBoundaries(text) {
		first, err := ParseLocation(text[:index])
		if err != nil {
			continue
		}
		second, err := ParseLocation(text[index:])
		if err != nil {
			continue
		}
		return first, second, true
	}
	return LatLng{}, LatLng{}, false
}

// splitProjArgs splits a proj command line into its origin, bearing, and
// distance. The bearing and distance are the last two whitespace-separated
// words, because the origin is the part that may contain spaces.
func (c *commandContext) splitProjArgs(args string) (LatLng, float64, float64, bool) {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) < 3 {
		return LatLng{}, 0, 0, false
	}
	distanceText := fields[len(fields)-1]
	bearingText := fields[len(fields)-2]
	originText := strings.Join(fields[:len(fields)-2], " ")

	origin, err := ParseLocation(originText)
	if err != nil {
		return LatLng{}, 0, 0, false
	}
	bearing, err := ParseBearing(bearingText)
	if err != nil {
		return LatLng{}, 0, 0, false
	}
	distance, err := ParseDistance(distanceText)
	if err != nil {
		return LatLng{}, 0, 0, false
	}
	return origin, bearing, distance, true
}

// splitSunArgs splits a sun command line into its location and its optional
// date. The date is at the end, and it may itself be several words ("June 21,
// 2026"), so each boundary from the right is tried until one parses both ways.
func (c *commandContext) splitSunArgs(args string) (LatLng, time.Time, bool) {
	text := strings.TrimSpace(args)
	if text == "" {
		return LatLng{}, time.Time{}, false
	}
	now := c.now()
	// The whole line may be a location with no date.
	if point, err := ParseLocation(text); err == nil {
		return point, utcMidnight(now), true
	}
	boundaries := splitBoundaries(text)
	for _, index := range slices.Backward(boundaries) {
		point, err := ParseLocation(text[:index])
		if err != nil {
			continue
		}
		day, err := ParseAlmanacDay(text[index:], now)
		if err != nil {
			continue
		}
		return point, day, true
	}
	return LatLng{}, time.Time{}, false
}

// splitBoundaries returns the byte offsets inside text at which a whitespace
// separator runs, so a caller can try every place a command line could have
// been split in two.
func splitBoundaries(text string) []int {
	var out []int
	for i := 0; i < len(text); i++ {
		if text[i] != ' ' {
			continue
		}
		// Only the first space of a run is a useful boundary.
		if i > 0 && text[i-1] == ' ' {
			continue
		}
		out = append(out, i)
	}
	return out
}
