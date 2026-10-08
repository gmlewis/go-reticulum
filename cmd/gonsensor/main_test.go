// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/bot"
)

// fixedClock is a clock that never moves, so a test that does not care about
// time still has one to inject.
type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the clock's instant.
func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// newFixedClock builds a clock pinned to a fixed instant.
func newFixedClock() *fixedClock {
	return &fixedClock{now: time.Date(2026, time.October, 8, 15, 53, 40, 0, time.UTC)}
}

// unfettered is the option set the tests start from: no rate limiting and no
// status reporting, so each test states only what it is about.
func unfettered() *options {
	return &options{}
}

// locationSample is one fix, as the Android side serializes it.
const locationSample = `{"t":"2026-10-08T15:53:40.123Z","provider":"gps",` +
	`"lat":35.123456,"lng":-106.567890,"alt":1620.5,"acc":3.8,` +
	`"speed":0.4,"course":271.3,"sats":8,"quality":1,"valid":true}`

// headingSample is one compass reading, as the Android side serializes it.
const headingSample = `{"t":"2026-10-08T15:53:40.223Z","heading":47.5,` +
	`"accuracy":3,"pitch":-1.2,"roll":0.8}`

// feedSensor runs one sample stream through the converter and returns what it
// wrote to the sentence stream and to the status stream.
func feedSensor(t *testing.T, opts *options, input string) (string, string) {
	t.Helper()

	var sentences, status bytes.Buffer
	if err := runSensor(strings.NewReader(input), &sentences, &status, opts, newFixedClock().Now); err != nil {
		t.Fatalf("runSensor: %v", err)
	}
	return sentences.String(), status.String()
}

// TestGonsensorFeedsGPSReader is the end-to-end property that makes the whole
// Android design safe: what gonsensor writes is read back by the same reader the
// bot and the network client use, over a real pipe, as the fix that went in.
func TestGonsensorFeedsGPSReader(t *testing.T) {
	t.Parallel()

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runSensor(strings.NewReader(locationSample+"\n"), pw, io.Discard, unfettered(), newFixedClock().Now)
		_ = pw.Close()
		done <- err
	}()

	reader := bot.NewGPSReader(pr)
	t.Cleanup(func() { _ = reader.Close() })
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("GPSReader.Run: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("runSensor: %v", err)
	}

	got := reader.LastFix()
	if !got.Valid {
		t.Fatalf("LastFix = %+v, want a valid fix", got)
	}
	if !closeWithin(got.Lat, 35.123456, 1e-5) || !closeWithin(got.Lng, -106.567890, 1e-5) {
		t.Fatalf("position = (%v,%v), want (35.123456,-106.56789)", got.Lat, got.Lng)
	}
	if !got.HasAltitude || !closeWithin(got.AltitudeM, 1620.5, 1e-6) {
		t.Fatalf("altitude = %v (has=%v), want 1620.5", got.AltitudeM, got.HasAltitude)
	}
	if got.FixQuality != 1 {
		t.Fatalf("fix quality = %v, want 1", got.FixQuality)
	}
	if got.Satellites != 8 {
		t.Fatalf("satellites = %v, want 8", got.Satellites)
	}
	if !closeWithin(got.CourseDeg, 271.3, 1e-6) {
		t.Fatalf("course = %v, want 271.3", got.CourseDeg)
	}
	want := time.Date(2026, time.October, 8, 15, 53, 40, 123_000_000, time.UTC)
	if !got.TimeUTC.Equal(want) {
		t.Fatalf("timestamp = %v, want %v", got.TimeUTC, want)
	}
}

// TestGonsensorFeedsCompassReader is the same property for the heading, which is
// the half of the feed a stationary device cannot get from its receiver.
func TestGonsensorFeedsCompassReader(t *testing.T) {
	t.Parallel()

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runSensor(strings.NewReader(headingSample+"\n"), pw, io.Discard, unfettered(), newFixedClock().Now)
		_ = pw.Close()
		done <- err
	}()

	reader := bot.NewCompassReader(pr)
	t.Cleanup(func() { _ = reader.Close() })
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("CompassReader.Run: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("runSensor: %v", err)
	}

	got := reader.LastHeading()
	if !got.HasMagnetic {
		t.Fatalf("LastHeading = %+v, want a magnetic heading", got)
	}
	if !closeWithin(got.MagneticDeg, 47.5, 1e-6) {
		t.Fatalf("magnetic heading = %v, want 47.5", got.MagneticDeg)
	}
}

// TestGonsensorDropsMalformedLines asserts a noisy stream is normal: every bad
// line is dropped in silence, and the good line beside it still arrives.
func TestGonsensorDropsMalformedLines(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
	}{
		{"garbage", `not json at all`},
		{"truncated", `{"lat":35.1,"lng":`},
		{"empty object", `{}`},
		{"latitude without longitude", `{"lat":35.1}`},
		{"longitude without latitude", `{"lng":-106.5}`},
		{"latitude out of range", `{"lat":90.5,"lng":-106.5}`},
		{"latitude far out of range", `{"lat":-91,"lng":-106.5}`},
		{"longitude out of range", `{"lat":35.1,"lng":181}`},
		{"longitude far out of range", `{"lat":35.1,"lng":-181}`},
		{"latitude is not a number", `{"lat":NaN,"lng":-106.5}`},
		{"longitude overflows a float", `{"lat":35.1,"lng":1e400}`},
		{"coordinate is a string", `{"lat":"35.1","lng":-106.5}`},
		{"heading is a string", `{"heading":"north"}`},
		{"array instead of object", `[35.1,-106.5]`},
		{"blank line", "   "},
		{"a comment the stream might carry", `# a note`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, status := feedSensor(t, unfettered(), tc.line+"\n"+locationSample+"\n")

			if strings.Contains(got, ",V,") {
				t.Fatalf("malformed line %q produced a void sentence: %q", tc.line, got)
			}
			// The good fix deserves its position, quality, and error
			// sentences, and nothing more.
			if count := strings.Count(got, "\r\n"); count != 3 {
				t.Fatalf("%q produced %v sentences, want the 3 the good fix deserves: %q", tc.line, count, got)
			}
			if status != "" {
				t.Fatalf("a malformed line was reported on the status stream: %q", status)
			}
		})
	}
}

// TestGonsensorRateLimiting asserts the emission rate is bounded by the sample
// stream's own clock, so a burst on the input cannot flood the FIFO a bot is
// reading — and the rate is asserted without sleeping on real time.
func TestGonsensorRateLimiting(t *testing.T) {
	t.Parallel()

	opts := unfettered()
	opts.rate, opts.compassRate = 1, 10

	var input strings.Builder
	for i := range 5 {
		stamp := "2026-10-08T15:53:40Z"
		if i >= 3 {
			// A three-second gap in the middle is what reopens both windows.
			stamp = "2026-10-08T15:53:43Z"
		}
		input.WriteString(`{"t":"` + stamp + `","lat":35.1,"lng":-106.5,"valid":true}` + "\n")
		input.WriteString(`{"t":"` + stamp + `","heading":47.5}` + "\n")
	}

	got, _ := feedSensor(t, opts, input.String())

	// At 1 Hz only the leading fix of each window survives: one at the start
	// of the stream and one after the three-second gap.
	if count := strings.Count(got, "GNRMC"); count != 2 {
		t.Fatalf("emitted %v fixes, want 2: %q", count, got)
	}
	// At 10 Hz the heading window is 100 ms, so the same gap reopens it twice
	// while the samples sharing a timestamp collapse to one each.
	if count := strings.Count(got, "HCHDM"); count != 2 {
		t.Fatalf("emitted %v headings, want 2: %q", count, got)
	}
}

// TestGonsensorRateLimitingWithoutSampleTimes asserts a stream that carries no
// timestamp of its own is still bounded, using the injected clock instead.
func TestGonsensorRateLimitingWithoutSampleTimes(t *testing.T) {
	t.Parallel()

	opts := unfettered()
	opts.rate, opts.compassRate = 1, 10

	var input strings.Builder
	for range 5 {
		input.WriteString(`{"lat":35.1,"lng":-106.5,"valid":true}` + "\n")
		input.WriteString(`{"heading":47.5}` + "\n")
	}

	// The clock never moves, so every sample but the first of each kind falls
	// inside the window.
	got, _ := feedSensor(t, opts, input.String())
	if count := strings.Count(got, "GNRMC"); count != 1 {
		t.Fatalf("emitted %v fixes, want 1: %q", count, got)
	}
	if count := strings.Count(got, "HCHDM"); count != 1 {
		t.Fatalf("emitted %v headings, want 1: %q", count, got)
	}
}

// TestGonsensorStopsOnEOF asserts the converter returns cleanly at end of
// stream rather than blocking, which is what lets a caller reap it.
func TestGonsensorStopsOnEOF(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var sentences bytes.Buffer
		if err := runSensor(strings.NewReader(""), &sentences, io.Discard, unfettered(), newFixedClock().Now); err != nil {
			t.Errorf("runSensor: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runSensor did not return on end of stream")
	}
}

// TestGonsensorStatusReportsProgress asserts the periodic status line says
// enough to tell a live pipeline from a dead one, which is the only diagnostic
// an operator has when the stream is a pipe nobody can inspect.
func TestGonsensorStatusReportsProgress(t *testing.T) {
	t.Parallel()

	opts := unfettered()
	opts.statusInterval = 5

	input := `{"t":"2026-10-08T15:53:40Z","lat":35.1,"lng":-106.5,"valid":true}` + "\n" +
		"this line is dropped\n" +
		`{"t":"2026-10-08T15:53:47Z","heading":47.5}` + "\n"

	_, status := feedSensor(t, opts, input)
	if !strings.Contains(status, "1 fixes, 1 headings, 1 dropped") {
		t.Fatalf("status = %q, want one fix, one heading, and one dropped sample", status)
	}
	if !strings.HasPrefix(status, "gonsensor: ") {
		t.Fatalf("status = %q, want a gonsensor prefix", status)
	}
}

// TestGonsensorNoFixEmitsStatusV asserts an explicitly invalid fix is published
// as a void sentence, so a reader is told there is no position rather than being
// left holding the last one.
func TestGonsensorNoFixEmitsStatusV(t *testing.T) {
	t.Parallel()

	got, _ := feedSensor(t, unfettered(), `{"t":"2026-10-08T15:53:40Z","valid":false}`+"\n")
	if !strings.Contains(got, ",V,") {
		t.Fatalf("output = %q, want a void status field", got)
	}
	if strings.Contains(got, "3507.") {
		t.Fatalf("output = %q, want no position in a void sentence", got)
	}
}

// TestGonsensorNoFixBeatsStaleCoordinates asserts a sample that says it is
// invalid never publishes the coordinates it happens to carry, because that is
// how a stale position becomes a confidently wrong one.
func TestGonsensorNoFixBeatsStaleCoordinates(t *testing.T) {
	t.Parallel()

	got, _ := feedSensor(t, unfettered(),
		`{"t":"2026-10-08T15:53:40Z","valid":false,"lat":35.123456,"lng":-106.567890}`+"\n")
	if strings.Contains(got, "3507.") || strings.Contains(got, "10634.") {
		t.Fatalf("output = %q, want the stale coordinates withheld", got)
	}
}

// TestGonsensorSentenceSelection asserts each --no-* switch suppresses exactly
// its own sentence and nothing else, so an operator can trim the stream to what
// the consumer actually parses.
func TestGonsensorSentenceSelection(t *testing.T) {
	t.Parallel()

	// Android's rotation vector is referenced to magnetic north, so a sample
	// with no frame of its own is a magnetic heading.
	magnetic := locationSample + "\n" + headingSample + "\n"
	trueNorth := locationSample + "\n" + `{"heading":47.5,"frame":"true"}` + "\n"

	all, _ := feedSensor(t, unfettered(), magnetic)
	for _, kind := range []string{"GNRMC", "GPGGA", "GNGST", "HCHDM"} {
		if !strings.Contains(all, kind) {
			t.Fatalf("default output is missing %v: %q", kind, all)
		}
	}
	if strings.Contains(all, "HCHDT") {
		t.Fatalf("a magnetic heading was also published as true: %q", all)
	}

	cases := []struct {
		name    string
		flag    string
		stream  string
		absent  string
		present []string
	}{
		{"no-rmc", "no-rmc", magnetic, "GNRMC", []string{"GPGGA", "HCHDM"}},
		{"no-gga", "no-gga", magnetic, "GPGGA", []string{"GNRMC", "HCHDM"}},
		{"no-gst", "no-gst", magnetic, "GNGST", []string{"GNRMC", "GPGGA", "HCHDM"}},
		{"no-hdm", "no-hdm", magnetic, "HCHDM", []string{"GNRMC", "GPGGA"}},
		{"no-hdt", "no-hdt", trueNorth, "HCHDT", []string{"GNRMC", "GPGGA"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts, err := parseFlags([]string{"--" + tc.flag}, io.Discard)
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			got, _ := feedSensor(t, opts, tc.stream)
			if strings.Contains(got, tc.absent) {
				t.Fatalf("--%v left %v in the stream: %q", tc.flag, tc.absent, got)
			}
			for _, kind := range tc.present {
				if !strings.Contains(got, kind) {
					t.Fatalf("--%v also removed %v: %q", tc.flag, kind, got)
				}
			}
		})
	}
}

// TestGonsensorHeadingFrameIsHonoured asserts a true heading and a magnetic
// heading are published as different sentences, because publishing a magnetic
// reading as true is an error of up to twenty degrees that reads as a real
// bearing.
func TestGonsensorHeadingFrameIsHonoured(t *testing.T) {
	t.Parallel()

	got, _ := feedSensor(t, unfettered(), `{"heading":47.5,"frame":"true"}`+"\n")
	if !strings.Contains(got, "HCHDT,47.5,T") {
		t.Fatalf("true-frame output = %q, want a true-heading sentence", got)
	}
	if strings.Contains(got, "HCHDM") {
		t.Fatalf("true-frame output = %q, want no magnetic-heading sentence", got)
	}

	named, _ := feedSensor(t, unfettered(), `{"heading":47.5,"frame":"MAGNETIC"}`+"\n")
	if !strings.Contains(named, "HCHDM,47.5,M") {
		t.Fatalf("explicitly magnetic output = %q, want a magnetic-heading sentence", named)
	}

	if _, ok := parseSample(`{"heading":47.5,"frame":"moon"}`); ok {
		t.Fatalf("an unknown heading frame was accepted")
	}
}

// TestGonsensorHeadingWraps asserts a heading outside a full turn is wrapped
// rather than rejected, matching the emitter's own contract.
func TestGonsensorHeadingWraps(t *testing.T) {
	t.Parallel()

	got, _ := feedSensor(t, unfettered(), `{"heading":372.5}`+"\n")
	if !strings.Contains(got, "HCHDM,12.5,M") {
		t.Fatalf("output = %q, want the heading wrapped to 12.5", got)
	}
}

// TestGonsensorOutputIsChecksummed asserts every emitted line is a complete,
// CRLF-terminated, checksummed sentence, because a half-written line into a FIFO
// is a corrupted position.
func TestGonsensorOutputIsChecksummed(t *testing.T) {
	t.Parallel()

	got, _ := feedSensor(t, unfettered(), locationSample+"\n"+headingSample+"\n")
	lines := strings.Split(got, "\r\n")
	if lines[len(lines)-1] != "" {
		t.Fatalf("output does not end with a CRLF terminator: %q", got)
	}
	for _, line := range lines[:len(lines)-1] {
		if !strings.HasPrefix(line, "$") {
			t.Fatalf("line %q does not start with $", line)
		}
		star := strings.LastIndexByte(line, '*')
		if star < 0 || len(line)-star-1 != 2 {
			t.Fatalf("line %q carries no two-digit checksum", line)
		}
	}
}

// TestGonsensorProviderIsAcceptedButNotLeaked asserts the provider name is
// accepted as an unknown-to-NMEA field without ever appearing on the wire, since
// a sentence stream has nowhere to put it and a reader must not be told a
// network fix came from a satellite.
func TestGonsensorProviderIsNotLeaked(t *testing.T) {
	t.Parallel()

	got, _ := feedSensor(t, unfettered(),
		`{"provider":"network","lat":35.1,"lng":-106.5,"valid":true}`+"\n")
	if strings.Contains(got, "network") {
		t.Fatalf("output = %q, want the provider name left off the wire", got)
	}
	if !strings.Contains(got, "GNRMC") {
		t.Fatalf("output = %q, want the fix emitted", got)
	}
}

// closeWithin reports whether got is within tolerance of want.
func closeWithin(got, want, tolerance float64) bool {
	return math.Abs(got-want) <= tolerance
}

// TestGonsensorCarriesAccuracy asserts the sample's own accuracy reaches the wire
// in the one sentence the format defines for it, and that a sample without one
// never reports a flattering zero. This is what lets the client tell a reader how
// much to trust the distance it renders.
func TestGonsensorCarriesAccuracy(t *testing.T) {
	t.Parallel()

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runSensor(strings.NewReader(locationSample+"\n"), pw, io.Discard, unfettered(), newFixedClock().Now)
		_ = pw.Close()
		done <- err
	}()

	reader := bot.NewGPSReader(pr)
	t.Cleanup(func() { _ = reader.Close() })
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("GPSReader.Run: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("runSensor: %v", err)
	}

	got := reader.LastFix()
	if !got.HasAccuracy {
		t.Fatalf("LastFix = %+v, want an accuracy", got)
	}
	if !closeWithin(got.AccuracyM, 3.8, 1e-6) {
		t.Fatalf("accuracy = %v, want the sample's own 3.8 m", got.AccuracyM)
	}
}

// TestGonsensorWithoutAccuracySaysSo asserts a sample that carried no accuracy
// publishes none, because a position reported as accurate to zero meters is the
// worst possible lie.
func TestGonsensorWithoutAccuracySaysSo(t *testing.T) {
	t.Parallel()

	got, _ := feedSensor(t, unfettered(), `{"lat":35.1,"lng":-106.5,"valid":true}`+"\n")
	if !strings.Contains(got, "GNGST") {
		t.Fatalf("output = %q, want a position-error sentence", got)
	}
	fix := gstFromStream(t, got)
	if fix.HasAccuracy {
		t.Fatalf("a sample with no accuracy produced one: %v m", fix.AccuracyM)
	}
}

// gstFromStream parses a whole sentence stream and returns the merged fix.
func gstFromStream(t *testing.T, stream string) bot.GPSFix {
	t.Helper()

	reader := bot.NewGPSReader(strings.NewReader(stream))
	if err := reader.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return reader.LastFix()
}
