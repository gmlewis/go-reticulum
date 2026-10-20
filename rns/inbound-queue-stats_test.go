// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
)

// TestInterfaceStatsInboundQueueFields pins the queue half of
// get_interface_stats (Reticulum.py:1539-1554): the absolute heights and drops
// per traffic class, their totals, and the five pressure ratios.
func TestInterfaceStatsInboundQueueFields(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.inboundQueueLengths = [NumTrafficClasses]int{10, 4, 8, 2}
	ts.enableInboundQueueLocked()
	// 3 queued data, 1 announce, 2 path requests, and one ingress-limited
	// packet dropped because that class holds only two.
	for range 3 {
		_ = ts.inboundQueues.Put(TCData, &Packet{})
	}
	_ = ts.inboundQueues.Put(TCAnnounce, &Packet{})
	for range 2 {
		_ = ts.inboundQueues.Put(TCPathRequest, &Packet{})
	}
	for range 3 {
		_ = ts.inboundQueues.Put(TCIngressLimited, &Packet{})
	}

	stats := (&Reticulum{transport: ts}).getInterfaceStats()

	wantInts := map[string]int{
		"rxqt": 8,
		"rxqd": 3, "rxqa": 1, "rxqp": 2, "rxqil": 2,
		"rxqtd": 1, "rxqdd": 0, "rxqad": 0, "rxqpd": 0, "rxqild": 1,
	}
	for key, want := range wantInts {
		got, ok := stats[key]
		if !ok {
			t.Errorf("stats missing %q", key)
			continue
		}
		if got != want {
			t.Errorf("stats[%q] = %v, want %v", key, got, want)
		}
	}

	// The ingress-limited class is exactly at capacity (two of two), so its
	// pressure is 1.0.
	wantPressures := map[string]float64{
		"tqpressure":  8.0 / 24.0,
		"dqpressure":  3.0 / 10.0,
		"aqpressure":  1.0 / 4.0,
		"pqpressure":  2.0 / 8.0,
		"ilqpressure": 1.0,
	}
	for key, want := range wantPressures {
		got, ok := stats[key].(float64)
		if !ok {
			t.Errorf("stats[%q] = %T/%v, want float64 %v", key, stats[key], stats[key], want)
			continue
		}
		if got != want {
			t.Errorf("stats[%q] = %v, want %v", key, got, want)
		}
	}
	if stats["txq"] != nil {
		t.Errorf("stats[txq] = %v, want nil (Python emits None)", stats["txq"])
	}
}

// TestInterfaceStatsInboundQueueEmpty pins Python's zero shape: an empty queue
// reports integer 0 for every height, drop and pressure (the `... if height
// else 0` ternary, Reticulum.py:1548-1554).
func TestInterfaceStatsInboundQueueEmpty(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.inboundQueueLengths = [NumTrafficClasses]int{10, 4, 8, 2}
	ts.enableInboundQueueLocked()
	stats := (&Reticulum{transport: ts}).getInterfaceStats()

	for _, key := range []string{
		"rxqt", "rxqd", "rxqa", "rxqp", "rxqil",
		"rxqtd", "rxqdd", "rxqad", "rxqpd", "rxqild",
		"tqpressure", "dqpressure", "aqpressure", "pqpressure", "ilqpressure",
	} {
		if got, ok := stats[key]; !ok || got != 0 {
			t.Errorf("stats[%q] = %v (present=%v), want integer 0", key, got, ok)
		}
	}
}

// TestInterfaceStatsInboundQueueUnstarted pins the unstarted-transport shape:
// no queues exist, so every count is zero rather than a panic.
func TestInterfaceStatsInboundQueueUnstarted(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	stats := (&Reticulum{transport: ts}).getInterfaceStats()
	if got := stats["rxqt"]; got != 0 {
		t.Errorf("stats[rxqt] = %v, want 0 on an unstarted transport", got)
	}
	if got := stats["tqpressure"]; got != 0 {
		t.Errorf("stats[tqpressure] = %v, want 0 on an unstarted transport", got)
	}
}
