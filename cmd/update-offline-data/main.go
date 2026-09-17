// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package main implements the update-offline-data utility, which checks or
// refreshes the embedded public datasets in go-reticulum: the NOAA NDBC
// weather buoys, the NOAA CO-OPS tide-prediction stations, the OurAirports
// airfields, and the NOAA World Magnetic Model notice.
//
// The embedded tables are those providers' catalogs in full, filtered only by
// the rule each table documents: a station is carried because it meets the
// filter, which is what lets the field tools answer "nearest" anywhere on
// earth. The commands that read the tables sort and paginate; this tool only
// keeps the data current.
package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"go/format"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// Default upstream URLs for offline public datasets.
var (
	defaultNDBCURL        = "https://www.ndbc.noaa.gov/activestations.xml"
	defaultCOOPSURL       = "https://api.tidesandcurrents.noaa.gov/mdapi/prod/webapi/stations.json?type=tidepredictions"
	defaultOurAirportsURL = "https://davidmegginson.github.io/ourairports-data/airports.csv"
	defaultWMMURL         = "https://www.ncei.noaa.gov/products/world-magnetic-model"
)

func main() {
	log.SetFlags(0)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, http.DefaultClient, ""))
}

func run(args []string, stdout, stderr io.Writer, client *http.Client, repoDir string) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			return exitOK
		}
		reportf(stderr, "%v\n", err)
		return exitUsage
	}

	if opts.version {
		reportf(stdout, "update-offline-data %v\n", rns.VERSION)
		return exitOK
	}

	if repoDir == "" {
		dir, err := findRepoRoot()
		if err != nil {
			reportf(stderr, "error: %v\n", err)
			return exitFailure
		}
		repoDir = dir
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	updater := &datasetUpdater{
		client:  client,
		repoDir: repoDir,
		dryRun:  opts.dryRun,
		verbose: opts.verbose,
		stdout:  stdout,
		stderr:  stderr,
		sleep:   sleepContext,
	}

	target := strings.ToLower(opts.target)
	hasError := false

	if target == "all" || target == "buoy" {
		if err := updater.updateBuoys(ctx); err != nil {
			reportf(stderr, "buoy update failed: %v\n", err)
			hasError = true
		}
	}
	if target == "all" || target == "tide" {
		if err := updater.updateTides(ctx); err != nil {
			reportf(stderr, "tide update failed: %v\n", err)
			hasError = true
		}
	}
	if target == "all" || target == "metar" {
		if err := updater.updateMETAR(ctx); err != nil {
			reportf(stderr, "metar update failed: %v\n", err)
			hasError = true
		}
	}
	if target == "all" || target == "wmm" {
		if err := updater.checkWMM(ctx); err != nil {
			reportf(stderr, "wmm check failed: %v\n", err)
			hasError = true
		}
	}

	if hasError {
		return exitFailure
	}
	return exitOK
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	curr := wd
	for {
		if _, err := os.Stat(filepath.Join(curr, "bot")); err == nil {
			if _, err := os.Stat(filepath.Join(curr, "go.mod")); err == nil {
				return curr, nil
			}
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return "", fmt.Errorf("unable to locate repository root from working directory %v", wd)
}

type datasetUpdater struct {
	client  *http.Client
	repoDir string
	dryRun  bool
	verbose bool
	stdout  io.Writer
	stderr  io.Writer
	// sleep waits between fetch attempts. It is injected so a test can exercise
	// the retry policy without waiting on a real delay.
	sleep func(context.Context, time.Duration) error
}

func (u *datasetUpdater) logv(format string, args ...any) {
	if u.verbose {
		reportf(u.stdout, format+"\n", args...)
	}
}

// Fetch policy for a public dataset. A catalog that is downloaded by a tool
// which then rewrites an embedded table must never be treated as authoritative
// when the download was interrupted: a partly received CSV that happens to end
// on a line boundary parses cleanly and looks exactly like upstream dropping
// thousands of stations.
const (
	// fetchAttempts bounds the tries per dataset, so a source that is genuinely
	// down fails the run instead of hanging it.
	fetchAttempts = 4
	// fetchBackoffBase is the first retry delay; each later attempt doubles it.
	fetchBackoffBase = 500 * time.Millisecond
	// fetchBackoffCap bounds a single retry delay.
	fetchBackoffCap = 8 * time.Second
)

// retryableError marks a fetch failure that is worth another attempt: a
// transport error, a server-side status, or a body that did not arrive whole.
type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

// retryablef builds a retryable failure.
func retryablef(format string, args ...any) error {
	return retryableError{err: fmt.Errorf(format, args...)}
}

// fetchSource downloads one upstream dataset in full and returns its bytes,
// retrying the failures that can be transient. The whole document is held in
// memory deliberately: it is what lets the download be checked against the
// length the server declared, and the largest catalog here is about 12 MB.
//
// The retries exist for a dropped connection or a server that is briefly
// unavailable. They cannot make a healthy source agree with a file that holds a
// different selection of stations: a successful fetch is returned on its first
// attempt, byte for byte.
func (u *datasetUpdater) fetchSource(ctx context.Context, name, url string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		body, err := u.fetchOnce(ctx, name, url)
		if err == nil {
			return body, nil
		}
		if _, ok := errors.AsType[retryableError](err); !ok {
			return nil, err
		}
		lastErr = err
		if attempt == fetchAttempts {
			break
		}
		delay := fetchBackoff(attempt)
		u.logv("%v: %v; retrying in %v (attempt %v of %v)", name, err, delay, attempt+1, fetchAttempts)
		if err := u.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%v failed after %v attempts: %w", name, fetchAttempts, lastErr)
}

// fetchOnce performs one request and reads the whole body, verifying that the
// server sent everything it said it would.
func (u *datasetUpdater) fetchOnce(ctx context.Context, name, url string) ([]byte, error) {
	u.logv("fetching %v from %v...", name, url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, retryableError{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// A 4xx other than 429 is a permanent answer: the dataset moved or the
		// request is wrong, and asking again cannot help.
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return nil, retryablef("HTTP %v from %v", resp.StatusCode, url)
		}
		return nil, fmt.Errorf("HTTP %v from %v", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, retryableError{err: fmt.Errorf("reading %v: %w", url, err)}
	}
	if resp.ContentLength >= 0 && int64(len(body)) != resp.ContentLength {
		return nil, retryablef("%v declared %v bytes and sent %v", url, resp.ContentLength, len(body))
	}
	u.logv("%v: HTTP %v, %v bytes, sha256 %x", name, resp.StatusCode, len(body), sha256.Sum256(body))
	return body, nil
}

// fetchBackoff returns how long to wait before the attempt after the given one:
// an exponential delay, capped. There is no jitter because the datasets are
// fetched one after another by a single tool, so there is no herd to spread.
func fetchBackoff(attempt int) time.Duration {
	delay := fetchBackoffBase << (attempt - 1)
	if delay > fetchBackoffCap || delay <= 0 {
		return fetchBackoffCap
	}
	return delay
}

// sleepContext waits for d, or returns early when ctx is done.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// Buoy Stations
// ---------------------------------------------------------------------------

type xmlActiveStations struct {
	XMLName  xml.Name     `xml:"stations"`
	Stations []xmlStation `xml:"station"`
}

type xmlStation struct {
	ID   string  `xml:"id,attr"`
	Lat  float64 `xml:"lat,attr"`
	Lon  float64 `xml:"lon,attr"`
	Name string  `xml:"name,attr"`
	Met  string  `xml:"met,attr"`
}

type buoyEntry struct {
	id     string
	name   string
	region string
	lat    float64
	lng    float64
}

// compareBuoys orders the table by station id, then by every remaining field.
// The tie-breakers are what make the emitted order a pure function of the *set*
// of rows the feed carries: with a partial key, two rows that share an id would
// be ordered by whatever position the feed happened to list them in, so the same
// stations could generate a different file from one run to the next.
func compareBuoys(a, b buoyEntry) int {
	return cmp.Or(
		cmp.Compare(a.id, b.id),
		cmp.Compare(a.name, b.name),
		cmp.Compare(a.region, b.region),
		cmp.Compare(a.lat, b.lat),
		cmp.Compare(a.lng, b.lng),
	)
}

// buoyFileHead and buoyFileTail are the parts of bot/buoy-stations.go that
// surround the station rows. The whole file is generated, so the adapter the
// discovery commands read and the catalog derived from it live here too: a
// template that emitted only the table would drop them and leave the package
// unable to build, which is exactly what an earlier version of this tool did.
const buoyFileHead = `// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Code generated by update-offline-data; DO NOT EDIT.
//
// This file holds the reference table of offshore weather buoys: the National
// Data Buoy Center stations whose realtime sea-state feed the buoy command
// reads, with the place each one reports from and where it floats.
//
// The table exists because a buoy id is opaque and nobody on a radio link
// should have to already know one. It is embedded rather than fetched, so
// "buoy near me" and "buoy search boston" answer offline, at the speed of the
// geodesy commands, and keep working when the provider is unreachable.
//
// It is the center's catalog in full: every station the feed lists that reports
// weather, not a selection of the ones somebody guessed would be useful. The
// device is meant to work anywhere on earth, and the way to make "buoy near me"
// answer with the nearest buoy is to carry them all; the search and pagination
// commands decide what fits on a page. The ids, names, and positions are data,
// and the code in buoy.go is the only reader.
//
// The station list changes slowly: stations are commissioned and retired over
// years, never within a session. Refresh it with
// ./scripts/update-offline-data.sh --target buoy, which takes the center's
// activestations.xml, keeps the entries with met="y", and rewrites the rows
// below. A station id that is not in the table is still accepted by the buoy
// command, because the feed is authoritative about its own stations and the
// table is only a shortcut for finding the right one.

package bot

// buoyStation is one reference buoy.
type buoyStation struct {
	// ID is the station identifier the realtime feed takes, like 46026.
	ID string
	// Name is the place the buoy reports from, like San Francisco.
	Name string
	// Region is the state code, or the basin code for a buoy outside the
	// states (ATL, GOM, CAR, PAC).
	Region string
	// Lat and Lng are the buoy's position in signed decimal degrees.
	Lat float64
	Lng float64
}

// buoyStations is the reference table, ordered by station id.
var buoyStations = []buoyStation{
`

const buoyFileTail = `}

// catalogEntry renders this buoy as a searchable catalog row: the place it
// reports from, with the region it belongs to. The provider's names usually
// carry the state already ("San Francisco, CA"), and the region is derived from
// that same suffix, so the region is added only when the name omits it.
func (s buoyStation) catalogEntry() catalogEntry {
	label := s.Name
	if s.Region != "" && !mentionsRegion(s.Name, s.Region) {
		label += ", " + s.Region
	}
	return catalogEntry{
		ID:     s.ID,
		Label:  label,
		Region: s.Region,
		Point:  LatLng{Lat: s.Lat, Lng: s.Lng},
	}
}

// buoyCatalog is the reference table in the uniform form the discovery commands
// search. It is built once from the same table the buoy command validates an id
// against, so the lookup and the discovery can never disagree.
var buoyCatalog = catalogFrom(buoyStations, buoyStation.catalogEntry)
`

func (u *datasetUpdater) updateBuoys(ctx context.Context) error {
	body, err := u.fetchSource(ctx, "NDBC buoy catalog", defaultNDBCURL)
	if err != nil {
		return err
	}

	var active xmlActiveStations
	if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&active); err != nil {
		return fmt.Errorf("failed to decode NDBC stations XML: %w", err)
	}

	var entries []buoyEntry
	for _, s := range active.Stations {
		if strings.ToLower(s.Met) != "y" {
			continue
		}
		id := strings.TrimSpace(s.ID)
		if id == "" {
			continue
		}
		name := cleanName(s.Name)
		// A station with no name cannot be searched for, listed, or read out
		// over the radio, and the table's coherence test refuses it.
		if name == "" {
			continue
		}
		region := deriveRegion(name, s.Lat, s.Lon)
		entries = append(entries, buoyEntry{
			id:     id,
			name:   name,
			region: region,
			lat:    s.Lat,
			lng:    s.Lon,
		})
	}

	slices.SortStableFunc(entries, compareBuoys)
	// A station the feed lists twice (two programs reporting one platform, say)
	// must not become two identical rows, and which of the two survives must not
	// depend on the order the feed happened to use.
	entries = canonicalize(entries, compareBuoys, func(e buoyEntry) string { return e.id })

	var buf bytes.Buffer
	buf.WriteString(buoyFileHead)

	for _, e := range entries {
		fmt.Fprintf(&buf, "\t{%q, %q, %q, %v, %v},\n", e.id, e.name, e.region, e.lat, e.lng)
	}
	buf.WriteString(buoyFileTail)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to format generated buoy code: %w", err)
	}

	targetPath := filepath.Join(u.repoDir, "bot", "buoy-stations.go")
	return u.commitFile(targetPath, formatted, len(entries), "buoy stations")
}

func deriveRegion(name string, lat, lon float64) string {
	if code := stateCodeIn(name); code != "" {
		return code
	}
	if lon < -120 {
		return "PAC"
	}
	if lat < 22 && lon > -90 && lon < -60 {
		return "CAR"
	}
	// The Gulf of Mexico, bounded on the east so that Florida's Atlantic coast
	// is not read as Gulf: Cape Canaveral and Fort Pierce sit at about 80.5W,
	// which the old -80 bound swept into the Gulf.
	if lat >= 20 && lat <= 31 && lon >= -98 && lon <= -81 {
		return "GOM"
	}
	return "ATL"
}

// stateCodeIn returns the two-letter state or territory code a station's name
// carries, if it carries one. The center's names put the code in a
// comma-separated part that often carries a station code after it ("Fort
// Pierce, FL (FTP)"), so the part is scanned for a code rather than compared
// whole, and a longer word is not mistaken for one ("Columbia" is not CO).
//
// The codes are the ones bot/discovery.go's usStateNames knows, which is what
// the region filter and the table's coherence test agree on.
func stateCodeIn(name string) string {
	parts := strings.Split(name, ",")
	for i := len(parts) - 1; i >= 1; i-- {
		field := strings.ToUpper(strings.TrimSpace(parts[i]))
		if len(field) < 2 {
			continue
		}
		code := field[:2]
		if _, ok := stationStateCodes[code]; !ok {
			continue
		}
		if len(field) > 2 && isLetterByte(field[2]) {
			continue
		}
		return code
	}
	return ""
}

// isLetterByte reports whether b is an ASCII letter.
func isLetterByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// stationStateCodes are the two-letter codes a station name may carry: the
// fifty states, the District of Columbia, and the territories. It mirrors
// bot/discovery.go's usStateNames, which the coherence test holds both of them
// to.
var stationStateCodes = map[string]bool{
	"AL": true, "AK": true, "AZ": true, "AR": true, "CA": true, "CO": true,
	"CT": true, "DE": true, "DC": true, "FL": true, "GA": true, "HI": true,
	"ID": true, "IL": true, "IN": true, "IA": true, "KS": true, "KY": true,
	"LA": true, "ME": true, "MD": true, "MA": true, "MI": true, "MN": true,
	"MS": true, "MO": true, "MT": true, "NE": true, "NV": true, "NH": true,
	"NJ": true, "NM": true, "NY": true, "NC": true, "ND": true, "OH": true,
	"OK": true, "OR": true, "PA": true, "RI": true, "SC": true, "SD": true,
	"TN": true, "TX": true, "UT": true, "VT": true, "VA": true, "WA": true,
	"WV": true, "WI": true, "WY": true, "PR": true, "VI": true, "AS": true,
	"GU": true, "MP": true,
}

// ---------------------------------------------------------------------------
// Tide Stations
// ---------------------------------------------------------------------------

type coopsStationsResp struct {
	Stations []coopsStation `json:"stations"`
}

type coopsStation struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	State string  `json:"state"`
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
}

// compareTides orders the table by station id, then by every remaining field, so
// the emitted order never depends on the order the provider listed the stations
// in. See compareBuoys.
func compareTides(a, b coopsStation) int {
	return cmp.Or(
		cmp.Compare(a.ID, b.ID),
		cmp.Compare(a.Name, b.Name),
		cmp.Compare(a.State, b.State),
		cmp.Compare(a.Lat, b.Lat),
		cmp.Compare(a.Lng, b.Lng),
	)
}

// tideFileHead and tideFileTail surround the station rows of
// bot/tide-stations.go. See buoyFileHead for why the whole file is generated.
const tideFileHead = `// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Code generated by update-offline-data; DO NOT EDIT.
//
// This file holds the complete reference table of tide stations: every
// tide-prediction station the provider lists, with its station id, name, and
// position. It is the provider's catalog in full rather than a selection of the
// ports somebody guessed would be useful, so "tide near me" answers with the
// nearest station wherever the device is; the search and pagination commands
// decide what fits on a page. The ids, names, and coordinates are data, and
// tide.go is the only reader.
//
// The table exists so that a position can be resolved to the nearest station
// without a second network call, which is what makes "tide near me" work on a
// link that can afford exactly one request. Refresh it with
// ./scripts/update-offline-data.sh --target tide, which takes the provider's
// tide-prediction station catalog. A station id that is not in the table is
// still accepted: the provider is authoritative about its own stations, and the
// table is only a shortcut for finding the right one.

package bot

// tideStation is one reference tide station.
type tideStation struct {
	// ID is the provider's station identifier.
	ID string
	// Name is the station's published name.
	Name string
	// State is the two-letter state or territory code, empty for a station
	// outside the states.
	State string
	// Lat and Lng are the station's position in signed decimal degrees.
	Lat float64
	Lng float64
}

// tideStations is the reference table, ordered by station id.
var tideStations = []tideStation{
`

const tideFileTail = `}

// catalogEntry renders this station as a searchable catalog row: the name a
// person would say, with the state it sits in.
func (s tideStation) catalogEntry() catalogEntry {
	label := s.Name
	if s.State != "" && !mentionsRegion(s.Name, s.State) {
		label += ", " + s.State
	}
	return catalogEntry{
		ID:     s.ID,
		Label:  label,
		Region: s.State,
		Point:  LatLng{Lat: s.Lat, Lng: s.Lng},
	}
}

// tideCatalog is the reference table in the uniform form the discovery commands
// search. It is built once from the same table the tide command resolves a
// station against, so the lookup and the discovery can never disagree.
var tideCatalog = catalogFrom(tideStations, tideStation.catalogEntry)
`

func (u *datasetUpdater) updateTides(ctx context.Context) error {
	body, err := u.fetchSource(ctx, "NOAA CO-OPS tide station catalog", defaultCOOPSURL)
	if err != nil {
		return err
	}

	var catalog coopsStationsResp
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&catalog); err != nil {
		return fmt.Errorf("failed to decode CO-OPS JSON: %w", err)
	}

	var stations []coopsStation
	for _, s := range catalog.Stations {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			continue
		}
		stations = append(stations, coopsStation{
			ID:    id,
			Name:  cleanName(s.Name),
			State: strings.ToUpper(strings.TrimSpace(s.State)),
			Lat:   s.Lat,
			Lng:   s.Lng,
		})
	}

	slices.SortStableFunc(stations, compareTides)
	stations = canonicalize(stations, compareTides, func(s coopsStation) string { return s.ID })

	var buf bytes.Buffer
	buf.WriteString(tideFileHead)

	for _, s := range stations {
		fmt.Fprintf(&buf, "\t{%q, %q, %q, %v, %v},\n", s.ID, s.Name, s.State, s.Lat, s.Lng)
	}
	buf.WriteString(tideFileTail)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to format generated tide code: %w", err)
	}

	targetPath := filepath.Join(u.repoDir, "bot", "tide-stations.go")
	return u.commitFile(targetPath, formatted, len(stations), "tide stations")
}

// ---------------------------------------------------------------------------
// METAR / Airfield Stations
// ---------------------------------------------------------------------------

type metarRow struct {
	icao    string
	iata    string
	name    string
	city    string
	state   string
	country string
	lat     float64
	lng     float64
	rank    int
}

// compareMETARs orders the table by importance, then by station code, then by
// every remaining field, so the emitted order never depends on the order the
// provider listed the airfields in. See compareBuoys.
func compareMETARs(a, b metarRow) int {
	return cmp.Or(
		cmp.Compare(a.rank, b.rank),
		cmp.Compare(a.icao, b.icao),
		cmp.Compare(a.iata, b.iata),
		cmp.Compare(a.name, b.name),
		cmp.Compare(a.city, b.city),
		cmp.Compare(a.state, b.state),
		cmp.Compare(a.country, b.country),
		cmp.Compare(a.lat, b.lat),
		cmp.Compare(a.lng, b.lng),
	)
}

// metarFileHead and metarFileTail surround the airfield rows of
// bot/metar-stations.go. See buoyFileHead for why the whole file is generated.
//
// metarCountryRegions is part of the tail because it is a judgement, not data:
// the countries whose two-letter code is also a United States state code. Those
// fields carry the country's name as their region instead, because a filter that
// could mean either would answer every "metar list CO" with half a continent of
// the wrong country.
const metarFileHead = `// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Code generated by update-offline-data; DO NOT EDIT.
//
// This file holds the reference table of airfields whose aviation weather the
// metar command can report: every large airport with an IATA code and every
// regional one that carries scheduled passenger service, with the city each one
// serves and where its runway is.
//
// The table exists because an ICAO code is opaque and nobody should have to
// already know one to ask what the weather is doing at the field they are
// flying into. It also makes the answer useful offline: "metar near me" picks
// the airfields around a position without a lookup service, because the
// positions are already here.
//
// The ids, names, cities, and positions are the OurAirports public-domain
// catalog in full: every field the provider lists that carries an IATA code and
// scheduled service, not a selection of the ones somebody guessed would be
// useful, so "metar near me" answers with the nearest field wherever the device
// is. They are data, and the code in metar.go is the only reader. Refresh them
// with
// ./scripts/update-offline-data.sh --target metar. A code that is not in the
// table is still accepted by the metar command, because the provider is
// authoritative about its own stations and the table is only a shortcut for
// finding the right one.

package bot

// metarStation is one reference airfield.
type metarStation struct {
	// ICAO is the four-letter station code the weather provider takes, like
	// KDEN.
	ICAO string
	// IATA is the three-letter code a passenger knows, like DEN.
	IATA string
	// Name is the field's published name, without its trailing "Airport".
	Name string
	// City is the municipality the field serves.
	City string
	// State is the two-letter state or territory code, empty outside the
	// states.
	State string
	// Country is the two-letter country code.
	Country string
	// Lat and Lng are the field's position in signed decimal degrees.
	Lat float64
	Lng float64
	// Rank orders fields of different importance: 0 is a major airport, 1 is a
	// satellite or regional field. It is what puts an international hub before
	// a reliever field when both match a city.
	Rank int
}

// metarStations is the reference table, ordered by importance and then by
// station code.
var metarStations = []metarStation{
`

const metarFileTail = `}

// metarCountryRegions names the countries whose two-letter code is also a United
// States state code. Those fields carry the country's name as their region
// instead, because "CO" has to mean Colorado: a filter that could mean either
// would answer every "metar list CO" with half a continent of the wrong country.
// The country's own name still filters its fields ("metar list colombia"), and
// every code that does not collide keeps working ("metar list GB").
var metarCountryRegions = map[string]string{
	"AR": "Argentina",
	"CA": "Canada",
	"CO": "Colombia",
	"DE": "Germany",
	"ID": "Indonesia",
	"IN": "India",
	"MA": "Morocco",
	"TN": "Tunisia",
}

// region is the code the list and search forms filter this field by: its state
// inside the states, the country's name where the country code would collide
// with a state code, and its country code elsewhere.
func (s metarStation) region() string {
	if s.State != "" {
		return s.State
	}
	if name, ok := metarCountryRegions[s.Country]; ok {
		return name
	}
	return s.Country
}

// catalogEntry renders this field as a searchable catalog row: the field's name
// with the region it sits in, and the city and IATA code as searchable keywords
// a row does not spend envelope space on.
func (s metarStation) catalogEntry() catalogEntry {
	label := s.Name
	if region := s.region(); region != "" {
		label += " (" + region + ")"
	}
	return catalogEntry{
		ID:       s.ICAO,
		Label:    label,
		Region:   s.region(),
		Point:    LatLng{Lat: s.Lat, Lng: s.Lng},
		Keywords: s.City + " " + s.IATA,
		Rank:     s.Rank,
	}
}

// metarCatalog is the reference table in the uniform form the discovery
// commands search. It is built once from the same table the metar command
// validates a code against, so the lookup and the discovery can never disagree.
var metarCatalog = catalogFrom(metarStations, metarStation.catalogEntry)
`

func (u *datasetUpdater) updateMETAR(ctx context.Context) error {
	body, err := u.fetchSource(ctx, "OurAirports airfield catalog", defaultOurAirportsURL)
	if err != nil {
		return err
	}

	r := csv.NewReader(bytes.NewReader(body))
	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("failed to read CSV header: %w", err)
	}

	colMap := make(map[string]int)
	for i, h := range header {
		colMap[h] = i
	}

	var rows []metarRow
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}

		ident := strings.TrimSpace(record[colMap["ident"]])
		iata := strings.TrimSpace(record[colMap["iata_code"]])
		typ := strings.TrimSpace(record[colMap["type"]])
		scheduled := strings.TrimSpace(record[colMap["scheduled_service"]])

		// Keep major international hubs and airports with scheduled service and IATA codes.
		if len(ident) != 4 || iata == "" {
			continue
		}
		if typ != "large_airport" && typ != "medium_airport" && scheduled != "yes" {
			continue
		}

		lat, _ := strconv.ParseFloat(record[colMap["latitude_deg"]], 64)
		lng, _ := strconv.ParseFloat(record[colMap["longitude_deg"]], 64)

		name := strings.TrimSpace(record[colMap["name"]])
		name = strings.TrimSuffix(name, " Airport")
		name = strings.TrimSuffix(name, " International Airport")

		city := strings.TrimSpace(record[colMap["municipality"]])
		country := strings.TrimSpace(record[colMap["iso_country"]])
		// A field with no name, no city, or no country cannot be found by the
		// things an operator actually searches with ("metar near me", "metar
		// search denver"), and the reference table's coherence test refuses it.
		if name == "" || city == "" || country == "" {
			continue
		}
		state := ""
		region := strings.TrimSpace(record[colMap["iso_region"]])
		// Only a United States region suffix is a state code. Every other
		// country's subdivisions are its own (Canada's "ON", Pakistan's "GB"
		// for Gilgit-Baltistan), and reading one as a state would make
		// "metar list GB" answer with Pakistani fields instead of British ones
		// and leave the country/state collision handling unreachable.
		if country == "US" {
			if withinCountry, ok := strings.CutPrefix(region, "US-"); ok {
				state = withinCountry
			}
		}

		rank := 1
		if typ == "large_airport" {
			rank = 0
		}

		rows = append(rows, metarRow{
			icao:    ident,
			iata:    iata,
			name:    name,
			city:    city,
			state:   state,
			country: country,
			lat:     lat,
			lng:     lng,
			rank:    rank,
		})
	}

	slices.SortStableFunc(rows, compareMETARs)
	// The table is ordered by importance, so a repeated station code is not
	// necessarily adjacent: canonicalize sorts into the emitted order first and
	// then keeps the first row for each code, which is the major-airport entry.
	rows = canonicalize(rows, compareMETARs, func(r metarRow) string { return r.icao })

	var buf bytes.Buffer
	buf.WriteString(metarFileHead)

	for _, r := range rows {
		fmt.Fprintf(&buf, "\t{%q, %q, %q, %q, %q, %q, %v, %v, %v},\n",
			r.icao, r.iata, r.name, r.city, r.state, r.country, r.lat, r.lng, r.rank)
	}
	buf.WriteString(metarFileTail)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to format generated METAR code: %w", err)
	}

	targetPath := filepath.Join(u.repoDir, "bot", "metar-stations.go")
	return u.commitFile(targetPath, formatted, len(rows), "METAR airfields")
}

// ---------------------------------------------------------------------------
// World Magnetic Model (WMM) Check
// ---------------------------------------------------------------------------

func (u *datasetUpdater) checkWMM(ctx context.Context) error {
	body, err := u.fetchSource(ctx, "NOAA World Magnetic Model notice", defaultWMMURL)
	if err != nil {
		return err
	}

	content := string(body)
	if strings.Contains(content, "WMM2025") && (strings.Contains(content, "2029") || strings.Contains(content, "December 31, 2029")) {
		reportf(u.stdout, "[UP TO DATE] bot/declination.go: WMM2025 spherical harmonic model is current (valid through 2029)\n")
	} else {
		reportf(u.stdout, "[UPDATE AVAILABLE] bot/declination.go: upstream WMM model notice changed on NCEI product page\n")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helper: Deterministic Tables
// ---------------------------------------------------------------------------

// canonicalize puts rows into the one order the generator emits and drops a row
// whose key has already been seen, so the bytes written are a pure function of
// the *set* of rows upstream carries.
//
// This is what keeps re-running the tool from rewriting a table whose data did
// not change: ordering by a partial key would leave two rows that share a key in
// whatever order the feed happened to list them, and every later run would
// regenerate the same stations in a different arrangement. Rows are ordered
// before they are de-duplicated, not after, because the emitted order and the
// key are not always the same thing: the airfield table is ordered by importance
// first, so a repeated station code can be separated by another field.
func canonicalize[E any](rows []E, compare func(a, b E) int, key func(E) string) []E {
	slices.SortStableFunc(rows, compare)
	seen := make(map[string]bool, len(rows))
	kept := rows[:0]
	for _, row := range rows {
		k := key(row)
		if seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, row)
	}
	return kept
}

// ---------------------------------------------------------------------------
// Helper: File Comparison & Disk Write
// ---------------------------------------------------------------------------

// generatedFileMode is the mode of a generated table: an ordinary source file.
const generatedFileMode = 0o644

// commitFile writes a regenerated table, or reports what would change. The
// verdict comes from a structural comparison of the two files rather than from a
// byte comparison alone, so an operator can see *what* upstream changed: four
// stations added is a different thing from the whole table reshuffled, and only
// one of those is worth committing.
func (u *datasetUpdater) commitFile(targetPath string, content []byte, count int, label string) error {
	relPath, err := filepath.Rel(u.repoDir, targetPath)
	if err != nil {
		relPath = targetPath
	}

	existing, err := os.ReadFile(targetPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if u.dryRun {
			reportf(u.stdout, "[UPDATE AVAILABLE] %v (new file: %v %v)\n", relPath, count, label)
			return nil
		}
		if err := writeFileAtomic(targetPath, content); err != nil {
			return fmt.Errorf("failed to write %v: %w", relPath, err)
		}
		reportf(u.stdout, "[UPDATED] %v (%v %v)\n", relPath, count, label)
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %v: %w", relPath, err)
	}

	if bytes.Equal(existing, content) {
		reportf(u.stdout, "[UP TO DATE] %v (%v entries)\n", relPath, count)
		return nil
	}

	// Report both sides of the comparison, because "upstream: 907" alone does not
	// say how far the file is from it: a table the provider has grown by four
	// stations and one that was never generated from this source at all read the
	// same until the two counts are shown side by side.
	fileRows := countRows(existing)
	delta := describeChange(string(existing), string(content))
	if u.dryRun {
		reportf(u.stdout, "[UPDATE AVAILABLE] %v (file: %v rows, upstream: %v %v) [%v]\n",
			relPath, fileRows, count, label, delta.summary())
		return nil
	}
	if err := writeFileAtomic(targetPath, content); err != nil {
		return fmt.Errorf("failed to write %v: %w", relPath, err)
	}
	reportf(u.stdout, "[UPDATED] %v (%v %v, replacing %v rows) [%v]\n",
		relPath, count, label, fileRows, delta.summary())
	return nil
}

// countRows counts the station rows a generated table holds, so a report can say
// how far the file is from upstream rather than only how large upstream is.
func countRows(content []byte) int {
	var n int
	for line := range strings.SplitSeq(string(content), "\n") {
		if firstQuotedField(line) != "" {
			n++
		}
	}
	return n
}

// reportf writes one operator-facing line and deliberately ignores a write
// failure: a report that cannot reach its stream has nowhere left to report
// itself, and failing the run over it would hide the very verdict the operator
// asked for. The other CLIs in this repository treat diagnostics the same way
// (see cmd/gobot's note).
func reportf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// writeFileAtomic replaces path with content in one step: the bytes are written
// to a sibling temporary file, flushed, and renamed over the target. A run that
// is interrupted, or that fails to write, therefore leaves either the old table
// or the new one, never a truncated file that breaks the build and makes every
// later check report an update forever.
func writeFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Any failure past this point must not leave the temporary file behind.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(generatedFileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// rowChange is what a regenerated table changed, counted by station rather than
// by line: a row whose own fields changed is one updated station, not an
// addition and a removal, and a table that merely moved between runs is no data
// change at all.
type rowChange struct {
	// added, removed, and updated hold station ids, in table order.
	added   []string
	removed []string
	updated []string
	// other counts the changed lines that are not table rows: the file's prose,
	// or the shape of the struct itself.
	other int
}

// summary describes the change for an operator, in the fewest words that are
// still actionable: what happened, and to which stations.
func (c rowChange) summary() string {
	var parts []string
	if len(c.added) > 0 {
		parts = append(parts, fmt.Sprintf("%v added (%v)", len(c.added), sampleIDs(c.added)))
	}
	if len(c.removed) > 0 {
		parts = append(parts, fmt.Sprintf("%v removed (%v)", len(c.removed), sampleIDs(c.removed)))
	}
	if len(c.updated) > 0 {
		parts = append(parts, fmt.Sprintf("%v updated (%v)", len(c.updated), sampleIDs(c.updated)))
	}
	if c.other > 0 {
		parts = append(parts, fmt.Sprintf("%v other lines", c.other))
	}
	if len(parts) == 0 {
		return "same rows in a different order"
	}
	return strings.Join(parts, ", ")
}

// describeChange compares two generated files station by station, so the result
// is independent of the order either file happens to be in. A station whose row
// appears on both sides of the line comparison has been updated, not added and
// removed.
func describeChange(oldText, newText string) rowChange {
	oldLines, newLines := strings.Split(oldText, "\n"), strings.Split(newText, "\n")
	addedLines := missingLines(oldLines, newLines)
	removedLines := missingLines(newLines, oldLines)

	addedIDs := idsOf(addedLines)
	removedIDs := idsOf(removedLines)

	var change rowChange
	change.other = nonRowLines(addedLines) + nonRowLines(removedLines)
	for _, id := range addedIDs {
		if slices.Contains(removedIDs, id) {
			change.updated = append(change.updated, id)
			continue
		}
		change.added = append(change.added, id)
	}
	for _, id := range removedIDs {
		if !slices.Contains(addedIDs, id) {
			change.removed = append(change.removed, id)
		}
	}
	return change
}

// missingLines returns the lines of want that have no unclaimed counterpart in
// have, in want's own order. It is a multiset difference: consuming a match
// rather than merely testing for one keeps a duplicated line honest.
func missingLines(have, want []string) []string {
	counts := make(map[string]int, len(have))
	for _, line := range have {
		counts[line]++
	}
	var missing []string
	for _, line := range want {
		if counts[line] > 0 {
			counts[line]--
			continue
		}
		missing = append(missing, line)
	}
	return missing
}

// idsOf returns the station ids of a set of changed lines, in order and without
// repeats, skipping every line that is not a table row.
func idsOf(lines []string) []string {
	var ids []string
	for _, line := range lines {
		id := firstQuotedField(line)
		if id == "" || slices.Contains(ids, id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// nonRowLines counts the changed lines that carry no station id.
func nonRowLines(lines []string) int {
	var n int
	for _, line := range lines {
		if firstQuotedField(line) == "" {
			n++
		}
	}
	return n
}

// sampleIDs names a few of the stations a change touched, so a report says which
// buoys moved rather than only how many. It is capped because on a first run
// every row is new, and the point of a sample is recognition, not completeness.
func sampleIDs(ids []string) string {
	const maxSample = 8
	if len(ids) > maxSample {
		return strings.Join(ids[:maxSample], " ") + " ..."
	}
	return strings.Join(ids, " ")
}

// firstQuotedField returns the first quoted string on a generated table row,
// which is that row's key, and "" for any other line.
func firstQuotedField(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return ""
	}
	start := strings.IndexByte(trimmed, '"')
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(trimmed[start+1:], '"')
	if end < 0 {
		return ""
	}
	return trimmed[start+1 : start+1+end]
}

func cleanName(s string) string {
	s = strings.ReplaceAll(s, "\"", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}
