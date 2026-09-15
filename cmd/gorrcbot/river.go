// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the river-gauge command: the stage and the discharge at a
// stream gauge, how fast the water is rising, and whether that is about to
// matter.
//
// Fording a river is one of the leading causes of wilderness deaths, and the
// mistake is almost always the same: the water looks passable because the
// crossing is judged by eye rather than by the gauge a mile upstream. The same
// is true downstream of a storm, where a bridge goes under after the rain has
// already stopped. So the answer reports three things together — the stage, the
// trend, and the flood category — because any one of them alone is misleading.
//
// The stages come from the national stream-gauge service; the flood thresholds
// are the river-forecast center's own categories, from its optional companion
// provider. When no threshold provider is configured the command reports the
// stage and says plainly that it has nothing to compare it against.

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// River command wordings and bounds.
const (
	// riverUsage is the usage line for the command.
	riverUsage = "river <gauge_id>"
	// riverNotConfiguredLine is the answer when the operator has set no
	// provider.
	riverNotConfiguredLine = "river is not configured: set river_url in config.toml to enable it"
	// riverMisconfiguredLine is the answer when river_url itself is unusable.
	riverMisconfiguredLine = "river is misconfigured: the operator must fix river_url"
	// riverGaugeRejectedLine is the answer to a gauge id outside the allowlist.
	// It never quotes the request back.
	riverGaugeRejectedLine = "river: give a USGS gauge id, like 01646500 (Potomac at Little Falls)"
	// riverFailedLine is the answer when the provider cannot be reached or its
	// answer carries no reading.
	riverFailedLine = "river lookup failed (network error). Try again later."
	// riverStageParameter and riverFlowParameter are the two published
	// parameters the command reads.
	riverStageParameter = "00065"
	riverFlowParameter  = "00060"
	// riverTrendWindow is how far back the trend is measured, which is the span
	// a rise on a small river shows up in.
	riverTrendWindow = 3 * time.Hour
	// maxRiverBodyBytes bounds the provider response the bot will read. A day
	// of five-minute values is well inside it.
	maxRiverBodyBytes = 256 << 10
)

// RiverReading is the current state of one stream gauge.
type RiverReading struct {
	// SiteID is the gauge's published identifier.
	SiteID string
	// SiteName is the gauge's published name.
	SiteName string
	// StageFt is the gage height in feet, and StageAt is when it was measured.
	StageFt  float64
	StageAt  time.Time
	HasStage bool
	// FlowCFS is the discharge in cubic feet per second.
	FlowCFS float64
	HasFlow bool
	// PreviousFt and PreviousAt are the stage measured about three hours
	// earlier, which the trend is computed from.
	PreviousFt  float64
	PreviousAt  time.Time
	HasPrevious bool
}

// RiverThresholds are the flood categories a river-forecast center publishes
// for a gauge. A category the center does not define is left absent rather than
// treated as zero.
type RiverThresholds struct {
	// Name is the center's own name for the gauge, when it publishes one.
	Name string
	// Action, Minor, Moderate, and Major are the stages of each category, in
	// feet.
	Action      float64
	Minor       float64
	Moderate    float64
	Major       float64
	HasAction   bool
	HasMinor    bool
	HasModerate bool
	HasMajor    bool
}

// ParseRiverGauge decodes the instantaneous-values answer. A gauge that
// publishes only one of the two parameters is read for the one it publishes.
func ParseRiverGauge(body string) (RiverReading, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return RiverReading{}, fmt.Errorf("river: the provider answered nothing")
	}
	var response struct {
		Value struct {
			TimeSeries []struct {
				SourceInfo struct {
					SiteName string `json:"siteName"`
					SiteCode []struct {
						Value string `json:"value"`
					} `json:"siteCode"`
				} `json:"sourceInfo"`
				Variable struct {
					VariableCode []struct {
						Value string `json:"value"`
					} `json:"variableCode"`
				} `json:"variable"`
				Values []struct {
					Value []struct {
						Value    string `json:"value"`
						DateTime string `json:"dateTime"`
					} `json:"value"`
				} `json:"values"`
			} `json:"timeSeries"`
		} `json:"value"`
	}
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return RiverReading{}, fmt.Errorf("river: decoding the provider answer: %w", err)
	}

	reading := RiverReading{}
	for _, series := range response.Value.TimeSeries {
		if reading.SiteID == "" && len(series.SourceInfo.SiteCode) > 0 {
			reading.SiteID = series.SourceInfo.SiteCode[0].Value
		}
		if reading.SiteName == "" {
			reading.SiteName = series.SourceInfo.SiteName
		}
		if len(series.Variable.VariableCode) == 0 || len(series.Values) == 0 {
			continue
		}
		points := riverPoints(series.Values[0].Value)
		if len(points) == 0 {
			continue
		}
		switch series.Variable.VariableCode[0].Value {
		case riverStageParameter:
			latest := points[len(points)-1]
			reading.StageFt, reading.StageAt, reading.HasStage = latest.value, latest.at, true
			if previous, ok := riverPointBefore(points, latest.at.Add(-riverTrendWindow)); ok {
				reading.PreviousFt, reading.PreviousAt, reading.HasPrevious = previous.value, previous.at, true
			}
		case riverFlowParameter:
			latest := points[len(points)-1]
			reading.FlowCFS, reading.HasFlow = latest.value, true
		}
	}
	if !reading.HasStage && !reading.HasFlow {
		return RiverReading{}, fmt.Errorf("river: the provider answer carried no stage or flow")
	}
	return reading, nil
}

// riverPoint is one measured value at one instant.
type riverPoint struct {
	at    time.Time
	value float64
}

// riverPoints decodes a series' points, dropping anything the provider left
// blank (a gauge that is out of service publishes an empty value) and sorting
// them oldest first.
func riverPoints(raw []struct {
	Value    string `json:"value"`
	DateTime string `json:"dateTime"`
}) []riverPoint {
	points := make([]riverPoint, 0, len(raw))
	for _, point := range raw {
		value, err := parseFloatValue(point.Value)
		if err != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(point.DateTime))
		if err != nil {
			continue
		}
		points = append(points, riverPoint{at: at.UTC(), value: value})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].at.Before(points[j].at) })
	return points
}

// riverPointBefore returns the newest point at or before an instant, which is
// what a trend measured over a fixed window needs.
func riverPointBefore(points []riverPoint, at time.Time) (riverPoint, bool) {
	var found riverPoint
	ok := false
	for _, point := range points {
		if point.at.After(at) {
			break
		}
		found, ok = point, true
	}
	return found, ok
}

// ParseRiverFloodThresholds decodes the flood categories a river-forecast
// center publishes. A category the center marks as undefined is left absent.
func ParseRiverFloodThresholds(body string) (RiverThresholds, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return RiverThresholds{}, fmt.Errorf("river: the threshold provider answered nothing")
	}
	var response struct {
		Name  string `json:"name"`
		Flood *struct {
			Categories map[string]struct {
				Stage float64 `json:"stage"`
			} `json:"categories"`
		} `json:"flood"`
	}
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return RiverThresholds{}, fmt.Errorf("river: decoding the threshold answer: %w", err)
	}
	if response.Flood == nil || len(response.Flood.Categories) == 0 {
		return RiverThresholds{}, fmt.Errorf("river: the threshold answer carried no categories")
	}
	thresholds := RiverThresholds{Name: strings.TrimSpace(response.Name)}
	for name, category := range response.Flood.Categories {
		// A category the center does not define is published as a large
		// negative number, which is not a stage anything can reach.
		if category.Stage < 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "action":
			thresholds.Action, thresholds.HasAction = category.Stage, true
		case "minor", "flood":
			thresholds.Minor, thresholds.HasMinor = category.Stage, true
		case "moderate":
			thresholds.Moderate, thresholds.HasModerate = category.Stage, true
		case "major":
			thresholds.Major, thresholds.HasMajor = category.Stage, true
		}
	}
	if !thresholds.HasAction && !thresholds.HasMinor && !thresholds.HasModerate && !thresholds.HasMajor {
		return RiverThresholds{}, fmt.Errorf("river: the threshold answer carried no usable category")
	}
	return thresholds, nil
}

// RiverFloodStatus names the flood category a stage falls in.
func RiverFloodStatus(stage float64, thresholds RiverThresholds) string {
	switch {
	case thresholds.HasMajor && stage >= thresholds.Major:
		return "MAJOR FLOOD"
	case thresholds.HasModerate && stage >= thresholds.Moderate:
		return "MODERATE FLOOD"
	case thresholds.HasMinor && stage >= thresholds.Minor:
		return "FLOOD"
	case thresholds.HasAction && stage >= thresholds.Action:
		return "ACTION STAGE"
	default:
		return "NORMAL / NO FLOOD"
	}
}

// runRiver answers with the current state of one gauge.
func (c *commandContext) runRiver() []string {
	cfg := c.reg.config()
	if cfg == nil || strings.TrimSpace(cfg.RiverURL) == "" {
		return []string{riverNotConfiguredLine}
	}
	gauge, err := riverGauge(c.Args)
	if err != nil {
		return []string{riverGaugeRejectedLine}
	}
	fetchURL, err := providerURL(strings.TrimSpace(cfg.RiverURL), gauge)
	if err != nil {
		logf("river: %v", err)
		return []string{riverMisconfiguredLine}
	}
	lines, err := c.reg.providerAnswer(fetchURL, c.now(), maxRiverBodyBytes, func(body []byte) ([]string, error) {
		reading, err := ParseRiverGauge(string(body))
		if err != nil {
			return nil, err
		}
		if reading.SiteID == "" {
			reading.SiteID = gauge
		}
		thresholds, hasThresholds := c.riverThresholds(gauge)
		if hasThresholds && thresholds.Name != "" {
			reading.SiteName = thresholds.Name
		}
		return renderRiverAnswer(reading, thresholds, hasThresholds, c.now()), nil
	})
	if err != nil {
		logf("river: lookup for %v failed: %v", gauge, err)
		return []string{riverFailedLine}
	}
	return lines
}

// riverThresholds asks the optional threshold provider for a gauge's flood
// categories. Every failure is a missing comparison rather than a failed
// answer: the stage and the flow are still worth reporting.
func (c *commandContext) riverThresholds(gauge string) (RiverThresholds, bool) {
	cfg := c.reg.config()
	if cfg == nil || strings.TrimSpace(cfg.RiverFloodURL) == "" {
		return RiverThresholds{}, false
	}
	fetchURL, err := providerURL(strings.TrimSpace(cfg.RiverFloodURL), gauge)
	if err != nil {
		logf("river: %v", err)
		return RiverThresholds{}, false
	}
	body, err := c.reg.fetchBounded(fetchURL, maxRiverBodyBytes)
	if err != nil {
		logf("river: threshold lookup for %v failed: %v", gauge, err)
		return RiverThresholds{}, false
	}
	thresholds, err := ParseRiverFloodThresholds(body)
	if err != nil {
		logf("river: %v", err)
		return RiverThresholds{}, false
	}
	return thresholds, true
}

// riverGauge validates a USGS site number: the eight-to-fifteen digit
// identifier the service uses.
func riverGauge(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 8 || len(trimmed) > 15 {
		return "", fmt.Errorf("river: %q is not a gauge id", text)
	}
	for i := range len(trimmed) {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			return "", fmt.Errorf("river: %q is not a gauge id", text)
		}
	}
	return trimmed, nil
}

// renderRiverAnswer renders the three-line gauge report.
func renderRiverAnswer(reading RiverReading, thresholds RiverThresholds, hasThresholds bool, now time.Time) []string {
	name := titleizeSiteName(reading.SiteName)
	if name == "" {
		name = "Gauge " + reading.SiteID
	}
	header := fmt.Sprintf("%v (%v)", name, reading.SiteID)
	if reading.HasStage {
		header += " " + formatAge(now.Sub(reading.StageAt)) + " ago:"
	} else {
		header += ":"
	}

	parts := make([]string, 0, 3)
	if reading.HasStage {
		stage := fmt.Sprintf("Stage: %.2f ft", reading.StageFt)
		if hasThresholds && thresholds.HasMinor {
			stage += fmt.Sprintf(" (Flood Stage: %.1f ft)", thresholds.Minor)
		}
		parts = append(parts, stage)
	}
	if reading.HasFlow {
		parts = append(parts, fmt.Sprintf("Flow: %v cfs", formatThousands(reading.FlowCFS)))
	}
	parts = append(parts, "Trend: "+riverTrend(reading))

	status := "Status: unknown (no flood thresholds: set river_flood_url in config.toml)"
	if hasThresholds && reading.HasStage {
		status = "Status: " + RiverFloodStatus(reading.StageFt, thresholds)
		if thresholds.HasAction {
			status += fmt.Sprintf(" (Action Stage at %.1f ft)", thresholds.Action)
		}
	}
	return []string{header, strings.Join(parts, " | "), status}
}

// riverTrend renders how the stage has moved over the trend window.
func riverTrend(reading RiverReading) string {
	if !reading.HasStage || !reading.HasPrevious {
		return "unknown (the provider returned no history)"
	}
	change := reading.StageFt - reading.PreviousFt
	hours := reading.StageAt.Sub(reading.PreviousAt).Hours()
	if math.Abs(change) < 0.05 {
		return fmt.Sprintf("Steady (%.0fh)", hours)
	}
	direction := "Rising"
	if change < 0 {
		direction = "Falling"
	}
	return fmt.Sprintf("%v (%+.2f ft/%.0fh)", direction, change, hours)
}

// formatThousands renders a whole number with its thousands separated, which is
// how a discharge is read off a gauge report.
func formatThousands(value float64) string {
	rounded := int64(math.Round(value))
	sign := ""
	if rounded < 0 {
		sign, rounded = "-", -rounded
	}
	digits := fmt.Sprintf("%v", rounded)
	var b strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return sign + b.String()
}

// titleizeSiteName renders a gauge name the service publishes in capitals as
// something readable. A name that is already mixed case is left alone, and the
// two-letter words a place name carries ("DC", "MO") keep the case they have,
// because they are abbreviations rather than words.
func titleizeSiteName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if trimmed != strings.ToUpper(trimmed) {
		// The provider already published a readable name.
		return trimmed
	}
	words := strings.Fields(trimmed)
	for i, word := range words {
		letters := strings.Trim(word, ".,;:()")
		if stateCodes[letters] || allCapsAbbreviations[letters] {
			continue
		}
		lower := strings.ToLower(word)
		words[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(words, " ")
}

// allCapsAbbreviations are the all-capitals words a gauge name carries that are
// already written the way they should be.
var allCapsAbbreviations = map[string]bool{
	"US": true, "USA": true, "USGS": true, "NOAA": true, "NWS": true,
}

// stateCodes are the two-letter state and territory codes a gauge name ends
// with. They keep their case; an ordinary two-letter word does not.
var stateCodes = map[string]bool{
	"AL": true, "AK": true, "AZ": true, "AR": true, "CA": true, "CO": true,
	"CT": true, "DE": true, "DC": true, "FL": true, "GA": true, "HI": true,
	"ID": true, "IL": true, "IN": true, "IA": true, "KS": true, "KY": true,
	"LA": true, "ME": true, "MD": true, "MA": true, "MI": true, "MN": true,
	"MS": true, "MO": true, "MT": true, "NE": true, "NV": true, "NH": true,
	"NJ": true, "NM": true, "NY": true, "NC": true, "ND": true, "OH": true,
	"OK": true, "OR": true, "PA": true, "RI": true, "SC": true, "SD": true,
	"TN": true, "TX": true, "UT": true, "VT": true, "VA": true, "WA": true,
	"WV": true, "WI": true, "WY": true,
	"PR": true, "VI": true, "GU": true, "AS": true, "MP": true,
}
