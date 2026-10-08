// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"math"
	"strings"
	"testing"
	"time"
)

// The golden sentences below were produced by an implementation of the NMEA
// XOR rule in Python, independently of this package's emitter, so a refactor
// that silently changes the wire format fails here rather than in the field.
const (
	goldenRMC         = "$GNRMC,155340.123,A,3507.4074,N,10634.0734,W,0.4,271.3,081026,,,A*6C\r\n"
	goldenGGA         = "$GPGGA,155340.123,3507.4074,N,10634.0734,W,1,08,0.9,1620.5,M,-8.5,M,,*6F\r\n"
	goldenHDM         = "$HCHDM,47.5,M*1F\r\n"
	goldenHDT         = "$HCHDT,47.5,T*1F\r\n"
	goldenRMCNoFix    = "$GNRMC,235959.900,V,,,,,,,081026,,,A*59\r\n"
	goldenRMCMidnight = "$GNRMC,000000.500,A,3507.4074,N,10634.0734,W,0.4,271.3,091026,,,A*6E\r\n"
)

// goldenAt is the instant the golden sentences are stamped with.
var goldenAt = time.Date(2026, time.October, 8, 15, 53, 40, 123_000_000, time.UTC)

// goldenFix is the fix the golden sentences describe.
func goldenFix() GPSFix {
	return GPSFix{
		Valid:       true,
		Lat:         35.123456,
		Lng:         -106.567890,
		AltitudeM:   1620.5,
		HasAltitude: true,
		GeoidSepM:   -8.5,
		SpeedKnots:  0.4,
		CourseDeg:   271.3,
		Satellites:  8,
		HDOP:        0.9,
		FixQuality:  1,
		TimeUTC:     goldenAt,
	}
}

// TestEmitGolden pins the exact bytes of every emitter, so the wire format is
// frozen against an independent computation rather than against the code.
func TestEmitGolden(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"RMC", EmitRMC(goldenFix(), goldenAt), goldenRMC},
		{"GGA", EmitGGA(goldenFix()), goldenGGA},
		{"HDM", EmitHDM(47.5, goldenAt), goldenHDM},
		{"HDT", EmitHDT(47.5, goldenAt), goldenHDT},
		{"RMC no fix", EmitRMC(GPSFix{TimeUTC: time.Date(2026, time.October, 8, 23, 59, 59, 900_000_000, time.UTC)}, time.Time{}), goldenRMCNoFix},
		{"RMC midnight", EmitRMC(goldenFix(), time.Date(2026, time.October, 9, 0, 0, 0, 500_000_000, time.UTC)), goldenRMCMidnight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.got != tc.want {
				t.Fatalf("emitted\n  %q\nwant\n  %q", tc.got, tc.want)
			}
		})
	}
}

// TestEmitRMCParseRoundTrip is the property that makes the whole Android
// design safe: the sentence the emitter writes is a sentence this package's
// own parser reads back as the same fix.
func TestEmitRMCParseRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fix  GPSFix
		at   time.Time
	}{
		{"typical", goldenFix(), goldenAt},
		{"southern western", GPSFix{Valid: true, Lat: -33.8688, Lng: 151.2093, SpeedKnots: 2.5, CourseDeg: 45.0}, time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)},
		{"equator prime meridian", GPSFix{Valid: true, Lat: 0, Lng: 0}, time.Date(2026, time.June, 30, 12, 0, 0, 0, time.UTC)},
		{"northern eastern", GPSFix{Valid: true, Lat: 51.5074, Lng: 0.1278, SpeedKnots: 0, CourseDeg: 0}, time.Date(2026, time.December, 31, 23, 59, 59, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			line := EmitRMC(tc.fix, tc.at)
			if !strings.HasSuffix(line, "\r\n") {
				t.Fatalf("EmitRMC(%+v) = %q, want a CRLF terminator", tc.fix, line)
			}
			if _, err := parseNMEALine(line); err != nil {
				t.Fatalf("parseNMEALine(%q): %v", line, err)
			}
			got := fixFromLine(t, line)
			if !got.Valid {
				t.Fatalf("fixFromLine(%q) reported an invalid fix: %+v", line, got)
			}
			if !closeWithin(got.Lat, tc.fix.Lat, 1e-5) {
				t.Fatalf("latitude = %v, want %v", got.Lat, tc.fix.Lat)
			}
			if !closeWithin(got.Lng, tc.fix.Lng, 1e-5) {
				t.Fatalf("longitude = %v, want %v", got.Lng, tc.fix.Lng)
			}
			want := tc.at.UTC().Truncate(time.Millisecond)
			if !got.TimeUTC.Equal(want) {
				t.Fatalf("timestamp = %v, want %v", got.TimeUTC, want)
			}
		})
	}
}

// TestEmitGGAParseRoundTrip asserts the fix-quality sentence carries a height
// and a quality indicator that survive the parser, and that the position it
// names is the position the recommended-minimum sentence named.
func TestEmitGGAParseRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fix  GPSFix
	}{
		{"typical", goldenFix()},
		{"no altitude", GPSFix{Valid: true, Lat: -12.5, Lng: 130.75, FixQuality: 2, Satellites: 12, HDOP: 0.7}},
		{"no hdop", GPSFix{Valid: true, Lat: 45.0, Lng: -93.25, AltitudeM: -7.25, HasAltitude: true, FixQuality: 4}},
		{"rtk float", GPSFix{Valid: true, Lat: 89.5, Lng: 179.5, AltitudeM: 4123.75, HasAltitude: true, GeoidSepM: 51.25, FixQuality: 5, Satellites: 27, HDOP: 0.5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			line := EmitGGA(tc.fix)
			if _, err := parseNMEALine(line); err != nil {
				t.Fatalf("parseNMEALine(%q): %v", line, err)
			}
			got := ggaFromLine(t, line)
			if !closeWithin(got.Lat, tc.fix.Lat, 1e-5) || !closeWithin(got.Lng, tc.fix.Lng, 1e-5) {
				t.Fatalf("position = (%v,%v), want (%v,%v)", got.Lat, got.Lng, tc.fix.Lat, tc.fix.Lng)
			}
			if got.FixQuality != tc.fix.FixQuality {
				t.Fatalf("fix quality = %v, want %v", got.FixQuality, tc.fix.FixQuality)
			}
			if got.Satellites != tc.fix.Satellites {
				t.Fatalf("satellites = %v, want %v", got.Satellites, tc.fix.Satellites)
			}
			if got.HasAltitude != tc.fix.HasAltitude {
				t.Fatalf("has altitude = %v, want %v", got.HasAltitude, tc.fix.HasAltitude)
			}
			if got.HasAltitude && !closeWithin(got.AltitudeM, tc.fix.AltitudeM, 1e-6) {
				t.Fatalf("altitude = %v, want %v", got.AltitudeM, tc.fix.AltitudeM)
			}
			if tc.fix.HDOP != 0 && !closeWithin(got.HDOP, tc.fix.HDOP, 1e-6) {
				t.Fatalf("hdop = %v, want %v", got.HDOP, tc.fix.HDOP)
			}
			if tc.fix.GeoidSepM != 0 && !closeWithin(got.GeoidSepM, tc.fix.GeoidSepM, 1e-6) {
				t.Fatalf("geoid separation = %v, want %v", got.GeoidSepM, tc.fix.GeoidSepM)
			}
		})
	}
}

// TestEmitHDMParseRoundTrip drives the magnetic sentence through the package's
// own compass reader and requires the heading to arrive unchanged.
func TestEmitHDMParseRoundTrip(t *testing.T) {
	t.Parallel()

	for _, want := range []float64{0, 0.1, 47.5, 90, 180, 270, 359.9} {
		line := EmitHDM(want, goldenAt)
		if _, err := parseNMEALine(line); err != nil {
			t.Fatalf("parseNMEALine(%q): %v", line, err)
		}
		got := compassFromLine(t, line)
		if !got.HasMagnetic {
			t.Fatalf("EmitHDM(%v) produced %q, which the reader did not read as magnetic", want, line)
		}
		if got.HasTrue {
			t.Fatalf("EmitHDM(%v) produced a true heading: %+v", want, got)
		}
		if !closeWithin(got.MagneticDeg, want, 1e-6) {
			t.Fatalf("magnetic heading = %v, want %v", got.MagneticDeg, want)
		}
	}
}

// TestEmitHDTParseRoundTrip does the same for the true-north sentence, which
// needs no declination and so is the one heading a sensorless device can trust.
func TestEmitHDTParseRoundTrip(t *testing.T) {
	t.Parallel()

	for _, want := range []float64{0, 0.1, 47.5, 90, 180, 270, 359.9} {
		line := EmitHDT(want, goldenAt)
		if _, err := parseNMEALine(line); err != nil {
			t.Fatalf("parseNMEALine(%q): %v", line, err)
		}
		got := compassFromLine(t, line)
		if !got.HasTrue {
			t.Fatalf("EmitHDT(%v) produced %q, which the reader did not read as true", want, line)
		}
		if !closeWithin(got.TrueDeg, want, 1e-6) {
			t.Fatalf("true heading = %v, want %v", got.TrueDeg, want)
		}
	}
}

// TestEmitChecksumIsValidXOR asserts the checksum is a real XOR over the body
// and that a sentence corrupted anywhere in its payload is rejected. A wrong
// position is worse than no position, so the emitter must never produce a
// sentence the validator cannot defend.
func TestEmitChecksumIsValidXOR(t *testing.T) {
	t.Parallel()

	line := strings.TrimRight(EmitRMC(goldenFix(), goldenAt), "\r\n")
	star := strings.LastIndexByte(line, '*')
	if star < 0 || len(line)-star-1 != 2 {
		t.Fatalf("EmitRMC produced %q, which carries no two-digit checksum", line)
	}
	want := nmeaChecksum(t, line[1:star])
	if got := line[star+1:]; got != want {
		t.Fatalf("checksum = %q, want %q", got, want)
	}

	// Every single-byte payload corruption must be caught. Flipping one bit
	// changes the XOR by that bit's value, so the stored checksum can never
	// match, and the parser must refuse the sentence rather than merge a
	// position that is one bit away from the truth.
	body := line[1:star]
	accepted := 0
	for i := range len(body) {
		mutated := body[:i] + string(body[i]^0x01) + body[i+1:]
		corrupted := "$" + mutated + "*" + line[star+1:]
		if _, err := parseNMEALine(corrupted); err == nil {
			accepted++
		}
	}
	if accepted != 0 {
		t.Fatalf("%v single-byte corruptions of %q were accepted", accepted, line)
	}
}

// TestEmitQuantizationErrorBound bounds the error the ddmm.mmmm field
// introduces. Four decimal minutes is about 18 cm of latitude, so the
// round-trip has to stay far inside a meter across the whole legal range.
func TestEmitQuantizationErrorBound(t *testing.T) {
	t.Parallel()

	const bound = 1e-5
	cases := []struct {
		name     string
		lat, lng float64
	}{
		{"origin", 0, 0},
		{"north pole", 90, 0},
		{"south pole", -90, 0},
		{"east antimeridian", 0, 180},
		{"west antimeridian", 0, -180},
		{"equator", 0, 12.345678},
		{"sub meter", 35.00000001, -106.00000001},
		{"six digit", 35.123456, -106.567890},
		{"just below a degree", 34.99999999, -105.99999999},
		{"high latitude", 89.99999999, 179.99999999},
		{"low latitude", -89.99999999, -179.99999999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fix := GPSFix{Valid: true, Lat: tc.lat, Lng: tc.lng}
			got := fixFromLine(t, EmitRMC(fix, goldenAt))
			if !got.Valid {
				t.Fatalf("EmitRMC(%+v) did not round-trip as a valid fix", fix)
			}
			if math.Abs(got.Lat-tc.lat) > bound {
				t.Fatalf("latitude %v round-tripped to %v, error %v > %v", tc.lat, got.Lat, math.Abs(got.Lat-tc.lat), bound)
			}
			if math.Abs(got.Lng-tc.lng) > bound {
				t.Fatalf("longitude %v round-tripped to %v, error %v > %v", tc.lng, got.Lng, math.Abs(got.Lng-tc.lng), bound)
			}
		})
	}
}

// TestEmitNegativeAndHemisphere asserts the hemisphere markers carry the sign,
// including the four quadrant corners and both zero-width cases, because a
// dropped 'S' is a position on the wrong side of the planet.
func TestEmitNegativeAndHemisphere(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		lat, lng float64
		wantLat  string
		wantLng  string
	}{
		{"north east", 35.5, 139.75, "N", "E"},
		{"north west", 35.5, -139.75, "N", "W"},
		{"south east", -35.5, 139.75, "S", "E"},
		{"south west", -35.5, -139.75, "S", "W"},
		{"equator east", 0, 10, "N", "E"},
		{"equator west", 0, -10, "N", "W"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			line := EmitRMC(GPSFix{Valid: true, Lat: tc.lat, Lng: tc.lng}, goldenAt)
			sentence, err := parseNMEALine(line)
			if err != nil {
				t.Fatalf("parseNMEALine(%q): %v", line, err)
			}
			if got := sentence.fields[4]; got != tc.wantLat {
				t.Fatalf("latitude hemisphere = %q, want %q in %q", got, tc.wantLat, line)
			}
			if got := sentence.fields[6]; got != tc.wantLng {
				t.Fatalf("longitude hemisphere = %q, want %q in %q", got, tc.wantLng, line)
			}
			got := fixFromLine(t, line)
			if !closeWithin(got.Lat, tc.lat, 1e-5) || !closeWithin(got.Lng, tc.lng, 1e-5) {
				t.Fatalf("position = (%v,%v), want (%v,%v)", got.Lat, got.Lng, tc.lat, tc.lng)
			}
		})
	}
}

// TestEmitNoFix asserts an invalid fix emits a void status with empty
// coordinates, and that the parser reports no position rather than a bogus one
// at the origin. A receiver without a fix does not know where it is.
func TestEmitNoFix(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 8, 23, 59, 59, 900_000_000, time.UTC)
	cases := []struct {
		name string
		line string
	}{
		{"RMC", EmitRMC(GPSFix{TimeUTC: at}, at)},
		{"GGA", EmitGGA(GPSFix{TimeUTC: at})},
		{"RMC with a stale position", EmitRMC(GPSFix{Valid: false, Lat: 35.5, Lng: -106.5, TimeUTC: at}, at)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sentence, err := parseNMEALine(tc.line)
			if err != nil {
				t.Fatalf("parseNMEALine(%q): %v", tc.line, err)
			}
			if sentence.kind == "RMC" && sentence.fields[2] != "V" {
				t.Fatalf("status field = %q in %q, want V", sentence.fields[2], tc.line)
			}
			if sentence.kind == "GGA" && sentence.fields[6] != "0" {
				t.Fatalf("fix quality = %q in %q, want 0", sentence.fields[6], tc.line)
			}
			got := fixFromLine(t, tc.line)
			if got.Valid {
				t.Fatalf("void sentence %q reported a valid fix: %+v", tc.line, got)
			}
			if got.Lat != 0 || got.Lng != 0 {
				t.Fatalf("void sentence %q reported a position (%v,%v)", tc.line, got.Lat, got.Lng)
			}
		})
	}
}

// TestEmitNoFixGoldenText asserts a fix that is marked invalid still emits its
// coordinates as empty fields even when it carries a stale position, because
// the status field is the only thing the reader trusts to reject a sentence.
func TestEmitNoFixGoldenText(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 8, 23, 59, 59, 900_000_000, time.UTC)
	got := EmitRMC(GPSFix{Valid: false, Lat: 35.5, Lng: -106.5, TimeUTC: at}, at)
	if got != goldenRMCNoFix {
		t.Fatalf("EmitRMC(void) = %q, want %q", got, goldenRMCNoFix)
	}
}

// TestEmitDateRollover asserts a timestamp near midnight stamps the date it
// really falls on, so a fix taken at 00:00:00 is not dated to the previous day.
func TestEmitDateRollover(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		at       time.Time
		wantTime string
		wantDate string
	}{
		{"just before midnight", time.Date(2026, time.October, 8, 23, 59, 59, 900_000_000, time.UTC), "235959.900", "081026"},
		{"exactly midnight", time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC), "000000.000", "091026"},
		{"just after midnight", time.Date(2026, time.October, 9, 0, 0, 0, 500_000_000, time.UTC), "000000.500", "091026"},
		{"month end", time.Date(2026, time.December, 31, 23, 59, 59, 0, time.UTC), "235959.000", "311226"},
		{"new year", time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC), "000000.000", "010127"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			line := EmitRMC(goldenFix(), tc.at)
			sentence, err := parseNMEALine(line)
			if err != nil {
				t.Fatalf("parseNMEALine(%q): %v", line, err)
			}
			if got := sentence.fields[1]; got != tc.wantTime {
				t.Fatalf("time field = %q, want %q in %q", got, tc.wantTime, line)
			}
			if got := sentence.fields[9]; got != tc.wantDate {
				t.Fatalf("date field = %q, want %q in %q", got, tc.wantDate, line)
			}
			got := fixFromLine(t, line)
			want := tc.at.UTC().Truncate(time.Millisecond)
			if !got.TimeUTC.Equal(want) {
				t.Fatalf("timestamp = %v, want %v", got.TimeUTC, want)
			}
		})
	}
}

// TestEmitSubSecondTimeTruncates asserts the milliseconds the time field cannot
// hold are dropped rather than rounded, so a stamp a hair before midnight is
// never carried into the next day's date.
func TestEmitSubSecondTimeTruncates(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 8, 23, 59, 59, 999_999_999, time.UTC)
	line := EmitRMC(goldenFix(), at)
	sentence, err := parseNMEALine(line)
	if err != nil {
		t.Fatalf("parseNMEALine(%q): %v", line, err)
	}
	if got := sentence.fields[1]; got != "235959.999" {
		t.Fatalf("time field = %q, want %q", got, "235959.999")
	}
	if got := sentence.fields[9]; got != "081026" {
		t.Fatalf("date field = %q, want %q", got, "081026")
	}
}

// TestEmitHeadingRejectsNonFinite asserts a heading that is not a number is
// emitted as an empty field, which the reader drops, rather than as a
// confidently wrong direction.
func TestEmitHeadingRejectsNonFinite(t *testing.T) {
	t.Parallel()

	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for name, line := range map[string]string{"HDM": EmitHDM(bad, goldenAt), "HDT": EmitHDT(bad, goldenAt)} {
			got := compassFromLine(t, line)
			if got.Valid {
				t.Fatalf("Emit%s(%v) = %q, which the reader accepted as %+v", name, bad, line, got)
			}
		}
	}
}

// TestEmitHeadingWraps asserts a heading outside [0,360) is wrapped rather than
// dropped or passed through, because a compass that reports exactly 360 and one
// that reports -12 both mean a real direction.
func TestEmitHeadingWraps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"full turn", 360, 0},
		{"negative", -12, 348},
		{"over a turn", 372, 12},
		{"two turns", 720, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := compassFromLine(t, EmitHDM(tc.in, goldenAt))
			if !got.HasMagnetic {
				t.Fatalf("EmitHDM(%v) was dropped by the reader", tc.in)
			}
			if !closeWithin(got.MagneticDeg, tc.want, 1e-9) {
				t.Fatalf("magnetic heading = %v, want %v", got.MagneticDeg, tc.want)
			}
		})
	}
}

// FuzzEmitParseRoundTrip asserts the emitter and the parser agree for random
// fixes anywhere on Earth. The seed corpus alone runs in an ordinary `go test`.
func FuzzEmitParseRoundTrip(f *testing.F) {
	f.Add(35.123456, -106.567890, 1620.5, -8.5, 1.0)
	f.Add(0.0, 0.0, 0.0, 0.0, 0.0)
	f.Add(-90.0, 180.0, -400.0, 100.0, 99.9)
	f.Add(89.999999, -179.999999, 8848.0, -51.25, 0.5)

	f.Fuzz(func(t *testing.T, lat, lng, alt, geoid, hdop float64) {
		if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
			t.Skip()
		}
		if math.IsNaN(lng) || math.IsInf(lng, 0) || lng < -180 || lng > 180 {
			t.Skip()
		}
		if math.IsNaN(alt) || math.IsInf(alt, 0) || math.IsNaN(geoid) || math.IsInf(geoid, 0) {
			t.Skip()
		}
		if math.IsNaN(hdop) || math.IsInf(hdop, 0) || hdop < 0 {
			t.Skip()
		}
		fix := GPSFix{
			Valid:       true,
			Lat:         lat,
			Lng:         lng,
			AltitudeM:   alt,
			HasAltitude: true,
			GeoidSepM:   geoid,
			HDOP:        hdop,
			FixQuality:  1,
			Satellites:  5,
		}
		got := fixFromLine(t, EmitRMC(fix, goldenAt))
		if !got.Valid {
			t.Fatalf("EmitRMC(%+v) did not round-trip as valid", fix)
		}
		if math.Abs(got.Lat-lat) > 1e-5 || math.Abs(got.Lng-lng) > 1e-5 {
			t.Fatalf("position (%v,%v) round-tripped to (%v,%v)", lat, lng, got.Lat, got.Lng)
		}
		if !got.TimeUTC.Equal(goldenAt.UTC().Truncate(time.Millisecond)) {
			t.Fatalf("timestamp = %v, want %v", got.TimeUTC, goldenAt)
		}
	})
}

// ggaFromLine parses one fix-quality sentence through a reader and returns the
// resulting fix.
func ggaFromLine(t *testing.T, line string) GPSFix {
	t.Helper()
	reader := NewGPSReader(strings.NewReader(line + "\r\n"))
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("Run(%q): %v", line, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return reader.LastFix()
}

// compassFromLine parses one heading sentence through a compass reader and
// returns the resulting heading.
func compassFromLine(t *testing.T, line string) CompassHeading {
	t.Helper()
	reader := NewCompassReader(strings.NewReader(line + "\r\n"))
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("Run(%q): %v", line, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return reader.LastHeading()
}
