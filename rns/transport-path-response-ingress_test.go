// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSharedInstanceRecordsPathResponseAnsweringPendingRequest verifies that a
// PATH_RESPONSE announce which answers an outstanding path request is installed
// in the shared instance's path table before it is relayed to the requestor.
//
// The two halves have to agree: the instance relays the response to the client
// that asked (transport.go forwardPathResponseToRequesters), and it must also
// record the path itself, because the client's link request is stamped with the
// instance's transport id and forwarded from the instance's path table. If the
// response is relayed while handleAnnounce's ingress-limit gate still holds it,
// the client learns a path that its own instance will refuse to forward for:
// the link request is dropped, the establishment window (hop count unknown, so
// PathfinderM hops) elapses, and the client expires the path and retries at
// PathfinderM hops — a state it cannot leave while the hub keeps announcing
// through an ingress-limited interface.
func TestSharedInstanceRecordsPathResponseAnsweringPendingRequest(t *testing.T) {
	logger := testSilentLogger()

	tsS := NewTransportSystem(logger) // the shared instance
	tsS.identity = mustTestNewIdentity(t, true)
	tsS.SetConnectedToSharedInstance(false)
	tsS.SetEnabled(true)
	if err := tsS.Start(testutils.TempDir(t, "rns-pathresp-hold")); err != nil {
		t.Fatalf("tsS.Start: %v", err)
	}

	// The interface the hub is reachable over, and the local interface of an
	// attached client that asked for the path.
	remote := interfaces.NewPipeInterface("remote", func([]byte, interfaces.Interface) {})
	requester := interfaces.NewPipeInterface("requestor", func([]byte, interfaces.Interface) {})
	tsS.RegisterInterface(remote)
	tsS.RegisterInterface(requester)
	t.Cleanup(func() {
		_ = remote.Detach()
		_ = requester.Detach()
	})

	// A remote peer's path-response announce, exactly as the hub sends it when
	// it answers the instance's relayed path request.
	tsD := NewTransportSystem(logger)
	tsD.identity = mustTestNewIdentity(t, true)
	destD := mustTestNewDestination(t, tsD, tsD.identity, DestinationIn, DestinationSingle, "lxmf", "delivery")
	packet, err := destD.buildAnnouncePacket(nil, true)
	if err != nil {
		t.Fatalf("buildAnnouncePacket: %v", err)
	}
	if packet.Context != ContextPathResponse {
		t.Fatalf("premise violated: announce context = %v, want PATH_RESPONSE", packet.Context)
	}
	if err := packet.Pack(); err != nil {
		t.Fatalf("pack announce: %v", err)
	}

	// Record the attached client's outstanding request, as forwardPathRequest
	// does for a request that arrived from a local client interface.
	tsS.mu.Lock()
	tsS.pendingPathRequests[string(destD.Hash)] = []interfaces.Interface{requester}
	tsS.mu.Unlock()

	// Drive the remote interface into an announce burst, so this announce is
	// one the ingress limiter would hold if it were not answering a request.
	for range 8 {
		remote.ReceivedAnnounceSize(200)
	}
	if !remote.ShouldIngressLimit() {
		t.Fatal("premise violated: remote interface is not ingress limiting")
	}

	tsS.Inbound(packet.Raw, remote)

	// The announce is processed asynchronously by the inbound drainer.
	waitFor(t, 5*time.Second, "the shared instance records the requested path", func() bool {
		return tsS.HopsTo(destD.Hash) != PathfinderM
	})
}
