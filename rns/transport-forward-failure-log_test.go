// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// stormIface is a controllable outbound interface for the forwarding-failure
// logging tests. It reports an up/down state and fails every send with the
// error a down interface produces ("interface X is not running"), counting
// attempts so a test can tell "suppressed the log" apart from "stopped
// forwarding".
type stormIface struct {
	dummyInterface

	mu        sync.Mutex
	up        bool
	sendCount int
	sendErr   error
}

func newStormIface(name string) *stormIface {
	return &stormIface{
		dummyInterface: dummyInterface{name: name},
		sendErr:        errors.New("interface " + name + " is not running"),
	}
}

func (s *stormIface) setUp(up bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.up = up
}

func (s *stormIface) Status() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.up
}

func (s *stormIface) sends() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendCount
}

func (s *stormIface) Send([]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendCount++
	return s.sendErr
}

// captureErrors returns a logger that records only Error-and-worse lines and a
// function that flushes the async writer and returns the lines seen so far.
func captureErrors(t *testing.T) (*Logger, func() []string) {
	t.Helper()

	logger := NewLogger()
	logger.SetLogLevel(LogError)
	logger.SetLogDest(LogCallback)

	var mu sync.Mutex
	var lines []string
	logger.SetLogCallback(func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, msg)
	})
	t.Cleanup(logger.Close)

	collected := func() []string {
		if !logger.Flush() {
			t.Error("logger.Flush() = false; the async writer never drained")
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
	return logger, collected
}

func countLines(lines []string, substr string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, substr) {
			count++
		}
	}
	return count
}

// linkDataFrame builds the HEADER_1 link-data frame a client behind a shared
// instance broadcasts for a link:
// [flags=Header1|DATA][hops][dest(16)][context][payload]. The payload varies
// with seq so each frame survives the duplicate filter.
func linkDataFrame(dest []byte, seq int) []byte {
	raw := make([]byte, 0, 2+len(dest)+1+4)
	raw = append(raw, byte(Header1<<6|PacketData), 0)
	raw = append(raw, dest...)
	raw = append(raw, ContextNone)
	return append(raw, byte(seq), byte(seq>>8), byte(seq>>16), byte(seq>>24))
}

// transportDataFrame builds the HEADER_2 link-data frame a transport node
// emits toward the next hop:
// [flags=Header2|DATA][hops][transportID(16)][dest(16)][context][payload].
func transportDataFrame(transportID, dest []byte, seq int) []byte {
	raw := make([]byte, 0, 2+len(transportID)+len(dest)+1+4)
	raw = append(raw, byte(Header2<<6|PacketData), 1)
	raw = append(raw, transportID...)
	raw = append(raw, dest...)
	raw = append(raw, ContextNone)
	return append(raw, byte(seq), byte(seq>>8), byte(seq>>16), byte(seq>>24))
}

// TestForwardFailureLoggingDoesNotStorm pins the once-per-down-transition
// logging of the packet-forwarding paths. Both of them used to report every
// relayed packet that failed to leave a down interface at Error level: during
// the 2026-09-13 glenn-kamrui outage (a WiFi re-association that black-holed
// the Beleth TCP path) the link-transport path logged 2278 of these lines in
// 0.3 s, and an earlier one logged 9994 — bursts that tripped journald's rate
// limiter and hid the incident's own diagnostics. A send that fails on an
// interface already known to be down carries no information: the interface
// reports its own down transition (TCPClientInterface.failConn). Only a real
// failure on an interface that was up is logged, once, through the
// claimDownNotify latch that sendRebroadcast and dispatchForwardSend already
// use (Transport.transmit's "not running" fast-fail is expected, not news).
func TestForwardFailureLoggingDoesNotStorm(t *testing.T) {
	t.Parallel()

	const burst = 200

	tests := []struct {
		name     string
		message  string
		forwards func(t *testing.T, ts *TransportSystem, iface *stormIface, seq int)
	}{
		{
			name:    "path table forward",
			message: "Failed to forward packet",
			forwards: func(t *testing.T, ts *TransportSystem, iface *stormIface, seq int) {
				t.Helper()
				dest := bytes.Repeat([]byte{0x22}, 16)
				ts.mu.Lock()
				if _, ok := ts.pathTable[string(dest)]; !ok {
					ts.pathTable[string(dest)] = &PathEntry{Hops: 1, Interface: iface, Timestamp: time.Now()}
				}
				transportID := copyBytes(ts.identity.Hash)
				ts.mu.Unlock()
				ts.Inbound(transportDataFrame(transportID, dest, seq), iface)
			},
		},
		{
			name:    "link transport forward",
			message: "Failed to forward link-transport packet",
			forwards: func(t *testing.T, ts *TransportSystem, iface *stormIface, seq int) {
				t.Helper()
				linkID := bytes.Repeat([]byte{0x5a}, 16)
				ts.mu.Lock()
				if _, ok := ts.linkTable[string(linkID)]; !ok {
					ts.linkTable[string(linkID)] = &LinkEntry{
						Timestamp:         time.Now(),
						Hops:              1,
						RemainingHops:     1,
						OutboundInterface: iface,
						ReceivedInterface: iface,
						DestinationHash:   bytes.Repeat([]byte{0x11}, 16),
					}
				}
				ts.mu.Unlock()
				ts.Inbound(linkDataFrame(linkID, seq), iface)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, collected := captureErrors(t)
			ts := NewTransportSystem(logger)
			ts.identity = mustTestNewIdentity(t, true)
			ts.SetEnabled(true)

			iface := newStormIface("storm-iface")

			// The interface is down for the whole burst: every forwarded
			// packet fast-fails Send exactly as a pruned TCP client does.
			for seq := range burst {
				tt.forwards(t, ts, iface, seq)
			}
			if got := iface.sends(); got != burst {
				t.Fatalf("forwarded %v packets, want %v (logging must be throttled, not forwarding)", got, burst)
			}
			if got := countLines(collected(), tt.message); got != 0 {
				t.Errorf("%q logged %v times for an interface that was already down, want 0", tt.message, got)
			}

			// A half-open peer still reports itself up while every write
			// fails: that is a genuine down transition, reported exactly once
			// no matter how many queued sends drain onto the dead socket.
			iface.setUp(true)
			for seq := range burst {
				tt.forwards(t, ts, iface, burst+seq)
			}
			if got := countLines(collected(), tt.message); got != 1 {
				t.Errorf("%q logged %v times after one down transition, want 1", tt.message, got)
			}
		})
	}
}
