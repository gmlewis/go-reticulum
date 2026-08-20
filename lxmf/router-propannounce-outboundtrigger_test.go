// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// validPropagationAppData builds a 7-element propagation-node announce
// app_data payload that passes decodePropagationAnnounceData, matching the
// format Python's pn_announce_data_is_valid accepts.
func validPropagationAppData(t *testing.T) []byte {
	t.Helper()
	appData, err := msgpack.Pack([]any{
		false,
		1700000000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node A")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}
	return appData
}

// TestPropagationAnnounceHandlerTriggersOutboundOnOutboundPNAnnounce verifies
// that when the configured outbound propagation node announces valid data, the
// propagation announce handler resets NextDeliveryAttempt on
// pending propagated messages and triggers ProcessOutbound, mirroring Python
// LXMFPropagationAnnounceHandler.received_announce (Handlers.py:44-52,
// v0.9.8+). The trigger is independent of propagationEnabled (Python does not
// guard it with propagation_node), so it fires even when this router is not
// itself a propagation node.
func TestPropagationAnnounceHandlerTriggersOutboundOnOutboundPNAnnounce(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	// propagationEnabled stays false to prove the trigger is independent of it.
	router.propagationEnabled = false

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	mustTest(t, router.SetOutboundPropagationNode(remoteHash))

	propagated := &Message{
		DestinationHash:     append([]byte{}, remoteHash...),
		Method:              MethodPropagated,
		NextDeliveryAttempt: 0,
	}
	direct := &Message{
		DestinationHash:     append([]byte{}, remoteHash...),
		Method:              MethodDirect,
		NextDeliveryAttempt: 0,
	}
	router.pendingOutbound = append(router.pendingOutbound, propagated, direct)

	before := router.now()
	triggered := make(chan struct{}, 1)
	router.outboundTriggerSleep = func(time.Duration) {}
	router.processOutbound = func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	}

	router.handlePropagationAnnounceWithContext(remoteHash, remoteIdentity, validPropagationAppData(t), false)

	// The propagated message's NextDeliveryAttempt is reset to ~now.
	if propagated.NextDeliveryAttempt == 0 {
		t.Fatal("expected propagated message NextDeliveryAttempt to be reset, got 0")
	}
	wantAttempt := float64(before.UnixNano()) / 1e9
	if propagated.NextDeliveryAttempt < wantAttempt {
		t.Fatalf("propagated NextDeliveryAttempt=%v want >= %v", propagated.NextDeliveryAttempt, wantAttempt)
	}
	// The direct message is untouched.
	if direct.NextDeliveryAttempt != 0 {
		t.Fatalf("direct message NextDeliveryAttempt=%v want=0 (only propagated messages reset)", direct.NextDeliveryAttempt)
	}

	// ProcessOutbound is triggered in a goroutine.
	select {
	case <-triggered:
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessOutbound was not triggered by the outbound-PN announce")
	}
}

// TestPropagationAnnounceHandlerIgnoresNonOutboundPNAnnounce is the negative
// control: an announce whose destination hash does not match the configured
// outbound propagation node does not reset NextDeliveryAttempt or trigger
// ProcessOutbound.
func TestPropagationAnnounceHandlerIgnoresNonOutboundPNAnnounce(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.propagationEnabled = false

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")

	otherIdentity := mustTestNewIdentity(t, true)
	otherHash := rns.CalculateHash(otherIdentity, AppName, "propagation")
	mustTest(t, router.SetOutboundPropagationNode(otherHash))

	propagated := &Message{
		DestinationHash:     append([]byte{}, remoteHash...),
		Method:              MethodPropagated,
		NextDeliveryAttempt: 0,
	}
	router.pendingOutbound = append(router.pendingOutbound, propagated)

	triggered := make(chan struct{}, 1)
	router.outboundTriggerSleep = func(time.Duration) {}
	router.processOutbound = func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	}

	router.handlePropagationAnnounceWithContext(remoteHash, remoteIdentity, validPropagationAppData(t), false)

	if propagated.NextDeliveryAttempt != 0 {
		t.Fatalf("propagated NextDeliveryAttempt=%v want=0 (hash did not match outbound PN)", propagated.NextDeliveryAttempt)
	}
	select {
	case <-triggered:
		t.Fatal("ProcessOutbound triggered for a non-outbound-PN announce")
	case <-time.After(100 * time.Millisecond):
	}
}
