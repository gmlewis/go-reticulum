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

// TestTransportExpiresTimedOutReceipts asserts the receipt-expiry contract the
// LXMF router's link recovery depends on.
//
// Python's Transport.jobs sweeps the outstanding-receipt list once per
// receipts_check_interval (1.0 s) and calls PacketReceipt.check_timeout() on
// every entry (RNS/Transport.py:569-587). check_timeout marks a receipt whose
// `sent_at + timeout` has passed as FAILED and runs its timeout callback
// (RNS/Packet.py:535-545).
//
// That callback is the only way a message handed to a link but never proven
// leaves SENDING: lxmf.sendMessagePacketLocked wires it to reset the message to
// OUTBOUND and to tear the link down (lxmf/router.go), and ProcessOutbound's
// ACTIVE-link branch skips any message still in SENDING with "Waiting for
// proof". Without the sweep such a message stays SENDING for ever, so the
// client keeps using the dead link and never rebuilds it.
//
// The sweep hangs off the transport's own 1 s maintenance ticker, so this runs
// on a synctest bubble's clock and advances virtual seconds rather than sleeping
// through real ones.
func TestTransportExpiresTimedOutReceipts(t *testing.T) {
	t.Parallel()

	tmpDir := testutils.TempDir(t, "rns-receipt-timeout-")

	testutils.RunInBubble(t, func(t *testing.T) {
		ts := NewTransportSystem(NewLogger())
		if err := ts.Start(tmpDir); err != nil {
			t.Errorf("Start: %v", err)
			return
		}

		var fired atomic.Bool
		receipt := &PacketReceipt{
			Hash:          bytes.Repeat([]byte{0x11}, TruncatedHashLength/8),
			TruncatedHash: bytes.Repeat([]byte{0x11}, TruncatedHashLength/8),
			Timeout:       0.01,
		}
		receipt.SetTimeoutCallback(func(*PacketReceipt) { fired.Store(true) })
		// Sent a second ago, so the timeout window has long since passed by the
		// time the transport's sweep runs.
		receipt.MarkSent(float64(time.Now().Add(-time.Second).UnixNano()) / 1e9)

		ts.mu.Lock()
		ts.receipts = append(ts.receipts, receipt)
		ts.mu.Unlock()

		// One maintenance interval suffices; allow three.
		time.Sleep(3 * time.Second)

		if !fired.Load() {
			t.Errorf("receipt timeout callback never fired: the transport never expires outstanding receipts, so a message handed to a link whose proof never arrives stays SENDING for ever (Python Transport.jobs -> PacketReceipt.check_timeout, Transport.py:569-587 / Packet.py:535-545)")
		}
		if got := receipt.Status; got != ReceiptFailed {
			t.Errorf("receipt status=%v, want %v (ReceiptFailed)", got, ReceiptFailed)
		}
		ts.mu.Lock()
		stillListed := len(ts.receipts)
		ts.mu.Unlock()
		if stillListed != 0 {
			t.Errorf("receipts still outstanding=%v, want 0 (expired receipts are removed, Transport.py:584-585)", stillListed)
		}

		// Join the maintenance and traffic loops before the bubble closes: the
		// runtime deadlocks loudly on a goroutine still parked on a bubble timer.
		ts.Stop()
		testutils.Wait()
	})
}
