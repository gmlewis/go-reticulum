// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	crand "crypto/rand"
	"errors"
	"testing"

	"github.com/gmlewis/go-reticulum/entropy"
)

// patternReader yields a fixed pattern forever, so a test can stand in a
// deterministic generator for the real one.
type patternReader struct {
	pattern []byte
	offset  int
}

func newPatternReader(pattern []byte) *patternReader {
	return &patternReader{pattern: pattern}
}

func (p *patternReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = p.pattern[p.offset]
		p.offset = (p.offset + 1) % len(p.pattern)
	}
	return len(b), nil
}

// shortReader runs dry after a few bytes, standing in for a peripheral that
// answers a request only partially.
type shortReader struct {
	remaining int
}

func (s *shortReader) Read(b []byte) (int, error) {
	if s.remaining <= 0 {
		return 0, errors.New("source exhausted")
	}
	n := min(len(b), s.remaining)
	s.remaining -= n
	return n, nil
}

// The tests in this file replace process-wide state, so they deliberately do not
// call t.Parallel: each one installs a source, observes the primitives that read
// from it, and restores the default before returning.

func TestRandomBytesUsesTheOSSourceByDefault(t *testing.T) {
	if got := RandomReader(); got != crand.Reader {
		t.Errorf("RandomReader() = %v, want the operating-system source", got)
	}

	first := make([]byte, 32)
	second := make([]byte, 32)
	if _, err := RandomBytes(first); err != nil {
		t.Fatalf("RandomBytes() = %v", err)
	}
	if _, err := RandomBytes(second); err != nil {
		t.Fatalf("RandomBytes() = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Error("two reads from the default source returned identical bytes")
	}
}

func TestSetRandomSourceInstallsTheReader(t *testing.T) {
	t.Cleanup(ResetRandomSource)

	pattern := make([]byte, 64)
	for i := range pattern {
		pattern[i] = byte(i)
	}
	SetRandomSource(newPatternReader(pattern))

	if got := RandomReader(); got == crand.Reader {
		t.Error("RandomReader() still reports the operating-system source")
	}

	out := make([]byte, len(pattern))
	if _, err := RandomBytes(out); err != nil {
		t.Fatalf("RandomBytes() = %v", err)
	}
	if !bytes.Equal(out, pattern) {
		t.Errorf("RandomBytes() = %#x, want the installed pattern", out)
	}

	ResetRandomSource()
	if got := RandomReader(); got != crand.Reader {
		t.Error("ResetRandomSource() did not restore the operating-system source")
	}
}

// TestRandomBytesFailsClosedOnShortRead checks that a source which cannot supply
// the bytes reports it, rather than handing back a partly-filled buffer that a
// caller might mistake for key material.
func TestRandomBytesFailsClosedOnShortRead(t *testing.T) {
	t.Cleanup(ResetRandomSource)

	SetRandomSource(&shortReader{remaining: 4})
	if _, err := RandomBytes(make([]byte, 32)); err == nil {
		t.Error("RandomBytes() = nil, want an error for a short read")
	}
}

// TestKeyGenerationFollowsTheInstalledSource pins the seam itself: with a raw
// deterministic reader installed, every key the stack generates is
// reproducible. That is the behavior the seam exposes — and the reason the raw
// source is never what a device should install.
func TestKeyGenerationFollowsTheInstalledSource(t *testing.T) {
	t.Cleanup(ResetRandomSource)

	install := func() {
		pattern := make([]byte, 256)
		for i := range pattern {
			pattern[i] = byte(i * 7)
		}
		SetRandomSource(newPatternReader(pattern))
	}

	install()
	firstX, err := GenerateX25519PrivateKey()
	if err != nil {
		t.Fatalf("GenerateX25519PrivateKey() = %v", err)
	}
	firstEd, err := GenerateEd25519PrivateKey()
	if err != nil {
		t.Fatalf("GenerateEd25519PrivateKey() = %v", err)
	}
	firstToken, err := GenerateTokenKey(false)
	if err != nil {
		t.Fatalf("GenerateTokenKey() = %v", err)
	}

	install()
	secondX, err := GenerateX25519PrivateKey()
	if err != nil {
		t.Fatalf("GenerateX25519PrivateKey() = %v", err)
	}
	secondEd, err := GenerateEd25519PrivateKey()
	if err != nil {
		t.Fatalf("GenerateEd25519PrivateKey() = %v", err)
	}
	secondToken, err := GenerateTokenKey(false)
	if err != nil {
		t.Fatalf("GenerateTokenKey() = %v", err)
	}

	if !bytes.Equal(firstX.PrivateBytes(), secondX.PrivateBytes()) {
		t.Error("X25519 key generation did not follow the installed source")
	}
	if !bytes.Equal(firstEd.PrivateBytes(), secondEd.PrivateBytes()) {
		t.Error("Ed25519 key generation did not follow the installed source")
	}
	if !bytes.Equal(firstToken, secondToken) {
		t.Error("token key generation did not follow the installed source")
	}
}

// TestSetEntropySourceFailsClosedOnStuckGenerator is the difference between the
// seam and the gate. A device whose hardware generator is stuck at one value
// must not create a key at all — not a weak one, and not a shareable one that
// every unit flashed with the same firmware would also produce.
func TestSetEntropySourceFailsClosedOnStuckGenerator(t *testing.T) {
	t.Cleanup(ResetRandomSource)

	stuck := entropy.SourceFunc{
		Label: "stuck-register",
		ReadFunc: func(b []byte) (int, error) {
			for i := range b {
				b[i] = 0x00
			}
			return len(b), nil
		},
	}
	if err := SetEntropySource(stuck, entropy.Options{Salt: []byte("device-0001")}); err != nil {
		t.Fatalf("SetEntropySource() = %v", err)
	}

	if _, err := GenerateX25519PrivateKey(); !errors.Is(err, entropy.ErrHealthTestFailed) {
		t.Errorf("GenerateX25519PrivateKey() error = %v, want ErrHealthTestFailed", err)
	}
	if _, err := GenerateEd25519PrivateKey(); !errors.Is(err, entropy.ErrHealthTestFailed) {
		t.Errorf("GenerateEd25519PrivateKey() error = %v, want ErrHealthTestFailed", err)
	}
	if _, err := GenerateTokenKey(true); !errors.Is(err, entropy.ErrHealthTestFailed) {
		t.Errorf("GenerateTokenKey() error = %v, want ErrHealthTestFailed", err)
	}
}

func TestSetEntropySourceProducesKeys(t *testing.T) {
	t.Cleanup(ResetRandomSource)

	if err := SetEntropySource(rampSource("sar-adc"), entropy.Options{Salt: []byte("device-0001")}); err != nil {
		t.Fatalf("SetEntropySource() = %v", err)
	}

	x, err := GenerateX25519PrivateKey()
	if err != nil {
		t.Fatalf("GenerateX25519PrivateKey() = %v", err)
	}
	ed, err := GenerateEd25519PrivateKey()
	if err != nil {
		t.Fatalf("GenerateEd25519PrivateKey() = %v", err)
	}
	if bytes.Equal(x.PrivateBytes(), make([]byte, 32)) {
		t.Error("X25519 private key is all zeros")
	}
	if bytes.Equal(ed.PrivateBytes(), make([]byte, 32)) {
		t.Error("Ed25519 private key is all zeros")
	}

	// A second device reading the same generator must still come up with
	// different key material, because its identifier differs.
	if err := SetEntropySource(rampSource("sar-adc"), entropy.Options{Salt: []byte("device-0002")}); err != nil {
		t.Fatalf("SetEntropySource() = %v", err)
	}
	other, err := GenerateX25519PrivateKey()
	if err != nil {
		t.Fatalf("GenerateX25519PrivateKey() = %v", err)
	}
	if bytes.Equal(x.PrivateBytes(), other.PrivateBytes()) {
		t.Error("two devices with the same generator and different identifiers produced the same key")
	}
}

func TestSetEntropySourceRejectsNilSource(t *testing.T) {
	if err := SetEntropySource(nil, entropy.Options{}); !errors.Is(err, entropy.ErrNoSource) {
		t.Errorf("SetEntropySource(nil) error = %v, want ErrNoSource", err)
	}
	if got := RandomReader(); got != crand.Reader {
		t.Error("a rejected source was installed anyway")
	}
}

// rampSource yields every byte value in turn: balanced enough to pass the health
// tests, deterministic enough to keep a test repeatable.
func rampSource(name string) entropy.Source {
	var next byte
	return entropy.SourceFunc{
		Label: name,
		ReadFunc: func(b []byte) (int, error) {
			for i := range b {
				b[i] = next
				next++
			}
			return len(b), nil
		},
	}
}
