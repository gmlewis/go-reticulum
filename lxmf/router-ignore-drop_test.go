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

// TestHandleInboundMessageDropsIgnoredSource verifies that a message whose
// source hash is on the router's ignored list is dropped at the top of
// handleInboundMessage before the delivery callback fires, mirroring Python
// LXMRouter.lxmf_delivery (LXMRouter.py:1902-1904, v1.0.0+):
//
//	if message.source_hash in self.ignored_list:
//	    RNS.log(str(self)+" ignored message from "+...)
//	    return False
//
// A non-ignored source still delivers normally. Unlike the blackhole check,
// the ignored-list check is keyed on the DESTINATION hash the app registered
// via IgnoreDestination (NomadNetworkApp.block_destination →
// LXMRouter.ignore_destination, NomadNetworkApp.py:566-574), so it also covers
// sources whose identity is not recalled (SourceBlackholed stays false for an
// unrecalled identity).
func TestHandleInboundMessageDropsIgnoredSource(t *testing.T) {
	t.Parallel()

	deliver := func(t *testing.T, ignore bool) (delivered bool) {
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

		if ignore {
			router.IgnoreDestination(sourceDest.Hash)
			if !router.IsIgnored(sourceDest.Hash) {
				t.Fatal("precondition: source hash must be on the router ignored list")
			}
		}

		router.RegisterDeliveryCallback(func(_ *Message) { delivered = true })

		msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
		mustTest(t, msg.Pack())

		unpacked, err := UnpackMessageFromBytes(ts, msg.Packed, MethodDirect)
		if err != nil {
			t.Fatalf("UnpackMessageFromBytes: %v", err)
		}

		router.handleInboundMessage(unpacked)
		return delivered
	}

	t.Run("ignored_source_not_delivered", func(t *testing.T) {
		t.Parallel()
		if deliver(t, true) {
			t.Fatal("delivery callback fired for an ignored source; message should have been dropped")
		}
	})

	t.Run("non_ignored_source_delivered", func(t *testing.T) {
		t.Parallel()
		if !deliver(t, false) {
			t.Fatal("delivery callback did not fire for a non-ignored source")
		}
	})

	t.Run("unignore_restores_delivery", func(t *testing.T) {
		t.Parallel()
		ts := rns.NewTransportSystem(nil)
		router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

		sourceID := mustTestNewIdentity(t, true)
		destID := mustTestNewIdentity(t, true)
		sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
		destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
		ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
		_, err := router.RegisterDeliveryIdentity(destID, "dest", nil)
		mustTest(t, err)

		router.IgnoreDestination(sourceDest.Hash)
		router.UnignoreDestination(sourceDest.Hash)
		if router.IsIgnored(sourceDest.Hash) {
			t.Fatal("precondition: unignored hash must not be on the ignored list")
		}

		var delivered int
		router.RegisterDeliveryCallback(func(_ *Message) { delivered++ })

		msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
		mustTest(t, msg.Pack())
		unpacked, err := UnpackMessageFromBytes(ts, msg.Packed, MethodDirect)
		if err != nil {
			t.Fatalf("UnpackMessageFromBytes: %v", err)
		}
		router.handleInboundMessage(unpacked)

		if delivered != 1 {
			t.Fatalf("after UnignoreDestination delivered = %v, want 1", delivered)
		}
	})
}
