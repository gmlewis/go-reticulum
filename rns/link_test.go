// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

func TestLink(t *testing.T) {
	// Create receiver identity
	receiverID, _ := NewIdentity(true)

	// Create destination for receiver
	receiverDest, _ := NewDestination(receiverID, DestinationIn, DestinationSingle, "receiver")

	// Initiator creates link to receiver
	link, err := NewLink(receiverDest)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate link ID (derived from packet hash in practice)
	link.linkID = []byte("simulated_link_id")
	link.hash = link.linkID

	// Receiver accepts link and generates its keys
	receiverLink, _ := NewLink(receiverDest)
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
	// Create two separate transport systems
	tsInitiator := &TransportSystem{
		pathTable:    make(map[string]*PathEntry),
		packetHashes: make(map[string]time.Time),
		destinations: make([]*Destination, 0),
		pendingLinks: make([]*Link, 0),
		activeLinks:  make([]*Link, 0),
	}
	idInitiator, _ := NewIdentity(true)
	tsInitiator.identity = idInitiator

	tsReceiver := &TransportSystem{
		pathTable:    make(map[string]*PathEntry),
		packetHashes: make(map[string]time.Time),
		destinations: make([]*Destination, 0),
		pendingLinks: make([]*Link, 0),
		activeLinks:  make([]*Link, 0),
	}
	idReceiver, _ := NewIdentity(true)
	tsReceiver.identity = idReceiver

	// Connect them with pipes
	var pipeInitiator, pipeReceiver *interfaces.PipeInterface
	pipeInitiator = interfaces.NewPipeInterface("initiator", func(data []byte, iface interfaces.Interface) {
		tsInitiator.Inbound(data, iface)
	})
	pipeReceiver = interfaces.NewPipeInterface("receiver", func(data []byte, iface interfaces.Interface) {
		tsReceiver.Inbound(data, iface)
	})
	pipeInitiator.Other = pipeReceiver
	pipeReceiver.Other = pipeInitiator

	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	// Setup receiver destination
	receiverDest, _ := NewDestinationWithTransport(tsReceiver, idReceiver, DestinationIn, DestinationSingle, "receiver")
	// Registering is done by constructor

	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) {
		establishedReceiver <- l
	}

	// Initiator creates link to receiver
	link, err := NewLinkWithTransport(tsInitiator, receiverDest)
	if err != nil {
		t.Fatal(err)
	}

	establishedInitiator := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	// 1. Initiator sends LINKREQUEST
	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	// Wait for establishment on both sides
	select {
	case <-establishedInitiator:
		// Initiator side established
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for initiator link establishment")
	}

	select {
	case l := <-establishedReceiver:
		// Receiver side established
		if l.status != LinkActive {
			t.Errorf("Receiver link not active")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for receiver link establishment")
	}

	if link.status != LinkActive {
		t.Errorf("Initiator link not active")
	}
}

func TestLinkIdentification(t *testing.T) {
	receiverID, _ := NewIdentity(true)
	receiverDest, _ := NewDestination(receiverID, DestinationIn, DestinationSingle, "receiver")

	link, _ := NewLink(receiverDest)
	link.linkID = []byte("link_id")
	link.status = LinkActive // Simulate established link

	// Initiator reveals identity to receiver over link
	initiatorID, _ := NewIdentity(true)
	pubKey := initiatorID.GetPublicKey()
	signedData := append(link.linkID, pubKey...)
	signature, _ := initiatorID.Sign(signedData)

	// Receiver verifies identity
	if !initiatorID.Verify(signature, signedData) {
		t.Errorf("initiator identity verification failed")
	}
}

func TestLinkIdentifyInvalidState(t *testing.T) {
	receiverID, _ := NewIdentity(true)
	receiverDest, _ := NewDestination(receiverID, DestinationIn, DestinationSingle, "receiver")

	link, _ := NewLink(receiverDest)
	initiatorID, _ := NewIdentity(true)

	if err := link.Identify(initiatorID); err == nil {
		t.Fatal("expected invalid state error")
	}
}

func TestLinkIdentifyPacketFlow(t *testing.T) {
	SetLogLevel(LogWarning)

	tsInitiator := &TransportSystem{
		pathTable:    make(map[string]*PathEntry),
		packetHashes: make(map[string]time.Time),
		destinations: make([]*Destination, 0),
		pendingLinks: make([]*Link, 0),
		activeLinks:  make([]*Link, 0),
	}
	idInitiator, _ := NewIdentity(true)
	tsInitiator.identity = idInitiator

	tsReceiver := &TransportSystem{
		pathTable:    make(map[string]*PathEntry),
		packetHashes: make(map[string]time.Time),
		destinations: make([]*Destination, 0),
		pendingLinks: make([]*Link, 0),
		activeLinks:  make([]*Link, 0),
	}
	idReceiver, _ := NewIdentity(true)
	tsReceiver.identity = idReceiver

	var pipeInitiator, pipeReceiver *interfaces.PipeInterface
	pipeInitiator = interfaces.NewPipeInterface("initiator", func(data []byte, iface interfaces.Interface) {
		tsInitiator.Inbound(data, iface)
	})
	pipeReceiver = interfaces.NewPipeInterface("receiver", func(data []byte, iface interfaces.Interface) {
		tsReceiver.Inbound(data, iface)
	})
	pipeInitiator.Other = pipeReceiver
	pipeReceiver.Other = pipeInitiator

	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest, _ := NewDestinationWithTransport(tsReceiver, idReceiver, DestinationIn, DestinationSingle, "receiver")
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
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}

	var receiverLink *Link
	select {
	case receiverLink = <-establishedReceiver:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}

	identified := make(chan *Identity, 1)
	receiverLink.callbacks.RemoteIdentified = func(l *Link, id *Identity) {
		identified <- id
	}

	if err := link.Identify(idInitiator); err != nil {
		t.Fatalf("Identify() error: %v", err)
	}

	select {
	case id := <-identified:
		if !bytes.Equal(id.Hash, idInitiator.Hash) {
			t.Fatalf("identified hash mismatch: got %x want %x", id.Hash, idInitiator.Hash)
		}
	case <-time.After(10 * time.Second):
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
