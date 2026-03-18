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

func TestRequestResponse(t *testing.T) {
	t.Parallel()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	defer cleanup()
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

	receiverDest.RegisterRequestHandler("/test/path", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		return "response data: " + string(data)
	}, AllowAll, nil, true)

	link := mustTestNewLink(t, tsInitiator, receiverDest)

	establishedInitiator := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(60 * time.Second):
		t.Fatal("Timeout waiting for link establishment")
	}

	responseReceived := make(chan string, 1)
	_, err := link.Request("/test/path", []byte("hello"), func(rr *RequestReceipt) {
		responseReceived <- rr.Response.(string)
	}, nil, nil, 0)

	mustTest(t, err)

	select {
	case res := <-responseReceived:
		expected := "response data: hello"
		if res != expected {
			t.Errorf("expected %v, got %v", expected, res)
		}
	case <-time.After(60 * time.Second):
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
			t.Parallel()
			tsInitiator := newTestTransportSystem(t)
			tsReceiver := newTestTransportSystem(t)

			pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
			defer cleanup()
			tsInitiator.RegisterInterface(pipeInitiator)
			tsReceiver.RegisterInterface(pipeReceiver)

			receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

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

			link := mustTestNewLink(t, tsInitiator, receiverDest)

			establishedInitiator := make(chan bool, 1)
			link.callbacks.LinkEstablished = func(l *Link) {
				establishedInitiator <- true
			}

			if err := link.Establish(); err != nil {
				t.Fatal(err)
			}

			select {
			case <-establishedInitiator:
			case <-time.After(60 * time.Second):
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

			mustTest(t, err)

			select {
			case res := <-responseReceived:
				if !bytes.Equal(res, tc.responseData) {
					t.Fatalf("response mismatch: got len=%v want len=%v", len(res), len(tc.responseData))
				}
			case <-time.After(60 * time.Second):
				t.Fatal("Timeout waiting for response")
			}
		})
	}
}
