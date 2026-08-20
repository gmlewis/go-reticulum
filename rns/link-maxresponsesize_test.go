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

// establishMaxResponseLinkPair stands up a loopback link pair (initiator <->
// receiver) for the max-response-size tests. The receiver
// destination registers a request handler at "/p" that echoes a configurable
// response payload, so the initiator can observe how an oversized response is
// rejected versus an in-limit one. It returns the active initiator link, the
// active receiver link, and the receiver destination.
func establishMaxResponseLinkPair(t *testing.T, responsePayload []byte) (initiator, receiver *Link, receiverDest *Destination) {
	t.Helper()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)
	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	t.Cleanup(cleanup)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest = mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "maxresp")

	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) { establishedReceiver <- l }

	// Echo handler: returns the configured response payload. AutoCompress is
	// disabled so the resource advertisement's read size (adv.D) is exactly
	// the packed-response length, keeping the size gate deterministic.
	receiverDest.RegisterRequestHandler("/p",
		func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
			return responsePayload
		},
		AllowAll, nil, false,
	)

	initiator = mustTestNewLink(t, tsInitiator, receiverDest)
	t.Cleanup(initiator.Teardown)
	establishedInitiator := make(chan struct{}, 1)
	initiator.callbacks.LinkEstablished = func(*Link) { close(establishedInitiator) }

	if err := initiator.Establish(); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}
	select {
	case receiver = <-establishedReceiver:
		t.Cleanup(receiver.Teardown)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}
	return initiator, receiver, receiverDest
}

// sendMaxResponseRequest issues a "/p" request over the initiator link with the
// given max response size and returns channels that receive the terminal
// receipt status via the response and failed callbacks. The request data is
// kept tiny so the request itself stays inline (the response is what is sized).
func sendMaxResponseRequest(t *testing.T, initiator *Link, maxResponseSize int64) (response, failed chan *RequestReceipt) {
	t.Helper()
	response = make(chan *RequestReceipt, 1)
	failed = make(chan *RequestReceipt, 1)
	_, err := initiator.Request("/p", bytes.Repeat([]byte{0x00}, 10),
		func(rr *RequestReceipt) { response <- rr },
		func(rr *RequestReceipt) { failed <- rr },
		nil,
		30*time.Second,
		maxResponseSize,
	)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	return response, failed
}

// TestLinkMaxResponseSize_InlineResponseRejectedOverLimit asserts that
// when a response arrives inline (ContextResponse) whose serialized size
// exceeds the receipt's max response size, the receipt is rejected and the
// failed callback fires (Link.py:862-876, 1009-1010). A 100-byte response
// stays inline (packed response ~137 bytes < mdu~431) and serializes to a
// response_size of ~100 bytes, which exceeds the 50-byte limit.
func TestLinkMaxResponseSize_InlineResponseRejectedOverLimit(t *testing.T) {
	t.Parallel()
	initiator, _, _ := establishMaxResponseLinkPair(t, bytes.Repeat([]byte{0x01}, 100))

	response, failed := sendMaxResponseRequest(t, initiator, 50)

	select {
	case rr := <-failed:
		if got := rr.GetStatus(); got != RequestFailed {
			t.Errorf("status after rejected response = %v, want RequestFailed", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: failed callback did not fire for an over-limit inline response")
	}
	select {
	case <-response:
		t.Error("response callback fired for an over-limit inline response; want it rejected")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestLinkMaxResponseSize_InlineResponseAcceptedUnderLimit is the control for
// the rejected case: the same 100-byte inline response with a generous limit is
// delivered normally and the response callback fires (Link.py:873).
func TestLinkMaxResponseSize_InlineResponseAcceptedUnderLimit(t *testing.T) {
	t.Parallel()
	initiator, _, _ := establishMaxResponseLinkPair(t, bytes.Repeat([]byte{0x01}, 100))

	response, failed := sendMaxResponseRequest(t, initiator, 1000)

	select {
	case rr := <-response:
		if got := rr.GetStatus(); got != RequestReady {
			t.Errorf("status after accepted response = %v, want RequestReady", got)
		}
		got, ok := rr.Response.([]byte)
		if !ok || len(got) != 100 {
			t.Errorf("response = %T len=%v, want []byte len=100", rr.Response, len(got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: response callback did not fire for an under-limit inline response")
	}
	select {
	case <-failed:
		t.Error("failed callback fired for an under-limit inline response; want it accepted")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestLinkMaxResponseSize_ResourceResponseRejectedOverLimit asserts that
// when a response is large enough to be sent as a resource, the
// response-resource advertisement's read size (adv.D) is checked against the
// receipt's max response size at advertisement time; when it exceeds the limit
// the transfer is rejected and the failed callback fires
// (Link.py:1038-1056). A 500-byte response packs to ~538 bytes > mdu~431, so
// it is advertised as a resource; adv.D ~538 > 100 => rejected.
func TestLinkMaxResponseSize_ResourceResponseRejectedOverLimit(t *testing.T) {
	t.Parallel()
	initiator, _, _ := establishMaxResponseLinkPair(t, bytes.Repeat([]byte{0x02}, 500))

	response, failed := sendMaxResponseRequest(t, initiator, 100)

	select {
	case rr := <-failed:
		if got := rr.GetStatus(); got != RequestFailed {
			t.Errorf("status after rejected response resource = %v, want RequestFailed", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout: failed callback did not fire for an over-limit response resource")
	}
	select {
	case <-response:
		t.Error("response callback fired for an over-limit response resource; want it rejected")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestLinkMaxResponseSize_ResourceResponseAcceptedUnlimited is the control for
// the rejected resource case: with no limit set (0 = unlimited), the same
// 500-byte response resource is accepted and delivered, firing the response
// callback — proving the rejection above is due to the size limit, not the
// test setup.
func TestLinkMaxResponseSize_ResourceResponseAcceptedUnlimited(t *testing.T) {
	t.Parallel()
	initiator, _, _ := establishMaxResponseLinkPair(t, bytes.Repeat([]byte{0x02}, 500))

	response, failed := sendMaxResponseRequest(t, initiator, 0)

	select {
	case rr := <-response:
		if got := rr.GetStatus(); got != RequestReady {
			t.Errorf("status after accepted response resource = %v, want RequestReady", got)
		}
		got, ok := rr.Response.([]byte)
		if !ok || len(got) != 500 {
			t.Errorf("response = %T len=%v, want []byte len=500", rr.Response, len(got))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout: response callback did not fire for an unlimited response resource")
	}
	select {
	case <-failed:
		t.Error("failed callback fired for an unlimited response resource; want it accepted")
	case <-time.After(200 * time.Millisecond):
	}
}
