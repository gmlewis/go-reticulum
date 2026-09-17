// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"bytes"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// The reference sentences below carry the checksum the NMEA XOR rule really
// produces. The specification's own example sentences are frequently quoted
// with a stale checksum — the two the Phase 0 brief quotes compute to *62 and
// *43, not to the *76 and *42 they were written with — so the positive cases
// use the corrected sentences and the brief's literal text is asserted to be
// rejected, which is exactly what a byte-for-byte validator must do.
const (
	testRMCSentence = "$GNRMC,204533.00,A,3745.31926,N,12227.16314,W,0.02,142.3,160926,,,A*62"
	testGGASentence = "$GNGGA,204533.00,3745.31926,N,12227.16314,W,1,09,0.8,142.4,M,-31.2,M,,*43"
)

// nmeaChecksum computes the NMEA XOR checksum of a sentence body, which is the
// test's own independent implementation of the rule the parser must apply.
func nmeaChecksum(t *testing.T, body string) string {
	t.Helper()
	var sum byte
	for i := range len(body) {
		sum ^= body[i]
	}
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[sum>>4], digits[sum&0x0f]})
}

// sentence builds one well-formed sentence from a body that has no "$" or
// checksum, so a test can state only the fields it cares about.
func sentence(t *testing.T, body string) string {
	t.Helper()
	return "$" + body + "*" + nmeaChecksum(t, body)
}

// fixFromLine parses one line through a reader and returns the resulting fix.
func fixFromLine(t *testing.T, line string) GPSFix {
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

// TestGPSChecksumRuleMatchesTheSpecification pins the XOR rule against the
// canonical sentences the NMEA specification itself publishes.
func TestGPSChecksumRuleMatchesTheSpecification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"gga", "GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,46.9,M,,", "47"},
		{"rmc", "GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W", "6A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := nmeaChecksum(t, tc.body); got != tc.want {
				t.Errorf("checksum of %q = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestGPSParsesRMC asserts every field a Recommended Minimum sentence carries
// lands in the fix, with south and west becoming negative.
func TestGPSParsesRMC(t *testing.T) {
	t.Parallel()

	fix := fixFromLine(t, testRMCSentence)
	if !fix.Valid {
		t.Fatal("Valid = false for an RMC status of A")
	}
	if math.Abs(fix.Lat-37.755321) > 1e-5 {
		t.Errorf("Lat = %v, want 37.755321", fix.Lat)
	}
	if math.Abs(fix.Lng-(-122.452719)) > 1e-5 {
		t.Errorf("Lng = %v, want -122.452719", fix.Lng)
	}
	if fix.SpeedKnots != 0.02 {
		t.Errorf("SpeedKnots = %v, want 0.02", fix.SpeedKnots)
	}
	if fix.CourseDeg != 142.3 {
		t.Errorf("CourseDeg = %v, want 142.3", fix.CourseDeg)
	}
	want := time.Date(2026, time.September, 16, 20, 45, 33, 0, time.UTC)
	if !fix.TimeUTC.Equal(want) {
		t.Errorf("TimeUTC = %v, want %v", fix.TimeUTC.UTC(), want)
	}
}

// TestGPSParsesGGA asserts the fix-quality sentence fills in altitude,
// satellites, HDOP, quality, and the geoidal separation.
func TestGPSParsesGGA(t *testing.T) {
	t.Parallel()

	fix := fixFromLine(t, testGGASentence)
	if !fix.Valid {
		t.Fatal("Valid = false for a GGA fix quality of 1")
	}
	if fix.AltitudeM != 142.4 {
		t.Errorf("AltitudeM = %v, want 142.4", fix.AltitudeM)
	}
	if fix.GeoidSepM != -31.2 {
		t.Errorf("GeoidSepM = %v, want -31.2", fix.GeoidSepM)
	}
	if fix.Satellites != 9 {
		t.Errorf("Satellites = %v, want 9", fix.Satellites)
	}
	if fix.HDOP != 0.8 {
		t.Errorf("HDOP = %v, want 0.8", fix.HDOP)
	}
	if fix.FixQuality != 1 {
		t.Errorf("FixQuality = %v, want 1", fix.FixQuality)
	}
	if math.Abs(fix.Lat-37.755321) > 1e-5 || math.Abs(fix.Lng-(-122.452719)) > 1e-5 {
		t.Errorf("position = %v,%v, want 37.755321,-122.452719", fix.Lat, fix.Lng)
	}
	if !fix.HasAltitude {
		t.Error("HasAltitude = false after a GGA that carried an altitude")
	}
}

// TestGPSWithoutAnAltitudeSaysSo asserts an altitude is only claimed when a
// sentence actually carried one, so a position with no measured height is never
// reported as sea level.
func TestGPSWithoutAnAltitudeSaysSo(t *testing.T) {
	t.Parallel()

	fix := fixFromLine(t, testRMCSentence)
	if fix.HasAltitude {
		t.Error("HasAltitude = true for an RMC, which carries no altitude")
	}
	if fix.AltitudeM != 0 {
		t.Errorf("AltitudeM = %v, want 0", fix.AltitudeM)
	}
}

// TestGPSRejectsStaleChecksums is the negative half of the checksum contract: a
// sentence whose final two characters do not match the XOR of its body is
// rejected, including the two the Phase 0 brief quotes.
func TestGPSRejectsStaleChecksums(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
	}{
		{"rmc quoted with a stale checksum", "$GNRMC,204533.00,A,3745.31926,N,12227.16314,W,0.02,142.3,160926,,,A*76"},
		{"gga quoted with a stale checksum", "$GNGGA,204533.00,3745.31926,N,12227.16314,W,1,09,0.8,142.4,M,-31.2,M,,*42"},
		{"one digit changed", strings.TrimSuffix(testRMCSentence, "62") + "63"},
		{"checksum truncated", strings.TrimSuffix(testRMCSentence, "2")},
		{"checksum not hexadecimal", strings.TrimSuffix(testRMCSentence, "62") + "ZZ"},
		{"no checksum at all", testRMCSentence[:strings.Index(testRMCSentence, "*")]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := NewGPSReader(strings.NewReader(tc.line + "\n"))
			if err := reader.Run(t.Context()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if fix := reader.LastFix(); fix.Valid {
				t.Errorf("line %q produced Valid = true", tc.line)
			}
		})
	}
}

// TestGPSIgnoresMalformedAndUnknownSentences asserts the parser is a stream
// filter: garbage, unknown talkers, and unsupported sentence types change
// nothing and are not fatal.
func TestGPSIgnoresMalformedAndUnknownSentences(t *testing.T) {
	t.Parallel()

	lines := []string{
		"",
		"   ",
		"not a sentence",
		"$",
		"$*00",
		"$GNRMC*",
		"$$GNRMC,1,2*7F",
		"$GNTXT,01,01,02,hello*11",
		"$GNGSA,A,3,04,05,,09,12,,,24,,,,,2.5,1.3,2.1*39",
		"$GPVTG,054.7,T,034.4,M,005.5,N,010.2,K*48",
	}
	reader := NewGPSReader(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fix := reader.LastFix(); fix.Valid {
		t.Errorf("garbage produced Valid = true: %+v", fix)
	}
}

// TestGPSStreamingMergesSentences asserts a reader fed a real stream reports the
// merged state of the last RMC and the last GGA, which is what a receiver
// actually emits once a second.
func TestGPSStreamingMergesSentences(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		"$GNTXT,01,01,02,u-blox ag - www.u-blox.com*4E",
		testGGASentence,
		testRMCSentence,
		"$GPGSA,A,3,04,05,,09,12,,,24,,,,,2.5,1.3,2.1*39",
	}, "\r\n") + "\r\n"

	reader := NewGPSReader(bytes.NewBufferString(stream))
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fix := reader.LastFix()
	if !fix.Valid {
		t.Fatal("Valid = false after a valid RMC and GGA")
	}
	if fix.AltitudeM != 142.4 {
		t.Errorf("AltitudeM = %v, want the GGA's 142.4", fix.AltitudeM)
	}
	if fix.Satellites != 9 {
		t.Errorf("Satellites = %v, want the GGA's 9", fix.Satellites)
	}
	if fix.SpeedKnots != 0.02 || fix.CourseDeg != 142.3 {
		t.Errorf("speed/course = %v/%v, want the RMC's 0.02/142.3", fix.SpeedKnots, fix.CourseDeg)
	}
}

// TestGPSVoidRMCReportsNoFix asserts a void RMC turns the fix invalid without
// discarding the last known position, which is what a receiver in a canyon
// emits between reacquisitions.
func TestGPSVoidRMCReportsNoFix(t *testing.T) {
	t.Parallel()

	valid := fixFromLine(t, testRMCSentence)
	if !valid.Valid {
		t.Fatal("the reference RMC must be valid")
	}
	void := sentence(t, "GNRMC,204534.00,V,3745.31926,N,12227.16314,W,0.02,142.3,160926,,,N")
	fix := fixFromLine(t, void)
	if fix.Valid {
		t.Error("Valid = true for an RMC status of V")
	}
	if math.Abs(fix.Lat-37.755321) > 1e-5 {
		t.Errorf("Lat = %v, want the last known position kept", fix.Lat)
	}
}

// TestGPSZeroQualityGGAReportsNoFix asserts a GGA with no fix quality is
// invalid even though it parses.
func TestGPSZeroQualityGGAReportsNoFix(t *testing.T) {
	t.Parallel()

	line := sentence(t, "GNGGA,204533.00,3745.31926,N,12227.16314,W,0,03,4.2,,,,,,,")
	fix := fixFromLine(t, line)
	if fix.Valid {
		t.Error("Valid = true for a GGA fix quality of 0")
	}
	if fix.Satellites != 3 {
		t.Errorf("Satellites = %v, want 3", fix.Satellites)
	}
}

// TestGPSPolarHemispheres asserts south and west are negative and north and
// east are positive, at every notation a receiver may emit.
func TestGPSPolarHemispheres(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		line     string
		lat, lng float64
	}{
		{"south-west", sentence(t, "GNRMC,120000.00,A,3352.00000,S,15112.00000,W,0.0,0.0,010100,,,A"), -33.8666667, -151.2},
		{"north-east", sentence(t, "GNRMC,120000.00,A,3352.00000,N,15112.00000,E,0.0,0.0,010100,,,A"), 33.8666667, 151.2},
		{"equator", sentence(t, "GNRMC,120000.00,A,0000.00000,N,00000.00000,E,0.0,0.0,010100,,,A"), 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fix := fixFromLine(t, tc.line)
			if !fix.Valid {
				t.Fatal("Valid = false")
			}
			if math.Abs(fix.Lat-tc.lat) > 1e-6 || math.Abs(fix.Lng-tc.lng) > 1e-6 {
				t.Errorf("position = %v,%v, want %v,%v", fix.Lat, fix.Lng, tc.lat, tc.lng)
			}
		})
	}
}

// TestGPSStaticProvider asserts the injected fix a headless deployment uses is
// what LastFix reports, with no reader behind it.
func TestGPSStaticProvider(t *testing.T) {
	t.Parallel()

	reader := NewGPSReader(nil)
	want := GPSFix{Valid: true, Lat: 37.7553, Lng: -122.4527, AltitudeM: 142.4, Satellites: 9, HDOP: 0.8, FixQuality: 1}
	reader.SetFix(want)
	if got := reader.LastFix(); got != want {
		t.Errorf("LastFix() = %+v, want %+v", got, want)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestGPSReaderIsRaceFree exercises the reader from several goroutines at once,
// which is the property a serial reader and an HTTP dashboard both depend on.
func TestGPSReaderIsRaceFree(t *testing.T) {
	t.Parallel()

	reader := NewGPSReader(strings.NewReader(testRMCSentence + "\n" + testGGASentence + "\n"))
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 200 {
				if i%2 == 0 {
					reader.SetFix(GPSFix{Valid: true, Lat: float64(i), Lng: float64(j)})
					continue
				}
				if fix := reader.LastFix(); fix.Lat < -90 || fix.Lat > 90 {
					t.Errorf("LastFix() returned an out-of-range latitude %v", fix.Lat)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestGPSReaderCloseStopsTheScan asserts a started reader is reaped: Close ends
// the scan goroutine and is safe to call twice.
func TestGPSReaderCloseStopsTheScan(t *testing.T) {
	t.Parallel()

	reader := NewGPSReader(strings.NewReader(testGGASentence + "\n"))
	reader.Start(t.Context())
	deadline := time.Now().Add(2 * time.Second)
	for !reader.LastFix().Valid && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !reader.LastFix().Valid {
		t.Fatal("the scan never reported the fix it was given")
	}
	if err := reader.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestGPSDegreesMinutesConversion pins the ddmm.mmmm conversion, including the
// three-digit longitude degree field.
func TestGPSDegreesMinutesConversion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text string
		want float64
	}{
		{"3745.31926", 37.755321},
		{"12227.16314", 122.452719},
		{"0000.00000", 0},
		{"9000.00000", 90},
		{"18000.00000", 180},
		{"0555.50000", 5.925},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			t.Parallel()
			got, err := parseNMEADegrees(tc.text)
			if err != nil {
				t.Fatalf("parseNMEADegrees(%q): %v", tc.text, err)
			}
			if math.Abs(got-tc.want) > 1e-6 {
				t.Errorf("parseNMEADegrees(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestGPSDegreesRejectsGarbage asserts a malformed coordinate is an error, not a
// silent zero that would beacon the Gulf of Guinea.
func TestGPSDegreesRejectsGarbage(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"", "  ", "abc", "37.7553", "37,45", "3745.31926N"} {
		if got, err := parseNMEADegrees(text); err == nil {
			t.Errorf("parseNMEADegrees(%q) = %v, want an error", text, got)
		}
	}
}
