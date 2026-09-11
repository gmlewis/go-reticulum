// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file verifies the stub announce observer host: without the wago
// build tag no observer host activates and announce handling keeps its
// existing behavior.

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestObserverHostStubInactive verifies that the stub build never activates
// an observer host.
func TestObserverHostStubInactive(t *testing.T) {
	t.Parallel()

	h := NewObserverHost(50*time.Millisecond, nil)
	if h == nil {
		t.Fatal("NewObserverHost returned nil")
	}
	if h.Active() {
		t.Fatal("stub ObserverHost reports Active, want inactive")
	}
	if err := h.LoadPlugin(testutils.TempDir(t, "gornsd-observers") + "observer.wasm"); err == nil {
		t.Fatal("stub LoadPlugin succeeded, want an error")
	}
	if err := h.HandleAnnounce([]byte(`{"destination_hash":"aa"}`)); err == nil {
		t.Fatal("stub HandleAnnounce succeeded, want an error")
	}
	h.Close()
	if h.Active() {
		t.Fatal("stub ObserverHost reports Active after Close")
	}
}

// TestSetupAnnounceHostsStub verifies that the stub build registers no
// announce handler (there are no active hosts to notify).
func TestSetupAnnounceHostsStub(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(testSilentRNSLogger())
	hosts := setupAnnounceHosts(ts, testutils.TempDir(t, "gornsd-observers"), testSilentRNSLogger())
	if len(hosts) != 0 {
		t.Fatalf("setupAnnounceHosts loaded %v host(s) in the stub build, want 0", len(hosts))
	}
	if got := len(ts.AnnounceHandlers()); got != 0 {
		t.Errorf("announce handlers registered = %v, want 0", got)
	}
	closeAnnounceHosts(hosts)
}

// TestErrAnnouncesNotLinked documents the stub-build error sentinel.
func TestErrAnnouncesNotLinked(t *testing.T) {
	t.Parallel()

	if !errors.Is(errAnnouncesNotLinked, errAnnouncesNotLinked) {
		t.Fatal("errAnnouncesNotLinked does not match itself")
	}
}
