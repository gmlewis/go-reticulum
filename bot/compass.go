// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the electronic compass front end: the NMEA-0183 heading
// sentences a digital compass or a marine instrument emits, and the streaming
// reader that keeps one live orientation.
//
// It exists because a satellite receiver cannot answer the one question an
// operator with a directional antenna needs answered while standing still.
// GNSS derives its course over ground from motion, so a person who is not
// moving has no heading at all — the receiver reports the last one it saw, or
// noise, or nothing. A three-axis magnetometer knows which way the device is
// pointing whether it is moving or not, and the sensor breakout most field
// builds use speaks NMEA, exactly like the receiver beside it. Parsing it with
// the same checksum discipline and the same streaming reader shape as the GNSS
// front end means one serial idiom serves both devices.
//
// A heading is a magnetic heading until it is corrected, so the reader also
// carries the one conversion that makes it useful: when a sentence reports
// magnetic north only, and the device knows where it is, the World Magnetic
// Model supplies the local variation and the reader reports true north as well.
package bot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// CompassHeading is one live orientation: where the device points, in both the
// magnetic and the true frame, together with the tilt a level-corrected sensor
// can report.
type CompassHeading struct {
	// Valid is true when at least one of the two headings is known.
	Valid bool
	// MagneticDeg is the heading relative to magnetic north, 0.0 to 359.9.
	MagneticDeg float64
	// TrueDeg is the heading relative to true north, 0.0 to 359.9.
	TrueDeg float64
	// DeclinationDeg is the magnetic variation in degrees, positive east and
	// negative west: the angle to add to MagneticDeg to obtain TrueDeg.
	DeclinationDeg float64
	// HasDeclination is true when TrueDeg was derived with a verified
	// declination, either from the sentence's own variation field or from the
	// World Magnetic Model at the device's position.
	HasDeclination bool
	// HasMagnetic and HasTrue report which of the two headings this reading
	// actually carries, so a magnetic heading is never rendered as a true one.
	// A true-only sentence from a fluxgate, and a magnetic-only sentence on a
	// device with no position, are both ordinary states.
	HasMagnetic bool
	HasTrue     bool
	// Cardinal is the sixteen-point compass sector of the best known heading.
	Cardinal string
	// PitchDeg and RollDeg are the tilt angles a level-corrected sensor
	// reports. A plain NMEA compass sentence carries neither, and they stay
	// zero until a tilt sensor supplies them.
	PitchDeg float64
	RollDeg  float64
	// TimeUTC is when the reading was taken.
	TimeUTC time.Time
}

// CardinalDirection names the sixteen-point compass sector an angle falls in.
// The sector names are shared with the navigation engine, so a bearing and a
// heading that point the same way are named the same way.
func CardinalDirection(deg float64) string {
	return CompassPoint(deg)
}

// headingCardinal returns the sector of whichever heading is known, preferring
// the true one.
func headingCardinal(h CompassHeading) string {
	switch {
	case h.HasTrue:
		return CardinalDirection(h.TrueDeg)
	case h.HasMagnetic:
		return CardinalDirection(h.MagneticDeg)
	}
	return ""
}

// CompassReader is a thread-safe streaming reader over one NMEA compass source,
// such as a magnetometer on a serial line, a multiplexed receiver feed, or a
// file. It keeps the most recent heading its owner can act on.
//
// A reader built with a nil source is the static provider: it holds only the
// heading SetHeading injected, which is what a rehearsing operator and a unit
// test use.
type CompassReader struct {
	// src is the NMEA byte stream, nil for a static provider.
	src io.Reader
	// now is the clock, injected so a test can pin the declination date.
	now func() time.Time
	// declination computes the magnetic variation at a position. It is the
	// World Magnetic Model, and it is a field so a test can pin it.
	declination func(lat, lng float64, t time.Time) float64
	// location supplies the device's own position, which is what the
	// declination needs. It is nil on a node with no receiver, and the reader
	// then reports the magnetic heading as magnetic rather than inventing a
	// correction from nowhere.
	location func() (GPSFix, bool)

	mu sync.Mutex
	// heading is the merged state every reader sees.
	heading CompassHeading
	// closed reports that Close has run, so a scanner error is no longer
	// reported as a failure.
	closed bool
	// closeOnce guards the source close, so Close is idempotent.
	closeOnce sync.Once
	// closeErr remembers what closing the source reported.
	closeErr error
	// wg tracks the scan goroutine Start launched, so Close can reap it.
	wg sync.WaitGroup
}

// NewCompassReader builds a reader over src. A nil src builds a static
// provider, which reports only what SetHeading was given.
func NewCompassReader(src io.Reader) *CompassReader {
	return &CompassReader{src: src, now: time.Now, declination: EstimateMagneticDeclination}
}

// SetLocationSource wires the device's own position into the reader, so a
// magnetic-only heading is converted to a true one with the local variation.
// The function is called on every read and must be safe to call concurrently.
func (c *CompassReader) SetLocationSource(fix func() (GPSFix, bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.location = fix
}

// SetHeading replaces the whole heading. It is how a static compass bearing is
// injected and how a test mocks one, and it is the only way a static provider
// is ever populated.
func (c *CompassReader) SetHeading(h CompassHeading) {
	h.Valid = h.HasMagnetic || h.HasTrue
	h.Cardinal = headingCardinal(h)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heading = h
}

// LastHeading returns the most recent heading, with the true heading filled in
// from the World Magnetic Model when the sentence carried magnetic north only
// and the device knows where it is. It is safe to call from any goroutine while
// a scan is running.
func (c *CompassReader) LastHeading() CompassHeading {
	c.mu.Lock()
	heading := c.heading
	location := c.location
	declination := c.declination
	now := c.now
	c.mu.Unlock()
	// A heading that already carries true north, and a heading with no position
	// to correct it against, are both returned exactly as they stand.
	if heading.HasTrue || heading.HasDeclination || !heading.HasMagnetic ||
		location == nil || declination == nil {
		return heading
	}
	fix, ok := location()
	if !ok {
		return heading
	}
	at := heading.TimeUTC
	if at.IsZero() {
		at = now()
	}
	variation := declination(fix.Lat, fix.Lng, at)
	heading.DeclinationDeg = variation
	heading.HasDeclination = true
	heading.TrueDeg = normalizeDegrees(heading.MagneticDeg + variation)
	heading.HasTrue = true
	heading.Cardinal = headingCardinal(heading)
	return heading
}

// Start launches the scan in the background and returns at once. The goroutine
// ends when src reports end of stream, when it fails, or when ctx is canceled,
// and Close waits for it, so nothing is left running past a shutdown.
func (c *CompassReader) Start(ctx context.Context) {
	if c.src == nil {
		return
	}
	c.wg.Go(func() {
		_ = c.Run(ctx)
	})
}

// Run scans src until it ends, fails, or ctx is canceled. It blocks, which is
// what a caller that owns its own goroutine wants, and it returns nil for an
// ordinary end of stream.
func (c *CompassReader) Run(ctx context.Context) error {
	if c.src == nil {
		return nil
	}
	scanner := bufio.NewScanner(c.src)
	scanner.Buffer(make([]byte, 0, 128), nmeaMaxLineBytes)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		c.consume(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil || c.isClosed() {
			return nil
		}
		return err
	}
	return nil
}

// Close stops the scan and releases the source. It is idempotent, and it waits
// for a scan goroutine Start launched, so the reader owns no goroutine after it
// returns.
func (c *CompassReader) Close() error {
	if c.src != nil {
		c.closeOnce.Do(func() {
			c.mu.Lock()
			c.closed = true
			c.mu.Unlock()
			if closer, ok := c.src.(io.Closer); ok {
				c.closeErr = closer.Close()
			}
		})
	}
	c.wg.Wait()
	return c.closeErr
}

// isClosed reports whether Close has run.
func (c *CompassReader) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// consume parses one line and merges it. An unparseable line is dropped without
// a word: a serial sensor line is noisy by nature, and a corrupted heading is a
// wrong direction, which is worse than no direction at all.
func (c *CompassReader) consume(line string) {
	parsed, err := parseNMEALine(line)
	if err != nil {
		return
	}
	switch parsed.kind {
	case "HDG":
		c.applyHDG(parsed.fields)
	case "HDM":
		c.applyHDM(parsed.fields)
	case "HDT":
		c.applyHDT(parsed.fields)
	}
}

// applyHDG merges a compass heading with deviation and variation sentence:
// heading, deviation, variation, and a true heading derived from them. The
// deviation is a compass-installation error that a calibrated installation has
// already accounted for, and it is validated but not applied; the variation is
// the Earth's field and is what turns the heading true.
func (c *CompassReader) applyHDG(fields []string) {
	if len(fields) < 5 {
		return
	}
	magnetic, ok := parseNMEAHeading(fields[1])
	if !ok {
		return
	}
	heading := CompassHeading{
		Valid:       true,
		HasMagnetic: true,
		MagneticDeg: magnetic,
		TimeUTC:     c.now(),
	}
	direction := ""
	if len(fields) > 5 {
		direction = fields[5]
	}
	if variation, ok := parseNMEAVariation(fields[4], direction); ok {
		heading.DeclinationDeg = variation
		heading.HasDeclination = true
		heading.TrueDeg = normalizeDegrees(magnetic + variation)
		heading.HasTrue = true
	}
	c.store(heading)
}

// applyHDM merges a magnetic heading sentence: heading and the magnetic
// indicator. A sentence whose indicator says true is refused rather than
// converted, because the two frames differ by up to twenty degrees and a
// mislabelled sentence is exactly the kind of error this reader exists to
// prevent.
func (c *CompassReader) applyHDM(fields []string) {
	if len(fields) < 2 || !compassFrameIndicator(fields, "M") {
		return
	}
	magnetic, ok := parseNMEAHeading(fields[1])
	if !ok {
		return
	}
	c.store(CompassHeading{
		Valid:       true,
		HasMagnetic: true,
		MagneticDeg: magnetic,
		TimeUTC:     c.now(),
	})
}

// applyHDT merges a true heading sentence: heading and the true indicator. It
// is the one sentence that needs no declination, because the instrument has
// already applied it.
func (c *CompassReader) applyHDT(fields []string) {
	if len(fields) < 2 || !compassFrameIndicator(fields, "T") {
		return
	}
	trueHeading, ok := parseNMEAHeading(fields[1])
	if !ok {
		return
	}
	c.store(CompassHeading{
		Valid:   true,
		HasTrue: true,
		TrueDeg: trueHeading,
		TimeUTC: c.now(),
	})
}

// compassFrameIndicator reports whether a sentence's optional third field names
// the frame the heading is in. An absent field is accepted, because some
// instruments omit it; a present one must match.
func compassFrameIndicator(fields []string, frame string) bool {
	if len(fields) < 3 || strings.TrimSpace(fields[2]) == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(fields[2]), frame)
}

// store records one heading, labeling it with its compass sector.
func (c *CompassReader) store(heading CompassHeading) {
	heading.Cardinal = headingCardinal(heading)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heading = heading
}

// parseNMEAHeading reads a heading field, which is a decimal degree count from
// zero up to and including a full turn. A receiver that reports exactly 360 is
// wrapped to zero rather than dropped, because it means the same direction.
func parseNMEAHeading(text string) (float64, bool) {
	value, ok := parseNMEAFloat(text)
	if !ok || value < 0 || value > 360 {
		return 0, false
	}
	return normalizeDegrees(value), true
}

// parseNMEAVariation reads the two fields of a magnetic variation: the angle in
// degrees and the hemisphere it is east or west of. A variation with no
// hemisphere, or with one that is neither east nor west, cannot be signed and
// is refused.
func parseNMEAVariation(value, direction string) (float64, bool) {
	magnitude, ok := parseNMEAFloat(value)
	if !ok || magnitude < 0 || magnitude > 180 {
		return 0, false
	}
	switch strings.ToUpper(strings.TrimSpace(direction)) {
	case "E":
		return magnitude, true
	case "W":
		return -magnitude, true
	}
	return 0, false
}

// openCompass builds the electronic compass source the operator configured, or
// nil when none is.
//
// A configured device is opened read-only and scanned in the background, the
// same way the GNSS receiver is. A configured static heading is held as the
// whole answer, which is what a headless node and a rehearsing operator use: it
// is a magnetic bearing, so the World Magnetic Model converts it to true north
// as soon as the device knows where it is.
//
// The caller owns the returned reader and must Close it.
func openCompass(cfg *BotConfig) (*CompassReader, error) {
	if cfg == nil {
		return nil, nil
	}
	if port := strings.TrimSpace(cfg.CompassPort); port != "" {
		file, err := os.OpenFile(port, os.O_RDONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("compass: could not open %v: %w", port, err)
		}
		reader := NewCompassReader(file)
		reader.Start(context.Background())
		return reader, nil
	}
	if text := strings.TrimSpace(cfg.CompassHeading); text != "" {
		bearing, err := ParseBearing(text)
		if err != nil {
			return nil, fmt.Errorf("compass_heading %q: %w", text, err)
		}
		reader := NewCompassReader(nil)
		reader.SetHeading(CompassHeading{
			Valid:       true,
			HasMagnetic: true,
			MagneticDeg: bearing,
			TimeUTC:     time.Now(),
		})
		return reader, nil
	}
	return nil, nil
}
