// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
)

func TestStampWorkblockDeterministic(t *testing.T) {
	material := []byte("material-for-stamps")
	w1, err := StampWorkblock(material, 2)
	if err != nil {
		t.Fatalf("StampWorkblock #1: %v", err)
	}
	w2, err := StampWorkblock(material, 2)
	if err != nil {
		t.Fatalf("StampWorkblock #2: %v", err)
	}

	if len(w1) != 512 {
		t.Fatalf("workblock length=%v want=512", len(w1))
	}
	if string(w1) != string(w2) {
		t.Fatal("workblock must be deterministic for same material and rounds")
	}
}

func TestStampValidAndGenerateStamp(t *testing.T) {
	material := []byte("message-id")
	stamp, value, rounds, err := GenerateStamp(material, 4, 1)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}
	if len(stamp) != StampSize {
		t.Fatalf("stamp length=%v want=%v", len(stamp), StampSize)
	}
	if rounds <= 0 {
		t.Fatalf("rounds=%v want>0", rounds)
	}

	workblock, err := StampWorkblock(material, 1)
	if err != nil {
		t.Fatalf("StampWorkblock: %v", err)
	}
	if !StampValid(stamp, 4, workblock) {
		t.Fatal("generated stamp should be valid for target cost")
	}
	if value < 4 {
		t.Fatalf("stamp value=%v want>=4", value)
	}
}

func TestStampValueLeadingZeroBits(t *testing.T) {
	workblock := []byte{0x01, 0x02, 0x03}
	stamp := make([]byte, StampSize)
	value := StampValue(workblock, stamp)
	if value < 0 || value > 256 {
		t.Fatalf("stamp value out of range: %v", value)
	}
}

func TestValidatePeeringKey(t *testing.T) {
	peeringID := []byte("router-hash-remote-hash")
	key, value, _, err := GenerateStamp(peeringID, 4, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}
	if value < 4 {
		t.Fatalf("key value=%v want>=4", value)
	}

	if !ValidatePeeringKey(peeringID, key, 4) {
		t.Fatal("expected generated key to validate")
	}
	if ValidatePeeringKey(peeringID, []byte("invalid"), 4) {
		t.Fatal("expected invalid key to fail validation")
	}
}
