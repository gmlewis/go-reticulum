// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the catalog machinery the discovery commands share: how a
// station table becomes rows a person can search, how a query is matched, how a
// position finds its nearest rows, and how the argument line splits into a
// sub-command, a filter, and a page.
//
// It exists because buoy, tide, and river-adjacent commands all face the same
// problem: the identifiers the providers use are opaque, and asking a user on a
// radio link to already know them is a dead end. Every catalog is therefore
// searchable by the name a person would say, filterable by the region they are
// in, and resolvable from a position, all without a network call. The rows are
// uniform — an identifier, a label, a region, and a point — so one matcher, one
// proximity sort, and one pager serve every catalog.

package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The discovery sub-commands every catalog command accepts.
const (
	// discoveryKindSearch matches a query against every row's text.
	discoveryKindSearch = "search"
	// discoveryKindNear lists the rows closest to a place or position.
	discoveryKindNear = "near"
	// discoveryKindList lists rows, optionally filtered by region.
	discoveryKindList = "list"
)

// catalogEntry is one searchable row of a station catalog: the identity the
// command takes as an argument, the text a person searches for and reads, the
// region the row filters by, and where it is.
type catalogEntry struct {
	// ID is the identifier the command takes, like 9414290 or KDEN.
	ID string
	// Label is the human-readable name, already carrying its region in the
	// catalog's own style ("San Francisco (Golden Gate), CA").
	Label string
	// Region is the state, country, or basin code the row filters by.
	Region string
	// Point is the row's position.
	Point LatLng
	// Keywords is extra searchable text the row does not display, like an
	// airport's city and its IATA code.
	Keywords string
	// Rank breaks a tie between equally good matches; lower comes first. A
	// catalog whose rows are not equally important sets it, so the busiest
	// airfield in a city is listed before its satellite fields.
	Rank int
}

// catalogDistance is one row of a proximity answer: the row, how far it is, and
// the bearing to it.
type catalogDistance struct {
	// Entry is the row.
	Entry catalogEntry
	// Meters is the great-circle distance from the reference point.
	Meters float64
	// Bearing is the initial bearing from the reference point, in degrees.
	Bearing float64
}

// catalogMatch is one search hit and the ranks it was given, lower being better.
type catalogMatch struct {
	entry catalogEntry
	score int
}

// haystack is the lowercased text a query is matched against: the identifier,
// the label, the region code, the state name the code stands for, and any extra
// keywords the catalog adds (an airport's city and its IATA code, say), so
// "ca", "california", "denver", "KDEN", and "DEN" all reach the same row.
func (e catalogEntry) haystack() string {
	haystack := strings.ToLower(e.ID + " " + e.Label + " " + e.Region + " " + e.Keywords)
	if name := usStateNames[strings.ToUpper(e.Region)]; name != "" {
		haystack += " " + name
	}
	return haystack
}

// searchWords splits a query into the words that must all be found. A word with
// no letter or digit in it is dropped, because it could only match another
// punctuation mark, and a query of nothing but punctuation is no query at all:
// the command answers with its usage rather than with a search for "???".
func searchWords(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == '.' || r == '/'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		word := strings.TrimSpace(field)
		if strings.ContainsFunc(word, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			out = append(out, word)
		}
	}
	return out
}

// searchCatalog returns the rows whose identifier, label, or region contains
// every word of the query, best match first. An exact identifier or a leading
// match ranks above a match buried in the middle, so "46026" and "san" both put
// the row a person meant at the top of the page; equally good matches keep the
// catalog's own order, which is its importance order.
func searchCatalog(entries []catalogEntry, query string) []catalogEntry {
	words := searchWords(query)
	if len(words) == 0 {
		return nil
	}
	phrase := strings.Join(words, " ")
	matches := make([]catalogMatch, 0, 8)
	for _, entry := range entries {
		haystack := entry.haystack()
		if !containsAll(haystack, words) {
			continue
		}
		matches = append(matches, catalogMatch{entry: entry, score: entry.matchScore(phrase)})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].entry.Rank < matches[j].entry.Rank
	})
	out := make([]catalogEntry, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.entry)
	}
	return out
}

// containsAll reports whether every word appears in the haystack.
func containsAll(haystack string, words []string) bool {
	for _, word := range words {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

// matchScore ranks one hit: an exact identifier is best, then a leading
// identifier or label, then a match inside the row.
func (e catalogEntry) matchScore(phrase string) int {
	id := strings.ToLower(e.ID)
	label := strings.ToLower(e.Label)
	switch {
	case id == phrase:
		return 0
	case strings.HasPrefix(id, phrase):
		return 1
	case strings.HasPrefix(label, phrase):
		return 2
	case strings.Contains(" "+label, " "+phrase):
		return 3
	default:
		return 4
	}
}

// findCatalogEntry resolves a token to one row: an exact identifier first, then
// the best search match. It is how a proximity request accepts the identifier of
// the thing being searched for, like "metar near KDEN".
func findCatalogEntry(entries []catalogEntry, token string) (catalogEntry, bool) {
	word := strings.ToLower(strings.TrimSpace(token))
	if word == "" {
		return catalogEntry{}, false
	}
	for _, entry := range entries {
		if strings.ToLower(entry.ID) == word {
			return entry, true
		}
	}
	if matches := searchCatalog(entries, token); len(matches) > 0 {
		return matches[0], true
	}
	return catalogEntry{}, false
}

// filterCatalogRegion keeps the rows whose region matches a filter. A filter
// matches a region code ("CA") or the state name it stands for ("california"),
// in any case.
func filterCatalogRegion(entries []catalogEntry, region string) []catalogEntry {
	filter := strings.ToLower(strings.TrimSpace(region))
	if filter == "" {
		return entries
	}
	out := make([]catalogEntry, 0, len(entries))
	for _, entry := range entries {
		if regionMatches(entry.Region, filter) {
			out = append(out, entry)
		}
	}
	return out
}

// regionMatches reports whether one row's region code answers a filter.
func regionMatches(region, filter string) bool {
	code := strings.ToLower(strings.TrimSpace(region))
	if code == "" {
		return false
	}
	if code == filter {
		return true
	}
	return usStateNames[strings.ToUpper(code)] == filter
}

// nearestCatalog returns the closest rows to a point, nearest first and capped
// at limit. Ties keep the catalog's own order, so the same question always
// answers the same way.
func nearestCatalog(entries []catalogEntry, from LatLng, limit int) []catalogDistance {
	near := make([]catalogDistance, 0, len(entries))
	for _, entry := range entries {
		near = append(near, catalogDistance{
			Entry:   entry,
			Meters:  HaversineDistance(from, entry.Point),
			Bearing: InitialBearing(from, entry.Point),
		})
	}
	sort.SliceStable(near, func(i, j int) bool { return near[i].Meters < near[j].Meters })
	if limit > 0 && len(near) > limit {
		near = near[:limit]
	}
	return near
}

// resolveCatalogPoint resolves a proximity request's argument to a position: a
// row of the catalog itself, by identifier or name, any of the notations the
// navigation commands accept, and otherwise any place known to the other offline
// reference catalogs (the airfield/city gazetteer, coastal tide stations, or
// weather buoys).
func resolveCatalogPoint(entries []catalogEntry, argument string) (LatLng, bool) {
	text := strings.TrimSpace(argument)
	if text == "" {
		return LatLng{}, false
	}
	if entry, ok := findCatalogEntry(entries, text); ok {
		return entry.Point, true
	}
	if point, err := ParseLocation(text); err == nil {
		return point, true
	}
	for _, fallback := range [][]catalogEntry{metarCatalog, tideCatalog, buoyCatalog} {
		if entry, ok := findCatalogEntry(fallback, text); ok {
			return entry.Point, true
		}
	}
	return LatLng{}, false
}

// splitDiscovery splits an argument line into a discovery sub-command and its
// text. It reports an empty kind when the line is not a discovery request, in
// which case the command's own argument parser gets the line unchanged.
func splitDiscovery(args string) (kind, text string) {
	word, rest := splitCommandLine(args)
	switch word {
	case discoveryKindSearch, discoveryKindNear, discoveryKindList:
		return word, rest
	}
	return "", args
}

// splitPageArgument peels a trailing page number off a search argument. A page
// is only recognized when something else remains, because a search for "46026"
// is a search, not a request for page 46026.
func splitPageArgument(text string) (query string, page int) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return strings.TrimSpace(text), 1
	}
	last := fields[len(fields)-1]
	parsed, err := strconv.Atoi(last)
	if err != nil || parsed < 1 {
		return strings.TrimSpace(text), 1
	}
	return strings.Join(fields[:len(fields)-1], " "), parsed
}

// splitListArgument splits a list argument into its region filter and its page.
// A lone number is a page, because a numeric region would filter nothing; a
// trailing number after a region is a page too.
func splitListArgument(text string) (region string, page int) {
	fields := strings.Fields(text)
	switch len(fields) {
	case 0:
		return "", 1
	case 1:
		if parsed, err := strconv.Atoi(fields[0]); err == nil && parsed >= 1 {
			return "", parsed
		}
		return fields[0], 1
	}
	last := fields[len(fields)-1]
	parsed, err := strconv.Atoi(last)
	if err != nil || parsed < 1 {
		return strings.Join(fields, " "), 1
	}
	return strings.Join(fields[:len(fields)-1], " "), parsed
}

// renderNearAnswer answers a "<cmd> near <place>" request from one catalog: it
// resolves the argument to a position, finds the closest rows, and renders them
// as the single page a proximity answer is.
func (c *commandContext) renderNearAnswer(q discoveryQuery, entries []catalogEntry, argument string) []string {
	if strings.TrimSpace(argument) == "" {
		return []string{"Usage: " + q.Command + " near <place|coords|pluscode>"}
	}
	point, ok := resolveCatalogPoint(entries, argument)
	if !ok {
		return []string{
			fmt.Sprintf("%v: %q is not a place, a coordinate, or a plus code I can place",
				q.Command, safeEcho(argument, maxDiscoveryEchoBytes)),
			fmt.Sprintf("Try coordinates (37.8,-122.4), a plus code (849VCWC8+R9), or %q.", q.Command+" list"),
		}
	}
	q.Text = strings.Join(strings.Fields(argument), " ")
	return c.renderNearPage(q, nearestCatalog(entries, point, discoveryNearLimit))
}

// searchHint returns the second line of an empty search answer: the two other
// ways into the same catalog, in the words the asker would type.
func searchHint(q discoveryQuery) string {
	return fmt.Sprintf("Try fewer words, a state code (%q), or a position (%q).",
		q.Command+" list CA", q.Command+" near 37.8,-122.4")
}

// listHint returns the second line of an empty region answer.
func listHint(q discoveryQuery) string {
	return fmt.Sprintf("Try a two-letter state code (CA, OR, WA), or %q for every row.", q.Command+" list")
}

// catalogFrom renders a typed station table into the uniform rows the discovery
// commands search, so a catalog is defined once and read by both the command
// that looks a station up and the commands that discover one.
func catalogFrom[S any](stations []S, entry func(S) catalogEntry) []catalogEntry {
	out := make([]catalogEntry, 0, len(stations))
	for _, station := range stations {
		out = append(out, entry(station))
	}
	return out
}

// usStateNames maps a two-letter state or territory code to the name a person
// might type instead of it, so "list california" and "list CA" are the same
// request.
var usStateNames = map[string]string{
	"AL": "alabama", "AK": "alaska", "AZ": "arizona", "AR": "arkansas",
	"CA": "california", "CO": "colorado", "CT": "connecticut", "DE": "delaware",
	"DC": "district of columbia", "FL": "florida", "GA": "georgia", "HI": "hawaii",
	"ID": "idaho", "IL": "illinois", "IN": "indiana", "IA": "iowa",
	"KS": "kansas", "KY": "kentucky", "LA": "louisiana", "ME": "maine",
	"MD": "maryland", "MA": "massachusetts", "MI": "michigan", "MN": "minnesota",
	"MS": "mississippi", "MO": "missouri", "MT": "montana", "NE": "nebraska",
	"NV": "nevada", "NH": "new hampshire", "NJ": "new jersey", "NM": "new mexico",
	"NY": "new york", "NC": "north carolina", "ND": "north dakota", "OH": "ohio",
	"OK": "oklahoma", "OR": "oregon", "PA": "pennsylvania", "RI": "rhode island",
	"SC": "south carolina", "SD": "south dakota", "TN": "tennessee", "TX": "texas",
	"UT": "utah", "VT": "vermont", "VA": "virginia", "WA": "washington",
	"WV": "west virginia", "WI": "wisconsin", "WY": "wyoming",
	"PR": "puerto rico", "VI": "virgin islands", "AS": "american samoa",
	"GU": "guam", "MP": "northern mariana islands",
}
