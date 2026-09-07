// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"encoding/hex"
	"testing"
)

func TestAsicX25519ParityWithGoldenVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		scalarHex         string
		uCoordHex         string
		expectedSharedHex string
	}{
		{
			name:              "RFC 7748 Vector 1",
			scalarHex:         "a546e36bf0527c9d3b16154b82465edd62144c0ac1fc5a18506a2244ba449ac4",
			uCoordHex:         "e6db6867583030db3594c1a424b15f7c726624ec26b3353b10a903a6d0ab1c4c",
			expectedSharedHex: "c3da55379de9c6908e94ea4df28d084f32eccf03491c71f754b4075577a28552",
		},
		{
			name:              "RFC 7748 Vector 2",
			scalarHex:         "4b66e9d4d1b4673c5ad22691957d6af5c11b6421e0ea01d42ca4169e7918ba0d",
			uCoordHex:         "e5210f12786811d3f4b7959d0538ae2c31dbe7106fc03c3efc4cd549c715a493",
			expectedSharedHex: "95cbde9476e8907d7aade45cb4b873f88b595a68799fa152e6f8f7647aac7957",
		},
		{
			name:              "Base Point u=9 with Scalar 1",
			scalarHex:         "0100000000000000000000000000000000000000000000000000000000000000",
			uCoordHex:         "0900000000000000000000000000000000000000000000000000000000000000",
			expectedSharedHex: "2fe57da347cd62431528daac5fbb290730fff684afc4cfc2ed90995f58cb3b74",
		},
		{
			name:              "Reticulum Handshake Key Exchange A",
			scalarHex:         "c8079d38767314f11b2a40701026702636e29618201d4fb3a6049c692a947fa5",
			uCoordHex:         "504602762c4b84965378ac4790be450123963286f14dd16b270733a4e3b0d595",
			expectedSharedHex: "86633513b4ad8ecbb5e22269af2a39cefa4f39acf116767af58582b113466b1a",
		},
		{
			name:              "Reticulum Handshake Key Exchange B",
			scalarHex:         "4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742",
			uCoordHex:         "438a6800f489f1a7d0394b507c7510a976ce5439f481ac60bdab3429b59e564e",
			expectedSharedHex: "aaf91d2b8b839649e5d6ae431eeece2d5c68dae6de1bde60dce5af764500471e",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scalar, err := hex.DecodeString(tc.scalarHex)
			if err != nil {
				t.Fatalf("hex.DecodeString(scalarHex) failed: %v", err)
			}
			uCoord, err := hex.DecodeString(tc.uCoordHex)
			if err != nil {
				t.Fatalf("hex.DecodeString(uCoordHex) failed: %v", err)
			}

			priv, err := NewX25519PrivateKeyFromBytes(scalar)
			if err != nil {
				t.Fatalf("NewX25519PrivateKeyFromBytes failed: %v", err)
			}
			pub, err := NewX25519PublicKeyFromBytes(uCoord)
			if err != nil {
				t.Fatalf("NewX25519PublicKeyFromBytes failed: %v", err)
			}

			shared, err := priv.Exchange(pub)
			if err != nil {
				t.Fatalf("Exchange failed: %v", err)
			}

			gotHex := hex.EncodeToString(shared)
			if gotHex != tc.expectedSharedHex {
				t.Errorf("ECDH shared secret mismatch: got %v, expected %v", gotHex, tc.expectedSharedHex)
			}
		})
	}
}
