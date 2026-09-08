// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type mockRefreshTransport struct {
	rns.Transport
	reqCh chan []byte
}

func (m *mockRefreshTransport) RequestPath(destHash []byte) error {
	select {
	case m.reqCh <- append([]byte(nil), destHash...):
	default:
	}
	return nil
}

// TestHubTimeoutTriggersPathRediscoveryOnClose verifies that when an active
// hub link closes due to a timeout or transport loss, onClosedWithReason
// requests network path rediscovery for the hub's destination hash.
func TestHubTimeoutTriggersPathRediscoveryOnClose(t *testing.T) {
	t.Parallel()

	hubHash := []byte("hub-dest-hash---")
	mock := &mockRefreshTransport{
		reqCh: make(chan []byte, 1),
	}

	mgr := NewManager(tempDir(t), func() []byte { return []byte("client-hash-----") })
	mgr.SetNickname("Client")
	hub := mgr.AddHub(hubHash, "rrc.chat", "TestHub")
	hub.transport = mock

	// Link closed due to timeout
	hub.onClosedWithReason("timeout")

	select {
	case req := <-mock.reqCh:
		if !bytes.Equal(req, hubHash) {
			t.Errorf("requested path %x, want %x", req, hubHash)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("onClosedWithReason('timeout') did not trigger path rediscovery for hub %x", hubHash)
	}
}

// TestHubConnectWorkerRequestsPathWhenUnresponsiveOrRepeatedAttempt verifies
// that connectWorker forces a path request when repeated reconnect attempts
// have failed (reconnectAttempts > 1) even if hasPath returns true.
func TestHubConnectWorkerRequestsPathWhenUnresponsiveOrRepeatedAttempt(t *testing.T) {
	t.Parallel()

	hubHash := []byte("hub-dest-hash---")

	mgr := NewManager(tempDir(t), func() []byte { return []byte("client-hash-----") })
	hub := mgr.AddHub(hubHash, "rrc.chat", "TestHub")
	hub.connectTimeout = 50 * time.Millisecond

	reqCh := make(chan []byte, 1)
	hub.hasPathFn = func(hash []byte) bool {
		// Simulates a stale/broken path still present in pathTable
		return true
	}
	hub.requestPathFn = func(hash []byte) error {
		select {
		case reqCh <- append([]byte(nil), hash...):
		default:
		}
		return nil
	}

	// Simulate 2nd reconnect attempt
	hub.reconnectAttempts = 2

	hub.connectWorker()

	select {
	case req := <-reqCh:
		if !bytes.Equal(req, hubHash) {
			t.Errorf("connectWorker requested %x, want %x", req, hubHash)
		}
	default:
		t.Errorf("connectWorker did not request path rediscovery on reconnect attempt %v", hub.reconnectAttempts)
	}
}
