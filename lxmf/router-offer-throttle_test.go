// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// offerThrottleRouter builds a router and remote identity used by the
// offerRequest throttle tests, with peering cost 1 so a valid peering key can
// be generated quickly when a full offer is needed.
func offerThrottleRouter(t *testing.T) (*Router, *rns.Identity, []byte) {
	t.Helper()
	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.propagationEnabled = true
	router.peeringCost = 1
	remoteID := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteID, AppName, "propagation")
	return router, remoteID, remoteHash
}

// assertThrottled asserts that offerRequest returned peerErrorThrottled.
func assertThrottled(t *testing.T, result any) {
	t.Helper()
	got, ok := result.(int)
	if !ok || got != peerErrorThrottled {
		t.Fatalf("offerRequest result=%v want peerErrorThrottled (%v)", result, peerErrorThrottled)
	}
}

// assertNotThrottled asserts that offerRequest did not return peerErrorThrottled
// (it advanced past the throttle to a later check).
func assertNotThrottled(t *testing.T, result any) {
	t.Helper()
	if got, ok := result.(int); ok && got == peerErrorThrottled {
		t.Fatalf("offerRequest returned peerErrorThrottled; expected to bypass throttle")
	}
}

// TestOfferRequestThrottlesWhenValidationBatchInProgress verifies that when
// sequential validation is enabled and a PN-stamp validation batch is in
// progress (validatingPnStampsFrom non-empty), offerRequest returns
// peerErrorThrottled (LXMRouter.py:2274-2278, v1.1.0).
func TestOfferRequestThrottlesWhenValidationBatchInProgress(t *testing.T) {
	t.Parallel()

	router, remoteID, _ := offerThrottleRouter(t)
	router.propagationSequentialValidation = true

	router.sequentialValidationMu.Lock()
	router.validatingPnStampsFrom["some-remote-hash"] = router.now()
	router.sequentialValidationMu.Unlock()

	result := router.offerRequest("", nil, nil, []byte("link-id"), remoteID, router.now())
	assertThrottled(t, result)
}

// TestOfferRequestThrottlesAtInboundSyncCap verifies that when the number of
// transferring inbound sync resources reaches propagationMaxInboundSyncs,
// offerRequest returns peerErrorThrottled (LXMRouter.py:2280-2283, v1.1.0).
func TestOfferRequestThrottlesAtInboundSyncCap(t *testing.T) {
	t.Parallel()

	router, remoteID, _ := offerThrottleRouter(t)
	router.propagationMaxInboundSyncs = 3

	router.acceptedOfferLinksMu.Lock()
	router.acceptedOfferLinks["link-a"] = OfferTransferring
	router.acceptedOfferLinks["link-b"] = OfferTransferring
	router.acceptedOfferLinks["link-c"] = OfferTransferring
	router.acceptedOfferLinksMu.Unlock()

	result := router.offerRequest("", nil, nil, []byte("link-id"), remoteID, router.now())
	assertThrottled(t, result)
}

// TestOfferRequestThrottleDoesNotFireBelowCap verifies that with fewer than
// propagationMaxInboundSyncs transferring resources and no
// validation batch, the throttle does not fire (the request advances to the
// later invalid-data check, proving it was not throttled).
func TestOfferRequestThrottleDoesNotFireBelowCap(t *testing.T) {
	t.Parallel()

	router, remoteID, _ := offerThrottleRouter(t)
	router.propagationMaxInboundSyncs = 3

	router.acceptedOfferLinksMu.Lock()
	router.acceptedOfferLinks["link-a"] = OfferTransferring
	router.acceptedOfferLinks["link-b"] = OfferTransferring
	router.acceptedOfferLinksMu.Unlock()

	result := router.offerRequest("", nil, nil, []byte("link-id"), remoteID, router.now())
	assertNotThrottled(t, result)
}

// TestOfferRequestStaticPeerBypassesThrottle verifies that a static peer
// bypasses the sequential-validation and inbound-sync-cap throttles when
// propagationStaticPeerSequential is false (its default), so a validation
// batch in progress does not throttle the offer (LXMRouter.py:2273, v1.1.0).
func TestOfferRequestStaticPeerBypassesThrottle(t *testing.T) {
	t.Parallel()

	router, remoteID, remoteHash := offerThrottleRouter(t)
	router.propagationSequentialValidation = true
	router.propagationStaticPeerSequential = false
	router.propagationMaxInboundSyncs = 1

	router.mu.Lock()
	router.staticPeers[string(remoteHash)] = struct{}{}
	router.mu.Unlock()

	router.sequentialValidationMu.Lock()
	router.validatingPnStampsFrom[string(remoteHash)] = router.now()
	router.sequentialValidationMu.Unlock()
	router.acceptedOfferLinksMu.Lock()
	router.acceptedOfferLinks["other-link"] = OfferTransferring
	router.acceptedOfferLinksMu.Unlock()

	result := router.offerRequest("", nil, nil, []byte("link-id"), remoteID, router.now())
	assertNotThrottled(t, result)
}

// TestOfferRequestStaticPeerSequentialDisablesBypass verifies that when
// propagationStaticPeerSequential is true, static peers do not bypass the
// throttle and are still throttled while a validation batch runs
// (LXMRouter.py:2273, v1.1.0).
func TestOfferRequestStaticPeerSequentialDisablesBypass(t *testing.T) {
	t.Parallel()

	router, remoteID, remoteHash := offerThrottleRouter(t)
	router.propagationSequentialValidation = true
	router.propagationStaticPeerSequential = true

	router.mu.Lock()
	router.staticPeers[string(remoteHash)] = struct{}{}
	router.mu.Unlock()

	router.sequentialValidationMu.Lock()
	router.validatingPnStampsFrom[string(remoteHash)] = router.now()
	router.sequentialValidationMu.Unlock()

	result := router.offerRequest("", nil, nil, []byte("link-id"), remoteID, router.now())
	assertThrottled(t, result)
}

// TestOfferRequestPartialAcceptRecordsOfferAccepted verifies that when an
// offer is partially accepted (some but not all transient IDs are wanted),
// offerRequest records acceptedOfferLinks[linkID] = OFFER_ACCEPTED
// so the subsequent resource transfer can advance the offer state, mirroring
// Python (LXMRouter.py:2326-2329, v1.1.0).
func TestOfferRequestPartialAcceptRecordsOfferAccepted(t *testing.T) {
	t.Parallel()

	router, remoteID, _ := offerThrottleRouter(t)
	router.propagationCost = 1

	wantedTransientID := rns.FullHash([]byte("wanted-message"))
	knownTransientID := rns.FullHash([]byte("known-message"))
	// Pre-populate the propagation store with the "known" transient ID so it is
	// not wanted, making the accept partial.
	router.mu.Lock()
	router.propagationEntries[string(knownTransientID)] = &propagationEntry{
		destinationHash: []byte{0x01, 0x02},
		payload:         []byte("known-payload"),
	}
	router.mu.Unlock()

	// Generate a valid peering key for peeringID = identity.hash + remote.hash.
	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteID.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteID.Hash...)
	peeringKey, _, _, err := GenerateStamp(peeringID, router.propagationCost, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}

	data, err := msgpack.Pack([]any{peeringKey, []any{wantedTransientID, knownTransientID}})
	if err != nil {
		t.Fatalf("Pack offer data: %v", err)
	}

	linkID := []byte("partial-link")
	result := router.offerRequest("", data, nil, linkID, remoteID, router.now())

	wanted, ok := result.([]any)
	if !ok {
		t.Fatalf("offerRequest result=%v want []any of wanted transient IDs", result)
	}
	if len(wanted) != 1 || !bytesEqual(wanted[0], wantedTransientID) {
		t.Fatalf("offerRequest wanted=%v want [%x]", wanted, wantedTransientID)
	}

	router.acceptedOfferLinksMu.Lock()
	got, tracked := router.acceptedOfferLinks[string(linkID)]
	router.acceptedOfferLinksMu.Unlock()
	if !tracked {
		t.Fatalf("acceptedOfferLinks[%x] not recorded; want OFFER_ACCEPTED", linkID)
	}
	if got != OfferAccepted {
		t.Fatalf("acceptedOfferLinks[%x]=%v want %v (OFFER_ACCEPTED)", linkID, got, OfferAccepted)
	}
}

// TestOfferRequestFullAcceptDoesNotRecordOfferLink verifies that a full
// accept (all transient IDs wanted) returns true without recording an
// accepted-offer link, since no partial accounting is needed — matching
// Python, which only records accepted_offer_links on a partial accept
// (LXMRouter.py:2322-2325, v1.1.0).
func TestOfferRequestFullAcceptDoesNotRecordOfferLink(t *testing.T) {
	t.Parallel()

	router, remoteID, _ := offerThrottleRouter(t)
	router.propagationCost = 1

	wantedTransientID := rns.FullHash([]byte("only-wanted-message"))

	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteID.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteID.Hash...)
	peeringKey, _, _, err := GenerateStamp(peeringID, router.propagationCost, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}

	data, err := msgpack.Pack([]any{peeringKey, []any{wantedTransientID}})
	if err != nil {
		t.Fatalf("Pack offer data: %v", err)
	}

	linkID := []byte("full-link")
	result := router.offerRequest("", data, nil, linkID, remoteID, router.now())
	if got, ok := result.(bool); !ok || !got {
		t.Fatalf("offerRequest result=%v want true (full accept)", result)
	}

	router.acceptedOfferLinksMu.Lock()
	_, tracked := router.acceptedOfferLinks[string(linkID)]
	router.acceptedOfferLinksMu.Unlock()
	if tracked {
		t.Fatalf("acceptedOfferLinks[%x] recorded on full accept; want absent", linkID)
	}
}

// bytesEqual reports whether an any (expected to be []byte) equals b.
func bytesEqual(v any, b []byte) bool {
	got, ok := v.([]byte)
	if !ok {
		return false
	}
	if len(got) != len(b) {
		return false
	}
	for i := range got {
		if got[i] != b[i] {
			return false
		}
	}
	return true
}
