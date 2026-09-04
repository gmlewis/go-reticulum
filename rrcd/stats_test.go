// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"testing"
)

// G5.5: the golden full-string output for a fixed counter snapshot,
// captured from Python format_stats (with the "".join no-separator quirk
// and the rrcd Version self-reference).
func TestFormatStatsGolden(t *testing.T) {
	t.Parallel()
	wall := 1730000000.0
	mono := 1000.0
	s := NewStatsManager(
		func() float64 { return wall },
		func() float64 { return mono },
	)
	s.SetStartTime()
	// Rewind the monotonic start by 100s: uptime_s=100.0.
	s.mu.Lock()
	mono2 := mono - 100.0
	s.startedMonotonic = &mono2
	s.mu.Unlock()

	s.Inc("pkts_in", 5)
	s.Inc("pkts_bad", 1)
	s.Inc("bytes_in", 120)
	s.Inc("bytes_out", 340)
	s.Inc("joins", 2)
	s.Inc("parts", 1)
	s.Inc("msgs_forwarded", 7)
	s.Inc("notices_forwarded", 3)
	s.Inc("actions_forwarded", 1)
	s.Inc("errors_sent", 1)
	s.Inc("rate_limited", 1)
	s.Inc("pings_in", 4)
	s.Inc("pings_out", 4)
	s.Inc("pongs_in", 3)
	s.Inc("pongs_out", 3)
	s.Inc("announces", 2)
	s.Inc("resources_sent", 1)
	s.Inc("resources_received", 1)
	s.Inc("resources_rejected", 0)
	s.Inc("resource_bytes_sent", 100)
	s.Inc("resource_bytes_received", 90)

	cfg := StatsConfig{
		RateLimitMsgsPerMinute: 240,
		MaxRoomsPerSession:     32,
		MaxRoomNameBytes:       64,
		MaxNickBytes:           32,
		PingIntervalS:          0.0,
		PingTimeoutS:           0.0,
		AnnounceOnStart:        true,
		AnnouncePeriodS:        0.0,
	}
	snap := StatsSnapshot{
		SessionsTotal:      2,
		SessionsWelcomed:   2,
		SessionsIdentified: 1,
		RoomsTotal:         1,
		Memberships:        2,
		TopRooms: []RoomCount{
			{Room: "general", Count: 2},
		},
		TrustedCount: 1,
		BannedCount:  0,
	}
	got := s.FormatStats(cfg, snap)
	want := "rrcd " + HubVersion + " stats" +
		"uptime_s=100.0" +
		"clients_total=2 clients_identified=1 clients_welcomed=2" +
		"rooms=1 memberships=2" +
		"top_rooms=general:2" +
		"trust: trusted=1 banned=0" +
		"limits: rate_limit_msgs_per_minute=240 max_rooms_per_session=32 max_room_name_bytes=64 max_nick_bytes=32" +
		"features: ping_interval_s=0.0 ping_timeout_s=0.0 announce_on_start=True announce_period_s=0.0" +
		"io: pkts_in=5 pkts_bad=1 bytes_in=120 bytes_out=340" +
		"events: joins=2 parts=1 msgs_forwarded=7 notices_forwarded=3 actions_forwarded=1 errors_sent=1 rate_limited=1" +
		"pings: in=4 out=4 pongs: in=3 out=3" +
		"resources: sent=1 received=1 rejected=0 bytes_sent=100 bytes_received=90"
	if got != want {
		t.Errorf("FormatStats:\n got %q\nwant %q", got, want)
	}
}

// Empty counters render as zeros.
func TestFormatStatsEmptyCounters(t *testing.T) {
	t.Parallel()
	mono := 1000.0
	s := NewStatsManager(
		func() float64 { return 1730000000.0 },
		func() float64 { return mono },
	)
	s.SetStartTime()
	cfg := StatsConfig{
		RateLimitMsgsPerMinute: 240,
		MaxRoomsPerSession:     32,
		MaxRoomNameBytes:       64,
		MaxNickBytes:           32,
		PingIntervalS:          0.0,
		PingTimeoutS:           0.0,
		AnnounceOnStart:        true,
		AnnouncePeriodS:        0.0,
	}
	snap := StatsSnapshot{}
	got := s.FormatStats(cfg, snap)
	want := "rrcd " + HubVersion + " stats" +
		"uptime_s=0.0" +
		"clients_total=0 clients_identified=0 clients_welcomed=0" +
		"rooms=0 memberships=0" +
		"trust: trusted=0 banned=0" +
		"limits: rate_limit_msgs_per_minute=240 max_rooms_per_session=32 max_room_name_bytes=64 max_nick_bytes=32" +
		"features: ping_interval_s=0.0 ping_timeout_s=0.0 announce_on_start=True announce_period_s=0.0" +
		"io: pkts_in=0 pkts_bad=0 bytes_in=0 bytes_out=0" +
		"events: joins=0 parts=0 msgs_forwarded=0 notices_forwarded=0 actions_forwarded=0 errors_sent=0 rate_limited=0" +
		"pings: in=0 out=0 pongs: in=0 out=0" +
		"resources: sent=0 received=0 rejected=0 bytes_sent=0 bytes_received=0"
	if got != want {
		t.Errorf("empty FormatStats:\n got %q\nwant %q", got, want)
	}
}

// Unknown counters are created by Inc (mirroring the dict get semantics).
func TestStatsIncCreatesUnknownKey(t *testing.T) {
	t.Parallel()
	s := NewStatsManager(nil, nil)
	s.Inc("bogus_counter", 5)
	if got := s.Counter("bogus_counter"); got != 5 {
		t.Errorf("bogus_counter = %v, want 5", got)
	}
}

// SetStartTime records both clocks.
func TestStatsSetStartTime(t *testing.T) {
	t.Parallel()
	wall := 1730000000.0
	mono := 1000.0
	s := NewStatsManager(
		func() float64 { return wall },
		func() float64 { return mono },
	)
	s.SetStartTime()
	if s.startedWallTime == nil || *s.startedWallTime != wall {
		t.Errorf("startedWallTime = %v", s.startedWallTime)
	}
	if s.startedMonotonic == nil || *s.startedMonotonic != mono {
		t.Errorf("startedMonotonic = %v", s.startedMonotonic)
	}
}
