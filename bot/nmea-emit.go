// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the writing half of the NMEA-0183 front end: the sentence
// emitters that turn a validated fix and a heading back into the bytes a
// receiver would have sent.
//
// It exists because a device that knows where it is may have to hand that
// knowledge to another process as a sensor stream — an Android app publishing
// its own GNSS receiver and magnetometer to a bot and a network client that
// live in a different application. The alternative is a second implementation
// of the format on the producing side, which is a second place for the
// checksum, the hemisphere marker, or the minute conversion to be wrong. Here
// the parser in gps.go and compass.go is the oracle: the emitters are written
// against it, and the round-trip property "parse(emit(fix)) equals fix" is a
// unit test rather than a hope.
//
// Two rules the emitters inherit from the parser, and both of them matter:
//
// A sentence that cannot be trusted is not emitted. A heading that is not a
// number, and a coordinate outside its legal range, become empty fields, which
// the parser drops. A wrong position is worse than no position, and an empty
// field is how this format says "unknown".
//
// The precision of every field is stated in the emitter that writes it. The
// position fields carry four decimal minutes, which is about 18 cm; the time
// field carries milliseconds; every other field is written in the shortest
// form that reads back bit-for-bit identical.
package bot

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// NMEA emission constants.
const (
	// nmeaDegreesFractionDigits is how many decimal places the minutes field
	// of a position carries: ddmm.mmmm, the format's own resolution.
	nmeaDegreesFractionDigits = 4
	// nmeaTimeFractionDigits is how many decimal places the time field
	// carries: hhmmss.sss.
	nmeaTimeFractionDigits = 3
	// nmeaEmitDecimalsPerDegree is the multiplier that turns "degrees and
	// decimal minutes" back into the single scaled number the format prints.
	nmeaEmitDecimalsPerDegree = 100
	// nmeaEmitSecondsPerMinute converts a fraction of a minute to seconds.
	nmeaEmitSecondsPerMinute = 60
	// nmeaEmitMinuteDigits is how many whole-minute digits a position carries;
	// it is what makes the field's zero padding right, because a latitude of
	// zero has to be written "0000.0000" and not "0.0000".
	nmeaEmitMinuteDigits = 2
	// nmeaEmitLatitudeDegrees and nmeaEmitLongitudeDegrees are how many whole
	// degree digits each axis carries.
	nmeaEmitLatitudeDegrees  = 2
	nmeaEmitLongitudeDegrees = 3
)

// EmitRMC renders one recommended-minimum position sentence from a fix. The
// returned sentence carries the leading '$', the trailing "*hh" checksum, and
// the CRLF terminator, so it can be written to a sensor stream unchanged.
//
// at is the instant the sentence is stamped with, and the fix's own TimeUTC is
// used when at is the zero time. The time field carries milliseconds and the
// sub-millisecond remainder is truncated rather than rounded, so a stamp a
// fraction of a second before midnight is never dated to the next day.
//
// A fix that is not valid is written as a void sentence: the status field is
// 'V' and every measured field — position, hemispheres, speed, and course —
// is left empty, so the reader cannot mistake a stale position for a current
// one.
//
// The position carries four decimal minutes, which bounds the round-trip error
// at about 18 cm. Speed and course are written in the shortest form that reads
// back exactly.
func EmitRMC(fix GPSFix, at time.Time) string {
	stamp := nmeaStamp(fix.TimeUTC, at)
	fields := []string{
		"GNRMC",
		nmeaEmitTime(stamp),
		"V", "", "", "", "", "", "",
		nmeaEmitDate(stamp),
		"", "",
		"A",
	}
	if fix.Valid {
		fields[2] = "A"
		fields[3], fields[4] = nmeaEmitCoordinate(fix.Lat, nmeaEmitLatitudeDegrees, 90,
			nmeaEmitNorthMarker, nmeaEmitSouthMarker)
		fields[5], fields[6] = nmeaEmitCoordinate(fix.Lng, nmeaEmitLongitudeDegrees, 180,
			nmeaEmitEastMarker, nmeaEmitWestMarker)
		fields[7] = nmeaEmitNumber(fix.SpeedKnots)
		fields[8] = nmeaEmitNumber(fix.CourseDeg)
	}
	return nmeaEmitSentence(fields)
}

// EmitGGA renders one fix-quality sentence from a fix. The timestamp comes from
// the fix itself: a fix-quality sentence has no date field, so the reader dates
// it from the most recent dated sentence, which is the recommended-minimum
// sentence emitted alongside it.
//
// Altitude, HDOP, and the geoidal separation are written only when the fix
// carries them, so a position nobody measured the height of is never reported
// as standing at sea level.
func EmitGGA(fix GPSFix) string {
	fields := []string{
		"GPGGA",
		nmeaEmitTime(fix.TimeUTC),
		"", "", "", "",
		"0", "", "", "",
		"M", "", "M",
		"", "",
	}
	if fix.Valid {
		fields[2], fields[3] = nmeaEmitCoordinate(fix.Lat, nmeaEmitLatitudeDegrees, 90,
			nmeaEmitNorthMarker, nmeaEmitSouthMarker)
		fields[4], fields[5] = nmeaEmitCoordinate(fix.Lng, nmeaEmitLongitudeDegrees, 180,
			nmeaEmitEastMarker, nmeaEmitWestMarker)
		fields[6] = strconv.Itoa(fix.FixQuality)
		if fix.Satellites > 0 {
			fields[7] = fmt.Sprintf("%02d", fix.Satellites)
		}
		if fix.HDOP > 0 {
			fields[8] = nmeaEmitNumber(fix.HDOP)
		}
		if fix.HasAltitude {
			fields[9] = nmeaEmitNumber(fix.AltitudeM)
		}
		if fix.GeoidSepM != 0 {
			fields[11] = nmeaEmitNumber(fix.GeoidSepM)
		}
	}
	return nmeaEmitSentence(fields)
}

// EmitHDM renders one magnetic-heading sentence. The heading is wrapped into
// 0..360, because a compass that reports 360 and one that reports -12 both mean
// a real direction, and a heading that is not a number is written as an empty
// field, which the reader drops.
//
// The heading sentences carry no timestamp field — NMEA-0183 has none for a
// compass — so the timestamp is not encoded here; the reader stamps the
// heading with its own clock as it parses it. The parameter is kept so every
// emitter in this file has one shape.
func EmitHDM(deg float64, _ time.Time) string {
	return nmeaEmitSentence([]string{"HCHDM", nmeaEmitHeading(deg), nmeaEmitMagneticMarker})
}

// EmitHDT renders one true-heading sentence, which is the one heading a device
// with no position can carry honestly: the instrument has already applied the
// magnetic variation.
//
// Like the magnetic sentence it has nowhere to put a timestamp, so the second
// parameter is unused. See EmitHDM.
func EmitHDT(deg float64, _ time.Time) string {
	return nmeaEmitSentence([]string{"HCHDT", nmeaEmitHeading(deg), nmeaEmitTrueMarker})
}

// Hemisphere and frame markers the emitters write.
const (
	nmeaEmitNorthMarker     = "N"
	nmeaEmitSouthMarker     = "S"
	nmeaEmitEastMarker      = "E"
	nmeaEmitWestMarker      = "W"
	nmeaEmitMagneticMarker  = "M"
	nmeaEmitTrueMarker      = "T"
	nmeaEmitSentenceStarter = "$"
	nmeaEmitChecksumMarker  = "*"
	nmeaEmitTerminator      = "\r\n"
)

// nmeaEmitSentence assembles one sentence from its comma-separated fields,
// appending the starter, the XOR checksum, and the CRLF terminator.
func nmeaEmitSentence(fields []string) string {
	body := strings.Join(fields, ",")
	return nmeaEmitSentenceStarter + body + nmeaEmitChecksumMarker +
		fmt.Sprintf("%02X", nmeaXOR(body)) + nmeaEmitTerminator
}

// nmeaStamp picks the instant a sentence is stamped with: the caller's explicit
// at when it supplied one, and the fix's own timestamp otherwise.
func nmeaStamp(fixTime, at time.Time) time.Time {
	if !at.IsZero() {
		return at.UTC()
	}
	return fixTime.UTC()
}

// nmeaEmitTime renders the hhmmss.sss time field of a sentence. A zero time has
// no field, which is how a sentence says it does not know when it was taken.
func nmeaEmitTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	utc := at.UTC()
	return fmt.Sprintf("%02d%02d%02d.%0*d", utc.Hour(), utc.Minute(), utc.Second(),
		nmeaTimeFractionDigits, utc.Nanosecond()/int(time.Millisecond))
}

// nmeaEmitDate renders the ddmmyy date field of a sentence. The two-digit year
// is the only form the format has, and the parser reads it as the 21st century.
func nmeaEmitDate(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	utc := at.UTC()
	return fmt.Sprintf("%02d%02d%02d", utc.Day(), int(utc.Month()), utc.Year()%100)
}

// nmeaEmitCoordinate renders one position field and its hemisphere marker. A
// coordinate that is not a number, or that lies outside its axis's legal range,
// yields two empty fields: the reader then reports no position rather than a
// position that is confidently wrong.
//
// The minutes are rounded to four decimal places and carried into the degrees
// when the rounding reaches a full degree, which is what keeps a latitude a
// hair below the pole from being written as 9000.0000 with a minute field of
// 60.0000, a value the parser refuses.
func nmeaEmitCoordinate(value float64, degreeDigits int, limit float64, positive, negative string) (string, string) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < -limit || value > limit {
		return "", ""
	}
	marker := positive
	if value < 0 {
		marker = negative
		value = -value
	}
	whole := math.Floor(value)
	minutes := (value - whole) * nmeaEmitSecondsPerMinute
	scale := math.Pow(10, nmeaDegreesFractionDigits)
	minutes = math.Round(minutes*scale) / scale
	if minutes >= nmeaEmitSecondsPerMinute {
		whole++
		minutes = 0
	}
	scaled := whole*nmeaEmitDecimalsPerDegree + minutes
	width := degreeDigits + nmeaEmitMinuteDigits + 1 + nmeaDegreesFractionDigits
	return fmt.Sprintf("%0*.*f", width, nmeaDegreesFractionDigits, scaled), marker
}

// nmeaEmitHeading renders a heading field, wrapped into 0..360. A heading that
// is not a number has no field at all.
func nmeaEmitHeading(deg float64) string {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return ""
	}
	return strconv.FormatFloat(normalizeDegrees(deg), 'f', -1, 64)
}

// nmeaEmitNumber renders an optional numeric field in the shortest form that
// reads back exactly. A value that is not a number has no field at all.
func nmeaEmitNumber(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return ""
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// EmitGST renders one position-error sentence from a fix: the receiver's own
// estimate of how far its position may be from the truth, in meters.
//
// It is the only sentence in the format that carries an error in meters, and it is
// what lets a reader answer "how much should I trust this distance" instead of
// guessing. The horizontal error is written as equal standard deviations on both
// axes, whose combination is exactly the accuracy the fix carried, so the
// round-trip is a property rather than an approximation.
//
// A fix nobody measured the error of writes the axis errors as empty fields, which
// the reader drops: an accuracy of zero meters is the worst possible lie.
func EmitGST(fix GPSFix) string {
	fields := []string{
		"GNGST",
		nmeaEmitTime(fix.TimeUTC),
		"", "", "", "",
		"", "", "",
	}
	if fix.HasAccuracy {
		axisError := nmeaEmitNumber(fix.AccuracyM / math.Sqrt2)
		fields[2] = nmeaEmitNumber(fix.AccuracyM)
		fields[3] = nmeaEmitNumber(fix.AccuracyM)
		fields[4] = nmeaEmitNumber(fix.AccuracyM)
		fields[5] = "0"
		fields[6] = axisError
		fields[7] = axisError
	}
	return nmeaEmitSentence(fields)
}
