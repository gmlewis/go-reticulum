// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// setupTwoRouterPipeNetwork creates two LXMF Routers connected via
// PipeInterface pair. Returns both routers, both delivery destinations,
// both transport systems, and a cleanup function.
func setupTwoRouterPipeNetwork(t *testing.T) (routerA, routerB *Router, destA, destB *rns.Destination, tsA, tsB *rns.TransportSystem, cleanup func()) {
	t.Helper()

	dirA := testutils.TempDir(t, "lxmf-int-a")
	dirB := testutils.TempDir(t, "lxmf-int-b")

	tsA = rns.NewTransportSystem(nil)
	if err := tsA.Start(dirA + "/rns"); err != nil {
		t.Fatalf("tsA.Start: %v", err)
	}
	tsB = rns.NewTransportSystem(nil)
	if err := tsB.Start(dirB + "/rns"); err != nil {
		t.Fatalf("tsB.Start: %v", err)
	}

	pipeA := interfaces.NewPipeInterface("a", func(data []byte, iface interfaces.Interface) {
		tsA.Inbound(data, iface)
	})
	pipeB := interfaces.NewPipeInterface("b", func(data []byte, iface interfaces.Interface) {
		tsB.Inbound(data, iface)
	})
	pipeA.SetOther(pipeB)
	pipeB.SetOther(pipeA)
	tsA.RegisterInterface(pipeA)
	tsB.RegisterInterface(pipeB)

	routerA, err := NewRouter(tsA, tsA.Identity(), dirA)
	if err != nil {
		t.Fatalf("NewRouter A: %v", err)
	}
	routerB, err = NewRouter(tsB, tsB.Identity(), dirB)
	if err != nil {
		t.Fatalf("NewRouter B: %v", err)
	}

	destA, err = routerA.RegisterDeliveryIdentity(tsA.Identity(), "Alice", nil)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity A: %v", err)
	}
	destB, err = routerB.RegisterDeliveryIdentity(tsB.Identity(), "Bob", nil)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity B: %v", err)
	}

	cleanup = func() {
		routerA.Close()
		routerB.Close()
		tsA.Stop()
		tsB.Stop()
		_ = pipeA.Detach()
		_ = pipeB.Detach()
	}

	return
}

// TestIntegrationRouterTwoNodeOpportunistic tests that two LXMF Routers
// connected via PipeInterface can exchange an opportunistic (single-packet,
// no link) message end-to-end. This exercises the full transport path:
// HandleOutbound → ProcessOutbound → sendMessagePacketLocked →
// packet.Send → tsA.Outbound → pipe → tsB.Inbound → destB.receive →
// deliveryPacket → handleInboundMessage → deliveryCallback.
func TestIntegrationRouterTwoNodeOpportunistic(t *testing.T) {
	testutils.SkipShortIntegration(t)

	routerA, routerB, destA, destB, tsA, tsB, cleanup := setupTwoRouterPipeNetwork(t)
	defer cleanup()

	receivedCh := make(chan *Message, 1)
	routerB.RegisterDeliveryCallback(func(msg *Message) {
		select {
		case receivedCh <- msg:
		default:
		}
	})

	if err := routerA.Announce(destA.Hash); err != nil {
		t.Fatalf("Announce A: %v", err)
	}
	if err := routerB.Announce(destB.Hash); err != nil {
		t.Fatalf("Announce B: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tsA.HasPath(destB.Hash) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !tsA.HasPath(destB.Hash) {
		t.Fatal("timed out waiting for path A->B after announce")
	}

	outDestB, err := rns.NewDestination(tsA, tsB.Identity(), rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination OUT B: %v", err)
	}

	msg, err := NewMessage(outDestB, destA, "hello from A", "test title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.DesiredMethod = MethodOpportunistic
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if err := routerA.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	select {
	case got := <-receivedCh:
		if got.TitleString() != "test title" {
			t.Errorf("title = %q, want %q", got.TitleString(), "test title")
		}
		if got.ContentString() != "hello from A" {
			t.Errorf("content = %q, want %q", got.ContentString(), "hello from A")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for message delivery via opportunistic method")
	}
}

// TestIntegrationRouterTwoNodeDirect tests that two LXMF Routers connected
// via PipeInterface can exchange a direct (link-based) message end-to-end.
func TestIntegrationRouterTwoNodeDirect(t *testing.T) {
	testutils.SkipShortIntegration(t)

	routerA, routerB, destA, destB, tsA, _, cleanup := setupTwoRouterPipeNetwork(t)
	defer cleanup()

	receivedCh := make(chan *Message, 1)
	routerB.RegisterDeliveryCallback(func(msg *Message) {
		select {
		case receivedCh <- msg:
		default:
		}
	})

	if err := routerA.Announce(destA.Hash); err != nil {
		t.Fatalf("Announce A: %v", err)
	}
	if err := routerB.Announce(destB.Hash); err != nil {
		t.Fatalf("Announce B: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tsA.HasPath(destB.Hash) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !tsA.HasPath(destB.Hash) {
		t.Fatal("timed out waiting for path A->B after announce")
	}

	msg, err := NewMessage(destB, destA, "hello direct", "direct title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.DesiredMethod = MethodDirect
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if err := routerA.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	select {
	case got := <-receivedCh:
		if got.TitleString() != "direct title" {
			t.Errorf("title = %q, want %q", got.TitleString(), "direct title")
		}
		if got.ContentString() != "hello direct" {
			t.Errorf("content = %q, want %q", got.ContentString(), "hello direct")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for message delivery via direct method")
	}
}

// TestIntegrationRouterTwoNodeDirectLargeMessage tests that two LXMF
// Routers connected via PipeInterface can exchange a large direct message
// that exceeds the single-packet MDU and must be sent as a resource over
// a link. This exercises link establishment, resource transfer, and
// delivery callback through the PipeInterface.
func TestIntegrationRouterTwoNodeDirectLargeMessage(t *testing.T) {
	testutils.SkipShortIntegration(t)

	routerA, routerB, destA, destB, tsA, tsB, cleanup := setupTwoRouterPipeNetwork(t)
	defer cleanup()

	receivedCh := make(chan *Message, 1)
	routerB.RegisterDeliveryCallback(func(msg *Message) {
		select {
		case receivedCh <- msg:
		default:
		}
	})

	if err := routerA.Announce(destA.Hash); err != nil {
		t.Fatalf("Announce A: %v", err)
	}
	if err := routerB.Announce(destB.Hash); err != nil {
		t.Fatalf("Announce B: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tsA.HasPath(destB.Hash) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !tsA.HasPath(destB.Hash) {
		t.Fatal("timed out waiting for path A->B after announce")
	}

	outDestB, err := rns.NewDestination(tsA, tsB.Identity(), rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination OUT B: %v", err)
	}

	largeContent := strings.Repeat("X", rns.MDU*2)
	msg, err := NewMessage(outDestB, destA, largeContent, "large direct title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.DesiredMethod = MethodDirect
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if err := routerA.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	select {
	case got := <-receivedCh:
		if got.TitleString() != "large direct title" {
			t.Errorf("title = %q, want %q", got.TitleString(), "large direct title")
		}
		if got.ContentString() != largeContent {
			t.Errorf("content length = %d, want %d", len(got.ContentString()), len(largeContent))
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for large message delivery via direct method")
	}
}
