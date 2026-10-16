// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// TestPeerNameFromDecodedMetadata pins how a propagation node's announced name is
// read back out of an announce.
//
// The name travels in the announce's metadata map under PNMetaName, and that map
// arrives decoded from msgpack, where an integer key comes back as an int64. A
// lookup keyed by an untyped constant is an int in a map[any]any and matches
// nothing: the node announced "go-nomadnet.duckdns.org" and every client still
// listed it as its bare <hash>, and a name search in the announce stream could
// never find it.
func TestPeerNameFromDecodedMetadata(t *testing.T) {
	t.Parallel()

	const want = "go-nomadnet.duckdns.org"

	packed, err := msgpack.Pack(map[any]any{PNMetaName: []byte(want)})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	keys, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("Unpack returned %T, want map[any]any", unpacked)
	}
	for key := range keys {
		if _, isInt64 := key.(int64); !isInt64 {
			t.Errorf("decoded metadata key = %T(%v), want int64: PNMetaName must key what msgpack decodes", key, key)
		}
	}

	// The production path an announce takes: peerMetadata normalizes the map, and
	// Peer.Name reads PNMetaName out of it.
	peer := &Peer{metadata: peerMetadata(unpacked)}
	if got := peer.Name(); got != want {
		t.Errorf("Peer.Name() = %q, want %q", got, want)
	}
}
