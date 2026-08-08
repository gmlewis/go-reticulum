// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// captureTransport records outbound packets and cache-request hashes without
// processing them, so watchdog tests can assert resend / cache-query counts
// against an injected clock.
type captureTransport struct {
	*TransportSystem
	mu       sync.Mutex
	sent     []*Packet
	cacheReq [][]byte
}

func newCaptureTransport() *captureTransport {
	return &captureTransport{TransportSystem: NewTransportSystem(testSilentLogger())}
}

func (c *captureTransport) Outbound(p *Packet) error {
	c.mu.Lock()
	c.sent = append(c.sent, p)
	c.mu.Unlock()
	return nil
}

func (c *captureTransport) CacheRequest(hash []byte, _ *Link) {
	c.mu.Lock()
	c.cacheReq = append(c.cacheReq, append([]byte(nil), hash...))
	c.mu.Unlock()
}

func (c *captureTransport) sentCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

func (c *captureTransport) cacheReqCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cacheReq)
}

// testAdvertisedResource builds an initiator resource on a link backed by a
// capture transport, then places it in the ADVERTISED state with a known
// advSent / timeout / retriesLeft, mirroring what Resource.Advertise sets
// (Python __advertise_job, Resource.py:520-541) but with a fixed advSent so
// the watchdog's injected clock is deterministic.
func testAdvertisedResource(t *testing.T, advSent time.Time, timeout float64) (*Resource, *captureTransport) {
	t.Helper()

	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.trafficTimeoutFactor = 6.0
	link.transport = ct
	link.logger = testSilentLogger()

	r, err := NewResourceWithOptions(make([]byte, 256), link, ResourceOptions{})
	if err != nil {
		t.Fatalf("NewResourceWithOptions: %v", err)
	}
	r.status = ResourceStatusAdvertised
	r.advSent = advSent
	r.lastActivity = advSent
	r.timeout = timeout
	r.retriesLeft = r.maxAdvRetries
	r.advertisementPacket = nil
	return r, ct
}

// TestWatchdogAdvertised asserts the ADVERTISED branch of the watchdog
// (Python Resource.__watchdog_job Resource.py:573-589): before the
// adv_sent+timeout+PROCESSING_GRACE deadline it does nothing; past the
// deadline with retries left it resends the advertisement (decrementing
// retries_left, resetting adv_sent); once retries are exhausted it cancels
// (status FAILED).
func TestWatchdogAdvertised(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_000_000, 0)
	const timeout = 5.0
	// deadline = advSent + timeout + ProcessingGrace = base + 6s.
	deadline := base.Add(time.Duration((timeout + ResourceProcessingGrace) * float64(time.Second)))

	r, ct := testAdvertisedResource(t, base, timeout)

	// Before the deadline: no action.
	sleep, _ := r.watchdogStep(base.Add(3 * time.Second))
	if sleep <= 0 {
		t.Fatalf("before deadline: sleep = %v, want > 0", sleep)
	}
	if r.retriesLeft != r.maxAdvRetries {
		t.Fatalf("before deadline: retriesLeft = %d, want %d", r.retriesLeft, r.maxAdvRetries)
	}
	if ct.sentCount() != 0 {
		t.Fatalf("before deadline: sent = %d, want 0", ct.sentCount())
	}
	if r.status != ResourceStatusAdvertised {
		t.Fatalf("before deadline: status = %v, want ADVERTISED", r.status)
	}

	// Past the deadline with retries left: resend advertisement.
	now1 := deadline.Add(time.Millisecond)
	sleep, _ = r.watchdogStep(now1)
	if sleep != 0.001 {
		t.Fatalf("after resend: sleep = %v, want 0.001", sleep)
	}
	if r.retriesLeft != r.maxAdvRetries-1 {
		t.Fatalf("after resend #1: retriesLeft = %d, want %d", r.retriesLeft, r.maxAdvRetries-1)
	}
	if ct.sentCount() != 1 {
		t.Fatalf("after resend #1: sent = %d, want 1", ct.sentCount())
	}
	if !r.advSent.Equal(now1) {
		t.Fatalf("after resend #1: advSent = %v, want %v", r.advSent, now1)
	}
	if r.status != ResourceStatusAdvertised {
		t.Fatalf("after resend #1: status = %v, want ADVERTISED", r.status)
	}
	if r.advertisementPacket == nil {
		t.Fatal("after resend #1: advertisementPacket = nil, want the resent packet")
	}

	// Drive resends until retries are exhausted (maxAdvRetries resends total).
	now := now1
	for r.retriesLeft > 0 {
		nextDeadline := now.Add(time.Duration((timeout + ResourceProcessingGrace) * float64(time.Second)))
		now = nextDeadline.Add(time.Millisecond)
		r.watchdogStep(now)
	}
	wantSends := r.maxAdvRetries
	if ct.sentCount() != wantSends {
		t.Fatalf("after all resends: sent = %d, want %d", ct.sentCount(), wantSends)
	}
	if r.retriesLeft != 0 {
		t.Fatalf("after all resends: retriesLeft = %d, want 0", r.retriesLeft)
	}
	if r.status != ResourceStatusAdvertised {
		t.Fatalf("after all resends: status = %v, want ADVERTISED (not yet cancelled)", r.status)
	}

	// One more step past the deadline with zero retries: cancel.
	finalDeadline := now.Add(time.Duration((timeout + ResourceProcessingGrace) * float64(time.Second)))
	r.watchdogStep(finalDeadline.Add(time.Millisecond))
	if r.status != ResourceStatusFailed {
		t.Fatalf("after exhaustion: status = %v, want FAILED", r.status)
	}
	if ct.sentCount() != wantSends {
		t.Fatalf("after exhaustion: sent = %d, want unchanged %d", ct.sentCount(), wantSends)
	}
}

// testTransferringReceiver builds a receiver resource in the TRANSFERRING
// state with a capture transport and a known, deterministic watchdog state:
// eifr = req_data_rtt_rate*8 (so update_eifr is independent of the link),
// req_resp_rtt_rate != 0 (selects the measured-RTT timeout formula), and a
// fixed lastActivity / outstanding_parts so the part-timeout deadline is
// exactly computable from the Python formula (Resource.py:594-606).
func testTransferringReceiver(t *testing.T, lastActivity time.Time) (*Resource, *captureTransport) {
	t.Helper()

	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.trafficTimeoutFactor = 6.0
	link.transport = ct
	link.logger = testSilentLogger()

	r := &Resource{
		link:              link,
		initiator:         false,
		status:            ResourceStatusTransferring,
		totalParts:        8,
		receivedCount:     0,
		window:            4,
		windowMax:         10,
		windowMin:         2,
		windowFlexibility: 4,
		sdu:               464,
		outstandingParts:  4,
		waitingForHmu:     false,
		reqRespRttRate:    500,
		reqDataRttRate:    1000,
		partTimeoutFactor: 4,
		maxRetries:        16,
		retriesLeft:       16,
		senderGraceTime:   10,
		timeoutFactor:     6,
		lastActivity:      lastActivity,
		hash:              bytes.Repeat([]byte{0xAB}, 16),
	}
	r.hashmap = make([][]byte, 8)
	r.parts = make([]*ResourcePart, 8)
	for i := range 8 {
		mh := []byte{0x10, 0x20, 0x30, byte(i)}
		r.parts[i] = &ResourcePart{Index: i, MapHash: mh}
		r.hashmap[i] = mh
	}
	return r, ct
}

// TestWatchdogTransferringReceiver asserts the TRANSFERRING-receiver branch
// (Python Resource.py:594-629). With eifr=req_data_rtt_rate*8=8000,
// outstanding_parts=4, sdu=464, part_timeout_factor=4, req_resp_rtt_rate!=0,
// the part-timeout deadline is last_activity + 4*((4*464*8)/8000) +
// RETRY_GRACE_TIME = last_activity + 7.674s. Before it: no action. Past it:
// the window shrinks (4->3, window_max 10->8 per the flexibility rules),
// retries_left decrements, waiting_for_hmu clears, and request_next is
// invoked. After max_retries the resource cancels.
func TestWatchdogTransferringReceiver(t *testing.T) {
	t.Parallel()

	base := time.Unix(2_000_000, 0)
	r, ct := testTransferringReceiver(t, base)

	// eifr = 1000*8 = 8000; expected_tof_remaining = (4*464*8)/8000 = 1.856;
	// deadline = base + 4*1.856 + 0.25 = base + 7.674s.
	const deadlineOffset = 4*((4*464*8)/8000.0) + 0.25
	deadline := base.Add(time.Duration(deadlineOffset * float64(time.Second)))

	// Well before the deadline: no action, sleep == time remaining.
	sleep, _ := r.watchdogStep(base.Add(7 * time.Second))
	wantSleep := deadline.Sub(base.Add(7 * time.Second)).Seconds()
	if sleep <= 0 || sleep < wantSleep-1e-9 || sleep > wantSleep+1e-9 {
		t.Fatalf("before deadline: sleep = %v, want %v", sleep, wantSleep)
	}
	if r.window != 4 || r.windowMax != 10 || r.retriesLeft != 16 {
		t.Fatalf("before deadline: window=%d windowMax=%d retriesLeft=%d, want 4/10/16", r.window, r.windowMax, r.retriesLeft)
	}
	if ct.sentCount() != 0 {
		t.Fatalf("before deadline: sent = %d, want 0", ct.sentCount())
	}

	// Past the deadline: retry fires.
	now1 := deadline.Add(time.Millisecond)
	sleep, _ = r.watchdogStep(now1)
	if sleep != 0.001 {
		t.Fatalf("after retry: sleep = %v, want 0.001", sleep)
	}
	if r.window != 3 {
		t.Fatalf("after retry: window = %d, want 3", r.window)
	}
	if r.windowMax != 8 {
		t.Fatalf("after retry: windowMax = %d, want 8", r.windowMax)
	}
	if r.retriesLeft != 15 {
		t.Fatalf("after retry: retriesLeft = %d, want 15", r.retriesLeft)
	}
	if r.waitingForHmu {
		t.Fatal("after retry: waitingForHmu = true, want false")
	}
	if ct.sentCount() != 1 {
		t.Fatalf("after retry: sent = %d, want 1 (requestNext invoked)", ct.sentCount())
	}
	if !r.lastActivity.Equal(now1) {
		t.Fatalf("after retry: lastActivity = %v, want %v (request_next resets it)", r.lastActivity, now1)
	}
	if r.status != ResourceStatusTransferring {
		t.Fatalf("after retry: status = %v, want TRANSFERRING", r.status)
	}

	// Drive retries to exhaustion: each +100s step is past the (growing)
	// deadline, so every step retries until retries_left hits 0.
	now := now1
	for r.retriesLeft > 0 {
		now = now.Add(100 * time.Second)
		r.watchdogStep(now)
	}
	if ct.sentCount() != r.maxRetries {
		t.Fatalf("after all retries: sent = %d, want %d", ct.sentCount(), r.maxRetries)
	}
	if r.status != ResourceStatusTransferring {
		t.Fatalf("after all retries: status = %v, want TRANSFERRING (not yet cancelled)", r.status)
	}

	// One more step past the deadline with zero retries: cancel.
	r.watchdogStep(now.Add(100 * time.Second))
	if r.status != ResourceStatusFailed {
		t.Fatalf("after exhaustion: status = %v, want FAILED", r.status)
	}
}

// TestWatchdogTransferringSender asserts the TRANSFERRING-sender branch
// (Python Resource.py:630-637): the sender cancels once
// last_activity + rtt*timeout_factor*max_retries + sender_grace_time +
// max_extra_wait is exceeded, and not before.
func TestWatchdogTransferringSender(t *testing.T) {
	t.Parallel()

	base := time.Unix(3_000_000, 0)
	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.transport = ct
	link.logger = testSilentLogger()

	r := &Resource{
		link:            link,
		initiator:       true,
		status:          ResourceStatusTransferring,
		maxRetries:      16,
		retriesLeft:     16,
		timeoutFactor:   6.0,
		senderGraceTime: 10.0,
		rtt:             new(1.0),
		lastActivity:    base,
	}

	// max_extra_wait = PER_RETRY_DELAY * sum(1..16) = 0.5 * 136 = 68.
	// max_wait = 1*6*16 + 10 + 68 = 174s.
	const maxWait = 1.0*6.0*16 + 10.0 + 68.0
	deadline := base.Add(time.Duration(maxWait * float64(time.Second)))

	// Just before the deadline: no cancel.
	sleep, _ := r.watchdogStep(base.Add(173 * time.Second))
	wantSleep := deadline.Sub(base.Add(173 * time.Second)).Seconds()
	if sleep <= 0 || sleep < wantSleep-1e-9 || sleep > wantSleep+1e-9 {
		t.Fatalf("before deadline: sleep = %v, want %v", sleep, wantSleep)
	}
	if r.status != ResourceStatusTransferring {
		t.Fatalf("before deadline: status = %v, want TRANSFERRING", r.status)
	}

	// Just after the deadline: cancel.
	r.watchdogStep(deadline.Add(time.Millisecond))
	if r.status != ResourceStatusFailed {
		t.Fatalf("after deadline: status = %v, want FAILED", r.status)
	}
}

// testAwaitingProofResource builds a sender resource in the AWAITING_PROOF
// state with a capture transport and a known hash/expected_proof/rtt so the
// proof-timeout deadline is exactly computable.
func testAwaitingProofResource(t *testing.T, lastPartSent time.Time) (*Resource, *captureTransport) {
	t.Helper()

	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.transport = ct
	link.logger = testSilentLogger()

	payload := []byte("payload-data")
	randomHash := []byte{0x01, 0x02, 0x03, 0x04}
	hash := FullHash(append(append([]byte{}, payload...), randomHash...))
	expectedProof := FullHash(append(append([]byte{}, payload...), hash...))

	r := &Resource{
		link:            link,
		initiator:       true,
		status:          ResourceStatusAwaitingProof,
		hash:            hash,
		expectedProof:   expectedProof,
		maxRetries:      16,
		retriesLeft:     16,
		senderGraceTime: 10.0,
		timeoutFactor:   6.0,
		rtt:             new(1.0),
		lastPartSent:    lastPartSent,
	}
	return r, ct
}

// TestWatchdogAwaitingProof asserts the AWAITING_PROOF branch (Python
// Resource.py:639-658): the timeout factor is reduced to PROOF_TIMEOUT_FACTOR
// (3); before last_part_sent + rtt*3 + sender_grace_time no action is taken;
// past it a cache_request is issued for the expected-proof packet
// (PROOF/RESOURCE_PRF, data = hash+expected_proof), retries_left decrements
// and last_part_sent resets; after exhaustion the resource cancels.
//
// The cache-request hash is the hash of a link-encrypted PROOF packet; link
// encryption uses a random nonce (as does Python's), so the hash is not
// byte-stable across runs. The parity bar is therefore that a cache request
// is issued with a hash of the correct (full packet-hash) length, built from
// the expected proof data — not a cross-run byte match.
func TestWatchdogAwaitingProof(t *testing.T) {
	t.Parallel()

	base := time.Unix(4_000_000, 0)
	r, ct := testAwaitingProofResource(t, base)

	// deadline = base + (1*PROOF_TIMEOUT_FACTOR + sender_grace_time) = base + 13s.
	const deadlineOffset = 1.0*ResourceProofTimeoutFactor + 10.0
	deadline := base.Add(time.Duration(deadlineOffset * float64(time.Second)))

	// Build a reference PROOF/RESOURCE_PRF packet to learn the packet-hash
	// length the cache request must carry.
	refData := append(append([]byte{}, r.hash...), r.expectedProof...)
	refPkt := NewPacket(r.link, refData)
	refPkt.PacketType = PacketProof
	refPkt.Context = ContextResourcePrf
	if err := refPkt.Pack(); err != nil {
		t.Fatalf("reference Pack: %v", err)
	}
	wantHashLen := len(refPkt.PacketHash)

	// Before the deadline: no action, timeout factor still reduced by the
	// branch (Python sets it unconditionally on entering AWAITING_PROOF).
	sleep, _ := r.watchdogStep(base.Add(12 * time.Second))
	if sleep <= 0 {
		t.Fatalf("before deadline: sleep = %v, want > 0", sleep)
	}
	if r.timeoutFactor != ResourceProofTimeoutFactor {
		t.Fatalf("before deadline: timeoutFactor = %v, want %v", r.timeoutFactor, ResourceProofTimeoutFactor)
	}
	if r.retriesLeft != 16 {
		t.Fatalf("before deadline: retriesLeft = %d, want 16", r.retriesLeft)
	}
	if ct.cacheReqCount() != 0 {
		t.Fatalf("before deadline: cacheReq = %d, want 0", ct.cacheReqCount())
	}

	// Past the deadline: cache query issued.
	now1 := deadline.Add(time.Millisecond)
	r.watchdogStep(now1)
	if ct.cacheReqCount() != 1 {
		t.Fatalf("after timeout: cacheReq = %d, want 1", ct.cacheReqCount())
	}
	if r.retriesLeft != 15 {
		t.Fatalf("after timeout: retriesLeft = %d, want 15", r.retriesLeft)
	}
	if !r.lastPartSent.Equal(now1) {
		t.Fatalf("after timeout: lastPartSent = %v, want %v", r.lastPartSent, now1)
	}
	ct.mu.Lock()
	gotHashLen := len(ct.cacheReq[0])
	ct.mu.Unlock()
	if gotHashLen != wantHashLen {
		t.Fatalf("cache request hash len = %d, want %d (full packet hash)", gotHashLen, wantHashLen)
	}
	if r.status != ResourceStatusAwaitingProof {
		t.Fatalf("after timeout: status = %v, want AWAITING_PROOF", r.status)
	}

	// Drive retries to exhaustion, then cancel.
	now := now1
	for r.retriesLeft > 0 {
		now = now.Add(100 * time.Second)
		r.watchdogStep(now)
	}
	if r.status != ResourceStatusAwaitingProof {
		t.Fatalf("after all retries: status = %v, want AWAITING_PROOF (not yet cancelled)", r.status)
	}
	r.watchdogStep(now.Add(100 * time.Second))
	if r.status != ResourceStatusFailed {
		t.Fatalf("after exhaustion: status = %v, want FAILED", r.status)
	}
	if ct.cacheReqCount() != r.maxRetries {
		t.Fatalf("after exhaustion: cacheReq = %d, want %d", ct.cacheReqCount(), r.maxRetries)
	}
}
