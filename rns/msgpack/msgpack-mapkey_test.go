// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package msgpack

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// TestUnpackMapWithKeysGoCannotHash pins what happens to a map key that Go
// cannot use as a map key. MessagePack allows any value there, and real peers
// send maps keyed by arrays and by extension values; storing one in a
// map[any]any panics the runtime ("hash of unhashable type []interface {}"),
// which kills the process decoding it.
//
// Python's umsgpack, which Reticulum and LXMF use, never dies here: it converts
// a list key to a tuple (hashable, compared by value) and raises
// UnhashableKeyException for the rest, which the caller catches and drops the
// packet with. So a map keyed by an array or an extension value must decode to a
// comparable stand-in that re-encodes to the same bytes, and a map keyed by a
// map — unhashable in Python too — must come back as an error.
func TestUnpackMapWithKeysGoCannotHash(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		dataHex string
		wantErr bool
		// wantKeyType is the concrete type the decoded key must have; the
		// zero value means "any" (an integer key stays an integer).
		wantKeyType any
	}{
		{name: "int key  {1: 1}", dataHex: "810101", wantKeyType: int64(0)},
		{name: "map key  {{a: 1}: 1}", dataHex: "8181a1610101", wantErr: true},
		{name: "array key {[1, 2]: 1}", dataHex: "8192010201", wantKeyType: collectionMapKey("")},
		{name: "ext key  {Ext(1, 02): 1}", dataHex: "81d4010201", wantKeyType: extMapKey{}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := hex.DecodeString(tc.dataHex)
			if err != nil {
				t.Fatal(err)
			}

			unpacked, err := Unpack(data)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Unpack(%x) = %v, want an error for an unhashable key", data, unpacked)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unpack(%x): %v", data, err)
			}

			m, ok := unpacked.(map[any]any)
			if !ok {
				t.Fatalf("Unpack(%x) = %T, want map[any]any", data, unpacked)
			}
			if len(m) != 1 {
				t.Fatalf("Unpack(%x) = %v, want one entry", data, m)
			}
			for k := range m {
				if tc.wantKeyType != nil {
					switch tc.wantKeyType.(type) {
					case int64:
						if _, ok := k.(int64); !ok {
							t.Errorf("key = %T(%v), want int64", k, k)
						}
					case collectionMapKey:
						if _, ok := k.(collectionMapKey); !ok {
							t.Errorf("key = %T(%v), want collectionMapKey", k, k)
						}
					case extMapKey:
						if _, ok := k.(extMapKey); !ok {
							t.Errorf("key = %T(%v), want extMapKey", k, k)
						}
					}
				}
			}

			// The stand-in must re-encode to the bytes it was decoded from, so a
			// message carrying such a key survives being forwarded.
			repacked, err := Pack(m)
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}
			if !bytes.Equal(repacked, data) {
				t.Errorf("re-packed %x, want the original %x", repacked, data)
			}
		})
	}
}
