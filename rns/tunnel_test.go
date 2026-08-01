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

// buildTunnelSynthesizeData constructs a tunnel-establishment payload exactly
// as Python Transport.synthesize_tunnel (Transport.py:2120-2131) does:
//
//	public_key (64) + interface_hash (32) + random_hash (16) + signature(64)
//	where signature = Sign(public_key + interface_hash + random_hash).
func buildTunnelSynthesizeData(t *testing.T, id *Identity, interfaceHash, randomHash []byte) []byte {
	t.Helper()
	pub := id.GetPublicKey()
	if len(pub) != IdentityKeySize/8 {
		t.Fatalf("public key len=%d, want %d", len(pub), IdentityKeySize/8)
	}
	if len(interfaceHash) != 32 {
		t.Fatalf("interface hash len=%d, want 32", len(interfaceHash))
	}
	if len(randomHash) != TruncatedHashLength/8 {
		t.Fatalf("random hash len=%d, want %d", len(randomHash), TruncatedHashLength/8)
	}
	tunnelIDData := append(append([]byte{}, pub...), interfaceHash...)
	signedData := append(append([]byte{}, tunnelIDData...), randomHash...)
	sig, err := id.Sign(signedData)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != IdentityKeySize/8 {
		t.Fatalf("signature len=%d, want %d", len(sig), IdentityKeySize/8)
	}
	return append(append(append([]byte{}, pub...), interfaceHash...), append(randomHash, sig...)...)
}

// TestTunnelSynthesizeHandlerGolden verifies the inbound tunnel-synthesis
// handler (Python Transport.tunnel_synthesize_handler, Transport.py:2141-2158)
// accepts a well-formed establishment packet: it derives
// tunnel_id = FullHash(public_key+interface_hash), validates the signature
// over (public_key+interface_hash+random_hash), and registers a tunnel entry
// on the receiving interface.
func TestTunnelSynthesizeHandlerGolden(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id, err := NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts.identity = id

	interfaceHash := FullHash([]byte("test-tunnel-interface"))
	randomHash := FullHash([]byte("random-hash-seed"))[:TruncatedHashLength/8]
	data := buildTunnelSynthesizeData(t, id, interfaceHash, randomHash)

	wantTunnelID := FullHash(append(append([]byte{}, id.GetPublicKey()...), interfaceHash...))

	recvIface := &dummyInterface{name: "recv-iface"}
	pkt := &Packet{ReceivingInterface: recvIface}

	ts.tunnelSynthesizeHandler(data, pkt)

	ts.mu.Lock()
	entry, ok := ts.tunnels[string(wantTunnelID)]
	ts.mu.Unlock()
	if !ok {
		t.Fatalf("tunnel %x not registered after valid synthesis", wantTunnelID)
	}
	if !bytes.Equal(entry.ID, wantTunnelID) {
		t.Fatalf("tunnel ID=%x, want %x", entry.ID, wantTunnelID)
	}
	if entry.Interface != interfaces.Interface(recvIface) {
		t.Fatalf("tunnel interface mismatch: got %#v, want recv-iface", entry.Interface)
	}
	if entry.Paths == nil {
		t.Fatal("tunnel Paths map is nil")
	}
}

// TestTunnelSynthesizeHandlerRejectsInvalid verifies the handler refuses
// packets with the wrong length or a bad signature, registering no tunnel.
func TestTunnelSynthesizeHandlerRejectsInvalid(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id, err := NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts.identity = id

	interfaceHash := FullHash([]byte("test-tunnel-interface"))
	randomHash := FullHash([]byte("random-hash-seed"))[:TruncatedHashLength/8]

	// Wrong length: no tunnel.
	recvIface := &dummyInterface{name: "recv-iface"}
	ts.tunnelSynthesizeHandler([]byte{0x01, 0x02, 0x03}, &Packet{ReceivingInterface: recvIface})
	if got := ts.tunnelCount(); got != 0 {
		t.Fatalf("wrong-length packet registered %d tunnels, want 0", got)
	}

	// Bad signature: flip a signature byte so validation fails.
	data := buildTunnelSynthesizeData(t, id, interfaceHash, randomHash)
	bad := append([]byte{}, data...)
	bad[len(bad)-1] ^= 0xFF
	ts.tunnelSynthesizeHandler(bad, &Packet{ReceivingInterface: recvIface})
	if got := ts.tunnelCount(); got != 0 {
		t.Fatalf("bad-signature packet registered %d tunnels, want 0", got)
	}
}

// tunnelCount returns the number of registered tunnels (test helper).
func (ts *TransportSystem) tunnelCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.tunnels)
}
