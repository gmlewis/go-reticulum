// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"reflect"
	"sync"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// retainCaptureTransport wraps a real TransportSystem, recording every
// RetainDestinationData call so tests can verify the router invokes the
// retain-destination hook on delivery. All other Transport methods are
// inherited from the embedded TransportSystem.
type retainCaptureTransport struct {
	*rns.TransportSystem

	mu       sync.Mutex
	retained [][]byte
}

func newRetainCaptureTransport() *retainCaptureTransport {
	return &retainCaptureTransport{TransportSystem: rns.NewTransportSystem(nil)}
}

// RetainDestinationData records the destination hash and delegates to the real
// TransportSystem so the retain flag is actually set on the known-destination
// entry.
func (ts *retainCaptureTransport) RetainDestinationData(destHash []byte) bool {
	ts.mu.Lock()
	ts.retained = append(ts.retained, append([]byte(nil), destHash...))
	ts.mu.Unlock()
	return ts.TransportSystem.RetainDestinationData(destHash)
}

func (ts *retainCaptureTransport) retainedSnapshot() [][]byte {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	out := make([][]byte, len(ts.retained))
	copy(out, ts.retained)
	return out
}

// knownDestEntry4IsRetained reads element 4 (the use-timestamp / retain flag)
// of the known-destination entry for destHash via reflection and reports
// whether it is the retain sentinel int64(-1). It avoids Interface() on
// unexported-derived values by using the reflect Value's typed accessors.
func knownDestEntry4IsRetained(t *testing.T, ts *rns.TransportSystem, destHash []byte) bool {
	t.Helper()
	kd := reflect.ValueOf(ts).Elem().FieldByName("knownDestinations")
	if !kd.IsValid() {
		t.Fatal("knownDestinations field not found on TransportSystem")
	}
	entry := kd.MapIndex(reflect.ValueOf(string(destHash)))
	if !entry.IsValid() {
		t.Fatalf("destination %x not present in knownDestinations", destHash)
	}
	if entry.Len() < 5 {
		t.Fatalf("known-destination entry for %x has %v elements, want >= 5", destHash, entry.Len())
	}
	e4 := entry.Index(4)
	if e4.Kind() == reflect.Interface {
		e4 = e4.Elem()
	}
	if e4.Kind() != reflect.Int64 {
		return false
	}
	return e4.Int() == -1
}

// TestProcessOutboundRetainsDestinationAfterDelivery verifies that when
// ProcessOutbound removes a DELIVERED message from the pending-outbound
// queue, it pins the destination's known-path entry via the retain-destination
// hook, mirroring Python LXMRouter.process_outbound (LXMRouter.py:2689-2692,
// v1.1.0) calling RNS.Reticulum._retain_destination_data.
func TestProcessOutboundRetainsDestinationAfterDelivery(t *testing.T) {
	t.Parallel()

	ts := newRetainCaptureTransport()
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	// Remember the destination so the retain hook has a known entry to pin.
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)

	message := mustTestNewMessage(t, destination, source, "delivered-content", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	message.SetState(StateDelivered)
	router.pendingOutbound = append(router.pendingOutbound, message)

	router.ProcessOutbound()

	// The delivered message is removed from the outbound queue.
	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pendingOutbound length=%v want=0 (delivered message should be removed)", len(router.pendingOutbound))
	}

	// The retain hook fired exactly once for the message's destination hash.
	retained := ts.retainedSnapshot()
	if len(retained) != 1 {
		t.Fatalf("retain call count=%v want=1", len(retained))
	}
	if !bytes.Equal(retained[0], message.DestinationHash) {
		t.Fatalf("retain destHash=%x want=%x", retained[0], message.DestinationHash)
	}

	// The retain flag is actually set on the known-destination entry.
	if !knownDestEntry4IsRetained(t, ts.TransportSystem, message.DestinationHash) {
		t.Fatal("destination entry element 4 is not the retain sentinel (-1) after delivery")
	}
}

// TestProcessOutboundDoesNotRetainForFailedMessage is the negative control:
// a FAILED message is removed from the outbound queue but
// the retain hook is NOT invoked (Python only retains on DELIVERED).
func TestProcessOutboundDoesNotRetainForFailedMessage(t *testing.T) {
	t.Parallel()

	ts := newRetainCaptureTransport()
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)

	message := mustTestNewMessage(t, destination, source, "failed-content", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	message.SetState(StateFailed)
	router.pendingOutbound = append(router.pendingOutbound, message)

	router.ProcessOutbound()

	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pendingOutbound length=%v want=0 (failed message should be removed)", len(router.pendingOutbound))
	}
	if retained := ts.retainedSnapshot(); len(retained) != 0 {
		t.Fatalf("retain call count for failed message=%v want=0", len(retained))
	}
}
