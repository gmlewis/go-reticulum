// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/bits"
	"sync"

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
	return GenerateStampWithContext(context.Background(), material, targetCost, expandRounds)
}

// GenerateStampWithContext is the cancellation-aware variant of
// GenerateStamp. The returned stamp, value, and round count match those of
// GenerateStamp, but the stamp search can be aborted by canceling the
// provided context. When the context is canceled, GenerateStampWithContext
// returns ctx.Err() and a nil stamp.
func GenerateStampWithContext(ctx context.Context, material []byte, targetCost int, expandRounds int) ([]byte, int, int, error) {
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
	buf := make([]byte, len(workblock)+StampSize)
	copy(buf, workblock)
	candidate := buf[len(workblock):]

	if _, err := rand.Read(candidate); err != nil {
		return nil, 0, rounds, fmt.Errorf("generate random stamp candidate: %w", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, rounds, err
		}
		rounds++

		h := sha256.Sum256(buf)
		if val := leadingZeroBits(h[:]); val >= targetCost {
			stampCopy := make([]byte, StampSize)
			copy(stampCopy, candidate)
			return stampCopy, val, rounds, nil
		}

		for j := 0; j < len(candidate); j++ {
			candidate[j]++
			if candidate[j] != 0 {
				break
			}
		}
	}
}

// GenerateStampParallel performs the stamp search using the given number
// of goroutines, each with its own candidate generator. As soon as one
// goroutine finds a valid stamp, the others exit and the call returns.
// It is the Go port of Python's job_linux (and the Linux path of
// generate_stamp). The returned values match those of GenerateStamp;
// the round count is the sum of rounds executed across all workers.
func GenerateStampParallel(material []byte, targetCost int, expandRounds int, workers int) ([]byte, int, int, error) {
	if targetCost < 0 {
		return nil, 0, 0, nil
	}
	if targetCost > 256 {
		return nil, 0, 0, fmt.Errorf("invalid target cost %v", targetCost)
	}
	if workers < 1 {
		workers = 1
	}
	if workers > 64 {
		workers = 64
	}

	workblock, err := StampWorkblock(material, expandRounds)
	if err != nil {
		return nil, 0, 0, err
	}

	type result struct {
		stamp []byte
		round int
	}
	results := make(chan result, workers)
	stopCh := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			localRounds := 0
			buf := make([]byte, len(workblock)+StampSize)
			copy(buf, workblock)
			candidate := buf[len(workblock):]

			if _, err := rand.Read(candidate); err != nil {
				results <- result{nil, localRounds}
				return
			}

			for {
				select {
				case <-stopCh:
					results <- result{nil, localRounds}
					return
				default:
				}

				localRounds++
				h := sha256.Sum256(buf)
				if val := leadingZeroBits(h[:]); val >= targetCost {
					stampCopy := make([]byte, StampSize)
					copy(stampCopy, candidate)
					results <- result{stampCopy, localRounds}
					return
				}

				for j := 0; j < len(candidate); j++ {
					candidate[j]++
					if candidate[j] != 0 {
						break
					}
				}
			}
		}()
	}

	// Wait for the first valid stamp.
	var stamp []byte
	totalRounds := 0
	received := 0
	for received < workers {
		res := <-results
		received++
		totalRounds += res.round
		if stamp == nil && res.stamp != nil {
			stamp = res.stamp
			close(stopCh)
		}
	}
	wg.Wait()

	if stamp == nil {
		return nil, 0, totalRounds, fmt.Errorf("no worker produced a valid stamp")
	}
	value := StampValue(workblock, stamp)
	return stamp, value, totalRounds, nil
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
