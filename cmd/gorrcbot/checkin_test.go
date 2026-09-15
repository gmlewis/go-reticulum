// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
	"time"
)

// TestCheckinStoreSetReplacesTheSameSender asserts one traveller has one plan at
// a time: a second timer replaces the first rather than arming two alarms.
func TestCheckinStoreSetReplacesTheSameSender(t *testing.T) {
	t.Parallel()

	store := newCheckinStore(tempDir(t))
	first := CheckinTimer{SenderHash: "alice", Location: "CM87", Deadline: time.Unix(1000, 0)}
	if _, err := store.set(first); err != nil {
		t.Fatalf("set: %v", err)
	}
	second := CheckinTimer{SenderHash: "alice", Location: "CM88", Deadline: time.Unix(2000, 0)}
	if _, err := store.set(second); err != nil {
		t.Fatalf("second set: %v", err)
	}
	if _, err := store.set(CheckinTimer{SenderHash: "bob", Location: "FN31", Deadline: time.Unix(500, 0)}); err != nil {
		t.Fatalf("third set: %v", err)
	}

	timers := store.list()
	if len(timers) != 2 {
		t.Fatalf("store holds %v timers, want 2", len(timers))
	}
	// The list is ordered by deadline, so bob's earlier deadline comes first.
	if timers[0].SenderHash != "bob" || timers[1].SenderHash != "alice" {
		t.Errorf("timers = %+v, want bob first by deadline", timers)
	}
	if timers[1].Location != "CM88" {
		t.Errorf("alice's timer location = %q, want the replacement CM88", timers[1].Location)
	}
}

// TestCheckinStoreCancelAndDue asserts cancelling removes a timer and that an
// expired timer is handed over exactly once.
func TestCheckinStoreCancelAndDue(t *testing.T) {
	t.Parallel()

	store := newCheckinStore(tempDir(t))
	deadline := time.Date(2026, 3, 15, 18, 30, 0, 0, time.UTC)
	if _, err := store.set(CheckinTimer{SenderHash: "alice", Deadline: deadline}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok := store.cancel("bob"); ok {
		t.Error("cancelling a timer that does not exist reported success")
	}
	if _, ok := store.cancel("alice"); !ok {
		t.Error("cancelling alice's timer failed")
	}
	if len(store.list()) != 0 {
		t.Error("the cancelled timer is still registered")
	}

	if _, err := store.set(CheckinTimer{SenderHash: "alice", Deadline: deadline}); err != nil {
		t.Fatalf("second set: %v", err)
	}
	if due := store.due(deadline.Add(-time.Minute)); len(due) != 0 {
		t.Errorf("%v timers expired before their deadline", len(due))
	}
	due := store.due(deadline)
	if len(due) != 1 || due[0].SenderHash != "alice" {
		t.Fatalf("due at the deadline = %+v, want alice's timer", due)
	}
	if again := store.due(deadline.Add(time.Hour)); len(again) != 0 {
		t.Errorf("the timer fired %v times, want once", len(again)+1)
	}
}

// TestCheckinStoreRoundTripsThroughDisk asserts a timer survives a restart.
func TestCheckinStoreRoundTripsThroughDisk(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	deadline := time.Date(2026, 3, 15, 18, 30, 0, 0, time.UTC)
	setAt := deadline.Add(-4 * time.Hour)
	first := newCheckinStore(dir)
	if _, err := first.set(CheckinTimer{
		Sender: "Alice", SenderHash: "alice", Location: "CM87", Note: "Hiking to Eagle Peak",
		Deadline: deadline, SetAt: setAt, Hub: "hubhex", Room: "general",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	second := newCheckinStore(dir)
	timers := second.list()
	if len(timers) != 1 {
		t.Fatalf("reloaded store holds %v timers, want 1", len(timers))
	}
	got := timers[0]
	if got.Sender != "Alice" || got.Note != "Hiking to Eagle Peak" || got.Room != "general" {
		t.Errorf("reloaded timer = %+v, want the registered one", got)
	}
	if !got.Deadline.Equal(deadline) || !got.SetAt.Equal(setAt) {
		t.Errorf("reloaded times = %v/%v, want %v/%v", got.SetAt, got.Deadline, setAt, deadline)
	}
}

// TestCheckinWatchdogRaisesExactlyOneAlarm asserts the daemon hands each expired
// timer to its delivery function once, and never for a timer that is not due.
func TestCheckinWatchdogRaisesExactlyOneAlarm(t *testing.T) {
	t.Parallel()

	store := newCheckinStore(tempDir(t))
	deadline := time.Date(2026, 3, 15, 18, 30, 0, 0, time.UTC)
	if _, err := store.set(CheckinTimer{SenderHash: "alice", Deadline: deadline}); err != nil {
		t.Fatalf("set: %v", err)
	}
	var delivered []CheckinTimer
	watchdog := newCheckinWatchdog(store)
	watchdog.now = func() time.Time { return deadline.Add(-time.Minute) }
	watchdog.deliver = func(timer CheckinTimer) { delivered = append(delivered, timer) }

	watchdog.check()
	if len(delivered) != 0 {
		t.Fatalf("%v alarms fired before the deadline", len(delivered))
	}
	watchdog.now = func() time.Time { return deadline }
	watchdog.check()
	if len(delivered) != 1 {
		t.Fatalf("%v alarms fired at the deadline, want 1", len(delivered))
	}
	watchdog.check()
	if len(delivered) != 1 {
		t.Errorf("%v alarms fired after a second tick, want still 1", len(delivered))
	}
}

// TestCheckinCommandSetsATimer asserts the confirmation names the window, the
// UTC deadline, the traveller, and the position.
func TestCheckinCommandSetsATimer(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)

	lines := runLinesAt(t, reg, session, "checkin 849VCWC8+R9 overdue 4h Hiking to Eagle Peak", now)
	if len(lines) != 1 {
		t.Fatalf("checkin returned %v lines, want 1: %v", len(lines), lines)
	}
	want := "Check-in timer set: overdue in 4h (18:30 UTC) for @Alice at 849VCWC8+R9"
	if lines[0] != want {
		t.Errorf("checkin =\n  %v\nwant\n  %v", lines[0], want)
	}
}

// TestCheckinCommandCapsTheWindow asserts a window outside the safe range is
// clamped, and the confirmation states the window that was actually set.
func TestCheckinCommandCapsTheWindow(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)

	short := runLinesAt(t, reg, session, "checkin 849VCWC8+R9 overdue 2m quick look", now)
	if len(short) != 1 || !strings.Contains(short[0], "overdue in 10m") {
		t.Errorf("a two-minute window = %v, want it capped up to 10m", short)
	}
	long := runLinesAt(t, reg, session, "checkin 849VCWC8+R9 overdue 100h long trip", now)
	if len(long) != 1 || !strings.Contains(long[0], "overdue in 48h") {
		t.Errorf("a hundred-hour window = %v, want it capped down to 48h", long)
	}
}

// TestCheckinCommandOKAndList asserts a check-in clears the timer, that a
// check-in without a timer says so, and that the listing shows what is set.
func TestCheckinCommandOKAndList(t *testing.T) {
	t.Parallel()

	reg, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)

	if lines := runLinesAt(t, reg, session, "checkin ok", now); len(lines) != 1 ||
		lines[0] != "no check-in timer is set for @Alice" {
		t.Fatalf("checkin ok with no timer = %v", lines)
	}

	runLinesAt(t, reg, session, "checkin 849VCWC8+R9 overdue 4h Hiking to Eagle Peak", now)
	listed := runLinesAt(t, reg, session, "checkin list", now.Add(time.Hour))
	if len(listed) != 2 {
		t.Fatalf("checkin list = %v, want a header and one row", listed)
	}
	if !strings.Contains(listed[0], "Active check-in timers: 1 timer") {
		t.Errorf("checkin list header = %q", listed[0])
	}
	want := "@Alice at 849VCWC8+R9: expires in 3h (18:30 UTC): Hiking to Eagle Peak"
	if listed[1] != want {
		t.Errorf("checkin list row =\n  %v\nwant\n  %v", listed[1], want)
	}

	cleared := runLinesAt(t, reg, session, "checkin ok", now.Add(time.Hour))
	if len(cleared) != 1 || cleared[0] != "Check-in received from @Alice. Watchdog timer cleared." {
		t.Errorf("checkin ok = %v, want the cleared line", cleared)
	}
	if empty := runLinesAt(t, reg, session, "checkin list", now.Add(time.Hour)); len(empty) != 1 ||
		empty[0] != checkinNoTimersLine {
		t.Errorf("checkin list after the check-in = %v, want %q", empty, checkinNoTimersLine)
	}
}

// TestCheckinWatchdogBroadcastsAnOverdueAlarm asserts an expired timer produces
// the alarm in the room it was registered in, exactly once.
func TestCheckinWatchdogBroadcastsAnOverdueAlarm(t *testing.T) {
	t.Parallel()

	_, session, fake, _ := storedFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	now := time.Now()
	if _, err := session.bot.checkins.set(CheckinTimer{
		Sender: "Alice", SenderHash: hexString(peerHashFor(0x11)),
		Location: "849VCWC8+R9", Note: "Hiking to Eagle Peak",
		Deadline: now.Add(-time.Minute), SetAt: now.Add(-4*time.Hour - time.Minute),
		Hub: session.cfg.Destination, Room: "general",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	watch := session.bot.checkinWatch
	watch.check()
	watch.check()

	var alarms []string
	for _, text := range noticeTexts(fake) {
		if strings.Contains(text, "[OVERDUE ALERT]") {
			alarms = append(alarms, text)
		}
	}
	if len(alarms) != 1 {
		t.Fatalf("alarms = %v, want exactly one", alarms)
	}
	want := `[OVERDUE ALERT] @Alice is overdue! Last report 4h ago at 849VCWC8+R9: "Hiking to Eagle Peak"`
	if alarms[0] != want {
		t.Errorf("alarm =\n  %v\nwant\n  %v", alarms[0], want)
	}
}

// TestCheckinWatchdogFallsBackToJoinedRooms asserts an alarm whose registered
// room is gone still reaches the rooms the bot is in: a warning in the wrong
// room beats no warning at all.
func TestCheckinWatchdogFallsBackToJoinedRooms(t *testing.T) {
	t.Parallel()

	_, session, fake, _ := storedFixture(t, nil, "general", "emergency")
	fake.setKnownPeer(hexString(peerHashFor(0x11)), "Alice")
	now := time.Now()
	if _, err := session.bot.checkins.set(CheckinTimer{
		Sender: "Alice", SenderHash: "alice", Location: "849VCWC8+R9",
		Deadline: now.Add(-time.Minute), SetAt: now.Add(-time.Hour),
		Hub: session.cfg.Destination, Room: "gone",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	session.bot.checkinWatch.check()

	var rooms []string
	fake.mu.Lock()
	for _, notice := range fake.notices {
		if strings.Contains(notice.Text, "[OVERDUE ALERT]") {
			rooms = append(rooms, notice.Room)
		}
	}
	fake.mu.Unlock()
	if len(rooms) != 2 {
		t.Fatalf("the alarm reached %v rooms (%v), want both joined rooms", len(rooms), rooms)
	}
}

// TestCheckinCommandRejectsBadRequests asserts a malformed check-in is answered
// with its usage rather than a wrong deadline.
func TestCheckinCommandRejectsBadRequests(t *testing.T) {
	t.Parallel()

	reg, session, _, _ := storedFixture(t, nil)
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
	for _, line := range []string{
		"checkin",
		"checkin 849VCWC8+R9",
		"checkin 849VCWC8+R9 overdue",
		"checkin 849VCWC8+R9 overdue 4h",
	} {
		lines := runLinesAt(t, reg, session, line, now)
		if len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+checkinUsage) {
			t.Errorf("%q = %v, want the usage line", line, lines)
		}
	}
	if lines := runLinesAt(t, reg, session, "checkin 849VCWC8+R9 overdue soon a while", now); len(lines) == 0 ||
		!strings.Contains(lines[0], "is not a duration") {
		t.Errorf("checkin with an unparseable window = %v, want the duration line", lines)
	}
}

// TestFormatDeadlineNamesTheDayWhenItDiffers asserts a deadline on another day
// carries its date, so "18:30 UTC" can never mean the wrong day.
func TestFormatDeadlineNamesTheDayWhenItDiffers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
	sameDay := time.Date(2026, 3, 15, 18, 30, 0, 0, time.UTC)
	if got, want := FormatDeadline(sameDay, now), "18:30 UTC"; got != want {
		t.Errorf("FormatDeadline(same day) = %q, want %q", got, want)
	}
	nextDay := time.Date(2026, 3, 16, 6, 30, 0, 0, time.UTC)
	if got, want := FormatDeadline(nextDay, now), "2026-03-16 06:30 UTC"; got != want {
		t.Errorf("FormatDeadline(next day) = %q, want %q", got, want)
	}
}
