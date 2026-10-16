// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestRouterPropagatedInboundDecryptsAndDelivers pins the local-delivery branch
// of the propagation-node sync path.
//
// A message downloaded from a propagation node does not arrive as plaintext
// LXMF bytes: lxmf_data is destinationHash + Encrypt(sourceHash + signature +
// packed payload), the same envelope a paper (URI) message carries, so the
// router has to decrypt it with the local delivery destination before it can
// unpack anything. Python does that in lxmf_propagation (LXMRouter.py:2508-2514)
// and Go's own paper-message path does it in ingestURIOutcome — but the
// network-sync path skipped it, so every propagated message was counted as
// downloaded by messageGetResponse and then silently discarded here: the sync
// reported "Downloaded 1 new messages" while the recipient's conversation
// stayed empty.
func TestRouterPropagatedInboundDecryptsAndDelivers(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	deliveryDest, err := router.RegisterDeliveryIdentity(router.identity, "", nil)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	delivered := make(chan *Message, 1)
	router.RegisterDeliveryCallback(func(m *Message) {
		select {
		case delivered <- m:
		default:
		}
	})

	// The peer that sends the message: its own identity and outbound delivery
	// destination, which is what the propagated envelope is signed by.
	senderID := mustTestNewIdentity(t, true)
	senderDest := mustTestNewDestination(t, ts, senderID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	const body = "propagated body"
	msg := mustTestNewMessage(t, deliveryDest, senderDest, body, "propagated title", nil)
	lxmfData := mustTestPropagatedPayload(t, msg)
	if len(lxmfData) < DestinationLength {
		t.Fatalf("propagated lxmf_data is %v bytes, want at least %v", len(lxmfData), DestinationLength)
	}
	if got := string(lxmfData[:DestinationLength]); got != string(deliveryDest.Hash) {
		t.Fatalf("propagated lxmf_data is addressed to %x, want %x", lxmfData[:DestinationLength], deliveryDest.Hash)
	}

	// Precondition: the router does consider itself the destination, so the
	// only thing that can drop the message from here is the unpack step.
	router.mu.Lock()
	_, isLocalDelivery := router.deliveryDestinations[string(lxmfData[:DestinationLength])]
	router.mu.Unlock()
	if !isLocalDelivery {
		t.Fatalf("router does not own the delivery destination %x", lxmfData[:DestinationLength])
	}

	if duplicate := router.handlePropagatedInbound(lxmfData); duplicate {
		t.Fatal("handlePropagatedInbound reported a freshly downloaded message as a duplicate")
	}

	select {
	case got := <-delivered:
		if got.ContentString() != body {
			t.Errorf("delivered content = %q, want %q", got.ContentString(), body)
		}
		if got.method != MethodPropagated {
			t.Errorf("delivered method = %v, want MethodPropagated", got.method)
		}
		if got.SourceHash == nil || string(got.SourceHash) != string(senderDest.Hash) {
			t.Errorf("delivered source = %x, want %x", got.SourceHash, senderDest.Hash)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the downloaded propagated message never reached the delivery callback")
	}

	// The transient ID must be recorded as delivered, so the same message is not
	// delivered twice on the next sync.
	router.mu.Lock()
	_, deliveredID := router.locallyDeliveredIDs[string(rns.FullHash(lxmfData))]
	router.mu.Unlock()
	if !deliveredID {
		t.Fatal("the delivered message's transient ID was not recorded in locallyDeliveredIDs")
	}
}
