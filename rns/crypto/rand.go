// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	crand "crypto/rand"
	"io"
	"sync"

	"github.com/gmlewis/go-reticulum/entropy"
)

// randomSource is the process-wide source of randomness for every key, nonce,
// token, and random hash in the stack. It defaults to crypto/rand, which the
// operating system backs with a seeded CSPRNG, and a bare-metal build replaces
// it with the gated hardware generator by calling SetEntropySource before
// anything creates an identity.
var (
	randomMu     sync.RWMutex
	randomSource io.Reader = crand.Reader
)

// SetRandomSource installs the reader that all random key material is drawn
// from. A nil reader restores the default.
//
// This is the low-level seam; a bare-metal build normally wants
// SetEntropySource, which wraps a hardware generator in the health-tested,
// fail-closed gate of the entropy package and then calls this.
func SetRandomSource(source io.Reader) {
	if source == nil {
		source = crand.Reader
	}
	randomMu.Lock()
	defer randomMu.Unlock()
	randomSource = source
}

// SetEntropySource installs a gated hardware entropy source, so that every
// identity, token key, and random hash in the process is drawn from it. It is
// the one call a bare-metal device makes at start-up before creating an
// identity:
//
//	if err := crypto.SetEntropySource(soc.RNGSource(), entropy.Options{
//		Salt: deviceID,
//	}); err != nil {
//		// No identity can be created. Report it and retry; there is no
//		// fallback, by design.
//	}
//
// The gate it installs fails closed: if the generator is stuck, absent, or
// grossly biased, key generation returns an error instead of producing a
// predictable identity.
func SetEntropySource(source entropy.Source, opts entropy.Options) error {
	reader, err := entropy.NewReader(source, opts)
	if err != nil {
		return err
	}
	SetRandomSource(reader)
	return nil
}

// ResetRandomSource restores the default operating-system source. It exists for
// tests and for a device that tears down a hardware source it no longer trusts.
func ResetRandomSource() {
	SetRandomSource(nil)
}

// RandomReader returns the current source. Callers that need an io.Reader, such
// as math/big's Rand for a shuffle, take it from here rather than reaching for
// crypto/rand directly, so the seam stays complete.
func RandomReader() io.Reader {
	randomMu.RLock()
	defer randomMu.RUnlock()
	return randomSource
}

// RandomBytes fills b completely with random bytes from the current source. It
// has the same signature as crypto/rand.Read, so it drops into the call sites
// that already take a read function.
//
// Unlike a single Read on the source, a short read is always an error: a caller
// that asked for 32 bytes of key material is never told it succeeded when the
// source supplied fewer. Some of b may already have been written when the error
// is returned, so a caller that sees one must discard b rather than use it.
func RandomBytes(b []byte) (int, error) {
	return io.ReadFull(RandomReader(), b)
}
