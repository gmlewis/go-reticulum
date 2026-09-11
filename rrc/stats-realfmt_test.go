// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestHandleStatsRealFormatStatsChunks pins that handleStats delivers the
// REAL StatsManager.FormatStats body as MTU-safe NOTICE chunks. The live
// fleet symptom (authorized /stats does nothing) was a single oversized
// EmitNotice envelope that RNS dropped.
func TestHandleStatsRealFormatStatsChunks(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	env.makeServerOp(peer)

	// Real FormatStats from a real StatsManager (not a mocked string).
	sm := NewStatsManager(
		func() float64 { return 1730000000.0 },
		func() float64 { return 2000.0 },
	)
	sm.SetStartTime()
	sm.Inc("pkts_in", 10)
	sm.Inc("bytes_in", 100)
	chat.hooks.FormatStats = func() string {
		return sm.FormatStats(StatsConfig{
			RateLimitMsgsPerMinute: 240,
			MaxRoomsPerSession:     32,
			MaxRoomNameBytes:       64,
			MaxNickBytes:           32,
			PingIntervalS:          30.0,
			PingTimeoutS:           60.0,
			AnnounceOnStart:        true,
			AnnouncePeriodS:        300.0,
		}, StatsSnapshot{
			SessionsTotal:      7,
			SessionsIdentified: 7,
			SessionsWelcomed:   7,
			RoomsTotal:         1,
			Memberships:        7,
			TrustedCount:       6,
		})
	}
	body := chat.hooks.FormatStats()
	if len(body) < 400 {
		t.Fatalf("real FormatStats is only %v bytes; fixture too small to exercise MTU", len(body))
	}

	outgoing := &OutgoingList{}
	if got := chat.HandleOperatorCommand(link, peer, nil, "/stats", outgoing); !got {
		t.Fatal("/stats should be recognized")
	}
	if len(outgoing.Queue) < 2 {
		t.Fatalf("queued %v packet(s) for a %v-byte FormatStats body; need multiple MTU-safe chunks",
			len(outgoing.Queue), len(body))
	}

	var rebuilt strings.Builder
	for i, item := range outgoing.Queue {
		if len(item.Payload) > rnsMDU {
			t.Errorf("payload[%d] = %v bytes > RNS MDU %v", i, len(item.Payload), rnsMDU)
		}
		sent := decodeOutgoing(t, &OutgoingList{Queue: []OutgoingItem{item}})
		if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
			t.Fatalf("payload[%d] = %+v, want one room-nil T_NOTICE", i, sent)
		}
		s, _ := sent[0].body.(string)
		if rebuilt.Len() > 0 {
			rebuilt.WriteString("\n")
		}
		rebuilt.WriteString(s)
	}
	// Chunks are complete lines; reassemble with the newlines FormatStats uses.
	if rebuilt.String() != body {
		t.Errorf("reassembled body:\n got %q\nwant %q", rebuilt.String(), body)
	}
	if !strings.Contains(rebuilt.String(), "rrcd") {
		t.Errorf("reassembled body missing rrcd header: %q", rebuilt.String())
	}
}
