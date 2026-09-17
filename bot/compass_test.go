// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"bytes"
	"context"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

// The compass sentences the tests work from. Every checksum is the real XOR of
// the sentence body, so a parser that skips validation cannot pass by
// accident: the Phase 0.6 brief prints *2C and *24 for the first two, but those
// are not the checksums of the sentences beside them, and a heading parser that
// accepted them would accept a corrupted sentence too.
const (
	hdgEastSentence = "$HCHDG,101.1,,,13.1,E*1B"
	hdgWestSentence = "$HCHDG,101.1,,,13.1,W*09"
	hdgDevSentence  = "$HCHDG,101.1,1.5,W,13.1,E*66"
	hdmSentence     = "$HCHDM,101.1,M*28"
	hdmBareSentence = "$HCHDM,101.1*49"
	hdtSentence     = "$HCHDT,114.2,T*2F"
)

// compassFixture builds a static reader holding one heading.
func compassFixture(t *testing.T, heading CompassHeading) *CompassReader {
	t.Helper()
	reader := NewCompassReader(nil)
	reader.SetHeading(heading)
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

// TestCompassParsesHeadingSentences asserts the three heading sentences the
// subsystem supports decode into the right frame: HDG carries both a magnetic
// heading and the variation that turns it true, HDM carries magnetic only, and
// HDT carries true only.
func TestCompassParsesHeadingSentences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		sentence    string
		wantValid   bool
		wantMagnet  bool
		wantTrue    bool
		wantMagDeg  float64
		wantTrueDeg float64
		wantVarDeg  float64
		wantHasVar  bool
	}{
		{"heading deviation variation east", hdgEastSentence,
			true, true, true, 101.1, 114.2, 13.1, true},
		{"heading deviation variation west", hdgWestSentence,
			true, true, true, 101.1, 88.0, -13.1, true},
		{"heading with a deviation field", hdgDevSentence,
			true, true, true, 101.1, 114.2, 13.1, true},
		{"magnetic heading", hdmSentence, true, true, false, 101.1, 0, 0, false},
		{"magnetic heading without an indicator", hdmBareSentence,
			true, true, false, 101.1, 0, 0, false},
		{"true heading", hdtSentence, true, false, true, 0, 114.2, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := NewCompassReader(strings.NewReader(tc.sentence))
			if err := reader.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := reader.LastHeading()
			if got.Valid != tc.wantValid {
				t.Errorf("Valid = %v, want %v", got.Valid, tc.wantValid)
			}
			if got.HasMagnetic != tc.wantMagnet || got.HasTrue != tc.wantTrue {
				t.Errorf("HasMagnetic/HasTrue = %v/%v, want %v/%v",
					got.HasMagnetic, got.HasTrue, tc.wantMagnet, tc.wantTrue)
			}
			if math.Abs(got.MagneticDeg-tc.wantMagDeg) > 1e-9 {
				t.Errorf("MagneticDeg = %v, want %v", got.MagneticDeg, tc.wantMagDeg)
			}
			if math.Abs(got.TrueDeg-tc.wantTrueDeg) > 1e-9 {
				t.Errorf("TrueDeg = %v, want %v", got.TrueDeg, tc.wantTrueDeg)
			}
			if got.HasDeclination != tc.wantHasVar {
				t.Errorf("HasDeclination = %v, want %v", got.HasDeclination, tc.wantHasVar)
			}
			if math.Abs(got.DeclinationDeg-tc.wantVarDeg) > 1e-9 {
				t.Errorf("DeclinationDeg = %v, want %v", got.DeclinationDeg, tc.wantVarDeg)
			}
		})
	}
	// The brief's example sentence turns 101.1 magnetic into 114.2 true with
	// 13.1 degrees of east variation, which is the arithmetic the whole
	// subsystem rests on.
	if got := 101.1 + 13.1; math.Abs(got-114.2) > 1e-9 {
		t.Errorf("101.1 + 13.1 = %v, want 114.2", got)
	}
}

// TestCompassDropsUnusableSentences asserts a corrupted, truncated, or
// nonsensical sentence is dropped rather than believed: a wrong heading is a
// wrong direction, which on a ridge in a storm is worse than no direction.
func TestCompassDropsUnusableSentences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		sentence string
	}{
		{"checksum mismatch", "$HCHDG,101.1,,,13.1,E*FF"},
		{"checksum missing", "$HCHDG,101.1,,,13.1,E"},
		{"no leading dollar", "HCHDG,101.1,,,13.1,E*1B"},
		{"heading beyond a full turn", "$HCHDM,361.5,M*28"},
		{"negative heading", "$HCHDM,-5.0,M*01"},
		{"heading is not a number", "$HCHDM,abc,M*67"},
		{"magnetic sentence labelled true", "$HCHDM,101.1,T*31"},
		{"true sentence labelled magnetic", "$HCHDT,114.2,M*36"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := NewCompassReader(strings.NewReader(tc.sentence))
			if err := reader.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := reader.LastHeading(); got.Valid {
				t.Errorf("%q produced a valid heading %+v, want it dropped", tc.sentence, got)
			}
		})
	}
}

// TestCompassKeepsTheHeadingWhenTheVariationIsUnusable asserts the one
// deliberate leniency in the parser: a sentence whose variation field is empty
// or malformed still yields its magnetic heading, with no true heading, rather
// than being thrown away. The heading field passed its own validation, and the
// declination engine fills the missing correction in from the model anyway, so
// discarding a good heading over a bad variation field would lose information
// the operator needs.
func TestCompassKeepsTheHeadingWhenTheVariationIsUnusable(t *testing.T) {
	t.Parallel()

	for _, sentence := range []string{
		"$HCHDG,101.1,,,13.1,*5E",
		"$HCHDG,101.1,,,13.1,X*06",
	} {
		reader := NewCompassReader(strings.NewReader(sentence))
		if err := reader.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		got := reader.LastHeading()
		if !got.Valid || !got.HasMagnetic || math.Abs(got.MagneticDeg-101.1) > 1e-9 {
			t.Errorf("%q = %+v, want the magnetic heading kept", sentence, got)
		}
		if got.HasDeclination || got.HasTrue {
			t.Errorf("%q = %+v, want no variation inferred from a bad field", sentence, got)
		}
	}
}

// TestCompassAcceptsTheFullTurn asserts a receiver that reports exactly 360
// degrees is read as zero rather than dropped, because the two mean the same
// direction.
func TestCompassAcceptsTheFullTurn(t *testing.T) {
	t.Parallel()

	reader := NewCompassReader(strings.NewReader("$HCHDM,360.0,M*2C"))
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := reader.LastHeading()
	if !got.Valid || got.MagneticDeg != 0 {
		t.Errorf("a 360-degree heading = %+v, want a valid zero", got)
	}
}

// TestCompassCardinalSectors asserts the sixteen-point rose maps every angle to
// the right sector, including the exact boundaries the brief pins.
func TestCompassCardinalSectors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		deg  float64
		want string
	}{
		{0, "N"}, {11.24, "N"}, {11.25, "NNE"}, {33.74, "NNE"}, {33.75, "NE"},
		{56.24, "NE"}, {56.25, "ENE"}, {78.74, "ENE"}, {78.75, "E"},
		{101.24, "E"}, {101.25, "ESE"}, {123.74, "ESE"}, {123.75, "SE"},
		{146.24, "SE"}, {146.25, "SSE"}, {168.74, "SSE"}, {168.75, "S"},
		{191.24, "S"}, {191.25, "SSW"}, {213.74, "SSW"}, {213.75, "SW"},
		{236.24, "SW"}, {236.25, "WSW"}, {258.74, "WSW"}, {258.75, "W"},
		{281.24, "W"}, {281.25, "WNW"}, {303.74, "WNW"}, {303.75, "NW"},
		{326.24, "NW"}, {326.25, "NNW"}, {348.74, "NNW"}, {348.75, "N"},
		{359.9, "N"}, {360, "N"},
	}
	for _, tc := range cases {
		if got := CardinalDirection(tc.deg); got != tc.want {
			t.Errorf("CardinalDirection(%v) = %v, want %v", tc.deg, got, tc.want)
		}
	}
}

// TestCompassCardinalIsSetFromTheBestHeading asserts the sector names the true
// heading when one is known and the magnetic heading otherwise, so the label
// and the number beside it never disagree.
func TestCompassCardinalIsSetFromTheBestHeading(t *testing.T) {
	t.Parallel()

	trueFirst := compassFixture(t, CompassHeading{
		Valid: true, HasMagnetic: true, MagneticDeg: 350,
		HasTrue: true, TrueDeg: 42, HasDeclination: true, DeclinationDeg: 52,
	})
	if got := trueFirst.LastHeading().Cardinal; got != "NE" {
		t.Errorf("cardinal of a true heading of 42° = %v, want NE", got)
	}
	magneticOnly := compassFixture(t, CompassHeading{
		Valid: true, HasMagnetic: true, MagneticDeg: 180,
	})
	if got := magneticOnly.LastHeading().Cardinal; got != "S" {
		t.Errorf("cardinal of a magnetic heading of 180° = %v, want S", got)
	}
}

// TestCompassReaderStreamsASequence asserts the reader keeps the most recent
// heading from a stream, the way a serial compass that repeats sentences
// forever is read.
func TestCompassReaderStreamsASequence(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		hdmSentence,
		hdgEastSentence,
		"$GPRMC,120000.00,A,3745.3189,N,12227.1631,W,0.0,0.0,160926,,,A*6E",
		hdtSentence,
	}, "\r\n")
	reader := NewCompassReader(bytes.NewBufferString(stream))
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := reader.LastHeading()
	if !got.HasTrue || math.Abs(got.TrueDeg-114.2) > 1e-9 {
		t.Errorf("after the stream the heading = %+v, want the last true heading 114.2", got)
	}
	if got.Cardinal != "ESE" {
		t.Errorf("cardinal = %v, want ESE", got.Cardinal)
	}
}

// TestCompassReaderStartAndClose assert the background scan really runs, the
// reader is idempotently closable, and no goroutine is left behind: Close waits
// for the scan it started.
func TestCompassReaderStartAndClose(t *testing.T) {
	t.Parallel()

	reader := NewCompassReader(strings.NewReader(hdgEastSentence))
	reader.Start(context.Background())
	if err := reader.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if got := reader.LastHeading(); !got.Valid {
		t.Errorf("the heading after a completed scan = %+v, want the parsed sentence", got)
	}
}

// TestCompassReaderDropsReadsAfterClose asserts a scan that fails because the
// source was closed is an ordinary shutdown, not an error: a device being
// released must not be reported as a fault.
func TestCompassReaderDropsReadsAfterClose(t *testing.T) {
	t.Parallel()

	reader := NewCompassReader(io.NopCloser(strings.NewReader("")))
	reader.Start(context.Background())
	if err := reader.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := reader.Run(context.Background()); err != nil {
		t.Errorf("Run after Close: %v, want an ordinary end of stream", err)
	}
}

// TestCompassReaderHonorsContextCancellation asserts a canceled scan stops
// rather than draining a device that emits forever.
func TestCompassReaderHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := NewCompassReader(strings.NewReader(hdgEastSentence + "\n" + hdmSentence))
	if err := reader.Run(ctx); err != nil {
		t.Errorf("Run with a canceled context: %v, want nil", err)
	}
	if got := reader.LastHeading(); got.Valid {
		t.Errorf("a canceled scan produced %+v, want no heading", got)
	}
}

// TestCompassReaderConvertsMagneticToTrueFromPosition asserts the integration
// the subsystem exists for: a magnetic-only sentence plus a known position
// yields a true heading, corrected with the World Magnetic Model at that
// position.
func TestCompassReaderConvertsMagneticToTrueFromPosition(t *testing.T) {
	t.Parallel()

	reader := NewCompassReader(strings.NewReader(hdmSentence))
	reader.SetLocationSource(func() (GPSFix, bool) {
		return GPSFix{Valid: true, Lat: 37.7553, Lng: -122.4527}, true
	})
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := reader.LastHeading()
	if !got.HasDeclination || !got.HasTrue {
		t.Fatalf("heading = %+v, want a declination and a true heading", got)
	}
	// San Francisco's variation is a little under thirteen degrees east, so a
	// magnetic 101.1 becomes a true heading near 114.
	if got.DeclinationDeg < 12.5 || got.DeclinationDeg > 13.5 {
		t.Errorf("declination = %v, want about +13 east", got.DeclinationDeg)
	}
	if want := normalizeDegrees(101.1 + got.DeclinationDeg); math.Abs(got.TrueDeg-want) > 1e-9 {
		t.Errorf("TrueDeg = %v, want %v", got.TrueDeg, want)
	}
	if got.Cardinal != "ESE" {
		t.Errorf("cardinal = %v, want ESE", got.Cardinal)
	}
}

// TestCompassReaderLeavesMagneticAloneWithoutAPosition asserts the reader never
// invents a correction: with no fix to evaluate the model at, a magnetic
// heading stays magnetic and says so.
func TestCompassReaderLeavesMagneticAloneWithoutAPosition(t *testing.T) {
	t.Parallel()

	reader := NewCompassReader(strings.NewReader(hdmSentence))
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := reader.LastHeading()
	if got.HasTrue || got.HasDeclination {
		t.Errorf("heading = %+v, want it to stay magnetic", got)
	}
	if got.Cardinal != "E" {
		t.Errorf("cardinal = %v, want the magnetic sector E (101.1° is just inside it)", got.Cardinal)
	}

	noFix := NewCompassReader(strings.NewReader(hdmSentence))
	noFix.SetLocationSource(func() (GPSFix, bool) { return GPSFix{}, false })
	if err := noFix.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := noFix.LastHeading(); got.HasDeclination {
		t.Errorf("an unlocked receiver produced a declination: %+v", got)
	}
}

// TestCompassReaderPrefersTheSentencesOwnVariation asserts a sentence that
// carries its own variation is believed over the model: the instrument
// measured the field where it stands.
func TestCompassReaderPrefersTheSentencesOwnVariation(t *testing.T) {
	t.Parallel()

	reader := NewCompassReader(strings.NewReader(hdgEastSentence))
	reader.SetLocationSource(func() (GPSFix, bool) {
		return GPSFix{Valid: true, Lat: 37.7553, Lng: -122.4527}, true
	})
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := reader.LastHeading()
	if math.Abs(got.DeclinationDeg-13.1) > 1e-9 || math.Abs(got.TrueDeg-114.2) > 1e-9 {
		t.Errorf("heading = %+v, want the sentence's own variation", got)
	}
}

// TestCompassReaderPinnedDeclination asserts the reader consults the declination
// engine with the device's own position and the reading's own time.
func TestCompassReaderPinnedDeclination(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, time.September, 16, 20, 45, 33, 0, time.UTC)
	reader := NewCompassReader(nil)
	at := when
	reader.now = func() time.Time { return at }
	reader.declination = func(lat, lng float64, at time.Time) float64 {
		if math.Abs(lat-37.7553) > 1e-9 || math.Abs(lng-(-122.4527)) > 1e-9 {
			t.Errorf("declination asked for (%v,%v), want the fix's own position", lat, lng)
		}
		if !at.Equal(when) {
			t.Errorf("declination asked for %v, want the reading's own time %v", at, when)
		}
		return 10.0
	}
	reader.SetLocationSource(func() (GPSFix, bool) {
		return GPSFix{Valid: true, Lat: 37.7553, Lng: -122.4527}, true
	})
	reader.SetHeading(CompassHeading{Valid: true, HasMagnetic: true, MagneticDeg: 350})
	got := reader.LastHeading()
	if math.Abs(got.TrueDeg) > 1e-9 && math.Abs(got.TrueDeg-360) > 1e-9 {
		t.Errorf("TrueDeg = %v, want 350 + 10 wrapped to 0", got.TrueDeg)
	}
	if got.DeclinationDeg != 10.0 {
		t.Errorf("DeclinationDeg = %v, want 10", got.DeclinationDeg)
	}
}

// TestOpenCompassFollowsTheConfiguration asserts the initializer reads the two
// Phase 0.6 keys: a device opens, a static heading is held, and neither means
// no compass at all and no overhead.
func TestOpenCompassFollowsTheConfiguration(t *testing.T) {
	t.Parallel()

	if reader, err := openCompass(nil); reader != nil || err != nil {
		t.Errorf("openCompass(nil) = %v, %v, want nothing", reader, err)
	}
	if reader, err := openCompass(&BotConfig{}); reader != nil || err != nil {
		t.Errorf("openCompass with no compass key = %v, %v, want nothing", reader, err)
	}

	missing := &BotConfig{CompassPort: "/tmp/gorrcbot-no-such-compass-device"}
	if reader, err := openCompass(missing); err == nil || reader != nil {
		t.Errorf("openCompass with an unopenable device = %v, %v, want an error", reader, err)
	}

	cases := []struct {
		heading string
		want    float64
	}{
		{"042", 42},
		{"NE", 45},
		{"359.9", 359.9},
	}
	for _, tc := range cases {
		reader, err := openCompass(&BotConfig{CompassHeading: tc.heading})
		if err != nil {
			t.Fatalf("openCompass(%q): %v", tc.heading, err)
		}
		if reader == nil {
			t.Fatalf("openCompass(%q) = nil, want a static reader", tc.heading)
		}
		got := reader.LastHeading()
		if !got.Valid || !got.HasMagnetic || math.Abs(got.MagneticDeg-tc.want) > 1e-9 {
			t.Errorf("static heading %q = %+v, want magnetic %v", tc.heading, got, tc.want)
		}
		_ = reader.Close()
	}

	if reader, err := openCompass(&BotConfig{CompassHeading: "not-a-bearing"}); err == nil || reader != nil {
		t.Errorf("openCompass with a bad heading = %v, %v, want an error", reader, err)
	}
}

// TestCompassReaderIsGoroutineClean asserts a closed reader reports no heading
// state of its own once its source ends, and that the static provider needs no
// source at all.
func TestCompassReaderIsGoroutineClean(t *testing.T) {
	t.Parallel()

	reader := NewCompassReader(nil)
	if err := reader.Run(context.Background()); err != nil {
		t.Errorf("Run on a static provider: %v, want nil", err)
	}
	reader.Start(context.Background())
	if err := reader.Close(); err != nil {
		t.Errorf("Close on a static provider: %v", err)
	}
	if got := reader.LastHeading(); got.Valid {
		t.Errorf("an unset static provider reports %+v, want no heading", got)
	}
}
