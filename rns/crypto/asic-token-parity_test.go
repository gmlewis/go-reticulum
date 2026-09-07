// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestAsicTokenParityWithGoldenVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		keyHex           string
		ivHex            string
		plaintextHex     string
		expectedTokenHex string
	}{
		{
			name:             "Empty Payload",
			keyHex:           "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			ivHex:            "202122232425262728292a2b2c2d2e2f",
			plaintextHex:     "",
			expectedTokenHex: "202122232425262728292a2b2c2d2e2fe82546cf4538181b3f0a24390107fd00d24f8903b0ac4069cc5c0ab7828624c46414bddde5d9f6741b80be50d1754359",
		},
		{
			name:             "Single Block 16-byte Message",
			keyHex:           "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
			ivHex:            "2122232425262728292a2b2c2d2e2f30",
			plaintextHex:     "524e5320506f636b6574204875622031",
			expectedTokenHex: "2122232425262728292a2b2c2d2e2f30e95f43dc4a22bd726d920f69763ee517d3a62e5b37618d62fb0dad22ceddff154f36e69bedbbeefe628c5cf9b7dddf7e3e27e3acd8cf08ad666fdd8be30b3ac8",
		},
		{
			name:             "32-byte Standard Payload",
			keyHex:           "a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf",
			ivHex:            "c0c1c2c3c4c5c6c7c8c9cacbcccdcecf",
			plaintextHex:     "5265746963756c756d20546f6b656e20456e67696e6520546573742031323334",
			expectedTokenHex: "c0c1c2c3c4c5c6c7c8c9cacbcccdcecf0077b41ab35e03e7b85319535216f6d31bdfbba5c263a541ee7008fe89b014c41d4f148a2330fa2d0debf2bd7f7736192e028164b31ebbea604a102d1f80280eccd830871b43a843e5952776bc044116",
		},
		{
			name:             "Multi-block RRC Chat Message (80B)",
			keyHex:           "feedfacefeedfacefeedfacefeedfacecafebabecafebabecafebabecafebabe",
			ivHex:            "1234567890abcdef1234567890abcdef",
			plaintextHex:     "524e5320436f6d6d756e69747920487562202367656e6572616c3a2048692065766572796f6e652c2077656c636f6d6520746f205265746963756c756d206d657368206e6574776f726b696e6721",
			expectedTokenHex: "1234567890abcdef1234567890abcdef3bf9d2cd4a3ec16430ae3fdfd9de81d35fa2e9a8b085b531d7de6d53ff76b58d36c53610c803f9060ca9a8d2142c3902d65d772fb80914c43a5ba0eeb1e676f08eee2e95d3c12a105a934e3de4e83ad37f1ebe4a77bc67a9bf4f4bbfc30f3ba2b8c831a7750197250ded51351a65fdb6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			keyBytes, err := hex.DecodeString(tc.keyHex)
			if err != nil {
				t.Fatalf("hex.DecodeString(keyHex) failed: %v", err)
			}
			ivBytes, err := hex.DecodeString(tc.ivHex)
			if err != nil {
				t.Fatalf("hex.DecodeString(ivHex) failed: %v", err)
			}
			ptBytes, err := hex.DecodeString(tc.plaintextHex)
			if err != nil {
				t.Fatalf("hex.DecodeString(plaintextHex) failed: %v", err)
			}
			expectedTokenBytes, err := hex.DecodeString(tc.expectedTokenHex)
			if err != nil {
				t.Fatalf("hex.DecodeString(expectedTokenHex) failed: %v", err)
			}

			// 1. Initialize Token from key material
			tokenObj, err := NewToken(keyBytes)
			if err != nil {
				t.Fatalf("NewToken failed: %v", err)
			}

			// 2. Deterministic Encryption: PKCS#7 Pad -> AES-128-CBC -> HMAC-SHA256
			padded := PKCS7Pad(ptBytes, 16)
			ct, err := AES128CBCEncrypt(padded, keyBytes[16:], ivBytes)
			if err != nil {
				t.Fatalf("AES128CBCEncrypt failed: %v", err)
			}
			signedParts := append(ivBytes, ct...)
			h := hmac.New(sha256.New, keyBytes[:16])
			h.Write(signedParts)
			mac := h.Sum(nil)
			sealedToken := append(signedParts, mac...)

			// Verify generated token matches expected golden token bit-for-bit
			if !bytes.Equal(sealedToken, expectedTokenBytes) {
				t.Errorf("Sealed token mismatch:\n got:      %x\n expected: %x", sealedToken, expectedTokenBytes)
			}

			// 3. Decrypt the expected golden token
			if !tokenObj.VerifyHMAC(expectedTokenBytes) {
				t.Errorf("VerifyHMAC returned false for valid golden token")
			}
			recoveredPt, err := tokenObj.Decrypt(expectedTokenBytes)
			if err != nil {
				t.Fatalf("Decrypt failed on golden token: %v", err)
			}
			if !bytes.Equal(recoveredPt, ptBytes) {
				t.Errorf("Recovered plaintext mismatch:\n got:      %x\n expected: %x", recoveredPt, ptBytes)
			}

			// 4. Tampering rejection: 1-bit ciphertext corruption
			tamperedCt := make([]byte, len(expectedTokenBytes))
			copy(tamperedCt, expectedTokenBytes)
			tamperedCt[20] ^= 0x01
			if tokenObj.VerifyHMAC(tamperedCt) {
				t.Errorf("VerifyHMAC succeeded on tampered ciphertext")
			}
			if _, err := tokenObj.Decrypt(tamperedCt); err == nil {
				t.Errorf("Decrypt succeeded on tampered ciphertext, expected error")
			}

			// 5. Tampering rejection: 1-bit HMAC corruption
			tamperedHmac := make([]byte, len(expectedTokenBytes))
			copy(tamperedHmac, expectedTokenBytes)
			tamperedHmac[len(tamperedHmac)-1] ^= 0x01
			if tokenObj.VerifyHMAC(tamperedHmac) {
				t.Errorf("VerifyHMAC succeeded on tampered HMAC")
			}
			if _, err := tokenObj.Decrypt(tamperedHmac); err == nil {
				t.Errorf("Decrypt succeeded on tampered HMAC, expected error")
			}
		})
	}
}
