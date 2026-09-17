// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the overdue-trip watchdog: the checkin command and the timer
// store and daemon behind it. A solo hiker, a scout patrol, or a survey team
// says where it is going and when it expects to be back; if that moment passes
// without a check-in, the bot raises the alarm on its own, without anybody
// having to remember to ask.
//
// This is the one part of the bot that acts with no request behind it, so it is
// deliberately conservative: a timer is capped at two days, one client can only
// ever have one timer, a timer fires exactly once and is removed when it does,
// and the alarm names the last known position and the note the traveller left.
// A watchdog that fires twice, or fires early, would be worse than none.

package bot

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Check-in wordings and bounds.
const (
	// checkinUsage is the usage line for setting a timer.
	checkinUsage = "checkin <loc> overdue <duration> <note>"
	// checkinOKUsage is the usage line for a check-in.
	checkinOKUsage = "checkin ok"
	// checkinListUsage is the usage line for a listing.
	checkinListUsage = "checkin list"
	// checkinFileName is the state file within the storage directory.
	checkinFileName = "checkin.json"
	// minCheckinDuration and maxCheckinDuration bound a timer. Ten minutes is
	// the shortest window in which a check-in is meaningful, and two days is
	// the longest an overdue alarm can still be acted on.
	minCheckinDuration = 10 * time.Minute
	maxCheckinDuration = 48 * time.Hour
	// maxCheckinTimers caps the store, so a room cannot grow it without limit.
	maxCheckinTimers = 100
	// maxCheckinNoteBytes bounds the note, which is repeated in the alarm.
	maxCheckinNoteBytes = 120
	// checkinWatchdogInterval is how often the daemon looks for expired timers.
	checkinWatchdogInterval = 30 * time.Second
	// checkinSetLine is the confirmation a successful registration gets.
	checkinSetLine = "Check-in timer set: overdue in %v (%v) for @%v at %v"
	// checkinClearedLine is the confirmation a check-in gets.
	checkinClearedLine = "Check-in received from @%v. Watchdog timer cleared."
	// checkinNoneLine is the answer to a check-in with no timer set.
	checkinNoneLine = "no check-in timer is set for @%v"
	// checkinOverdueLine is the alarm the watchdog broadcasts.
	checkinOverdueLine = "[OVERDUE ALERT] @%v is overdue! Last report %v ago at %v: %q"
	// checkinNoTimersLine is the answer to a listing with nothing set.
	checkinNoTimersLine = "No active check-in timers."
)

// CheckinTimer is one registered safety timer.
type CheckinTimer struct {
	// Sender is the human-readable traveller: nick and hash prefix.
	Sender string `json:"sender"`
	// SenderHash is the traveller's identity hash, hex, which is what makes the
	// timer theirs to clear.
	SenderHash string `json:"sender_hash"`
	// Location is the normalized Plus Code of the last known position.
	Location string `json:"location"`
	// LatLng is the last known position.
	LatLng LatLng `json:"coords"`
	// Deadline is when the traveller said they would be back.
	Deadline time.Time `json:"deadline"`
	// SetAt is when the timer was registered, which is the last report the
	// alarm quotes.
	SetAt time.Time `json:"set_at"`
	// Note is the sanitized plan the traveller left.
	Note string `json:"note"`
	// Hub is the hub the registration arrived on, hex; the alarm goes back
	// through it because that is the only hub that can reach the room.
	Hub string `json:"hub"`
	// Room is the room the registration arrived in, empty for a direct notice.
	Room string `json:"room"`
}

// checkinStore is the bounded, persistent timer store.
type checkinStore struct {
	mu      sync.Mutex
	path    string
	timers  []CheckinTimer
	loaded  bool
	loadErr error
	now     func() time.Time
}

// newCheckinStore builds an empty store backed by storageDir. An empty
// storageDir keeps everything in memory.
func newCheckinStore(storageDir string) *checkinStore {
	s := &checkinStore{now: time.Now}
	if strings.TrimSpace(storageDir) != "" {
		s.path = filepath.Join(storageDir, checkinFileName)
	}
	return s
}

// checkinState is the on-disk shape of the store.
type checkinState struct {
	Timers []CheckinTimer `json:"timers"`
}

// load reads the store from disk once.
func (s *checkinStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	var state checkinState
	found, err := readJSONState(s.path, &state)
	if err != nil {
		s.loadErr = err
		return
	}
	if found {
		s.timers = state.Timers
	}
}

// persist writes the store to disk. The caller holds the lock.
func (s *checkinStore) persist() error {
	return writeJSONState(s.path, checkinState{Timers: s.timers})
}

// set registers a timer, replacing any timer the same identity already has: one
// traveller has one plan at a time, and keeping an older timer would fire an
// alarm for a trip that has been superseded.
func (s *checkinStore) set(timer CheckinTimer) (CheckinTimer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if timer.SetAt.IsZero() {
		timer.SetAt = s.now()
	}
	s.timers = removeTimer(s.timers, timer.SenderHash)
	s.timers = append(s.timers, timer)
	if len(s.timers) > maxCheckinTimers {
		s.timers = s.timers[len(s.timers)-maxCheckinTimers:]
	}
	return timer, s.persist()
}

// cancel removes one identity's timer and reports it.
func (s *checkinStore) cancel(senderHash string) (CheckinTimer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	for i := range s.timers {
		if s.timers[i].SenderHash != senderHash {
			continue
		}
		timer := s.timers[i]
		s.timers = append(s.timers[:i], s.timers[i+1:]...)
		return timer, s.persist() == nil
	}
	return CheckinTimer{}, false
}

// list returns the registered timers, soonest deadline first.
func (s *checkinStore) list() []CheckinTimer {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	out := append([]CheckinTimer(nil), s.timers...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Deadline.Before(out[j-1].Deadline); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// due removes and returns every timer whose deadline has passed. Removal is
// what makes an alarm fire exactly once.
func (s *checkinStore) due(at time.Time) []CheckinTimer {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	var expired []CheckinTimer
	kept := s.timers[:0]
	for _, timer := range s.timers {
		if !at.Before(timer.Deadline) {
			expired = append(expired, timer)
			continue
		}
		kept = append(kept, timer)
	}
	s.timers = kept
	if len(expired) > 0 {
		if err := s.persist(); err != nil {
			logf("checkin: could not persist the store after %v expired timer(s): %v", len(expired), err)
		}
	}
	return expired
}

// removeTimer drops the timer belonging to one identity.
func removeTimer(timers []CheckinTimer, senderHash string) []CheckinTimer {
	kept := timers[:0]
	for _, timer := range timers {
		if timer.SenderHash == senderHash {
			continue
		}
		kept = append(kept, timer)
	}
	return kept
}

// checkinWatchdog is the daemon that raises overdue alarms. Its clock and its
// delivery function are injected, so the whole loop is tested without waiting.
type checkinWatchdog struct {
	store    *checkinStore
	deliver  func(CheckinTimer)
	interval time.Duration
	now      func() time.Time
}

// newCheckinWatchdog builds an inert watchdog over a store.
func newCheckinWatchdog(store *checkinStore) *checkinWatchdog {
	return &checkinWatchdog{store: store, interval: checkinWatchdogInterval, now: time.Now}
}

// check raises an alarm for every expired timer, exactly once.
func (w *checkinWatchdog) check() {
	if w == nil || w.store == nil {
		return
	}
	for _, timer := range w.store.due(w.now()) {
		if w.deliver != nil {
			w.deliver(timer)
		}
	}
}

// start runs the watchdog until stop closes.
func (w *checkinWatchdog) start(wg *sync.WaitGroup, stop <-chan struct{}) {
	if w == nil || w.store == nil || wg == nil || stop == nil {
		return
	}
	interval := w.interval
	if interval <= 0 {
		interval = checkinWatchdogInterval
	}
	wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				w.check()
			}
		}
	})
}

// runCheckin dispatches the checkin subcommands.
func (c *commandContext) runCheckin() []string {
	arg := strings.TrimSpace(c.Args)
	switch strings.ToLower(arg) {
	case "":
		return []string{"Usage: " + checkinUsage, checkinOKUsage + " | " + checkinListUsage}
	case "ok":
		return c.runCheckinOK()
	case "list":
		return c.runCheckinList()
	}
	return c.runCheckinSet(arg)
}

// runCheckinSet registers a safety timer.
func (c *commandContext) runCheckinSet(args string) []string {
	location, durationText, note, ok := splitCheckinSet(args)
	if !ok {
		return []string{"Usage: " + checkinUsage}
	}
	point, err := ParseLocation(location)
	if err != nil {
		return []string{"checkin: no location found — " + locationNotationHelp}
	}
	duration, err := time.ParseDuration(durationText)
	if err != nil || duration <= 0 {
		return []string{fmt.Sprintf("checkin: %q is not a duration like 4h, 90m, or 1h30m", durationText)}
	}
	// The window is capped rather than refused: a traveller who types a silly
	// number still gets a watchdog, and the confirmation states the window that
	// was actually set.
	if duration < minCheckinDuration {
		duration = minCheckinDuration
	}
	if duration > maxCheckinDuration {
		duration = maxCheckinDuration
	}
	store := c.reg.checkins()
	if store == nil {
		return []string{"checkin: the watchdog is unavailable on this bot"}
	}
	now := c.now()
	code, err := EncodeOLC(point.Lat, point.Lng, olcCodeLength)
	if err != nil {
		return []string{"checkin: could not build a Plus Code: " + err.Error()}
	}
	nick := safeEcho(c.peerName(c.req.Msg.Src), maxEchoNickBytes)
	timer, err := store.set(CheckinTimer{
		Sender:     nick,
		SenderHash: hexString(c.req.Msg.Src),
		Location:   code,
		LatLng:     point,
		Deadline:   now.Add(duration),
		SetAt:      now,
		Note:       safeEcho(note, maxCheckinNoteBytes),
		Hub:        c.conn().HubAddressHex(),
		Room:       c.req.Room,
	})
	if err != nil {
		logf("checkin: could not persist the timer for %v: %v", nick, err)
	}
	return []string{fmt.Sprintf(checkinSetLine, formatWindow(duration),
		FormatDeadline(timer.Deadline, now), nick, timer.Location)}
}

// splitCheckinSet splits a set request into its location, its duration, and its
// note. The word "overdue" separates the location from the window, and the
// location is everything before it, because a location may contain spaces.
func splitCheckinSet(args string) (string, string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(args))
	overdueIndex := -1
	for i, field := range fields {
		if strings.EqualFold(field, "overdue") {
			overdueIndex = i
			break
		}
	}
	// The note is required: it is what the alarm quotes when the window runs
	// out, and an alarm with no plan in it tells a searcher nothing.
	if overdueIndex < 1 || overdueIndex >= len(fields)-2 {
		return "", "", "", false
	}
	return strings.Join(fields[:overdueIndex], " "),
		fields[overdueIndex+1],
		strings.Join(fields[overdueIndex+2:], " "), true
}

// runCheckinOK clears the caller's timer.
func (c *commandContext) runCheckinOK() []string {
	store := c.reg.checkins()
	if store == nil {
		return []string{"checkin: the watchdog is unavailable on this bot"}
	}
	nick := safeEcho(c.peerName(c.req.Msg.Src), maxEchoNickBytes)
	if _, ok := store.cancel(hexString(c.req.Msg.Src)); !ok {
		return []string{fmt.Sprintf(checkinNoneLine, nick)}
	}
	return []string{fmt.Sprintf(checkinClearedLine, nick)}
}

// runCheckinList lists the registered timers.
func (c *commandContext) runCheckinList() []string {
	store := c.reg.checkins()
	if store == nil {
		return []string{"checkin: the watchdog is unavailable on this bot"}
	}
	timers := store.list()
	if len(timers) == 0 {
		return []string{checkinNoTimersLine}
	}
	now := c.now()
	lines := []string{fmt.Sprintf("Active check-in timers: %v", pluralCount(len(timers), "timer", "timers"))}
	for _, timer := range timers {
		line := fmt.Sprintf("@%v at %v: %v (%v)",
			safeEcho(timer.Sender, maxEchoNickBytes), timer.Location,
			formatHorizon(timer.Deadline.Sub(now)), FormatDeadline(timer.Deadline, now))
		if timer.Note != "" {
			line += ": " + timer.Note
		}
		lines = append(lines, line)
	}
	return lines
}

// checkinOverdueText renders the alarm for one expired timer.
func checkinOverdueText(timer CheckinTimer, now time.Time) string {
	return fmt.Sprintf(checkinOverdueLine, safeEcho(timer.Sender, maxEchoNickBytes),
		formatAge(now.Sub(timer.SetAt)), timer.Location, timer.Note)
}

// deliverOverdue broadcasts one overdue alarm through the hub the timer was
// registered on. The registered room is tried first; if the bot is no longer
// joined to it, every joined room gets the alarm, because a warning that
// reaches the wrong room is still better than one that reaches nobody.
func (b *bot) deliverOverdue(timer CheckinTimer) {
	text := checkinOverdueText(timer, time.Now())
	for _, session := range b.sessions {
		if session.conn.HubAddressHex() != timer.Hub {
			continue
		}
		if timer.Room != "" && session.isJoined(timer.Room) {
			if _, err := session.conn.SendNotice(timer.Room, text); err == nil {
				b.logf("checkin: overdue alarm for %v sent to %v", timer.Sender, timer.Room)
				return
			}
		}
		rooms := session.conn.JoinedRoomList()
		if len(rooms) == 0 {
			b.logf("checkin: %v is overdue but this session has joined no room to say so in", timer.Sender)
			return
		}
		for _, room := range rooms {
			if _, err := session.conn.SendNotice(room, text); err != nil {
				b.logf("checkin: could not alert %q: %v", room, err)
			}
		}
		b.logf("checkin: overdue alarm for %v sent to %v room(s)", timer.Sender, len(rooms))
		return
	}
	b.logf("checkin: %v is overdue but hub %v is no longer connected", timer.Sender, timer.Hub)
}

// FormatDeadline renders a deadline as a UTC wall-clock time, with the date
// when it falls on a different day from now, so "18:30 UTC" never means the
// wrong day.
func FormatDeadline(deadline, now time.Time) string {
	deadline = deadline.UTC()
	now = now.UTC()
	if deadline.Year() == now.Year() && deadline.YearDay() == now.YearDay() {
		return deadline.Format("15:04 UTC")
	}
	return deadline.Format("2006-01-02 15:04 UTC")
}

// formatWindow renders a duration as the compact window a person would say:
// "45m", "4h", or "1h30m".
func formatWindow(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%vh", int(d.Hours()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%vm", int(d.Minutes()))
	}
	return fmt.Sprintf("%vh%vm", int(d.Hours()), int(d.Minutes())%60)
}

// checkins returns the bot's timer store, or nil when the bot was built without
// one.
func (r *registry) checkins() *checkinStore {
	if r.bot == nil {
		return nil
	}
	return r.bot.checkins
}
