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

// TestHandleStatsChunksAreWholeLines pins the readable /stats reply:
// FormatStats lines must be newline-separated so QueueNoticeChunks emits
// one complete stat line per NOTICE. Joining with "" made the authorized
// reply a single 600+ byte blob that chunked mid-word on the wire
// (live capture: "…rate_limi" / "ted=0pings: …").
func TestHandleStatsChunksAreWholeLines(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	env.makeServerOp(peer)

	sm := NewStatsManager(
		func() float64 { return 1730000000.0 },
		func() float64 { return 2000.0 },
	)
	sm.SetStartTime()
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
			SessionsTotal:      2,
			SessionsIdentified: 2,
			SessionsWelcomed:   2,
			RoomsTotal:         1,
			Memberships:        2,
			TrustedCount:       6,
		})
	}

	// FormatStats itself must be newline-separated (like /list's "\n".join).
	body := chat.hooks.FormatStats()
	if !strings.Contains(body, "\n") {
		t.Fatalf("FormatStats body has no newlines (would chunk mid-word): %q", body)
	}
	lines := strings.Split(body, "\n")
	if len(lines) < 5 {
		t.Fatalf("FormatStats produced %v lines, want the full stat set", len(lines))
	}
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" && i != len(lines)-1 {
			t.Errorf("FormatStats line %d is blank", i)
		}
	}

	outgoing := &OutgoingList{}
	if got := chat.HandleOperatorCommand(link, peer, nil, "/stats", outgoing); !got {
		t.Fatal("/stats should be recognized")
	}
	if len(outgoing.Queue) == 0 {
		t.Fatal("no NOTICE chunks queued")
	}

	// Every chunk must be one complete FormatStats line — never a mid-word
	// fragment of a joined blob.
	wantSet := map[string]bool{}
	for _, ln := range lines {
		if ln != "" {
			wantSet[ln] = true
		}
	}
	var gotLines []string
	for i, item := range outgoing.Queue {
		if len(item.Payload) > rnsMDU {
			t.Errorf("payload[%d] = %v > MDU %v", i, len(item.Payload), rnsMDU)
		}
		sent := decodeOutgoing(t, &OutgoingList{Queue: []OutgoingItem{item}})
		if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
			t.Fatalf("payload[%d] not a room-nil NOTICE", i)
		}
		text, _ := sent[0].body.(string)
		gotLines = append(gotLines, text)
		if !wantSet[text] {
			t.Errorf("chunk[%d] = %q is not a complete FormatStats line", i, text)
		}
	}
	// Every non-empty FormatStats line must have been delivered.
	if len(gotLines) != len(wantSet) {
		t.Errorf("delivered %v chunks, want %v complete lines\ngot: %q", len(gotLines), len(wantSet), gotLines)
	}
}
