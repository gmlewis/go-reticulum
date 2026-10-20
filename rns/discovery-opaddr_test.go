// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestDiscoveryOPAddrAnnounce pins the announce half of OP_ADDR
// (RNS/Discovery.py:140): the operator LXMF address is emitted only when one is
// configured.
func TestDiscoveryOPAddrAnnounce(t *testing.T) {
	t.Parallel()

	transportID := bytes.Repeat([]byte{0x11}, 16)
	addr := bytes.Repeat([]byte{0xaa}, 16)

	info := discoveryBaseInfo(interfaces.DiscoveryConfig{LXMFAddress: addr}, "TCPServerInterface", true, transportID, "node", nil, nil, nil)
	if got, ok := info[discoveryFieldOPAddr].([]byte); !ok || !bytes.Equal(got, addr) {
		t.Errorf("OP_ADDR = %v, want %x", info[discoveryFieldOPAddr], addr)
	}

	info = discoveryBaseInfo(interfaces.DiscoveryConfig{}, "TCPServerInterface", true, transportID, "node", nil, nil, nil)
	if _, ok := info[discoveryFieldOPAddr]; ok {
		t.Error("OP_ADDR emitted for a config without an LXMF address")
	}
}

// TestDiscoveryOPAddrDecode pins the receive half (RNS/Discovery.py:417-420):
// OP_ADDR may be nil or a byte string; any other type is a decode error; a
// non-empty 16-byte value becomes the operator_lxmf_address hex string, and a
// wrong-length byte string is silently ignored.
func TestDiscoveryOPAddrDecode(t *testing.T) {
	t.Parallel()

	addr := bytes.Repeat([]byte{0xab}, 16)
	tests := []struct {
		name string
		m    map[any]any
		want string
		ok   bool
		err  bool
	}{
		{name: "absent", m: map[any]any{}},
		{name: "nil", m: map[any]any{discoveryFieldOPAddr: nil}},
		{name: "valid", m: map[any]any{discoveryFieldOPAddr: addr}, want: "abababababababababababababababab", ok: true},
		{name: "empty bytes", m: map[any]any{discoveryFieldOPAddr: []byte{}}},
		{name: "wrong length", m: map[any]any{discoveryFieldOPAddr: []byte{1, 2, 3}}},
		{name: "wrong type", m: map[any]any{discoveryFieldOPAddr: "hex"}, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := discoveryOperatorLXMFAddress(tt.m)
			if (err != nil) != tt.err {
				t.Fatalf("err = %v, want err=%v", err, tt.err)
			}
			if got != tt.want || ok != tt.ok {
				t.Errorf("operator address = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestParseDiscoveryLXMFAddress pins Reticulum.py:919-923: exactly 32 hex
// characters are accepted; anything else leaves the field unset.
func TestParseDiscoveryLXMFAddress(t *testing.T) {
	t.Parallel()

	const valid = "abababababababababababababababab"
	cfg, _ := parseDiscoveryConfig(&ConfigSection{Properties: map[string]string{
		"discoverable":           "yes",
		"discovery_lxmf_address": valid,
	}}, "TCPServerInterface", interfaces.ModeFull)
	if got := len(cfg.LXMFAddress); got != 16 {
		t.Errorf("LXMFAddress length = %v, want 16", got)
	}

	for _, bad := range []string{"abcd", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", valid + "00"} {
		cfg, _ := parseDiscoveryConfig(&ConfigSection{Properties: map[string]string{
			"discoverable":           "yes",
			"discovery_lxmf_address": bad,
		}}, "TCPServerInterface", interfaces.ModeFull)
		if cfg.LXMFAddress != nil {
			t.Errorf("LXMFAddress(%q) = %x, want nil", bad, cfg.LXMFAddress)
		}
	}
}

// TestDiscoveryAddressClassifiers pins is_onion_address and
// is_invalid_ip_address (RNS/Discovery.py:871-879) used by the discovery
// auto-connect guards.
func TestDiscoveryAddressClassifiers(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"abc.onion", "ABC.ONION", "x.y.onion."} {
		want := addr == "abc.onion" || addr == "ABC.ONION"
		if got := IsOnionAddress(addr); got != want {
			t.Errorf("IsOnionAddress(%q) = %v, want %v", addr, got, want)
		}
	}
	if IsOnionAddress("onion.example.com") {
		t.Error("IsOnionAddress(onion.example.com) = true, want false")
	}
	for _, addr := range []string{"127.0.0.1", "0.0.0.0"} {
		if !IsInvalidIPAddress(addr) {
			t.Errorf("IsInvalidIPAddress(%q) = false, want true", addr)
		}
	}
	if IsInvalidIPAddress("192.168.1.1") {
		t.Error("IsInvalidIPAddress(192.168.1.1) = true, want false")
	}
}
