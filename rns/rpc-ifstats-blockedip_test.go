// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// rpcBlockedIPInterface is a dummy interface that exposes the fast-flap
// BlockedIPCount/BlockedIPList accessors, mirroring BackboneInterface, so the
// ifstats map builder populates blocked_ips/blocked_ip_list
// (RNS/Reticulum.py:1463-1467, v1.4.0).
type rpcBlockedIPInterface struct {
	*interfaces.BaseInterface
	blockedCount int
	blockedList  []string
}

func (b *rpcBlockedIPInterface) Type() string            { return "BackboneInterface" }
func (b *rpcBlockedIPInterface) Send([]byte) error       { return nil }
func (b *rpcBlockedIPInterface) IsOut() bool             { return true }
func (b *rpcBlockedIPInterface) Detach() error           { return nil }
func (b *rpcBlockedIPInterface) Status() bool            { return true }
func (b *rpcBlockedIPInterface) BlockedIPCount() int     { return b.blockedCount }
func (b *rpcBlockedIPInterface) BlockedIPList() []string { return b.blockedList }

// TestRPCInterfaceStatsIncludesBlockedIPs covers Phase 18 task 4: the
// per-interface ifstats map carries blocked_ips (int) and blocked_ip_list
// ([]string) for an interface that exposes fast-flap blocking
// (RNS/Reticulum.py:1463-1467, v1.4.0), and DecodeInterfaceStats surfaces them
// on InterfaceStat.BlockedIPs/BlockedIPList.
func TestRPCInterfaceStatsIncludesBlockedIPs(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := &rpcBlockedIPInterface{
		BaseInterface: interfaces.NewBaseInterface("BackboneInterface[flap]", interfaces.ModeFull, 1000),
		blockedCount:  3,
		blockedList:   []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
	}
	ts.RegisterInterface(iface)
	r := &Reticulum{transport: ts}

	resp := r.handleRPCRequest(map[any]any{"get": "interface_stats"})
	stats, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("expected stats map[string]any, got %#v", resp)
	}
	entries, ok := stats["interfaces"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected non-empty interfaces list, got %#v", stats["interfaces"])
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("expected interface entry map[string]any, got %#v", entries[0])
	}

	count, ok := entry["blocked_ips"].(int)
	if !ok {
		t.Fatalf("blocked_ips = %#v, want int 3", entry["blocked_ips"])
	}
	if count != 3 {
		t.Fatalf("blocked_ips = %d, want 3", count)
	}

	list, ok := entry["blocked_ip_list"].([]string)
	if !ok {
		t.Fatalf("blocked_ip_list = %#v, want []string", entry["blocked_ip_list"])
	}
	if len(list) != 3 || list[0] != "10.0.0.1" || list[1] != "10.0.0.2" || list[2] != "10.0.0.3" {
		t.Fatalf("blocked_ip_list = %v, want [10.0.0.1 10.0.0.2 10.0.0.3]", list)
	}

	// DecodeInterfaceStats surfaces the fields on InterfaceStat.
	snap := DecodeInterfaceStats(stats)
	if len(snap.Interfaces) != 1 {
		t.Fatalf("decoded %d interfaces, want 1", len(snap.Interfaces))
	}
	di := snap.Interfaces[0]
	if di.BlockedIPs == nil || *di.BlockedIPs != 3 {
		t.Fatalf("decoded BlockedIPs = %v, want *3", di.BlockedIPs)
	}
	if len(di.BlockedIPList) != 3 || di.BlockedIPList[0] != "10.0.0.1" {
		t.Fatalf("decoded BlockedIPList = %v, want 3 entries starting 10.0.0.1", di.BlockedIPList)
	}
}

// TestRPCInterfaceStatsBlockedIPsAbsentForNonBlocker covers Phase 18 task 4: an
// interface that does NOT expose BlockedIPCount/BlockedIPList yields a nil
// blocked_ips/blocked_ip_list (mirroring Python's hasattr absence), so
// non-Backbone interfaces never report blocked IPs.
func TestRPCInterfaceStatsBlockedIPsAbsentForNonBlocker(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := &rpcFreqInterface{BaseInterface: interfaces.NewBaseInterface("freq-iface", interfaces.ModeFull, 1000)}
	ts.RegisterInterface(iface)
	r := &Reticulum{transport: ts}

	resp := r.handleRPCRequest(map[any]any{"get": "interface_stats"})
	stats, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("expected stats map[string]any, got %#v", resp)
	}
	entries, ok := stats["interfaces"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected non-empty interfaces list, got %#v", stats["interfaces"])
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("expected interface entry map[string]any, got %#v", entries[0])
	}
	if entry["blocked_ips"] != nil {
		t.Fatalf("blocked_ips = %#v, want nil for non-blocker", entry["blocked_ips"])
	}
	if entry["blocked_ip_list"] != nil {
		t.Fatalf("blocked_ip_list = %#v, want nil for non-blocker", entry["blocked_ip_list"])
	}

	snap := DecodeInterfaceStats(stats)
	di := snap.Interfaces[0]
	if di.BlockedIPs != nil {
		t.Fatalf("decoded BlockedIPs = %v, want nil for non-blocker", di.BlockedIPs)
	}
	if di.BlockedIPList != nil {
		t.Fatalf("decoded BlockedIPList = %v, want nil for non-blocker", di.BlockedIPList)
	}
}
