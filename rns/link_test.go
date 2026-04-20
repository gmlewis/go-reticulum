// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func TestLinkMDUCalculation(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")
	link := mustTestNewLink(t, ts, receiverDest)

	tests := []struct {
		mtu  int
		want int
	}{
		{mtu: 500, want: 431},
		{mtu: 400, want: 319},
		{mtu: 100, want: 31},
	}

	for _, tt := range tests {
		link.mtu = tt.mtu
		link.UpdateMDU()

		expected := int(math.Floor(float64(tt.mtu-IFACMinSize-HeaderMinSize-TokenOverhead)/float64(AES128BlockSize)))*AES128BlockSize - 1
		if expected != tt.want {
			t.Fatalf("test expectation mismatch for mtu=%v: expected=%v want=%v", tt.mtu, expected, tt.want)
		}
		if link.mdu != tt.want {
			t.Fatalf("UpdateMDU() with mtu=%v set mdu=%v, want %v", tt.mtu, link.mdu, tt.want)
		}
	}
}

func TestLinkHandleRTTDecryptsAndUpdatesKeepalive(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")

	initiator := mustTestNewLink(t, ts, receiverDest)
	receiver := mustTestNewLink(t, ts, receiverDest)
	receiver.initiator = false

	initiator.linkID = []byte("simulated_link_id")
	initiator.hash = initiator.linkID
	receiver.linkID = initiator.linkID
	receiver.hash = initiator.linkID

	if err := initiator.LoadPeer(receiver.pubBytes, receiver.sigPubBytes); err != nil {
		t.Fatalf("initiator LoadPeer failed: %v", err)
	}
	if err := receiver.LoadPeer(initiator.pubBytes, initiator.sigPubBytes); err != nil {
		t.Fatalf("receiver LoadPeer failed: %v", err)
	}
	if err := initiator.Handshake(); err != nil {
		t.Fatalf("initiator handshake failed: %v", err)
	}
	if err := receiver.Handshake(); err != nil {
		t.Fatalf("receiver handshake failed: %v", err)
	}

	receiver.status = LinkHandshake
	receiver.requestTime = time.Now().Add(-150 * time.Millisecond)

	rttData, err := msgpack.Pack(2.0)
	if err != nil {
		t.Fatalf("Pack RTT: %v", err)
	}
	encrypted, err := initiator.Encrypt(rttData)
	if err != nil {
		t.Fatalf("Encrypt RTT: %v", err)
	}

	receiver.HandleRTT(&Packet{Data: encrypted})

	if receiver.status != LinkActive {
		t.Fatalf("receiver status=%v want=%v", receiver.status, LinkActive)
	}
	if receiver.rtt < 2.0 {
		t.Fatalf("receiver RTT=%v want >= 2.0", receiver.rtt)
	}
	if receiver.keepalive != LinkKeepaliveMax {
		t.Fatalf("receiver keepalive=%v want=%v", receiver.keepalive, LinkKeepaliveMax)
	}
	if receiver.staleTime != time.Duration(LinkStaleFactor)*LinkKeepaliveMax {
		t.Fatalf("receiver staleTime=%v want=%v", receiver.staleTime, time.Duration(LinkStaleFactor)*LinkKeepaliveMax)
	}
}

func TestLinkWatchdogPendingTimeout(t *testing.T) {
	t.Parallel()

	link := &Link{
		status:               LinkPending,
		requestTime:          time.Now().Add(-2 * time.Second),
		establishmentTimeout: time.Second,
		watchdogStop:         make(chan struct{}),
	}

	sleep := link.watchdogStep(time.Now())

	if link.status != LinkClosed {
		t.Fatalf("link status=%v want=%v", link.status, LinkClosed)
	}
	if link.teardownReason != TeardownTimeout {
		t.Fatalf("link teardownReason=%v want=%v", link.teardownReason, TeardownTimeout)
	}
	if sleep != time.Millisecond {
		t.Fatalf("watchdog sleep=%v want=%v", sleep, time.Millisecond)
	}
}

func TestLinkWatchdogActiveMarksStaleAndSendsKeepalive(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")
	link := mustTestNewLink(t, ts, receiverDest)
	iface := &capturingInterface{name: "capture"}

	link.initiator = true
	link.status = LinkActive
	link.linkID = []byte("watchdog_link_id")
	link.hash = link.linkID
	link.attachedInterface = iface
	link.keepalive = 5 * time.Second
	link.staleTime = 10 * time.Second
	link.activatedAt = time.Now().Add(-11 * time.Second)

	sleep := link.watchdogStep(time.Now())

	if link.status != LinkStale {
		t.Fatalf("link status=%v want=%v", link.status, LinkStale)
	}
	if iface.sendCount != 1 {
		t.Fatalf("keepalive sendCount=%v want=1", iface.sendCount)
	}
	if sleep != LinkStaleGrace {
		t.Fatalf("watchdog sleep=%v want=%v", sleep, LinkStaleGrace)
	}
}

func TestLinkWatchdogStaleClosesWithTimeout(t *testing.T) {
	t.Parallel()

	link := &Link{
		status:       LinkStale,
		watchdogStop: make(chan struct{}),
	}

	sleep := link.watchdogStep(time.Now())

	if link.status != LinkClosed {
		t.Fatalf("link status=%v want=%v", link.status, LinkClosed)
	}
	if link.teardownReason != TeardownTimeout {
		t.Fatalf("link teardownReason=%v want=%v", link.teardownReason, TeardownTimeout)
	}
	if sleep != time.Millisecond {
		t.Fatalf("watchdog sleep=%v want=%v", sleep, time.Millisecond)
	}
}

func TestLink(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	// Create receiver identity
	receiverID := mustTestNewIdentity(t, true)

	// Create destination for receiver
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")

	// Initiator creates link to receiver
	link := mustTestNewLink(t, ts, receiverDest)

	// Simulate link ID (derived from packet hash in practice)
	link.linkID = []byte("simulated_link_id")
	link.hash = link.linkID

	// Receiver accepts link and generates its keys
	receiverLink := mustTestNewLink(t, ts, receiverDest)
	receiverLink.initiator = false
	receiverLink.linkID = link.linkID
	receiverLink.hash = link.linkID

	// Exchange public keys
	if err := link.LoadPeer(receiverLink.pubBytes, receiverLink.sigPubBytes); err != nil {
		t.Fatalf("initiator LoadPeer failed: %v", err)
	}
	if err := receiverLink.LoadPeer(link.pubBytes, link.sigPubBytes); err != nil {
		t.Fatalf("receiver LoadPeer failed: %v", err)
	}

	// Perform handshake on both sides
	if err := link.Handshake(); err != nil {
		t.Fatalf("initiator handshake failed: %v", err)
	}
	if err := receiverLink.Handshake(); err != nil {
		t.Fatalf("receiver handshake failed: %v", err)
	}

	// Verify session keys match
	if !bytes.Equal(link.derivedKey, receiverLink.derivedKey) {
		t.Errorf("derived session keys mismatch")
	}

	// Test encrypted communication over link
	msg := []byte("secret link message")
	encrypted, err := link.Encrypt(msg)
	mustTest(t, err)

	decrypted, err := receiverLink.Decrypt(encrypted)
	mustTest(t, err)

	if !bytes.Equal(msg, decrypted) {
		t.Errorf("link encryption/decryption failed")
	}
}

func TestLinkHandshakeFull(t *testing.T) {
	t.Parallel()

	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	defer cleanup()
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	// Setup receiver destination
	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) {
		establishedReceiver <- l
	}

	link := mustTestNewLink(t, tsInitiator, receiverDest)
	t.Cleanup(link.Teardown)

	establishedInitiator := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for initiator link establishment")
	}

	select {
	case l := <-establishedReceiver:
		t.Cleanup(l.Teardown)
		if l.status != LinkActive {
			t.Errorf("Receiver link not active")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for receiver link establishment")
	}

	if link.status != LinkActive {
		t.Errorf("Initiator link not active")
	}
}

func TestLinkIdentification(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")

	link := mustTestNewLink(t, ts, receiverDest)
	link.linkID = []byte("link_id")
	link.status = LinkActive // Simulate established link

	// Initiator reveals identity to receiver over link
	initiatorID := mustTestNewIdentity(t, true)
	pubKey := initiatorID.GetPublicKey()
	signedData := append(link.linkID, pubKey...)
	signature, err := initiatorID.Sign(signedData)
	mustTest(t, err)

	// Receiver verifies identity
	if !initiatorID.Verify(signature, signedData) {
		t.Errorf("initiator identity verification failed")
	}
}

func TestLinkIdentifyInvalidState(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")
	link := mustTestNewLink(t, ts, receiverDest)
	initiatorID := mustTestNewIdentity(t, true)

	if err := link.Identify(initiatorID); err == nil {
		t.Fatal("expected invalid state error")
	}
}

func TestLinkIdentifyPacketFlow(t *testing.T) {
	t.Parallel()

	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	defer cleanup()
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")
	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) {
		establishedReceiver <- l
	}

	link := mustTestNewLink(t, tsInitiator, receiverDest)
	t.Cleanup(link.Teardown)

	establishedInitiator := make(chan struct{}, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- struct{}{}
	}

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}

	var receiverLink *Link
	select {
	case receiverLink = <-establishedReceiver:
		t.Cleanup(receiverLink.Teardown)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}

	identified := make(chan *Identity, 1)
	receiverLink.callbacks.RemoteIdentified = func(l *Link, id *Identity) {
		identified <- id
	}

	if err := link.Identify(tsInitiator.identity); err != nil {
		t.Fatalf("Identify() error: %v", err)
	}

	select {
	case id := <-identified:
		if !bytes.Equal(id.Hash, tsInitiator.identity.Hash) {
			t.Fatalf("identified hash mismatch: got %x want %x", id.Hash, tsInitiator.identity.Hash)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for remote identification")
	}
}

func TestLinkIdentifyWaitsForReceiverEstablishedCallback(t *testing.T) {
	t.Parallel()

	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	defer cleanup()
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")
	allowReceiverSetup := make(chan struct{})
	identified := make(chan *Identity, 1)
	receiverDest.SetLinkEstablishedCallback(func(l *Link) {
		<-allowReceiverSetup
		l.SetRemoteIdentifiedCallback(func(_ *Link, id *Identity) {
			identified <- id
		})
	})

	link := mustTestNewLink(t, tsInitiator, receiverDest)
	t.Cleanup(link.Teardown)
	establishedInitiator := make(chan struct{}, 1)
	link.SetLinkEstablishedCallback(func(*Link) {
		establishedInitiator <- struct{}{}
	})

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}

	if err := link.Identify(tsInitiator.identity); err != nil {
		t.Fatalf("Identify() error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	close(allowReceiverSetup)

	select {
	case id := <-identified:
		if !bytes.Equal(id.Hash, tsInitiator.identity.Hash) {
			t.Fatalf("identified hash mismatch: got %x want %x", id.Hash, tsInitiator.identity.Hash)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for receiver remote identification")
	}
}

func TestSetResourceStrategyValidatesInput(t *testing.T) {
	t.Parallel()
	l := &Link{}

	if err := l.SetResourceStrategy(AcceptAll); err != nil {
		t.Fatalf("SetResourceStrategy(AcceptAll): %v", err)
	}
	if l.resourceStrategy != AcceptAll {
		t.Fatalf("resourceStrategy=%v want=%v", l.resourceStrategy, AcceptAll)
	}

	if err := l.SetResourceStrategy(99); err == nil {
		t.Fatal("expected invalid strategy error")
	}
}
