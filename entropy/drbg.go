// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	// drbgLabel domain-separates this generator's stream from any other use of
	// HKDF over the same seed material.
	drbgLabel = "go-reticulum/entropy/v1"

	// DefaultRefillSize is how many bytes of stream are produced per HKDF
	// expansion. It is large enough that the expansion cost is paid once per
	// kilobyte rather than once per key.
	DefaultRefillSize = 1024

	// MinSeedBytes is the shortest seed a DRBG will accept. A short seed would
	// be accepted by HKDF and silently produce a low-entropy stream, which is
	// the failure this package exists to make impossible.
	MinSeedBytes = 32
)

// DRBG turns conditioned seed material into an unbounded stream of key
// material: HKDF-SHA256 extraction, then counter-mode expansion over the
// resulting pseudorandom key.
//
// Conditioning is what makes a raw hardware generator usable. A generator read
// once for 64 bytes yields 64 bytes of whatever quality the silicon offers;
// extracted through HKDF, the same 64 bytes seed a stream that can serve every
// key, token, and nonce the device will ever need, and the raw generator is
// touched only as often as the caller chooses to reseed.
//
// The output of a DRBG is only as unpredictable as its seed. It does not create
// entropy, and it will faithfully stretch a deterministic seed into a
// deterministic stream — which is why the gate, not this type, is what a caller
// should reach for.
type DRBG struct {
	mu         sync.Mutex
	prk        []byte
	generation uint64
	buf        []byte
	offset     int
}

var _ io.Reader = (*DRBG)(nil)

// NewDRBG conditions seed into a pseudorandom key and returns a generator over
// it. The salt is the HKDF salt: supplying the device's unique identifier here
// makes the stream of every device distinct even when the seeds match, so a
// fleet cannot collide in software.
func NewDRBG(seed, salt []byte) (*DRBG, error) {
	d := &DRBG{}
	if err := d.Reseed(seed, salt); err != nil {
		return nil, err
	}
	return d, nil
}

// Reseed replaces the seed material and discards any buffered output, so the
// stream continues from a new pseudorandom key. It is how a long-running device
// folds freshly collected entropy into a generator that is already in use.
func (d *DRBG) Reseed(seed, salt []byte) error {
	if len(seed) < MinSeedBytes {
		return fmt.Errorf("%w: seed is %v bytes, need at least %v",
			ErrBadParameter, len(seed), MinSeedBytes)
	}
	prk, err := hkdf.Extract(sha256.New, seed, salt)
	if err != nil {
		return fmt.Errorf("could not condition entropy seed: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.prk = prk
	d.generation++
	d.buf = nil
	d.offset = 0
	return nil
}

// Read fills b with stream output, refilling the buffer as needed.
func (d *DRBG) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.prk == nil {
		return 0, ErrDestroyed
	}

	written := 0
	for written < len(b) {
		if d.offset >= len(d.buf) {
			if err := d.refill(); err != nil {
				return written, err
			}
		}
		n := copy(b[written:], d.buf[d.offset:])
		written += n
		d.offset += n
	}
	return written, nil
}

// refill expands the next slice of stream into the buffer. The caller holds the
// lock, and the generation counter keeps every slice of stream distinct without
// depending on the buffer having been consumed in any particular order.
func (d *DRBG) refill() error {
	info := fmt.Sprintf("%v/generation/%v", drbgLabel, d.generation)
	buf, err := hkdf.Expand(sha256.New, d.prk, info, DefaultRefillSize)
	if err != nil {
		return fmt.Errorf("could not expand entropy stream: %w", err)
	}
	d.generation++
	d.buf = buf
	d.offset = 0
	return nil
}

// Destroy discards the seed material. Keys already derived from it stay valid;
// this only prevents further output.
func (d *DRBG) Destroy() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.prk {
		d.prk[i] = 0
	}
	d.prk = nil
	d.buf = nil
	d.offset = 0
}

// ErrDestroyed is returned by a DRBG that has been destroyed.
var ErrDestroyed = errors.New("entropy generator has been destroyed")
