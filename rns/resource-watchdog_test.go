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

// lastSentPacket returns the most recently recorded outbound packet, or nil.
func (c *captureTransport) lastSentPacket() *Packet {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		return nil
	}
	return c.sent[len(c.sent)-1]
}

// receiverFromSender builds a receiver-side Resource from a real initiator
// (sender) resource, mirroring Accept's receiver construction
// (resource.go Accept, Resource.py:167-239) but WITHOUT spawning the initial
// RequestNext goroutine or the watchdog loop. The receiver shares the
// sender's link (so Assemble can decrypt with the same token) and copies the
// sender's hashmap / hash / randomHash so getMapHash matches and hash
// validation succeeds. Skipping the goroutines keeps the watchdog's injected
// clock fully deterministic.
func receiverFromSender(t *testing.T, sender *Resource, link *Link) *Resource {
	t.Helper()

	r := &Resource{
		link:              link,
		initiator:         false,
		status:            ResourceStatusTransferring,
		size:              sender.size,
		uncompressedSize:  sender.uncompressedSize,
		totalSize:         sender.totalSize,
		hash:              append([]byte(nil), sender.hash...),
		randomHash:        append([]byte(nil), sender.randomHash...),
		originalHash:      append([]byte(nil), sender.originalHash...),
		totalParts:        sender.totalParts,
		encrypted:         sender.encrypted,
		compressed:        sender.compressed,
		sdu:               sender.sdu,
		window:            4,
		windowMax:         10,
		windowMin:         2,
		windowFlexibility: ResourceWindowFlexibility,
		maxRetries:        ResourceMaxRetries,
		maxAdvRetries:     ResourceMaxAdvRetries,
		retriesLeft:       ResourceMaxRetries,
		timeoutFactor:     link.trafficTimeoutFactor,
		partTimeoutFactor: ResourcePartTimeoutFactor,
		senderGraceTime:   ResourceSenderGraceTime,
	}
	r.parts = make([]*ResourcePart, r.totalParts)
	r.hashmap = make([][]byte, r.totalParts)
	for i := 0; i < r.totalParts; i++ {
		r.hashmap[i] = append([]byte(nil), sender.hashmap[i]...)
		r.parts[i] = &ResourcePart{Index: i, MapHash: r.hashmap[i]}
	}
	return r
}

// deliverPart mirrors Resource.ReceivePart's storage path (resource.go:905-956)
// — match the part data against the hashmap, store it, bump receivedCount —
// minus the auto RequestNext / Assemble goroutine dispatches, and using a
// test-supplied instant for lastActivity so the watchdog's injected clock
// stays deterministic.
func deliverPart(t *testing.T, r, sender *Resource, idx int, now time.Time) {
	t.Helper()

	partData := sender.parts[idx].Data
	partHash := r.getMapHash(partData)

	r.mu.Lock()
	matched := false
	for i, mh := range r.hashmap {
		if bytes.Equal(mh, partHash) {
			matched = true
			if r.parts[i] != nil && r.parts[i].ReceivedData == nil {
				r.parts[i].ReceivedData = partData
				r.receivedCount++
			}
			break
		}
	}
	r.lastActivity = now
	r.mu.Unlock()

	if !matched {
		t.Fatalf("delivered sender part %d matched no receiver hashmap entry", idx)
	}
}

// TestWatchdogLossyLinkRecovery drives the full resource-transfer loss-recovery
// loop end-to-end against a real initiator resource (real encryption,
// real hashmap, real advertisement geometry):
//
//  1. A sender is split into >=5 parts; the receiver accepts the hashmap.
//  2. Parts 0 and 1 are delivered; the rest are "lost" on the link.
//  3. The watchdog (driven by an injected clock past the part-timeout
//     deadline) detects the loss, shrinks the window, decrements retries,
//     and re-requests the missing parts (a real request packet whose data
//     carries the missing parts' map hashes and excludes the received ones).
//  4. The remaining parts are delivered and the resource is assembled.
//  5. Status is COMPLETE and the decrypted payload equals the original.
//
// This is the golden recovery test that replaces the former
// TestResourceWatchdogJobIsNoOp pin.
func TestWatchdogLossyLinkRecovery(t *testing.T) {
	t.Parallel()

	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.trafficTimeoutFactor = 6.0
	link.transport = ct
	link.logger = testSilentLogger()
	// Force a small SDU so the payload spans several parts.
	link.mtu = 120

	data := bytes.Repeat([]byte("R"), 400)
	sender, err := NewResourceWithOptions(data, link, ResourceOptions{})
	if err != nil {
		t.Fatalf("NewResourceWithOptions: %v", err)
	}
	if sender.totalParts < 5 {
		t.Fatalf("test requires >=5 parts, got %d (sdu=%d, size=%d)", sender.totalParts, sender.sdu, sender.size)
	}

	receiver := receiverFromSender(t, sender, link)
	// Deterministic watchdog timing (mirrors testTransferringReceiver):
	// eifr = req_data_rtt_rate*8 = 8000, req_resp_rtt_rate != 0 selects the
	// measured-RTT deadline formula.
	receiver.reqDataRttRate = 1000
	receiver.reqRespRttRate = 500
	receiver.rtt = new(1.0)
	receiver.partTimeoutFactor = 4

	base := time.Unix(5_000_000, 0)

	// (1) Deliver parts 0 and 1; drop the rest.
	deliverPart(t, receiver, sender, 0, base)
	deliverPart(t, receiver, sender, 1, base.Add(1*time.Second))
	if receiver.receivedCount != 2 {
		t.Fatalf("after initial delivery: receivedCount=%d, want 2", receiver.receivedCount)
	}

	missing := receiver.totalParts - 2
	lastAct := base.Add(1 * time.Second)
	receiver.mu.Lock()
	receiver.lastActivity = lastAct
	receiver.outstandingParts = missing
	receiver.waitingForHmu = false
	receiver.retriesLeft = receiver.maxRetries
	receiver.mu.Unlock()

	// Part-timeout deadline (Python Resource.py:594-606, req_resp_rtt_rate!=0):
	// deadline = last_activity + part_timeout_factor*expected_tof_remaining
	//            + expected_hmu_wait_remaining + RETRY_GRACE_TIME + extra_wait
	// expected_tof_remaining = (outstanding_parts * sdu * 8) / eifr
	// expected_hmu_wait_remaining = 0 (not waiting_for_hmu, outstanding>0)
	// extra_wait = 0 (no retries used yet).
	eifr := receiver.reqDataRttRate * 8 // 8000
	expectedTofRemaining := float64(missing) * float64(receiver.sdu) * 8 / eifr
	deadlineOffset := float64(receiver.partTimeoutFactor)*expectedTofRemaining + ResourceRetryGraceTime
	deadline := lastAct.Add(time.Duration(deadlineOffset * float64(time.Second)))

	baseline := ct.sentCount()

	// (2) Before the deadline: the watchdog must not re-request.
	sleep, cont := receiver.watchdogStep(deadline.Add(-1 * time.Second))
	if !cont {
		t.Fatalf("before deadline: cont=false, want true (still transferring)")
	}
	if sleep <= 0 {
		t.Fatalf("before deadline: sleep=%v, want > 0", sleep)
	}
	if ct.sentCount() != baseline {
		t.Fatalf("before deadline: sends=%d, want %d (no retry yet)", ct.sentCount(), baseline)
	}
	if receiver.retriesLeft != receiver.maxRetries {
		t.Fatalf("before deadline: retriesLeft=%d, want %d (unchanged)", receiver.retriesLeft, receiver.maxRetries)
	}
	if receiver.window != 4 {
		t.Fatalf("before deadline: window=%d, want 4 (unchanged)", receiver.window)
	}

	// (3) Past the deadline: the watchdog re-requests the missing parts.
	now1 := deadline.Add(time.Millisecond)
	sleep, cont = receiver.watchdogStep(now1)
	if !cont {
		t.Fatalf("after deadline: cont=false, want true (still transferring)")
	}
	if sleep != 0.001 {
		t.Fatalf("after deadline: sleep=%v, want 0.001", sleep)
	}
	if ct.sentCount() != baseline+1 {
		t.Fatalf("after deadline: sends=%d, want %d (one re-request)", ct.sentCount(), baseline+1)
	}
	if receiver.window != 3 {
		t.Fatalf("after deadline: window=%d, want 3 (shrunk from 4)", receiver.window)
	}
	if receiver.windowMax != 8 {
		t.Fatalf("after deadline: windowMax=%d, want 8 (flexibility shrink)", receiver.windowMax)
	}
	if receiver.retriesLeft != receiver.maxRetries-1 {
		t.Fatalf("after deadline: retriesLeft=%d, want %d", receiver.retriesLeft, receiver.maxRetries-1)
	}
	if receiver.waitingForHmu {
		t.Fatal("after deadline: waitingForHmu=true, want false (cleared on retry)")
	}
	if !receiver.lastActivity.Equal(now1) {
		t.Fatalf("after deadline: lastActivity=%v, want %v (request_next resets it)", receiver.lastActivity, now1)
	}

	// The re-request packet must carry the first 3 missing parts' map hashes
	// (window shrank to 3) and must NOT re-request the 2 received parts.
	req := ct.lastSentPacket()
	if req == nil {
		t.Fatal("after deadline: no re-request packet recorded")
	}
	wantReq := min(missing, 3)
	for i := 2; i < 2+wantReq; i++ {
		if !bytes.Contains(req.Data, receiver.hashmap[i]) {
			t.Fatalf("re-request packet missing part %d map hash", i)
		}
	}
	if bytes.Contains(req.Data, receiver.hashmap[0]) || bytes.Contains(req.Data, receiver.hashmap[1]) {
		t.Fatal("re-request packet re-requests an already-received part (0 or 1)")
	}

	// (4) Deliver the remaining parts (simulating the sender answering the
	// re-request) and assemble.
	for i := 2; i < receiver.totalParts; i++ {
		deliverPart(t, receiver, sender, i, now1.Add(time.Duration(i)*time.Second))
	}
	if receiver.receivedCount != receiver.totalParts {
		t.Fatalf("after recovery delivery: receivedCount=%d, want %d", receiver.receivedCount, receiver.totalParts)
	}

	receiver.Assemble()

	// (5) The transfer completes and the decrypted payload matches.
	if receiver.status != ResourceStatusComplete {
		t.Fatalf("after assemble: status=%v, want COMPLETE", receiver.status)
	}
	if !bytes.Equal(receiver.data, data) {
		t.Fatalf("after assemble: data mismatch (got %d bytes, want %d)", len(receiver.data), len(data))
	}
}
