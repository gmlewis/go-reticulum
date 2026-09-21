// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package entropy is the randomness seam for go-reticulum on devices that boot
// without an operating system.
//
// A hosted build takes its key material from crypto/rand, which the operating
// system backs with a seeded, health-checked CSPRNG. A bare-metal build has no
// such thing: the only randomness available is whatever the SoC generator
// produces at the moment an identity is first created. On several SoC families
// that generator is documented as producing true random numbers only while a
// particular entropy source is enabled — an ADC, or the radio subsystem. A
// firmware that reads the register anyway still gets bytes back; they are just
// not guaranteed to be unpredictable.
//
// The failure that matters is not a crash. It is a device whose permanent
// Reticulum identity was derived from entropy the silicon never provided. Every
// unit flashed with the same firmware then shares one identity, or an attacker
// can derive it. Both are silent: the network works, announces verify, and
// nothing looks wrong until two nodes start answering each other's traffic.
//
// This package exists so a first boot cannot come up that way quietly:
//
//   - Source is the raw hardware generator, behind Name and Read.
//   - Health applies the NIST SP 800-90B on-line tests to the raw samples, so a
//     stuck, shorted, or grossly biased register is caught before any byte of
//     it reaches a key.
//   - DRBG conditions the collected samples through HKDF-SHA256 into a stream,
//     the construction Espressif recommends for the ESP32 family.
//   - Reader ties the three together as an io.Reader. It refuses to emit
//     anything until enough min-entropy has been credited, and it fails
//     closed: an unhealthy or absent source produces an error, never a
//     fallback key.
//
// # What the health tests do and do not buy
//
// The tests detect the failures silicon actually exhibits: a register stuck at
// one value, a source that has become grossly biased, a peripheral whose clock
// is gated so it returns the same word forever. They cannot detect a source
// that is well distributed but not secret — a counter, a free-running clock,
// another device's public identifier. No statistical test can; unpredictability
// is not a property statistics can observe.
//
// That property has to come from the hardware. Point Source at a documented
// hardware generator with its entropy source demonstrably enabled, and pass the
// device's unique identifier as Options.Salt. The salt makes two units collide
// only if they collide in hardware, which no amount of broken firmware can
// undo. It buys uniqueness and never secrecy: identifiers are public, so the
// salt must stay a salt and never become the secret itself.
//
// # Use
//
// The gate is installed into the Reticulum stack with rns/crypto's
// SetEntropySource, once, before anything creates an identity:
//
//	// soc.NewRNGSource is a Source over the SoC generator, and deviceID is the
//	// eFuse identifier read at boot.
//	if err := crypto.SetEntropySource(soc.NewRNGSource(), entropy.Options{
//		Salt: deviceID,
//	}); err != nil {
//		// No identity can be created. Report it on the display and retry;
//		// do not substitute anything for it.
//	}
//
// Afterwards every identity, token key, and random hash in the process is drawn
// from the gate, and a first boot on a chip with a dead generator fails to
// create an identity instead of creating a shareable one.
package entropy

import (
	"errors"
	"io"
)

// Errors reported by the source and gate.
var (
	// ErrNoSource reports a gate that was created, or asked to seed, without
	// an entropy source. It is a construction error, not a runtime one.
	ErrNoSource = errors.New("no entropy source")

	// ErrInsufficientEntropy reports that a source did not yield enough
	// min-entropy to reach the configured floor within the configured number
	// of collection rounds. The usual cause is a source that is present but
	// not actually running — an unclocked peripheral answering with a constant
	// is caught earlier, by the health tests, and reported as
	// ErrHealthTestFailed.
	ErrInsufficientEntropy = errors.New("insufficient entropy")

	// ErrHealthTestFailed reports that the raw source failed an on-line health
	// test. The gate discards everything it had collected from that source and
	// refuses to seed.
	ErrHealthTestFailed = errors.New("entropy source failed a health test")

	// ErrBadParameter reports a health-test or gate parameter that cannot
	// produce a meaningful result, such as a window smaller than the count it
	// is meant to bound.
	ErrBadParameter = errors.New("invalid entropy parameter")
)

// Source is a raw hardware entropy source: the SoC generator, behind the one
// method the gate needs.
//
// Implementations must fill b completely or return an error; a partial read is
// an error. They must not block indefinitely, because the caller is usually
// holding the lock that guards identity creation.
//
// The bytes a Source returns are treated as raw samples and are never used as
// key material directly. They are health-tested, folded into an entropy pool,
// and conditioned through HKDF before any of them reach a key.
type Source interface {
	// Name identifies the source in error messages and logs. Use something an
	// operator can act on: "esp32c5-sar-adc" says which generator and which
	// entropy source, while "rng" does not.
	Name() string

	// Read fills b with raw samples from the device.
	io.Reader
}

// Stats reports what a gate has collected from its source. It exists for the
// first-boot path on a device with a display: an appliance that is still
// gathering entropy can say so rather than appearing to hang.
type Stats struct {
	// SourceName is the name reported by the source.
	SourceName string

	// CreditedBits is the min-entropy credited so far, in bits. It reaches the
	// configured floor before the first output byte is produced.
	CreditedBits float64

	// Rounds is the number of completed collection rounds.
	Rounds int

	// Seeded reports whether the gate has produced its first seed.
	Seeded bool

	// Healthy reports whether every sample seen so far has passed the on-line
	// health tests.
	Healthy bool
}
