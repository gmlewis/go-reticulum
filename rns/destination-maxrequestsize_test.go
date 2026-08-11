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

// establishMaxRequestLinkPair stands up a loopback link pair (initiator <->
// receiver) for the Phase 8 Task 1 max-request-size tests and returns the
// active initiator link, the active receiver link, and the receiver
// destination whose request handlers and max-request size are under test.
func establishMaxRequestLinkPair(t *testing.T) (initiator, receiver *Link, receiverDest *Destination) {
	t.Helper()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)
	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	t.Cleanup(cleanup)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest = mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "maxreq")

	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) { establishedReceiver <- l }

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

// TestDestinationSetMaxRequestSizeValidation asserts Phase 8 Task 1: the
// setter mirrors Python Destination.set_max_request_size (Destination.py:369-
// 379) — zero is accepted (meaning unlimited) and a negative value is rejected
// (Python raises ValueError "Maximum request size cannot be negative").
func TestDestinationSetMaxRequestSizeValidation(t *testing.T) {
	t.Parallel()
	ts := newTestTransportSystem(t)
	dest := mustTestNewDestination(t, ts, ts.identity, DestinationIn, DestinationSingle, "validation")

	if got := dest.MaxRequestSize(); got != 0 {
		t.Errorf("default MaxRequestSize = %v, want 0 (unlimited)", got)
	}
	if err := dest.SetMaxRequestSize(0); err != nil {
		t.Errorf("SetMaxRequestSize(0) error = %v, want nil (zero is unlimited)", err)
	}
	if got := dest.MaxRequestSize(); got != 0 {
		t.Errorf("MaxRequestSize after SetMaxRequestSize(0) = %v, want 0", got)
	}
	if err := dest.SetMaxRequestSize(2048); err != nil {
		t.Errorf("SetMaxRequestSize(2048) error = %v, want nil", err)
	}
	if got := dest.MaxRequestSize(); got != 2048 {
		t.Errorf("MaxRequestSize after SetMaxRequestSize(2048) = %v, want 2048", got)
	}
	if err := dest.SetMaxRequestSize(-1); err == nil {
		t.Error("SetMaxRequestSize(-1) returned nil error, want error (negative rejected)")
	}
}

// TestDestinationMaxRequestSize_InlineRequestAcceptedUnderLimit asserts Phase
// 8 Task 1: an inline (ContextRequest) request whose decrypted packed size is
// within the destination's max-request size is delivered to the registered
// request handler (Link.py:992-997).
func TestDestinationMaxRequestSize_InlineRequestAcceptedUnderLimit(t *testing.T) {
	t.Parallel()
	initiator, _, receiverDest := establishMaxRequestLinkPair(t)

	// packed size of a request with 10 bytes of data is ~54 bytes (well under
	// mdu~431, so it stays inline) and under the 100-byte limit.
	if err := receiverDest.SetMaxRequestSize(100); err != nil {
		t.Fatalf("SetMaxRequestSize: %v", err)
	}

	seen := make(chan struct{}, 1)
	receiverDest.RegisterRequestHandler("/p",
		func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
			select {
			case seen <- struct{}{}:
			default:
			}
			return nil
		},
		AllowAll, nil, false,
	)

	if _, err := initiator.Request("/p", bytes.Repeat([]byte{0x01}, 10), nil, nil, nil, 0, 0); err != nil {
		t.Fatalf("Request: %v", err)
	}
	select {
	case <-seen:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: request handler not called for an under-limit inline request")
	}
}

// TestDestinationMaxRequestSize_InlineRequestDroppedOverLimit asserts Phase 8
// Task 1: an inline (ContextRequest) request whose decrypted packed size
// exceeds the destination's max-request size is dropped before the handler is
// dispatched (Link.py:992-997 "Ignored request with excessive size").
func TestDestinationMaxRequestSize_InlineRequestDroppedOverLimit(t *testing.T) {
	t.Parallel()
	initiator, _, receiverDest := establishMaxRequestLinkPair(t)

	// packed size of a request with 120 bytes of data is ~164 bytes: over the
	// 100-byte limit but still under mdu~431, so it remains an inline request
	// (not escalated to a resource).
	if err := receiverDest.SetMaxRequestSize(100); err != nil {
		t.Fatalf("SetMaxRequestSize: %v", err)
	}

	seen := make(chan struct{}, 1)
	receiverDest.RegisterRequestHandler("/p",
		func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
			select {
			case seen <- struct{}{}:
			default:
			}
			return nil
		},
		AllowAll, nil, false,
	)

	if _, err := initiator.Request("/p", bytes.Repeat([]byte{0x02}, 120), nil, nil, nil, 0, 0); err != nil {
		t.Fatalf("Request: %v", err)
	}
	// The over-limit request must be dropped: the handler is never invoked.
	select {
	case <-seen:
		t.Error("request handler was called for an over-limit inline request; want it dropped")
	case <-time.After(800 * time.Millisecond):
	}
}

// TestDestinationMaxRequestSize_ResourceRequestRejectedOverLimit asserts Phase
// 8 Task 1: a request whose packed size exceeds the link MDU is sent as a
// resource (ResourceAdvertisement with is_request=true); when the advertised
// data size (read_size == adv.D) exceeds the destination's max-request size,
// the receiver rejects the resource (Link.py:1031-1037) rather than accepting
// it. The observable is the resource-started callback: Accept fires it, Reject
// does not.
func TestDestinationMaxRequestSize_ResourceRequestRejectedOverLimit(t *testing.T) {
	t.Parallel()
	initiator, receiver, receiverDest := establishMaxRequestLinkPair(t)

	// 500 bytes of data => packed request ~544 bytes > mdu~431 => escalated to
	// a resource. adv.D (= len(packedRequest)) ~544 > 100 => rejected.
	if err := receiverDest.SetMaxRequestSize(100); err != nil {
		t.Fatalf("SetMaxRequestSize: %v", err)
	}

	receiverDest.RegisterRequestHandler("/p",
		func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
			return nil
		},
		AllowAll, nil, false,
	)

	started := make(chan struct{}, 1)
	receiver.callbacks.ResourceStarted = func(*Resource) {
		select {
		case started <- struct{}{}:
		default:
		}
	}

	if _, err := initiator.Request("/p", bytes.Repeat([]byte{0x03}, 500), nil, nil, nil, 0, 0); err != nil {
		t.Fatalf("Request: %v", err)
	}
	select {
	case <-started:
		t.Error("oversized request resource was accepted (ResourceStarted fired); want it rejected")
	case <-time.After(800 * time.Millisecond):
	}
}

// TestDestinationMaxRequestSize_ResourceRequestAcceptedUnlimited asserts the
// control for the rejected case: with no max-request size set (0 = unlimited),
// the same oversized resource request is accepted (ResourceStarted fires),
// proving the rejection above is due to the size limit, not the test setup.
func TestDestinationMaxRequestSize_ResourceRequestAcceptedUnlimited(t *testing.T) {
	t.Parallel()
	initiator, receiver, receiverDest := establishMaxRequestLinkPair(t)

	// No limit set: 0 means unlimited.
	receiverDest.RegisterRequestHandler("/p",
		func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
			return nil
		},
		AllowAll, nil, false,
	)

	started := make(chan struct{}, 1)
	receiver.callbacks.ResourceStarted = func(*Resource) {
		select {
		case started <- struct{}{}:
		default:
		}
	}

	if _, err := initiator.Request("/p", bytes.Repeat([]byte{0x04}, 500), nil, nil, nil, 0, 0); err != nil {
		t.Fatalf("Request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: oversized request resource was not accepted (ResourceStarted did not fire) with no limit set")
	}
}
