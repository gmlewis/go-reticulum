// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
)

// TestRegisterInterfaceDedupsDuplicateRegistration covers Phase 12 task 6:
// RegisterInterface is the canonical add path and skips a repeated
// registration of the same interface, matching Python's
// Transport.add_interface "if not interface in Transport.interfaces: append"
// (Transport.py:438-441). The interface appears at most once.
func TestRegisterInterfaceDedupsDuplicateRegistration(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	iface := newBootstrapConstructorTestInterface("dup", "TCPServerInterface")
	ts.RegisterInterface(iface)
	ts.RegisterInterface(iface)
	ts.RegisterInterface(iface)

	if got, want := len(ts.GetInterfaces()), 1; got != want {
		t.Fatalf("GetInterfaces len = %v, want %v (duplicate registration should dedup)", got, want)
	}
}

// TestRegisterInterfaceKeepsDistinctInterfaces covers Phase 12 task 6: the
// dedup is by interface identity, so two distinct interfaces both register.
func TestRegisterInterfaceKeepsDistinctInterfaces(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	a := newBootstrapConstructorTestInterface("a", "TCPServerInterface")
	b := newBootstrapConstructorTestInterface("b", "TCPServerInterface")
	ts.RegisterInterface(a)
	ts.RegisterInterface(b)

	if got, want := len(ts.GetInterfaces()), 2; got != want {
		t.Fatalf("GetInterfaces len = %v, want %v (distinct interfaces must both register)", got, want)
	}
}

// TestRemoveInterfaceIsIdempotent covers Phase 12 task 6: RemoveInterface is
// the canonical remove path and is a no-op when the interface is not present
// (Python Transport.remove_interface, Transport.py:444-447).
func TestRemoveInterfaceIsIdempotent(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	iface := newBootstrapConstructorTestInterface("rm", "TCPServerInterface")
	ts.RegisterInterface(iface)
	ts.RemoveInterface(iface)
	// Removing again must not panic and must leave the list empty.
	ts.RemoveInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("GetInterfaces len = %v, want 0 after duplicate remove", got)
	}
}
