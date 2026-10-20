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

// TestInflightPathRequestBatching pins Python Transport.inflight_path_requests
// (Transport.py:183,1771-1777): the first request for a destination is
// processed (and registered), a second request with a different tag while the
// first is in flight is batched (consumed without processing), the stale cull
// removes the entry after PATH_REQUEST_GATE_TIMEOUT, and answering a request
// clears it immediately.
func TestInflightPathRequestBatching(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.identity = mustTestNewIdentity(t, true)

	target := bytes.Repeat([]byte{0x42}, TruncatedHashLength/8)
	tag := func(b byte) []byte { return bytes.Repeat([]byte{b}, TruncatedHashLength/8) }
	request := func(dest, t []byte) (*Packet, []byte) {
		data := append(append([]byte(nil), dest...), t...)
		return &Packet{Data: data}, data
	}

	// An unknown destination is forwarded, so handlePathRequest returns false.
	pkt, data := request(target, tag(0xA1))
	if ts.handlePathRequest(data, pkt) {
		t.Fatal("first path request = answered/batched, want forwarded")
	}
	ts.mu.Lock()
	_, inflight := ts.inflightPathRequests[string(target)]
	ts.mu.Unlock()
	if !inflight {
		t.Fatal("first path request was not registered as in flight")
	}

	// A different tag for the same destination is batched while in flight.
	pkt2, data2 := request(target, tag(0xB2))
	if !ts.handlePathRequest(data2, pkt2) {
		t.Fatal("duplicate in-flight path request = forwarded, want batched")
	}

	// The stale cull removes the entry after the gate timeout, so a later
	// request is processed again.
	ts.cullPathRequests(time.Now().Add(pathRequestCullAfter + time.Second))
	ts.mu.Lock()
	_, inflight = ts.inflightPathRequests[string(target)]
	ts.mu.Unlock()
	if inflight {
		t.Fatal("stale in-flight path request was not culled")
	}
	pkt3, data3 := request(target, tag(0xC3))
	if ts.handlePathRequest(data3, pkt3) {
		t.Fatal("request after the cull = batched, want processed again")
	}

	// Answering a request clears its in-flight entry.
	dest := mustTestNewDestination(t, ts, ts.identity, DestinationIn, DestinationSingle, "inflight-local")
	pkt4, data4 := request(dest.Hash, tag(0xD4))
	if !ts.handlePathRequest(data4, pkt4) {
		t.Fatal("local-destination path request = forwarded, want answered")
	}
	ts.mu.Lock()
	_, inflight = ts.inflightPathRequests[string(dest.Hash)]
	ts.mu.Unlock()
	if inflight {
		t.Error("answered path request left its in-flight entry behind")
	}
}
