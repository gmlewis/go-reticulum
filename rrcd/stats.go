// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// StatsManager tracks hub statistics, mirroring Python's StatsManager with
// the exact 21 counter names.
type StatsManager struct {
	mu               sync.Mutex
	counters         map[string]int
	startedWallTime  *float64
	startedMonotonic *float64
	// wall and mono return the wall-clock and monotonic clock readings in
	// seconds (injectable in tests).
	wall func() float64
	mono func() float64
}

// NewStatsManager creates a stats manager; wall and mono supply the clock
// readings (defaulting to time.Now and a monotonic base).
func NewStatsManager(wall, mono func() float64) *StatsManager {
	s := &StatsManager{
		counters: initialCounters(),
	}
	if wall == nil {
		wall = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	if mono == nil {
		mono = func() float64 { return float64(time.Since(statsProcessStart).Nanoseconds()) / 1e9 }
	}
	s.wall = wall
	s.mono = mono
	return s
}

// initialCounters returns the counters initialized to zero, with the exact
// names stats.py uses.
func initialCounters() map[string]int {
	return map[string]int{
		"bytes_in":                0,
		"bytes_out":               0,
		"pkts_in":                 0,
		"pkts_bad":                0,
		"rate_limited":            0,
		"errors_sent":             0,
		"joins":                   0,
		"parts":                   0,
		"msgs_forwarded":          0,
		"notices_forwarded":       0,
		"actions_forwarded":       0,
		"pings_in":                0,
		"pings_out":               0,
		"pongs_in":                0,
		"pongs_out":               0,
		"announces":               0,
		"resources_sent":          0,
		"resources_received":      0,
		"resources_rejected":      0,
		"resource_bytes_sent":     0,
		"resource_bytes_received": 0,
	}
}

// StartedWallTime returns the wall-clock start time, or nil before
// set_start_time.
func (s *StatsManager) StartedWallTime() *float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedWallTime
}

// SetStartTime sets the start time for uptime calculations, mirroring
// set_start_time.
func (s *StatsManager) SetStartTime() {
	s.mu.Lock()
	defer s.mu.Unlock()
	wall := s.wallTime()
	s.startedWallTime = &wall
	mono := s.monotonic()
	s.startedMonotonic = &mono
}

// wallTime returns the wall clock reading in seconds.
func (s *StatsManager) wallTime() float64 {
	if s.wall != nil {
		return s.wall()
	}
	return float64(time.Now().UnixNano()) / 1e9
}

// monotonic returns the monotonic clock reading in seconds.
func (s *StatsManager) monotonic() float64 {
	if s.mono != nil {
		return s.mono()
	}
	return float64(time.Since(statsProcessStart).Nanoseconds()) / 1e9
}

// statsProcessStart is the process start used for default monotonic reads.
var statsProcessStart = time.Now()

// Inc increments a counter by the given delta; unknown keys are created.
func (s *StatsManager) Inc(key string, delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counters[key] = s.counters[key] + delta
}

// Counter returns the current value of one counter (for tests).
func (s *StatsManager) Counter(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counters[key]
}

// StatsSnapshot holds the cross-manager values format_stats renders.
type StatsSnapshot struct {
	SessionsTotal      int
	SessionsWelcomed   int
	SessionsIdentified int
	RoomsTotal         int
	Memberships        int
	TopRooms           []RoomCount
	TrustedCount       int
	BannedCount        int
}

// StatsConfig holds the config values format_stats renders.
type StatsConfig struct {
	RateLimitMsgsPerMinute int
	MaxRoomsPerSession     int
	MaxRoomNameBytes       int
	MaxNickBytes           int
	PingIntervalS          float64
	PingTimeoutS           float64
	AnnounceOnStart        bool
	AnnouncePeriodS        float64
}

// FormatStats renders the statistics as one human-readable string, joining
// the lines with "" (no separators), mirroring format_stats.
func (s *StatsManager) FormatStats(cfg StatsConfig, snap StatsSnapshot) string {
	s.mu.Lock()
	c := make(map[string]int, len(s.counters))
	maps.Copy(c, s.counters)
	uptimeS := 0.0
	if s.startedMonotonic != nil {
		uptimeS = s.mono() - *s.startedMonotonic
	}
	s.mu.Unlock()

	lines := []string{
		"rrcd " + rns.VERSION + " stats",
		"uptime_s=" + fmtFloatDot1(uptimeS),
		"clients_total=" + itoa(snap.SessionsTotal) +
			" clients_identified=" + itoa(snap.SessionsIdentified) +
			" clients_welcomed=" + itoa(snap.SessionsWelcomed),
		"rooms=" + itoa(snap.RoomsTotal) + " memberships=" + itoa(snap.Memberships),
	}
	if len(snap.TopRooms) > 0 {
		parts := make([]string, len(snap.TopRooms))
		for i, rc := range snap.TopRooms {
			parts[i] = rc.Room + ":" + itoa(rc.Count)
		}
		lines = append(lines, "top_rooms="+strings.Join(parts, ", "))
	}
	lines = append(lines,
		"trust: trusted="+itoa(snap.TrustedCount)+" banned="+itoa(snap.BannedCount))
	lines = append(lines, "limits: rate_limit_msgs_per_minute="+
		itoa(cfg.RateLimitMsgsPerMinute)+
		" max_rooms_per_session="+itoa(cfg.MaxRoomsPerSession)+
		" max_room_name_bytes="+itoa(cfg.MaxRoomNameBytes)+
		" max_nick_bytes="+itoa(cfg.MaxNickBytes))
	lines = append(lines, "features: ping_interval_s="+toml.FormatFloat(cfg.PingIntervalS)+
		" ping_timeout_s="+toml.FormatFloat(cfg.PingTimeoutS)+
		" announce_on_start="+pythonBool(cfg.AnnounceOnStart)+
		" announce_period_s="+toml.FormatFloat(cfg.AnnouncePeriodS))
	lines = append(lines, "io: pkts_in="+itoa(c["pkts_in"])+
		" pkts_bad="+itoa(c["pkts_bad"])+
		" bytes_in="+itoa(c["bytes_in"])+
		" bytes_out="+itoa(c["bytes_out"]))
	lines = append(lines, "events: joins="+itoa(c["joins"])+
		" parts="+itoa(c["parts"])+
		" msgs_forwarded="+itoa(c["msgs_forwarded"])+
		" notices_forwarded="+itoa(c["notices_forwarded"])+
		" actions_forwarded="+itoa(c["actions_forwarded"])+
		" errors_sent="+itoa(c["errors_sent"])+
		" rate_limited="+itoa(c["rate_limited"]))
	lines = append(lines, "pings: in="+itoa(c["pings_in"])+
		" out="+itoa(c["pings_out"])+
		" pongs: in="+itoa(c["pongs_in"])+
		" out="+itoa(c["pongs_out"]))
	lines = append(lines, "resources: sent="+itoa(c["resources_sent"])+
		" received="+itoa(c["resources_received"])+
		" rejected="+itoa(c["resources_rejected"])+
		" bytes_sent="+itoa(c["resource_bytes_sent"])+
		" bytes_received="+itoa(c["resource_bytes_received"]))

	return strings.Join(lines, "")
}

// pythonBool renders a bool the way Python's str() does.
func pythonBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// fmtFloatDot1 renders a float with one decimal digit ({:.1f}).
func fmtFloatDot1(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// itoa renders an int in decimal.
func itoa(n int) string {
	return strconv.Itoa(n)
}

// StatsIncForTest increments a counter (test helper).
func (s *StatsManager) StatsIncForTest(key string, delta int) { s.Inc(key, delta) }
