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

// TestResourceRequestStopsReAdvertise pins Python Resource.request's state
// transition (Resource.py:988-996): the first request for an advertised
// resource moves the sender from ADVERTISED to TRANSFERRING and restarts the
// retry budget.
//
// Leaving the sender in ADVERTISED is not cosmetic. The watchdog's ADVERTISED
// branch re-sends the advertisement on its own timer, and a receiver that sees
// the same advertisement again accepts it *again* — Python's Link.receive has no
// per-resource-hash dedup (Link.py:1048) — so one transfer becomes several. Each
// copy requests parts independently, every delivered part triggers a request on
// every copy, and the node answers each of them: the request/part traffic
// multiplies instead of converging, the link saturates, and the transfer never
// completes. That is the shape observed live fetching a page from a two-hop
// node: up to five registered resources for one hash, tens of thousands of
// deliveries of the same packets, and progress stuck short of the whole.
func TestResourceRequestStopsReAdvertise(t *testing.T) {
	t.Parallel()

	r, ct := testAdvertisedResource(t, time.Now(), 1.0)
	r.totalParts = 2
	zero := bytes.Repeat([]byte{0xAB}, ResourceMapHashLen)
	one := bytes.Repeat([]byte{0xCD}, ResourceMapHashLen)
	r.parts = []*ResourcePart{{Index: 0, MapHash: zero}, {Index: 1, MapHash: one}}
	r.hashmap = [][]byte{zero, one}

	// A not-exhausted request for the second part: [0x00][resource hash][the
	// wanted map hashes] — Python's HASHMAP_IS_NOT_EXHAUSTED form
	// (Resource.py:943-970).
	requestData := make([]byte, 0, 1+len(r.hash)+ResourceMapHashLen)
	requestData = append(requestData, 0x00)
	requestData = append(requestData, r.hash...)
	requestData = append(requestData, one...)

	if err := r.Request(requestData); err != nil {
		t.Fatalf("Request: %v", err)
	}

	if r.status != ResourceStatusTransferring {
		t.Errorf("status after first request = %v, want ResourceStatusTransferring (%v); an ADVERTISED sender re-advertises and the receiver registers a second resource for the same hash",
			r.status, ResourceStatusTransferring)
	}
	if r.retriesLeft != r.maxRetries {
		t.Errorf("retriesLeft after first request = %v, want maxRetries (%v) (Python Resource.py:998 resets it on every request)",
			r.retriesLeft, r.maxRetries)
	}

	// With the status moved off ADVERTISED, the watchdog must no longer take
	// the advertisement-resend branch: an expired advSent deadline that used to
	// emit a re-advertisement is now a no-op for this resource.
	r.advSent = time.Now().Add(-time.Hour)
	before := ct.sentCount()
	sleep, cont := r.watchdogStep(time.Now())
	if sleep <= 0 && cont {
		t.Errorf("watchdogStep after the request returned sleep=%v cont=%v; expected a positive wait, not an immediate resend", sleep, cont)
	}
	if ct.sentCount() != before {
		t.Errorf("watchdogStep re-sent %v advertisement(s) after the first request; the sender must stop advertising once it is transferring",
			ct.sentCount()-before)
	}
}

// TestResourceRequestKeepsAwaitingProofOnLastPart pins the tail of Python's
// Resource.request: once the request has been answered, every part is out, and
// the sender moves to AWAITING_PROOF. The transition to TRANSFERRING must not
// mask it.
func TestResourceRequestKeepsAwaitingProofOnLastPart(t *testing.T) {
	t.Parallel()

	r, _ := testAdvertisedResource(t, time.Now(), 1.0)
	r.totalParts = 1
	zero := bytes.Repeat([]byte{0xAB}, ResourceMapHashLen)
	r.parts = []*ResourcePart{{Index: 0, MapHash: zero}}
	r.hashmap = [][]byte{zero}

	requestData := make([]byte, 0, 1+len(r.hash)+ResourceMapHashLen)
	requestData = append(requestData, 0x00)
	requestData = append(requestData, r.hash...)
	requestData = append(requestData, zero...)

	if err := r.Request(requestData); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if r.status != ResourceStatusAwaitingProof {
		t.Errorf("status after the only part was requested = %v, want ResourceStatusAwaitingProof (%v)", r.status, ResourceStatusAwaitingProof)
	}
}
