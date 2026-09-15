// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the solar and lunar almanac: when the sun rises and sets,
// when civil twilight begins and ends, how much daylight there is, and what the
// moon is doing. It is pure arithmetic on the standard NOAA solar-position
// formulas, so it needs no ephemeris file, no network, and no clock beyond the
// date it is asked about.
//
// The value of this in the field is planning and safety: how many hours of
// light remain decides whether a party can reach shelter, and the moon's phase
// decides whether a night movement is possible at all. Every time this engine
// reports is UTC, because a mesh has no way to know the reader's time zone and
// a stated zone is the only honest one.

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Solar geometry constants.
const (
	// sunriseZenith is the official sunrise/sunset zenith: the sun's center is
	// 50 arc minutes below the horizon, which accounts for atmospheric
	// refraction and the sun's own radius.
	sunriseZenith = 90.0 + 50.0/60.0
	// civilZenith is the civil twilight zenith: the sun is six degrees below
	// the horizon, the point at which outdoor activity needs artificial light.
	civilZenith = 96.0
	// jdUnixEpoch is the Julian day number of the Unix epoch.
	jdUnixEpoch = 2440587.5
	// jdJ2000 is the Julian day number of the J2000.0 epoch.
	jdJ2000 = 2451545.0
	// daysPerCentury is how many days a Julian century holds.
	daysPerCentury = 36525.0
	// minutesPerDay is how many minutes a solar day holds.
	minutesPerDay = 720.0
	// minutesPerDegree is how many minutes of time one degree of longitude is
	// worth: four, exactly.
	minutesPerDegree = 4.0
)

// Lunar constants.
const (
	// synodicMonth is the mean length of the lunar synodic month in days.
	synodicMonth = 29.530588853
	// newMoonEpochJD is the Julian day number of a known new moon,
	// 2000-01-06 18:14 UTC, from which the phase is counted forward.
	newMoonEpochJD = 2451550.1
	// lunarPhaseCount is how many phases the report names.
	lunarPhaseCount = 8
)

// MoonPhaseNames are the eight phases this engine names, in order from new
// moon.
var MoonPhaseNames = []string{
	"New Moon", "Waxing Crescent", "First Quarter", "Waxing Gibbous",
	"Full Moon", "Waning Gibbous", "Last Quarter", "Waning Crescent",
}

// SolarDay is one day's almanac for one location. The times are absolute, so a
// sunset that falls on the next UTC day is still later than the sunrise and
// subtracting the two gives the true day length. A zero time means the event
// does not occur that day.
type SolarDay struct {
	// Day is the UTC date the almanac is for.
	Day time.Time
	// Dawn and Dusk are civil twilight, when the sun is six degrees below the
	// horizon.
	Dawn time.Time
	Dusk time.Time
	// Sunrise, Noon, and Sunset bound the daylight.
	Sunrise time.Time
	Noon    time.Time
	Sunset  time.Time
	// MidnightSun reports that the sun never sets: there is no sunrise or
	// sunset to report, and the day is all daylight.
	MidnightSun bool
	// PolarNight reports that the sun never rises: there is no daylight at all.
	PolarNight bool
	// TwilightAllNight reports that the sun never reaches six degrees below the
	// horizon, so there is no dawn or dusk: the sky stays brighter than civil
	// twilight through the night.
	TwilightAllNight bool
}

// DayLength is how long the sun is above the horizon. A day with no sunrise or
// sunset reports a full day under the midnight sun and none during polar night.
func (d SolarDay) DayLength() time.Duration {
	switch {
	case d.MidnightSun:
		return 24 * time.Hour
	case d.PolarNight:
		return 0
	case d.Sunrise.IsZero() || d.Sunset.IsZero():
		return 0
	default:
		return d.Sunset.Sub(d.Sunrise)
	}
}

// SolarAlmanac computes the almanac for the UTC date of day at the given
// coordinate. Every time it returns is in UTC.
func SolarAlmanac(lat, lng float64, day time.Time) SolarDay {
	midnight := utcMidnight(day)
	declination, equationOfTime := solarPosition(julianDay(midnight))
	noonMinutes := minutesPerDay - minutesPerDegree*normalizeLongitude(lng) - equationOfTime

	result := SolarDay{Day: midnight, Noon: solarMinutesToTime(midnight, noonMinutes)}
	if hours, status := solarHourAngle(lat, declination, sunriseZenith); status == 0 {
		result.Sunrise = solarMinutesToTime(midnight, noonMinutes-minutesPerDegree*hours)
		result.Sunset = solarMinutesToTime(midnight, noonMinutes+minutesPerDegree*hours)
	} else if status < 0 {
		result.MidnightSun = true
	} else {
		result.PolarNight = true
	}
	if hours, status := solarHourAngle(lat, declination, civilZenith); status == 0 {
		result.Dawn = solarMinutesToTime(midnight, noonMinutes-minutesPerDegree*hours)
		result.Dusk = solarMinutesToTime(midnight, noonMinutes+minutesPerDegree*hours)
	} else {
		result.TwilightAllNight = true
	}
	return result
}

// solarPosition returns the sun's declination in degrees and the equation of
// time in minutes for a Julian day, using the NOAA solar-position formulas.
// These are the same closed-form expressions the published sunrise/sunset
// tables are computed from, and they are good to well under a minute for any
// date this bot will ever be asked about.
func solarPosition(jd float64) (declination, equationOfTime float64) {
	t := (jd - jdJ2000) / daysPerCentury

	// Geometric mean longitude and mean anomaly of the sun, in degrees.
	meanLongitude := math.Mod(280.46646+t*(36000.76983+t*0.0003032), 360)
	meanAnomaly := 357.52911 + t*(35999.05029-0.0001537*t)
	// Eccentricity of the Earth's orbit.
	eccentricity := 0.016708634 - t*(0.000042037+0.0000001267*t)

	// Equation of the center.
	anomalyRad := radians(meanAnomaly)
	center := math.Sin(anomalyRad)*(1.914602-t*(0.004817+0.000014*t)) +
		math.Sin(2*anomalyRad)*(0.019993-0.000101*t) +
		math.Sin(3*anomalyRad)*0.000289

	// True and apparent longitude, and the corrected obliquity of the
	// ecliptic.
	omega := 125.04 - 1934.136*t
	apparentLongitude := meanLongitude + center - 0.00569 - 0.00478*math.Sin(radians(omega))
	meanObliquity := 23 + (26+(21.448-t*(46.815+t*(0.00059-t*0.001813)))/60)/60
	obliquity := meanObliquity + 0.00256*math.Cos(radians(omega))

	declination = degrees(math.Asin(math.Sin(radians(obliquity)) * math.Sin(radians(apparentLongitude))))

	// Equation of time, in minutes of clock time.
	y := math.Tan(radians(obliquity / 2))
	y *= y
	longitudeRad := radians(meanLongitude)
	equationOfTime = minutesPerDegree * degrees(
		y*math.Sin(2*longitudeRad)-
			2*eccentricity*math.Sin(anomalyRad)+
			4*eccentricity*y*math.Sin(anomalyRad)*math.Cos(2*longitudeRad)-
			0.5*y*y*math.Sin(4*longitudeRad)-
			1.25*eccentricity*eccentricity*math.Sin(2*anomalyRad))
	return declination, equationOfTime
}

// solarHourAngle returns the hour angle in degrees at which the sun crosses the
// given zenith, and a status: zero when the crossing happens, -1 when the sun
// never descends to that zenith (it stays above), and 1 when it never ascends
// to it (it stays below). The status is what distinguishes a midnight sun from
// a polar night.
func solarHourAngle(lat, declination, zenith float64) (float64, int) {
	cosHourAngle := (math.Cos(radians(zenith)) - math.Sin(radians(lat))*math.Sin(radians(declination))) /
		(math.Cos(radians(lat)) * math.Cos(radians(declination)))
	switch {
	case cosHourAngle > 1:
		return 0, 1
	case cosHourAngle < -1:
		return 0, -1
	default:
		return degrees(math.Acos(cosHourAngle)), 0
	}
}

// solarMinutesToTime converts minutes past the given UTC midnight into a time.
// The value may be negative or beyond a day, which is exactly how an event that
// falls on the neighbouring UTC day is represented without losing the ordering.
func solarMinutesToTime(midnight time.Time, minutes float64) time.Time {
	return midnight.Add(time.Duration(minutes * float64(time.Minute)))
}

// julianDay returns the Julian day number of a time.
func julianDay(t time.Time) float64 {
	return float64(t.UTC().UnixNano())/float64(24*time.Hour) + jdUnixEpoch
}

// utcMidnight returns the UTC midnight at the start of the day t falls on.
func utcMidnight(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

// MoonPhase is what the moon is doing at one moment.
type MoonPhase struct {
	// Name is the phase name, one of MoonPhaseNames.
	Name string
	// Index is the phase's position in MoonPhaseNames: 0 is new moon, 4 is
	// full moon.
	Index int
	// Age is how many days have passed since the last new moon.
	Age float64
	// Illumination is the illuminated fraction of the disc, as a percentage
	// from 0 to 100.
	Illumination float64
}

// MoonPhaseAt reports the moon's phase at a moment, counted from a known new
// moon forward through the mean synodic month. The mean month is accurate to
// about a day and a half, which is well inside the eight named phases the
// report distinguishes; the illumination is derived from the same age, so the
// two never contradict each other.
func MoonPhaseAt(t time.Time) MoonPhase {
	age := math.Mod(julianDay(t)-newMoonEpochJD, synodicMonth)
	if age < 0 {
		age += synodicMonth
	}
	fraction := age / synodicMonth
	index := int(math.Floor(fraction*lunarPhaseCount+0.5)) % lunarPhaseCount
	if index < 0 {
		index += lunarPhaseCount
	}
	return MoonPhase{
		Name:         MoonPhaseNames[index],
		Index:        index,
		Age:          age,
		Illumination: (1 - math.Cos(2*math.Pi*fraction)) / 2 * 100,
	}
}

// ParseAlmanacDay parses the optional date a sun command names. An empty date
// is today; today, tomorrow, and yesterday are accepted by name; and an
// absolute date may be written in any of the common shapes.
func ParseAlmanacDay(text string, now time.Time) (time.Time, error) {
	trimmed := strings.TrimSpace(text)
	switch strings.ToLower(trimmed) {
	case "", "today":
		return utcMidnight(now), nil
	case "tomorrow":
		return utcMidnight(now).AddDate(0, 0, 1), nil
	case "yesterday":
		return utcMidnight(now).AddDate(0, 0, -1), nil
	}
	layouts := []string{
		"2006-01-02", "2006/01/02", "02/01/2006",
		"2 Jan 2006", "02 Jan 2006", "Jan 2 2006", "Jan 2, 2006",
		"2 January 2006", "January 2 2006", "January 2, 2006",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return utcMidnight(parsed), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q: use YYYY-MM-DD, or today, tomorrow, or yesterday", text)
}

// FormatUTCClock renders a UTC instant as a wall-clock time, or a placeholder
// when the event does not occur.
func FormatUTCClock(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("15:04")
}

// FormatDayLength renders a duration of daylight as hours and minutes.
func FormatDayLength(d time.Duration) string {
	if d <= 0 {
		return "0h00m"
	}
	total := int(d.Round(time.Minute).Minutes())
	return fmt.Sprintf("%vh%02vm", total/60, total%60)
}
