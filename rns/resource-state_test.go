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
// watchdog depends on equals the value captured from Python's
// RNS/Resource.py:97-137 (live `python3.14 -c "import RNS; ..."` run, rns
// 1.3.5). These are the golden values the watchdog state machine in
// Phase E.3-E.6 is computed against, so a drift here would silently change
// every retry/timeout.
func TestResourceConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"ProcessingGrace", ResourceProcessingGrace, float64(1.0)},
		{"RetryGraceTime", ResourceRetryGraceTime, float64(0.25)},
		{"PerRetryDelay", ResourcePerRetryDelay, float64(0.5)},
		{"WatchdogMaxSleep", ResourceWatchdogMaxSleep, float64(1.0)},
		{"ProofTimeoutFactor", ResourceProofTimeoutFactor, float64(3.0)},
		{"PartTimeoutFactor", ResourcePartTimeoutFactor, float64(4.0)},
		{"PartTimeoutFactorAfterRtt", ResourcePartTimeoutFactorAfterRtt, float64(2.0)},
		{"SenderGraceTime", ResourceSenderGraceTime, float64(10.0)},
		{"HmuWaitFactor", ResourceHmuWaitFactor, float64(3.5)},
		{"MaxRetries", ResourceMaxRetries, int(16)},
		{"MaxAdvRetries", ResourceMaxAdvRetries, int(4)},
		{"WindowFlexibility", ResourceWindowFlexibility, int(4)},
	}

	for _, tc := range tests {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Fatalf("Resource constant %s = %v (%T), want %v (%T)", tc.name, tc.got, tc.got, tc.want, tc.want)
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
// Resource.py:543-558) computes eifr and pushes it onto link.expected_rate
// for every input combination. Golden values were captured from a live
// `import RNS` run by constructing a Resource via __new__ and setting the
// same rtt / req_data_rtt_rate / previous_eifr / link.rtt /
// link.establishment_cost attributes, then calling update_eifr().
func TestUpdateEifr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		rtt            *float64
		linkRTT        float64
		establishment  float64
		reqDataRttRate float64
		previousEifr   *float64
		wantEifr       float64
	}{
		{
			name:           "rtt_unset_link_rtt_0.5_ec_1000_no_prev",
			rtt:            nil,
			linkRTT:        0.5,
			establishment:  1000,
			reqDataRttRate: 0,
			previousEifr:   nil,
			wantEifr:       16000.0,
		},
		{
			name:           "rtt_0.5_rdrr_200",
			rtt:            new(0.5),
			linkRTT:        0.5,
			establishment:  9999,
			reqDataRttRate: 200,
			previousEifr:   nil,
			wantEifr:       1600.0,
		},
		{
			name:           "rtt_unset_prev_5000",
			rtt:            nil,
			linkRTT:        0.5,
			establishment:  9999,
			reqDataRttRate: 0,
			previousEifr:   new(float64(5000)),
			wantEifr:       5000.0,
		},
		{
			name:           "rtt_unset_link_rtt_1.0_ec_256",
			rtt:            nil,
			linkRTT:        1.0,
			establishment:  256,
			reqDataRttRate: 0,
			previousEifr:   nil,
			wantEifr:       2048.0,
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
			wantEifr:       3333.0,
		},
	}

	for _, tc := range cases {
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
				t.Fatalf("eifr = nil, want %v", tc.wantEifr)
			}
			if *r.eifr != tc.wantEifr {
				t.Fatalf("eifr = %v, want %v", *r.eifr, tc.wantEifr)
			}
			if link.expectedRate != tc.wantEifr {
				t.Fatalf("link.expectedRate = %v, want %v", link.expectedRate, tc.wantEifr)
			}
		})
	}
}
