// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the pure-Go NMEA-0183 GNSS receiver front end: the sentence
// parser and the streaming reader that keeps one live fix.
//
// It exists because the Go Reticulum Lifesaver has to know where it is with no
// network, no Cgo driver, and no third-party library: a GNSS receiver is a
// serial device that emits ASCII sentences, and turning those into a validated
// coordinate is a self-contained parsing problem. The parser is deliberately
// strict about the checksum, because a corrupted sentence is a wrong position,
// and on a device whose whole purpose is to tell a rescue party where somebody
// is, a wrong position is worse than no position at all. A sentence that fails
// validation is dropped in silence: it is an expected event on a noisy serial
// line, never something to log about or crash on.
//
// The reader is the only stateful part. It merges the sentences a receiver
// emits in rotation — the recommended-minimum position sentence and the
// fix-quality sentence — into one GPSFix under a mutex, so a command handler,
// an HTTP dashboard, and the serial scan goroutine can all touch it at once.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NMEA parsing bounds and sentinels.
const (
	// nmeaMaxLineBytes bounds one sentence. The specification caps a sentence
	// at 82 characters, and the scanner's limit is generous so a receiver with
	// a proprietary long sentence still cannot exhaust memory.
	nmeaMaxLineBytes = 512
	// nmeaTalkerLen is the talker-id prefix length in front of the
	// three-character sentence type ("GN" + "RMC").
	nmeaTalkerLen = 2
	// nmeaChecksumLen is how many hexadecimal characters the checksum carries.
	nmeaChecksumLen = 2
	// nmeaMinutesPerDegree divides the minutes field of a ddmm.mmmm value.
	nmeaMinutesPerDegree = 60
	// nmeaSecondsPerMinute and nmeaCenturyBase turn an hhmmss.sss time and a
	// ddmmyy date into an absolute timestamp.
	nmeaSecondsPerMinute = 60
	nmeaCenturyBase      = 2000
)

// Errors the NMEA parser reports. They are internal to this file: the reader
// treats every one of them as "drop the sentence", and a caller that needs to
// tell them apart can, but nothing outside the parser does.
var (
	// errNMEATooShort reports a line that cannot hold a sentence.
	errNMEATooShort = errors.New("nmea: line too short")
	// errNMEAStart reports a line that does not begin with a sentence starter.
	errNMEAStart = errors.New("nmea: line does not start with $ or !")
	// errNMEAChecksum reports a missing, malformed, or mismatched checksum.
	errNMEAChecksum = errors.New("nmea: checksum does not match")
	// errNMEAFields reports a sentence with too few comma-separated fields.
	errNMEAFields = errors.New("nmea: too few fields")
	// errNMEACoordinate reports a coordinate field that is not ddmm.mmmm.
	errNMEACoordinate = errors.New("nmea: malformed coordinate")
)

// GPSFix is one validated GNSS fix, already merged from the sentences that
// describe it. Every field is in the units the field's name states, so a reader
// never has to remember which sentence carried what.
type GPSFix struct {
	// Valid is true when the receiver reports a live fix: an 'A' status in the
	// recommended-minimum sentence, or a non-zero fix quality in the
	// fix-quality sentence.
	Valid bool
	// Lat is the WGS-84 latitude in signed decimal degrees, -90 to +90.
	Lat float64
	// Lng is the WGS-84 longitude in signed decimal degrees, -180 to +180.
	Lng float64
	// AltitudeM is the antenna altitude above mean sea level in meters.
	AltitudeM float64
	// HasAltitude reports that AltitudeM was measured by the receiver rather
	// than defaulting to zero. A position nobody measured the height of — a
	// static fix, or a sentence that omitted the field — must never be
	// reported as sea level.
	HasAltitude bool
	// GeoidSepM is the geoidal separation in meters: the height of the
	// ellipsoid above mean sea level at this position, which a survey-grade
	// reader subtracts to move between the two datums.
	GeoidSepM float64
	// SpeedKnots is the speed over ground in knots.
	SpeedKnots float64
	// CourseDeg is the true track angle in degrees, 0 to 360.
	CourseDeg float64
	// Satellites is how many satellites the receiver used for the fix.
	Satellites int
	// HDOP is the horizontal dilution of precision: lower is better, and about
	// 1.0 is a good open-sky fix.
	HDOP float64
	// FixQuality is the fix-quality indicator: 0 is no fix, 1 is a standalone
	// GPS fix, 2 is differential, and 4 and 5 are the RTK fixed and float
	// solutions.
	FixQuality int
	// TimeUTC is the timestamp the receiver reported, in UTC. It is the zero
	// time until a sentence carrying a time has been seen.
	TimeUTC time.Time
}

// Position returns the fix as the navigation layer's coordinate type.
func (f GPSFix) Position() LatLng {
	return LatLng{Lat: f.Lat, Lng: f.Lng}
}

// nmeaSentence is one validated NMEA sentence: its talker, its type, and its
// comma-separated fields with the talker+type still in field zero.
type nmeaSentence struct {
	// talker is the two-character talker id, like GN for a multi-constellation
	// receiver or GP for a GPS-only one.
	talker string
	// kind is the three-character sentence type, like RMC or GGA.
	kind string
	// fields is the sentence body split on commas, field zero included.
	fields []string
}

// GPSReader is a thread-safe streaming reader over one NMEA source, such as a
// serial GNSS receiver, a Unix FIFO, or a file. It keeps the merged state of
// the sentences it has accepted and hands it out as one GPSFix.
//
// A reader built with a nil source is the static provider: it holds only the
// fix SetFix injected, which is what a headless node and a unit test use.
type GPSReader struct {
	// src is the NMEA byte stream, nil for a static provider.
	src io.Reader
	// now is the clock, injected so a test does not depend on the wall clock
	// to fill in a sentence that omitted its date.
	now func() time.Time

	mu sync.Mutex
	// fix is the merged state every reader sees.
	fix GPSFix
	// lastDate is the UTC date of the most recent dated sentence, which is how
	// a fix-quality sentence with no date gets one.
	lastDate time.Time
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

// NewGPSReader builds a reader over src. A nil src builds a static provider,
// which reports only what SetFix was given.
func NewGPSReader(src io.Reader) *GPSReader {
	return &GPSReader{src: src, now: time.Now}
}

// LastFix returns the merged state of the sentences accepted so far. It is safe
// to call from any goroutine while a scan is running.
func (g *GPSReader) LastFix() GPSFix {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fix
}

// SetFix replaces the whole fix. It is how a headless deployment injects a
// known position and how a test mocks one, and it is the only way a static
// provider is ever populated.
func (g *GPSReader) SetFix(fix GPSFix) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fix = fix
	if !fix.TimeUTC.IsZero() {
		g.lastDate = utcMidnight(fix.TimeUTC)
	}
}

// Start launches the scan in the background and returns at once. The goroutine
// ends when src reports end of stream, when it fails, or when ctx is canceled,
// and Close waits for it, so nothing is left running past a shutdown.
func (g *GPSReader) Start(ctx context.Context) {
	if g.src == nil {
		return
	}
	g.wg.Go(func() {
		_ = g.Run(ctx)
	})
}

// Run scans src until it ends, fails, or ctx is canceled. It blocks, which is
// what a caller that owns its own goroutine wants, and it returns nil for an
// ordinary end of stream.
func (g *GPSReader) Run(ctx context.Context) error {
	if g.src == nil {
		return nil
	}
	scanner := bufio.NewScanner(g.src)
	scanner.Buffer(make([]byte, 0, 128), nmeaMaxLineBytes)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		g.consume(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil || g.isClosed() {
			return nil
		}
		return err
	}
	return nil
}

// Close stops the scan and releases the source. It is idempotent, and it waits
// for a scan goroutine Start launched, so the reader owns no goroutine after it
// returns.
func (g *GPSReader) Close() error {
	if g.src != nil {
		g.closeOnce.Do(func() {
			g.mu.Lock()
			g.closed = true
			g.mu.Unlock()
			if closer, ok := g.src.(io.Closer); ok {
				g.closeErr = closer.Close()
			}
		})
	}
	g.wg.Wait()
	return g.closeErr
}

// isClosed reports whether Close has run.
func (g *GPSReader) isClosed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// consume parses one line and merges it. An unparseable line is dropped without
// a word: a serial GNSS line is noisy by nature, and the useful signal is the
// next sentence, not a log entry about the last one.
func (g *GPSReader) consume(line string) {
	parsed, err := parseNMEALine(line)
	if err != nil {
		return
	}
	switch parsed.kind {
	case "RMC":
		g.applyRMC(parsed.fields)
	case "GGA":
		g.applyGGA(parsed.fields)
	}
}

// parseNMEALine validates one sentence and splits it. The checksum is verified
// byte-for-byte against the XOR of everything between the starter and the '*',
// because that is the only integrity check the format offers.
func parseNMEALine(line string) (nmeaSentence, error) {
	text := strings.TrimSpace(line)
	if len(text) < len("$GNRMC")+1+nmeaChecksumLen {
		return nmeaSentence{}, errNMEATooShort
	}
	if text[0] != '$' && text[0] != '!' {
		return nmeaSentence{}, errNMEAStart
	}
	star := strings.LastIndexByte(text, '*')
	if star < 0 || len(text)-star-1 != nmeaChecksumLen {
		return nmeaSentence{}, errNMEAChecksum
	}
	body := text[1:star]
	want, err := strconv.ParseUint(text[star+1:], 16, 8)
	if err != nil || nmeaXOR(body) != byte(want) {
		return nmeaSentence{}, errNMEAChecksum
	}
	fields := strings.Split(body, ",")
	if len(fields) < 2 {
		return nmeaSentence{}, errNMEAFields
	}
	tag := fields[0]
	if len(tag) < nmeaTalkerLen+3 {
		return nmeaSentence{}, errNMEAFields
	}
	return nmeaSentence{
		talker: strings.ToUpper(tag[:len(tag)-3]),
		kind:   strings.ToUpper(tag[len(tag)-3:]),
		fields: fields,
	}, nil
}

// nmeaXOR is the NMEA checksum: the exclusive-or of every byte of the sentence
// body, which is everything between the starter and the '*'.
func nmeaXOR(body string) byte {
	var sum byte
	for i := range len(body) {
		sum ^= body[i]
	}
	return sum
}

// applyRMC merges one recommended-minimum position sentence: time, date,
// validity, position, speed, and course.
func (g *GPSReader) applyRMC(fields []string) {
	if len(fields) < 10 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if day, ok := parseNMEADate(fields[9]); ok {
		g.lastDate = day
	}
	if at, ok := parseNMEATime(fields[1], g.fallbackDateLocked()); ok {
		g.fix.TimeUTC = at
	}
	if lat, lng, ok := parseNMEAPosition(fields[3], fields[4], fields[5], fields[6]); ok {
		g.fix.Lat, g.fix.Lng = lat, lng
	}
	if speed, ok := parseNMEAFloat(fields[7]); ok {
		g.fix.SpeedKnots = speed
	}
	if course, ok := parseNMEAFloat(fields[8]); ok {
		g.fix.CourseDeg = course
	}
	g.fix.Valid = strings.EqualFold(strings.TrimSpace(fields[2]), "A") && g.havePositionLocked()
}

// applyGGA merges one fix-quality sentence: position, fix quality, satellites,
// HDOP, altitude, and the geoidal separation.
func (g *GPSReader) applyGGA(fields []string) {
	if len(fields) < 10 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if at, ok := parseNMEATime(fields[1], g.fallbackDateLocked()); ok {
		g.fix.TimeUTC = at
	}
	if lat, lng, ok := parseNMEAPosition(fields[2], fields[3], fields[4], fields[5]); ok {
		g.fix.Lat, g.fix.Lng = lat, lng
	}
	quality, _ := strconv.Atoi(strings.TrimSpace(fields[6]))
	g.fix.FixQuality = quality
	if sats, err := strconv.Atoi(strings.TrimSpace(fields[7])); err == nil {
		g.fix.Satellites = sats
	}
	if hdop, ok := parseNMEAFloat(fields[8]); ok {
		g.fix.HDOP = hdop
	}
	if altitude, ok := parseNMEAFloat(fields[9]); ok {
		g.fix.AltitudeM = altitude
		g.fix.HasAltitude = true
	}
	if len(fields) > 11 {
		if geoid, ok := parseNMEAFloat(fields[11]); ok {
			g.fix.GeoidSepM = geoid
		}
	}
	g.fix.Valid = quality > 0 && g.havePositionLocked()
}

// fallbackDateLocked is the UTC day a sentence with no date of its own belongs
// to: the last dated sentence's day, and otherwise today.
func (g *GPSReader) fallbackDateLocked() time.Time {
	if !g.lastDate.IsZero() {
		return g.lastDate
	}
	return utcMidnight(g.now())
}

// havePositionLocked reports whether a plausible position has been merged. An
// empty coordinate field never parses, so this is the range check alone; the
// equator and the prime meridian are real places and stay valid.
func (g *GPSReader) havePositionLocked() bool {
	return g.fix.Lat >= -90 && g.fix.Lat <= 90 && g.fix.Lng >= -180 && g.fix.Lng <= 180
}

// parseNMEAPosition converts the four position fields of a sentence into signed
// decimal degrees, rejecting anything outside the legal range.
func parseNMEAPosition(latText, latHemi, lngText, lngHemi string) (float64, float64, bool) {
	lat, err := parseNMEADegrees(latText)
	if err != nil {
		return 0, 0, false
	}
	lng, err := parseNMEADegrees(lngText)
	if err != nil {
		return 0, 0, false
	}
	if strings.EqualFold(strings.TrimSpace(latHemi), "S") {
		lat = -lat
	} else if !strings.EqualFold(strings.TrimSpace(latHemi), "N") {
		return 0, 0, false
	}
	if strings.EqualFold(strings.TrimSpace(lngHemi), "W") {
		lng = -lng
	} else if !strings.EqualFold(strings.TrimSpace(lngHemi), "E") {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

// parseNMEADegrees converts a ddmm.mmmm or dddmm.mmmm field into decimal
// degrees. The integer part must be the four digits of a latitude or the five
// of a longitude, and the minutes must fall in 0..60: a field that fails either
// test is rejected rather than wrapped, because a wrapped minute is a position
// tens of kilometers from the one the receiver meant.
func parseNMEADegrees(text string) (float64, error) {
	trimmed := strings.TrimSpace(text)
	dot := strings.IndexByte(trimmed, '.')
	if dot != 4 && dot != 5 {
		return 0, errNMEACoordinate
	}
	for i := range dot {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			return 0, errNMEACoordinate
		}
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, errNMEACoordinate
	}
	degrees := math.Trunc(value / 100)
	minutes := value - degrees*100
	if minutes < 0 || minutes >= nmeaMinutesPerDegree {
		return 0, errNMEACoordinate
	}
	return degrees + minutes/nmeaMinutesPerDegree, nil
}

// parseNMEAFloat reads an optional numeric field. An empty field is absent, not
// an error: it is how a receiver says it has no speed or no course to report.
func parseNMEAFloat(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// parseNMEATime turns an hhmmss.sss time field into an absolute instant on the
// given UTC day.
func parseNMEATime(text string, day time.Time) (time.Time, bool) {
	seconds, ok := parseNMEAFloat(text)
	if !ok || seconds < 0 {
		return time.Time{}, false
	}
	whole := int(seconds)
	nanos := int(math.Round((seconds - float64(whole)) * float64(time.Second)))
	if nanos >= int(time.Second) {
		nanos -= int(time.Second)
		whole++
	}
	hour := whole / 10000
	minute := (whole / 100) % 100
	second := whole % 100
	if hour > 23 || minute > 59 || second > 60 {
		return time.Time{}, false
	}
	day = utcMidnight(day)
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute +
		time.Duration(second)*time.Second + time.Duration(nanos)), true
}

// parseNMEADate turns a ddmmyy date field into the UTC midnight that starts that
// day. Two-digit years are read as the 21st century, which is the only reading
// that makes sense for a device built now.
func parseNMEADate(text string) (time.Time, bool) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) != 6 {
		return time.Time{}, false
	}
	day, err1 := strconv.Atoi(trimmed[0:2])
	month, err2 := strconv.Atoi(trimmed[2:4])
	year, err3 := strconv.Atoi(trimmed[4:6])
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, false
	}
	if day < 1 || day > 31 || month < 1 || month > 12 {
		return time.Time{}, false
	}
	return time.Date(nmeaCenturyBase+year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
}

// openGPS builds the GNSS source the operator configured, or nil when none is.
//
// A configured device is opened read-only and scanned in the background: a GNSS
// receiver is a character device that emits sentences forever, so it is read
// the same way a file is, and Phase 0 relies on the operating system's own
// configuration of the port's line speed. A configured static fix is held as
// the whole answer, which is what a headless node and a rehearsing operator
// use.
//
// The caller owns the returned reader and must Close it: Close stops the scan
// goroutine and releases the device, so no read is left running past a
// shutdown.
func openGPS(cfg *BotConfig) (*GPSReader, error) {
	if cfg == nil {
		return nil, nil
	}
	if port := strings.TrimSpace(cfg.GPSPort); port != "" {
		file, err := os.OpenFile(port, os.O_RDONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("gps: could not open %v: %w", port, err)
		}
		reader := NewGPSReader(file)
		reader.Start(context.Background())
		return reader, nil
	}
	if text := strings.TrimSpace(cfg.GPSFix); text != "" {
		point, err := ParseLocation(text)
		if err != nil {
			return nil, fmt.Errorf("gps_fix %q: %w", text, err)
		}
		reader := NewGPSReader(nil)
		reader.SetFix(GPSFix{Valid: true, Lat: point.Lat, Lng: point.Lng, FixQuality: 1})
		return reader, nil
	}
	return nil, nil
}
