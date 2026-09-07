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

type asicGoldenCase struct {
	name             string
	material         string
	expandRounds     int
	workblockLen     int
	midstateHex      string
	totalLenBits     uint64
	baseCandidateHex string
	startNonce       uint64
	targetCost       int
	expectedNonce    uint64
	expectedZeros    int
	expectedDigest   string
	expectedCandHex  string
	expectedRounds   uint64
}

var asicGoldenCases = []asicGoldenCase{
	{
		name:             "LXMF Message - Target 1",
		material:         "lxmf-msg-id-8a3b4c5d6e7f0123",
		expandRounds:     2,
		workblockLen:     512,
		midstateHex:      "f21f8fad8e7d36d1e6630185fd3fe0d93801c0e2d362a62d8b14634e0c3a0b94",
		totalLenBits:     4352,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       1,
		expectedNonce:    1,
		expectedZeros:    2,
		expectedDigest:   "3842d01d729f3c8bdc7fdd33d09c4751ce3fe489835e19398e508c54e163fb68",
		expectedCandHex:  "0202030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   2,
	},
	{
		name:             "LXMF Message - Target 2",
		material:         "lxmf-msg-id-8a3b4c5d6e7f0123",
		expandRounds:     2,
		workblockLen:     512,
		midstateHex:      "f21f8fad8e7d36d1e6630185fd3fe0d93801c0e2d362a62d8b14634e0c3a0b94",
		totalLenBits:     4352,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       2,
		expectedNonce:    1,
		expectedZeros:    2,
		expectedDigest:   "3842d01d729f3c8bdc7fdd33d09c4751ce3fe489835e19398e508c54e163fb68",
		expectedCandHex:  "0202030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   2,
	},
	{
		name:             "LXMF Message - Target 3",
		material:         "lxmf-msg-id-8a3b4c5d6e7f0123",
		expandRounds:     2,
		workblockLen:     512,
		midstateHex:      "f21f8fad8e7d36d1e6630185fd3fe0d93801c0e2d362a62d8b14634e0c3a0b94",
		totalLenBits:     4352,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       3,
		expectedNonce:    12,
		expectedZeros:    3,
		expectedDigest:   "10e5e05850cea9de2f4310785d900a4ba5670646ddecac3595ca78f5cd4d5546",
		expectedCandHex:  "0d02030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   13,
	},
	{
		name:             "LXMF Message - Target 4",
		material:         "lxmf-msg-id-8a3b4c5d6e7f0123",
		expandRounds:     2,
		workblockLen:     512,
		midstateHex:      "f21f8fad8e7d36d1e6630185fd3fe0d93801c0e2d362a62d8b14634e0c3a0b94",
		totalLenBits:     4352,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       4,
		expectedNonce:    20,
		expectedZeros:    5,
		expectedDigest:   "0653f7bdd4b027e4ceea9680a96d85049d3862bfe469b2fdee0042c460532957",
		expectedCandHex:  "1502030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   21,
	},
	{
		name:             "LXMF Message - Target 6",
		material:         "lxmf-msg-id-8a3b4c5d6e7f0123",
		expandRounds:     2,
		workblockLen:     512,
		midstateHex:      "f21f8fad8e7d36d1e6630185fd3fe0d93801c0e2d362a62d8b14634e0c3a0b94",
		totalLenBits:     4352,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       6,
		expectedNonce:    42,
		expectedZeros:    6,
		expectedDigest:   "021582d7937b58962c4f7c5d347ba43a5ed225ee79161543a5e4b85f854afba4",
		expectedCandHex:  "2b02030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   43,
	},
	{
		name:             "LXMF Message - Target 8",
		material:         "lxmf-msg-id-8a3b4c5d6e7f0123",
		expandRounds:     2,
		workblockLen:     512,
		midstateHex:      "f21f8fad8e7d36d1e6630185fd3fe0d93801c0e2d362a62d8b14634e0c3a0b94",
		totalLenBits:     4352,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       8,
		expectedNonce:    350,
		expectedZeros:    9,
		expectedDigest:   "0074870218770b63d9cbcbb192883dadb5c3e7ee176a6f1d25f3681dc6bda80f",
		expectedCandHex:  "5f03030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   351,
	},
	{
		name:             "RNode Peering - Target 1",
		material:         "rnode-peering-session-alpha",
		expandRounds:     1,
		workblockLen:     256,
		midstateHex:      "fed58e1fe08d3cd8545a71d872b356b99a7fee6dc4ff82dc467b539eb3943dd3",
		totalLenBits:     2304,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       1,
		expectedNonce:    0,
		expectedZeros:    2,
		expectedDigest:   "3420f77d19b2d1017f0e5f634e47393721f7e61ae539fa15294c4610d1371734",
		expectedCandHex:  "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   1,
	},
	{
		name:             "RNode Peering - Target 2",
		material:         "rnode-peering-session-alpha",
		expandRounds:     1,
		workblockLen:     256,
		midstateHex:      "fed58e1fe08d3cd8545a71d872b356b99a7fee6dc4ff82dc467b539eb3943dd3",
		totalLenBits:     2304,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       2,
		expectedNonce:    0,
		expectedZeros:    2,
		expectedDigest:   "3420f77d19b2d1017f0e5f634e47393721f7e61ae539fa15294c4610d1371734",
		expectedCandHex:  "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   1,
	},
	{
		name:             "RNode Peering - Target 4",
		material:         "rnode-peering-session-alpha",
		expandRounds:     1,
		workblockLen:     256,
		midstateHex:      "fed58e1fe08d3cd8545a71d872b356b99a7fee6dc4ff82dc467b539eb3943dd3",
		totalLenBits:     2304,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       4,
		expectedNonce:    17,
		expectedZeros:    4,
		expectedDigest:   "0c1adacd3fb060c24fabfd76bcf18f7e93e3313e7d6c5555cb84072b951f17bc",
		expectedCandHex:  "1202030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   18,
	},
	{
		name:             "RNode Peering - Target 7",
		material:         "rnode-peering-session-alpha",
		expandRounds:     1,
		workblockLen:     256,
		midstateHex:      "fed58e1fe08d3cd8545a71d872b356b99a7fee6dc4ff82dc467b539eb3943dd3",
		totalLenBits:     2304,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       0,
		targetCost:       7,
		expectedNonce:    179,
		expectedZeros:    9,
		expectedDigest:   "006ab20e6366f69174443aae9b15b318fd9fd7e902671d8706c3de39d5b8d243",
		expectedCandHex:  "b402030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   180,
	},
	{
		name:             "Offset Search - Target 5",
		material:         "lxmf-msg-id-8a3b4c5d6e7f0123",
		expandRounds:     2,
		workblockLen:     512,
		midstateHex:      "f21f8fad8e7d36d1e6630185fd3fe0d93801c0e2d362a62d8b14634e0c3a0b94",
		totalLenBits:     4352,
		baseCandidateHex: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		startNonce:       50,
		targetCost:       5,
		expectedNonce:    57,
		expectedZeros:    7,
		expectedDigest:   "01507faa878bb2f1a2d593820b0915673864f71e4725724ca0f9315ccc6b87ba",
		expectedCandHex:  "3a02030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		expectedRounds:   8,
	},
}

func addNonceHelper(base []byte, nonce uint64) []byte {
	res := make([]byte, len(base))
	copy(res, base)
	carry := nonce
	for i := range 8 {
		sum := uint64(res[i]) + (carry & 0xFF)
		res[i] = byte(sum)
		carry = (carry >> 8) + (sum >> 8)
	}
	return res
}

func TestAsicParityWithGoldenVectors(t *testing.T) {
	t.Parallel()

	for _, tc := range asicGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// 1. Generate workblock
			wb, err := StampWorkblock([]byte(tc.material), tc.expandRounds)
			if err != nil {
				t.Fatalf("StampWorkblock failed: %v", err)
			}
			if len(wb) != tc.workblockLen {
				t.Fatalf("workblock len = %v, want %v", len(wb), tc.workblockLen)
			}

			// 2. Parse candidate
			baseCand, err := hex.DecodeString(tc.baseCandidateHex)
			if err != nil {
				t.Fatalf("decode base candidate: %v", err)
			}
			expCand, err := hex.DecodeString(tc.expectedCandHex)
			if err != nil {
				t.Fatalf("decode expected candidate: %v", err)
			}

			// Verify candidate derivation from baseCand + expectedNonce
			derivedCand := addNonceHelper(baseCand, tc.expectedNonce)
			if hex.EncodeToString(derivedCand) != tc.expectedCandHex {
				t.Fatalf("candidate derivation mismatch:\n  got:  %x\n  want: %s", derivedCand, tc.expectedCandHex)
			}

			// 3. Verify digest and leading zero count
			fullHash := rns.FullHash(append(wb, expCand...))
			if gotHex := hex.EncodeToString(fullHash); gotHex != tc.expectedDigest {
				t.Fatalf("digest mismatch:\n  got:  %s\n  want: %s", gotHex, tc.expectedDigest)
			}

			val := StampValue(wb, expCand)
			if val != tc.expectedZeros {
				t.Fatalf("StampValue = %v, want %v", val, tc.expectedZeros)
			}

			if !StampValid(expCand, tc.targetCost, wb) {
				t.Fatalf("StampValid returned false for winning candidate")
			}

			// 4. Verify that NO prior nonce between startNonce and expectedNonce-1 met the target
			for n := tc.startNonce; n < tc.expectedNonce; n++ {
				priorCand := addNonceHelper(baseCand, n)
				if StampValid(priorCand, tc.targetCost, wb) {
					t.Fatalf("premature winner at nonce %v (expected winner was %v)", n, tc.expectedNonce)
				}
			}

			// 5. Verify round count
			expectedRounds := (tc.expectedNonce - tc.startNonce) + 1
			if expectedRounds != tc.expectedRounds {
				t.Fatalf("expectedRounds calculation mismatch: %v vs %v", expectedRounds, tc.expectedRounds)
			}
		})
	}
}
