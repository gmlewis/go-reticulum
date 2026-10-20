// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestRouterAccessors pins the accessors Python's LXMRouter exposes:
// requires_authentication, ignore_stamps, set_retain_node_lxms,
// disallow_control, get_outbound_stamp_cost,
// set/get_inbound_propagation_node and
// get_propagation_node_announce_metadata (LXMRouter.py:319-322,396-401,
// 444-462,483-486,699-700).
func TestRouterAccessors(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	hash := bytes.Repeat([]byte{0x11}, rns.TruncatedHashLength/8)

	if router.RequiresAuthentication() {
		t.Error("RequiresAuthentication = true by default, want false")
	}
	router.SetAuthRequired(true)
	if !router.RequiresAuthentication() {
		t.Error("SetAuthRequired(true) did not take effect")
	}

	router.EnforceStamps()
	if !router.StampsEnforced() {
		t.Fatal("EnforceStamps did not take effect")
	}
	router.IgnoreStamps()
	if router.StampsEnforced() {
		t.Error("IgnoreStamps did not clear enforcement")
	}

	router.SetRetainNodeLXMs(true)
	if !router.RetainNodeLXMs() {
		t.Error("SetRetainNodeLXMs(true) did not take effect")
	}
	router.SetRetainNodeLXMs(false)
	if router.RetainNodeLXMs() {
		t.Error("SetRetainNodeLXMs(false) did not take effect")
	}

	router.AllowControl(hash)
	router.DisallowControl(hash)
	if _, ok := router.controlAllowed[string(hash)]; ok {
		t.Error("DisallowControl did not remove the identity")
	}

	if got := router.GetOutboundStampCost(hash); got != nil {
		t.Errorf("GetOutboundStampCost(unknown) = %v, want nil", got)
	}
	router.mu.Lock()
	router.outboundStampCosts[string(hash)] = outboundStampCostEntry{stampCost: int64(7)}
	router.mu.Unlock()
	if got := router.GetOutboundStampCost(hash); got != int64(7) {
		t.Errorf("GetOutboundStampCost = %v, want 7", got)
	}

	if err := router.SetInboundPropagationNode(hash); err == nil {
		t.Error("SetInboundPropagationNode = nil error, want the reference's not-implemented error")
	}
	if err := router.SetOutboundPropagationNode(hash); err != nil {
		t.Fatalf("SetOutboundPropagationNode: %v", err)
	}
	if got := router.GetInboundPropagationNode(); !bytes.Equal(got, hash) {
		t.Errorf("GetInboundPropagationNode = %x, want the outbound node %x", got, hash)
	}

	if metadata := router.GetPropagationNodeAnnounceMetadata(); len(metadata) != 0 {
		t.Errorf("metadata for an unnamed router = %v, want empty", metadata)
	}
	router.mu.Lock()
	router.name = "Test Node"
	metadata := router.propagationNodeAnnounceMetadataLocked()
	appData := router.getPropagationNodeAppDataLocked()
	router.mu.Unlock()
	if got := string(metadata[PNMetaName].([]byte)); got != "Test Node" {
		t.Errorf("metadata name = %q, want Test Node", got)
	}
	// The announce payload carries the same metadata; the reference's own
	// parser must read the name back out of it.
	name, ok := PNNameFromAppData(appData)
	if !ok || name != "Test Node" {
		t.Errorf("PNNameFromAppData(announce) = (%q, %v), want (\"Test Node\", true)", name, ok)
	}
}
