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

// TestRouterCloseTeardownGolden is a golden test: it constructs a
// router with a live delivery destination (plus an established inbound delivery
// link), a propagation destination and a propagation control destination (each
// with their request handlers and link/packet callbacks registered), and an
// active propagation link, then calls Close and asserts the teardown half of
// Python's LXMRouter.exit_handler (LXMRouter.py:1311-1359) happened:
//
//   - Every delivery destination's packet and link-established callbacks are
//     cleared and its active links are torn down (status LinkClosed).
//   - The propagation destination's link-established and packet callbacks are
//     cleared and the offer/message_get request handlers are deregistered.
//   - The propagation control destination's stats/sync/unpeer request handlers
//     are deregistered.
//   - Every link in activePropagationLinks is torn down (status LinkClosed).
func TestRouterCloseTeardownGolden(t *testing.T) {
	t.Parallel()

	// The router's own transport system, started so real inbound links can be
	// established to its delivery destination.
	routerTs := rns.NewTransportSystem(nil)
	routerDir := testutils.TempDir(t, tempDirPrefix)
	if err := routerTs.Start(routerDir); err != nil {
		t.Fatalf("routerTs.Start: %v", err)
	}
	t.Cleanup(routerTs.Stop)

	router := mustTestNewRouter(t, routerTs, nil, testutils.TempDir(t, tempDirPrefix))

	// --- Delivery destination + inbound delivery link ---
	deliveryID := mustTestNewIdentity(t, true)
	deliveryDest, err := router.RegisterDeliveryIdentity(deliveryID, "", nil)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}
	if deliveryDest.PacketCallback() == nil {
		t.Fatal("delivery dest packet callback not set after registration")
	}
	if deliveryDest.LinkEstablishedCallback() == nil {
		t.Fatal("delivery dest link-established callback not set after registration")
	}

	// Wire a remote transport system to the router via a pipe pair and
	// establish a real link to the delivery destination, mirroring
	// establishTestLinkForCleanLinks but with the router as the receiver.
	tsA := rns.NewTransportSystem(nil)
	dirA := testutils.TempDir(t, tempDirPrefix)
	if err := tsA.Start(dirA); err != nil {
		t.Fatalf("tsA.Start: %v", err)
	}
	t.Cleanup(tsA.Stop)

	pipeA := interfaces.NewPipeInterface("a", func(data []byte, iface interfaces.Interface) {
		tsA.Inbound(data, iface)
	})
	pipeR := interfaces.NewPipeInterface("r", func(data []byte, iface interfaces.Interface) {
		routerTs.Inbound(data, iface)
	})
	pipeA.SetOther(pipeR)
	pipeR.SetOther(pipeA)
	t.Cleanup(func() { _ = pipeA.Detach() })
	t.Cleanup(func() { _ = pipeR.Detach() })
	tsA.RegisterInterface(pipeA)
	routerTs.RegisterInterface(pipeR)

	initiatorLink, err := rns.NewLink(tsA, deliveryDest)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	t.Cleanup(initiatorLink.Teardown)
	establishedInitiator := make(chan struct{}, 1)
	initiatorLink.SetLinkEstablishedCallback(func(*rns.Link) { establishedInitiator <- struct{}{} })
	if err := initiatorLink.Establish(); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator delivery link establishment")
	}

	// Wait for the router's linkEstablished callback to record the inbound
	// delivery link (mirroring Python's delivery_destination.links).
	routerDeliveryLink := waitForDeliveryLink(t, router)
	if routerDeliveryLink.GetStatus() != rns.LinkActive {
		t.Fatalf("recorded delivery link not active: %v", routerDeliveryLink.GetStatus())
	}

	// --- Propagation + control destinations with handlers ---
	propagationDest, err := router.RegisterPropagationDestination()
	if err != nil {
		t.Fatalf("RegisterPropagationDestination: %v", err)
	}
	if !propagationDest.HasRequestHandler(offerRequestPath) {
		t.Fatal("propagation dest missing offer handler before Close")
	}
	if !propagationDest.HasRequestHandler(messageGetPath) {
		t.Fatal("propagation dest missing message_get handler before Close")
	}
	if propagationDest.PacketCallback() == nil {
		t.Fatal("propagation dest packet callback not set before Close")
	}
	if propagationDest.LinkEstablishedCallback() == nil {
		t.Fatal("propagation dest link-established callback not set before Close")
	}

	controlDest, err := router.RegisterPropagationControlDestination(nil)
	if err != nil {
		t.Fatalf("RegisterPropagationControlDestination: %v", err)
	}
	if !controlDest.HasRequestHandler(statsGetPath) {
		t.Fatal("control dest missing stats handler before Close")
	}
	if !controlDest.HasRequestHandler(peerSyncPath) {
		t.Fatal("control dest missing sync handler before Close")
	}
	if !controlDest.HasRequestHandler(peerUnpeerPath) {
		t.Fatal("control dest missing unpeer handler before Close")
	}

	// Enable propagation so Close persists peers (matching exit_handler's
	// `if self.propagation_node` guard) and compileStats is valid.
	router.mu.Lock()
	router.propagationEnabled = true
	router.propagationNodeStart = router.now()
	router.mu.Unlock()

	// --- Active propagation link (real, active) ---
	propLink, _ := establishTestLinkForCleanLinks(t)
	if propLink.GetStatus() != rns.LinkActive {
		t.Fatalf("propagation link not active: %v", propLink.GetStatus())
	}
	router.mu.Lock()
	router.activePropagationLinks = append(router.activePropagationLinks, propLink)
	router.mu.Unlock()

	// --- Close ---
	if err := router.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Delivery destination callbacks cleared.
	if cb := deliveryDest.PacketCallback(); cb != nil {
		t.Fatal("delivery dest packet callback not cleared after Close")
	}
	if cb := deliveryDest.LinkEstablishedCallback(); cb != nil {
		t.Fatal("delivery dest link-established callback not cleared after Close")
	}
	// Delivery links torn down.
	router.mu.Lock()
	deliveryLinks := append([]*rns.Link(nil), router.deliveryLinks...)
	router.mu.Unlock()
	if len(deliveryLinks) == 0 {
		t.Fatal("no delivery links recorded")
	}
	for i, l := range deliveryLinks {
		if l.GetStatus() != rns.LinkClosed {
			t.Fatalf("delivery link %d status=%v want LinkClosed", i, l.GetStatus())
		}
	}

	// Propagation destination callbacks cleared + offer/message_get handlers
	// deregistered.
	if cb := propagationDest.PacketCallback(); cb != nil {
		t.Fatal("propagation dest packet callback not cleared after Close")
	}
	if cb := propagationDest.LinkEstablishedCallback(); cb != nil {
		t.Fatal("propagation dest link-established callback not cleared after Close")
	}
	if propagationDest.HasRequestHandler(offerRequestPath) {
		t.Fatal("offer handler still registered after Close")
	}
	if propagationDest.HasRequestHandler(messageGetPath) {
		t.Fatal("message_get handler still registered after Close")
	}

	// Control destination stats/sync/unpeer handlers deregistered.
	if controlDest.HasRequestHandler(statsGetPath) {
		t.Fatal("stats handler still registered after Close")
	}
	if controlDest.HasRequestHandler(peerSyncPath) {
		t.Fatal("sync handler still registered after Close")
	}
	if controlDest.HasRequestHandler(peerUnpeerPath) {
		t.Fatal("unpeer handler still registered after Close")
	}

	// activePropagationLinks torn down.
	router.mu.Lock()
	propLinks := append([]*rns.Link(nil), router.activePropagationLinks...)
	router.mu.Unlock()
	for i, l := range propLinks {
		if l.GetStatus() != rns.LinkClosed {
			t.Fatalf("propagation link %d status=%v want LinkClosed", i, l.GetStatus())
		}
	}
}

// waitForDeliveryLink polls router.deliveryLinks until an inbound delivery link
// is recorded (by configureDeliveryLink, mirroring delivery_destination.links),
// failing the test after a timeout.
func waitForDeliveryLink(t *testing.T, router *Router) *rns.Link {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		router.mu.Lock()
		links := router.deliveryLinks
		router.mu.Unlock()
		if len(links) > 0 {
			return links[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for inbound delivery link to be recorded")
	return nil
}
