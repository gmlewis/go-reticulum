// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the distress beacon registry: the sos command and the
// persistent store behind it. A distress call on a mesh is the one message that
// must not be lost, and the link that carried it will very probably fail a
// moment later — the transmitter is on a ridge with a dying battery, or the
// antenna came down with the tent. The registry is therefore written to disk as
// soon as it is accepted, so a bot that restarts, or a node that reboots, still
// knows who called for help, where they were, and how urgent it was.
//
// A beacon is also acted on immediately, not merely recorded: the sender gets a
// direct NOTICE confirming it landed, every room the bot has joined gets an
// alert, and — when the operator has configured one — a dispatch destination
// gets the same report over LXMF, which is store-and-forward and keeps trying
// after the local link is gone.

package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

// SOS command wordings and bounds.
const (
	// sosUsage is the usage line for the whole command.
	sosUsage = "sos <loc> <RED|YELLOW|GREEN|INFO> <details>"
	// sosListUsage is the usage line for the listing.
	sosListUsage = "sos list"
	// sosClearUsage is the usage line for clearing one beacon.
	sosClearUsage = "sos clear <id>"
	// sosFileName is the state file within the storage directory.
	sosFileName = "sos.json"
	// maxSOSRecords caps the registry. When it is full the oldest resolved
	// beacon is dropped first, and only then the oldest active one, so a long
	// incident cannot silently evict the beacons that still matter.
	maxSOSRecords = 100
	// maxSOSDetailsBytes bounds the free text a beacon carries. It is repeated
	// into every room and over LXMF, so it is kept short enough to fit one
	// envelope with room to spare.
	maxSOSDetailsBytes = 120
	// sosRecordedLine is the confirmation the sender and the room see.
	sosRecordedLine = "[SOS #%d RECORDED] %v @ %v by @%v: %v | %v"
	// sosAlertLine is the high-priority broadcast every joined room sees.
	sosAlertLine = "[SOS ALERT #%d] %v at %v by @%v: %v"
	// sosDirectLine is the direct NOTICE the sender gets.
	sosDirectLine = "[SOS #%d] %v recorded at %v. %v"
	// sosNoBeaconsLine is the answer to a listing with nothing active.
	sosNoBeaconsLine = "No active SOS beacons."
	// sosUnknownBeaconLine is the answer to clearing an id that does not exist.
	sosUnknownBeaconLine = "no SOS #%v"
	// sosNotYoursLine is the answer to clearing somebody else's beacon.
	sosNotYoursLine = "only the client that raised SOS #%v can clear it"
	// sosClearedLine is the answer to a successful clear.
	sosClearedLine = "[SOS #%v CLEARED]"
	// sosLXMFQueued is the confirmation suffix for a queued dispatch message.
	sosLXMFQueued = "queued to LXMF dispatch"
	// sosLXMFLine is the dispatch payload.
	sosLXMFLine = "[SOS #%d] %v at %v by %v: %v"
)

// SOSTriageLevels are the triage categories a beacon may carry, most urgent
// first. They are the START/JMESI words a first responder already knows.
var SOSTriageLevels = []string{"RED", "YELLOW", "GREEN", "INFO"}

// SOSRecord is one distress beacon.
type SOSRecord struct {
	// ID is the beacon's number, unique within this bot's registry.
	ID int `json:"id"`
	// Sender is the human-readable sender: nick and hash prefix.
	Sender string `json:"sender"`
	// SenderHash is the sender's identity hash, hex, which is what makes the
	// beacon theirs to clear.
	SenderHash string `json:"sender_hash"`
	// Location is the normalized Plus Code.
	Location string `json:"location"`
	// LatLng is the beacon's coordinate.
	LatLng LatLng `json:"coords"`
	// Triage is one of SOSTriageLevels.
	Triage string `json:"triage"`
	// Details is the sanitized free text the sender supplied.
	Details string `json:"details"`
	// Timestamp is when the beacon was raised.
	Timestamp time.Time `json:"timestamp"`
	// Resolved reports that the beacon has been stood down.
	Resolved bool `json:"resolved"`
}

// sosStore is the bounded, persistent beacon registry. Its zero value is
// unusable; newSOSStore builds one.
type sosStore struct {
	mu      sync.Mutex
	path    string
	records []SOSRecord
	nextID  int
	loaded  bool
	// loadErr remembers why the last load failed, so the command can say the
	// registry is unusable instead of silently starting empty.
	loadErr error
	// now is the clock, injectable so the age of a beacon is tested without
	// waiting.
	now func() time.Time
}

// newSOSStore builds an empty registry backed by storageDir. An empty
// storageDir keeps everything in memory, which is what a unit test or a bot
// with no configured storage gets.
func newSOSStore(storageDir string) *sosStore {
	s := &sosStore{now: time.Now, nextID: 1}
	if strings.TrimSpace(storageDir) != "" {
		s.path = filepath.Join(storageDir, sosFileName)
	}
	return s
}

// sosState is the on-disk shape of the registry.
type sosState struct {
	NextID  int         `json:"next_id"`
	Records []SOSRecord `json:"records"`
}

// load reads the registry from disk once. A file that cannot be decoded leaves
// the registry empty and remembers the failure, so a corrupt file is reported
// rather than silently overwritten with an empty one.
func (s *sosStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	var state sosState
	found, err := readJSONState(s.path, &state)
	if err != nil {
		s.loadErr = err
		return
	}
	if !found {
		return
	}
	s.records = state.Records
	if state.NextID > 0 {
		s.nextID = state.NextID
	}
	// A hand-edited file could name an id the counter has already passed, and
	// reusing a number would make two different beacons indistinguishable.
	for _, record := range s.records {
		if record.ID >= s.nextID {
			s.nextID = record.ID + 1
		}
	}
}

// persist writes the registry to disk, truncating the report to the bounded
// state. The caller holds the lock.
func (s *sosStore) persist() error {
	return writeJSONState(s.path, sosState{NextID: s.nextID, Records: s.records})
}

// add records one beacon and returns it with its assigned id.
func (s *sosStore) add(record SOSRecord) (SOSRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	record.ID = s.nextID
	s.nextID++
	if record.Timestamp.IsZero() {
		record.Timestamp = s.now()
	}
	s.records = append(s.records, record)
	s.enforceCapLocked()
	return record, s.persist()
}

// enforceCapLocked drops the oldest resolved beacon, and then the oldest beacon
// of any kind, until the registry fits its bound.
func (s *sosStore) enforceCapLocked() {
	for len(s.records) > maxSOSRecords {
		dropped := -1
		for i := range s.records {
			if s.records[i].Resolved {
				dropped = i
				break
			}
		}
		if dropped < 0 {
			dropped = 0
		}
		s.records = append(s.records[:dropped], s.records[dropped+1:]...)
	}
}

// active returns the unresolved beacons, newest last.
func (s *sosStore) active() []SOSRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	out := make([]SOSRecord, 0, len(s.records))
	for _, record := range s.records {
		if !record.Resolved {
			out = append(out, record)
		}
	}
	return out
}

// clear stands a beacon down. Only the identity that raised it may clear it,
// which is what keeps a stranger in a room from silencing somebody else's call
// for help.
func (s *sosStore) clear(id int, senderHash string) (SOSRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	for i := range s.records {
		record := &s.records[i]
		if record.ID != id {
			continue
		}
		if record.SenderHash != senderHash {
			return SOSRecord{}, errSOSNotYours
		}
		record.Resolved = true
		return *record, s.persist()
	}
	return SOSRecord{}, errSOSUnknownBeacon
}

// Errors the registry reports. They are sentinels so the command can render the
// right line without matching on strings.
var (
	// errSOSUnknownBeacon reports an id that is not in the registry.
	errSOSUnknownBeacon = fmt.Errorf("unknown SOS beacon")
	// errSOSNotYours reports an attempt to clear somebody else's beacon.
	errSOSNotYours = fmt.Errorf("not your SOS beacon")
)

// runSOS dispatches the sos subcommands: raise a beacon, list the active ones,
// or stand one down.
func (c *commandContext) runSOS() []string {
	sub, rest := splitCommandLine(c.Args)
	switch strings.ToLower(sub) {
	case "":
		return []string{"Usage: " + sosUsage, sosListUsage + " | " + sosClearUsage}
	case "list":
		return c.runSOSList()
	case "clear":
		return c.runSOSClear(rest)
	default:
		return c.runSOSRaise(c.Args)
	}
}

// runSOSRaise records one beacon and raises the alarm everywhere the bot can
// reach.
func (c *commandContext) runSOSRaise(args string) []string {
	location, triage, details, ok := splitSOSRaise(args)
	if !ok {
		return []string{"Usage: " + sosUsage, "the triage level is one of " + strings.Join(SOSTriageLevels, ", ")}
	}
	point, err := ParseLocation(location)
	if err != nil {
		return []string{"sos: no location found — " + locationNotationHelp}
	}
	details = safeEcho(details, maxSOSDetailsBytes)
	if details == "" {
		return []string{"Usage: " + sosUsage, "the details say what is wrong and how many people need help"}
	}
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		return []string{"sos: could not build a Plus Code: " + err.Error()}
	}
	if c.reg.sos() == nil {
		return []string{"sos: the beacon registry is unavailable on this bot"}
	}

	nick := safeEcho(c.peerName(c.req.Msg.Src), maxEchoNickBytes)
	senderHash := hexString(c.req.Msg.Src)
	record, err := c.reg.sos().add(SOSRecord{
		Sender:     nick,
		SenderHash: senderHash,
		Location:   code,
		LatLng:     point,
		Triage:     triage,
		Details:    details,
		Timestamp:  c.now(),
	})
	if err != nil {
		// The beacon is still acted on below: a disk failure must never cost
		// somebody their call for help.
		logf("sos: could not persist beacon #%v: %v", record.ID, err)
	}

	alerted := c.alertSOS(record)
	dispatched := c.dispatchSOS(record)
	summary := sosAlertSummary(alerted, dispatched)
	c.confirmSOS(record, summary)
	return []string{fmt.Sprintf(sosRecordedLine, record.ID, record.Triage, record.Location,
		nick, record.Details, summary)}
}

// splitSOSRaise splits a raise request into its location, triage level, and
// details. The location comes first and may contain spaces, so the triage word
// is the first field that is a triage level.
func splitSOSRaise(args string) (string, string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(args))
	triageIndex := -1
	for i, field := range fields {
		if i == 0 {
			continue
		}
		if isSOSTriage(field) {
			triageIndex = i
			break
		}
	}
	if triageIndex < 1 || triageIndex == len(fields)-1 {
		return "", "", "", false
	}
	return strings.Join(fields[:triageIndex], " "),
		strings.ToUpper(fields[triageIndex]),
		strings.Join(fields[triageIndex+1:], " "), true
}

// isSOSTriage reports whether a word is a triage level, ignoring case.
func isSOSTriage(word string) bool {
	return slices.Contains(SOSTriageLevels, strings.ToUpper(strings.TrimSpace(word)))
}

// alertSOS broadcasts the high-priority alert into every room the session has
// joined, and returns how many notices went out. The emergency room is not
// special-cased: every joined room gets the alert, which is strictly more
// useful than one designated room, because a room is only reachable while the
// bot is joined to it.
func (c *commandContext) alertSOS(record SOSRecord) int {
	text := fmt.Sprintf(sosAlertLine, record.ID, record.Triage, record.Location,
		safeEcho(record.Sender, maxEchoNickBytes), record.Details)
	sent := 0
	for _, room := range c.conn().JoinedRoomList() {
		if _, err := c.conn().SendNotice(room, text); err != nil {
			logf("sos: could not alert %q: %v", room, err)
			continue
		}
		sent++
	}
	return sent
}

// dispatchSOS queues the beacon to the operator's LXMF dispatch destination,
// when one is configured. LXMF is store-and-forward, so this is the copy that
// keeps trying after the local link has failed.
func (c *commandContext) dispatchSOS(record SOSRecord) bool {
	cfg := c.reg.config()
	if cfg == nil || !cfg.LXMFEnabled || c.reg.lxmf == nil {
		return false
	}
	if len(cfg.EmergencyLXMFDestinationHash) != rrc.IdentityHashLen {
		return false
	}
	ask := lxmfAsk{
		AskerHash: c.req.Msg.Src,
		AskerNick: safeEcho(c.peerName(c.req.Msg.Src), maxEchoNickBytes),
		HubHex:    c.conn().HubAddressHex(),
		PeerName:  "dispatch",
	}
	payload := fmt.Sprintf(sosLXMFLine, record.ID, record.Triage, record.Location,
		record.Sender, record.Details)
	if _, err := c.reg.lxmf.Send(lxmfSend{
		Ask:      ask,
		PeerHash: cfg.EmergencyLXMFDestinationHash,
		Text:     safeEcho(payload, maxLXMFTextBytes),
		OnResult: func(o lxmfOutcome) { c.reg.noticeLXMFOutcome(ask, o) },
	}); err != nil {
		logf("sos: could not queue beacon #%v to LXMF dispatch: %v", record.ID, err)
		return false
	}
	return true
}

// confirmSOS sends the sender a direct NOTICE that their beacon landed. A
// direct notice rides the hub link, so it reaches them even though the alarm
// went into rooms they may not be reading.
func (c *commandContext) confirmSOS(record SOSRecord, summary string) {
	if len(c.req.Msg.Src) != rrc.IdentityHashLen {
		return
	}
	text := fmt.Sprintf(sosDirectLine, record.ID, record.Triage, record.Location, summary)
	if err := c.conn().SendDirectNotice(c.req.Msg.Src, text); err != nil {
		logf("sos: could not confirm beacon #%v to the sender: %v", record.ID, err)
	}
}

// sosAlertSummary renders what the bot did about a beacon, in the confirmation:
// how many rooms were told, and whether the store-and-forward dispatch copy was
// queued.
func sosAlertSummary(alerted int, dispatched bool) string {
	var summary string
	switch alerted {
	case 0:
		summary = "no room to alert"
	case 1:
		summary = "Alerted room"
	default:
		summary = fmt.Sprintf("Alerted %v rooms", alerted)
	}
	if dispatched {
		return summary + ", " + sosLXMFQueued
	}
	return summary
}

// runSOSList lists the active beacons, newest last, with how long each has been
// standing.
func (c *commandContext) runSOSList() []string {
	store := c.reg.sos()
	if store == nil {
		return []string{"sos: the beacon registry is unavailable on this bot"}
	}
	active := store.active()
	if len(active) == 0 {
		return []string{sosNoBeaconsLine}
	}
	now := c.now()
	lines := []string{fmt.Sprintf("Active SOS beacons: %v", pluralCount(len(active), "beacon", "beacons"))}
	for _, record := range active {
		lines = append(lines, fmt.Sprintf("#%v [%v] %v (%v ago) by @%v: %v",
			record.ID, record.Triage, record.Location, formatAge(now.Sub(record.Timestamp)),
			safeEcho(record.Sender, maxEchoNickBytes), record.Details))
	}
	return lines
}

// runSOSClear stands one beacon down.
func (c *commandContext) runSOSClear(args string) []string {
	store := c.reg.sos()
	if store == nil {
		return []string{"sos: the beacon registry is unavailable on this bot"}
	}
	id, err := parseStoreID(args)
	if err != nil {
		return []string{"Usage: " + sosClearUsage}
	}
	record, err := store.clear(id, hexString(c.req.Msg.Src))
	switch {
	case err == nil:
		return []string{fmt.Sprintf(sosClearedLine, record.ID)}
	case isError(err, errSOSNotYours):
		return []string{fmt.Sprintf(sosNotYoursLine, id)}
	default:
		return []string{fmt.Sprintf(sosUnknownBeaconLine, id)}
	}
}

// parseStoreID parses the "#12", "12", or "sos12" form of a record number.
func parseStoreID(args string) (int, error) {
	text := strings.TrimSpace(args)
	text = strings.TrimPrefix(text, "#")
	text = strings.TrimPrefix(strings.ToLower(text), "sos")
	text = strings.TrimPrefix(text, "#")
	text = strings.TrimSpace(text)
	value := 0
	if text == "" {
		return 0, fmt.Errorf("empty id")
	}
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			return 0, fmt.Errorf("not a number: %q", args)
		}
		value = value*10 + int(text[i]-'0')
	}
	if value <= 0 {
		return 0, fmt.Errorf("id must be positive: %q", args)
	}
	return value, nil
}

// sos returns the bot's beacon registry, or nil when the registry was built
// without one.
func (r *registry) sos() *sosStore {
	if r.bot == nil {
		return nil
	}
	return r.bot.sos
}
