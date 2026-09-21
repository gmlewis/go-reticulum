// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func testSeed() []byte {
	seed := make([]byte, MinSeedBytes)
	for i := range seed {
		seed[i] = byte(i)
	}
	return seed
}

func TestDRBGDeterministic(t *testing.T) {
	t.Parallel()

	seed := testSeed()
	salt := []byte("device-0001")

	first, err := NewDRBG(seed, salt)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	second, err := NewDRBG(seed, salt)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}

	a := make([]byte, 64)
	b := make([]byte, 64)
	if _, err := first.Read(a); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if _, err := second.Read(b); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two generators over the same seed and salt produced different streams")
	}
}

// TestDRBGSaltSeparatesStreams is the software half of the fleet-collision
// guarantee: two devices whose hardware generators produce identical bytes must
// still come up with different key material, because their identifiers differ.
func TestDRBGSaltSeparatesStreams(t *testing.T) {
	t.Parallel()

	seed := testSeed()

	a := make([]byte, 64)
	b := make([]byte, 64)
	fromA, err := NewDRBG(seed, []byte("device-0001"))
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	fromB, err := NewDRBG(seed, []byte("device-0002"))
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	if _, err := fromA.Read(a); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if _, err := fromB.Read(b); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if bytes.Equal(a, b) {
		t.Error("identical seeds under different salts produced the same stream")
	}
}

func TestDRBGSeedSeparatesStreams(t *testing.T) {
	t.Parallel()

	salt := []byte("device-0001")
	seed := testSeed()
	otherSeed := bytes.Clone(seed)
	otherSeed[0] ^= 0x01

	a := make([]byte, 64)
	b := make([]byte, 64)
	fromA, err := NewDRBG(seed, salt)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	fromB, err := NewDRBG(otherSeed, salt)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	if _, err := fromA.Read(a); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if _, err := fromB.Read(b); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if bytes.Equal(a, b) {
		t.Error("different seeds produced the same stream")
	}
}

// TestDRBGChunkingIsTransparent checks that the stream does not depend on how
// the caller chops up its reads: a key generator that changes its output when a
// caller reads 31 bytes instead of 32 is a bug waiting for a different caller.
func TestDRBGChunkingIsTransparent(t *testing.T) {
	t.Parallel()

	const total = 4 * DefaultRefillSize
	seed := testSeed()
	salt := []byte("device-0001")

	atOnce, err := NewDRBG(seed, salt)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	want := make([]byte, total)
	if _, err := atOnce.Read(want); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	inPieces, err := NewDRBG(seed, salt)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	got := make([]byte, 0, total)
	for _, chunk := range []int{1, 3, 7, 31, 1023, 1025, total} {
		pending := chunk
		if len(got)+pending > total {
			pending = total - len(got)
		}
		if pending <= 0 {
			continue
		}
		piece := make([]byte, pending)
		if _, err := inPieces.Read(piece); err != nil {
			t.Fatalf("Read() = %v", err)
		}
		got = append(got, piece...)
	}

	if !bytes.Equal(want, got) {
		t.Error("the stream changed when read in pieces instead of at once")
	}
}

func TestDRBGReseedChangesStream(t *testing.T) {
	t.Parallel()

	d, err := NewDRBG(testSeed(), []byte("device-0001"))
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	before := make([]byte, 64)
	if _, err := d.Read(before); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	fresh := testSeed()
	fresh[7] ^= 0xFF
	if err := d.Reseed(fresh, []byte("device-0001")); err != nil {
		t.Fatalf("Reseed() = %v", err)
	}

	after := make([]byte, 64)
	if _, err := d.Read(after); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if bytes.Equal(before, after) {
		t.Error("the stream did not change after a reseed")
	}
}

func TestDRBGRejectsShortSeed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seed []byte
	}{
		{name: "nil seed", seed: nil},
		{name: "empty seed", seed: []byte{}},
		{name: "one byte short", seed: make([]byte, MinSeedBytes-1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewDRBG(tt.seed, nil); !errors.Is(err, ErrBadParameter) {
				t.Errorf("NewDRBG(%v bytes) error = %v, want ErrBadParameter", len(tt.seed), err)
			}
		})
	}
}

func TestDRBGZeroLengthReadKeepsStream(t *testing.T) {
	t.Parallel()

	d, err := NewDRBG(testSeed(), nil)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	if n, err := d.Read(nil); n != 0 || err != nil {
		t.Fatalf("Read(nil) = (%v, %v), want (0, nil)", n, err)
	}
	first := make([]byte, 32)
	if _, err := d.Read(first); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if n, err := d.Read([]byte{}); n != 0 || err != nil {
		t.Fatalf("Read([]byte{}) = (%v, %v), want (0, nil)", n, err)
	}

	fresh, err := NewDRBG(testSeed(), nil)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	want := make([]byte, 32)
	if _, err := fresh.Read(want); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if !bytes.Equal(first, want) {
		t.Error("an empty read perturbed the stream")
	}
}

func TestDRBGOutputIsDistinct(t *testing.T) {
	t.Parallel()

	d, err := NewDRBG(testSeed(), []byte("device-0001"))
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	out := make([]byte, 4096)
	if _, err := d.Read(out); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	seen := make(map[string]bool, len(out)/32)
	for i := 0; i+32 <= len(out); i += 32 {
		block := string(out[i : i+32])
		if seen[block] {
			t.Fatalf("block %v repeated in the stream", i/32)
		}
		seen[block] = true
	}
	if bytes.Equal(out, make([]byte, len(out))) {
		t.Error("the stream is all zeros")
	}
}

func TestDRBGDestroyStopsOutput(t *testing.T) {
	t.Parallel()

	d, err := NewDRBG(testSeed(), nil)
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}
	d.Destroy()
	if _, err := d.Read(make([]byte, 32)); !errors.Is(err, ErrDestroyed) {
		t.Errorf("Read after Destroy() error = %v, want ErrDestroyed", err)
	}
}

func TestDRBGConcurrentReadsAreDistinct(t *testing.T) {
	t.Parallel()

	d, err := NewDRBG(testSeed(), []byte("device-0001"))
	if err != nil {
		t.Fatalf("NewDRBG() = %v", err)
	}

	const readers = 16
	var wg sync.WaitGroup
	results := make([][]byte, readers)
	errs := make([]error, readers)
	for i := range readers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			buf := make([]byte, 64)
			_, errs[i] = d.Read(buf)
			results[i] = buf
		}(i)
	}
	wg.Wait()

	seen := make(map[string]int, readers)
	for i, buf := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent Read() = %v", errs[i])
		}
		if prev, dup := seen[string(buf)]; dup {
			t.Fatalf("readers %v and %v received identical bytes", prev, i)
		}
		seen[string(buf)] = i
	}
}
