// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// establishTestLinkForCleanLinks wires two TransportSystems together with a
// PipeInterface pair, registers an IN destination on the receiver, and
// establishes a real link from the initiator to it. It returns the active
// initiator link and the receiver destination hash (the directLinks key).
// The link's lastData is advanced to ~now by the real handshake, so by default
// NoDataFor is ~0 (non-stale); tests inject a future clock via SetNowForTest
// to drive it stale. The watchdog goroutine uses real time.Now (not the
// injected clock), so injecting a future now does not make the watchdog tear
// the link down prematurely.
func establishTestLinkForCleanLinks(t *testing.T) (link *rns.Link, destHash []byte) {
	t.Helper()

	tsA := rns.NewTransportSystem(nil)
	tsB := rns.NewTransportSystem(nil)
	dirA := testutils.TempDir(t, tempDirPrefix)
	dirB := testutils.TempDir(t, tempDirPrefix)
	if err := tsA.Start(dirA); err != nil {
		t.Fatalf("tsA.Start: %v", err)
	}
	if err := tsB.Start(dirB); err != nil {
		t.Fatalf("tsB.Start: %v", err)
	}
	t.Cleanup(func() {
		tsA.Stop()
		tsB.Stop()
	})

	pipeA := interfaces.NewPipeInterface("a", func(data []byte, iface interfaces.Interface) {
		tsA.Inbound(data, iface)
	})
	pipeB := interfaces.NewPipeInterface("b", func(data []byte, iface interfaces.Interface) {
		tsB.Inbound(data, iface)
	})
	pipeA.SetOther(pipeB)
	pipeB.SetOther(pipeA)
	t.Cleanup(func() {
		_ = pipeA.Detach()
		_ = pipeB.Detach()
	})
	tsA.RegisterInterface(pipeA)
	tsB.RegisterInterface(pipeB)

	receiverID := tsB.Identity()
	destB, err := rns.NewDestination(tsB, receiverID, rns.DestinationIn, rns.DestinationSingle, AppName, "receiver")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	establishedReceiver := make(chan *rns.Link, 1)
	destB.SetLinkEstablishedCallback(func(l *rns.Link) { establishedReceiver <- l })

	link, err = rns.NewLink(tsA, destB)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	t.Cleanup(link.Teardown)

	establishedInitiator := make(chan struct{}, 1)
	link.SetLinkEstablishedCallback(func(*rns.Link) { establishedInitiator <- struct{}{} })

	if err := link.Establish(); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}
	select {
	case l := <-establishedReceiver:
		t.Cleanup(l.Teardown)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}

	destHash = append([]byte(nil), destB.Hash...)
	return link, destHash
}

// TestCleanLinksDirectLinksRemovesStale verifies that a direct-delivery
// link whose no_data_for exceeds LXMRouter.LINK_MAX_INACTIVITY (10 min) is
// torn down and removed from directLinks, and its validated_peer_links entry
// (keyed by link_id) is cleared. Mirrors Python LXMRouter.clean_links
// (LXMRouter.py:913-927). The link is driven stale by injecting a future
// clock; the watchdog uses real time so it does not tear the link down first.
func TestCleanLinksDirectLinksRemovesStale(t *testing.T) {
	t.Parallel()

	routerTs := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, routerTs, nil, testutils.TempDir(t, tempDirPrefix))

	link, destHash := establishTestLinkForCleanLinks(t)
	if link.GetStatus() != rns.LinkActive {
		t.Fatalf("link not active: %v", link.GetStatus())
	}

	// Register the link in directLinks and mark its link_id as a validated
	// peer link, mirroring the state Python's clean_links sweeps.
	router.RegisterDirectLink(destHash, link)
	linkID := append([]byte(nil), link.GetHash()...)
	router.mu.Lock()
	router.validatedPeerLinks[string(linkID)] = true
	router.mu.Unlock()

	// Inject a clock 15 min in the future => no_data_for > 10 min => stale.
	link.SetNowForTest(func() time.Time { return time.Now().Add(15 * time.Minute) })
	if got := link.NoDataFor(); got <= LinkMaxInactivity {
		t.Fatalf("precondition NoDataFor = %v, want > %v", got, LinkMaxInactivity)
	}

	router.CleanLinks()

	router.mu.Lock()
	_, dPresent := router.directLinks[string(destHash)]
	_, vPresent := router.validatedPeerLinks[string(linkID)]
	router.mu.Unlock()
	if dPresent {
		t.Fatal("stale direct link still present in directLinks after CleanLinks")
	}
	if vPresent {
		t.Fatal("stale direct link still present in validatedPeerLinks after CleanLinks")
	}
	if link.GetStatus() != rns.LinkClosed {
		t.Fatalf("stale link status = %v, want LinkClosed after teardown", link.GetStatus())
	}
}

// TestCleanLinksDirectLinksSurvivesWhenActive verifies that an active
// direct-delivery link with recent data is NOT removed by CleanLinks. This
// pins the survivor set matching Python (inactive_time > threshold, strict).
func TestCleanLinksDirectLinksSurvivesWhenActive(t *testing.T) {
	t.Parallel()

	routerTs := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, routerTs, nil, testutils.TempDir(t, tempDirPrefix))

	link, destHash := establishTestLinkForCleanLinks(t)

	router.RegisterDirectLink(destHash, link)

	// Inject a clock only 1 min in the future => no_data_for < 10 min => kept.
	link.SetNowForTest(func() time.Time { return time.Now().Add(time.Minute) })
	if got := link.NoDataFor(); got >= LinkMaxInactivity {
		t.Fatalf("precondition NoDataFor = %v, want < %v", got, LinkMaxInactivity)
	}

	router.CleanLinks()

	router.mu.Lock()
	stored, present := router.directLinks[string(destHash)]
	router.mu.Unlock()
	if !present || stored != link {
		t.Fatal("active direct link was removed by CleanLinks, want it retained")
	}
	if link.GetStatus() == rns.LinkClosed {
		t.Fatal("active link was torn down by CleanLinks, want it retained")
	}
}

// TestCleanLinksPropagationSweep verifies that inbound propagation links
// in activePropagationLinks whose no_data_for exceeds
// LXMRouter.P_LINK_MAX_INACTIVITY (3 min) are torn down and removed, while
// active ones survive. Mirrors Python LXMRouter.clean_links
// (LXMRouter.py:929-940).
func TestCleanLinksPropagationSweep(t *testing.T) {
	t.Parallel()

	routerTs := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, routerTs, nil, testutils.TempDir(t, tempDirPrefix))

	stale, _ := establishTestLinkForCleanLinks(t)
	active, _ := establishTestLinkForCleanLinks(t)

	router.mu.Lock()
	router.activePropagationLinks = append(router.activePropagationLinks, stale, active)
	router.mu.Unlock()

	// Drive the "stale" link past P_LINK_MAX_INACTIVITY; leave "active" recent.
	stale.SetNowForTest(func() time.Time { return time.Now().Add(4 * time.Minute) })
	if got := stale.NoDataFor(); got <= PLinkMaxInactivity {
		t.Fatalf("stale NoDataFor = %v, want > %v", got, PLinkMaxInactivity)
	}
	if got := active.NoDataFor(); got >= PLinkMaxInactivity {
		t.Fatalf("active NoDataFor = %v, want < %v", got, PLinkMaxInactivity)
	}

	router.CleanLinks()

	router.mu.Lock()
	remaining := router.activePropagationLinks
	router.mu.Unlock()

	if len(remaining) != 1 {
		t.Fatalf("activePropagationLinks len = %d, want 1 (only the active link)", len(remaining))
	}
	if remaining[0] != active {
		t.Fatal("the active propagation link was removed, want it retained")
	}
	if stale.GetStatus() != rns.LinkClosed {
		t.Fatalf("stale propagation link status = %v, want LinkClosed", stale.GetStatus())
	}
	if active.GetStatus() == rns.LinkClosed {
		t.Fatal("active propagation link was torn down, want it retained")
	}
}

// TestCleanLinksSweepsStaleAcceptedOfferLinks covers the 1.1.0 delta
// (LXMRouter.py d909619): CleanLinks walks activePropagationLinks, collects
// their link_ids, and deletes any acceptedOfferLinks entry whose link is no
// longer active, so a propagation link dying mid-transfer without the
// concluded/failure callback stops counting against propagation_max_inbound
// _syncs. An orphaned entry (link not in activePropagationLinks) is reaped;
// an entry whose link is still active survives.
func TestCleanLinksSweepsStaleAcceptedOfferLinks(t *testing.T) {
	t.Parallel()

	routerTs := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, routerTs, nil, testutils.TempDir(t, tempDirPrefix))

	active, _ := establishTestLinkForCleanLinks(t)
	stale, _ := establishTestLinkForCleanLinks(t)

	activeID := append([]byte(nil), active.GetHash()...)
	staleID := append([]byte(nil), stale.GetHash()...)

	// Only the active link is registered as an inbound propagation link.
	router.mu.Lock()
	router.activePropagationLinks = append(router.activePropagationLinks, active)
	router.mu.Unlock()

	// Seed accepted-offer accounting for both links: the active link is
	// mid-transfer (TRANSFERRING); the stale link's accounting is orphaned
	// because its link is not in activePropagationLinks.
	router.acceptedOfferLinksMu.Lock()
	router.acceptedOfferLinks[string(activeID)] = OfferTransferring
	router.acceptedOfferLinks[string(staleID)] = OfferValidating
	router.acceptedOfferLinksMu.Unlock()

	// Confirm the active link is recent enough to survive the propagation sweep.
	if got := active.NoDataFor(); got >= PLinkMaxInactivity {
		t.Fatalf("active NoDataFor = %v, want < %v", got, PLinkMaxInactivity)
	}

	router.CleanLinks()

	router.acceptedOfferLinksMu.Lock()
	_, activePresent := router.acceptedOfferLinks[string(activeID)]
	_, stalePresent := router.acceptedOfferLinks[string(staleID)]
	router.acceptedOfferLinksMu.Unlock()

	if stalePresent {
		t.Fatal("orphaned acceptedOfferLinks entry for a link no longer in activePropagationLinks was not reaped by CleanLinks")
	}
	if !activePresent {
		t.Fatal("acceptedOfferLinks entry for an active propagation link was reaped by CleanLinks, want it retained")
	}
}
