// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the disaster situation-report board: the sitrep command and
// the geolocated, crowdsourced report store behind it. After an earthquake, a
// flood, or a hurricane, the useful question is never "what happened" in
// general but "what is within reach of here": which bridge is out, where the
// potable water is, which shelter still has power. Reports are therefore
// recorded with a position and answered by proximity.
//
// The board is a bounded, self-expiring store: at most five hundred reports,
// and nothing older than a week. A situation report is only actionable while it
// is current, and an unbounded board on a node with a small disk would
// eventually cost more than it gives.

package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Situation-report wordings and bounds.
const (
	// sitrepUsage is the usage line for the whole command.
	sitrepUsage = "sitrep <add|near|recent> ..."
	// sitrepAddUsage is the usage line for adding a report.
	sitrepAddUsage = "sitrep add <loc> <" + "HAZARD|RESOURCE|SHELTER|ROAD|INFO" + "> <text>"
	// sitrepNearUsage is the usage line for a proximity search.
	sitrepNearUsage = "sitrep near <loc> [radius]"
	// sitrepRecentUsage is the usage line for a recency listing.
	sitrepRecentUsage = "sitrep recent [n]"
	// sitrepFileName is the state file within the storage directory.
	sitrepFileName = "sitrep.json"
	// maxSitrepReports bounds the board. When it is full the oldest report is
	// dropped, which is reasonable because a report also expires after a week.
	maxSitrepReports = 500
	// sitrepTTL is how long one report stays on the board. Beyond a week a
	// situation report describes a world that no longer exists.
	sitrepTTL = 7 * 24 * time.Hour
	// maxSitrepTextBytes bounds the free text of one report.
	maxSitrepTextBytes = 140
	// defaultSitrepRadius is how far a proximity search reaches when the asker
	// does not say.
	defaultSitrepRadius = 25 * 1000.0
	// maxSitrepRadius bounds a proximity search, so one request cannot walk an
	// enormous board at once.
	maxSitrepRadius = 500 * 1000.0
	// defaultSitrepRecent and maxSitrepRecent bound a recency listing.
	defaultSitrepRecent = 10
	maxSitrepRecent     = 25
	// sitrepAddedLine is the confirmation a new report gets.
	sitrepAddedLine = "SitRep #%d recorded [%v @ %v] by @%v"
	// sitrepNearHeader is the header of a proximity answer.
	sitrepNearHeader = "SitReps near %v (within %v):"
	// sitrepNearRow is one proximity answer row.
	sitrepNearRow = "[%v %v] %v @ %v (%v ago): %v"
	// sitrepRecentHeader is the header of a recency answer.
	sitrepRecentHeader = "Most recent SitReps: %v"
	// sitrepRecentRow is one recency answer row.
	sitrepRecentRow = "%v [%v @ %v] (%v ago) by @%v: %v"
	// sitrepEmptyNearLine is the answer to a proximity search that finds
	// nothing.
	sitrepEmptyNearLine = "no SitReps within %v of %v"
	// sitrepEmptyBoardLine is the answer to a listing of an empty board.
	sitrepEmptyBoardLine = "No SitReps on the board."
)

// SitrepCategories are the report categories, in the order help lists them.
var SitrepCategories = []string{"HAZARD", "RESOURCE", "SHELTER", "ROAD", "INFO"}

// SitrepReport is one situation report.
type SitrepReport struct {
	// ID is the report's number, unique within this bot's board.
	ID int `json:"id"`
	// Sender is the human-readable reporter: nick and hash prefix.
	Sender string `json:"sender"`
	// SenderHash is the reporter's identity hash, hex.
	SenderHash string `json:"sender_hash"`
	// Location is the normalized Plus Code.
	Location string `json:"location"`
	// LatLng is the report's coordinate.
	LatLng LatLng `json:"coords"`
	// Category is one of SitrepCategories.
	Category string `json:"category"`
	// Text is the sanitized report text.
	Text string `json:"text"`
	// Timestamp is when the report was filed.
	Timestamp time.Time `json:"timestamp"`
}

// sitrepStore is the bounded, self-expiring report board.
type sitrepStore struct {
	mu      sync.Mutex
	path    string
	reports []SitrepReport
	nextID  int
	loaded  bool
	loadErr error
	now     func() time.Time
}

// newSitrepStore builds an empty board backed by storageDir. An empty
// storageDir keeps everything in memory.
func newSitrepStore(storageDir string) *sitrepStore {
	s := &sitrepStore{now: time.Now, nextID: 1}
	if strings.TrimSpace(storageDir) != "" {
		s.path = filepath.Join(storageDir, sitrepFileName)
	}
	return s
}

// sitrepState is the on-disk shape of the board.
type sitrepState struct {
	NextID  int            `json:"next_id"`
	Reports []SitrepReport `json:"reports"`
}

// load reads the board from disk once and ages out anything past its life.
func (s *sitrepStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	var state sitrepState
	found, err := readJSONState(s.path, &state)
	if err != nil {
		s.loadErr = err
		return
	}
	if !found {
		return
	}
	s.reports = state.Reports
	if state.NextID > 0 {
		s.nextID = state.NextID
	}
	for _, report := range s.reports {
		if report.ID >= s.nextID {
			s.nextID = report.ID + 1
		}
	}
	s.pruneLocked(s.now())
}

// persist writes the board to disk. The caller holds the lock.
func (s *sitrepStore) persist() error {
	return writeJSONState(s.path, sitrepState{NextID: s.nextID, Reports: s.reports})
}

// pruneLocked drops expired reports and enforces the board's bound.
func (s *sitrepStore) pruneLocked(now time.Time) {
	cutoff := now.Add(-sitrepTTL)
	kept := s.reports[:0]
	for _, report := range s.reports {
		if report.Timestamp.Before(cutoff) {
			continue
		}
		kept = append(kept, report)
	}
	s.reports = kept
	if len(s.reports) > maxSitrepReports {
		s.reports = s.reports[len(s.reports)-maxSitrepReports:]
	}
}

// add records one report and returns it with its assigned id.
func (s *sitrepStore) add(report SitrepReport) (SitrepReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.pruneLocked(s.now())
	report.ID = s.nextID
	s.nextID++
	if report.Timestamp.IsZero() {
		report.Timestamp = s.now()
	}
	s.reports = append(s.reports, report)
	if len(s.reports) > maxSitrepReports {
		s.reports = s.reports[len(s.reports)-maxSitrepReports:]
	}
	return report, s.persist()
}

// all returns a copy of every current report on the board, oldest first. The
// board is aged out on the way, so a report that has outlived its week is never
// answered with, whether or not anything has been written since.
func (s *sitrepStore) all() []SitrepReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.pruneLocked(s.now())
	return append([]SitrepReport(nil), s.reports...)
}

// sitrepNear is one report together with how far away it is.
type sitrepNear struct {
	Report  SitrepReport
	Meters  float64
	Bearing float64
}

// near returns the reports within radius meters of point, closest first.
func (s *sitrepStore) near(point LatLng, radius float64) []sitrepNear {
	var found []sitrepNear
	for _, report := range s.all() {
		meters := HaversineDistance(point, report.LatLng)
		if meters > radius {
			continue
		}
		found = append(found, sitrepNear{
			Report:  report,
			Meters:  meters,
			Bearing: InitialBearing(point, report.LatLng),
		})
	}
	for i := 1; i < len(found); i++ {
		for j := i; j > 0 && found[j].Meters < found[j-1].Meters; j-- {
			found[j], found[j-1] = found[j-1], found[j]
		}
	}
	return found
}

// recent returns the newest reports, newest first, at most limit of them.
func (s *sitrepStore) recent(limit int) []SitrepReport {
	all := s.all()
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}
	out := make([]SitrepReport, 0, limit)
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out
}

// runSitrep dispatches the sitrep subcommands.
func (c *commandContext) runSitrep() []string {
	sub, rest := splitCommandLine(c.Args)
	switch strings.ToLower(sub) {
	case "":
		return []string{"Usage: " + sitrepUsage, sitrepAddUsage, sitrepNearUsage, sitrepRecentUsage}
	case "add":
		return c.runSitrepAdd(rest)
	case "near":
		return c.runSitrepNear(rest)
	case "recent":
		return c.runSitrepRecent(rest)
	default:
		return []string{"Usage: " + sitrepUsage, sitrepAddUsage, sitrepNearUsage, sitrepRecentUsage}
	}
}

// runSitrepAdd records one report.
func (c *commandContext) runSitrepAdd(args string) []string {
	location, category, text, ok := splitSitrepAdd(args)
	if !ok {
		return []string{"Usage: " + sitrepAddUsage,
			"the category is one of " + strings.Join(SitrepCategories, ", ")}
	}
	point, err := ParseLocation(location)
	if err != nil {
		return []string{"sitrep: no location found — " + locationNotationHelp}
	}
	text = safeEcho(text, maxSitrepTextBytes)
	if text == "" {
		return []string{"Usage: " + sitrepAddUsage, "the text says what is there"}
	}
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		return []string{"sitrep: could not build a Plus Code: " + err.Error()}
	}
	store := c.reg.sitreps()
	if store == nil {
		return []string{"sitrep: the report board is unavailable on this bot"}
	}
	nick := safeEcho(c.peerName(c.req.Msg.Src), maxEchoNickBytes)
	report, err := store.add(SitrepReport{
		Sender:     nick,
		SenderHash: hexString(c.req.Msg.Src),
		Location:   code,
		LatLng:     point,
		Category:   category,
		Text:       text,
		Timestamp:  c.now(),
	})
	if err != nil {
		logf("sitrep: could not persist report #%v: %v", report.ID, err)
	}
	return []string{fmt.Sprintf(sitrepAddedLine, report.ID, report.Category, report.Location, nick)}
}

// splitSitrepAdd splits an add request into its location, category, and text.
// The location comes first and may contain spaces, so the category is the first
// field that is a known category.
func splitSitrepAdd(args string) (string, string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(args))
	categoryIndex := -1
	for i, field := range fields {
		if i == 0 {
			continue
		}
		if isSitrepCategory(field) {
			categoryIndex = i
			break
		}
	}
	if categoryIndex < 1 || categoryIndex == len(fields)-1 {
		return "", "", "", false
	}
	return strings.Join(fields[:categoryIndex], " "),
		strings.ToUpper(fields[categoryIndex]),
		strings.Join(fields[categoryIndex+1:], " "), true
}

// isSitrepCategory reports whether a word is a report category, ignoring case.
func isSitrepCategory(word string) bool {
	return slices.Contains(SitrepCategories, strings.ToUpper(strings.TrimSpace(word)))
}

// runSitrepNear answers what is within reach of a position.
func (c *commandContext) runSitrepNear(args string) []string {
	location, radius, ok := splitSitrepNear(args)
	if !ok {
		return []string{"Usage: " + sitrepNearUsage}
	}
	point, err := ParseLocation(location)
	if err != nil {
		return []string{"sitrep: no location found — " + locationNotationHelp}
	}
	store := c.reg.sitreps()
	if store == nil {
		return []string{"sitrep: the report board is unavailable on this bot"}
	}
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		return []string{"sitrep: could not build a Plus Code: " + err.Error()}
	}
	found := store.near(point, radius)
	if len(found) == 0 {
		return []string{fmt.Sprintf(sitrepEmptyNearLine, formatRadius(radius), code)}
	}
	now := c.now()
	lines := []string{fmt.Sprintf(sitrepNearHeader, code, formatRadius(radius))}
	for _, entry := range found {
		lines = append(lines, fmt.Sprintf(sitrepNearRow,
			formatNearDistance(entry.Meters), CompassPoint(entry.Bearing),
			entry.Report.Category, entry.Report.Location,
			formatAge(now.Sub(entry.Report.Timestamp)), entry.Report.Text))
	}
	return lines
}

// splitSitrepNear splits a proximity request into its location and its radius.
// The radius is the last field when it parses as a distance, and the location
// is everything else.
func splitSitrepNear(args string) (string, float64, bool) {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return "", 0, false
	}
	radius := defaultSitrepRadius
	if len(fields) > 1 {
		if parsed, err := ParseDistance(fields[len(fields)-1]); err == nil {
			radius = parsed
			fields = fields[:len(fields)-1]
		}
	}
	if radius < 0 || radius > maxSitrepRadius {
		radius = maxSitrepRadius
	}
	location := strings.Join(fields, " ")
	if location == "" {
		return "", 0, false
	}
	return location, radius, true
}

// runSitrepRecent lists the newest reports on the board.
func (c *commandContext) runSitrepRecent(args string) []string {
	store := c.reg.sitreps()
	if store == nil {
		return []string{"sitrep: the report board is unavailable on this bot"}
	}
	limit := defaultSitrepRecent
	if trimmed := strings.TrimSpace(args); trimmed != "" {
		parsed, err := parseStoreID(trimmed)
		if err != nil {
			return []string{"Usage: " + sitrepRecentUsage}
		}
		limit = min(parsed, maxSitrepRecent)
	}
	reports := store.recent(limit)
	if len(reports) == 0 {
		return []string{sitrepEmptyBoardLine}
	}
	now := c.now()
	lines := []string{fmt.Sprintf(sitrepRecentHeader, pluralCount(len(reports), "report", "reports"))}
	for _, report := range reports {
		lines = append(lines, fmt.Sprintf(sitrepRecentRow,
			fmt.Sprintf("#%v", report.ID), report.Category, report.Location,
			formatAge(now.Sub(report.Timestamp)),
			safeEcho(report.Sender, maxEchoNickBytes), report.Text))
	}
	return lines
}

// formatRadius renders a search radius: kilometers for anything past a
// kilometer, meters below it, with no trailing zeros.
func formatRadius(meters float64) string {
	if meters < 1000 {
		return fmt.Sprintf("%.0f m", meters)
	}
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", meters/1000), "0"), ".") + " km"
}

// formatNearDistance renders how far away a report is: meters below a
// kilometer, kilometers to one decimal place above it.
func formatNearDistance(meters float64) string {
	if meters < 1000 {
		return fmt.Sprintf("%.0fm", meters)
	}
	return fmt.Sprintf("%.1fkm", meters/1000)
}

// sitreps returns the bot's report board, or nil when the bot was built without
// one.
func (r *registry) sitreps() *sitrepStore {
	if r.bot == nil {
		return nil
	}
	return r.bot.sitreps
}
