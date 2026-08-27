// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestAutoProcessIncomingDeliversWhileEchoStalled verifies the LAN data plane
// keeps delivering frames even when the self-echo health monitor has flagged
// every interface timed out (ai.online == 0).
//
// Parity: Python AutoInterface never couples inbound data processing to echo
// freshness — self.online is set once in final_init (AutoInterface.py:350) and
// cleared only on detach (:607); a missed echo merely logs and marks the
// interface timed out (:463-467), which nothing else consumes. The Go port's
// processIncoming used to drop EVERY datagram while online==0, silently
// blacking out the whole LAN plane for the duration of an echo gap (Wi-Fi
// power-save, AP multicast rate-limiting, interface roam) while sockets stayed
// open and TCP planes kept flowing — the observed "node deaf to fleet,
// relays fine" window on Jetson/Pi-class hosts.
func TestAutoProcessIncomingDeliversWhileEchoStalled(t *testing.T) {
	t.Parallel()
	got := make(chan string, 4)
	ai := &AutoInterface{
		BaseInterface: NewBaseInterface("auto-echo-stall", ModeFull, AutoBitrateGuess),
		inboundHandler: func(data []byte, iface Interface) {
			got <- string(data)
		},
		spawnedInterfaces: map[string]*AutoInterfacePeer{},
		recentFrames:      map[[32]byte]time.Time{},
		multiIfDequeTTL:   30 * time.Millisecond,
	}
	ai.running.Store(1)
	// Health monitor currently says the interface is offline (echo gap).
	atomic.StoreInt32(&ai.online, 0)

	ai.spawnedInterfaces["fe80::9"] = &AutoInterfacePeer{
		BaseInterface: NewBaseInterface("p9", ModeFull, AutoBitrateGuess),
		owner:         ai,
		addr:          "fe80::9",
		interfaceName: "if0",
	}

	payload := []byte("data-during-echo-gap")
	ai.processIncoming(payload, "fe80::9")

	select {
	case d := <-got:
		if d != string(payload) {
			t.Fatalf("delivered wrong payload %q", d)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("inbound frame dropped while online==0; data plane must not be gated on echo freshness")
	}
}
