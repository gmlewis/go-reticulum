// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// A mock upstream whose snapshot a test can swap between runs
// ---------------------------------------------------------------------------

// feedSnapshot is one complete upstream snapshot: the raw bytes of each dataset.
type feedSnapshot struct {
	buoys string
	tides string
	metar string
}

// serveFeed points the tool's upstream URLs at a mock server and returns a
// function that replaces the snapshot the server hands out. The URLs are
// restored when the test ends.
func serveFeed(t *testing.T, first feedSnapshot) func(feedSnapshot) {
	t.Helper()

	var mu sync.Mutex
	current := first
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		snapshot := current
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "activestations.xml"):
			_, _ = w.Write([]byte(snapshot.buoys))
		case strings.HasSuffix(r.URL.Path, "stations.json"):
			_, _ = w.Write([]byte(snapshot.tides))
		case strings.HasSuffix(r.URL.Path, "airports.csv"):
			_, _ = w.Write([]byte(snapshot.metar))
		case strings.HasSuffix(r.URL.Path, "WMM.COF"):
			_, _ = w.Write([]byte(`<html>WMM2025 is valid until December 31, 2029</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	saved := [...]string{defaultNDBCURL, defaultCOOPSURL, defaultOurAirportsURL, defaultWMMURL}
	defaultNDBCURL = server.URL + "/activestations.xml"
	defaultCOOPSURL = server.URL + "/stations.json"
	defaultOurAirportsURL = server.URL + "/airports.csv"
	defaultWMMURL = server.URL + "/WMM.COF"
	t.Cleanup(func() {
		defaultNDBCURL, defaultCOOPSURL = saved[0], saved[1]
		defaultOurAirportsURL, defaultWMMURL = saved[2], saved[3]
	})

	return func(next feedSnapshot) {
		mu.Lock()
		current = next
		mu.Unlock()
	}
}

// repoFixture makes the empty repository layout one run writes into.
func repoFixture(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "bot"), 0o755); err != nil {
		t.Fatalf("failed to create the bot directory: %v", err)
	}
	return dir
}

// generatedTables reads back the three generated tables of a fixture.
func generatedTables(t *testing.T, repoDir string) map[string]string {
	t.Helper()
	out := make(map[string]string, 3)
	for _, name := range []string{"buoy-stations.go", "tide-stations.go", "metar-stations.go"} {
		data, err := os.ReadFile(filepath.Join(repoDir, "bot", name))
		if err != nil {
			t.Fatalf("failed to read %v: %v", name, err)
		}
		out[name] = string(data)
	}
	return out
}

// runTool runs the tool against a fixture and returns its report.
func runTool(t *testing.T, args []string, repoDir string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, http.DefaultClient, repoDir); code != exitOK {
		t.Fatalf("run(%v) = %v, want %v (stderr: %v)", args, code, exitOK, stderr.String())
	}
	return stdout.String()
}

// ---------------------------------------------------------------------------
// Snapshot builders
// ---------------------------------------------------------------------------

func buoyFeed(rows ...string) string {
	return "<stations>\n" + strings.Join(rows, "\n") + "\n</stations>"
}

func buoyRow(id, name string, lat, lon float64) string {
	return fmt.Sprintf(`<station id=%q lat="%v" lon="%v" name=%q met="y"/>`, id, lat, lon, name)
}

func tideFeed(rows ...string) string {
	return `{"stations":[` + strings.Join(rows, ",") + `]}`
}

func tideRow(id, name, state string, lat, lng float64) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"state":%q,"lat":%v,"lng":%v}`, id, name, state, lat, lng)
}

const metarHeader = "ident,type,name,latitude_deg,longitude_deg,iso_country," +
	"iso_region,municipality,scheduled_service,iata_code\n"

func metarFeed(rows ...string) string {
	return metarHeader + strings.Join(rows, "\n") + "\n"
}

func metarAirfield(ident, typ, name, region, city, iata string, lat, lng float64) string {
	country := region
	if idx := strings.IndexByte(region, '-'); idx > 0 {
		country = region[:idx]
	}
	return fmt.Sprintf("%v,%v,%v Airport,%v,%v,%v,%v,%v,yes,%v",
		ident, typ, name, lat, lng, country, region, city, iata)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestGeneratedTablesIgnoreUpstreamRowOrder is the regression test for the
// report that the generated tables kept changing order from one run to the next.
// The bytes written must be a pure function of the *set* of rows upstream
// carries, so a feed that lists the same stations in a different order must
// regenerate byte-identical files.
func TestGeneratedTablesIgnoreUpstreamRowOrder(t *testing.T) {
	buoys := []string{
		buoyRow("41002", "South Hatteras", 31.743, -74.955),
		buoyRow("41004", "Edisto, SC", 32.502, -79.099),
		buoyRow("44013", "Boston, MA", 42.346, -70.651),
	}
	tides := []string{
		tideRow("9410135", "South San Diego Bay", "CA", 32.629101, -117.107803),
		tideRow("9410660", "Los Angeles", "CA", 33.72, -118.272),
		tideRow("8443970", "Boston", "MA", 42.353889, -71.050278),
	}
	airfields := []string{
		metarAirfield("KDEN", "large_airport", "Denver International", "US-CO", "Denver", "DEN", 39.861698, -104.672997),
		metarAirfield("KASE", "medium_airport", "Aspen-Pitkin County", "US-CO", "Aspen", "ASE", 39.223202, -106.868896),
		metarAirfield("KEGE", "medium_airport", "Eagle County Regional", "US-CO", "Eagle", "EGE", 39.642601, -106.917999),
	}

	// The same three stations of each dataset, arriving in three different
	// orders. A feed that reshuffles itself is exactly what a live upstream
	// snapshot does.
	snapshots := []feedSnapshot{
		{buoys: buoyFeed(buoys...), tides: tideFeed(tides...), metar: metarFeed(airfields...)},
		{
			buoys: buoyFeed(buoys[2], buoys[1], buoys[0]),
			tides: tideFeed(tides[1], tides[2], tides[0]),
			metar: metarFeed(airfields[1], airfields[0], airfields[2]),
		},
		{
			buoys: buoyFeed(buoys[1], buoys[2], buoys[0]),
			tides: tideFeed(tides[2], tides[0], tides[1]),
			metar: metarFeed(airfields[2], airfields[1], airfields[0]),
		},
	}

	setSnapshot := serveFeed(t, snapshots[0])
	var want map[string]string
	for i, snapshot := range snapshots {
		setSnapshot(snapshot)
		repoDir := repoFixture(t)
		runTool(t, nil, repoDir)
		got := generatedTables(t, repoDir)
		if i == 0 {
			want = got
			continue
		}
		for name, content := range got {
			if content != want[name] {
				t.Errorf("arrival order %v regenerated %v differently:\ngot:\n%v\nwant:\n%v",
					i, name, content, want[name])
			}
		}
	}

	// The canonical order is by station id, and the tables are still whole.
	if !strings.Contains(want["buoy-stations.go"], `{"41002"`) {
		t.Error("the buoy table lost its station rows")
	}
	// A second run over the very same snapshot must not report an update.
	setSnapshot(snapshots[0])
	repoDir := repoFixture(t)
	runTool(t, nil, repoDir)
	out := runTool(t, []string{"-n"}, repoDir)
	for _, name := range []string{"buoy-stations.go", "tide-stations.go", "metar-stations.go"} {
		if !strings.Contains(out, "[UP TO DATE] bot/"+name) {
			t.Errorf("a second run was not idempotent for %v: %v", name, out)
		}
	}
}

// TestUpstreamDuplicateRowsAreCollapsedDeterministically asserts a feed that
// lists one station twice cannot produce two table rows, and that which of the
// two survives does not depend on the order the feed used.
func TestUpstreamDuplicateRowsAreCollapsedDeterministically(t *testing.T) {
	first := buoyRow("41002", "South Hatteras", 31.743, -74.955)
	second := buoyRow("41002", "South Hatteras (IOOS)", 31.743, -74.955)
	tideFirst := tideRow("9410135", "South San Diego Bay", "CA", 32.629101, -117.107803)
	tideSecond := tideRow("9410135", "San Diego Bay", "CA", 32.629101, -117.107803)
	// The same station code twice, once as a major hub and once as a regional
	// field: the airfield table is ordered by importance, so these two rows are
	// not adjacent once sorted, and a plain "drop adjacent duplicates" pass would
	// keep both.
	major := metarAirfield("KDEN", "large_airport", "Denver International", "US-CO", "Denver", "DEN", 39.861698, -104.672997)
	reliever := metarAirfield("KDEN", "medium_airport", "Denver Reliever", "US-CO", "Denver", "DEN", 39.861698, -104.672997)
	filler := metarAirfield("KASE", "medium_airport", "Aspen-Pitkin County", "US-CO", "Aspen", "ASE", 39.223202, -106.868896)

	snapshots := []feedSnapshot{
		{
			buoys: buoyFeed(first, second),
			tides: tideFeed(tideFirst, tideSecond),
			metar: metarFeed(major, filler, reliever),
		},
		{
			buoys: buoyFeed(second, first),
			tides: tideFeed(tideSecond, tideFirst),
			metar: metarFeed(reliever, filler, major),
		},
	}

	setSnapshot := serveFeed(t, snapshots[0])
	var want map[string]string
	for i, snapshot := range snapshots {
		setSnapshot(snapshot)
		repoDir := repoFixture(t)
		runTool(t, nil, repoDir)
		got := generatedTables(t, repoDir)

		if n := strings.Count(got["buoy-stations.go"], `{"41002"`); n != 1 {
			t.Errorf("arrival order %v emitted %v rows for buoy 41002, want 1", i, n)
		}
		if n := strings.Count(got["tide-stations.go"], `{"9410135"`); n != 1 {
			t.Errorf("arrival order %v emitted %v rows for tide 9410135, want 1", i, n)
		}
		// Exactly one KDEN row survives, and it is the major-airport one.
		if n := strings.Count(got["metar-stations.go"], `{"KDEN"`); n != 1 {
			t.Errorf("arrival order %v emitted %v rows for KDEN, want 1", i, n)
		}
		if !strings.Contains(got["metar-stations.go"], `{"KDEN", "DEN", "Denver International"`) {
			t.Errorf("arrival order %v kept the wrong KDEN row:\n%v", i, got["metar-stations.go"])
		}
		if i == 0 {
			want = got
			continue
		}
		for name, content := range got {
			if content != want[name] {
				t.Errorf("arrival order %v collapsed duplicates differently in %v:\ngot:\n%v\nwant:\n%v",
					i, name, content, want[name])
			}
		}
	}
}

// TestDryRunReportsWhatChanged asserts a check says which stations changed
// rather than only that something did: "more changes are needed" with no noun is
// what made a live upstream snapshot look like a broken tool.
func TestDryRunReportsWhatChanged(t *testing.T) {
	before := feedSnapshot{
		buoys: buoyFeed(
			buoyRow("41002", "South Hatteras", 31.743, -74.955),
			buoyRow("41004", "Edisto, SC", 32.502, -79.099),
		),
		tides: tideFeed(tideRow("9410135", "South San Diego Bay", "CA", 32.629101, -117.107803)),
		metar: metarFeed(metarAirfield("KDEN", "large_airport", "Denver International", "US-CO", "Denver", "DEN", 39.861698, -104.672997)),
	}
	// One station leaves the feed and another arrives.
	after := feedSnapshot{
		buoys: buoyFeed(
			buoyRow("41004", "Edisto, SC", 32.502, -79.099),
			buoyRow("44013", "Boston, MA", 42.346, -70.651),
		),
		tides: before.tides,
		metar: before.metar,
	}

	setSnapshot := serveFeed(t, before)
	repoDir := repoFixture(t)
	runTool(t, nil, repoDir)
	written := generatedTables(t, repoDir)

	setSnapshot(after)
	out := runTool(t, []string{"-n"}, repoDir)
	for _, want := range []string{
		"[UPDATE AVAILABLE] bot/buoy-stations.go",
		"1 added (44013)",
		"1 removed (41002)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry-run report does not mention %q:\n%v", want, out)
		}
	}
	if !strings.Contains(out, "[UP TO DATE] bot/tide-stations.go") {
		t.Errorf("an unchanged dataset was reported as changed:\n%v", out)
	}
	// A check must never touch the tree.
	for name, content := range generatedTables(t, repoDir) {
		if content != written[name] {
			t.Errorf("-n rewrote %v", name)
		}
	}
}

// TestReorderOnlyIsReportedAndNormalized asserts a table whose rows are all
// present but in an older order is reported as exactly that, and that a real run
// normalizes it and then converges.
func TestReorderOnlyIsReportedAndNormalized(t *testing.T) {
	snapshot := feedSnapshot{
		buoys: buoyFeed(
			buoyRow("41002", "South Hatteras", 31.743, -74.955),
			buoyRow("41004", "Edisto, SC", 32.502, -79.099),
			buoyRow("44013", "Boston, MA", 42.346, -70.651),
		),
		tides: tideFeed(tideRow("9410135", "South San Diego Bay", "CA", 32.629101, -117.107803)),
		metar: metarFeed(metarAirfield("KDEN", "large_airport", "Denver International", "US-CO", "Denver", "DEN", 39.861698, -104.672997)),
	}
	serveFeed(t, snapshot)

	repoDir := repoFixture(t)
	runTool(t, nil, repoDir)
	canonical := generatedTables(t, repoDir)

	// An older generator, or a hand-kept table, can hold the same rows in a
	// different order. That is not a data change, and the report must say so.
	buoyPath := filepath.Join(repoDir, "bot", "buoy-stations.go")
	reverseTableRows(t, buoyPath)
	reordered := generatedTables(t, repoDir)["buoy-stations.go"]
	if reordered == canonical["buoy-stations.go"] {
		t.Fatal("the fixture did not reorder the table")
	}

	out := runTool(t, []string{"-n"}, repoDir)
	if !strings.Contains(out, "[UPDATE AVAILABLE] bot/buoy-stations.go") {
		t.Fatalf("a reordered table was not reported:\n%v", out)
	}
	if !strings.Contains(out, "same rows in a different order") {
		t.Errorf("a reordered table was reported as a data change:\n%v", out)
	}
	if got := generatedTables(t, repoDir)["buoy-stations.go"]; got != reordered {
		t.Error("-n rewrote a reordered table")
	}

	// Normalizing it is a real run, and afterwards the tool converges.
	out = runTool(t, nil, repoDir)
	if !strings.Contains(out, "[UPDATED] bot/buoy-stations.go") ||
		!strings.Contains(out, "same rows in a different order") {
		t.Errorf("normalizing a reordered table was not reported:\n%v", out)
	}
	if got := generatedTables(t, repoDir)["buoy-stations.go"]; got != canonical["buoy-stations.go"] {
		t.Error("normalizing a reordered table did not restore the canonical order")
	}
	if out = runTool(t, []string{"-n"}, repoDir); !strings.Contains(out, "[UP TO DATE] bot/buoy-stations.go") {
		t.Errorf("the tool did not converge after normalizing:\n%v", out)
	}
}

// TestWriteFileAtomic asserts a table is replaced in one step: the file keeps
// its mode, no temporary file survives, and a write that cannot be completed
// leaves the previous table exactly as it was rather than a truncated file that
// would break the build and make every later check report an update.
func TestWriteFileAtomic(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "table.go")

	if err := writeFileAtomic(path, []byte("first\n")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != generatedFileMode {
		t.Errorf("mode = %v, want %v", perm, generatedFileMode)
	}

	if err := writeFileAtomic(path, []byte("second\n")); err != nil {
		t.Fatalf("writeFileAtomic (replace): %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second\n" {
		t.Errorf("content = %q, want the replacement", got)
	}

	// A rename that cannot succeed (the target is a directory) must fail and
	// leave nothing of its own behind.
	target := filepath.Join(dir, "subdir")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := writeFileAtomic(target, []byte("doomed\n")); err == nil {
		t.Error("writeFileAtomic replaced a directory, want an error")
	}
	if err := writeFileAtomic(filepath.Join(dir, "absent", "nested.go"), []byte("doomed\n")); err == nil {
		t.Error("writeFileAtomic wrote into a missing directory, want an error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %v", entry.Name())
		}
	}
	if len(entries) != 2 {
		t.Errorf("directory holds %v entries, want the table and the subdirectory", len(entries))
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after a failed write: %v", err)
	}
	if string(got) != "second\n" {
		t.Errorf("a failed write changed the table: %q", got)
	}
}

// TestGeneratedFilesCarryTheReadersAdapters asserts each generated table also
// carries the code the rest of the package reads it through. An earlier template
// emitted only the table, which dropped every catalogEntry method and every
// *Catalog value and left the package unable to build — while the tool reported
// "[UPDATED]" and moved on.
func TestGeneratedFilesCarryTheReadersAdapters(t *testing.T) {
	snapshot := feedSnapshot{
		buoys: buoyFeed(
			buoyRow("41002", "South Hatteras", 31.743, -74.955),
			buoyRow("44013", "Boston, MA", 42.346, -70.651),
		),
		tides: tideFeed(
			tideRow("9410135", "South San Diego Bay", "CA", 32.629101, -117.107803),
			tideRow("9414290", "San Francisco", "CA", 37.806331, -122.465889),
		),
		metar: metarFeed(
			metarAirfield("KDEN", "large_airport", "Denver International", "US-CO", "Denver", "DEN", 39.861698, -104.672997),
			metarAirfield("EGLL", "large_airport", "London Heathrow", "GB-ENG", "London", "LHR", 51.4706, -0.461941),
		),
	}
	serveFeed(t, snapshot)
	repoDir := repoFixture(t)
	runTool(t, nil, repoDir)

	required := map[string][]string{
		"buoy-stations.go": {
			"func (s buoyStation) catalogEntry() catalogEntry",
			"var buoyCatalog = catalogFrom(buoyStations",
		},
		"tide-stations.go": {
			"func (s tideStation) catalogEntry() catalogEntry",
			"var tideCatalog = catalogFrom(tideStations",
		},
		"metar-stations.go": {
			"func (s metarStation) region() string",
			"func (s metarStation) catalogEntry() catalogEntry",
			"var metarCountryRegions = map[string]string{",
			"var metarCatalog = catalogFrom(metarStations",
		},
	}
	for name, wants := range required {
		content := generatedTables(t, repoDir)[name]
		for _, want := range wants {
			if !strings.Contains(content, want) {
				t.Errorf("%v does not declare %q:\n%v", name, want, content)
			}
		}
		if _, err := parser.ParseFile(token.NewFileSet(), name, content, parser.AllErrors); err != nil {
			t.Errorf("%v is not valid Go: %v", name, err)
		}
	}
}

// reverseTableRows rewrites a generated table with its row lines in reverse
// order, which is what a file written by an older generator looks like.
func reverseTableRows(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	var rows []int
	for i, line := range lines {
		if strings.HasPrefix(line, "\t{") {
			rows = append(rows, i)
		}
	}
	if len(rows) < 2 {
		t.Fatalf("%v has %v table rows, want at least 2", path, len(rows))
	}
	for lo, hi := 0, len(rows)-1; lo < hi; lo, hi = lo+1, hi-1 {
		lines[rows[lo]], lines[rows[hi]] = lines[rows[hi]], lines[rows[lo]]
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), generatedFileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
