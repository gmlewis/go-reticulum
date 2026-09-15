// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the aviation-weather reporter: the metar command and the
// decoder behind it. A METAR is already the most compact honest description of
// the weather at an airfield — about forty bytes of coded groups — and it is
// what a pilot, a drone operator, or a search aircraft actually uses. What it
// is not is readable at a glance, so this decoder turns one into a single line
// that names the wind, the visibility, the temperature and dewpoint, and the
// altimeter setting, in both units wherever two are in use.
//
// The raw report is the fallback: if a group does not decode, the command
// answers with the report itself rather than with a guess. A METAR is designed
// to be read by a human who knows the format, so the fallback is still useful.

package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// METAR wordings and bounds.
const (
	// metarUsage is the usage line for the command.
	metarUsage = "metar <ICAO>"
	// metarNotConfiguredLine is the answer when the operator has set no
	// provider.
	metarNotConfiguredLine = "metar is not configured: set metar_url in config.toml to enable it"
	// metarMisconfiguredLine is the answer when metar_url itself is unusable.
	// It never quotes the template, which may name a private host or carry a
	// key.
	metarMisconfiguredLine = "metar is misconfigured: the operator must fix metar_url"
	// metarPlaceRejectedLine is the answer to a station code outside the
	// allowlist. It never quotes the request back.
	metarPlaceRejectedLine = "metar: give a 4-letter ICAO station code, like KDEN, EGLL, or KJFK"
	// metarFailedLine is the answer when the provider cannot be reached.
	metarFailedLine = "metar lookup failed (network error). Try again later."
	// maxMetarBodyBytes bounds the provider response the bot will read.
	maxMetarBodyBytes = 64 << 10
	// metarRawFallbackBytes bounds the raw report the fallback line carries.
	metarRawFallbackBytes = 180
	// metarStationLength is the length of an ICAO station code.
	metarStationLength = 4
)

// METAR group patterns. The groups are fixed-width by design, which is what
// makes a METAR decodable without a parser generator.
var (
	metarTimePattern      = regexp.MustCompile(`^\d{6}Z$`)
	metarWindPattern      = regexp.MustCompile(`^(\d{3}|VRB)(\d{2,3})(?:G(\d{2,3}))?(KT|MPS)$`)
	metarVisibilitySM     = regexp.MustCompile(`^(M?\d+(?:/\d+)?)SM$`)
	metarVisibilityMeters = regexp.MustCompile(`^\d{4}$`)
	metarTempPattern      = regexp.MustCompile(`^(M?\d{2})/(M?\d{2})$`)
	metarAltimeterInHg    = regexp.MustCompile(`^A(\d{4})$`)
	metarAltimeterHPa     = regexp.MustCompile(`^Q(\d{4})$`)
	metarSkyPattern       = regexp.MustCompile(`^(FEW|SCT|BKN|OVC)(\d{3})(CB|TCU)?$`)
	metarClearPattern     = regexp.MustCompile(`^(CLR|SKC|NSC|NCD)$`)
	metarWeatherPattern   = regexp.MustCompile(`^[-+]?(VC)?(MI|PR|BC|DR|BL|SH|TS|FZ)?` +
		`(DZ|RA|SN|SG|IC|PL|GR|GS|UP|BR|FG|FU|VA|DU|SA|HZ|PY|PO|SQ|FC|SS|DS)+$`)
	metarStationPattern = regexp.MustCompile(`^[A-Z0-9]{4}$`)
)

// METARReport is a decoded METAR.
type METARReport struct {
	// Station is the reporting airfield's ICAO code.
	Station string
	// Time is the observation time group, as published.
	Time string
	// Wind is the wind rendered in words, empty when the report has none.
	Wind string
	// Visibility is the visibility group, as published.
	Visibility string
	// TemperatureC and DewpointC are the temperatures in Celsius.
	TemperatureC float64
	DewpointC    float64
	// HasTemperature reports that the report carried a temperature group.
	HasTemperature bool
	// AltimeterInHg and AltimeterHPa are the two renderings of the altimeter
	// setting.
	AltimeterInHg float64
	AltimeterHPa  float64
	// HasAltimeter reports that the report carried an altimeter group.
	HasAltimeter bool
	// Sky is the most significant cloud group, as published.
	Sky string
	// Weather is the most significant weather group, as published.
	Weather string
}

// ParseMETAR decodes one METAR into its groups. It is deliberately lenient: a
// group it does not recognize is skipped rather than failing the report,
// because a real METAR carries remarks and local codes that no general decoder
// should reject. It fails only when nothing at all was recognized.
func ParseMETAR(raw string) (METARReport, error) {
	text := strings.ToUpper(strings.TrimSpace(raw))
	if text == "" {
		return METARReport{}, fmt.Errorf("metar: the report is empty")
	}
	tokens := strings.Fields(text)
	// A report may begin with its type; the type is not a group to decode.
	if len(tokens) > 0 && (tokens[0] == "METAR" || tokens[0] == "SPECI") {
		tokens = tokens[1:]
	}
	report := METARReport{}
	// The station is part of every report and decodes to nothing on its own, so
	// only the other groups count toward "this report was understood".
	recognized := 0
	for _, token := range tokens {
		switch {
		case report.Station == "" && metarStationPattern.MatchString(token):
			report.Station = token
		case report.Time == "" && metarTimePattern.MatchString(token):
			report.Time = token
			recognized++
		case report.Wind == "" && metarWindPattern.MatchString(token):
			report.Wind = renderMetarWind(metarWindPattern.FindStringSubmatch(token))
			recognized++
		case report.Visibility == "" && (metarVisibilitySM.MatchString(token) || metarVisibilityMeters.MatchString(token)):
			report.Visibility = token
			recognized++
		case !report.HasTemperature && metarTempPattern.MatchString(token):
			parts := metarTempPattern.FindStringSubmatch(token)
			report.TemperatureC = parseMetarTemperature(parts[1])
			report.DewpointC = parseMetarTemperature(parts[2])
			report.HasTemperature = true
			recognized++
		case !report.HasAltimeter && metarAltimeterInHg.MatchString(token):
			inHg, err := strconv.ParseFloat(metarAltimeterInHg.FindStringSubmatch(token)[1], 64)
			if err == nil {
				report.AltimeterInHg = inHg / 100
				report.AltimeterHPa = math.Round(report.AltimeterInHg * 33.8639)
				report.HasAltimeter = true
				recognized++
			}
		case !report.HasAltimeter && metarAltimeterHPa.MatchString(token):
			hPa, err := strconv.ParseFloat(metarAltimeterHPa.FindStringSubmatch(token)[1], 64)
			if err == nil {
				report.AltimeterHPa = hPa
				report.AltimeterInHg = math.Round(hPa/33.8639*100) / 100
				report.HasAltimeter = true
				recognized++
			}
		case report.Sky == "" && (metarSkyPattern.MatchString(token) || metarClearPattern.MatchString(token)):
			report.Sky = token
			recognized++
		case report.Weather == "" && metarWeatherPattern.MatchString(token):
			report.Weather = token
			recognized++
		}
	}
	if recognized == 0 {
		return METARReport{}, fmt.Errorf("metar: no group of the report could be decoded")
	}
	return report, nil
}

// renderMetarWind renders a matched wind group as words. The speed and gust are
// printed as numbers rather than as the zero-padded groups the report uses,
// because "8kt" is what a person reads and "08kt" is what a teleprinter emits.
func renderMetarWind(parts []string) string {
	direction, speed, gust, unit := parts[1], parts[2], parts[3], strings.ToLower(parts[4])
	speedValue := parseMetarNumber(speed)
	gustValue := parseMetarNumber(gust)
	if direction == "VRB" {
		return fmt.Sprintf("variable %v%v%v", speedValue, unit, metarGustSuffix(gust, gustValue, unit))
	}
	if speedValue == 0 {
		return "calm"
	}
	return fmt.Sprintf("%v° %v%v%v", direction, speedValue, unit, metarGustSuffix(gust, gustValue, unit))
}

// metarGustSuffix renders the gusting part of a wind group, empty when the
// report carries no gust.
func metarGustSuffix(gust string, value int, unit string) string {
	if gust == "" {
		return ""
	}
	return fmt.Sprintf(" gusting %v%v", value, unit)
}

// parseMetarNumber parses a zero-padded group into its integer value.
func parseMetarNumber(text string) int {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0
	}
	return value
}

// parseMetarTemperature parses one "M05"-style temperature into signed Celsius.
func parseMetarTemperature(text string) float64 {
	if magnitude, negative := strings.CutPrefix(text, "M"); negative {
		value, err := strconv.ParseFloat(magnitude, 64)
		if err != nil {
			return 0
		}
		return -value
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return value
}

// Line renders the decoded report as the single readable line the command
// answers with.
func (r METARReport) Line() string {
	header := strings.TrimSpace(r.Station + " " + r.Time)
	if header == "" {
		header = "METAR"
	}
	segments := make([]string, 0, 6)
	if r.Wind != "" {
		segments = append(segments, "Wind "+r.Wind)
	}
	if r.Visibility != "" {
		segments = append(segments, "Vis "+r.Visibility)
	}
	if r.HasTemperature {
		segments = append(segments, fmt.Sprintf("Temp %.0f°C (%.0f°F) / DP %.0f°C",
			r.TemperatureC, r.TemperatureC*9/5+32, r.DewpointC))
	}
	if r.HasAltimeter {
		segments = append(segments, fmt.Sprintf("Altimeter %.2f inHg (%.0f hPa)", r.AltimeterInHg, r.AltimeterHPa))
	}
	if r.Sky != "" {
		segments = append(segments, "Sky "+r.Sky)
	}
	if r.Weather != "" {
		segments = append(segments, "Wx "+r.Weather)
	}
	if len(segments) == 0 {
		return header
	}
	return header + ": " + strings.Join(segments, " | ")
}

// metarStation validates and normalizes an ICAO station code.
func metarStation(token string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(token))
	if len(trimmed) != metarStationLength {
		return "", fmt.Errorf("metar: %q is not a station code", token)
	}
	for i := range len(trimmed) {
		c := trimmed[i]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		return "", fmt.Errorf("metar: %q is not a station code", token)
	}
	return trimmed, nil
}

// runMetar answers with the decoded report for one station.
func (c *commandContext) runMetar() []string {
	template := strings.TrimSpace(c.reg.metarURL())
	if template == "" {
		return []string{metarNotConfiguredLine}
	}
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + metarUsage}
	}
	station, err := metarStation(c.Args)
	if err != nil {
		return []string{metarPlaceRejectedLine}
	}
	fetchURL, err := providerURL(template, strings.ToLower(station))
	if err != nil {
		logf("metar: %v", err)
		return []string{metarMisconfiguredLine}
	}
	lines, err := c.reg.providerAnswer(fetchURL, c.now(), maxMetarBodyBytes, func(body []byte) ([]string, error) {
		return renderMetarAnswer(string(body), station)
	})
	if err != nil {
		logf("metar: lookup for %v failed: %v", station, err)
		return []string{metarFailedLine}
	}
	return lines
}

// renderMetarAnswer decodes the provider's answer into reply lines, falling
// back to the raw report when it does not decode.
func renderMetarAnswer(body, station string) ([]string, error) {
	raw := strings.TrimSpace(body)
	if raw == "" {
		return nil, fmt.Errorf("metar: the provider answered nothing")
	}
	// A provider may return several reports; the one for the station asked
	// about is the answer.
	for _, candidate := range metarReports(raw) {
		report, err := ParseMETAR(candidate)
		if err != nil {
			continue
		}
		if report.Station != "" && report.Station != station {
			continue
		}
		return []string{report.Line()}, nil
	}
	// Nothing decoded: the report itself is still the honest answer.
	line, err := sanitizeProviderLine(strings.ReplaceAll(raw, "\n", " "))
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, fmt.Errorf("metar: the provider answer carried no usable text")
	}
	return []string{truncateUTF8Bytes(station+" (raw): "+line, metarRawFallbackBytes)}, nil
}

// metarReports splits a provider answer into candidate reports. Many providers
// return one report per line, and one report per blank-line-separated block.
func metarReports(raw string) []string {
	blocks := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' })
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if trimmed := strings.TrimSpace(block); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// metarURL returns the configured METAR provider template.
func (r *registry) metarURL() string {
	if r.bot == nil || r.bot.cfg == nil {
		return ""
	}
	return r.bot.cfg.MetarURL
}
