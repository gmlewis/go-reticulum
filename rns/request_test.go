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

func TestRequestResponse(t *testing.T) {
	ResetTransport()
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

	// Register request handler
	receiverDest.RegisterRequestHandler("/test/path", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		return "response data: " + string(data)
	}, AllowAll, nil, true)

	// Initiator creates link to receiver
	link, _ := NewLinkWithTransport(tsInitiator, receiverDest)

	establishedInitiator := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	// 1. Establish link
	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for link establishment")
	}

	// 2. Perform request
	responseReceived := make(chan string, 1)
	_, err := link.Request("/test/path", []byte("hello"), func(rr *RequestReceipt) {
		responseReceived <- rr.Response.(string)
	}, nil, nil, 0)

	if err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-responseReceived:
		expected := "response data: hello"
		if res != expected {
			t.Errorf("expected %v, got %v", expected, res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestRequestResponseAutoCompressPolicyInlineAndResource(t *testing.T) {
	tests := []struct {
		name         string
		responseData []byte
	}{
		{name: "InlineResponse", responseData: []byte("small-inline-response")},
		{name: "ResourceResponse", responseData: bytes.Repeat([]byte("R"), 4096)},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ResetTransport()

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

			receiverDest.RegisterRequestHandlerWithAutoCompressLimit(
				"/test/path",
				func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
					return tc.responseData
				},
				AllowAll,
				nil,
				true,
				ResourceAutoCompressMaxSize,
			)

			link, _ := NewLinkWithTransport(tsInitiator, receiverDest)

			establishedInitiator := make(chan bool, 1)
			link.callbacks.LinkEstablished = func(l *Link) {
				establishedInitiator <- true
			}

			if err := link.Establish(); err != nil {
				t.Fatal(err)
			}

			select {
			case <-establishedInitiator:
			case <-time.After(2 * time.Second):
				t.Fatal("Timeout waiting for link establishment")
			}

			responseReceived := make(chan []byte, 1)
			_, err := link.Request("/test/path", []byte("hello"), func(rr *RequestReceipt) {
				respBytes, ok := rr.Response.([]byte)
				if !ok {
					t.Fatalf("expected []byte response, got %T", rr.Response)
				}
				responseReceived <- respBytes
			}, nil, nil, 0)

			if err != nil {
				t.Fatal(err)
			}

			select {
			case res := <-responseReceived:
				if !bytes.Equal(res, tc.responseData) {
					t.Fatalf("response mismatch: got len=%v want len=%v", len(res), len(tc.responseData))
				}
			case <-time.After(4 * time.Second):
				t.Fatal("Timeout waiting for response")
			}
		})
	}
}
