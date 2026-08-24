// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"net"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// setupTwoRouterTCPNetwork creates two LXMF Routers connected via a TCP
// transport (server on B, client on A), mirroring the real-world gonomadnet
// network path that B11 reported as broken. Returns both routers, both
// delivery destinations, both transport systems, and a cleanup function.
func setupTwoRouterTCPNetwork(t *testing.T) (routerA, routerB *Router, destA, destB *rns.Destination, tsA, tsB *rns.TransportSystem, cleanup func()) {
	t.Helper()

	dirA := testutils.TempDir(t, "lxmf-tcp-a")
	dirB := testutils.TempDir(t, "lxmf-tcp-b")

	tsA = rns.NewTransportSystem(nil)
	if err := tsA.Start(dirA + "/rns"); err != nil {
		t.Fatalf("tsA.Start: %v", err)
	}
	tsB = rns.NewTransportSystem(nil)
	if err := tsB.Start(dirB + "/rns"); err != nil {
		t.Fatalf("tsB.Start: %v", err)
	}

	// Reserve a TCP port for the test.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	// B is the TCP server.
	serverB, err := interfaces.NewTCPServerInterface("server-b", "127.0.0.1", port, func(data []byte, iface interfaces.Interface) {
		tsB.Inbound(data, iface)
	}, nil)
	if err != nil {
		t.Fatalf("NewTCPServerInterface B: %v", err)
	}
	tsB.RegisterInterface(serverB)

	// A is the TCP client, connects to B.
	clientA, err := interfaces.NewTCPClientInterface("client-a", "127.0.0.1", port, false, func(data []byte, iface interfaces.Interface) {
		tsA.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewTCPClientInterface A: %v", err)
	}
	tsA.RegisterInterface(clientA)

	// Wait for the TCP connection to establish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if clientA.Status() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !clientA.Status() {
		t.Fatal("TCP client A did not connect to server B")
	}

	routerA, err = NewRouter(tsA, tsA.Identity(), dirA)
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
		_ = clientA.Detach()
		_ = serverB.Detach()
	}

	return
}

// TestB11TCPDirectDelivery reproduces B11: two gonomadnet (Go) instances
// connected via TCP transport must exchange a direct (link-based) LXMF
// message end-to-end. Before the fix, the sender showed a sent status but
// the message never arrived at the receiver.
func TestB11TCPDirectDelivery(t *testing.T) {
	testutils.SkipShortIntegration(t)

	routerA, routerB, destA, destB, tsA, _, cleanup := setupTwoRouterTCPNetwork(t)
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

	// Wait for A to learn a path to B (announce propagation).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if tsA.HasPath(destB.Hash) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !tsA.HasPath(destB.Hash) {
		t.Fatal("timed out waiting for path A->B after announce (B6/B11: announce not propagated)")
	}

	msg, err := NewMessage(destB, destA, "hello via TCP direct", "tcp title", nil)
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
		if got.TitleString() != "tcp title" {
			t.Errorf("title = %q, want %q", got.TitleString(), "tcp title")
		}
		if got.ContentString() != "hello via TCP direct" {
			t.Errorf("content = %q, want %q", got.ContentString(), "hello via TCP direct")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("B11: timed out waiting for direct message delivery via TCP")
	}
}
