// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestDisablePropagationAnnouncesPropagationNode pins Python's
// LXMRouter.disable_propagation (LXMF/LXMRouter.py:692-694, v1.1.1):
//
//	def disable_propagation(self):
//	    self.propagation_node = False
//	    self.announce_propagation_node()
//
// Clearing the serving flag is followed by a propagation-node announce, so
// peers learn that this router's propagation address exists but is no longer
// serving (the payload's node_state element goes false). That declaration is
// what makes the address a NomadNet client derives from a node's identity — the
// "LXMF Propagation Node Address is <hex>" line, Network.py:629 — a real,
// announced destination. Without it the address refers to nothing at all.
func TestDisablePropagationAnnouncesPropagationNode(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	var announces int
	var delays []time.Duration
	router.propagationAnnounceSleep = func(d time.Duration) {
		announces++
		delays = append(delays, d)
	}

	// Precondition: an enabled router declares node_state true.
	router.propagationEnabled = true
	if serving, ok := PropagationNodeStateFromAppData(router.getPropagationNodeAppDataLocked()); !ok || !serving {
		t.Fatalf("enabled router declares (serving=%v, ok=%v), want (true, true)", serving, ok)
	}

	router.DisablePropagation()

	if router.PropagationEnabled() {
		t.Error("PropagationEnabled() = true after DisablePropagation, want false")
	}
	if announces != 1 {
		t.Errorf("propagation announce path ran %v times, want 1 (Python announces on disable)", announces)
	}
	if len(delays) == 1 && delays[0] <= 0 {
		t.Errorf("announce delay = %v, want the positive NODE_ANNOUNCE_DELAY", delays[0])
	}

	// The withdrawal is declared with node_state false, and the declared
	// destination is this router's own lxmf.propagation address.
	appData := router.getPropagationNodeAppDataLocked()
	serving, ok := PropagationNodeStateFromAppData(appData)
	if !ok {
		t.Fatalf("disabled router's announce carries no node-state element: %x", appData)
	}
	if serving {
		t.Error("node_state = true after DisablePropagation, want false")
	}
	if router.propagationDestination == nil {
		t.Fatal("router has no propagation destination to declare")
	}
	if want := rns.CalculateHash(router.identity, AppName, "propagation"); !bytes.Equal(router.propagationDestination.Hash, want) {
		t.Errorf("declared destination = %x, want the identity's lxmf.propagation address %x",
			router.propagationDestination.Hash, want)
	}
}

// TestDisablePropagationAnnounceIsIdempotentAndSafeWhenClosed verifies the
// announce in DisablePropagation cannot panic or wedge: repeated disables
// each declare the withdrawal, and a router with no propagation destination
// (closed) is a no-op.
func TestDisablePropagationAnnounceIsIdempotentAndSafeWhenClosed(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	var announces int
	router.propagationAnnounceSleep = func(time.Duration) { announces++ }

	router.DisablePropagation()
	router.DisablePropagation()
	if announces != 2 {
		t.Errorf("announce path ran %v times over two disables, want 2", announces)
	}

	router.propagationDestination = nil
	router.DisablePropagation() // must not panic with nothing to announce
	if announces != 2 {
		t.Errorf("announce path ran %v times with no propagation destination, want 2 (no-op)", announces)
	}
}

// TestPropagationNodeStateFromAppData pins the node-state reader against real
// announce payloads captured from the live network, plus the acceptance rules of
// Python's pn_announce_data_is_valid (all seven elements required).
func TestPropagationNodeStateFromAppData(t *testing.T) {
	t.Parallel()

	// validPropagationAppData builds a seven-element payload with node_state
	// true (see router-propannounce-outboundtrigger_test.go).
	servingPayload := validPropagationAppData(t)

	cases := []struct {
		name    string
		appData []byte
		serving bool
		ok      bool
	}{
		{
			// A live golxmd propagation-node announce: node_state true, metadata
			// names the node.
			name:    "live_serving_node",
			appData: mustHexDecode(t, "97c2d26ad36b28c3cb4070000000000000cb40c4000000000000931003108101c417676f2d6e6f6d61646e65742e6475636b646e732e6f7267"),
			serving: true,
			ok:      true,
		},
		{
			// A live announce from a node that has propagation switched off:
			// the address is declared, node_state false.
			name:    "live_not_serving_node",
			appData: mustHexDecode(t, "97c2ce6ad27d40c2cb4070000000000000cb40c40000000000009310031282ccfea46c786d6400a5312e322e30"),
			serving: false,
			ok:      true,
		},
		{"packed_serving", servingPayload, true, true},
		{"nil_payload", nil, false, false},
		{"empty_payload", []byte{}, false, false},
		// The original raw-string announce format has no state element.
		{"raw_string", []byte("Node A"), false, false},
		// Six elements: pn_announce_data_is_valid rejects it.
		{"too_short", mustHexDecode(t, "96c2ce6ad36b28c3cb4070000000000000cb40c40000000000009310031081"), false, false},
		// Seven elements but the state slot is not a boolean.
		{"state_not_bool", mustHexDecode(t, "97c2ce6ad36b28c0cb4070000000000000cb40c4000000000000931003108101c417676f2d6e6f6d61646e65742e6475636b646e732e6f7267"), false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			serving, ok := PropagationNodeStateFromAppData(tc.appData)
			if serving != tc.serving || ok != tc.ok {
				t.Errorf("PropagationNodeStateFromAppData(%s) = (serving=%v, ok=%v), want (%v, %v)",
					tc.name, serving, ok, tc.serving, tc.ok)
			}
		})
	}
}
