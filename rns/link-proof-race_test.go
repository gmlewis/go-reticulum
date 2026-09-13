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

	"github.com/gmlewis/go-reticulum/rns/crypto"
)

// TestLinkLastProofConcurrentWithWatchdog drives the transport's link-proof
// path (PacketReceipt.ValidateLinkProof, which stamps the link's proof
// timestamp) from one goroutine while the link watchdog reads the same
// timestamp through effectiveLastInbound from another. The receipt path runs
// inside TransportSystem.Inbound's receipt loop, which holds ts.mu and the
// receipt's own mutex, so a plain l.mu-guarded field would leave the write
// unsynchronised with the watchdog's read; the proof timestamp is therefore an
// atomic snapshot. Run under -race: this test reports a DATA RACE on the field
// if the write ever stops being safe against the watchdog read.
func TestLinkLastProofConcurrentWithWatchdog(t *testing.T) {
	t.Parallel()

	signer, err := crypto.GenerateEd25519PrivateKey()
	mustTest(t, err)

	link := &Link{logger: NewLogger(), peerSigPub: signer.PublicKey()}
	link.status.Store(LinkActive)
	link.lastInbound = time.Now()
	link.lastOutbound = link.lastInbound
	// A full keepalive interval keeps the watchdog on its read-only path:
	// neither the keepalive nor the stale branch fires during a test run.
	link.keepalive = LinkKeepaliveMax
	link.staleTime = time.Duration(LinkStaleFactor) * LinkKeepaliveMax

	hash := bytes.Repeat([]byte{0xAB}, HashLength/8)
	proof := append(append([]byte(nil), hash...), signer.Sign(hash)...)
	receipt := &PacketReceipt{Hash: hash}

	const rounds = 1000
	start := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		<-start
		for i := range rounds {
			if !receipt.ValidateLinkProof(proof, link, nil) {
				t.Errorf("ValidateLinkProof rejected a valid proof on round %v", i)
				return
			}
		}
	})

	wg.Go(func() {
		<-start
		for i := range rounds {
			if inbound := link.effectiveLastInbound(); inbound.IsZero() {
				t.Errorf("effectiveLastInbound() is the zero time on round %v", i)
				return
			}
			link.watchdogStep(time.Now())
		}
	})

	close(start)
	wg.Wait()

	if got := link.lastProofTime(); got.IsZero() {
		t.Error("lastProofTime() is the zero time after validating proofs")
	}
}
