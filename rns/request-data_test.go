// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// formRequestRig wires an in-process link pair with a request handler that
// reports every data value it receives. It mirrors the shape the gonomadnet
// browser uses when it submits Micron form fields: link.Request is handed a
// map rather than a packed byte payload.
type formRequestRig struct {
	link     *Link
	received chan any
}

// newFormRequestRig establishes a link from a local initiator transport to a
// remote destination whose single request path reports its data verbatim.
func newFormRequestRig(t *testing.T, handler func(path string, data any, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any) *formRequestRig {
	t.Helper()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	t.Cleanup(cleanup)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")
	receiverDest.RegisterRequestHandler("/form/path", handler, AllowAll, nil, true)

	link := mustTestNewLink(t, tsInitiator, receiverDest)
	established := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		established <- true
	}
	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-established:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for link establishment")
	}
	return &formRequestRig{link: link, received: make(chan any, 1)}
}

// send fires a request whose data is the given value and waits for the
// handler's response.
func (r *formRequestRig) send(t *testing.T, data any) any {
	t.Helper()
	responded := make(chan any, 1)
	failed := make(chan struct{}, 1)
	_, err := r.link.Request("/form/path", data, func(rr *RequestReceipt) {
		responded <- rr.Response
	}, func(rr *RequestReceipt) {
		failed <- struct{}{}
	}, nil, 10*time.Second, 0)
	mustTest(t, err)

	select {
	case res := <-responded:
		return res
	case <-failed:
		t.Fatal("request failed: no response from the remote peer")
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
	return nil
}

// TestRequestDataMapReachesHandler pins Python's request-data pass-through: RNS
// Link.handle_request assigns `request_data = unpacked_request[2]` without
// constraining its MessagePack type (Link.py:803-808), so a peer that sends a
// map for Micron form fields must reach the generator verbatim. The gonomadnet
// browser relies on exactly this when it submits form data as a map.
func TestRequestDataMapReachesHandler(t *testing.T) {
	t.Parallel()
	received := make(chan any, 1)
	rig := newFormRequestRig(t, func(path string, data any, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		received <- data
		return "handled"
	})

	sent := map[string]any{"var_a": "1", "field_title": "hi"}
	if res := rig.send(t, sent); res != "handled" {
		t.Fatalf("expected the handler's response, got %v", res)
	}

	select {
	case got := <-received:
		gotMap, ok := got.(map[any]any)
		if !ok {
			t.Fatalf("expected the handler to receive a msgpack map, got %T (%v)", got, got)
		}
		if len(gotMap) != len(sent) {
			t.Fatalf("expected %v entries, got %v (%v)", len(sent), len(gotMap), gotMap)
		}
		for key, want := range sent {
			if gotMap[key] != want {
				t.Errorf("request_data[%v] = %v, want %v", key, gotMap[key], want)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request handler was never invoked: map-typed request data was dropped")
	}
}

// TestRequestDataBytesPassThrough pins that a peer which packs its own payload
// still arrives at the handler as bytes, so byte-oriented handlers (rnx,
// rngit, rncp) keep their existing decode path.
func TestRequestDataBytesPassThrough(t *testing.T) {
	t.Parallel()
	received := make(chan any, 1)
	rig := newFormRequestRig(t, func(path string, data any, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		received <- data
		return "handled"
	})

	packed, err := msgpack.Pack([]any{[]byte("ls"), 1.5})
	mustTest(t, err)
	if res := rig.send(t, packed); res != "handled" {
		t.Fatalf("expected the handler's response, got %v", res)
	}

	select {
	case got := <-received:
		raw, ok := got.([]byte)
		if !ok {
			t.Fatalf("expected the handler to receive bytes, got %T (%v)", got, got)
		}
		unpacked, err := msgpack.Unpack(raw)
		mustTest(t, err)
		parts, ok := unpacked.([]any)
		if !ok || len(parts) != 2 || string(parts[0].([]byte)) != "ls" {
			t.Fatalf("expected the pre-packed payload to round-trip, got %v", unpacked)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request handler was never invoked")
	}
}
