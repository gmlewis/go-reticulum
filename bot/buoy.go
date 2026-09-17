// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the marine buoy command: the real-time sea state from an
// offshore weather buoy, decoded from the compact fixed-column feed the
// National Data Buoy Center publishes.
//
// Swell period is the number that matters offshore. A six-foot sea at five
// seconds is chop a small boat can push through; the same six feet at sixteen
// seconds is groundswell that has crossed an ocean, carries its energy deep,
// and breaks on a bar without warning. The command therefore reports the
// period alongside the height and names the difference in words, because the
// height alone is the number that gets people into trouble.
//
// The feed is a few kilobytes of whitespace-delimited columns with "MM" for a
// missing sensor. The parser reads the header to learn the column order rather
// than assuming it, so a buoy that publishes fewer sensors is read correctly,
// and a missing sensor is reported as missing rather than as a zero.

package bot

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Buoy command wordings and bounds.
const (
	// buoyUsage is the usage line for the command.
	buoyUsage = "buoy <station_id>"
	// buoyCatalogName names the buoys in a discovery answer.
	buoyCatalogName = "Weather buoys"
	// buoyUsageHint is the second line of an unusable request: the three ways
	// into the catalog, which are how an asker who does not know a station id
	// gets one.
	buoyUsageHint = "search, near, and list find a station: " +
		"buoy search <query> [page] | buoy near <place|coords|pluscode> | buoy list [region|state] [page]"
	// buoyNotConfiguredLine is the answer when the operator has set no provider.
	buoyNotConfiguredLine = "buoy is not configured: set buoy_url in config.toml to enable it"
	// buoyMisconfiguredLine is the answer when buoy_url itself is unusable.
	buoyMisconfiguredLine = "buoy is misconfigured: the operator must fix buoy_url"
	// buoyStationRejectedLine is the answer to a station id outside the
	// allowlist. It never quotes the request back.
	buoyStationRejectedLine = "buoy: give a 4 to 6 character station id, like 46026, 41009, or 44013"
	// buoyFailedLine is the answer when the provider cannot be reached or its
	// answer carries no observation.
	buoyFailedLine = "buoy lookup failed (network error). Try again later."
	// buoyMissingValue is the token the provider publishes for a sensor that
	// reported nothing.
	buoyMissingValue = "MM"
	// maxBuoyBodyBytes bounds the provider response the bot will read. The feed
	// publishes a whole deployment, so the cap is the transport's own read
	// limit; the newest observations are at the top and only those are parsed.
	maxBuoyBodyBytes = maxProviderBodyBytes
	// metersPerSecondToKnots converts the feed's wind unit to the unit a
	// mariner reads.
	metersPerSecondToKnots = 1.9438444924406
	// metersToFeet converts the feed's wave unit to the unit a mariner reads.
	metersToFeet = 1 / 0.3048
)

// BuoyObservation is one decoded row of the buoy feed.
type BuoyObservation struct {
	// At is the observation time in UTC.
	At time.Time
	// WindDirectionDeg, WindSpeedKt, and WindGustKt are the wind, with the
	// speed converted from the feed's meters per second to knots.
	WindDirectionDeg float64
	WindSpeedKt      float64
	WindGustKt       float64
	HasWind          bool
	HasWindDirection bool
	HasGust          bool
	// WaveHeightFt and WaveHeightM are the significant wave height in both
	// units.
	WaveHeightFt float64
	WaveHeightM  float64
	HasWave      bool
	// DominantPeriodSec is the dominant wave period, the number that decides
	// whether the sea is groundswell or chop.
	DominantPeriodSec float64
	HasPeriod         bool
	// MeanWaveDirectionDeg is the direction the waves come from.
	MeanWaveDirectionDeg float64
	HasWaveDirection     bool
	// PressureHPa is the barometric pressure.
	PressureHPa float64
	HasPressure bool
	// WaterTempC and WaterTempF are the sea surface temperature in both units.
	WaterTempC   float64
	WaterTempF   float64
	HasWaterTemp bool
	// AirTempC is the air temperature.
	AirTempC   float64
	HasAirTemp bool
}

// BuoyReport is one buoy's current condition.
type BuoyReport struct {
	// Station is the buoy id the answer is for.
	Station string
	// Observation is the newest row in the feed.
	Observation BuoyObservation
	// Oldest is the oldest row in the feed, which is what the pressure trend is
	// measured against. It is nil when the feed carries only one row.
	Oldest *BuoyObservation
	// Rows is how many observations the feed carried.
	Rows int
}

// ParseBuoyRealtime decodes the fixed-column feed into its newest observation.
// The header row names the columns, so the positions are taken from the feed
// itself rather than assumed.
func ParseBuoyRealtime(station, body string) (BuoyReport, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	columns := []string{}
	observations := make([]BuoyObservation, 0, 24)
	for _, line := range lines {
		trimmed := strings.TrimSpace(stripEscapes(line))
		if trimmed == "" {
			continue
		}
		if header, isHeader := strings.CutPrefix(trimmed, "#"); isHeader {
			if len(columns) == 0 {
				columns = strings.Fields(header)
			}
			continue
		}
		if len(columns) == 0 {
			continue
		}
		observation, ok := parseBuoyRow(columns, trimmed)
		if ok {
			observations = append(observations, observation)
		}
	}
	if len(observations) == 0 {
		return BuoyReport{}, fmt.Errorf("buoy: the feed carried no observation")
	}
	sort.Slice(observations, func(i, j int) bool { return observations[i].At.After(observations[j].At) })
	report := BuoyReport{Station: station, Observation: observations[0], Rows: len(observations)}
	if len(observations) > 1 {
		oldest := observations[len(observations)-1]
		report.Oldest = &oldest
	}
	return report, nil
}

// parseBuoyRow decodes one whitespace-delimited row against the header.
func parseBuoyRow(columns []string, line string) (BuoyObservation, bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return BuoyObservation{}, false
	}
	value := func(name string) (string, bool) {
		for i, column := range columns {
			if column == name && i < len(fields) {
				return fields[i], true
			}
		}
		return "", false
	}
	number := func(name string) (float64, bool) {
		text, ok := value(name)
		if !ok || strings.EqualFold(text, buoyMissingValue) {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}

	year, okYear := number("YY")
	month, okMonth := number("MM")
	day, okDay := number("DD")
	hour, okHour := number("hh")
	// The feed's first header names the columns (YY MM DD hh mm); its second
	// names the units (yr mo dy hr mn), so the minute column is "mm" here.
	minute, okMinute := number("mm")
	if !okYear || !okMonth || !okDay || !okHour || !okMinute {
		return BuoyObservation{}, false
	}
	// The column is named YY, but the modern feed publishes the full year; a
	// two-digit value is still accepted, because older deployments use it.
	fullYear := int(year)
	if fullYear < 100 {
		fullYear += 2000
	}
	observation := BuoyObservation{
		At: time.Date(fullYear, time.Month(month), int(day), int(hour), int(minute), 0, 0, time.UTC),
	}
	if direction, ok := number("WDIR"); ok {
		observation.WindDirectionDeg = direction
		observation.HasWindDirection = true
	}
	speed, okSpeed := number("WSPD")
	gust, okGust := number("GST")
	if okSpeed {
		observation.WindSpeedKt = speed * metersPerSecondToKnots
		observation.HasWind = true
	}
	if okGust {
		observation.WindGustKt = gust * metersPerSecondToKnots
		observation.HasGust = true
	}
	if height, ok := number("WVHT"); ok {
		observation.WaveHeightM = height
		observation.WaveHeightFt = height * metersToFeet
		observation.HasWave = true
	}
	if period, ok := number("DPD"); ok {
		observation.DominantPeriodSec = period
		observation.HasPeriod = true
	}
	if direction, ok := number("MWD"); ok {
		observation.MeanWaveDirectionDeg = direction
		observation.HasWaveDirection = true
	}
	if pressure, ok := number("PRES"); ok {
		observation.PressureHPa = pressure
		observation.HasPressure = true
	}
	if temperature, ok := number("WTMP"); ok {
		observation.WaterTempC = temperature
		observation.WaterTempF = temperature*9/5 + 32
		observation.HasWaterTemp = true
	}
	if temperature, ok := number("ATMP"); ok {
		observation.AirTempC = temperature
		observation.HasAirTemp = true
	}
	return observation, true
}

// BuoySwellRating names what a wave period means to a small boat.
func BuoySwellRating(periodSec float64) string {
	switch {
	case periodSec >= 14:
		return "heavy groundswell"
	case periodSec >= 10:
		return "groundswell"
	case periodSec >= 6:
		return "wind swell"
	default:
		return "choppy wind swell"
	}
}

// BuoyPressureTrend describes how the pressure has moved across the feed, which
// is the earliest warning of a front arriving.
func BuoyPressureTrend(report BuoyReport) string {
	if report.Oldest == nil || !report.Observation.HasPressure || !report.Oldest.HasPressure {
		return ""
	}
	hours := report.Observation.At.Sub(report.Oldest.At).Hours()
	if hours < 0.5 {
		return ""
	}
	change := report.Observation.PressureHPa - report.Oldest.PressureHPa
	switch {
	case math.Abs(change) < 0.5:
		return "steady"
	case change > 0:
		return fmt.Sprintf("rising, %+.1f hPa over %.0fh", change, hours)
	default:
		return fmt.Sprintf("falling, %+.1f hPa over %.0fh", change, hours)
	}
}

// runBuoy answers with the current sea state at one buoy, or with a page of the
// offline buoy catalog when the request is a discovery one. Discovery is
// answered before the provider is consulted: finding the id of a buoy near a
// place is a question the reference table can answer, and it must work on a
// link that cannot reach the feed at all.
func (c *commandContext) runBuoy() []string {
	if kind, text := splitDiscovery(c.Args); kind != "" {
		return c.runBuoyDiscovery(kind, text)
	}
	template := strings.TrimSpace(c.reg.buoyURL())
	if template == "" {
		return []string{buoyNotConfiguredLine}
	}
	station, err := buoyStationID(c.Args)
	if err != nil {
		return []string{buoyStationRejectedLine, buoyUsageHint}
	}
	fetchURL, err := providerURL(template, strings.ToLower(station))
	if err != nil {
		logf("buoy: %v", err)
		return []string{buoyMisconfiguredLine}
	}
	lines, err := c.reg.providerAnswer(fetchURL, c.now(), maxBuoyBodyBytes, func(body []byte) ([]string, error) {
		report, err := ParseBuoyRealtime(station, string(body))
		if err != nil {
			return nil, err
		}
		return renderBuoyAnswer(report, c.now()), nil
	})
	if err != nil {
		logf("buoy: lookup for %v failed: %v", station, err)
		return []string{buoyFailedLine}
	}
	return lines
}

// runBuoyDiscovery answers a buoy search, near, or list request from the station
// catalog. Every answer is offline, so an asker can find an id before the
// operator has configured the feed, or while it is unreachable.
func (c *commandContext) runBuoyDiscovery(kind, text string) []string {
	q := discoveryQuery{Command: "buoy", Catalog: buoyCatalogName, Kind: kind}
	switch kind {
	case discoveryKindSearch:
		words, page := splitPageArgument(text)
		query := strings.Join(searchWords(words), " ")
		if query == "" {
			return []string{"Usage: buoy search <query> [page]", buoyUsageHint}
		}
		q.Text = query
		matches := searchCatalog(buoyCatalog, query)
		if len(matches) == 0 {
			return []string{fmt.Sprintf("No %v match %q.", buoyCatalogName, q.Text), searchHint(q)}
		}
		return c.renderCatalogPage(q, matches, page)
	case discoveryKindNear:
		return c.renderNearAnswer(q, buoyCatalog, text)
	case discoveryKindList:
		region, page := splitListArgument(text)
		entries := filterCatalogRegion(buoyCatalog, region)
		if len(entries) == 0 {
			return []string{fmt.Sprintf("No %v in %v.", buoyCatalogName, safeEcho(region, maxDiscoveryEchoBytes)), listHint(q)}
		}
		q.Text = strings.ToUpper(strings.Join(strings.Fields(region), " "))
		return c.renderCatalogPage(q, entries, page)
	default:
		return []string{"Usage: " + buoyUsage, buoyUsageHint}
	}
}

// buoyStationID validates and normalizes a station id: the four to six
// characters the feed uses.
func buoyStationID(text string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(text))
	if len(trimmed) < 4 || len(trimmed) > 6 {
		return "", fmt.Errorf("buoy: %q is not a station id", text)
	}
	for i := range len(trimmed) {
		c := trimmed[i]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		return "", fmt.Errorf("buoy: %q is not a station id", text)
	}
	return trimmed, nil
}

// renderBuoyAnswer renders the two-line sea-state report.
func renderBuoyAnswer(report BuoyReport, now time.Time) []string {
	sea := fmt.Sprintf("Buoy %v (%v ago): %v | %v",
		report.Station, formatAge(now.Sub(report.Observation.At)),
		buoyWaveSegment(report.Observation), buoyWindSegment(report.Observation))

	conditions := buoyConditionsSegment(report.Observation)
	if trend := BuoyPressureTrend(report); trend != "" {
		conditions += " (" + trend + ")"
	}
	return []string{sea, conditions}
}

// buoyWaveSegment renders the sea state: the height, the period, the direction,
// and what the period means.
func buoyWaveSegment(observation BuoyObservation) string {
	if !observation.HasWave {
		return "Wave: no data"
	}
	segment := fmt.Sprintf("Wave %.1f ft", observation.WaveHeightFt)
	if observation.HasPeriod {
		segment += fmt.Sprintf(" @ %.0fs", observation.DominantPeriodSec)
	}
	if observation.HasWaveDirection {
		segment += " " + CompassPoint(observation.MeanWaveDirectionDeg)
	}
	if observation.HasPeriod {
		segment += " (" + BuoySwellRating(observation.DominantPeriodSec) + ")"
	}
	return segment
}

// buoyWindSegment renders the wind, with the gust only when the buoy published
// one.
func buoyWindSegment(observation BuoyObservation) string {
	if !observation.HasWind {
		return "Wind: no data"
	}
	segment := fmt.Sprintf("Wind %.0f kt", observation.WindSpeedKt)
	if observation.HasGust {
		segment += fmt.Sprintf(" G %.0f kt", observation.WindGustKt)
	}
	if observation.HasWindDirection {
		segment += " " + CompassPoint(observation.WindDirectionDeg)
	}
	return segment
}

// buoyConditionsSegment renders the water temperature and the pressure, with
// each half present only when the buoy reported it.
func buoyConditionsSegment(observation BuoyObservation) string {
	parts := make([]string, 0, 2)
	if observation.HasWaterTemp {
		parts = append(parts, fmt.Sprintf("Water Temp: %.1f°F (%.1f°C)",
			observation.WaterTempF, observation.WaterTempC))
	} else {
		parts = append(parts, "Water Temp: no data")
	}
	if observation.HasPressure {
		parts = append(parts, fmt.Sprintf("Pressure: %.1f hPa", observation.PressureHPa))
	} else {
		parts = append(parts, "Pressure: no data")
	}
	return strings.Join(parts, " | ")
}

// buoyURL returns the configured buoy provider template.
func (r *registry) buoyURL() string {
	if r.bot == nil || r.bot.cfg == nil {
		return ""
	}
	return r.bot.cfg.BuoyURL
}
