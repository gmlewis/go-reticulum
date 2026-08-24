// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"encoding/hex"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// pythonSingleFieldMessageB10 is a nomadnet (Python)-signed LXMF message with a
// single fields-map key (FIELD_RENDERER=0x0F only), fixed timestamp 1000000.0.
// Generated via RNS.Identity + LXMF.LXMessage with deterministic keys.
const pythonSingleFieldMessageB10 = "7da3112fb9797db7e9b3bc8cefd3f423" +
	"fae321c442e3c9bdcd7a3e79d850e03c" +
	"dc52e65e92f76251f33094b91b0569e5c7a5a96a1bb37a7d38d23cffa94bbe15094f8a2fe1a67bfe8f82347a45dba8d8ff39facc28a1439afcdf45578a7ca90e" +
	"94cb412e848000000000c400c42c48656c6c6f204d6163204d696e692120466972737420636f6e746163742066726f6d2074686520746573742e810f02"

// pythonMultiFieldMessageB10 reproduces a nomadnet (Python)-signed LXMF message
// with TWO fields-map keys in NON-SORTED insertion order: FIELD_RENDERER (0x0F)
// first, then FIELD_TICKET (0x0C) — matching nomadnet's include_ticket flow for
// trusted peers (send_message sets FIELD_RENDERER, then handle_outbound adds
// FIELD_TICKET). Python's msgpack.packb preserves insertion order (0x0F, 0x0C);
// Go's msgpack.PackSorted reorders to (0x0C, 0x0F), producing a different hash
// and breaking signature verification. Fixed timestamp 1000000.0.
const pythonMultiFieldMessageB10 = "cd4fffa7ae57c813e4e07db50c26c664" +
	"fae321c442e3c9bdcd7a3e79d850e03c" +
	"6a7cffbc0abd4676730356bb33e5c2130db9d7877317d7aa01afd034ffb4912aeb57173982f4030f4b9c15a05e5b128274b9404fc1a6efe23eb94d4a2c90820b" +
	"94cb412e848000000000c400c42c48656c6c6f204d6163204d696e692120466972737420636f6e746163742066726f6d2074686520746573742e820f020cc420404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f"

// pythonSourcePubKeyB10 is the source identity's full public key
// (X25519 public 32 bytes + Ed25519 public 32 bytes = 64 bytes).
const pythonSourcePubKeyB10 = "8f40c5adb68f25624ae5b214ea767a6ec94d829d3d7b5e1ad1ba6f3e2138285f29acbae141bccaf0b22e1a94d34d0bc7361e526d0bfe12c89794bc9322966dd7"

// pythonSourceHashB10 is the source destination hash (16 bytes).
const pythonSourceHashB10 = "fae321c442e3c9bdcd7a3e79d850e03c"

// TestB10PythonSignedLXMFVerifiesInGo reproduces B10: a nomadnet (Python)-sent
// LXMF message must verify under go-reticulum's UnpackMessageFromBytes. The
// multi-field case (FIELD_RENDERER + FIELD_TICKET in non-sorted insertion order)
// fails because Go's msgpack.PackSorted reorders the map keys, producing a
// different hash than Python's insertion-ordered packb.
func TestB10PythonSignedLXMFVerifiesInGo(t *testing.T) {
	t.Parallel()

	sourcePubKey, err := hex.DecodeString(pythonSourcePubKeyB10)
	if err != nil {
		t.Fatalf("decode source pub key hex: %v", err)
	}

	sourceHash, err := hex.DecodeString(pythonSourceHashB10)
	if err != nil {
		t.Fatalf("decode source hash hex: %v", err)
	}

	cases := []struct {
		name   string
		packed string
	}{
		{"single_field", pythonSingleFieldMessageB10},
		{"multi_field_ticket", pythonMultiFieldMessageB10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			packed, err := hex.DecodeString(tc.packed)
			if err != nil {
				t.Fatalf("decode packed hex: %v", err)
			}

			ts := rns.NewTransportSystem(nil)
			ts.Remember(nil, sourceHash, sourcePubKey, nil)

			msg, err := UnpackMessageFromBytes(ts, packed, MethodOpportunistic)
			if err != nil {
				t.Fatalf("UnpackMessageFromBytes: %v", err)
			}

			if !msg.SignatureValidated {
				t.Errorf("Python-signed LXMF message failed signature verification: reason=%v", msg.UnverifiedReason)
			}
		})
	}
}
