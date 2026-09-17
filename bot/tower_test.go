// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// towerConfig returns a test configuration whose tower catalog is the embedded
// one alone.
func towerConfig() *BotConfig {
	return defaultTestConfig()
}

// towerConfigWithCSV returns a test configuration that merges a local towers.csv.
func towerConfigWithCSV(path string) *BotConfig {
	cfg := defaultTestConfig()
	cfg.TowersPath = path
	return cfg
}

// writeTowersCSV writes a towers.csv into a fresh temp directory and returns its
// path.
func writeTowersCSV(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(tempDir(t), "towers.csv")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %v: %v", path, err)
	}
	return path
}

// towerTestCSV is a local dataset shaped exactly like the documented format: one
// row that overrides an embedded site and one that adds a new one.
const towerTestCSV = `id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev
W6PW-2M,Local Sutro Repeater,RPT,37.7600,-122.4500,146.100 MHz,-0.6 MHz,100.0 Hz,W6LOCAL,San Francisco,CA,US,250
ZZ-TEST-01,Test Ridge Relay,RPT,39.0000,-120.0000,147.000 MHz,+0.6 MHz,100.0 Hz,W6TEST,Testville,CA,US,1200
`

// TestParseTowersCSVReadsTheDocumentedFormat asserts the streaming reader
// decodes the documented 13-column format, with and without a header row.
func TestParseTowersCSVReadsTheDocumentedFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "with a header", text: towerTestCSV},
		{name: "without a header", text: strings.SplitN(towerTestCSV, "\n", 2)[1]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			records, warnings, err := parseTowersCSV(strings.NewReader(tt.text))
			if err != nil {
				t.Fatalf("parseTowersCSV: %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("warnings = %v, want none", warnings)
			}
			if len(records) != 2 {
				t.Fatalf("read %v rows, want 2: %+v", len(records), records)
			}
			first := records[0]
			if first.ID != "W6PW-2M" || first.Name != "Local Sutro Repeater" {
				t.Errorf("row 1 = %+v, want the local Sutro row", first)
			}
			if first.Type != TowerTypeRepeater {
				t.Errorf("row 1 type = %q, want %q", first.Type, TowerTypeRepeater)
			}
			if first.Lat != 37.76 || first.Lng != -122.45 {
				t.Errorf("row 1 position = %v,%v, want 37.76,-122.45", first.Lat, first.Lng)
			}
			if first.Freq != "146.100 MHz" || first.Offset != "-0.6 MHz" || first.Tone != "100.0 Hz" {
				t.Errorf("row 1 frequency = %q %q %q", first.Freq, first.Offset, first.Tone)
			}
			if first.ElevMeters != 250 {
				t.Errorf("row 1 elevation = %v, want 250", first.ElevMeters)
			}
			if records[1].ID != "ZZ-TEST-01" || records[1].City != "Testville" {
				t.Errorf("row 2 = %+v, want the new test row", records[1])
			}
		})
	}
}

// TestParseTowersCSVSkipsMalformedRowsAndSaysSo asserts one bad row cannot take
// the whole file down: the readable rows survive and each rejection is named, so
// an operator who exported a dataset with the wrong column order can see why
// most of it is missing.
func TestParseTowersCSVSkipsMalformedRowsAndSaysSo(t *testing.T) {
	t.Parallel()

	text := `id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev
GOOD-01,Good Ridge,RPT,39.0000,-120.0000,147.000 MHz,+0.6 MHz,100.0 Hz,W6TEST,Testville,CA,US,1200
SHORT-01,Too Few Columns,RPT,39.0,-120.0
BADLAT-01,Bad Latitude,RPT,north,-120.0000,147.000 MHz,+0.6 MHz,100.0 Hz,W6,Testville,CA,US,10
BADC-01,Out Of Range,CELL,91.0000,-120.0000,Band 2,,,Carrier,Testville,CA,US,10
BADTYPE-01,Unknown Service,TELEPORT,39.0000,-120.0000,147.000 MHz,,,W6,Testville,CA,US,10
BADCOUNTRY-01,No Country,RPT,39.0000,-120.0000,147.000 MHz,+0.6 MHz,100.0 Hz,W6,Testville,CA,USX,10
`
	records, warnings, err := parseTowersCSV(strings.NewReader(text))
	if err != nil {
		t.Fatalf("parseTowersCSV: %v", err)
	}
	if len(records) != 1 || records[0].ID != "GOOD-01" {
		t.Fatalf("records = %+v, want only GOOD-01", records)
	}
	if len(warnings) != 5 {
		t.Errorf("warnings = %v, want 5 (one per rejected row)", warnings)
	}
	for _, want := range []string{"SHORT-01", "BADLAT-01", "BADC-01", "BADTYPE-01", "BADCOUNTRY-01"} {
		found := slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, want) })
		if !found {
			t.Errorf("no warning names %v: %v", want, warnings)
		}
	}
}

// TestTowerCatalogMergesALocalDataset asserts a local towers.csv is merged over
// the embedded catalog: a row with an embedded identifier overrides that row in
// place, and a new identifier is appended, so every query sees both.
func TestTowerCatalogMergesALocalDataset(t *testing.T) {
	t.Parallel()

	path := writeTowersCSV(t, towerTestCSV)
	reg, session, _ := commandFixture(t, towerConfigWithCSV(path))

	// The overriding row replaces the embedded Sutro entry.
	lines := runLines(t, reg, session, "tower info W6PW-2M")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Local Sutro Repeater") {
		t.Errorf("info = %v, want the local override", lines)
	}
	if strings.Contains(joined, "145.150 MHz") {
		t.Errorf("info = %v, want the embedded frequency replaced", lines)
	}
	if !strings.Contains(joined, "146.100 MHz") {
		t.Errorf("info = %v, want the local frequency", lines)
	}

	// The new row is searchable and findable by proximity.
	if lines := runLines(t, reg, session, "tower search testville"); len(lines) == 0 ||
		!strings.Contains(strings.Join(lines, "\n"), "ZZ-TEST-01") {
		t.Errorf("search testville = %v, want the local row", lines)
	}
	near := runLines(t, reg, session, "tower near 39.0000,-120.0000")
	if !strings.Contains(strings.Join(near, "\n"), "ZZ-TEST-01") {
		t.Errorf("near 39,-120 = %v, want the local row first", near)
	}
}

// TestTowerCatalogMergesWithoutADataset asserts the embedded catalog answers
// when no local dataset exists, and when the configured path names a file that
// is not there: an absent towers.csv is a normal configuration, not an error.
func TestTowerCatalogMergesWithoutADataset(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(tempDir(t), "not-there.csv")
	reg, session, _ := commandFixture(t, towerConfigWithCSV(missing))
	lines := runLines(t, reg, session, "tower info W6PW-2M")
	if !strings.Contains(strings.Join(lines, "\n"), "Sutro Tower") {
		t.Errorf("info = %v, want the embedded row", lines)
	}
}

// TestTowerStoreKeepsTheEmbeddedCatalogOnABadDataset asserts an unreadable local
// dataset degrades to the embedded catalog instead of answering nothing: a
// field tool must never lose its built-in data because of a bad export.
func TestTowerStoreKeepsTheEmbeddedCatalogOnABadDataset(t *testing.T) {
	t.Parallel()

	// A directory in place of a file is the simplest unreadable dataset.
	dir := tempDir(t)
	records, _ := mergeTowerRecords(towerRecords, dir)
	if len(records) != len(towerRecords) {
		t.Errorf("merged %v rows from an unreadable path, want the %v embedded rows",
			len(records), len(towerRecords))
	}
}

// TestTowerNearReportsDistanceBearingAndFrequency asserts the proximity answer is
// the shape an operator acts on: a distance in kilometers, a bearing and compass
// point, the service, the frequency with its offset and tone, and the place.
func TestTowerNearReportsDistanceBearingAndFrequency(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	lines := runLines(t, reg, session, "tower near 37.7553,-122.4527")
	if len(lines) < 3 {
		t.Fatalf("tower near = %v, want a header, rows, and a footer", lines)
	}
	if want := "Tower sites near 37.7553,-122.4527 (Page 1 of 1):"; lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	first := lines[1]
	for _, want := range []string{
		"W6PW-2M", "(0.0 km", "[RPT]:", "145.150 MHz -0.6 (PL 114.8)", "Sutro Tower, San Francisco, CA",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("row = %q, want it to contain %q", first, want)
		}
	}
	if last := lines[len(lines)-1]; last != "[Page 1 of 1: end of results]" {
		t.Errorf("footer = %q, want the end-of-results line", last)
	}
}

// TestTowerNearInChinaAlsoPrintsTheMarsCoordinate asserts the international
// parity requirement for a proximity answer: a site in China additionally prints
// the GCJ-02 coordinate, so the operator can paste it into Amap/WeChat, while a
// site outside China prints no such line.
func TestTowerNearInChinaAlsoPrintsTheMarsCoordinate(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())

	china := runLines(t, reg, session, "tower near 39.9055,116.3976")
	joined := strings.Join(china, "\n")
	if !strings.Contains(joined, "GCJ-02") {
		t.Errorf("near Beijing = %v, want a GCJ-02 line", china)
	}
	if !strings.Contains(joined, "Amap") {
		t.Errorf("near Beijing = %v, want the line to name the map apps", china)
	}
	if !strings.Contains(joined, "BJ-RPT-03") {
		t.Errorf("near Beijing = %v, want a Beijing site", china)
	}

	us := runLines(t, reg, session, "tower near 37.7553,-122.4527")
	if strings.Contains(strings.Join(us, "\n"), "GCJ-02") {
		t.Errorf("near San Francisco = %v, want no GCJ-02 line", us)
	}
}

// TestTowerNearAcceptsEveryNotationAndCatalogName asserts the proximity argument
// is resolved the same way every other catalog resolves one: a coordinate pair,
// a Plus Code, a Maidenhead grid, a Chinese Mars coordinate, and the name or
// identifier of a site in the catalog itself.
func TestTowerNearAcceptsEveryNotationAndCatalogName(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	tests := []struct {
		name    string
		arg     string
		wantRow string
	}{
		{name: "decimal degrees", arg: "37.7553,-122.4527", wantRow: "W6PW-2M"},
		{name: "plus code", arg: "849VCWC8+R9", wantRow: "LP-2M"},
		{name: "maidenhead grid", arg: "CM87ss", wantRow: "W6PW-2M"},
		{name: "mars coordinate", arg: "gcj:39.9069,116.4038", wantRow: "BJ-RPT-03"},
		{name: "site id", arg: "W6PW-2M", wantRow: "W6PW-2M"},
		{name: "site name", arg: "sutro", wantRow: "W6PW-2M"},
		{name: "city name", arg: "beijing", wantRow: "BJ-RPT-03"},
		{name: "pinyin province city", arg: "chengdu", wantRow: "SC-RPT-01"},
		{name: "airfield from the metar catalog", arg: "KDEN", wantRow: "LK-2M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := runLines(t, reg, session, "tower near "+tt.arg)
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tt.wantRow) {
				t.Errorf("tower near %v = %v, want it to name %v", tt.arg, lines, tt.wantRow)
			}
		})
	}
}

// TestTowerNearRejectsAnUnknownPlace asserts an argument that is not a place
// produces an actionable refusal rather than three arbitrary rows.
func TestTowerNearRejectsAnUnknownPlace(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	lines := runLines(t, reg, session, "tower near nowhere-at-all-xyzzy")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "not a place") {
		t.Errorf("tower near unknown = %v, want a refusal", lines)
	}
	if strings.Contains(joined, "[RPT]") {
		t.Errorf("tower near unknown = %v, want no rows", lines)
	}
}

// TestTowerSearchFindsASiteByEveryHandle asserts the offline search reaches a
// site by callsign, site name, city, frequency, operator, and province name.
func TestTowerSearchFindsASiteByEveryHandle(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "callsign", query: "W6PW", want: "W6PW-2M"},
		{name: "site name", query: "sutro", want: "W6PW-2M"},
		{name: "us city", query: "san francisco", want: "W6PW-2M"},
		{name: "us state name", query: "colorado", want: "LK-2M"},
		{name: "frequency", query: "145.150", want: "W6PW-2M"},
		{name: "pinyin city", query: "beijing", want: "BJ-RPT-01"},
		{name: "pinyin province", query: "sichuan", want: "SC-RPT-01"},
		{name: "carrier", query: "china mobile", want: "CN-BJ-T001"},
		{name: "international city", query: "tokyo", want: "JP-TKY-01"},
		{name: "service word", query: "maritime", want: "US-CA-M001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := runLines(t, reg, session, "tower search "+tt.query)
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tt.want) {
				t.Errorf("tower search %v = %v, want it to include %v", tt.query, lines, tt.want)
			}
			if !strings.Contains(lines[0], "Tower sites matching") {
				t.Errorf("tower search %v header = %q, want a matching header", tt.query, lines[0])
			}
		})
	}
}

// TestTowerSearchPaginatesAndMoreContinues asserts a long search answer is cut
// into pages that fit the reply budget, that the footer names the exact next
// command, and that "more" reaches the page after the one just sent.
func TestTowerSearchPaginatesAndMoreContinues(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	first := runLines(t, reg, session, "tower search relay")
	if len(first) < 3 {
		t.Fatalf("tower search relay = %v, want a paged answer", first)
	}
	if !strings.Contains(first[0], "(Page 1 of ") {
		t.Fatalf("header = %q, want a page count", first[0])
	}
	if !strings.Contains(first[len(first)-1], " for next]") {
		t.Fatalf("footer = %q, want a next-page command", first[len(first)-1])
	}

	second := runLines(t, reg, session, "more")
	if len(second) == 0 || !strings.Contains(second[0], "(Page 2 of ") {
		t.Fatalf("more = %v, want page 2", second)
	}
	if second[0] == first[0] {
		t.Error("more repeated the page it followed")
	}

	// A page past the end is named, not silently clamped.
	past := runLines(t, reg, session, "tower search relay 999")
	if len(past) != 1 || !strings.Contains(past[0], "not found") {
		t.Errorf("page 999 = %v, want a not-found line", past)
	}
}

// TestTowerSearchAndListRejectEmptyQueries asserts a bare search or list prints
// its usage rather than a page of arbitrary rows.
func TestTowerSearchAndListRejectEmptyQueries(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	for _, line := range []string{"tower search", "tower info", "tower near"} {
		lines := runLines(t, reg, session, line)
		if len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "Usage:") {
			t.Errorf("%v = %v, want a usage line", line, lines)
		}
	}
}

// TestTowerListFiltersByCountryAndRegion asserts the list filter reaches a whole
// country (US, CN), a country by name (china), a US state (CA) and its name
// (california), and a Chinese province (BJ, GD, SC).
func TestTowerListFiltersByCountryAndRegion(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	tests := []struct {
		name      string
		arg       string
		wantRow   string
		wantInAll []string
	}{
		{name: "united states", arg: "US", wantRow: "W6PW-2M"},
		{name: "china", arg: "CN", wantRow: "BJ-RPT-01"},
		{name: "china by name", arg: "china", wantRow: "BJ-RPT-01"},
		{name: "california", arg: "CA", wantRow: "W6PW-2M"},
		{name: "california by name", arg: "california", wantRow: "W6PW-2M"},
		{name: "beijing", arg: "BJ", wantRow: "BJ-RPT-01"},
		{name: "guangdong", arg: "GD", wantRow: "GD-RPT-01"},
		{name: "sichuan shares the code with south carolina", arg: "SC", wantRow: "SC-RPT-01"},
		{name: "colorado", arg: "CO", wantRow: "LK-2M"},
		{name: "japan", arg: "JP", wantRow: "JP-TKY-01"},
		{name: "canada by name", arg: "canada", wantRow: "CA-TOR-01"},
		{name: "germany by name", arg: "germany", wantRow: "DE-BER-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := runLines(t, reg, session, "tower list "+tt.arg)
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tt.wantRow) {
				t.Errorf("tower list %v = %v, want it to include %v", tt.arg, lines, tt.wantRow)
			}
			if !strings.Contains(lines[0], "Tower sites in ") {
				t.Errorf("tower list %v header = %q, want a list header", tt.arg, lines[0])
			}
		})
	}

	// A filter that is a US state code must not silently become a country: CA is
	// California, and Canada is reached by name.
	california := runLines(t, reg, session, "tower list CA")
	if strings.Contains(strings.Join(california, "\n"), "CN Tower Relay") {
		t.Errorf("tower list CA = %v, want California rather than Canada", california)
	}
}

// TestTowerListRejectsAnUnknownFilter asserts a filter that matches nothing says
// so and names the ways to widen it.
func TestTowerListRejectsAnUnknownFilter(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	lines := runLines(t, reg, session, "tower list ZZ")
	if len(lines) == 0 || !strings.Contains(lines[0], "No Tower sites in ZZ") {
		t.Errorf("tower list ZZ = %v, want a no-results line", lines)
	}
}

// TestTowerListPagesWithTheNextCommand asserts a country-sized list is paginated
// and that the footer quotes the exact command for the next page.
func TestTowerListPagesWithTheNextCommand(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	lines := runLines(t, reg, session, "tower list US")
	if len(lines) < 4 {
		t.Fatalf("tower list US = %v, want a header, rows, and a footer", lines)
	}
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, `"tower list US 2"`) {
		t.Errorf("footer = %q, want it to quote the next-page command", footer)
	}
	page2 := runLines(t, reg, session, "tower list US 2")
	if len(page2) == 0 || !strings.Contains(page2[0], "(Page 2 of ") {
		t.Errorf("tower list US 2 = %v, want page 2", page2)
	}
}

// TestTowerInfoRendersEveryField asserts the detailed single-site answer carries
// the identifier, the service, the place, the exact WGS-84 position, the
// Maidenhead grid, the elevation, the frequency, the offset, the tone, and the
// operator.
func TestTowerInfoRendersEveryField(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	lines := runLines(t, reg, session, "tower info W6PW-2M")
	if len(lines) != 3 {
		t.Fatalf("tower info = %v, want three lines", lines)
	}
	if want := "W6PW-2M [RPT]: Sutro Tower, San Francisco, CA"; lines[0] != want {
		t.Errorf("line 1 = %q, want %q", lines[0], want)
	}
	for _, want := range []string{
		"WGS-84", "37.755300, -122.452700", "CM87", "elevation 254 m", "Amateur repeater",
	} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("line 2 = %q, want it to contain %q", lines[1], want)
		}
	}
	for _, want := range []string{"145.150 MHz", "offset -0.6 MHz", "tone 114.8 Hz", "operator W6PW"} {
		if !strings.Contains(lines[2], want) {
			t.Errorf("line 3 = %q, want it to contain %q", lines[2], want)
		}
	}
}

// TestTowerInfoForAChineseSiteAddsTheMarsCoordinate asserts the detail view
// carries both datums for a site inside China, which is what lets an operator
// cross-check a coordinate against a Chinese map app.
func TestTowerInfoForAChineseSiteAddsTheMarsCoordinate(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	lines := runLines(t, reg, session, "tower info BJ-RPT-01")
	if len(lines) != 4 {
		t.Fatalf("tower info = %v, want four lines", lines)
	}
	if want := "BJ-RPT-01 [RPT]: Xiangshan Relay, Beijing, BJ, CN"; lines[0] != want {
		t.Errorf("line 1 = %q, want %q", lines[0], want)
	}
	if !strings.Contains(lines[1], "WGS-84") || !strings.Contains(lines[1], "39.993600, 116.188300") {
		t.Errorf("line 2 = %q, want the WGS-84 position", lines[1])
	}
	if !strings.Contains(lines[2], "GCJ-02") {
		t.Errorf("line 3 = %q, want a GCJ-02 position", lines[2])
	}
	lat, lng := WGS84ToGCJ02(39.9936, 116.1883)
	if !strings.Contains(lines[2], FormatLatLng(LatLng{Lat: lat, Lng: lng})) {
		t.Errorf("line 3 = %q, want the Mars coordinate %v,%v", lines[2], lat, lng)
	}

	us := runLines(t, reg, session, "tower info W6PW-2M")
	if strings.Contains(strings.Join(us, "\n"), "GCJ-02") {
		t.Errorf("tower info for a US site = %v, want no GCJ-02 line", us)
	}
}

// TestTowerInfoAcceptsANameAndRefusesAnUnknownID asserts the detail view resolves
// a site name as well as an identifier, and that an identifier it does not know
// produces an actionable refusal.
func TestTowerInfoAcceptsANameAndRefusesAnUnknownID(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())
	byName := runLines(t, reg, session, "tower info sutro")
	if !strings.Contains(strings.Join(byName, "\n"), "W6PW-2M") {
		t.Errorf("tower info sutro = %v, want the Sutro row", byName)
	}
	unknown := runLines(t, reg, session, "tower info ZZ-NOPE-99")
	joined := strings.Join(unknown, "\n")
	if !strings.Contains(joined, "ZZ-NOPE-99") || !strings.Contains(joined, "tower search") {
		t.Errorf("tower info unknown = %v, want a refusal naming the search command", unknown)
	}
}

// TestTowerBareForms asserts the two useful shorthands: a bare identifier shows
// the site, a bare position answers with the three closest sites, and anything
// else prints the usage rather than guessing.
func TestTowerBareForms(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, towerConfig())

	byID := runLines(t, reg, session, "tower W6PW-2M")
	if !strings.Contains(strings.Join(byID, "\n"), "Sutro Tower") {
		t.Errorf("tower W6PW-2M = %v, want the detail view", byID)
	}

	byPoint := runLines(t, reg, session, "tower 37.7553,-122.4527")
	if !strings.Contains(strings.Join(byPoint, "\n"), "Sutro Tower") {
		t.Errorf("tower <coords> = %v, want the nearest sites", byPoint)
	}

	unknown := runLines(t, reg, session, "tower x")
	joined := strings.Join(unknown, "\n")
	if !strings.Contains(joined, "Usage: "+towerUsage) {
		t.Errorf("tower x = %v, want the usage", unknown)
	}

	bare := runLines(t, reg, session, "tower")
	if len(bare) != 2 || bare[0] != "Usage: "+towerUsage {
		t.Errorf("tower = %v, want the usage and its hint", bare)
	}
}

// TestTowerIsRegisteredWithItsAliases asserts the command, its three aliases,
// their shared handler, and the help text an operator sees.
func TestTowerIsRegisteredWithItsAliases(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, towerConfig())
	for _, alias := range []string{"repeater", "cell", "mast"} {
		if got := reg.aliases[alias]; got != "tower" {
			t.Errorf("aliases[%v] = %q, want tower", alias, got)
		}
	}
	for _, name := range []string{"tower", "repeater", "cell", "mast"} {
		cmd, ok := reg.byName[name]
		if !ok {
			t.Fatalf("command %q is not registered", name)
		}
		if cmd.usage != towerUsage {
			t.Errorf("%v usage = %q, want %q", name, cmd.usage, towerUsage)
		}
		if len(cmd.detail) == 0 {
			t.Errorf("%v has no detail for help to print", name)
		}
	}

	reg2, session, _ := commandFixture(t, towerConfig())
	for _, name := range []string{"tower", "repeater", "cell", "mast"} {
		lines := runLines(t, reg2, session, "help "+name)
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, towerUsage) {
			t.Errorf("help %v = %v, want the usage", name, lines)
		}
	}

	// Each alias answers exactly as the primary command does.
	for _, name := range []string{"repeater", "cell", "mast"} {
		lines := runLines(t, reg2, session, name+" near 37.7553,-122.4527")
		if !strings.Contains(strings.Join(lines, "\n"), "W6PW-2M") {
			t.Errorf("%v near = %v, want the same answer as tower", name, lines)
		}
	}
}

// TestTowerRepliesFitTheReplyBudget asserts a tower page is sized from the
// operator's max_reply_lines, exactly as every other catalog page is.
func TestTowerRepliesFitTheReplyBudget(t *testing.T) {
	t.Parallel()

	cfg := towerConfig()
	cfg.MaxReplyLines = 4
	reg, session, _ := commandFixture(t, cfg)
	lines := runLines(t, reg, session, "tower list US")
	if len(lines) > cfg.MaxReplyLines {
		t.Errorf("tower list US = %v lines, want at most %v", len(lines), cfg.MaxReplyLines)
	}
}

// TestRelativeBearingWrapsIntoPlusMinus180 asserts the angular difference
// between a target bearing and a heading is reported as a signed turn: positive
// to the right, negative to the left, and never more than half a turn, so the
// operator is always told the shorter way round.
func TestRelativeBearingWrapsIntoPlusMinus180(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		target  float64
		heading float64
		want    float64
	}{
		{"dead ahead", 42, 42, 0},
		{"a little right", 42, 27, 15},
		{"a little left", 42, 57, -15},
		{"due right", 90, 0, 90},
		{"due left", 270, 0, -90},
		{"straight behind", 180, 0, -180},
		{"behind the other way", 0, 180, -180},
		{"a full turn is no turn", 30, 390, 0},
		{"across the zero crossing", 10, 350, 20},
		{"across the zero crossing the other way", 350, 10, -20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RelativeBearingOf(tc.target, tc.heading)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("RelativeBearingOf(%v, %v) = %v, want %v", tc.target, tc.heading, got, tc.want)
			}
			if got < -180 || got > 180 {
				t.Errorf("RelativeBearingOf(%v, %v) = %v, want a signed half turn", tc.target, tc.heading, got)
			}
		})
	}
}

// TestClockPositionNamesTheClockFace asserts the clock position an operator
// reads is a real clock face: twelve straight ahead, three to the right, six
// behind, and nine to the left.
func TestClockPositionNamesTheClockFace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		delta float64
		want  int
	}{
		{0, 12}, {15, 1}, {30, 1}, {44, 1}, {45, 2}, {60, 2}, {90, 3},
		{120, 4}, {150, 5}, {180, 6}, {-15, 11}, {-45, 10}, {-90, 9},
		{-120, 8}, {-150, 7}, {-180, 6},
	}
	for _, tc := range cases {
		if got := ClockPositionOf(tc.delta); got != tc.want {
			t.Errorf("ClockPositionOf(%v) = %v, want %v", tc.delta, got, tc.want)
		}
	}
}

// TestFormatSteeringInstructionAcrossTheFourQuadrants asserts the four things a
// steering instruction may say — ahead, a graded right turn, a graded left
// turn, and behind — and pins the Phase 0.6 worked example, a fifteen-degree
// right turn at one o'clock.
func TestFormatSteeringInstructionAcrossTheFourQuadrants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		target  float64
		heading float64
		want    string
	}{
		{"dead ahead", 42, 42, "[Ahead · 12 o'clock]"},
		{"two degrees off is still ahead", 42, 40, "[Ahead · 12 o'clock]"},
		{"the ahead boundary", 42, 37, "[Ahead · 12 o'clock]"},
		{"the brief's worked example", 42, 27, "[Turn 15° RIGHT · 1 o'clock]"},
		{"due right", 90, 0, "[Turn 90° RIGHT · 3 o'clock]"},
		{"due left", 270, 0, "[Turn 90° LEFT · 9 o'clock]"},
		{"a little left", 42, 60, "[Turn 18° LEFT · 11 o'clock]"},
		{"the near-behind boundary", 165, 0, "[Turn 165° RIGHT · 6 o'clock]"},
		{"just past behind, right", 166, 0, "[Behind · 6 o'clock]"},
		{"just past behind, left", 194, 0, "[Behind · 6 o'clock]"},
		{"straight behind", 180, 0, "[Behind · 6 o'clock]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatSteeringInstruction(tc.target, tc.heading); got != tc.want {
				t.Errorf("FormatSteeringInstruction(%v, %v) = %q, want %q",
					tc.target, tc.heading, got, tc.want)
			}
		})
	}
}

// towerSteeringFixture builds a tower-catalog session whose device holds a live
// fix and a compass heading. A reading that already carries true north is used
// exactly as given; a magnetic-only reading is left uncorrected when the reader
// is not wired to the fix, which is what a node with no position source looks
// like.
func towerSteeringFixture(t *testing.T, heading CompassHeading, wireFix bool) (*registry, *hubSession) {
	t.Helper()
	reg, session := whereamiFixture(t, sfFix())
	if !heading.Valid {
		return reg, session
	}
	reader := NewCompassReader(nil)
	reader.SetHeading(heading)
	if wireFix {
		reader.SetLocationSource(reg.currentFix)
	}
	t.Cleanup(func() { _ = reader.Close() })
	reg.compass = reader
	return reg, session
}

// sutroTargetBearing returns the true bearing from the reference fix to the
// nearest catalog site, which is the row a proximity answer puts first.
func sutroTargetBearing(t *testing.T) float64 {
	t.Helper()
	record, ok := towerRecordByID(towerRecords, "W6PW-2M")
	if !ok {
		t.Fatal("the embedded catalog no longer holds W6PW-2M")
	}
	return InitialBearing(sfFix().Position(), LatLng{Lat: record.Lat, Lng: record.Lng})
}

// TestTowerNearSteersTheAntennaWithALiveHeading asserts the direction-finding
// payoff: with a live fix and a true heading, a proximity answer stops being a
// bearing to interpret and becomes a turn to make.
func TestTowerNearSteersTheAntennaWithALiveHeading(t *testing.T) {
	t.Parallel()

	target := sutroTargetBearing(t)
	reg, session := towerSteeringFixture(t, CompassHeading{
		Valid: true, HasMagnetic: true, MagneticDeg: normalizeDegrees(target - 15),
		HasTrue: true, TrueDeg: normalizeDegrees(target - 15), HasDeclination: true, DeclinationDeg: 0,
	}, false)
	lines := runLines(t, reg, session, "tower near")
	first := lines[1]
	if !strings.Contains(first, "[Turn 15° RIGHT · 1 o'clock]") {
		t.Errorf("tower near with a heading = %q, want the one-o'clock steering", first)
	}
	if !strings.Contains(first, "True") {
		t.Errorf("tower near with a heading = %q, want the bearing named as true", first)
	}

	reg, session = towerSteeringFixture(t, CompassHeading{
		Valid: true, HasMagnetic: true, MagneticDeg: target,
		HasTrue: true, TrueDeg: target, HasDeclination: true, DeclinationDeg: 0,
	}, false)
	if first := runLines(t, reg, session, "tower near")[1]; !strings.Contains(first, "[Ahead · 12 o'clock]") {
		t.Errorf("tower near aimed at the site = %q, want it reported as ahead", first)
	}
}

// TestTowerNearOmitsSteeringWithoutACompass asserts the answer an operator with
// no compass gets is exactly the answer the command gave before the compass
// existed: a distance and a bearing, with nothing invented.
func TestTowerNearOmitsSteeringWithoutACompass(t *testing.T) {
	t.Parallel()

	reg, session := towerSteeringFixture(t, CompassHeading{}, false)
	first := runLines(t, reg, session, "tower near")[1]
	if strings.Contains(first, "o'clock") {
		t.Errorf("tower near without a compass = %q, want no steering", first)
	}
	if strings.Contains(first, "True") {
		t.Errorf("tower near without a compass = %q, want the plain bearing", first)
	}
}

// TestTowerNearOmitsSteeringForANamedPlace asserts a proximity answer about
// somewhere else never steers: the operator is not standing at the place they
// asked about, so a relative turn from their own heading would be nonsense.
func TestTowerNearOmitsSteeringForANamedPlace(t *testing.T) {
	t.Parallel()

	target := sutroTargetBearing(t)
	reg, session := towerSteeringFixture(t, CompassHeading{
		Valid: true, HasTrue: true, TrueDeg: normalizeDegrees(target - 15),
		HasMagnetic: true, MagneticDeg: normalizeDegrees(target - 15),
	}, false)
	first := runLines(t, reg, session, "tower near "+refPlus10)[1]
	if strings.Contains(first, "o'clock") {
		t.Errorf("tower near a named place = %q, want no steering", first)
	}
}

// TestTowerNearOmitsSteeringForAMagneticOnlyHeading asserts a heading that has
// not been corrected to true north is never steered by: the target bearing is a
// true bearing, and mixing the two frames would aim the operator off by the
// local variation.
func TestTowerNearOmitsSteeringForAMagneticOnlyHeading(t *testing.T) {
	t.Parallel()

	target := sutroTargetBearing(t)
	reg, session := towerSteeringFixture(t, CompassHeading{
		Valid: true, HasMagnetic: true, MagneticDeg: normalizeDegrees(target - 15),
	}, false)
	first := runLines(t, reg, session, "tower near")[1]
	if strings.Contains(first, "o'clock") {
		t.Errorf("tower near with an uncorrected heading = %q, want no steering", first)
	}
}
