// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type probeSendClock struct {
	now time.Time
}

func (c *probeSendClock) Now() time.Time        { return c.now }
func (c *probeSendClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

func TestSleepBetweenProbes(t *testing.T) {
	t.Parallel()

	var slept time.Duration
	sleepBetweenProbes(0, 1.5, func(d time.Duration) { slept = d })
	if slept != 0 {
		t.Fatalf("expected no sleep before first probe, got %v", slept)
	}

	sleepBetweenProbes(1, 1.5, func(d time.Duration) { slept = d })
	if got, want := slept, 1500*time.Millisecond; got != want {
		t.Fatalf("sleep duration = %v, want %v", got, want)
	}
}

func TestWaitForProbeReceiptPrintsSpinnerAndReturnsDelivered(t *testing.T) {
	t.Parallel()

	clock := &probeSendClock{now: time.Unix(0, 0)}
	receipt := &rns.PacketReceipt{Status: rns.ReceiptSent}
	var out bytes.Buffer
	if ok := waitForProbeReceiptAt(&out, receipt, 1.0, clock.Now, func(d time.Duration) {
		clock.Sleep(d)
		receipt.Status = rns.ReceiptDelivered
	}); !ok {
		t.Fatal("expected delivered receipt")
	}
	if got := out.String(); len(got) == 0 || got == "Probe timed out\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestWaitForProbeReceiptTimesOut(t *testing.T) {
	t.Parallel()

	clock := &probeSendClock{now: time.Unix(0, 0)}
	receipt := &rns.PacketReceipt{Status: rns.ReceiptSent}
	var out bytes.Buffer
	if ok := waitForProbeReceiptAt(&out, receipt, 0.1, clock.Now, clock.Sleep); ok {
		t.Fatal("expected receipt timeout")
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte("Probe timed out")) {
		t.Fatalf("missing timeout message: %q", got)
	}
}
