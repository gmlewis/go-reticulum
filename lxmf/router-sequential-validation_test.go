// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestRouterConfigSequentialValidationDefaults verifies that NewRouterFromConfig
// with an unspecified sequential-validation config keeps
// the Python defaults — sequential validation enabled, static peers exempt,
// and a max-inbound-syncs cap of 3 (LXMRouter.py:56-58,143-145, v1.1.0).
func TestRouterConfigSequentialValidationDefaults(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath: testutils.TempDir(t, tempDirPrefix),
	})

	if !router.propagationSequentialValidation {
		t.Fatalf("propagationSequentialValidation=false, want default true")
	}
	if router.propagationStaticPeerSequential {
		t.Fatalf("propagationStaticPeerSequential=true, want default false")
	}
	if got, want := router.propagationMaxInboundSyncs, DefaultMaxInboundSyncs; got != want {
		t.Fatalf("propagationMaxInboundSyncs=%v want %v", got, want)
	}
}

// TestRouterConfigSequentialValidationOverrides verifies that explicit
// sequential-validation config values propagate onto the router,
// including MaxInboundSyncs=0 which disables the inbound-sync cap
// (LXMRouter.py:2281, v1.1.0).
func TestRouterConfigSequentialValidationOverrides(t *testing.T) {
	t.Parallel()

	off := false
	maxSyncs := 5
	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath:          testutils.TempDir(t, tempDirPrefix),
		SequentialValidation: &off,
		StaticSequential:     true,
		MaxInboundSyncs:      &maxSyncs,
	})

	if router.propagationSequentialValidation {
		t.Fatalf("propagationSequentialValidation=true, want false")
	}
	if !router.propagationStaticPeerSequential {
		t.Fatalf("propagationStaticPeerSequential=false, want true")
	}
	if got, want := router.propagationMaxInboundSyncs, 5; got != want {
		t.Fatalf("propagationMaxInboundSyncs=%v want %v", got, want)
	}

	disabled := 0
	router2 := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath:     testutils.TempDir(t, tempDirPrefix),
		MaxInboundSyncs: &disabled,
	})
	if got, want := router2.propagationMaxInboundSyncs, 0; got != want {
		t.Fatalf("propagationMaxInboundSyncs=%v want %v (disabled)", got, want)
	}
}

// TestPropagationResourcesTransferringCountsAboveAccepted verifies that
// PropagationResourcesTransferring counts only accepted-offer links
// whose state is strictly greater than OFFER_ACCEPTED (i.e. TRANSFERRING and
// VALIDATING), mirroring Python's propagation_resources_transferring property
// (LXMRouter.py:2197-2204, v1.1.0).
func TestPropagationResourcesTransferringCountsAboveAccepted(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	router.acceptedOfferLinksMu.Lock()
	router.acceptedOfferLinks["unknown-link"] = OfferUnknown
	router.acceptedOfferLinks["accepted-link"] = OfferAccepted
	router.acceptedOfferLinks["transferring-link"] = OfferTransferring
	router.acceptedOfferLinks["validating-link"] = OfferValidating
	router.acceptedOfferLinksMu.Unlock()

	if got, want := router.PropagationResourcesTransferring(), 2; got != want {
		t.Fatalf("PropagationResourcesTransferring()=%v want %v", got, want)
	}
}

// TestPropagationResourceAdvertisedTransitionsAcceptedToTransferring verifies
// that when an offer has already been recorded as OFFER_ACCEPTED (by a
// partial-accept offer request), propagationResourceAdvertised advances
// it to OFFER_TRANSFERRING; a link with no recorded offer stays untracked
// (LXMRouter.py:2226-2232, v1.1.0).
func TestPropagationResourceAdvertisedTransitionsAcceptedToTransferring(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.propagationEnabled = true

	remoteID := mustTestNewIdentity(t, true)
	remoteDest := mustTestNewDestination(t, ts, remoteID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	link, err := rns.NewLink(ts, remoteDest)
	mustTest(t, err)
	activateRouterTestLink(t, link)
	setRouterLinkField(t, link, "remoteIdentity", remoteID)
	// NewLink leaves linkID nil until Establish; assign distinct IDs so the two
	// links do not collide on the empty-string map key.
	linkID := bytes.Repeat([]byte{0xA1}, 16)
	setRouterLinkField(t, link, "linkID", linkID)

	// A link whose offer was already accepted transitions to TRANSFERRING.
	router.acceptedOfferLinksMu.Lock()
	router.acceptedOfferLinks[string(linkID)] = OfferAccepted
	router.acceptedOfferLinksMu.Unlock()

	if !router.propagationResourceAdvertised(link, &rns.ResourceAdvertisement{D: 1001}) {
		t.Fatalf("propagationResourceAdvertised returned false; want true")
	}
	router.acceptedOfferLinksMu.Lock()
	got := router.acceptedOfferLinks[string(linkID)]
	router.acceptedOfferLinksMu.Unlock()
	if got != OfferTransferring {
		t.Fatalf("acceptedOfferLinks[%x]=%v want %v (OFFER_TRANSFERRING)", linkID, got, OfferTransferring)
	}

	// A link with no recorded offer is left untracked but still accepted.
	otherLink, err := rns.NewLink(ts, remoteDest)
	mustTest(t, err)
	activateRouterTestLink(t, otherLink)
	setRouterLinkField(t, otherLink, "remoteIdentity", remoteID)
	otherID := bytes.Repeat([]byte{0xB2}, 16)
	setRouterLinkField(t, otherLink, "linkID", otherID)
	if !router.propagationResourceAdvertised(otherLink, &rns.ResourceAdvertisement{D: 1001}) {
		t.Fatalf("propagationResourceAdvertised returned false for untracked link; want true")
	}
	router.acceptedOfferLinksMu.Lock()
	_, tracked := router.acceptedOfferLinks[string(otherID)]
	router.acceptedOfferLinksMu.Unlock()
	if tracked {
		t.Fatalf("untracked link %x was added to acceptedOfferLinks; want absent", otherID)
	}
}

// TestPropagationResourceConcludedRecordsValidatingAndCleansUp verifies that
// during propagationResourceConcluded the offer state advances to
// OFFER_VALIDATING and the remote hash is recorded in validatingPnStampsFrom
// for the duration of stamp validation, then both the validation-batch entry
// and the accepted-offer link are cleaned up regardless of validation
// outcome, mirroring Python's finally block (LXMRouter.py:2390-2424, v1.1.0).
func TestPropagationResourceConcludedRecordsValidatingAndCleansUp(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	remoteID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, remoteID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	message := mustTestNewMessage(t, remoteDest, sourceDest, "payload", "title", nil)
	mustTest(t, message.Pack())

	propagationStamp, _, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
	mustTest(t, err)
	transientData := append(append([]byte{}, message.Packed...), propagationStamp...)
	resourceData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	mustTest(t, err)

	link, err := rns.NewLink(ts, remoteDest)
	mustTest(t, err)
	activateRouterTestLink(t, link)
	setRouterLinkField(t, link, "remoteIdentity", remoteID)
	linkID := bytes.Repeat([]byte{0xC3}, 16)
	setRouterLinkField(t, link, "linkID", linkID)
	remoteHash := rns.CalculateHash(remoteID, AppName, "propagation")

	// The offer was accepted and has begun transferring.
	router.acceptedOfferLinksMu.Lock()
	router.acceptedOfferLinks[string(linkID)] = OfferTransferring
	router.acceptedOfferLinksMu.Unlock()

	// Observe the mid-validation state via the validation seam: when the
	// validation function runs, the offer must already be OFFER_VALIDATING and
	// the remote hash must be recorded in validatingPnStampsFrom.
	observed := make(chan error, 1)
	router.validatePropagationMessagesFn = func(messages [][]byte, minAcceptedCost int) []validatedPropagationMessage {
		router.acceptedOfferLinksMu.Lock()
		offerState, offerTracked := router.acceptedOfferLinks[string(linkID)]
		router.acceptedOfferLinksMu.Unlock()
		router.sequentialValidationMu.Lock()
		_, validating := router.validatingPnStampsFrom[string(remoteHash)]
		router.sequentialValidationMu.Unlock()
		if !offerTracked || offerState != OfferValidating {
			observed <- fmt.Errorf("offer state=%v tracked=%v, want %v (OFFER_VALIDATING)", offerState, offerTracked, OfferValidating)
			return validatePropagationMessages(messages, minAcceptedCost)
		}
		if !validating {
			observed <- fmt.Errorf("remote hash %x not in validatingPnStampsFrom during validation", remoteHash)
			return validatePropagationMessages(messages, minAcceptedCost)
		}
		observed <- nil
		return validatePropagationMessages(messages, minAcceptedCost)
	}

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)

	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("validation seam never ran; offer VALIDATING state not observed")
	}

	// After conclusion, both the offer link and the validation-batch entry are
	// cleaned up.
	router.acceptedOfferLinksMu.Lock()
	_, offerLeft := router.acceptedOfferLinks[string(linkID)]
	router.acceptedOfferLinksMu.Unlock()
	if offerLeft {
		t.Fatalf("acceptedOfferLinks[%x] still present after concluded; want cleaned up", linkID)
	}
	router.sequentialValidationMu.Lock()
	_, validatingLeft := router.validatingPnStampsFrom[string(remoteHash)]
	router.sequentialValidationMu.Unlock()
	if validatingLeft {
		t.Fatalf("validatingPnStampsFrom[%x] still present after concluded; want cleaned up", remoteHash)
	}
}
