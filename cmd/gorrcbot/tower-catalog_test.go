// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestTowerCatalogIntegrity asserts every embedded row is well formed: a unique
// identifier, a plausible service type, a position inside the legal range, an
// upper-case two-letter country code, a named place, a frequency, and a
// non-negative elevation. A catalog is data, and data rots silently: this is the
// test that fails when a hand-edited row is malformed rather than at the moment
// an operator needs it.
func TestTowerCatalogIntegrity(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(towerRecords))
	for i, record := range towerRecords {
		if strings.TrimSpace(record.ID) == "" {
			t.Errorf("row %v (%v) has an empty ID", i, record.Name)
		}
		if seen[record.ID] {
			t.Errorf("row %v duplicates the ID %q", i, record.ID)
		}
		seen[record.ID] = true
		if strings.TrimSpace(record.Name) == "" {
			t.Errorf("row %v (%v) has an empty name", i, record.ID)
		}
		if _, ok := towerTypeNames[record.Type]; !ok {
			t.Errorf("row %v (%v) has the unknown type %q", i, record.ID, record.Type)
		}
		if record.Lat < -90 || record.Lat > 90 {
			t.Errorf("row %v (%v) latitude %v is outside -90..90", i, record.ID, record.Lat)
		}
		if record.Lng < -180 || record.Lng > 180 {
			t.Errorf("row %v (%v) longitude %v is outside -180..180", i, record.ID, record.Lng)
		}
		if strings.TrimSpace(record.Freq) == "" {
			t.Errorf("row %v (%v) names no frequency or band", i, record.ID)
		}
		if strings.TrimSpace(record.City) == "" {
			t.Errorf("row %v (%v) names no city", i, record.ID)
		}
		if strings.TrimSpace(record.Region) == "" {
			t.Errorf("row %v (%v) names no region", i, record.ID)
		}
		if len(record.Country) != 2 || record.Country != strings.ToUpper(record.Country) {
			t.Errorf("row %v (%v) country %q is not an upper-case two-letter code", i, record.ID, record.Country)
		}
		if record.ElevMeters < 0 {
			t.Errorf("row %v (%v) elevation %v is negative", i, record.ID, record.ElevMeters)
		}
		if strings.TrimSpace(record.Operator) == "" {
			t.Errorf("row %v (%v) names no operator", i, record.ID)
		}
	}
}

// TestTowerCatalogRepeatersCarryTheirOffset asserts every repeater row has the
// transmit offset an operator must set, and that any row claiming one states it
// in the standard shape. A repeater without its offset is unusable on a
// handheld, which is the one thing this catalog exists to prevent; an emergency
// relay that repeats also carries one, while a cellular band plan never does.
func TestTowerCatalogRepeatersCarryTheirOffset(t *testing.T) {
	t.Parallel()

	for _, record := range towerRecords {
		offset := strings.TrimSpace(record.Offset)
		if record.Type == TowerTypeRepeater && offset == "" {
			t.Errorf("repeater %v (%v) has no offset", record.ID, record.Name)
		}
		if record.Type == TowerTypeCellular && offset != "" {
			t.Errorf("cellular row %v (%v) claims the repeater offset %q", record.ID, record.Name, offset)
		}
		if offset == "" {
			continue
		}
		if !strings.HasPrefix(offset, "+") && !strings.HasPrefix(offset, "-") {
			t.Errorf("%v: the offset %q is not a signed shift", record.ID, offset)
		}
		if !strings.HasSuffix(offset, "MHz") {
			t.Errorf("%v: the offset %q is not stated in MHz", record.ID, offset)
		}
		if tone := strings.TrimSpace(record.Tone); tone != "" && !strings.HasSuffix(tone, "Hz") {
			t.Errorf("%v: the tone %q is not stated in Hz", record.ID, tone)
		}
	}
}

// TestTowerCatalogIsBalancedForUSAndChina asserts the international-parity
// requirement directly: the catalog must be genuinely useful in both countries
// rather than a US list with a handful of foreign rows bolted on. It also checks
// the two countries' province and state coverage, because "useful in China"
// means more than one city.
func TestTowerCatalogIsBalancedForUSAndChina(t *testing.T) {
	t.Parallel()

	const minPerCountry = 60
	const minRegions = 12
	countries := map[string]int{}
	regions := map[string]map[string]bool{}
	for _, record := range towerRecords {
		countries[record.Country]++
		if regions[record.Country] == nil {
			regions[record.Country] = map[string]bool{}
		}
		regions[record.Country][record.Region] = true
	}

	if len(towerRecords) < 150 {
		t.Errorf("the catalog holds %v rows, want at least 150", len(towerRecords))
	}
	for _, country := range []string{"US", "CN"} {
		if got := countries[country]; got < minPerCountry {
			t.Errorf("the catalog holds %v rows for %v, want at least %v", got, country, minPerCountry)
		}
		if got := len(regions[country]); got < minRegions {
			t.Errorf("the catalog covers %v regions in %v, want at least %v: %v",
				got, country, minRegions, sortedKeys(regions[country]))
		}
	}

	// International parity is not only about the two headline countries: a
	// third of the catalog is meant to be reachable from anywhere, so several
	// other countries must be represented.
	if got := len(countries); got < 10 {
		t.Errorf("the catalog covers %v countries, want at least 10: %v", got, sortedKeys(countries))
	}
}

// TestTowerCatalogCoversEveryServiceType asserts all four services appear in
// both headline countries, because an operator in either one may need a
// repeater, a mast, an emergency relay, or a marine channel.
func TestTowerCatalogCoversEveryServiceType(t *testing.T) {
	t.Parallel()

	counts := map[TowerType]int{}
	for _, record := range towerRecords {
		counts[record.Type]++
	}
	for _, towerType := range towerTypeCodes {
		if counts[towerType] == 0 {
			t.Errorf("no row carries the service type %v", towerType)
		}
	}
	for _, country := range []string{"US", "CN"} {
		byCountry := map[TowerType]int{}
		for _, record := range towerRecords {
			if record.Country == country {
				byCountry[record.Type]++
			}
		}
		for _, towerType := range towerTypeCodes {
			if byCountry[towerType] == 0 {
				t.Errorf("%v has no row of service type %v", country, towerType)
			}
		}
	}
}

// TestTowerCountryNamesCoverEveryCountryInTheCatalog asserts the searchable
// country-name table never drifts from the catalog: a country an operator can
// list by code must also be nameable in words.
func TestTowerCountryNamesCoverEveryCountryInTheCatalog(t *testing.T) {
	t.Parallel()

	present := map[string]bool{}
	for _, record := range towerRecords {
		present[record.Country] = true
	}
	for country := range present {
		if towerCountryNames[country] == "" {
			t.Errorf("country %v appears in the catalog but has no searchable name", country)
		}
	}
	for country, name := range towerCountryNames {
		if !present[country] {
			t.Errorf("towerCountryNames names %v (%v), which the catalog does not contain", country, name)
		}
		if name != strings.ToLower(name) {
			t.Errorf("the country name %q for %v must be lower-case to match a query", name, country)
		}
	}
}

// TestTowerCatalogEntryIsSearchable asserts the bridge from a catalog row to the
// uniform discovery row preserves everything a person might search by: the
// identifier, the place, the frequency, the operator, and the country.
func TestTowerCatalogEntryIsSearchable(t *testing.T) {
	t.Parallel()

	for _, record := range towerRecords {
		entry := record.catalogEntry()
		if entry.ID != record.ID {
			t.Errorf("entry ID = %q, want %q", entry.ID, record.ID)
		}
		if entry.Point.Lat != record.Lat || entry.Point.Lng != record.Lng {
			t.Errorf("%v: entry point = %+v, want %v,%v", record.ID, entry.Point, record.Lat, record.Lng)
		}
		if entry.Region != record.Region {
			t.Errorf("%v: entry region = %q, want %q", record.ID, entry.Region, record.Region)
		}
		haystack := entry.haystack()
		for _, want := range []string{
			strings.ToLower(record.ID),
			strings.ToLower(record.City),
			strings.ToLower(record.Freq),
			strings.ToLower(record.Operator),
			strings.ToLower(record.Country),
		} {
			if !strings.Contains(haystack, want) {
				t.Errorf("%v: haystack %q does not contain %q", record.ID, haystack, want)
			}
		}
	}
}

// TestTowerCatalogSpatialLookupIsSane asserts the proximity search this catalog
// feeds returns what a person standing at a landmark would expect: San
// Francisco's nearest repeater is on Sutro Tower, and Beijing's nearest is in
// Beijing.
func TestTowerCatalogSpatialLookupIsSane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		point     LatLng
		wantID    string
		wantCity  string
		maxMeters float64
	}{
		{
			name: "Sutro Tower", point: LatLng{Lat: 37.7553, Lng: -122.4527},
			wantID: "W6PW-2M", wantCity: "San Francisco", maxMeters: 100,
		},
		{
			name: "Golden Gate", point: LatLng{Lat: 37.8199, Lng: -122.4783},
			wantCity: "San Francisco", maxMeters: 12000,
		},
		{
			name: "Tiananmen Square", point: LatLng{Lat: 39.9055, Lng: 116.3976},
			wantCity: "Beijing", maxMeters: 9000,
		},
		{
			name: "Chengdu center", point: LatLng{Lat: 30.5728, Lng: 104.0668},
			wantCity: "Chengdu", maxMeters: 35000,
		},
		{
			name: "Lhasa", point: LatLng{Lat: 29.6520, Lng: 91.1721},
			wantID: "XZ-RPT-01", wantCity: "Lhasa", maxMeters: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			near := nearestCatalog(towerCatalog, tt.point, 1)
			if len(near) != 1 {
				t.Fatalf("nearestCatalog returned %v rows, want 1", len(near))
			}
			if tt.wantID != "" && near[0].Entry.ID != tt.wantID {
				t.Errorf("nearest to %v is %v, want %v", tt.name, near[0].Entry.ID, tt.wantID)
			}
			if tt.wantCity != "" && !strings.Contains(near[0].Entry.Label, tt.wantCity) {
				t.Errorf("nearest to %v is %q, want a row in %v",
					tt.name, near[0].Entry.Label, tt.wantCity)
			}
			if near[0].Meters > tt.maxMeters {
				t.Errorf("nearest to %v is %.0f m away, want at most %.0f m",
					tt.name, near[0].Meters, tt.maxMeters)
			}
		})
	}
}

// TestTowerCatalogSearchReachesEveryNotation asserts a person can find a row by
// any of the things they know about it: a callsign, a city, a pinyin place name,
// a frequency, an operator, or a province code.
func TestTowerCatalogSearchReachesEveryNotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "callsign", query: "W6PW", want: "W6PW-2M"},
		{name: "site name", query: "sutro", want: "W6PW-2M"},
		{name: "us city", query: "san francisco", want: "W6PW-2M"},
		{name: "us state name", query: "california", want: "W6PW-2M"},
		{name: "frequency", query: "145.150", want: "W6PW-2M"},
		{name: "pinyin city", query: "beijing", want: "BJ-RPT-01"},
		{name: "chinese site id", query: "BJ-RPT-01", want: "BJ-RPT-01"},
		{name: "chinese province code", query: "sichuan", want: "SC-RPT-01"},
		{name: "carrier", query: "china mobile", want: "CN-BJ-T001"},
		{name: "international", query: "tokyo", want: "JP-TKY-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches := searchCatalog(towerCatalog, tt.query)
			if len(matches) == 0 {
				t.Fatalf("searchCatalog(%q) found nothing", tt.query)
			}
			found := false
			for _, match := range matches {
				if match.ID == tt.want {
					found = true
					break
				}
			}
			if !found {
				ids := make([]string, 0, len(matches))
				for _, match := range matches {
					ids = append(ids, match.ID)
				}
				t.Errorf("searchCatalog(%q) = %v, want it to include %v", tt.query, ids, tt.want)
			}
		})
	}
}

// TestTowerRegionMatchesHonorsStatesAndCountries asserts the filtering rules a
// list answer rests on: a region code, a state's name, a country code, and a
// country's name all work, and a two-letter code that is a US state means the
// state — except that a Chinese province sharing the code is listed beside it,
// which is what makes both "list SC" (South Carolina) and "list SC" (Sichuan)
// true at once.
func TestTowerRegionMatchesHonorsStatesAndCountries(t *testing.T) {
	t.Parallel()

	record := func(region, country string) TowerRecord {
		return TowerRecord{ID: "X", Region: region, Country: country}
	}
	tests := []struct {
		name   string
		record TowerRecord
		filter string
		want   bool
	}{
		{name: "empty filter matches everything", record: record("CA", "US"), filter: "", want: true},
		{name: "region code", record: record("CA", "US"), filter: "ca", want: true},
		{name: "state name", record: record("CA", "US"), filter: "california", want: true},
		{name: "other region", record: record("CO", "US"), filter: "ca", want: false},
		{name: "country code", record: record("BJ", "CN"), filter: "cn", want: true},
		{name: "country name", record: record("BJ", "CN"), filter: "china", want: true},
		{name: "canadian province is not a US state", record: record("ON", "CA"), filter: "ca", want: false},
		{name: "canada by name", record: record("ON", "CA"), filter: "canada", want: true},
		{name: "sichuan by province code", record: record("SC", "CN"), filter: "sc", want: true},
		{name: "south carolina by province code", record: record("SC", "US"), filter: "sc", want: true},
		{name: "germany by name when the code is a state", record: record("BE", "DE"), filter: "germany", want: true},
		{name: "delaware wins over germany", record: record("BE", "DE"), filter: "de", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := towerRegionMatches(tt.record, strings.ToLower(tt.filter)); got != tt.want {
				t.Errorf("towerRegionMatches(%+v, %q) = %v, want %v",
					tt.record, tt.filter, got, tt.want)
			}
		})
	}
}

// sortedKeys returns a map's string keys in sorted order, so a failure message
// is stable and readable.
func sortedKeys[M ~map[string]V, V any](m M) []string {
	return slices.Sorted(maps.Keys(m))
}
