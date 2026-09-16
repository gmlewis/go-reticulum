// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the tower command: the offline finder for the cellular masts,
// amateur repeaters, emergency relays, and marine stations an operator can reach
// from where they are.
//
// The question it answers is a decision, not a lookup. A handheld with a rubber
// antenna hears nothing, a directional antenna pointed the right way hears a
// mast forty kilometers off, and the difference between the two is a bearing.
// The same bearing decides which way to walk when a hiker has lost coverage, and
// which machine to raise when the cellular network is gone but a mountain-top
// repeater still has power. Every answer here is therefore a distance, a
// bearing, and the details needed to use the site once it is found: the
// frequency, the offset a repeater needs, the tone it requires, and who runs it.
//
// Nothing in a query touches the network. The embedded catalog is the data, the
// geometry is closed form, and the answer is available with the binary alone —
// which is the only kind of answer that is any use in the situation the command
// exists for. An operator who needs more sites than the curated catalog holds
// drops a towers.csv beside the configuration and every query sees it; the
// parser, the merge, and the fallback are all here.
//
// Where a site is in China, the answer prints the GCJ-02 coordinate alongside
// the WGS-84 one, so a coordinate from this catalog can be pasted straight into
// Amap, Gaode, or WeChat — and a coordinate taken from those apps can be handed
// back to the bot with the "gcj:" prefix and searched from. That reciprocity is
// what makes the command as useful in Sichuan as it is in Colorado.

package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Tower command wordings and bounds.
const (
	// towerUsage is the usage line for the command and its aliases.
	towerUsage = "tower <near|search|list|info> [args]"
	// towerCatalogName names the catalog rows in a discovery answer.
	towerCatalogName = "Tower sites"
	// towerUsageHint is the second line of a bare tower request: the four ways
	// into the catalog, in the words the asker would type.
	towerUsageHint = "search, near, list, and info find a site: " +
		"tower search <query> [page] | tower near <place|coords|pluscode> | " +
		"tower list [country|region] [page] | tower info <id>"
	// towerInfoKind is the sub-command that renders one site in full.
	towerInfoKind = "info"
	// towerCSVColumns is how many fields a local towers.csv row must carry.
	towerCSVColumns = 13
	// towerCSVHeader is the documented column order of a local towers.csv.
	towerCSVHeader = "id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev"
)

// towerStore caches the resolved catalog for one bot: the embedded rows with any
// local dataset merged over them. A search on a busy room would otherwise re-read
// and re-parse the operator's CSV on every request, and the merge is by
// definition the same every time.
type towerStore struct {
	mu      sync.Mutex
	loaded  bool
	path    string
	records []TowerRecord
	entries []catalogEntry
}

// catalog returns the records and their uniform discovery rows, loading and
// merging the local dataset once per configured path.
func (s *towerStore) catalog(path string) ([]TowerRecord, []catalogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded && s.path == path {
		return s.records, s.entries
	}
	records, warnings := mergeTowerRecords(towerRecords, path)
	for _, warning := range warnings {
		logf("tower: %v", warning)
	}
	entries := catalogFrom(records, TowerRecord.catalogEntry)
	s.loaded, s.path, s.records, s.entries = true, path, records, entries
	return s.records, s.entries
}

// towersPath returns the configured local dataset path. A bot with no
// configuration reads the embedded catalog alone.
func (r *registry) towersPath() string {
	if r.bot == nil || r.bot.cfg == nil {
		return ""
	}
	return r.bot.cfg.TowersPath
}

// towerRecords returns this bot's resolved catalog: the embedded rows, with the
// operator's local dataset merged over them when one is configured.
func (c *commandContext) towerRecords() ([]TowerRecord, []catalogEntry) {
	return c.reg.towers.catalog(c.reg.towersPath())
}

// mergeTowerRecords overlays a local dataset on the embedded catalog. A row whose
// identifier is already in the catalog replaces it in place, so a corrected
// frequency is corrected everywhere at once; a row with a new identifier is
// appended, so an OpenCelliD extract adds sites rather than replacing the
// curated ones. An absent, empty, or unreadable dataset is not an error: the
// embedded catalog is the answer, and a bad export must never cost a field tool
// its built-in data.
func mergeTowerRecords(embedded []TowerRecord, path string) ([]TowerRecord, []string) {
	if strings.TrimSpace(path) == "" {
		return embedded, nil
	}
	external, warnings, err := readTowersCSV(path)
	if err != nil {
		return embedded, append(warnings, fmt.Sprintf(
			"%v could not be read (%v); the embedded catalog is used", path, err))
	}
	if len(external) == 0 {
		return embedded, warnings
	}
	merged := slices.Clone(embedded)
	index := make(map[string]int, len(merged))
	for i, record := range merged {
		index[strings.ToUpper(record.ID)] = i
	}
	overrode := 0
	for _, record := range external {
		key := strings.ToUpper(record.ID)
		if i, ok := index[key]; ok {
			merged[i] = record
			overrode++
			continue
		}
		index[key] = len(merged)
		merged = append(merged, record)
	}
	warnings = append(warnings, fmt.Sprintf("loaded %v local sites from %v (%v replaced an embedded row)",
		len(external), path, overrode))
	return merged, warnings
}

// readTowersCSV opens one local dataset. A file that is not there is reported as
// no rows and no error, because the documented way to use the feature is to drop
// the file in only when it is wanted.
func readTowersCSV(path string) ([]TowerRecord, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	return parseTowersCSV(file)
}

// parseTowersCSV streams the documented 13-column format into records. A row the
// reader cannot use is skipped with a warning naming it, so a dataset exported
// with the wrong column order is diagnosable rather than silently short; a row
// that is merely blank, or the optional header line, is skipped without a word.
func parseTowersCSV(r io.Reader) ([]TowerRecord, []string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	reader.Comment = '#'

	var (
		records  []TowerRecord
		warnings []string
		line     int
	)
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return records, warnings, fmt.Errorf("row %v: %w", line, err)
		}
		if len(row) == 0 || (len(row) == 1 && strings.TrimSpace(row[0]) == "") {
			continue
		}
		if line == 1 && strings.EqualFold(strings.TrimSpace(row[0]), "id") {
			continue
		}
		record, err := parseTowerCSVRow(row)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("row %v skipped: %v", line, err))
			continue
		}
		records = append(records, record)
	}
	return records, warnings, nil
}

// parseTowerCSVRow decodes one dataset row and validates it. The row identifies
// itself in the warning, so a skipped row can be found in the file without
// counting lines.
func parseTowerCSVRow(row []string) (TowerRecord, error) {
	for i := range row {
		row[i] = strings.TrimSpace(row[i])
	}
	label := "row " + strconv.Quote(firstNonEmpty(row))
	if len(row) != towerCSVColumns {
		return TowerRecord{}, fmt.Errorf("%v has %v columns, want %v (%v)",
			label, len(row), towerCSVColumns, towerCSVHeader)
	}
	id := row[0]
	if id == "" {
		return TowerRecord{}, fmt.Errorf("%v has no id", label)
	}
	towerType, err := towerRecordType(row[2])
	if err != nil {
		return TowerRecord{}, fmt.Errorf("%v (%v): %w", label, id, err)
	}
	lat, err := parseTowerCSVFloat(row[3])
	if err != nil {
		return TowerRecord{}, fmt.Errorf("%v (%v): latitude %q is not a number", label, id, row[3])
	}
	lng, err := parseTowerCSVFloat(row[4])
	if err != nil {
		return TowerRecord{}, fmt.Errorf("%v (%v): longitude %q is not a number", label, id, row[4])
	}
	if lat < -90 || lat > 90 {
		return TowerRecord{}, fmt.Errorf("%v (%v): latitude %v is outside -90..90", label, id, lat)
	}
	if lng < -180 || lng > 180 {
		return TowerRecord{}, fmt.Errorf("%v (%v): longitude %v is outside -180..180", label, id, lng)
	}
	if row[5] == "" {
		return TowerRecord{}, fmt.Errorf("%v (%v) names no frequency or band", label, id)
	}
	country := strings.ToUpper(row[11])
	if len(country) != 2 || !isUpperLetters(country) {
		return TowerRecord{}, fmt.Errorf("%v (%v): country %q is not a two-letter code", label, id, row[11])
	}
	elev := 0
	if row[12] != "" {
		value, err := strconv.Atoi(row[12])
		if err != nil {
			return TowerRecord{}, fmt.Errorf("%v (%v): elevation %q is not a whole number", label, id, row[12])
		}
		if value < 0 {
			return TowerRecord{}, fmt.Errorf("%v (%v): elevation %v is negative", label, id, value)
		}
		elev = value
	}
	return TowerRecord{
		ID:         id,
		Name:       firstNonEmpty([]string{row[1], id}),
		Type:       towerType,
		Lat:        lat,
		Lng:        lng,
		Freq:       row[5],
		Offset:     row[6],
		Tone:       row[7],
		Operator:   firstNonEmpty([]string{row[8], "unknown"}),
		City:       firstNonEmpty([]string{row[9], "unknown"}),
		Region:     strings.ToUpper(row[10]),
		Country:    country,
		ElevMeters: elev,
	}, nil
}

// parseTowerCSVFloat parses one numeric dataset field, tolerating the quotes and
// padding a spreadsheet export adds.
func parseTowerCSVFloat(text string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(text), 64)
}

// isUpperLetters reports whether text is made only of upper-case ASCII letters.
func isUpperLetters(text string) bool {
	for i := range len(text) {
		if text[i] < 'A' || text[i] > 'Z' {
			return false
		}
	}
	return len(text) > 0
}

// firstNonEmpty returns the first non-empty entry, or an empty string.
func firstNonEmpty(values []string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// runTower answers one tower request: a discovery sub-command, the detailed view
// of one site, or the two shorthands an operator is most likely to type.
func (c *commandContext) runTower() []string {
	records, entries := c.towerRecords()
	command := towerCommandWord(c.req.Command)
	if kind, text := splitDiscovery(c.Args); kind != "" {
		return c.runTowerDiscovery(command, records, entries, kind, text)
	}
	if word, rest := splitCommandLine(c.Args); word == towerInfoKind {
		return c.runTowerInfo(command, records, entries, rest)
	}
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + towerUsage, towerUsageHint}
	}
	// A bare identifier is the detailed view, which is what "tower W6PW-2M"
	// obviously means, and a bare position is the proximity answer, which is
	// what "tower 37.8,-122.4" obviously means. Anything else is a name that
	// could be a place or a site, and guessing between the two would answer a
	// different question than the one asked, so the usage says so instead.
	if record, ok := towerRecordByID(records, c.Args); ok {
		return towerInfoLines(record)
	}
	if point, err := ParseLocation(c.Args); err == nil {
		q := discoveryQuery{Command: command, Catalog: towerCatalogName, Kind: discoveryKindNear}
		return c.renderTowerNear(q, records, entries, point, c.Args)
	}
	return []string{"Usage: " + towerUsage, towerUsageHint}
}

// towerCommandWord returns the command word the request was typed with, which is
// the alias when an alias was used, so a page footer quotes the command the asker
// actually knows.
func towerCommandWord(line string) string {
	if name, _ := splitCommandLine(line); name != "" {
		return name
	}
	return "tower"
}

// runTowerDiscovery answers a search, near, or list request from the catalog.
// Every answer is offline, so the command works with no provider at all — which
// is the only kind of provider this command has.
func (c *commandContext) runTowerDiscovery(command string, records []TowerRecord, entries []catalogEntry, kind, text string) []string {
	q := discoveryQuery{Command: command, Catalog: towerCatalogName, Kind: kind}
	switch kind {
	case discoveryKindSearch:
		words, page := splitPageArgument(text)
		query := strings.Join(searchWords(words), " ")
		if query == "" {
			return []string{"Usage: " + command + " search <query> [page]", towerSearchHint(command)}
		}
		q.Text = query
		matches := searchCatalog(entries, query)
		if len(matches) == 0 {
			return []string{fmt.Sprintf("No %v match %q.", towerCatalogName, q.Text), towerSearchHint(command)}
		}
		return c.renderCatalogPage(q, matches, page)
	case discoveryKindNear:
		return c.renderTowerNearArgument(q, records, entries, text)
	case discoveryKindList:
		filter, page := splitListArgument(text)
		filtered := filterTowerRecords(records, filter)
		if len(filtered) == 0 {
			return []string{
				fmt.Sprintf("No %v in %v.", towerCatalogName, safeEcho(filter, maxDiscoveryEchoBytes)),
				towerListHint(command),
			}
		}
		q.Text = strings.ToUpper(strings.Join(strings.Fields(filter), " "))
		return c.renderCatalogPage(q, catalogFrom(filtered, TowerRecord.catalogEntry), page)
	default:
		return []string{"Usage: " + towerUsage, towerUsageHint}
	}
}

// renderTowerNearArgument resolves a proximity request's argument and renders the
// closest sites, or explains why the argument could not be placed.
func (c *commandContext) renderTowerNearArgument(q discoveryQuery, records []TowerRecord, entries []catalogEntry, argument string) []string {
	if strings.TrimSpace(argument) == "" {
		return []string{"Usage: " + q.Command + " near <place|coords|pluscode>"}
	}
	point, ok := resolveCatalogPoint(entries, argument)
	if !ok {
		return []string{
			fmt.Sprintf("%v: %q is not a place, a coordinate, or a plus code I can place",
				q.Command, safeEcho(argument, maxDiscoveryEchoBytes)),
			fmt.Sprintf("Try coordinates (37.8,-122.4), a plus code (849VCWC8+R9), or %q.",
				q.Command+" list"),
		}
	}
	return c.renderTowerNear(q, records, entries, point, argument)
}

// renderTowerNear renders the three closest sites to a position as the single
// page a proximity answer is. The rows carry the distance in kilometers and the
// bearing, because those are what a directional antenna is aimed with, and a
// site inside China adds the GCJ-02 coordinate the operator's map app expects.
func (c *commandContext) renderTowerNear(q discoveryQuery, records []TowerRecord, entries []catalogEntry, point LatLng, argument string) []string {
	q.Text = strings.Join(strings.Fields(argument), " ")
	near := nearestCatalog(entries, point, discoveryNearLimit)
	if len(near) == 0 {
		return []string{fmt.Sprintf("No %v near %v.", towerCatalogName, q.Text)}
	}
	lines := make([]string, 0, len(near)+3)
	lines = append(lines, q.header(1, 1))
	for i := range near {
		record, ok := towerRecordByID(records, near[i].Entry.ID)
		if !ok {
			continue
		}
		lines = append(lines, c.towerNearRow(q, record, &near[i]))
	}
	if hint := towerGCJHint(records, near); hint != "" {
		lines = append(lines, hint)
	}
	// The footer stays the last line, which is the contract every paged answer
	// keeps: the line that says how to continue is always the last one.
	return append(lines, c.catalogFooter(q, 1, 1)...)
}

// towerNearRow renders one proximity row: the identity the detailed view takes,
// how far away the site is and on what bearing, the service, the frequency with
// its offset and tone in the compact form a radio operator reads, and the place.
func (c *commandContext) towerNearRow(q discoveryQuery, record TowerRecord, distance *catalogDistance) string {
	id := record.ID
	if c.micronLinks() {
		id = micronLink(record.ID, "/msg "+c.echoNick()+" "+q.Command+" "+towerInfoKind+" "+record.ID)
	}
	return fmt.Sprintf("  %v (%.1f km %.0f° %v) [%v]: %v - %v", id,
		distance.Meters/1000, normalizeDegrees(distance.Bearing), CompassPoint(distance.Bearing),
		record.Type, towerFrequencySegment(record), towerPlace(record))
}

// towerFrequencySegment renders the frequency, the repeater offset, and the
// tone a machine requires, in the compact form a band plan is written in:
// "145.150 -0.6 (PL 114.8)". A site with no offset or tone — a cellular mast, a
// marine channel — prints its band or channel alone.
func towerFrequencySegment(record TowerRecord) string {
	segment := record.Freq
	if offset := compactUnit(record.Offset, "MHz"); offset != "" {
		segment += " " + offset
	}
	if tone := compactUnit(record.Tone, "Hz"); tone != "" {
		segment += " (PL " + tone + ")"
	}
	return segment
}

// compactUnit drops a trailing unit from a value that is already displayed beside
// its neighbors' units, so a row reads as a band plan rather than as prose. The
// catalogs' own values keep their units; only the compact row shortens them.
func compactUnit(value, unit string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), unit))
}

// towerPlace renders where a site is: its name, its city, and its state or
// province. The country is appended only outside the United States, where it
// carries information the reader does not already have.
func towerPlace(record TowerRecord) string {
	place := record.Name + ", " + record.City + ", " + record.Region
	if record.Country != "US" {
		place += ", " + record.Country
	}
	return place
}

// towerGCJHint returns the GCJ-02 coordinate of the closest site inside China in
// a proximity answer, so the operator can paste it into a Chinese map app, or an
// empty string when no site in the answer is in China.
func towerGCJHint(records []TowerRecord, near []catalogDistance) string {
	for i := range near {
		record, ok := towerRecordByID(records, near[i].Entry.ID)
		if !ok || !IsInChina(record.Lat, record.Lng) {
			continue
		}
		gcjLat, gcjLng := WGS84ToGCJ02(record.Lat, record.Lng)
		return fmt.Sprintf("GCJ-02 %v (paste into Amap/Gaode/WeChat): %v",
			record.ID, FormatLatLng(LatLng{Lat: gcjLat, Lng: gcjLng}))
	}
	return ""
}

// runTowerInfo renders one site in full, or explains that no site by that
// identifier exists. A name is accepted as well as an identifier, because an
// operator who heard a callsign on the air may not know the catalog's own id.
func (c *commandContext) runTowerInfo(command string, records []TowerRecord, entries []catalogEntry, id string) []string {
	token := strings.TrimSpace(id)
	if token == "" {
		return []string{"Usage: " + command + " info <id>"}
	}
	if record, ok := towerRecordByID(records, token); ok {
		return towerInfoLines(record)
	}
	if entry, ok := findCatalogEntry(entries, token); ok {
		if record, ok := towerRecordByID(records, entry.ID); ok {
			return towerInfoLines(record)
		}
	}
	return []string{
		fmt.Sprintf("No tower site with the id %q.", safeEcho(token, maxDiscoveryEchoBytes)),
		fmt.Sprintf("Try %q or %q.", command+" search <query>", command+" list"),
	}
}

// towerInfoLines renders the detailed single-site answer: the identity, the
// service, the place, the exact position in both datums when the site is in
// China, the Maidenhead grid an operator signs with, the elevation, and every
// radio detail needed to use the site.
func towerInfoLines(record TowerRecord) []string {
	lines := []string{fmt.Sprintf("%v [%v]: %v", record.ID, record.Type, towerPlace(record))}
	position := fmt.Sprintf("  WGS-84 %v | Maidenhead %v | %v | %v",
		FormatLatLng(LatLng{Lat: record.Lat, Lng: record.Lng}),
		LatLngToMaidenhead(record.Lat, record.Lng),
		towerElevationSegment(record),
		towerTypeNames[record.Type])
	lines = append(lines, position)
	if gcj := FormatGCJ02(record.Lat, record.Lng); gcj != "" {
		lines = append(lines, fmt.Sprintf("  GCJ-02 %v (paste into Amap/Gaode/WeChat)", gcj))
	}
	radio := []string{record.Freq}
	if record.Offset != "" {
		radio = append(radio, "offset "+record.Offset)
	}
	if record.Tone != "" {
		radio = append(radio, "tone "+record.Tone)
	}
	radio = append(radio, "operator "+record.Operator)
	return append(lines, "  "+strings.Join(radio, ", "))
}

// towerElevationSegment renders the elevation, or says plainly that it is not
// recorded rather than printing a zero that looks like sea level.
func towerElevationSegment(record TowerRecord) string {
	if record.ElevMeters == 0 {
		return "elevation not recorded"
	}
	return fmt.Sprintf("elevation %v m", record.ElevMeters)
}

// towerRecordByID resolves an identifier against a record table, ignoring case
// and surrounding space.
func towerRecordByID(records []TowerRecord, id string) (TowerRecord, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	if key == "" {
		return TowerRecord{}, false
	}
	for _, record := range records {
		if strings.ToLower(record.ID) == key {
			return record, true
		}
	}
	return TowerRecord{}, false
}

// filterTowerRecords keeps the records a list filter names, in catalog order.
func filterTowerRecords(records []TowerRecord, filter string) []TowerRecord {
	out := make([]TowerRecord, 0, len(records))
	for _, record := range records {
		if towerRegionMatches(record, filter) {
			out = append(out, record)
		}
	}
	return out
}

// towerSearchHint returns the second line of an empty search answer: the other
// ways into the same catalog, in the words the asker would type.
func towerSearchHint(command string) string {
	return fmt.Sprintf("Try fewer words, a province code (%q), or a position (%q).",
		command+" list SC", command+" near 39.9,116.4")
}

// towerListHint returns the second line of an empty filter answer.
func towerListHint(command string) string {
	return fmt.Sprintf("Try a country (%v), a province or state (BJ, GD, SC, CA), or %q for every site.",
		"US, CN", command+" list")
}
