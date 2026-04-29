// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"crypto/rand"
	"fmt"
	"math/bits"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// StampWorkblock generates the deterministic workblock used as the foundation for anti-spam hashcash calculations.
func StampWorkblock(material []byte, expandRounds int) ([]byte, error) {
	if len(material) == 0 {
		return nil, fmt.Errorf("stamp workblock material is required")
	}
	if expandRounds <= 0 {
		expandRounds = WorkblockExpandRounds
	}

	workblock := make([]byte, 0, expandRounds*256)
	for n := 0; n < expandRounds; n++ {
		nPacked, err := msgpack.Pack(int64(n))
		if err != nil {
			return nil, fmt.Errorf("pack round value %v: %w", n, err)
		}

		saltMaterial := make([]byte, 0, len(material)+len(nPacked))
		saltMaterial = append(saltMaterial, material...)
		saltMaterial = append(saltMaterial, nPacked...)
		salt := rns.FullHash(saltMaterial)

		part, err := crypto.HKDF(256, material, salt, nil)
		if err != nil {
			return nil, fmt.Errorf("derive workblock round %v: %w", n, err)
		}

		workblock = append(workblock, part...)
	}

	return workblock, nil
}

// StampValue computes the hashcash value of a stamp by measuring the number of leading zero bits in its hash.
func StampValue(workblock, stamp []byte) int {
	material := make([]byte, 0, len(workblock)+len(stamp))
	material = append(material, workblock...)
	material = append(material, stamp...)
	h := rns.FullHash(material)
	return leadingZeroBits(h)
}

// StampValid verifies if a provided stamp meets or exceeds the required target cost for the given workblock.
func StampValid(stamp []byte, targetCost int, workblock []byte) bool {
	if targetCost <= 0 {
		return true
	}
	if targetCost > 256 {
		return false
	}

	value := StampValue(workblock, stamp)
	return value >= targetCost
}

// ValidatePeeringKey validates a prospective peer's key against a target computational cost to prevent peering spam.
func ValidatePeeringKey(peeringID, peeringKey []byte, targetCost int) bool {
	if len(peeringID) == 0 || len(peeringKey) == 0 {
		return false
	}
	workblock, err := StampWorkblock(peeringID, WorkblockExpandRoundsPeering)
	if err != nil {
		return false
	}
	return StampValid(peeringKey, targetCost, workblock)
}

// GenerateStamp performs the computational work required to produce a valid stamp meeting the specified target cost.
func GenerateStamp(material []byte, targetCost int, expandRounds int) ([]byte, int, int, error) {
	if targetCost < 0 {
		return nil, 0, 0, nil
	}
	if targetCost > 256 {
		return nil, 0, 0, fmt.Errorf("invalid target cost %v", targetCost)
	}

	workblock, err := StampWorkblock(material, expandRounds)
	if err != nil {
		return nil, 0, 0, err
	}

	rounds := 0
	for {
		candidate := make([]byte, StampSize)
		if _, err := rand.Read(candidate); err != nil {
			return nil, 0, rounds, fmt.Errorf("generate random stamp candidate: %w", err)
		}
		rounds++

		if StampValid(candidate, targetCost, workblock) {
			value := StampValue(workblock, candidate)
			return candidate, value, rounds, nil
		}
	}
}

func leadingZeroBits(data []byte) int {
	count := 0
	for _, b := range data {
		if b == 0 {
			count += 8
			continue
		}
		count += bits.LeadingZeros8(uint8(b))
		break
	}
	return count
}
