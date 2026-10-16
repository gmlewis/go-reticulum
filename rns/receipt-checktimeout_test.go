// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPacketReceiptCheckTimeout pins Python's PacketReceipt.check_timeout
// semantics (RNS/Packet.py:535-545) on their own, without waiting for a sweep:
// only a receipt that is still SENT and whose sent_at+timeout has passed
// concludes, and the -1 timeout sentinel concludes it as CULLED rather than
// FAILED. The SENT guard is what makes the transition idempotent, which matters
// because two passes reach these receipts (the transport's
// processReceipts and the link Channel's checkTimeouts).
func TestPacketReceiptCheckTimeout(t *testing.T) {
	t.Parallel()

	const now = 1000.0

	tests := []struct {
		name       string
		sentAt     float64
		timeout    float64
		status     int
		wantFired  bool
		wantStatus int
	}{
		{"elapsed window concludes as failed", now - 10, 5, ReceiptSent, true, ReceiptFailed},
		{"window still open does not conclude", now - 1, 5, ReceiptSent, false, ReceiptSent},
		{"window expiring exactly now does not conclude (Python uses a strict comparison)", now - 5, 5, ReceiptSent, false, ReceiptSent},
		{"cull sentinel concludes as culled", now - 10, receiptTimeoutCulled, ReceiptSent, true, ReceiptCulled},
		{"an already-delivered receipt is left alone", now - 10, 5, ReceiptDelivered, false, ReceiptDelivered},
		{"a second call is a no-op", now - 10, 5, ReceiptFailed, false, ReceiptFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var fires atomic.Int32
			pr := &PacketReceipt{
				Hash:          bytes.Repeat([]byte{0x22}, TruncatedHashLength/8),
				TruncatedHash: bytes.Repeat([]byte{0x22}, TruncatedHashLength/8),
				Sent:          true,
				SentAt:        tc.sentAt,
				Timeout:       tc.timeout,
				Status:        tc.status,
			}
			pr.SetTimeoutCallback(func(*PacketReceipt) { fires.Add(1) })

			if got := pr.CheckTimeout(now); got != tc.wantFired {
				t.Errorf("CheckTimeout = %v, want %v", got, tc.wantFired)
			}
			if got := pr.GetStatus(); got != tc.wantStatus {
				t.Errorf("status = %v, want %v", got, tc.wantStatus)
			}
			if got := fires.Load(); got != btoi(tc.wantFired) {
				t.Errorf("timeout callback fired %v times, want %v", got, btoi(tc.wantFired))
			}
		})
	}
}

// TestPacketReceiptCheckTimeoutFiresOnce verifies that repeated passes over the
// same receipt — which is what the transport sweep and the channel's retry pass
// both do once a second — retire it exactly once. Before the SENT guard the
// callback fired on every pass, which for a channel envelope means a retry storm.
func TestPacketReceiptCheckTimeoutFiresOnce(t *testing.T) {
	t.Parallel()

	var fires atomic.Int32
	pr := &PacketReceipt{
		Hash:          bytes.Repeat([]byte{0x33}, TruncatedHashLength/8),
		TruncatedHash: bytes.Repeat([]byte{0x33}, TruncatedHashLength/8),
		Sent:          true,
		SentAt:        1000,
		Timeout:       5,
		Status:        ReceiptSent,
	}
	pr.SetTimeoutCallback(func(*PacketReceipt) { fires.Add(1) })

	for range 10 {
		pr.CheckTimeout(2000)
	}
	if got := fires.Load(); got != 1 {
		t.Errorf("timeout callback fired %v times over ten passes, want 1", got)
	}
}

// TestProcessReceiptsLeavesUnwatchedReceiptsAlone pins the Go-only conservatism
// in TransportSystem.processReceipts: a receipt nobody registered a timeout
// callback for is never expired, however long it sits. Python arms and sweeps
// every receipt, but with no observer there is nothing for the expiry to mean,
// and inventing one is how a mesh-wide spurious teardown would start.
func TestProcessReceiptsLeavesUnwatchedReceiptsAlone(t *testing.T) {
	t.Parallel()

	tmpDir := testutils.TempDir(t, "rns-receipt-timeout-")

	testutils.RunInBubble(t, func(t *testing.T) {
		ts := NewTransportSystem(NewLogger())
		if err := ts.Start(tmpDir); err != nil {
			t.Errorf("Start: %v", err)
			return
		}

		unwatched := &PacketReceipt{
			Hash:          bytes.Repeat([]byte{0x44}, TruncatedHashLength/8),
			TruncatedHash: bytes.Repeat([]byte{0x44}, TruncatedHashLength/8),
			Timeout:       0, // unarmed: as created by Packet.Send before registration
		}
		unwatched.MarkSent(float64(time.Now().Add(-time.Hour).UnixNano()) / 1e9)

		ts.mu.Lock()
		ts.receipts = append(ts.receipts, unwatched)
		ts.mu.Unlock()

		// Several maintenance intervals of virtual time.
		time.Sleep(5 * time.Second)

		if got := unwatched.GetStatus(); got != ReceiptSent {
			t.Errorf("unwatched receipt status = %v, want %v (an unobserved receipt must never be expired)",
				got, ReceiptSent)
		}
		ts.mu.Lock()
		stillListed := len(ts.receipts)
		ts.mu.Unlock()
		if stillListed == 0 {
			t.Error("unwatched receipt was removed from the outstanding list")
		}

		ts.Stop()
		testutils.Wait()
	})
}

func btoi(b bool) int32 {
	if b {
		return 1
	}
	return 0
}
