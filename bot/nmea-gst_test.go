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

// TestEmitGSTParseRoundTrip asserts the position-error sentence survives the
// parser as the accuracy it described. It is the only sentence in the format that
// carries an error in meters, and a reader deciding whether to trust a distance
// needs to know whether the fix behind it is good to three meters or to forty.
func TestEmitGSTParseRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		acc  float64
	}{
		{"three metres", 3.0},
		{"sub metre", 0.85},
		{"typical phone", 12.5},
		{"poor", 63.574177},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fix := GPSFix{Valid: true, Lat: 35.1, Lng: -106.5, AccuracyM: tc.acc, HasAccuracy: true}
			line := EmitGST(fix)
			if _, err := parseNMEALine(line); err != nil {
				t.Fatalf("parseNMEALine(%q): %v", line, err)
			}
			got := gstFromLine(t, line)
			if !got.HasAccuracy {
				t.Fatalf("EmitGST(%v) produced %q, which carried no accuracy", tc.acc, line)
			}
			if math.Abs(got.AccuracyM-tc.acc) > 1e-6 {
				t.Fatalf("accuracy = %v, want %v", got.AccuracyM, tc.acc)
			}
		})
	}
}

// TestEmitGSTWithoutAccuracy asserts a fix nobody measured the error of emits no
// error rather than a flattering zero. A position reported as accurate to zero
// meters is the worst possible lie.
func TestEmitGSTWithoutAccuracy(t *testing.T) {
	t.Parallel()

	line := EmitGST(GPSFix{Valid: true, Lat: 35.1, Lng: -106.5})
	got := gstFromLine(t, line)
	if got.HasAccuracy {
		t.Fatalf("EmitGST without an accuracy produced %q, which the reader accepted as %v m", line, got.AccuracyM)
	}
}

// TestEmitGSTGolden pins the exact bytes of the error sentence against an
// independent computation, so a refactor cannot silently change the field order.
func TestEmitGSTGolden(t *testing.T) {
	t.Parallel()

	fix := GPSFix{
		Valid:       true,
		Lat:         35.123456,
		Lng:         -106.567890,
		AccuracyM:   3.8,
		HasAccuracy: true,
		TimeUTC:     goldenAt,
	}
	wantBody := "GNGST,155340.123,3.8,3.8,3.8,0,2.68700576850888,2.68700576850888,"
	if got := strings.TrimSuffix(EmitGST(fix), "\r\n"); !strings.HasPrefix(got, "$"+wantBody) {
		t.Fatalf("EmitGST = %q, want a body of %q", got, wantBody)
	}
	line := strings.TrimRight(EmitGST(fix), "\r\n")
	if !strings.Contains(line, "*") {
		t.Fatalf("EmitGST produced %q, which carries no checksum", line)
	}
	if wrong := "$" + wantBody + "*" + nmeaChecksum(t, wantBody); line != wrong {
		t.Fatalf("EmitGST = %q, want %q", line, wrong)
	}
}

// TestGSTDoesNotInventAPosition asserts the error sentence alone never makes the
// reader report a position: it describes the quality of a fix, and without a
// sentence that actually carries a position there is nothing to describe.
func TestGSTDoesNotInventAPosition(t *testing.T) {
	t.Parallel()

	fix := gstFromLine(t, EmitGST(GPSFix{Valid: true, Lat: 35.1, Lng: -106.5, AccuracyM: 3.8, HasAccuracy: true}))
	if fix.Valid {
		t.Fatalf("an error sentence alone reported a valid fix: %+v", fix)
	}
	if fix.Lat != 0 || fix.Lng != 0 {
		t.Fatalf("an error sentence alone reported a position: %+v", fix)
	}
}

// TestAccuracySurvivesASentenceRotation asserts the accuracy is merged alongside
// the position, which is how a receiver really emits: one sentence per field group,
// in rotation.
func TestAccuracySurvivesASentenceRotation(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		testRMCSentence,
		testGGASentence,
		EmitGST(GPSFix{AccuracyM: 4.25, HasAccuracy: true, TimeUTC: goldenAt}),
	}, "\r\n") + "\r\n"

	fix := fixFromLine(t, stream)
	if !fix.Valid {
		t.Fatalf("the rotation reported no fix: %+v", fix)
	}
	if !fix.HasAccuracy || math.Abs(fix.AccuracyM-4.25) > 1e-6 {
		t.Fatalf("accuracy = %v (has=%v), want 4.25", fix.AccuracyM, fix.HasAccuracy)
	}
}

// TestRejectsAMalformedErrorSentence asserts a corrupt error sentence is dropped
// like any other: a wrong accuracy is a wrong confidence.
func TestRejectsAMalformedErrorSentence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
	}{
		{"too few fields", sentence(t, "GNGST,155340.123,3.8,3.8")},
		{"latitude error is not a number", sentence(t, "GNGST,155340.123,3.8,3.8,3.8,0,east,2.6,")},
		{"longitude error is missing", sentence(t, "GNGST,155340.123,3.8,3.8,3.8,0,2.6,,")},
		{"negative error", sentence(t, "GNGST,155340.123,3.8,3.8,3.8,0,-2.6,2.6,")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fix := gstFromLine(t, tc.line)
			if fix.HasAccuracy {
				t.Fatalf("%q was accepted as an accuracy of %v m", tc.line, fix.AccuracyM)
			}
		})
	}
}

// gstFromLine parses one sentence through a reader and returns the resulting fix.
func gstFromLine(t *testing.T, line string) GPSFix {
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

// TestEmitGSTIsFrozenInTime asserts the emitter's timestamp field carries the same
// instant the recommended-minimum sentence would, so a rotation of sentences all
// describes one reading.
func TestEmitGSTIsFrozenInTime(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 8, 15, 53, 40, 123_000_000, time.UTC)
	sentenceText, err := parseNMEALine(EmitGST(GPSFix{AccuracyM: 1, HasAccuracy: true, TimeUTC: at}))
	if err != nil {
		t.Fatalf("parseNMEALine: %v", err)
	}
	if got := sentenceText.fields[1]; got != "155340.123" {
		t.Fatalf("time field = %q, want %q", got, "155340.123")
	}
	if got := sentenceText.talker; got != "GN" {
		t.Fatalf("talker = %q, want GN", got)
	}
	if got := sentenceText.kind; got != "GST" {
		t.Fatalf("kind = %q, want GST", got)
	}
}
