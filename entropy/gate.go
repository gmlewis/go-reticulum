// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"sync"
)

// Default gate parameters.
const (
	// DefaultMinEntropyBits is the credited-entropy floor a source must reach
	// before the gate produces its first output byte. 256 bits is the size of a
	// symmetric key: it is what the keys drawn from the stream are worth, and
	// banking less than that would make the conditioning cosmetic.
	DefaultMinEntropyBits = 256.0

	// DefaultMinEntropyBitsPerByte is the min-entropy credited per accepted
	// byte. It is deliberately an order of magnitude below what a healthy byte
	// generator holds, because the gate cannot measure the true figure and
	// crediting optimistically is how a weak source gets promoted to a strong
	// one.
	DefaultMinEntropyBitsPerByte = 1.0

	// DefaultChunkSize is how many raw bytes are drawn from the source per
	// collection round.
	DefaultChunkSize = 32

	// DefaultMaxRounds bounds the rounds a single collection attempt may take.
	// With the default chunk size and credit rate it allows 2048 raw bytes for
	// a 256-bit floor, which leaves ample headroom for a slow generator while
	// still terminating when a source is present but not producing.
	DefaultMaxRounds = 64
)

// Options configures a gate. The zero value is valid and applies the defaults.
type Options struct {
	// Salt is mixed into the DRBG extraction as the HKDF salt, and is the
	// device's unique identifier — an eFuse MAC, a serial number, a secure
	// element's public identifier. Supplying it makes two units collide only if
	// they collide in hardware, which no firmware fault can undo.
	//
	// It is a salt and never a secret. Identifiers are public, so this buys
	// uniqueness and never secrecy, and the seed must not be derived from it.
	Salt []byte

	// MinEntropyBits is the credited-entropy floor, in bits. Zero selects
	// DefaultMinEntropyBits; a negative value is rejected.
	MinEntropyBits float64

	// MinEntropyBitsPerByte is the min-entropy credited per accepted byte. Zero
	// selects DefaultMinEntropyBitsPerByte; a negative value is rejected.
	MinEntropyBitsPerByte float64

	// ChunkSize is how many raw bytes are drawn from the source per round. Zero
	// selects DefaultChunkSize.
	ChunkSize int

	// MaxRounds bounds the rounds one collection attempt may take. Zero selects
	// DefaultMaxRounds.
	MaxRounds int

	// Health configures the on-line health tests. The zero value applies the
	// defaults, which is what almost every caller wants.
	Health Health
}

// Reader is the fail-closed entropy gate: the io.Reader a device hands to the
// Reticulum stack in place of crypto/rand.
//
// It draws raw samples from a Source, runs the on-line health tests over every
// sample, folds what passes into an entropy pool, and conditions the pool into a
// DRBG stream. Nothing is emitted until the pool holds the configured floor of
// credited min-entropy, and a source that errors, fails a health test, or does
// not reach the floor within the configured rounds produces an error rather than
// a fallback.
//
// That last property is the point. A device that cannot obtain entropy must fail
// to create an identity, loudly, in front of the person holding it — never
// create one from whatever happened to be in the register.
type Reader struct {
	mu     sync.Mutex
	source Source
	opts   Options
	health Health

	pool         []byte
	creditedBits float64
	rounds       int
	healthy      bool
	drbg         *DRBG
}

var _ io.Reader = (*Reader)(nil)

// NewReader returns a gate over source. It fails if no source is given or if the
// options cannot describe a meaningful test, so a misconfigured device fails at
// start-up rather than at the moment its identity is created.
func NewReader(source Source, opts Options) (*Reader, error) {
	if source == nil {
		return nil, ErrNoSource
	}
	normalized, err := opts.normalized()
	if err != nil {
		return nil, err
	}
	if _, _, err := normalized.Health.Cutoffs(); err != nil {
		return nil, err
	}
	return &Reader{
		source:  source,
		opts:    normalized,
		health:  normalized.Health,
		healthy: true,
	}, nil
}

// normalized fills in the defaults and rejects values that cannot be satisfied.
func (o Options) normalized() (Options, error) {
	switch {
	case math.IsNaN(o.MinEntropyBits) || math.IsInf(o.MinEntropyBits, 0):
		return o, fmt.Errorf("%w: min-entropy floor %v", ErrBadParameter, o.MinEntropyBits)
	case o.MinEntropyBits < 0:
		return o, fmt.Errorf("%w: min-entropy floor %v", ErrBadParameter, o.MinEntropyBits)
	case math.IsNaN(o.MinEntropyBitsPerByte) || math.IsInf(o.MinEntropyBitsPerByte, 0):
		return o, fmt.Errorf("%w: min-entropy per byte %v", ErrBadParameter, o.MinEntropyBitsPerByte)
	case o.MinEntropyBitsPerByte < 0:
		return o, fmt.Errorf("%w: min-entropy per byte %v", ErrBadParameter, o.MinEntropyBitsPerByte)
	}
	if o.MinEntropyBits == 0 {
		o.MinEntropyBits = DefaultMinEntropyBits
	}
	if o.MinEntropyBitsPerByte == 0 {
		o.MinEntropyBitsPerByte = DefaultMinEntropyBitsPerByte
	}
	if o.ChunkSize <= 0 {
		o.ChunkSize = DefaultChunkSize
	}
	if o.MaxRounds <= 0 {
		o.MaxRounds = DefaultMaxRounds
	}
	o.Salt = bytes.Clone(o.Salt)
	return o, nil
}

// Read fills b with conditioned key material, collecting and seeding on first
// use. It returns an error, and no bytes, if the source cannot supply the
// configured floor of min-entropy.
func (r *Reader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.drbg == nil {
		if err := r.seedLocked(); err != nil {
			return 0, err
		}
	}
	return r.drbg.Read(b)
}

// Seed collects entropy and seeds the generator, without waiting for the first
// Read. A device with a display should call it during start-up so that entropy
// being slow is reported where a person can see it, rather than at the moment
// the identity is first needed.
//
// It is a no-op once the gate is seeded.
func (r *Reader) Seed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.drbg != nil {
		return nil
	}
	return r.seedLocked()
}

// Reseed folds freshly collected entropy into a generator that is already in
// use. It is how a long-running device keeps a stream that has produced a lot of
// key material from resting on a single boot-time seed.
//
// A reseed that fails leaves the existing generator intact and still usable, so
// a transient source fault cannot take a working device offline.
func (r *Reader) Reseed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.drbg == nil {
		return r.seedLocked()
	}

	r.creditedBits = 0
	pool, err := r.gatherLocked(r.opts.MinEntropyBits)
	if err != nil {
		return err
	}
	return r.drbg.Reseed(pool, r.opts.Salt)
}

// Stats reports what the gate has collected so far.
func (r *Reader) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{
		SourceName:   r.source.Name(),
		CreditedBits: r.creditedBits,
		Rounds:       r.rounds,
		Seeded:       r.drbg != nil,
		Healthy:      r.healthy,
	}
}

// seedLocked collects the floor of entropy and seeds a new generator from the
// pool. The caller holds the lock.
func (r *Reader) seedLocked() error {
	pool, err := r.gatherLocked(r.opts.MinEntropyBits)
	if err != nil {
		return err
	}
	drbg, err := NewDRBG(pool, r.opts.Salt)
	if err != nil {
		return err
	}
	r.drbg = drbg
	return nil
}

// gatherLocked draws from the source until needBits of min-entropy have been
// credited, returning the pool. Every failure path discards the pool first: a
// source that produced some samples before failing must not leave any of them
// behind for a later attempt to credit. The caller holds the lock.
func (r *Reader) gatherLocked(needBits float64) ([]byte, error) {
	buf := make([]byte, r.opts.ChunkSize)
	r.rounds = 0
	for r.creditedBits < needBits {
		if r.rounds >= r.opts.MaxRounds {
			credited, rounds := r.creditedBits, r.rounds
			r.discardLocked()
			return nil, fmt.Errorf("%w: %v credited of %v bits from %v after %v rounds",
				ErrInsufficientEntropy, credited, needBits, r.source.Name(), rounds)
		}
		r.rounds++

		if _, err := io.ReadFull(r.source, buf); err != nil {
			r.discardLocked()
			return nil, fmt.Errorf("entropy source %v failed: %w", r.source.Name(), err)
		}
		for _, sample := range buf {
			if err := r.health.Observe(sample); err != nil {
				r.discardLocked()
				r.healthy = false
				return nil, fmt.Errorf("entropy source %v: %w", r.source.Name(), err)
			}
		}

		r.pool = foldSamples(r.pool, buf)
		r.creditedBits += float64(len(buf)) * r.opts.MinEntropyBitsPerByte
	}
	return r.pool, nil
}

// discardLocked throws away everything collected from the source. The health
// tests restart with it: a source that failed and was replaced, or that came
// back, should be judged on its new samples rather than on the record of the
// attempt that failed. The failure itself has already been reported to the
// caller. The caller holds the lock.
func (r *Reader) discardLocked() {
	for i := range r.pool {
		r.pool[i] = 0
	}
	r.pool = nil
	r.creditedBits = 0
	r.health.Reset()
}

// foldSamples folds a round of accepted samples into the pool. A SHA-256 digest
// is the pool: it retains up to 256 bits of whatever entropy is folded in, and
// folding is one-way, so no attacker who learns the pool learns the samples.
func foldSamples(pool, samples []byte) []byte {
	h := sha256.New()
	h.Write(pool)
	h.Write(samples)
	return h.Sum(nil)
}
