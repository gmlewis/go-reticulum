// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

func mustTestNewLink(t *testing.T, destination *Destination) *Link {
	t.Helper()
	link, err := NewLink(destination)
	mustTest(t, err)
	return link
}

func mustTestNewLinkWithTransport(t *testing.T, ts *TransportSystem, destination *Destination) *Link {
	t.Helper()
	link, err := NewLinkWithTransport(ts, destination)
	mustTest(t, err)
	return link
}

func TestLink(t *testing.T) {
	// Create receiver identity
	receiverID := mustTestNewIdentity(t, true)

	// Create destination for receiver
	receiverDest := mustTestNewDestination(t, receiverID, DestinationIn, DestinationSingle, "receiver")

	// Initiator creates link to receiver
	link := mustTestNewLink(t, receiverDest)

	// Simulate link ID (derived from packet hash in practice)
	link.linkID = []byte("simulated_link_id")
	link.hash = link.linkID

	// Receiver accepts link and generates its keys
	receiverLink, err := NewLink(receiverDest)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := receiverLink.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(msg, decrypted) {
		t.Errorf("link encryption/decryption failed")
	}
}

func TestLinkHandshakeFull(t *testing.T) {
	SetLogLevel(LogExtreme)

	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver := newTestPipes(t, tsInitiator, tsReceiver)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	// Setup receiver destination
	receiverDest, err := NewDestinationWithTransport(tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")
	if err != nil {
		t.Fatal(err)
	}

	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) {
		establishedReceiver <- l
	}

	link, err := NewLinkWithTransport(tsInitiator, receiverDest)
	if err != nil {
		t.Fatal(err)
	}

	establishedInitiator := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for initiator link establishment")
	}

	select {
	case l := <-establishedReceiver:
		if l.status != LinkActive {
			t.Errorf("Receiver link not active")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for receiver link establishment")
	}

	if link.status != LinkActive {
		t.Errorf("Initiator link not active")
	}
}

func TestLinkIdentification(t *testing.T) {
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, receiverID, DestinationIn, DestinationSingle, "receiver")

	link := mustTestNewLink(t, receiverDest)
	link.linkID = []byte("link_id")
	link.status = LinkActive // Simulate established link

	// Initiator reveals identity to receiver over link
	initiatorID := mustTestNewIdentity(t, true)
	pubKey := initiatorID.GetPublicKey()
	signedData := append(link.linkID, pubKey...)
	signature, err := initiatorID.Sign(signedData)
	if err != nil {
		t.Fatal(err)
	}

	// Receiver verifies identity
	if !initiatorID.Verify(signature, signedData) {
		t.Errorf("initiator identity verification failed")
	}
}

func TestLinkIdentifyInvalidState(t *testing.T) {
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, receiverID, DestinationIn, DestinationSingle, "receiver")
	link := mustTestNewLink(t, receiverDest)
	initiatorID := mustTestNewIdentity(t, true)

	if err := link.Identify(initiatorID); err == nil {
		t.Fatal("expected invalid state error")
	}
}

func TestLinkIdentifyPacketFlow(t *testing.T) {
	SetLogLevel(LogWarning)

	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver := newTestPipes(t, tsInitiator, tsReceiver)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest, err := NewDestinationWithTransport(tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) {
		establishedReceiver <- l
	}

	link, err := NewLinkWithTransport(tsInitiator, receiverDest)
	if err != nil {
		t.Fatal(err)
	}

	establishedInitiator := make(chan struct{}, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- struct{}{}
	}

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}

	var receiverLink *Link
	select {
	case receiverLink = <-establishedReceiver:
	case <-time.After(30 * time.Second):
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
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for remote identification")
	}
}

func TestSetResourceStrategyValidatesInput(t *testing.T) {
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
