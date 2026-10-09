// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gonsensor converts a JSON sensor sample stream on stdin into NMEA-0183
// sentences on stdout, so that an Android application holding the platform's
// own GNSS receiver and magnetometer can feed the Go Reticulum Buddy and the
// Nomad Network client without either side reimplementing the format.
//
// # Why this program exists
//
// Android hands a program Java objects, not bytes: a Location, a float array
// from SensorManager. The consumer of a position on this side is a strict
// NMEA-0183 parser that already exists and is already tested — the one behind
// every position-dependent bot command. Writing the emitter in Go, in the very
// package that owns the parser, makes the parser the oracle: the round-trip
// property "parse(emit(fix)) equals fix" is a Go test, and the Android side
// stays a thin, boring adapter from platform objects to JSON.
//
// # Input
//
// One JSON object per line on stdin. A line describing a fix carries a
// timestamp, a provider, a position, and a validity flag:
//
//	{"t":"2026-10-08T15:53:40.123Z","provider":"gps","lat":35.123456,
//	 "lng":-106.567890,"alt":1620.5,"acc":3.8,"speed":0.4,"course":271.3,
//	 "sats":8,"quality":1,"valid":true}
//
// A line describing a heading carries the angle and the frame it is measured
// in:
//
//	{"t":"2026-10-08T15:53:40.223Z","heading":47.5,"frame":"magnetic"}
//
// # Design decisions worth stating
//
// A heading is a magnetic heading until it is corrected, and Android's rotation
// vector is referenced to magnetic north, so an absent "frame" means magnetic.
// A sample in a magnetic frame produces the magnetic sentence and a sample in a
// true frame produces the true one; the two are never emitted for the same
// reading, because a true heading written from a magnetic one is off by the
// local variation — up to twenty degrees — and is exactly the kind of
// confidently wrong answer this pipeline exists to avoid.
//
// The sample's accuracy rides the wire in the NMEA position-error sentence, which
// is the one sentence the format defines for an error in meters. Pitch and roll do
// not: no heading sentence carries tilt, and inventing one would make the stream
// unreadable to a standard consumer, so they stay in the producing app's own UI and
// its own sample log. The ill-conditioned case is signalled the one way a reader
// must honour: the producer withholds the heading field, and no heading sentence is
// emitted for that sample.
//
// A malformed line is dropped in silence. A noisy stream is normal — a sensor
// service restarts, a phone call arrives, a buffer is half flushed — and the
// useful signal is the next good sample, not a log entry about the last bad one.
// The periodic count on stderr is the only place a bad line is visible at all, and
// it counts the two cases apart: "dropped" is a line this program could not read,
// while "withheld" is a well-formed sample whose producer reported that it had
// nothing to report. Only the first is a failure; the second is the producer
// declining to state a number it does not believe, which is what the Android
// appliance does when its heading reference axis is ill-conditioned. Keeping them
// in one counter would make a deliberate silence look like lost data.
//
// # Output
//
// NMEA-0183 sentences on stdout, each beginning with '$', carrying its XOR
// checksum, and terminated with CRLF. The stream never ends while stdin is
// open, so nothing reading it ever sees an end of file that is not the end of
// the sensor service. gonsensor exits 0 when stdin reaches end of file.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/bot"
	"github.com/gmlewis/go-reticulum/rns"
)

// Input bounds.
const (
	// maxSampleBytes bounds one input line. A sensor sample is a few hundred
	// bytes; the limit is generous so a chatty producer whose line carries an
	// unexpected field still gets through, while a stream that has lost its
	// framing cannot exhaust memory.
	maxSampleBytes = 64 * 1024
	// scannerInitialBytes is the initial scan buffer, sized to the ordinary
	// sample so the common case never reallocates.
	scannerInitialBytes = 1024
)

// headingFrame names the reference a heading is measured against.
type headingFrame string

const (
	// frameMagnetic is a heading relative to magnetic north. It is the default,
	// because that is what an Android rotation vector reports.
	frameMagnetic headingFrame = "magnetic"
	// frameTrue is a heading relative to true north, which the producer has
	// already corrected.
	frameTrue headingFrame = "true"
)

// sample is one line of the input stream, decoded. Every measurement is a
// pointer so that "absent" and "zero" stay distinguishable: a heading of zero is
// due north and a missing heading is no heading at all.
type sample struct {
	// Timestamp is the acquisition time the producer reported. It is the zero
	// time when the sample carried none or carried one that will not parse.
	Timestamp time.Time
	// HasTimestamp reports whether Timestamp came from the sample itself.
	HasTimestamp bool
	// Valid is the producer's own verdict on the fix. It is nil when the sample
	// did not say, which is read as valid.
	Valid *bool
	// Lat and Lng are the WGS-84 position in signed decimal degrees.
	Lat *float64
	Lng *float64
	// Alt is the antenna altitude above mean sea level in meters.
	Alt *float64
	// Speed is the speed over ground in knots.
	Speed *float64
	// Course is the true track angle in degrees.
	Course *float64
	// Quality is the fix-quality indicator, 1 for an ordinary standalone fix.
	Quality *int
	// Sats is how many satellites the receiver used.
	Sats *int
	// Acc is the producer's own estimate of the fix's horizontal error in
	// meters. It is the only field that reaches the wire as a real accuracy,
	// through the NMEA position-error sentence.
	Acc *float64
	// Heading is the angle from the frame's north, in degrees.
	Heading *float64
	// Frame is the reference the heading is measured against.
	Frame headingFrame

	// isFix and isHeading report which halves of the sample are present, so a
	// line that described neither is rejected rather than silently ignored.
	isFix     bool
	isHeading bool
}

// rawSample mirrors the JSON wire form. encoding/json ignores unknown fields, so
// a producer may add anything it likes without breaking this program.
type rawSample struct {
	T        *string  `json:"t"`
	Provider *string  `json:"provider"`
	Lat      *float64 `json:"lat"`
	Lng      *float64 `json:"lng"`
	Alt      *float64 `json:"alt"`
	Speed    *float64 `json:"speed"`
	Course   *float64 `json:"course"`
	Quality  *int     `json:"quality"`
	Sats     *int     `json:"sats"`
	Acc      *float64 `json:"acc"`
	Valid    *bool    `json:"valid"`
	Heading  *float64 `json:"heading"`
	Frame    *string  `json:"frame"`
}

func main() {
	log.SetFlags(0)

	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if opts.version {
		fmt.Printf("gonsensor %v\n", rns.VERSION)
		os.Exit(0)
	}
	if err := runSensor(os.Stdin, os.Stdout, os.Stderr, opts, time.Now); err != nil {
		log.Fatal(err)
	}
}

// runSensor reads the JSON sample stream from in, writes NMEA-0183 sentences to
// sentences, and writes periodic status lines to status. It returns nil at end
// of input, which is the signal for a caller to reap it.
//
// now is the clock, injected so a test can pin the time a sample with no
// timestamp of its own is stamped with.
func runSensor(in io.Reader, sentences, status io.Writer, opts *options, now func() time.Time) error {
	if opts == nil {
		opts = &options{}
	}
	if now == nil {
		now = time.Now
	}
	state := &sensorState{opts: opts, now: now}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, scannerInitialBytes), maxSampleBytes)
	for scanner.Scan() {
		if err := state.consume(scanner.Text(), sentences, status); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// sensorState is the running state of one conversion: the rate-limit windows,
// the status clock, and the counters the status line reports.
type sensorState struct {
	// opts is the command line.
	opts *options
	// now is the clock.
	now func() time.Time
	// lastFixAt and lastHeadingAt are when the last sentence of each kind was
	// emitted, which is what opens the next rate-limit window.
	lastFixAt     time.Time
	lastHeadingAt time.Time
	// lastStatusAt is when the last status line was written.
	lastStatusAt time.Time
	// fixes, headings, withheld, and dropped count what has arrived so far.
	// withheld samples are not a loss: see lineWithheld.
	fixes    int
	headings int
	withheld int
	dropped  int
}

// consume handles one input line.
func (s *sensorState) consume(line string, sentences, status io.Writer) error {
	text := strings.TrimSpace(line)
	if text == "" {
		return nil
	}
	sample, outcome := parseSample(text)
	at := s.sampleClock(sample)
	switch outcome {
	case lineRefused:
		s.dropped++
		return s.maybeStatus(at, status)
	case lineWithheld:
		s.withheld++
		return s.maybeStatus(at, status)
	}
	if sample.isFix {
		if err := s.emitFix(sample, at, sentences); err != nil {
			return err
		}
	}
	if sample.isHeading {
		if err := s.emitHeading(sample, at, sentences); err != nil {
			return err
		}
	}
	return s.maybeStatus(at, status)
}

// sampleClock is the instant a line is dated with: the sample's own timestamp
// when it carried one, and the process clock otherwise. Every rate limit and the
// status interval are measured against it, so a producer's own sense of time
// governs its stream even when the two processes' clocks disagree.
func (s *sensorState) sampleClock(sample sample) time.Time {
	if sample.HasTimestamp {
		return sample.Timestamp
	}
	return s.now()
}

// emitFix writes the sentences that describe one position sample.
func (s *sensorState) emitFix(sample sample, at time.Time, sentences io.Writer) error {
	fix := s.fixFrom(sample, at)
	if !s.withinWindow(at, &s.lastFixAt, s.opts.rate) {
		return nil
	}
	if !s.opts.noRMC {
		if _, err := io.WriteString(sentences, bot.EmitRMC(fix, at)); err != nil {
			return err
		}
	}
	if !s.opts.noGGA {
		if _, err := io.WriteString(sentences, bot.EmitGGA(fix)); err != nil {
			return err
		}
	}
	if !s.opts.noGST {
		if _, err := io.WriteString(sentences, bot.EmitGST(fix)); err != nil {
			return err
		}
	}
	s.fixes++
	return nil
}

// fixFrom turns one decoded sample into the validation type the emitter takes.
// A sample that is invalid, or that never carried a position, becomes a void
// fix: the status field says so and every measured field is empty, so a reader
// is never left holding the previous position as if it were current.
func (s *sensorState) fixFrom(sample sample, at time.Time) bot.GPSFix {
	fix := bot.GPSFix{TimeUTC: at}
	if sample.Valid != nil && !*sample.Valid {
		return fix
	}
	if sample.Lat == nil || sample.Lng == nil {
		return fix
	}
	fix.Valid = true
	fix.Lat = *sample.Lat
	fix.Lng = *sample.Lng
	fix.FixQuality = 1
	if sample.Quality != nil {
		fix.FixQuality = *sample.Quality
	}
	if sample.Alt != nil {
		fix.AltitudeM = *sample.Alt
		fix.HasAltitude = true
	}
	if sample.Sats != nil {
		fix.Satellites = *sample.Sats
	}
	if sample.Acc != nil {
		fix.AccuracyM = *sample.Acc
		fix.HasAccuracy = true
	}
	if sample.Speed != nil {
		fix.SpeedKnots = *sample.Speed
	}
	if sample.Course != nil {
		fix.CourseDeg = *sample.Course
	}
	return fix
}

// emitHeading writes the heading sentence for one sample, in the frame the
// sample declared. Exactly one sentence is written: writing both would claim the
// true heading equals the magnetic one.
func (s *sensorState) emitHeading(sample sample, at time.Time, sentences io.Writer) error {
	trueFrame := sample.Frame == frameTrue
	if trueFrame && s.opts.noHDT {
		return nil
	}
	if !trueFrame && s.opts.noHDM {
		return nil
	}
	if !s.withinWindow(at, &s.lastHeadingAt, s.opts.compassRate) {
		return nil
	}
	sentence := bot.EmitHDM(*sample.Heading, at)
	if trueFrame {
		sentence = bot.EmitHDT(*sample.Heading, at)
	}
	if _, err := io.WriteString(sentences, sentence); err != nil {
		return err
	}
	s.headings++
	return nil
}

// withinWindow applies the leading-edge rate limit: the first sentence of a
// window is written and the rest are dropped, and a zero rate turns the limit
// off. A sample that arrives with an earlier timestamp than the last one — a
// producer whose clock stepped backwards — is treated as inside the window
// rather than as a fresh one, because a stream that rewinds must not be able to
// flood the reader.
func (s *sensorState) withinWindow(at time.Time, last *time.Time, rate float64) bool {
	if rate <= 0 {
		*last = at
		return true
	}
	window := time.Duration(float64(time.Second) / rate)
	if last.IsZero() || at.Sub(*last) >= window {
		*last = at
		return true
	}
	return false
}

// maybeStatus writes the periodic summary line, which is the only diagnostic an
// operator has when the stream is a pipe nobody can inspect.
func (s *sensorState) maybeStatus(at time.Time, status io.Writer) error {
	if status == nil || s.opts.statusInterval <= 0 {
		return nil
	}
	if s.lastStatusAt.IsZero() {
		s.lastStatusAt = at
		return nil
	}
	interval := time.Duration(s.opts.statusInterval * float64(time.Second))
	if at.Sub(s.lastStatusAt) < interval {
		return nil
	}
	s.lastStatusAt = at
	_, err := fmt.Fprintf(status, "gonsensor: %v fixes, %v headings, %v withheld, %v dropped\n",
		s.fixes, s.headings, s.withheld, s.dropped)
	return err
}

// lineOutcome is what one input line turned out to be, which is the whole
// difference between a reading that was lost and a reading that was never taken.
type lineOutcome int

const (
	// lineRefused is a line this program could not read: it is not a JSON
	// object, one of its values is outside its legal range, or nothing in it
	// identifies it as a sample at all. It is counted as dropped, because a
	// sample was sent and no reading came of it.
	lineRefused lineOutcome = iota

	// lineWithheld is a well-formed sample carrying no measurement: the producer
	// reported, at that instant, that it had nothing to report. It is counted as
	// withheld, which is a statement about the producer's decision rather than
	// about this program's reading of the stream. A producer is expected to
	// withhold deliberately — the Android appliance does when its heading
	// reference axis is ill-conditioned — and to say why in its own log once,
	// rather than to emit a number it does not believe.
	lineWithheld

	// lineMeasured is a sample carrying at least one measurement.
	lineMeasured
)

// parseSample decodes one input line. A line that is not a JSON object, that
// carries a value outside the legal range, or that describes neither a fix nor a
// heading is refused unless it is a sample the producer deliberately sent empty:
// a wrong position is worse than no position, but an announced absence of
// readings is not a wrong reading.
func parseSample(text string) (sample, lineOutcome) {
	var raw rawSample
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return sample{}, lineRefused
	}

	decoded := sample{Frame: frameMagnetic}
	if raw.T != nil {
		if at, err := time.Parse(time.RFC3339Nano, *raw.T); err == nil {
			decoded.Timestamp = at.UTC()
			decoded.HasTimestamp = true
		}
	}
	if raw.Frame != nil {
		frame := headingFrame(strings.ToLower(strings.TrimSpace(*raw.Frame)))
		if frame != frameMagnetic && frame != frameTrue {
			return sample{}, lineRefused
		}
		decoded.Frame = frame
	}

	decoded.Valid = raw.Valid
	decoded.Quality = raw.Quality
	decoded.Sats = raw.Sats
	decoded.Acc = raw.Acc
	if !validCoordinate(raw.Lat, 90) || !validCoordinate(raw.Lng, 180) {
		return sample{}, lineRefused
	}
	if (raw.Lat == nil) != (raw.Lng == nil) {
		return sample{}, lineRefused
	}
	if raw.Lat != nil {
		decoded.Lat, decoded.Lng = raw.Lat, raw.Lng
		decoded.isFix = true
	} else if raw.Valid != nil {
		decoded.isFix = true
	}
	if raw.Alt != nil {
		if !finite(*raw.Alt) {
			return sample{}, lineRefused
		}
		decoded.Alt = raw.Alt
		decoded.isFix = true
	}
	if raw.Speed != nil {
		if !finite(*raw.Speed) {
			return sample{}, lineRefused
		}
		decoded.Speed = raw.Speed
		decoded.isFix = true
	}
	if raw.Course != nil {
		if !finite(*raw.Course) {
			return sample{}, lineRefused
		}
		decoded.Course = raw.Course
		decoded.isFix = true
	}
	if raw.Heading != nil {
		if !finite(*raw.Heading) {
			return sample{}, lineRefused
		}
		decoded.Heading = raw.Heading
		decoded.isHeading = true
	}
	if raw.Provider != nil || raw.Quality != nil || raw.Sats != nil {
		decoded.isFix = true
	}
	if !decoded.isFix && !decoded.isHeading {
		// Nothing measured. A timestamp is what separates a producer saying
		// "nothing to report right now" from a line that is simply not a sample:
		// every sample this format carries is dated, so a dated line with no
		// readings is a reading that was deliberately not taken.
		if decoded.HasTimestamp {
			return decoded, lineWithheld
		}
		return sample{}, lineRefused
	}
	return decoded, lineMeasured
}

// validCoordinate reports whether an optional coordinate is absent, or present
// and usable: finite, and inside its axis's legal range.
func validCoordinate(value *float64, limit float64) bool {
	if value == nil {
		return true
	}
	return finite(*value) && *value >= -limit && *value <= limit
}

// finite reports whether a value is a real number.
func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
