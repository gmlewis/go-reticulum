// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the tide command: the high and low water predictions for a
// station, the state of the tide between them, and the spring/neap assessment
// that says whether the range is about to become extreme.
//
// The question this answers is the one that decides whether a party can cross a
// tidal flat, round a headland, or float a kayak down a shallow channel. The
// provider supplies the two events that bracket the present moment; everything
// between them comes from the rule of twelfths, which is the arithmetic every
// coastal guide teaches and which needs no water-level feed of its own. That
// matters on a link that can afford one request: one pair of predictions gives
// the depth at any moment in the six hours between them.
//
// Spring and neap tides are read from the Moon's elongation, which the bot
// computes offline. A spring tide is the one that catches people out, because
// the same crossing that was knee-deep a week ago is chest-deep under it, so
// the assessment names it rather than leaving it to be worked out.

package bot

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tide command wordings and bounds.
const (
	// tideUsage is the usage line for the command.
	tideUsage = "tide <station|coords|place> [date]"
	// tideCatalogName names the tide stations in a discovery answer.
	tideCatalogName = "Tide stations"
	// tideUsageHint is the second line of a bare tide request: the three ways
	// into the catalog, which are how an asker who does not know a station id
	// gets one.
	tideUsageHint = "search, near, and list find a station: " +
		"tide search <query> [page] | tide near <place|coords|pluscode> | tide list [state] [page]"
	// tideNotConfiguredLine is the answer when the operator has set no provider.
	tideNotConfiguredLine = "tide is not configured: set tide_url in config.toml to enable it"
	// tideMisconfiguredLine is the answer when tide_url itself is unusable. It
	// never quotes the template, which may name a private host or carry a key.
	tideMisconfiguredLine = "tide is misconfigured: the operator must fix tide_url"
	// tideFailedLine is the answer when the provider cannot be reached.
	tideFailedLine = "tide lookup failed (network error). Try again later."
	// tideDatum is the reference level and time zone the answer is stated in.
	tideDatum = "MLLW UTC"
	// tideMaxBodyBytes bounds the provider response the bot will read.
	tideMaxBodyBytes = 256 << 10
	// tideStationToken and tideDateToken are the substitution tokens the
	// template must carry.
	tideStationToken = "{place}"
	tideDateToken    = "{date}"
	// tideNearLimitKm is how far a position may be from a reference station
	// before the command refuses to name one. Beyond this the tide is not
	// meaningfully the same tide.
	tideNearLimitKm = 250.0
	// tideEventLayout is the timestamp format the provider publishes.
	tideEventLayout = "2006-01-02 15:04"
)

// tideTwelfths is the rule of twelfths: the cumulative twelfths of the range
// covered after each hour of a six-hour half-cycle. The first and last hours
// move a twelfth each, the middle hours move three.
var tideTwelfths = []float64{0, 1, 3, 6, 9, 11, 12}

// TideEvent is one high or low water.
type TideEvent struct {
	// At is when the water reached its extreme, in UTC.
	At time.Time
	// Height is the predicted height in feet above the datum.
	Height float64
	// High reports that this is a high water rather than a low water.
	High bool
}

// Kind names the event the way a tide table does.
func (e TideEvent) Kind() string {
	if e.High {
		return "High"
	}
	return "Low"
}

// TideCurrent is the state of the tide between the two events that bracket the
// present moment.
type TideCurrent struct {
	// Previous and Next are the events the estimate sits between.
	Previous TideEvent
	Next     TideEvent
	// Flooding reports that the water is rising toward the next high water.
	Flooding bool
	// Height is the estimated height now, from the rule of twelfths.
	Height float64
	// Change is how far the water has moved since the previous event, positive
	// when it has risen.
	Change float64
	// PercentToNext is how much of the range between the events is covered.
	PercentToNext float64
}

// TideDay is one day's tide at one station.
type TideDay struct {
	// Station is the station the answer is for.
	Station tideStation
	// Day is the UTC date the answer is for.
	Day time.Time
	// Events are the predicted highs and lows inside that UTC day.
	Events []TideEvent
	// Current is the state of the tide now, when now falls inside the provider's
	// window. HasCurrent reports whether it does.
	Current    TideCurrent
	HasCurrent bool
	// Spring is the spring/neap assessment.
	Spring string
}

// parseTidePredictions decodes the provider's high/low predictions. The
// provider's own error form is recognized and reported as an error, so a
// station it does not publish cannot look like an empty day.
func parseTidePredictions(body string) ([]TideEvent, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, fmt.Errorf("tide: the provider answered nothing")
	}
	var response struct {
		Predictions []struct {
			T    string `json:"t"`
			V    string `json:"v"`
			Type string `json:"type"`
		} `json:"predictions"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return nil, fmt.Errorf("tide: decoding the provider answer: %w", err)
	}
	if response.Error != nil {
		// The provider's message may name its own internals, so it goes to the
		// log rather than into the room.
		return nil, fmt.Errorf("tide: the provider reported an error: %v", response.Error.Message)
	}
	events := make([]TideEvent, 0, len(response.Predictions))
	for _, prediction := range response.Predictions {
		at, err := time.ParseInLocation(tideEventLayout, strings.TrimSpace(prediction.T), time.UTC)
		if err != nil {
			continue
		}
		height, err := parseFloatValue(prediction.V)
		if err != nil {
			continue
		}
		events = append(events, TideEvent{At: at, Height: height, High: strings.EqualFold(prediction.Type, "H")})
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("tide: the provider answer carried no predictions")
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	return events, nil
}

// parseFloatValue parses one numeric provider field.
func parseFloatValue(text string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(text), 64)
}

// tideInterpolate returns the estimated height at a moment between two events,
// by the rule of twelfths. The rule divides a half-cycle into six equal parts;
// a real interval is rarely exactly six hours, so the position within the cycle
// is scaled to the interval's actual length.
func tideInterpolate(previous, next TideEvent, at time.Time) (height, percent float64) {
	span := next.At.Sub(previous.At).Seconds()
	if span <= 0 {
		return previous.Height, 0
	}
	fraction := at.Sub(previous.At).Seconds() / span
	fraction = math.Max(0, math.Min(1, fraction))
	position := fraction * 6
	index := min(int(math.Floor(position)), 5)
	within := position - float64(index)
	progress := (tideTwelfths[index] + within*(tideTwelfths[index+1]-tideTwelfths[index])) / 12
	return previous.Height + (next.Height-previous.Height)*progress, progress * 100
}

// tideStateAt computes the state of the tide at a moment from the events around
// it. The moment must fall inside the window the events cover.
func tideStateAt(events []TideEvent, at time.Time) (TideCurrent, bool) {
	for i := 0; i+1 < len(events); i++ {
		previous, next := events[i], events[i+1]
		if at.Before(previous.At) || !at.Before(next.At) {
			continue
		}
		height, percent := tideInterpolate(previous, next, at)
		return TideCurrent{
			Previous:      previous,
			Next:          next,
			Flooding:      next.Height > previous.Height,
			Height:        height,
			Change:        height - previous.Height,
			PercentToNext: percent,
		}, true
	}
	return TideCurrent{}, false
}

// SpringNeapAssessment names where the tidal range stands in the spring/neap
// cycle. The range is greatest when the Sun and Moon pull together — at new and
// full moon — and least when they pull at right angles, at the quarters. The
// assessment is read from the Moon's elongation, so it is available offline and
// agrees with what the moon command reports.
func SpringNeapAssessment(at time.Time) string {
	springness := math.Abs(math.Cos(radians(MoonElongationDeg(at))))
	later := math.Abs(math.Cos(radians(MoonElongationDeg(at.Add(12 * time.Hour)))))
	switch {
	case springness > 0.85:
		return "Spring Tide (extreme tidal range)"
	case springness < 0.35:
		return "Neap Tide (minimal tidal range)"
	case later > springness:
		return "Approaching Spring Tide (extreme tidal range expected)"
	default:
		return "Approaching Neap Tide (minimal tidal range expected)"
	}
}

// findTideStation resolves a station id or a port name against the reference
// table. An exact id wins, then an exact name, then the shortest name that
// contains the query, so "boston" finds Boston rather than a Boston Creek.
func findTideStation(query string) (tideStation, bool) {
	word := strings.ToLower(strings.TrimSpace(query))
	if word == "" {
		return tideStation{}, false
	}
	for _, station := range tideStations {
		if station.ID == word {
			return station, true
		}
	}
	for _, station := range tideStations {
		if strings.ToLower(station.Name) == word {
			return station, true
		}
	}
	// Nothing matched exactly, so prefer the station whose name starts with what
	// was asked for over one that merely contains it, and the shortest of
	// equally good matches. The catalog carries every station the provider
	// publishes, so "san francisco" must reach the Golden Gate station rather
	// than South San Francisco.
	best := tideStation{}
	bestScore := 0
	found := false
	for _, station := range tideStations {
		name := strings.ToLower(station.Name)
		var score int
		switch {
		case strings.HasPrefix(name, word):
			score = 2
		case strings.Contains(name, word):
			score = 1
		default:
			continue
		}
		switch {
		case !found, score > bestScore:
			best, bestScore, found = station, score, true
		case score == bestScore && len(station.Name) < len(best.Name):
			best = station
		}
	}
	return best, found
}

// nearestTideStation returns the reference station closest to a position, with
// the distance in meters.
func nearestTideStation(point LatLng) (tideStation, float64, bool) {
	if len(tideStations) == 0 {
		return tideStation{}, 0, false
	}
	best := tideStations[0]
	bestMeters := HaversineDistance(point, LatLng{Lat: best.Lat, Lng: best.Lng})
	for _, station := range tideStations[1:] {
		meters := HaversineDistance(point, LatLng{Lat: station.Lat, Lng: station.Lng})
		if meters < bestMeters {
			best, bestMeters = station, meters
		}
	}
	return best, bestMeters, true
}

// runTide answers with the day's tide at a station, or with a page of the
// offline station catalog when the request is a discovery one. Discovery is
// answered before the provider is consulted: finding the id of a station is a
// question the reference table can answer, and it must work on a link that
// cannot reach the provider at all.
func (c *commandContext) runTide() []string {
	if kind, text := splitDiscovery(c.Args); kind != "" {
		return c.runTideDiscovery(kind, text)
	}
	template := strings.TrimSpace(c.reg.tideURL())
	if template == "" {
		return []string{tideNotConfiguredLine}
	}
	station, day, ok := c.resolveTideRequest(strings.TrimSpace(c.Args))
	if !ok {
		return []string{"Usage: " + tideUsage, tideUsageHint}
	}
	fetchURL, err := providerURLValues(template, map[string]string{
		tideStationToken: station.ID,
		tideDateToken:    day.Format("20060102"),
	})
	if err != nil {
		logf("tide: %v", err)
		return []string{tideMisconfiguredLine}
	}
	lines, err := c.reg.providerAnswer(fetchURL, c.now(), tideMaxBodyBytes, func(body []byte) ([]string, error) {
		return renderTideAnswer(string(body), station, day, c.now())
	})
	if err != nil {
		logf("tide: lookup for %v failed: %v", station.ID, err)
		return []string{tideFailedLine}
	}
	return lines
}

// runTideDiscovery answers a tide search, near, or list request from the station
// catalog. Every answer is offline: the catalog is the provider's own station
// list, embedded, so it answers at the same speed as the geodesy commands.
func (c *commandContext) runTideDiscovery(kind, text string) []string {
	q := discoveryQuery{Command: "tide", Catalog: tideCatalogName, Kind: kind}
	switch kind {
	case discoveryKindSearch:
		words, page := splitPageArgument(text)
		query := strings.Join(searchWords(words), " ")
		if query == "" {
			return []string{"Usage: tide search <query> [page]", searchHint(q)}
		}
		q.Text = query
		matches := searchCatalog(tideCatalog, query)
		if len(matches) == 0 {
			return []string{fmt.Sprintf("No %v match %q.", tideCatalogName, q.Text), searchHint(q)}
		}
		return c.renderCatalogPage(q, matches, page)
	case discoveryKindNear:
		return c.renderNearAnswer(q, tideCatalog, text)
	case discoveryKindList:
		region, page := splitListArgument(text)
		entries := filterCatalogRegion(tideCatalog, region)
		if len(entries) == 0 {
			return []string{fmt.Sprintf("No %v in %v.", tideCatalogName, safeEcho(region, maxDiscoveryEchoBytes)), listHint(q)}
		}
		q.Text = strings.ToUpper(strings.Join(strings.Fields(region), " "))
		return c.renderCatalogPage(q, entries, page)
	default:
		return []string{"Usage: " + tideUsage, tideUsageHint}
	}
}

// resolveTideRequest works out which station and which day a request means. A
// station id or a port name may be followed by a date; a position is resolved
// to the nearest reference station.
func (c *commandContext) resolveTideRequest(text string) (tideStation, time.Time, bool) {
	now := c.now()
	if text == "" {
		return tideStation{}, time.Time{}, false
	}
	// The whole request may be a station with no date.
	if station, ok := findTideStation(text); ok {
		return station, utcMidnight(now), true
	}
	// Otherwise the last words may be a date.
	for _, index := range splitBoundaries(text) {
		station, ok := findTideStation(text[:index])
		if !ok {
			continue
		}
		day, err := ParseAlmanacDay(text[index:], now)
		if err != nil {
			continue
		}
		return station, day, true
	}
	// Finally the request may be a position, with or without a date.
	point, day, ok := c.splitSunArgs(text)
	if !ok {
		return tideStation{}, time.Time{}, false
	}
	station, meters, ok := nearestTideStation(point)
	if !ok || meters > tideNearLimitKm*1000 {
		return tideStation{}, time.Time{}, false
	}
	return station, day, true
}

// renderTideAnswer decodes one provider answer into the reply lines.
func renderTideAnswer(body string, station tideStation, day, now time.Time) ([]string, error) {
	events, err := parseTidePredictions(body)
	if err != nil {
		return nil, err
	}
	state := TideDay{
		Station: station,
		Day:     day,
		Events:  tideEventsOn(events, day),
		Spring:  SpringNeapAssessment(now),
	}
	// The state of the tide is only "current" when the request is for today:
	// a table for another date is a table, not a present-tense report.
	if current, ok := tideStateAt(events, now); ok && utcMidnight(now).Equal(utcMidnight(day)) {
		state.Current, state.HasCurrent = current, true
	}

	lines := []string{fmt.Sprintf("Tides for %v (%v) on %v (%v):",
		state.Station.Name, state.Station.ID, state.Day.Format("Jan 2"), tideDatum)}
	if len(state.Events) == 0 {
		lines = append(lines, "No high or low water is predicted on this date.")
	} else {
		lines = append(lines, tideEventLine(state.Events))
	}
	if state.HasCurrent {
		lines = append(lines, tideCurrentLine(state.Current, now))
	}
	lines = append(lines, "Spring/Neap: "+state.Spring)
	return lines, nil
}

// tideEventsOn selects the events that fall inside one UTC day.
func tideEventsOn(events []TideEvent, day time.Time) []TideEvent {
	start := utcMidnight(day)
	end := start.AddDate(0, 0, 1)
	out := make([]TideEvent, 0, 4)
	for _, event := range events {
		if event.At.Before(start) || !event.At.Before(end) {
			continue
		}
		out = append(out, event)
	}
	return out
}

// tideEventLine renders the day's events as one table row, with the times
// aligned so a column of them can be read at a glance.
func tideEventLine(events []TideEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%-4v %v (%.1f ft)", event.Kind(), event.At.Format("15:04"), event.Height))
	}
	return strings.Join(parts, " | ")
}

// tideCurrentLine renders the state of the tide now, and the event it is
// heading for and how long it has left to run.
func tideCurrentLine(current TideCurrent, now time.Time) string {
	direction := "EBBING"
	if current.Flooding {
		direction = "FLOODING"
	}
	toward := "low"
	if current.Next.High {
		toward = "high"
	}
	return fmt.Sprintf("Current: %v (%+.1f ft, %.0f%% to %v) | Next: %v %v (in %v)",
		direction, current.Change, current.PercentToNext, toward,
		current.Next.Kind(), current.Next.At.Format("15:04"), formatWindow(current.Next.At.Sub(now)))
}

// tideURL returns the configured tide provider template.
func (r *registry) tideURL() string {
	if r.bot == nil || r.bot.cfg == nil {
		return ""
	}
	return r.bot.cfg.TideURL
}
