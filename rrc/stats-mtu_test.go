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

// TestHandleStatsChunksLongBody pins that an authorized /stats reply is
// delivered as MTU-safe NOTICE chunks. EmitNotice of the full ~624-byte
// FormatStats body produces one oversized packet that RNS silently drops
// (live symptom: authorized /stats does nothing). handleStats must go
// through QueueNoticeChunks so every queued payload fits the RNS MDU.
func TestHandleStatsChunksLongBody(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	env.makeServerOp(peer)

	// Realistic FormatStats body: "".join of the ~12 stat lines (no
	// separators), matching live hub output (~620+ bytes).
	var sb strings.Builder
	sb.WriteString("rrcd 0.3.2 stats")
	sb.WriteString("uptime_s=1234.5")
	sb.WriteString("clients_total=7 clients_identified=7 clients_welcomed=7")
	sb.WriteString("rooms=1 memberships=7")
	sb.WriteString("top_rooms=general:7")
	sb.WriteString("trust: trusted=6 banned=0")
	sb.WriteString("limits: rate_limit_msgs_per_minute=240 max_rooms_per_session=32 max_room_name_bytes=64 max_nick_bytes=32")
	sb.WriteString("features: ping_interval_s=30.0 ping_timeout_s=60.0 announce_on_start=True announce_period_s=21600.0")
	sb.WriteString("io: pkts_in=100 pkts_bad=0 bytes_in=5000 bytes_out=8000")
	sb.WriteString("events: joins=7 parts=0 msgs_forwarded=50 notices_forwarded=20 actions_forwarded=1 errors_sent=2 rate_limited=0")
	sb.WriteString("pings: in=0 out=0 pongs: in=0 out=0")
	sb.WriteString("resources: sent=0 received=0 rejected=0 bytes_sent=0 bytes_received=0")
	statsText := sb.String()
	if len(statsText) < 500 {
		t.Fatalf("fixture stats body too short (%v bytes); need a realistic oversized body", len(statsText))
	}
	chat.hooks.FormatStats = func() string { return statsText }

	outgoing := &OutgoingList{}
	if got := chat.HandleOperatorCommand(link, peer, nil, "/stats", outgoing); !got {
		t.Fatal("/stats should be recognized for a server op")
	}
	if len(outgoing.Queue) == 0 {
		t.Fatal("op /stats queued nothing — the stats reply would never reach the client")
	}

	// Every queued payload must fit the RNS MDU or RNS drops the packet.
	var rebuilt strings.Builder
	for i, item := range outgoing.Queue {
		if len(item.Payload) > rnsMDU {
			t.Errorf("queued payload[%d] is %v bytes > RNS MDU %v — RNS will drop it",
				i, len(item.Payload), rnsMDU)
		}
		sent := decodeOutgoing(t, &OutgoingList{Queue: []OutgoingItem{item}})
		if len(sent) != 1 || sent[0].msgType != TNotice {
			t.Fatalf("queued payload[%d] = %+v, want one T_NOTICE", i, sent[0])
		}
		if sent[0].room != nil {
			t.Errorf("queued payload[%d] room = %v, want room=nil", i, *sent[0].room)
		}
		body, _ := sent[0].body.(string)
		rebuilt.WriteString(body)
	}
	if rebuilt.String() != statsText {
		t.Errorf("reassembled NOTICE bodies:\n got %q\nwant %q", rebuilt.String(), statsText)
	}
	if len(outgoing.Queue) < 2 {
		t.Errorf("queued %v packet(s); a %v-byte body must be split into multiple MTU-safe chunks",
			len(outgoing.Queue), len(statsText))
	}
}
