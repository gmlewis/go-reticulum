// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// capturingLogger returns a logger routed to LogCallback whose output is
// accumulated in a buffer, so tests can assert which events the router logs.
func capturingLogger(t *testing.T) (*rns.Logger, func() string) {
	t.Helper()
	var mu sync.Mutex
	var buf strings.Builder
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogDebug)
	logger.SetLogCallback(func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		buf.WriteString(msg)
		buf.WriteString("\n")
	})
	logger.SetLogDest(rns.LogCallback)
	return logger, func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// TestHandleInboundMessageLogsDeliveryEvents verifies that the router logs
// LXMF receive/deliver events through the RNS logger (so they land in the
// embedding app's logfile, as Python's LXMRouter events land in the app log
// via RNS.log), instead of only the standard logger's stderr stream.
func TestHandleInboundMessageLogsDeliveryEvents(t *testing.T) {
	t.Parallel()
	logger, snapshot := capturingLogger(t)
	ts := rns.NewTransportSystem(logger)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	mustTest(t, msg.Pack())

	delivered := make(chan *Message, 1)
	router.RegisterDeliveryCallback(func(m *Message) { delivered <- m })

	router.handleInboundMessage(msg)

	select {
	case m := <-delivered:
		if m != msg {
			t.Fatal("delivery callback received a different message")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery callback was not invoked")
	}

	logged := snapshot()
	sourceHex := fmt.Sprintf("%x", msg.SourceHash)
	if !strings.Contains(logged, "Received LXM") {
		t.Errorf("expected a 'Received LXM' log event, got:\n%v", logged)
	}
	if !strings.Contains(logged, sourceHex) {
		t.Errorf("expected the receive event to name the source hash %v, got:\n%v", sourceHex, logged)
	}

	// A second delivery attempt of the same message must be ignored, with an
	// event logged (Python LXMRouter.lxmf_delivery, LXMRouter.py:1918-1919:
	// "<router> ignored already received message from <source>").
	router.handleInboundMessage(msg)
	logged = snapshot()
	if !strings.Contains(logged, "ignored already received message") {
		t.Errorf("expected an 'ignored already received message' log event, got:\n%v", logged)
	}
}

// TestHandleInboundMessageLogsBlackholeDrop verifies the blackhole drop event
// goes through the RNS logger (Python logs it at LOG_DEBUG,
// LXMRouter.py:1843).
func TestHandleInboundMessageLogsBlackholeDrop(t *testing.T) {
	t.Parallel()
	logger, snapshot := capturingLogger(t)
	ts := rns.NewTransportSystem(logger)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	mustTest(t, msg.Pack())
	msg.SourceBlackholed = true

	delivered := make(chan *Message, 1)
	router.RegisterDeliveryCallback(func(m *Message) { delivered <- m })

	router.handleInboundMessage(msg)

	select {
	case <-delivered:
		t.Fatal("blackholed message should not be delivered")
	case <-time.After(100 * time.Millisecond):
	}

	logged := snapshot()
	if !strings.Contains(logged, "Dropping LXM from blackholed identity") {
		t.Errorf("expected a blackhole-drop log event, got:\n%v", logged)
	}
}

// TestProcessOutboundLogsDeliveryOccurred verifies the queue-removal event
// (Python LXMRouter.process_outbound, LXMRouter.py:2687: "Delivery has
// occurred for <lxm>, removing from outbound queue").
func TestProcessOutboundLogsDeliveryOccurred(t *testing.T) {
	t.Parallel()
	logger, snapshot := capturingLogger(t)
	ts := rns.NewTransportSystem(logger)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	mustTest(t, msg.Pack())
	msg.SetState(StateDelivered)

	router.mu.Lock()
	router.pendingOutbound = append(router.pendingOutbound, msg)
	router.mu.Unlock()

	router.ProcessOutbound()

	logged := snapshot()
	if !strings.Contains(logged, "Delivery has occurred for") {
		t.Errorf("expected a 'Delivery has occurred' log event, got:\n%v", logged)
	}

	router.mu.Lock()
	got := len(router.pendingOutbound)
	router.mu.Unlock()
	if got != 0 {
		t.Errorf("pendingOutbound length after delivery=%v want=0", got)
	}
}

// TestFailMessageLogsSendFailure verifies the send-failure event (Python
// LXMRouter.fail_message, LXMRouter.py:2565: "<lxm> failed to send").
func TestFailMessageLogsSendFailure(t *testing.T) {
	t.Parallel()
	logger, snapshot := capturingLogger(t)
	ts := rns.NewTransportSystem(logger)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	mustTest(t, msg.Pack())

	failed := make(chan *Message, 1)
	msg.FailedCallback = func(m *Message) { failed <- m }

	router.mu.Lock()
	router.failMessageLocked(msg)
	router.mu.Unlock()

	select {
	case <-failed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("failed callback was not invoked")
	}
	if msg.State() != StateFailed {
		t.Errorf("state=%v want=%v", msg.State(), StateFailed)
	}

	logged := snapshot()
	if !strings.Contains(logged, "failed to send") {
		t.Errorf("expected a 'failed to send' log event, got:\n%v", logged)
	}
}
