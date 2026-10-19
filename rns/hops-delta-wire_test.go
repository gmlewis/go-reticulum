// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestHopsDeltaRewritesTheEmittedHopByte puts the local_hops_delta disguise on a real
// wire and checks the byte that arrives. The gate, transform and forwarding expressions
// are pinned against Python by TestHopsDeltaVectorsAgainstPython
// (rns/hops-delta-vectors_test.go); what no test did was emit a frame and read its hop
// byte back.
//
// Python draws the delta once per transport as `(ord(os.urandom(1))%6)+2`
// (Transport.py:328) and rewrites the hop byte of a locally-originated unicast frame
// (`should_apply_delta` / `mangle_hops`, Transport.py:1384,1555-1560). The draw is made
// deterministic here through the injected byte source: (3%6)+2 = 5.
func TestHopsDeltaRewritesTheEmittedHopByte(t *testing.T) {
	t.Parallel()

	sender := topologyTransport(t, "delta-wire-sender", false, false)
	receiver := topologyTransport(t, "delta-wire-receiver", false, false)

	// Capture on the RECEIVING end: the transport hands every frame it sends to the
	// peer's OnReceive, so this sees exactly the bytes that crossed the link.
	var (
		mu     sync.Mutex
		frames [][]byte
	)
	pipeSend := interfaces.NewPipeInterface("delta-wire-send", func(data []byte, iface interfaces.Interface) {
		sender.Inbound(data, iface)
	})
	pipeRecv := interfaces.NewPipeInterface("delta-wire-recv", func(data []byte, iface interfaces.Interface) {
		mu.Lock()
		frames = append(frames, append([]byte{}, data...))
		mu.Unlock()
		receiver.Inbound(data, iface)
	})
	pipeSend.SetOther(pipeRecv)
	pipeRecv.SetOther(pipeSend)
	sender.RegisterInterface(pipeSend)
	receiver.RegisterInterface(pipeRecv)
	t.Cleanup(func() {
		_ = pipeSend.Detach()
		_ = pipeRecv.Detach()
	})

	sender.SetLocalHopsDeltaRand(func() byte { return 3 })
	sender.SetLocalHopsDeltaEnabled(true)
	if got := sender.LocalHopsDelta(); got != 5 {
		t.Fatalf("local delta = %v, want 5", got)
	}

	dest := mustTestNewDestination(t, receiver, receiver.identity, DestinationIn, DestinationSingle, "delta", "wire")
	// The sender's own view of the destination. Sending through the receiver's IN object
	// would dispatch on the receiver's transport, a self-send that never reaches the wire.
	outDest := mustTestNewDestination(t, sender, receiver.identity, DestinationOut, DestinationSingle, "delta", "wire")

	if err := dest.Announce(nil); err != nil {
		t.Fatalf("announce: %v", err)
	}
	waitForPath(t, sender, outDest.Hash)

	// The announce has done its job; the frame under test is the one after it.
	mu.Lock()
	frames = nil
	mu.Unlock()

	if err := NewPacket(outDest, []byte("delta payload")).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}

	testutils.EventuallyFatal(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) > 0
	}, "the unicast frame never crossed the link")

	mu.Lock()
	frame := frames[len(frames)-1]
	mu.Unlock()

	if len(frame) < 2 {
		t.Fatalf("captured frame is too short to carry a hop byte: %x", frame)
	}
	if got := frame[1]; got != 5 {
		t.Errorf("the emitted frame's hop byte = %v, want the sender's delta 5 (Transport.py:1384 mangles a locally-originated unicast frame)", got)
	}
}
