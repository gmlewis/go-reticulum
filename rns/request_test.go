// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
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
	case <-time.After(10 * time.Second):
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
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestRequestResponseAutoCompressPolicyInlineAndResource(t *testing.T) {
	t.Parallel()
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
			case <-time.After(10 * time.Second):
				t.Fatal("Timeout waiting for response")
			}
		})
	}
}

func TestRequestResponseResourceProgressCallback(t *testing.T) {
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
			return bytes.Repeat([]byte("R"), 4096)
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
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for link establishment")
	}

	progressObserved := make(chan int, 1)
	responseReceived := make(chan struct{}, 1)
	_, err := link.Request(
		"/test/path",
		[]byte("hello"),
		func(rr *RequestReceipt) {
			responseReceived <- struct{}{}
		},
		nil,
		func(rr *RequestReceipt) {
			select {
			case progressObserved <- rr.GetStatus():
			default:
			}
		},
		0,
	)

	mustTest(t, err)

	select {
	case status := <-progressObserved:
		if status != RequestReceiving {
			t.Fatalf("progress callback status = %v, want %v", status, RequestReceiving)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for request progress callback")
	}

	select {
	case <-responseReceived:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestRequestReceiptStoresMetadata(t *testing.T) {
	t.Parallel()

	metadata := map[string][]byte{
		"name": []byte("example.txt"),
	}
	rr := &RequestReceipt{}

	rr.responseReceived([]byte("payload"), metadata)

	if got, want := rr.GetStatus(), RequestReady; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
	if got, want := rr.Response.([]byte), []byte("payload"); !bytes.Equal(got, want) {
		t.Fatalf("response = %q, want %q", got, want)
	}
	got, ok := rr.Metadata.(map[string][]byte)
	if !ok {
		t.Fatalf("metadata type = %T, want map[string][]byte", rr.Metadata)
	}
	if !bytes.Equal(got["name"], metadata["name"]) {
		t.Fatalf("metadata[name] = %q, want %q", got["name"], metadata["name"])
	}
}

func TestLinkResponseMetadata(t *testing.T) {
	t.Parallel()

	requestID := []byte("request-id")
	metadata := map[string][]byte{
		"name": []byte("inline.txt"),
	}
	rr := &RequestReceipt{RequestID: requestID}
	link := &Link{
		logger:          NewLogger(),
		status:          LinkActive,
		pendingRequests: []*RequestReceipt{rr},
	}

	link.handleResponse(requestID, []byte("inline"), metadata)

	got, ok := rr.Metadata.(map[string][]byte)
	if !ok {
		t.Fatalf("metadata type = %T, want map[string][]byte", rr.Metadata)
	}
	if !bytes.Equal(got["name"], metadata["name"]) {
		t.Fatalf("metadata[name] = %q, want %q", got["name"], metadata["name"])
	}
	if len(link.pendingRequests) != 0 {
		t.Fatalf("pendingRequests len = %v, want 0", len(link.pendingRequests))
	}
}

func TestResourceResponseMetadata(t *testing.T) {
	t.Parallel()

	requestID := []byte("resource-request-id")
	metadata := map[string][]byte{
		"name": []byte("resource.bin"),
	}
	packedResponse, err := msgpack.Pack([]any{requestID, []byte("resource-response")})
	if err != nil {
		t.Fatalf("failed to pack response: %v", err)
	}
	rr := &RequestReceipt{RequestID: requestID}
	link := &Link{
		logger:          NewLogger(),
		status:          LinkActive,
		pendingRequests: []*RequestReceipt{rr},
	}
	resource := &Resource{
		link:     link,
		status:   ResourceStatusComplete,
		data:     packedResponse,
		metadata: metadata,
	}

	link.responseResourceConcluded(resource)

	got, ok := rr.Metadata.(map[string][]byte)
	if !ok {
		t.Fatalf("metadata type = %T, want map[string][]byte", rr.Metadata)
	}
	if !bytes.Equal(got["name"], metadata["name"]) {
		t.Fatalf("metadata[name] = %q, want %q", got["name"], metadata["name"])
	}
}
