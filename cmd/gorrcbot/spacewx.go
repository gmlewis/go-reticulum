// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the space-weather command: the solar flux, the sunspot
// number, the planetary K-index, and the geomagnetic storm scale, plus the HF
// band outlook that follows from them.
//
// The question this answers is a diagnostic one: when an HF link that worked an
// hour ago stops working, the operator needs to know whether the ionosphere
// went away or the radio did. A K-index of six with a G2 storm says the band is
// gone and there is nothing to fix; a K-index of zero says the problem is local
// and worth climbing the mast for.
//
// The reading comes from an optional provider URL, cached for an hour, because
// indices are published every few minutes and a mesh node has better things to
// do with a scarce link than re-fetch them. When no provider is configured, or
// the link is down, the last reading is still reported with its age, so the
// answer degrades to "here is what we knew" rather than to silence. An operator
// can also enter a reading by hand from a voice net.

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Space-weather wordings and bounds.
const (
	// spacewxUsage is the usage line for the command.
	spacewxUsage = "spacewx [set sfi=N ssn=N kp=N]"
	// spacewxNotConfiguredLine is the answer when the operator has set no
	// provider and no reading has ever been seen.
	spacewxNotConfiguredLine = "spacewx is not configured: set space_weather_url in config.toml to enable it, or enter a reading with spacewx set"
	// spacewxMisconfiguredLine is the answer when space_weather_url itself is
	// unusable. It never quotes the template, which may name a private host or
	// carry a key.
	spacewxMisconfiguredLine = "spacewx is misconfigured: the operator must fix space_weather_url"
	// spacewxFailedLine is the answer when the provider cannot be reached and
	// nothing has been cached.
	spacewxFailedLine = "space weather lookup failed (network error). Try again later."
	// spacewxCacheTTL is how long a fetched reading is reused. The indices are
	// published every few minutes and change slowly.
	spacewxCacheTTL = time.Hour
	// maxSpaceWeatherBodyBytes bounds the provider response the bot will read.
	maxSpaceWeatherBodyBytes = 256 << 10
)

// SpaceWeatherReading is one observation of the space-weather indices.
type SpaceWeatherReading struct {
	// SFI is the solar flux index at 10.7 cm.
	SFI float64
	// SSN is the sunspot number.
	SSN float64
	// Kp is the planetary K-index, 0 to 9.
	Kp float64
	// At is when the reading was observed or entered.
	At time.Time
	// Manual reports that an operator typed the reading in rather than a
	// provider serving it.
	Manual bool
}

// spaceWeatherCache holds the most recent reading, fetched or manual.
type spaceWeatherCache struct {
	mu      sync.Mutex
	reading SpaceWeatherReading
	have    bool
}

// get returns the cached reading and its age, if one has ever been stored.
func (c *spaceWeatherCache) get() (SpaceWeatherReading, bool) {
	if c == nil {
		return SpaceWeatherReading{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reading, c.have
}

// put stores a reading.
func (c *spaceWeatherCache) put(reading SpaceWeatherReading) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reading = reading
	c.have = true
}

// fresh reports whether a reading is young enough to reuse without asking the
// provider again.
func spaceWeatherFresh(reading SpaceWeatherReading, now time.Time, ttl time.Duration) bool {
	return !reading.At.IsZero() && now.Sub(reading.At) < ttl
}

// runSpacewx answers with the current space weather, fetching it if the cache
// is cold and a provider is configured.
func (c *commandContext) runSpacewx() []string {
	if sub, rest := splitCommandLine(c.Args); strings.EqualFold(sub, "set") {
		return c.runSpacewxSet(rest)
	}
	if strings.TrimSpace(c.Args) != "" {
		return []string{"Usage: " + spacewxUsage}
	}
	cfg := c.reg.config()
	if cfg == nil {
		return []string{spacewxNotConfiguredLine}
	}

	now := c.now()
	cached, haveCached := c.reg.spaceWeather.get()
	if haveCached && spaceWeatherFresh(cached, now, spacewxCacheTTL) {
		return []string{cached.line(now)}
	}

	template := strings.TrimSpace(cfg.SpaceWeatherURL)
	if template == "" {
		if haveCached {
			// No provider, but a reading has been seen or entered: report it
			// with its age rather than pretending there is nothing.
			return []string{cached.line(now)}
		}
		return []string{spacewxNotConfiguredLine}
	}
	if err := validateProviderURL(template); err != nil {
		logf("spacewx: %v", err)
		return []string{spacewxMisconfiguredLine}
	}
	body, err := c.reg.fetchBounded(template, maxSpaceWeatherBodyBytes)
	if err != nil {
		logf("spacewx: provider lookup failed: %v", err)
		if haveCached {
			return []string{cached.line(now)}
		}
		return []string{spacewxFailedLine}
	}
	reading, err := parseSpaceWeather(body)
	if err != nil {
		logf("spacewx: could not read the provider answer: %v", err)
		if haveCached {
			return []string{cached.line(now)}
		}
		return []string{spacewxFailedLine}
	}
	reading.At = now
	c.reg.spaceWeather.put(reading)
	return []string{reading.line(now)}
}

// runSpacewxSet records an operator-entered reading, which is how a station
// that heard the numbers on a voice net gets them into the bot.
func (c *commandContext) runSpacewxSet(args string) []string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return []string{"Usage: " + spacewxUsage}
	}
	reading := SpaceWeatherReading{At: c.now(), Manual: true}
	for _, field := range fields {
		key, value, found := strings.Cut(field, "=")
		if !found {
			return []string{"Usage: " + spacewxUsage}
		}
		number, ok := parseSpaceWeatherNumber(value)
		if !ok {
			return []string{fmt.Sprintf("spacewx: %q is not a number", safeEcho(value, maxEchoNickBytes))}
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "sfi", "flux":
			reading.SFI = number
		case "ssn", "sunspots":
			reading.SSN = number
		case "kp":
			if number < 0 || number > 9 {
				return []string{"spacewx: the K-index runs from 0 to 9"}
			}
			reading.Kp = number
		default:
			return []string{fmt.Sprintf("spacewx: unknown setting %q; use sfi=, ssn=, or kp=",
				safeEcho(key, maxEchoNickBytes))}
		}
	}
	c.reg.spaceWeather.put(reading)
	return []string{reading.line(c.now())}
}

// line renders the one-line report the command answers with, including how old
// the reading is when it is not from this moment.
func (r SpaceWeatherReading) line(now time.Time) string {
	line := fmt.Sprintf("SFI: %.0f | Sunspots: %.0f | Kp-index: %.0f (%v) | Geomag: %v | HF Bands: %v",
		r.SFI, r.SSN, r.Kp, KpDescription(r.Kp), GeomagneticScale(r.Kp), hfBandOutlook(r.SFI, r.Kp))
	switch {
	case r.Manual:
		line += " (operator entry)"
	case !r.At.IsZero() && now.Sub(r.At) > time.Minute:
		line += fmt.Sprintf(" (reading from %v ago)", formatAge(now.Sub(r.At)))
	}
	return line
}

// KpDescription names the disturbance a K-index describes. The bands are the
// conventional ones: quiet below three, unsettled through four, and a named
// storm from five, which is where the geomagnetic scale starts.
func KpDescription(kp float64) string {
	switch {
	case kp < 3:
		return "Quiet"
	case kp < 5:
		return "Unsettled"
	case kp < 6:
		return "Minor storm"
	case kp < 7:
		return "Moderate storm"
	case kp < 8:
		return "Strong storm"
	case kp < 9:
		return "Severe storm"
	default:
		return "Extreme storm"
	}
}

// GeomagneticScale maps a K-index onto the NOAA G scale: G0 below storm level,
// then one step per integer from G1 at five to G5 at nine.
func GeomagneticScale(kp float64) string {
	level := min(max(int(kp)-4, 0), 5)
	names := []string{"Quiet", "Minor", "Moderate", "Strong", "Severe", "Extreme"}
	return fmt.Sprintf("G%v %v", level, names[level])
}

// hfBandOutlook is a coarse field heuristic: which HF bands are worth trying
// given the solar flux and the current disturbance. It is deliberately
// conservative, because sending an operator to a dead band costs more than
// telling them the low bands are the safe choice.
func hfBandOutlook(sfi, kp float64) string {
	switch {
	case kp >= 5:
		return "Poor on 20m-10m; 40m-30m remain workable"
	case sfi >= 200:
		return "Good across 20m-10m"
	case sfi >= 150:
		return "Fair across 20m-15m"
	case sfi >= 110:
		return "Fair across 30m-15m"
	case sfi >= 90:
		return "Poor across 20m-15m; 40m is the best band"
	default:
		return "Poor; 40m-80m only"
	}
}

// parseSpaceWeather reads the indices out of a provider's answer. Providers
// differ in shape — flat objects, nested objects, and NOAA's own array-of-arrays
// with a header row are all seen in the wild — so the parser walks the whole
// document and accepts the first value it finds under any of the names the
// indices are published under.
func parseSpaceWeather(body string) (SpaceWeatherReading, error) {
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return SpaceWeatherReading{}, fmt.Errorf("decoding the provider answer: %w", err)
	}
	found := map[string]float64{}
	collectSpaceWeather(document, found)

	reading := SpaceWeatherReading{}
	var ok bool
	if reading.SFI, ok = firstOf(found, "sfi", "solarflux", "solarfluxindex", "flux", "f107", "f107cm"); !ok {
		reading.SFI = math.NaN()
	}
	if reading.SSN, ok = firstOf(found, "ssn", "sunspotnumber", "sunspots", "sunspot"); !ok {
		reading.SSN = math.NaN()
	}
	if reading.Kp, ok = firstOf(found, "kp", "kpindex", "planetarykindex", "kpindexvalue"); !ok {
		reading.Kp = math.NaN()
	}
	if math.IsNaN(reading.SFI) && math.IsNaN(reading.SSN) && math.IsNaN(reading.Kp) {
		return SpaceWeatherReading{}, fmt.Errorf("the provider answer carries none of sfi, ssn, or kp")
	}
	// A missing index is reported as zero rather than as a hole: the line has a
	// fixed shape, and a zero is visibly wrong to an operator who knows the
	// current numbers.
	reading.SFI = nanToZero(reading.SFI)
	reading.SSN = nanToZero(reading.SSN)
	reading.Kp = nanToZero(reading.Kp)
	return reading, nil
}

// collectSpaceWeather walks a decoded JSON document and records every numeric
// value under a normalized key. An array of arrays whose first row is strings is
// treated as a header row plus data rows, which is the shape the NOAA SWPC
// product endpoints use; the newest row is the one kept.
func collectSpaceWeather(node any, found map[string]float64) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			normalized := normalizeSpaceWeatherKey(key)
			if number, ok := spaceWeatherNumber(child); ok {
				found[normalized] = number
				continue
			}
			collectSpaceWeather(child, found)
		}
	case []any:
		if header, ok := spaceWeatherHeader(value); ok {
			collectSpaceWeatherRows(value, header, found)
			return
		}
		for _, child := range value {
			collectSpaceWeather(child, found)
		}
	}
}

// spaceWeatherHeader recognizes an array-of-arrays whose first row is a row of
// column names.
func spaceWeatherHeader(rows []any) ([]string, bool) {
	if len(rows) < 2 {
		return nil, false
	}
	first, ok := rows[0].([]any)
	if !ok || len(first) == 0 {
		return nil, false
	}
	header := make([]string, len(first))
	for i, cell := range first {
		text, ok := cell.(string)
		if !ok {
			return nil, false
		}
		header[i] = normalizeSpaceWeatherKey(text)
	}
	return header, true
}

// collectSpaceWeatherRows pairs every data row with the header row and keeps the
// values from the last row, which is the newest in every published product.
func collectSpaceWeatherRows(rows []any, header []string, found map[string]float64) {
	row, ok := rows[len(rows)-1].([]any)
	if !ok {
		return
	}
	for i, cell := range row {
		if i >= len(header) {
			break
		}
		if number, ok := spaceWeatherNumber(cell); ok {
			found[header[i]] = number
		}
	}
}

// spaceWeatherNumber reads a JSON value as a number when it is one, including
// the case of a numeric string.
func spaceWeatherNumber(node any) (float64, bool) {
	switch value := node.(type) {
	case json.Number:
		number, err := value.Float64()
		return number, err == nil
	case float64:
		return value, true
	case string:
		return parseSpaceWeatherNumber(value)
	default:
		return 0, false
	}
}

// parseSpaceWeatherNumber parses a decimal number, refusing anything with
// trailing text.
func parseSpaceWeatherNumber(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	var number float64
	if _, err := fmt.Sscanf(trimmed, "%g", &number); err != nil {
		return 0, false
	}
	// Sscanf is happy with trailing garbage; require the value to account for
	// the whole string.
	if !spaceWeatherNumberIsWhole(trimmed, number) {
		return 0, false
	}
	return number, true
}

// spaceWeatherNumberIsWhole reports whether the text is exactly the rendering of
// the parsed number, which rejects "4 storms" while accepting "4" and "4.33".
func spaceWeatherNumberIsWhole(text string, number float64) bool {
	candidate := fmt.Sprintf("%g", number)
	return text == candidate || strings.TrimSuffix(text, ".0") == strings.TrimSuffix(candidate, ".0")
}

// normalizeSpaceWeatherKey reduces a published column name to lowercase letters
// and digits, so "Kp-index", "kp_index", and "Kp Index" are the same key.
func normalizeSpaceWeatherKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstOf returns the first of a set of candidate keys that is present.
func firstOf(found map[string]float64, keys ...string) (float64, bool) {
	for _, key := range keys {
		if value, ok := found[key]; ok {
			return value, true
		}
	}
	return 0, false
}

// nanToZero replaces a missing value with zero.
func nanToZero(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return value
}
