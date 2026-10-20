// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestFailedSendRetryCallbackDoesNotDeadlock pins the re-entrancy a failed send
// performs in the client, and which the router must survive.
//
// A send runs on the caller's goroutine: the nomadnet client's Ctrl-D handler
// reaches Router.HandleOutbound directly, and HandleOutbound calls
// ProcessOutbound() inline, so the whole outbound pass — and every failure it
// reaches — runs under r.mu on the caller's goroutine. failMessageLocked fires
// the message's FailedCallback from inside that hold, and the client's
// FailedCallback is Conversation.MessageNotification, which on a permanent
// failure with TryPropagationOnFail still set retries the message as
// propagated, through HandleOutbound, synchronously — and HandleOutbound takes
// r.mu itself (router.go:2129 for a propagated message with no propagation node,
// :2187 for the queue append). Go's sync.Mutex is not reentrant, so holding r.mu
// across the callback while the callback re-enters makes the caller wait on a
// lock it already holds, forever.
//
// Python permits this re-entrancy: its process_outbound returns immediately when
// the outbound lock is already taken (LXMRouter.py:2683, `if
// self.outbound_processing_lock.locked(): return`) and its handle_outbound takes
// no lock at all, so a retry issued from a failure callback re-enters freely.
// The router must not hold a lock across foreign code.
func TestFailedSendRetryCallbackDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router, err := NewRouter(ts, nil, testutils.TempDir(t, tempDirPrefix))
	mustTest(t, err)
	router.stopJobLoop()
	// Cleanup is deliberately not registered for the failure case: Close waits
	// on the same r.mu a deadlocked caller holds, so a deferred Close would hang
	// the test binary instead of failing this test.

	dest := mustTestNewDestination(t, ts, mustTestNewIdentity(t, true),
		rns.DestinationIn, rns.DestinationSingle, "lxmf", "delivery")
	source := mustTestNewDestination(t, ts, mustTestNewIdentity(t, true),
		rns.DestinationIn, rns.DestinationSingle, "lxmf", "delivery")

	msg := mustTestNewMessage(t, dest, source, "body", "", nil)
	// The message has spent its delivery attempts, so this pass is the one that
	// fails it, and try-propagation-on-fail is still set — the state a send
	// reaches when a peer cannot be reached. No propagation node is configured,
	// so the router fails the message rather than switching it to propagated
	// delivery.
	msg.DeliveryAttempts = maxDeliveryAttempts
	msg.TryPropagationOnFail = true

	// Conversation.MessageNotification's failure branch, verbatim: a permanent
	// failure with try-propagation-on-fail still set retries the message as
	// propagated, through HandleOutbound. The retry clears the flag, so a second
	// failure takes the client's other branch (ingest, which calls back into
	// nothing here) and the re-entrancy ends there — as it does in Python.
	msg.FailedCallback = func(m *Message) {
		if m.State() != StateFailed || !m.TryPropagationOnFail {
			return
		}
		m.TryPropagationOnFail = false
		m.DesiredMethod = MethodPropagated
		_ = router.HandleOutbound(m)
	}

	// The send, on this goroutine, exactly as the client's key handler runs it.
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_ = router.HandleOutbound(msg)
	}()

	select {
	case <-sent:
		_ = router.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("a failed send's retry callback re-entered HandleOutbound and blocked " +
			"on r.mu, which the failing pass still holds: the caller waits on itself " +
			"forever, which is the client's Ctrl-D freeze")
	}
}
