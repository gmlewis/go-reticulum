// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// errTestSend stands in for a transient write failure (a TCP peer whose
// connection was replaced under the write, say).
var errTestSend = errors.New("test: transient send failure")

// failingInterface is defined in transport_test.go; flakyInterface is the same
// interface with a failure budget: it fails the first `failures` sends and
// succeeds on every send after that, so a test can tell a single-shot send
// (the frame is lost) from a retried one (the frame arrives on the retry).
type flakyInterface struct {
	dummyInterface

	mu       sync.Mutex
	failures int
	attempts int
	sent     [][]byte
}

func newFlakyInterface(name string, failures int) *flakyInterface {
	return &flakyInterface{dummyInterface: dummyInterface{name: name}, failures: failures}
}

func (f *flakyInterface) Send(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failures {
		return errTestSend
	}
	f.sent = append(f.sent, append([]byte(nil), data...))
	return nil
}

func (f *flakyInterface) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *flakyInterface) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// TestDispatchForwardSendRetriesTransientFailure pins that one failed write
// does not lose the frame.
//
// A forward or a rebroadcast is put on the wire by a single send. Under a
// flapping uplink — a TCP peer whose connection is replaced under the write —
// that one send can fail and the request, the response or the announce it
// carried is gone for good, with nothing but a log line. Python has the same
// fire-and-forget shape, so this is where the port can be better than the
// original rather than merely equal to it: the frame is cheap to re-send, and
// the interface's own up/down transition (not a retry count) is what stops the
// attempts on a peer that is genuinely down.
func TestDispatchForwardSendRetriesTransientFailure(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := newFlakyInterface("flaky", 1)

	ts.dispatchForwardSend(iface, []byte("frame"), "test frame")

	if iface.sentCount() != 1 {
		t.Fatalf("frame delivered %v time(s) after one transient send failure, want 1 (attempts: %v)",
			iface.sentCount(), iface.attemptCount())
	}
	if iface.Status() != true {
		t.Error("interface reported down after a transient send failure")
	}
	if len(ts.downNotified) != 0 {
		t.Errorf("a transient send failure invalidated paths: downNotified has %v entry/entries", len(ts.downNotified))
	}
}

// testDownInterface is up until its first failed send, when it reports itself
// down — the shape of a peer whose connection is torn down under the write.
type testDownInterface struct {
	dummyInterface

	mu       sync.Mutex
	up       bool
	attempts int
}

func newTestDownInterface(name string) *testDownInterface {
	return &testDownInterface{dummyInterface: dummyInterface{name: name}, up: true}
}

func (d *testDownInterface) Status() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.up
}

func (d *testDownInterface) Send([]byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attempts++
	d.up = false
	return errTestSend
}

func (d *testDownInterface) attemptCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.attempts
}

// TestDispatchForwardSendGivesUpOnADownInterface pins the other side: a peer
// that is actually down must not be retried into a storm, and the down
// transition must still be reported exactly once so the transport can
// invalidate the paths that routed through it.
func TestDispatchForwardSendGivesUpOnADownInterface(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := newTestDownInterface("down")

	ts.dispatchForwardSend(iface, []byte("frame"), "test frame")

	if _, latched := ts.downNotified[iface]; !latched {
		t.Error("a down interface did not report the down transition, so paths through it stay invalid")
	}
	if iface.attemptCount() != 1 {
		t.Errorf("a down interface was sent to %v times, want 1: an interface that reports itself down is not retried",
			iface.attemptCount())
	}
}

// TestHandlePathRequestRetriesFailedSend pins the same retry on the path
// response that answers a request for a LOCAL destination. This response used
// to be sent once, inline, on the receiving interface; a single failed write
// consumed the request and the requestor never learned the path.
func TestHandlePathRequestRetriesFailedSend(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	recvIface := newFlakyInterface("recv", 1)
	ts.interfaces = append(ts.interfaces, recvIface)

	localDest := mustTestNewDestination(t, ts, mustTestNewIdentity(t, true), DestinationIn, DestinationSingle, "flaky", "target")
	tag := bytes.Repeat([]byte{0xAB}, TruncatedHashLength/8)
	requestData := append(append([]byte(nil), localDest.Hash...), tag...)

	if !ts.handlePathRequest(requestData, &Packet{ReceivingInterface: recvIface}) {
		t.Fatal("handlePathRequest did not answer a request for a local destination")
	}
	if recvIface.sentCount() != 1 {
		t.Fatalf("path response delivered %v time(s) after one transient send failure, want 1 (attempts: %v)",
			recvIface.sentCount(), recvIface.attemptCount())
	}
}

// TestForwardPathResponseKeepsRequestersWhenNothingIsSendable pins that a
// pending path request is not forgotten just because no response could be
// dispatched this time.
//
// The pending entry is the only record of who asked. Deleting it when every
// requester had gone down (or was the source itself) means the late response
// the requester is still waiting for has nowhere to go, and the request is
// only re-issued when the requester gives up and asks again. The TTL cull
// already bounds how long the entry lives, so keeping it is free.
func TestForwardPathResponseKeepsRequestersWhenNothingIsSendable(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	down := &stormIface{dummyInterface: dummyInterface{name: "down"}} // Status() false
	source := &capturingInterface{name: "source"}
	ts.interfaces = append(ts.interfaces, down, source)
	ts.pendingPathRequests["dest"] = []interfaces.Interface{down}
	ts.pendingPathRequestAt["dest"] = time.Now()

	packet := &Packet{DestinationHash: []byte("dest"), Raw: []byte{0x00, 0x00}}
	if ts.forwardPathResponseToRequesters(packet, source) {
		t.Fatal("forwardPathResponseToRequesters reported a send to a down requester")
	}
	if _, ok := ts.pendingPathRequests["dest"]; !ok {
		t.Error("the pending path request was forgotten although no response could be dispatched; the late response has nowhere to go")
	}
}
