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

// newHMUReceiver builds a receiver-side Resource whose advertised hashmap is
// truncated after knownParts entries, exactly what a peer that packs only one
// advertisement segment leaves behind (ResourceAdvertisement.pack). The plain
// stream is the random-hash prefix followed by data, split into parts of 16
// bytes; every part's map hash is the real getMapHash, so the parts can be
// replayed through ReceivePart.
func newHMUReceiver(t *testing.T, data []byte, knownParts, window int) (*Resource, *captureTransport, [][]byte) {
	t.Helper()

	link := testActiveResourceLink(t)
	link.logger = testSilentLogger()
	ct := newCaptureTransport()
	link.transport = ct

	r := &Resource{
		link:                       link,
		initiator:                  false,
		status:                     ResourceStatusTransferring,
		randomHash:                 []byte{0x11, 0x22, 0x33, 0x44},
		window:                     window,
		windowMax:                  10,
		windowMin:                  2,
		windowFlexibility:          ResourceWindowFlexibility,
		maxRetries:                 ResourceMaxRetries,
		consecutiveCompletedHeight: -1,
	}
	r.hash = FullHash(append(copyBytes(data), r.randomHash...))

	stream := append(copyBytes(r.randomHash), data...)
	const sdu = 16
	parts := len(stream) / sdu
	if len(stream)%sdu != 0 {
		parts++
	}
	r.totalParts = parts
	r.parts = make([]*ResourcePart, parts)
	r.hashmap = make([][]byte, parts)

	plaintext := make([][]byte, parts)
	for i := range parts {
		start := i * sdu
		end := min((i+1)*sdu, len(stream))
		plaintext[i] = copyBytes(stream[start:end])
		r.parts[i] = &ResourcePart{Index: i}
		r.hashmap[i] = r.getMapHash(plaintext[i])
	}
	for i := knownParts; i < parts; i++ {
		r.hashmap[i] = nil
	}
	r.hashmapHeight = min(knownParts, parts)
	return r, ct, plaintext
}

// hmRequestPackets returns the ResourceReq packets the capture transport saw.
func hmRequestPackets(ct *captureTransport) []*Packet {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	var out []*Packet
	for _, p := range ct.sent {
		if p.Context == ContextResourceReq {
			out = append(out, p)
		}
	}
	return out
}

// TestReceiverRequestsExhaustedHashmap pins the receiver half of the resource
// hashmap-update protocol (Python Resource.request_next, Resource.py:936-982).
//
// A sender packs only one advertisement segment, so a receiver of a resource
// with more parts than HASHMAP_MAX_LEN sees the tail of its hashmap as unknown.
// Python stops the scan there, sets HASHMAP_IS_EXHAUSTED (0xFF), appends the
// last known map hash so the sender can locate the segment, sets
// waiting_for_hmu and sends. Until this port did the same it silently skipped
// the unknown entries, requested nothing more, and the transfer stalled at the
// end of the first segment. The request must be sent exactly once: every retry
// re-sends it only after the watchdog clears waiting_for_hmu.
func TestReceiverRequestsExhaustedHashmap(t *testing.T) {
	t.Parallel()

	const knownParts = hashmapMaxLen
	data := make([]byte, (knownParts+3)*16)
	for i := range data {
		data[i] = byte(i)
	}
	r, ct, plaintext := newHMUReceiver(t, data, knownParts, 4)
	// The first segment is what the advertisement named; the receiver has
	// consumed all of it, so the next scan starts at the first unknown entry.
	r.consecutiveCompletedHeight = knownParts - 1

	if err := r.RequestNext(); err != nil {
		t.Fatalf("RequestNext: %v", err)
	}

	reqs := hmRequestPackets(ct)
	if len(reqs) != 1 {
		t.Fatalf("sent %v requests, want exactly 1", len(reqs))
	}
	got := reqs[0].Data
	const wantMarker = 0xFF
	if len(got) < 1+ResourceMapHashLen+len(r.hash) {
		t.Fatalf("request too short: %x", got)
	}
	if got[0] != wantMarker {
		t.Fatalf("request marker = %#x, want HASHMAP_IS_EXHAUSTED (%#x) (Resource.py:958-961)", got[0], wantMarker)
	}
	wantLast := r.hashmap[knownParts-1]
	if !bytes.Equal(got[1:1+ResourceMapHashLen], wantLast) {
		t.Errorf("last map hash = %x, want %x (Resource.py:966 takes hashmap[hashmap_height-1])", got[1:1+ResourceMapHashLen], wantLast)
	}
	hashStart := 1 + ResourceMapHashLen
	if !bytes.Equal(got[hashStart:hashStart+len(r.hash)], r.hash) {
		t.Errorf("resource hash after the marker is %x, want %x", got[hashStart:hashStart+len(r.hash)], r.hash)
	}
	if !r.waitingForHmu {
		t.Error("waitingForHmu = false after an exhausted request, want true (Resource.py:968)")
	}

	// A second request while waiting for the update must not go on the wire.
	if err := r.RequestNext(); err != nil {
		t.Fatalf("second RequestNext: %v", err)
	}
	if n := len(hmRequestPackets(ct)); n != 1 {
		t.Errorf("sent %v requests while waiting for the HMU, want 1", n)
	}

	// Feed the missing segment exactly as the sender builds it
	// (Resource.py:1058-1059): the resource hash followed by msgpack
	// [segment, hashmap bytes].
	segment := 1
	flat := make([]byte, 0, (r.totalParts-knownParts)*ResourceMapHashLen)
	for i := knownParts; i < r.totalParts; i++ {
		flat = append(flat, r.getMapHash(plaintext[i])...)
	}
	packed, err := msgpack.Pack([]any{segment, flat})
	if err != nil {
		t.Fatalf("msgpack.Pack: %v", err)
	}
	update := append(copyBytes(r.hash), packed...)

	before := len(hmRequestPackets(ct))
	if err := r.HashmapUpdate(update); err != nil {
		t.Fatalf("HashmapUpdate: %v", err)
	}
	if r.waitingForHmu {
		t.Error("waitingForHmu = true after the hashmap update, want false (Resource.py:502)")
	}
	if r.hashmapHeight != r.totalParts {
		t.Errorf("hashmapHeight = %v after the update, want %v", r.hashmapHeight, r.totalParts)
	}
	for i := knownParts; i < r.totalParts; i++ {
		if len(r.hashmap[i]) != ResourceMapHashLen {
			t.Fatalf("hashmap[%v] was not extended by the update", i)
		}
	}
	// Extending the map must immediately ask for the parts it now names.
	if n := len(hmRequestPackets(ct)); n <= before {
		t.Errorf("no part request followed the hashmap update; want a request for the newly named parts")
	}
}

// TestReceiverAssemblesAfterHashmapUpdate drives the same truncated-hashmap
// receiver to completion: it asks for the missing segment, the update names the
// remaining parts, and once they are delivered the resource assembles. This is
// the "assembles the resource once the segment arrives" half of the unit.
func TestReceiverAssemblesAfterHashmapUpdate(t *testing.T) {
	t.Parallel()

	const knownParts = hashmapMaxLen
	// 80 streams parts of 16 bytes, so six of them lie beyond the first segment.
	data := make([]byte, 80*16-ResourceRandomHashSize)
	for i := range data {
		data[i] = byte(i * 7)
	}
	r, _, plaintext := newHMUReceiver(t, data, knownParts, 4)
	if r.totalParts <= knownParts {
		t.Fatalf("test needs more than %v parts, got %v", knownParts, r.totalParts)
	}
	// The first advertised segment has already been received: that is what
	// puts the receiver at the point where the map runs out.
	for i := range knownParts {
		r.parts[i].ReceivedData = plaintext[i]
	}
	r.receivedCount = knownParts
	r.consecutiveCompletedHeight = knownParts - 1

	if err := r.RequestNext(); err != nil {
		t.Fatalf("RequestNext: %v", err)
	}

	segment := 1
	flat := make([]byte, 0, (r.totalParts-knownParts)*ResourceMapHashLen)
	for i := knownParts; i < r.totalParts; i++ {
		flat = append(flat, r.getMapHash(plaintext[i])...)
	}
	packed, err := msgpack.Pack([]any{segment, flat})
	if err != nil {
		t.Fatalf("msgpack.Pack: %v", err)
	}
	if err := r.HashmapUpdate(append(copyBytes(r.hash), packed...)); err != nil {
		t.Fatalf("HashmapUpdate: %v", err)
	}

	// Deliver the parts the update named, as the sender would.
	for i := knownParts; i < r.totalParts; i++ {
		if err := r.ReceivePart(&Packet{Data: plaintext[i]}); err != nil {
			t.Fatalf("ReceivePart(%v): %v", i, err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		status := r.status
		r.mu.Unlock()
		switch status {
		case ResourceStatusComplete:
			return
		case ResourceStatusFailed, ResourceStatusCorrupt:
			t.Fatalf("resource ended in status %v after every part was delivered", status)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("resource did not complete after every part was delivered")
}
