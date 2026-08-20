// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

// TestResourceConstants asserts every Resource timing/state constant the
// watchdog depends on equals the live value captured from Python's
// RNS/Resource.py:99-137 via `import RNS`. These are the golden values the
// watchdog state machine is computed against, so a drift
// here would silently change every retry/timeout.
func TestResourceConstants(t *testing.T) {
	t.Parallel()

	// name -> Python constant name; got is the Go constant (typed).
	tests := []struct {
		name string
		py   string
		got  any
	}{
		{"ProcessingGrace", "PROCESSING_GRACE", ResourceProcessingGrace},
		{"RetryGraceTime", "RETRY_GRACE_TIME", ResourceRetryGraceTime},
		{"PerRetryDelay", "PER_RETRY_DELAY", ResourcePerRetryDelay},
		{"WatchdogMaxSleep", "WATCHDOG_MAX_SLEEP", ResourceWatchdogMaxSleep},
		{"ProofTimeoutFactor", "PROOF_TIMEOUT_FACTOR", ResourceProofTimeoutFactor},
		{"PartTimeoutFactor", "PART_TIMEOUT_FACTOR", ResourcePartTimeoutFactor},
		{"PartTimeoutFactorAfterRtt", "PART_TIMEOUT_FACTOR_AFTER_RTT", ResourcePartTimeoutFactorAfterRtt},
		{"SenderGraceTime", "SENDER_GRACE_TIME", ResourceSenderGraceTime},
		{"HmuWaitFactor", "HMU_WAIT_FACTOR", ResourceHmuWaitFactor},
		{"MaxRetries", "MAX_RETRIES", ResourceMaxRetries},
		{"MaxAdvRetries", "MAX_ADV_RETRIES", ResourceMaxAdvRetries},
		{"WindowFlexibility", "WINDOW_FLEXIBILITY", ResourceWindowFlexibility},
	}

	live := pythonResourceConstants(t)

	for _, tc := range tests {
		// Coerce the live Python value to the Go constant's type so the
		// comparison is type-faithful (float64 constants vs int constants).
		var want any
		switch tc.got.(type) {
		case float64:
			want = live[tc.py]
		case int:
			want = int(live[tc.py])
		default:
			t.Fatalf("Resource constant %s has unexpected Go type %T", tc.name, tc.got)
		}
		if !reflect.DeepEqual(tc.got, want) {
			t.Fatalf("Resource constant %s = %v (%T), want live Python %v (%T)", tc.name, tc.got, tc.got, want, want)
		}
	}
}

// testSilentLogger returns a logger that emits nothing, so Accept's debug
// logging and the failed-send path in the spawned RequestNext goroutine do
// not clutter test output.
func testSilentLogger() *Logger {
	l := NewLogger()
	l.SetLogLevel(LogNone)
	return l
}

// testReceiverResource builds an initiator resource, packs its advertisement
// (mirroring Resource.Advertise's advertisement construction, but without
// sending), and accepts it on a bare active link, returning the receiver
// Resource. The spawned RequestNext goroutine fails harmlessly (no transport
// on the bare link); the watchdog-relevant fields it does not touch are
// stable for assertion.
func testReceiverResource(t *testing.T) *Resource {
	t.Helper()

	link := testActiveResourceLink(t)
	link.trafficTimeoutFactor = 6.0
	link.rtt = 1.0
	link.establishmentCost = 256
	link.logger = testSilentLogger()

	data := bytes.Repeat([]byte("A"), 256)
	initR, err := NewResourceWithOptions(data, link, ResourceOptions{})
	if err != nil {
		t.Fatalf("NewResourceWithOptions: %v", err)
	}

	hashmapRaw := make([]byte, 0, len(initR.hashmap)*ResourceMapHashLen)
	for _, mh := range initR.hashmap {
		hashmapRaw = append(hashmapRaw, mh...)
	}
	adv := &ResourceAdvertisement{
		T:           initR.size,
		D:           initR.uncompressedSize,
		H:           initR.hash,
		R:           initR.randomHash,
		O:           initR.originalHash,
		N:           initR.totalParts,
		L:           1,
		I:           1,
		Q:           initR.requestID,
		M:           hashmapRaw,
		Encrypted:   initR.encrypted,
		Compressed:  initR.compressed,
		HasMetadata: len(initR.metadata) > 0,
	}
	advData, err := adv.Pack()
	if err != nil {
		t.Fatalf("adv.Pack: %v", err)
	}
	pkt := &Packet{Destination: link, Data: advData}

	r, err := Accept(pkt, nil, nil, nil)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if r == nil {
		t.Fatal("Accept returned nil resource")
	}
	// Accept now launches the watchdog loop (mirroring Python). Stop it so
	// this unit helper does not leave a background goroutine running on a
	// bare link; watchdogJobID remains 1 (set by WatchdogJob).
	r.stopWatchdog()
	return r
}

// TestResourceStateInitiator asserts the watchdog state fields initialize to
// the Python defaults (Resource.py:335-365) on a freshly constructed
// initiator (sender) resource, before Advertise is called.
func TestResourceStateInitiator(t *testing.T) {
	t.Parallel()

	link := testActiveResourceLink(t)
	link.trafficTimeoutFactor = 6.0
	link.logger = testSilentLogger()

	r, err := NewResourceWithOptions(bytes.Repeat([]byte("A"), 256), link, ResourceOptions{})
	if err != nil {
		t.Fatalf("NewResourceWithOptions: %v", err)
	}

	if r.maxRetries != ResourceMaxRetries {
		t.Fatalf("maxRetries = %d, want %d", r.maxRetries, ResourceMaxRetries)
	}
	if r.maxAdvRetries != ResourceMaxAdvRetries {
		t.Fatalf("maxAdvRetries = %d, want %d", r.maxAdvRetries, ResourceMaxAdvRetries)
	}
	if r.retriesLeft != r.maxRetries {
		t.Fatalf("retriesLeft = %d, want %d (maxRetries)", r.retriesLeft, r.maxRetries)
	}
	if r.timeoutFactor != link.trafficTimeoutFactor {
		t.Fatalf("timeoutFactor = %v, want %v (link.trafficTimeoutFactor)", r.timeoutFactor, link.trafficTimeoutFactor)
	}
	if r.partTimeoutFactor != ResourcePartTimeoutFactor {
		t.Fatalf("partTimeoutFactor = %v, want %v", r.partTimeoutFactor, ResourcePartTimeoutFactor)
	}
	if r.senderGraceTime != ResourceSenderGraceTime {
		t.Fatalf("senderGraceTime = %v, want %v", r.senderGraceTime, ResourceSenderGraceTime)
	}
	if r.windowFlexibility != ResourceWindowFlexibility {
		t.Fatalf("windowFlexibility = %d, want %d", r.windowFlexibility, ResourceWindowFlexibility)
	}
	if r.sdu != MDU {
		t.Fatalf("sdu = %d, want %d (MDU, link with mtu=0,mdu=MDU)", r.sdu, MDU)
	}
	if r.outstandingParts != 0 {
		t.Fatalf("outstandingParts = %d, want 0", r.outstandingParts)
	}
	if r.waitingForHmu {
		t.Fatalf("waitingForHmu = true, want false")
	}
	if r.reqRespRttRate != 0 {
		t.Fatalf("reqRespRttRate = %v, want 0", r.reqRespRttRate)
	}
	if r.reqDataRttRate != 0 {
		t.Fatalf("reqDataRttRate = %v, want 0", r.reqDataRttRate)
	}
	if r.rtt != nil {
		t.Fatalf("rtt = %v, want nil (Python None)", r.rtt)
	}
	if r.eifr != nil {
		t.Fatalf("eifr = %v, want nil (Python None)", r.eifr)
	}
	if r.previousEifr != nil {
		t.Fatalf("previousEifr = %v, want nil (Python None)", r.previousEifr)
	}
	if !r.advSent.IsZero() {
		t.Fatalf("advSent = %v, want zero (unset before advertise)", r.advSent)
	}
	if !r.lastPartSent.IsZero() {
		t.Fatalf("lastPartSent = %v, want zero (unset)", r.lastPartSent)
	}
	if r.watchdogJobID != 0 {
		t.Fatalf("watchdogJobID = %d, want 0", r.watchdogJobID)
	}
	if r.advertisementPacket != nil {
		t.Fatalf("advertisementPacket = %v, want nil (unset before advertise)", r.advertisementPacket)
	}
	if !r.initiator {
		t.Fatalf("initiator = false, want true")
	}
}

// TestResourceStateReceiver asserts the watchdog state fields initialize to
// the Python defaults on a receiver resource constructed via Accept
// (Resource.py:167-239 accept + __init__).
func TestResourceStateReceiver(t *testing.T) {
	t.Parallel()

	r := testReceiverResource(t)

	if r.maxRetries != ResourceMaxRetries {
		t.Fatalf("maxRetries = %d, want %d", r.maxRetries, ResourceMaxRetries)
	}
	if r.maxAdvRetries != ResourceMaxAdvRetries {
		t.Fatalf("maxAdvRetries = %d, want %d", r.maxAdvRetries, ResourceMaxAdvRetries)
	}
	if r.retriesLeft != r.maxRetries {
		t.Fatalf("retriesLeft = %d, want %d", r.retriesLeft, r.maxRetries)
	}
	if r.timeoutFactor != 6.0 {
		t.Fatalf("timeoutFactor = %v, want 6.0", r.timeoutFactor)
	}
	if r.partTimeoutFactor != ResourcePartTimeoutFactor {
		t.Fatalf("partTimeoutFactor = %v, want %v", r.partTimeoutFactor, ResourcePartTimeoutFactor)
	}
	if r.senderGraceTime != ResourceSenderGraceTime {
		t.Fatalf("senderGraceTime = %v, want %v", r.senderGraceTime, ResourceSenderGraceTime)
	}
	if r.windowFlexibility != ResourceWindowFlexibility {
		t.Fatalf("windowFlexibility = %d, want %d", r.windowFlexibility, ResourceWindowFlexibility)
	}
	if r.sdu != MDU {
		t.Fatalf("sdu = %d, want %d", r.sdu, MDU)
	}
	if r.outstandingParts != 0 {
		t.Fatalf("outstandingParts = %d, want 0", r.outstandingParts)
	}
	if r.waitingForHmu {
		t.Fatalf("waitingForHmu = true, want false")
	}
	if r.rtt != nil {
		t.Fatalf("rtt = %v, want nil", r.rtt)
	}
	// Accept launches the watchdog, whose first tick calls updateEifr
	// (mirroring Python Resource.__watchdog_job, Resource.py:599) and
	// sets eifr to the establishment-based estimate establishment_cost*8
	// / rtt. testReceiverResource stops the watchdog only after this
	// first tick has completed (stopWatchdog blocks for goroutine exit),
	// so eifr is the computed rate (2048.0 for ec=256, rtt=1.0) rather
	// than the construction default (Python None). The rate itself is
	// golden-tested by the updateEifr cases below.
	if r.eifr == nil {
		t.Fatalf("eifr = nil, want the establishment-based rate from the first watchdog tick")
	}
	if wantEifr := float64(256) * 8 / 1.0; *r.eifr != wantEifr {
		t.Fatalf("eifr = %v, want %v (establishmentCost*8/rtt)", *r.eifr, wantEifr)
	}
	if r.previousEifr != nil {
		t.Fatalf("previousEifr = %v, want nil", r.previousEifr)
	}
	if r.advertisementPacket == nil {
		t.Fatal("advertisementPacket = nil, want the accepted advertisement packet")
	}
	// Accept launches the watchdog job (mirroring Python Resource.py:234),
	// which increments watchdogJobID from 0 to 1.
	if r.watchdogJobID != 1 {
		t.Fatalf("watchdogJobID = %d, want 1 (Accept starts the watchdog)", r.watchdogJobID)
	}
	if r.initiator {
		t.Fatalf("initiator = true, want false (receiver)")
	}
	if math.Signbit(r.reqDataRttRate) {
		t.Fatalf("reqDataRttRate = %v, want 0", r.reqDataRttRate)
	}
}

// TestUpdateEifr asserts Resource.updateEifr (Python Resource.update_eifr,
// Resource.py:552-562) computes eifr and pushes it onto link.expected_rate
// for every input combination. The expected eifr for each case is captured
// live by running the real Python Resource.update_eifr (via a bare
// __new__-constructed Resource with a fake link) over the same inputs.
func TestUpdateEifr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		rtt            *float64
		linkRTT        float64
		establishment  float64
		reqDataRttRate float64
		previousEifr   *float64
	}{
		{
			name:           "rtt_unset_link_rtt_0.5_ec_1000_no_prev",
			rtt:            nil,
			linkRTT:        0.5,
			establishment:  1000,
			reqDataRttRate: 0,
			previousEifr:   nil,
		},
		{
			name:           "rtt_0.5_rdrr_200",
			rtt:            new(0.5),
			linkRTT:        0.5,
			establishment:  9999,
			reqDataRttRate: 200,
			previousEifr:   nil,
		},
		{
			name:           "rtt_unset_prev_5000",
			rtt:            nil,
			linkRTT:        0.5,
			establishment:  9999,
			reqDataRttRate: 0,
			previousEifr:   new(float64(5000)),
		},
		{
			name:           "rtt_unset_link_rtt_1.0_ec_256",
			rtt:            nil,
			linkRTT:        1.0,
			establishment:  256,
			reqDataRttRate: 0,
			previousEifr:   nil,
		},
		{
			// rtt is set, but req_data_rtt_rate==0 and previous_eifr is
			// set, so previous_eifr wins (rtt only matters in the
			// establishment_cost fallback branch).
			name:           "rtt_2.0_rdrr_0_prev_3333",
			rtt:            new(2.0),
			linkRTT:        9.9,
			establishment:  9999,
			reqDataRttRate: 0,
			previousEifr:   new(float64(3333)),
		},
	}

	pyCases := make([]pyEifrCase, len(cases))
	for i, tc := range cases {
		pyCases[i] = pyEifrCase{
			Rtt:            tc.rtt,
			LinkRtt:        tc.linkRTT,
			Establishment:  tc.establishment,
			ReqDataRttRate: tc.reqDataRttRate,
			PreviousEifr:   tc.previousEifr,
		}
	}
	wantEifr := pythonUpdateEifr(t, pyCases)

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link := testActiveResourceLink(t)
			link.rtt = tc.linkRTT
			link.establishmentCost = tc.establishment

			r := &Resource{link: link}
			r.rtt = tc.rtt
			r.reqDataRttRate = tc.reqDataRttRate
			r.previousEifr = tc.previousEifr

			r.updateEifr()

			if r.eifr == nil {
				t.Fatalf("eifr = nil, want %v", wantEifr[i])
			}
			if *r.eifr != wantEifr[i] {
				t.Fatalf("eifr = %v, want live Python %v", *r.eifr, wantEifr[i])
			}
			if link.expectedRate != wantEifr[i] {
				t.Fatalf("link.expectedRate = %v, want live Python %v", link.expectedRate, wantEifr[i])
			}
		})
	}
}
