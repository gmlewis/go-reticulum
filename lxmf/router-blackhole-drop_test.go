// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestUnpackSetsSourceBlackholed covers Phase 15 task 1: unpacking an LXM
// records whether the recalled source identity is on the local blackhole
// list, mirroring Python LXMessage.unpack_from_bytes (LXMessage.py:804,
// v1.0.0+) which sets message.source_blackholed via
// RNS.Reticulum.get_instance().is_blackholed(source_identity). When the
// source identity is blackholed SourceBlackholed is true; otherwise false.
func TestUnpackSetsSourceBlackholed(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T, blackhole bool) (*Message, *rns.TransportSystem, []byte) {
		t.Helper()
		ts := rns.NewTransportSystem(nil)
		sourceID := mustTestNewIdentity(t, true)
		destID := mustTestNewIdentity(t, true)
		sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
		destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
		// Make the source identity recallable from its delivery-destination hash.
		ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)

		if blackhole {
			if !ts.BlackholeIdentity(sourceID.Hash, nil, "test-blackhole") {
				t.Fatal("BlackholeIdentity returned false")
			}
		}

		msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
		mustTest(t, msg.Pack())

		unpacked, err := UnpackMessageFromBytes(ts, msg.Packed, MethodDirect)
		if err != nil {
			t.Fatalf("UnpackMessageFromBytes: %v", err)
		}
		return unpacked, ts, sourceID.Hash
	}

	t.Run("blackholed_source", func(t *testing.T) {
		t.Parallel()
		unpacked, ts, wantHash := setup(t, true)
		if !unpacked.SourceBlackholed {
			t.Fatal("expected SourceBlackholed=true for a blackholed source identity")
		}
		// Sanity: the source identity hash is on this transport's blackhole list.
		if !ts.IsBlackholed(wantHash) {
			t.Fatal("precondition: source identity hash must be blackholed")
		}
	})

	t.Run("non_blackholed_source", func(t *testing.T) {
		t.Parallel()
		unpacked, _, _ := setup(t, false)
		if unpacked.SourceBlackholed {
			t.Fatal("expected SourceBlackholed=false for a non-blackholed source identity")
		}
	})
}

// TestHandleInboundMessageDropsBlackholedSource covers Phase 15 task 1: a
// message whose source identity is blackholed is dropped at the top of
// handleInboundMessage before the delivery callback fires, mirroring Python
// LXMRouter.lxmf_delivery (LXMRouter.py:1841-1843, v1.0.0+) which logs and
// returns False. A non-blackholed source still delivers normally.
func TestHandleInboundMessageDropsBlackholedSource(t *testing.T) {
	t.Parallel()

	deliver := func(t *testing.T, blackhole bool) (delivered bool) {
		t.Helper()
		ts := rns.NewTransportSystem(nil)
		router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

		sourceID := mustTestNewIdentity(t, true)
		destID := mustTestNewIdentity(t, true)
		sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
		destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
		ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
		_, err := router.RegisterDeliveryIdentity(destID, "dest", nil)
		mustTest(t, err)

		if blackhole {
			if !ts.BlackholeIdentity(sourceID.Hash, nil, "test-blackhole") {
				t.Fatal("BlackholeIdentity returned false")
			}
		}

		router.RegisterDeliveryCallback(func(_ *Message) { delivered = true })

		msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
		mustTest(t, msg.Pack())

		unpacked, err := UnpackMessageFromBytes(ts, msg.Packed, MethodDirect)
		if err != nil {
			t.Fatalf("UnpackMessageFromBytes: %v", err)
		}
		if blackhole && !unpacked.SourceBlackholed {
			t.Fatal("precondition: unpacked message must report SourceBlackholed=true")
		}

		router.handleInboundMessage(unpacked)
		return delivered
	}

	t.Run("blackholed_source_not_delivered", func(t *testing.T) {
		t.Parallel()
		if deliver(t, true) {
			t.Fatal("delivery callback fired for a blackholed source; message should have been dropped")
		}
	})

	t.Run("non_blackholed_source_delivered", func(t *testing.T) {
		t.Parallel()
		if !deliver(t, false) {
			t.Fatal("delivery callback did not fire for a non-blackholed source")
		}
	})
}
