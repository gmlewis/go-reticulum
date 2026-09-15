// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the advanced lunar almanac: the Moon's position, when it
// rises, transits and sets, how much light it will give during the coming
// night, and when the next four principal phases fall.
//
// The astronomy is Meeus's *Astronomical Algorithms*: the abridged ELP series
// of chapter 47 for the Moon's geocentric longitude, latitude and distance
// (the periodic terms live in moon-tables.go), and the mean obliquity of the
// ecliptic of chapter 22. The position is evaluated in Terrestrial Time, so a
// polynomial for Delta T bridges the clock the bot reads from the clock the
// series is defined on.
//
// Why a bot on a mesh needs this: night movement, search grids, and light
// discipline all hinge on whether the Moon will be up and how bright it will
// be, and the answer has to come from the node itself. Nothing here reaches the
// network. A new moon and a full moon also drive the spring tides a coastal
// party plans around, which is why the command names them.
//
// Accuracy is the field standard rather than the almanac standard: the position
// is within a few arcminutes, which puts a rise or set within a couple of
// minutes, and a phase within a minute or two. Delta T is a closed-form
// approximation, which is the largest single error term here.

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Lunar constants.
const (
	// moonEarthRadiusKm is the Earth's equatorial radius, used for the
	// equatorial horizontal parallax.
	moonEarthRadiusKm = 6378.14
	// moonRiseZenithOffset is the standard rise/set altitude of the Moon's
	// center: refraction and the Moon's own radius are folded into the parallax
	// term by this factor.
	moonRiseZenithOffset = 0.7275
	// moonRefractionDeg is the standard refraction allowance at the horizon.
	moonRefractionDeg = 0.5667
	// moonUsage is the usage line for the command.
	moonUsage = "moon [place|pluscode|coords] [date]"
	// moonEventScanStep is how finely the rise/set search samples the day. One
	// minute is far finer than the Moon's altitude can cross the horizon in,
	// and the bisection then refines the crossing to well under a second.
	moonEventScanStep = time.Minute
	// moonPhaseScanStep is how finely the phase search samples the month. The
	// elongation moves about 12.19 degrees a day, so six hours is three
	// degrees: a comfortably safe bracket for the bisection.
	moonPhaseScanStep = 6 * time.Hour
	// moonPhaseScanDays is how far the phase search will look before giving up.
	// One synodic month plus a margin always contains every phase.
	moonPhaseScanDays = 40
)

// The four principal lunar phases, as the elongation from the Sun.
const (
	moonPhaseNewElongation   = 0.0
	moonPhaseFirstElongation = 90.0
	moonPhaseFullElongation  = 180.0
	moonPhaseLastElongation  = 270.0
)

// MoonPosition is the Moon's geocentric place at one instant.
type MoonPosition struct {
	// Longitude is the geocentric ecliptic longitude in degrees, referred to
	// the mean equinox of the date.
	Longitude float64
	// Latitude is the geocentric ecliptic latitude in degrees.
	Latitude float64
	// DistanceKm is the distance from the Earth's center in kilometers.
	DistanceKm float64
	// ParallaxDeg is the equatorial horizontal parallax in degrees.
	ParallaxDeg float64
	// RightAscension and Declination are the equatorial coordinates in degrees,
	// referred to the mean equinox of the date.
	RightAscension float64
	Declination    float64
}

// MoonDay is one day's lunar almanac for one place. Every time is UTC, and a
// zero time means the event does not fall inside that UTC day.
type MoonDay struct {
	// Day is the UTC date the almanac is for.
	Day time.Time
	// Rise, Transit, and Set are the events inside the UTC day. Transit is the
	// upper transit, when the Moon crosses the observer's meridian.
	Rise    time.Time
	Transit time.Time
	Set     time.Time
	// HasRise, HasTransit, and HasSet report which of them occur. The Moon can
	// be above or below the horizon for a whole UTC day, and a high-latitude
	// observer can have a day with no rise and no set at all.
	HasRise    bool
	HasTransit bool
	HasSet     bool
}

// MoonPhase is what the Moon is doing at one moment.
type MoonPhase struct {
	// Name is the phase name, one of MoonPhaseNames.
	Name string
	// Index is the phase's position in MoonPhaseNames: 0 is new moon, 4 is
	// full moon.
	Index int
	// Age is the phase angle expressed in mean synodic days: how long ago the
	// last new moon would have been if the Moon moved at its mean rate.
	Age float64
	// Illumination is the illuminated fraction of the disc, as a percentage
	// from 0 to 100.
	Illumination float64
}

// MoonPhaseEvent is one of the next principal phases.
type MoonPhaseEvent struct {
	// Name is the phase name, one of the "Quarter" or "Moon" names.
	Name string
	// At is when the phase occurs, in UTC.
	At time.Time
	// SpringTide reports that this phase is a new or full moon, which is when
	// the Sun and Moon pull together and the tidal range is greatest.
	SpringTide bool
}

// moonPositionAt computes the Moon's geocentric place for a Terrestrial Time
// Julian day.
func moonPositionAt(jdTT float64) MoonPosition {
	t := (jdTT - jdJ2000) / daysPerCentury

	// The fundamental arguments (Meeus 47.1-47.5), in degrees.
	meanLongitude := 218.3164477 + 481267.88123421*t - 0.0015786*t*t +
		t*t*t/538841 - t*t*t*t/65194000
	elongation := 297.8501921 + 445267.1114034*t - 0.0018819*t*t +
		t*t*t/545868 - t*t*t*t/113065000
	sunAnomaly := 357.5291092 + 35999.0502909*t - 0.0001536*t*t + t*t*t/24490000
	moonAnomaly := 134.9633964 + 477198.8675055*t + 0.0087414*t*t +
		t*t*t/69699 - t*t*t*t/14712000
	argumentOfLatitude := 93.2720950 + 483202.0175233*t - 0.0036539*t*t -
		t*t*t/3526000 + t*t*t*t/863310000
	a1 := 119.75 + 131.849*t
	a2 := 53.09 + 479264.290*t
	a3 := 313.45 + 481266.484*t
	// The eccentricity factor multiplies every term that uses the Sun's mean
	// anomaly, once for each power of it in the argument.
	eccentricity := 1 - 0.002516*t - 0.0000074*t*t

	var sumL, sumR, sumB float64
	for _, term := range moonLRTerms {
		argument := radians(float64(term.D)*elongation + float64(term.M)*sunAnomaly +
			float64(term.Mp)*moonAnomaly + float64(term.F)*argumentOfLatitude)
		factor := math.Pow(eccentricity, math.Abs(float64(term.M)))
		sumL += term.L * factor * math.Sin(argument)
		sumR += term.R * factor * math.Cos(argument)
	}
	for _, term := range moonBTerms {
		argument := radians(float64(term.D)*elongation + float64(term.M)*sunAnomaly +
			float64(term.Mp)*moonAnomaly + float64(term.F)*argumentOfLatitude)
		factor := math.Pow(eccentricity, math.Abs(float64(term.M)))
		sumB += term.B * factor * math.Sin(argument)
	}

	// The additive terms of Meeus 47.6-47.7 that the tables cannot carry:
	// Venus, Jupiter, and the flattening of the Earth's figure.
	sumL += 3958*math.Sin(radians(a1)) + 1962*math.Sin(radians(meanLongitude-argumentOfLatitude)) +
		318*math.Sin(radians(a2))
	sumB += -2235*math.Sin(radians(meanLongitude)) + 382*math.Sin(radians(a3)) +
		175*math.Sin(radians(a1-argumentOfLatitude)) + 175*math.Sin(radians(a1+argumentOfLatitude)) +
		127*math.Sin(radians(meanLongitude-moonAnomaly)) - 115*math.Sin(radians(meanLongitude+moonAnomaly))

	position := MoonPosition{
		Longitude:  normalizeDegrees(meanLongitude + sumL/1e6),
		Latitude:   sumB / 1e6,
		DistanceKm: 385000.56 + sumR/1000,
	}
	position.ParallaxDeg = degrees(math.Asin(moonEarthRadiusKm / position.DistanceKm))

	// Ecliptic to equatorial, with the mean obliquity of the date (Meeus 22.2
	// and 13.3-13.4).
	obliquity := meanObliquityDegrees(t)
	lambda := radians(position.Longitude)
	beta := radians(position.Latitude)
	epsilon := radians(obliquity)
	position.RightAscension = normalizeDegrees(degrees(math.Atan2(
		math.Sin(lambda)*math.Cos(epsilon)-math.Tan(beta)*math.Sin(epsilon),
		math.Cos(lambda))))
	position.Declination = degrees(math.Asin(
		math.Sin(beta)*math.Cos(epsilon) + math.Cos(beta)*math.Sin(epsilon)*math.Sin(lambda)))
	return position
}

// meanObliquityDegrees returns the mean obliquity of the ecliptic for a number
// of Julian centuries from J2000.0 (Meeus 22.2).
func meanObliquityDegrees(t float64) float64 {
	return 23 + (26+(21.448-t*(46.8150+t*(0.00059-t*0.001813)))/60)/60
}

// deltaTSeconds approximates the difference between Terrestrial Time and UT for
// a calendar year. It is the Espenak-Meeus polynomial, which is good to a few
// seconds across the decades this bot is used in.
func deltaTSeconds(year float64) float64 {
	switch {
	case year >= 2005 && year <= 2050:
		u := year - 2000
		return 62.92 + 0.32217*u + 0.005589*u*u
	case year > 2050 && year <= 2150:
		return -20 + 32*math.Pow((year-1820)/100, 2) - 0.5628*(2150-year)
	default:
		u := (year - 1820) / 100
		return -20 + 32*u*u
	}
}

// deltaTDays returns the TT-UT offset in days for the instant, using the UTC
// calendar year for the year-dependent polynomial.
func deltaTDays(t time.Time) float64 {
	utc := t.UTC()
	year := float64(utc.Year()) + float64(utc.YearDay())/365.25
	return deltaTSeconds(year) / 86400
}

// moonPositionAtUTC computes the Moon's place for a UTC instant.
func moonPositionAtUTC(t time.Time) MoonPosition {
	return moonPositionAt(julianDay(t) + deltaTDays(t))
}

// moonRiseAltitudeDeg is the geocentric altitude the Moon's center has at the
// moment of apparent rising or setting: refraction and the Moon's own radius,
// less the parallax that lifts it for an observer on the surface.
func moonRiseAltitudeDeg(parallaxDeg float64) float64 {
	return moonRiseZenithOffset*parallaxDeg - moonRefractionDeg
}

// moonAltitudeDeg returns the Moon's geocentric altitude above the horizon at a
// UTC instant for an observer at the given latitude and longitude.
func moonAltitudeDeg(at time.Time, lat, lng float64) float64 {
	position := moonPositionAtUTC(at)
	hourAngle := normalizeDegrees(localSiderealDegrees(at, lng) - position.RightAscension)
	phi := radians(lat)
	delta := radians(position.Declination)
	h := radians(hourAngle)
	return degrees(math.Asin(math.Sin(phi)*math.Sin(delta) +
		math.Cos(phi)*math.Cos(delta)*math.Cos(h)))
}

// moonCrossingValue is the quantity whose sign the rise/set search looks at: the
// Moon's geocentric altitude less the altitude it has at rising.
func moonCrossingValue(at time.Time, lat, lng float64) float64 {
	return moonCrossingValueAt(moonPositionAtUTC(at), at, lat, lng)
}

// moonCrossingValueAt is moonCrossingValue with the position already in hand,
// so one evaluation serves both the rise search and the transit search.
func moonCrossingValueAt(position MoonPosition, at time.Time, lat, lng float64) float64 {
	hourAngle := normalizeDegrees(localSiderealDegrees(at, lng) - position.RightAscension)
	phi := radians(lat)
	delta := radians(position.Declination)
	altitude := degrees(math.Asin(math.Sin(phi)*math.Sin(delta) +
		math.Cos(phi)*math.Cos(delta)*math.Cos(radians(hourAngle))))
	return altitude - moonRiseAltitudeDeg(position.ParallaxDeg)
}

// localSiderealDegrees returns the local apparent sidereal time in degrees. The
// mean sidereal time is used: its difference from the apparent value is under
// an arcsecond, which no rise time can show.
func localSiderealDegrees(at time.Time, lng float64) float64 {
	return normalizeDegrees(greenwichSiderealDegrees(at) + lng)
}

// greenwichSiderealDegrees returns the mean sidereal time at Greenwich in
// degrees (Meeus 12.4).
func greenwichSiderealDegrees(at time.Time) float64 {
	jd := julianDay(at)
	t := (jd - jdJ2000) / daysPerCentury
	return normalizeDegrees(280.46061837 + 360.98564736629*(jd-jdJ2000) +
		0.000387933*t*t - t*t*t/38710000)
}

// MoonAlmanac computes the rise, upper transit, and set inside the UTC day of
// day, for an observer at the given latitude and longitude.
func MoonAlmanac(lat, lng float64, day time.Time) MoonDay {
	start := utcMidnight(day)
	result := MoonDay{Day: start}
	end := start.AddDate(0, 0, 1)

	previous := start
	previousPosition := moonPositionAtUTC(previous)
	previousValue := moonCrossingValueAt(previousPosition, previous, lat, lng)
	previousHourAngle := moonHourAngleAt(previousPosition, previous, lng)
	for at := start.Add(moonEventScanStep); !at.After(end); at = at.Add(moonEventScanStep) {
		position := moonPositionAtUTC(at)
		value := moonCrossingValueAt(position, at, lat, lng)
		hourAngle := moonHourAngleAt(position, at, lng)
		switch {
		case previousValue <= 0 && value > 0:
			result.Rise = refineMoonCrossing(previous, at, func(t time.Time) float64 {
				return moonCrossingValue(t, lat, lng)
			})
			result.HasRise = true
		case previousValue >= 0 && value < 0:
			result.Set = refineMoonCrossing(previous, at, func(t time.Time) float64 {
				return moonCrossingValue(t, lat, lng)
			})
			result.HasSet = true
		}
		if previousHourAngle < 0 && hourAngle >= 0 {
			result.Transit = refineMoonCrossing(previous, at, func(t time.Time) float64 {
				return moonHourAngle(t, lng)
			})
			result.HasTransit = true
		}
		previous, previousValue, previousHourAngle = at, value, hourAngle
	}
	return result
}

// moonHourAngle returns the Moon's local hour angle in degrees, normalized to
// -180..180. It crosses zero at the upper transit.
func moonHourAngle(at time.Time, lng float64) float64 {
	return moonHourAngleAt(moonPositionAtUTC(at), at, lng)
}

// moonHourAngleAt is moonHourAngle with the position already in hand.
func moonHourAngleAt(position MoonPosition, at time.Time, lng float64) float64 {
	return normalizeDegrees(localSiderealDegrees(at, lng)-position.RightAscension+180) - 180
}

// refineMoonCrossing bisects the interval [lo, hi] for the instant where value
// crosses zero, for either an increasing or a decreasing crossing.
func refineMoonCrossing(lo, hi time.Time, value func(time.Time) float64) time.Time {
	loValue := value(lo)
	for range 60 {
		mid := lo.Add(hi.Sub(lo) / 2)
		midValue := value(mid)
		// The root is in the second half when the midpoint has the same sign as
		// the low end, whichever way the function is going.
		if (loValue < 0) == (midValue < 0) {
			lo, loValue = mid, midValue
			continue
		}
		hi = mid
	}
	return lo.Add(hi.Sub(lo) / 2).UTC()
}

// MoonElongationDeg returns the Moon's elongation from the Sun in degrees, from
// 0 at new moon through 180 at full moon and back. It is the phase angle the
// illumination and the phase name are derived from.
func MoonElongationDeg(at time.Time) float64 {
	return moonElongationAt(julianDay(at) + deltaTDays(at))
}

// moonElongationAt computes the elongation for a Terrestrial Time Julian day.
func moonElongationAt(jdTT float64) float64 {
	moon := moonPositionAt(jdTT)
	_, _, sunLongitude := solarPosition(jdTT)
	return normalizeDegrees(moon.Longitude - sunLongitude)
}

// MoonPhaseAt reports the Moon's phase at an instant. The age is the phase
// angle expressed in mean synodic days, so the illumination, the name, and the
// age are all readings of one quantity and can never contradict each other.
func MoonPhaseAt(t time.Time) MoonPhase {
	elongation := MoonElongationDeg(t)
	fraction := elongation / 360
	index := int(math.Floor(fraction*lunarPhaseCount+0.5)) % lunarPhaseCount
	if index < 0 {
		index += lunarPhaseCount
	}
	return MoonPhase{
		Name:         MoonPhaseNames[index],
		Index:        index,
		Age:          fraction * synodicMonth,
		Illumination: (1 - math.Cos(radians(elongation))) / 2 * 100,
	}
}

// MoonPhaseTimes returns the next occurrence of each of the four principal
// phases after the given instant, soonest first.
func MoonPhaseTimes(after time.Time) []MoonPhaseEvent {
	targets := []struct {
		name       string
		elongation float64
		spring     bool
	}{
		{"New Moon", moonPhaseNewElongation, true},
		{"First Quarter", moonPhaseFirstElongation, false},
		{"Full Moon", moonPhaseFullElongation, true},
		{"Last Quarter", moonPhaseLastElongation, false},
	}
	events := make([]MoonPhaseEvent, 0, len(targets))
	for _, target := range targets {
		at, ok := nextMoonElongation(after, target.elongation)
		if !ok {
			continue
		}
		events = append(events, MoonPhaseEvent{Name: target.name, At: at, SpringTide: target.spring})
	}
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].At.Before(events[j-1].At); j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
	return events
}

// nextMoonElongation finds the next instant after which the Moon's elongation
// passes through the target value.
func nextMoonElongation(after time.Time, target float64) (time.Time, bool) {
	// The signed distance to the target, so a crossing is a sign change.
	distance := func(at time.Time) float64 {
		return normalizeDegrees(MoonElongationDeg(at)-target+180) - 180
	}
	previous := after
	previousDistance := distance(previous)
	limit := after.AddDate(0, 0, moonPhaseScanDays)
	for at := after.Add(moonPhaseScanStep); !at.After(limit); at = at.Add(moonPhaseScanStep) {
		current := distance(at)
		if previousDistance <= 0 && current > 0 {
			return refineMoonCrossing(previous, at, distance), true
		}
		previous, previousDistance = at, current
	}
	return time.Time{}, false
}

// NightLight is how much light the Moon gives during the coming night.
type NightLight struct {
	// Rating is the field assessment: Dark Night, Moderate Light, or Bright
	// Moonlight.
	Rating string
	// MoonUp reports that the Moon is above the horizon at the middle of the
	// night.
	MoonUp bool
	// AltitudeDeg is the Moon's altitude at the middle of the night.
	AltitudeDeg float64
	// WindowStart and WindowEnd are the night the assessment covers.
	WindowStart time.Time
	WindowEnd   time.Time
}

// Night illumination ratings, from the field vocabulary.
const (
	nightRatingDark     = "Dark Night"
	nightRatingModerate = "Moderate Light"
	nightRatingBright   = "Bright Moonlight"
)

// NightIllumination assesses how much light the Moon gives over the night that
// follows the given UTC day at a place. The night runs from dusk to the next
// day's dawn when the Sun reaches civil twilight; at a latitude with no civil
// darkness the assessment falls back to the six hours around local solar
// midnight, and says nothing it cannot support.
func NightIllumination(lat, lng float64, day time.Time) NightLight {
	start := utcMidnight(day)
	windowStart, windowEnd := nightWindow(lat, lng, start)
	result := NightLight{WindowStart: windowStart, WindowEnd: windowEnd}

	middle := windowStart.Add(windowEnd.Sub(windowStart) / 2)
	result.AltitudeDeg = moonAltitudeDeg(middle, lat, lng)
	result.MoonUp = result.AltitudeDeg > 0

	switch {
	case !result.MoonUp:
		// A Moon below the horizon lights nothing, whatever its phase.
		result.Rating = nightRatingDark
	default:
		illumination := MoonPhaseAt(middle).Illumination
		switch {
		case illumination < 25:
			result.Rating = nightRatingDark
		case illumination <= 65:
			result.Rating = nightRatingModerate
		default:
			result.Rating = nightRatingBright
		}
	}
	return result
}

// nightWindow returns the darkness that follows a UTC day: civil dusk to the
// next day's civil dawn. With no civil darkness — a high-latitude summer — it
// returns the six hours centered on local solar midnight instead.
func nightWindow(lat, lng float64, day time.Time) (time.Time, time.Time) {
	dusk := SolarAlmanac(lat, lng, day).Dusk
	dawn := SolarAlmanac(lat, lng, day.AddDate(0, 0, 1)).Dawn
	if !dusk.IsZero() && !dawn.IsZero() && dawn.After(dusk) {
		return dusk, dawn
	}
	// Local solar midnight is twelve hours after solar noon.
	noon := SolarAlmanac(lat, lng, day).Noon
	middle := noon.Add(12 * time.Hour)
	return middle.Add(-3 * time.Hour), middle.Add(3 * time.Hour)
}

// FormatMoonClock renders an event time as a UTC wall clock, or a dash when the
// event does not occur in the day.
func FormatMoonClock(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("15:04") + " UTC"
}

// FormatMoonPhaseMoment renders a phase instant as the month and day it falls
// on, which is how a party plans a night or a tide.
func FormatMoonPhaseMoment(t time.Time) string {
	return t.UTC().Format("Jan 2")
}

// runMoon reports the lunar almanac: the phase, and — when a place is known —
// when the Moon rises, transits and sets, and how bright the coming night will
// be. With no place it answers the phase and the next quarters, which are the
// same everywhere on Earth.
func (c *commandContext) runMoon() []string {
	args := strings.TrimSpace(c.Args)
	now := c.now()
	var (
		point    LatLng
		day      time.Time
		hasPlace bool
	)
	switch {
	case args == "":
		day = utcMidnight(now)
	default:
		parsed, date, ok := c.splitSunArgs(args)
		switch {
		case ok:
			point, day, hasPlace = parsed, date, true
		default:
			// With no location, a bare date still answers: the phase and the
			// next quarters are the same everywhere on Earth.
			date, err := ParseAlmanacDay(args, now)
			if err != nil {
				return []string{"Usage: " + moonUsage, locationNotationHelp}
			}
			day = date
		}
	}

	phase := MoonPhaseAt(day.Add(12 * time.Hour))
	first := fmt.Sprintf("Moon: %v (%.0f%% illumination, age %.1fd)", phase.Name, phase.Illumination,
		phase.Age)
	lines := []string{first}

	if hasPlace {
		almanac := MoonAlmanac(point.Lat, point.Lng, day)
		first += " | Transit: " + FormatMoonClock(almanac.Transit)
		lines[0] = first
		light := NightIllumination(point.Lat, point.Lng, day)
		lines = append(lines, fmt.Sprintf("Moonrise: %v | Moonset: %v | Night Illumination: %v",
			FormatMoonClock(almanac.Rise), FormatMoonClock(almanac.Set), light.Rating))
	}
	lines = append(lines, moonNextLine(MoonPhaseTimes(day.Add(12*time.Hour))))
	return lines
}

// moonNextLine renders the next four principal phases in one line, naming the
// spring-tide ones because that is what a coastal plan turns on.
func moonNextLine(events []MoonPhaseEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		part := fmt.Sprintf("%v %v", event.Name, FormatMoonPhaseMoment(event.At))
		if event.SpringTide {
			part += " (Spring Tides)"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "Next: unknown"
	}
	return "Next: " + strings.Join(parts, " | ")
}
