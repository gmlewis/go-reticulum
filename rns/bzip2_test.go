// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"testing"

	vendoredbzip2 "github.com/gmlewis/go-reticulum/compress/bzip2"
)

func TestBzip2RoundTripByLevel(t *testing.T) {
	t.Parallel()

	input := bytes.Repeat([]byte("reticulum-bzip2-parity-"), 64)

	for _, level := range []int{1, 3, 6, 9} {
		t.Run("level", func(t *testing.T) {
			t.Parallel()

			compressed, err := CompressBzip2(input, level)
			if err != nil {
				t.Fatalf("CompressBzip2(level=%v) error: %v", level, err)
			}

			decompressed, err := DecompressBzip2(compressed)
			if err != nil {
				t.Fatalf("DecompressBzip2(level=%v) error: %v", level, err)
			}

			if !bytes.Equal(decompressed, input) {
				t.Fatalf("round-trip mismatch at level=%v", level)
			}
		})
	}
}

func TestBzip2CompressMatchesVendoredLibrary(t *testing.T) {
	t.Parallel()

	input := bytes.Repeat([]byte("deterministic-bzip2-output-"), 32)

	got, err := CompressBzip2(input, 9)
	if err != nil {
		t.Fatalf("CompressBzip2 error: %v", err)
	}

	var buf bytes.Buffer
	w, err := vendoredbzip2.NewWriter(&buf, &vendoredbzip2.WriterConfig{Level: 9})
	if err != nil {
		t.Fatalf("vendoredbzip2.NewWriter error: %v", err)
	}
	if _, err := w.Write(input); err != nil {
		_ = w.Close()
		t.Fatalf("vendoredbzip2.Writer.Write error: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("vendoredbzip2.Writer.Close error: %v", err)
	}
	want := buf.Bytes()

	if !bytes.Equal(got, want) {
		t.Fatal("compressed payload mismatch with vendored implementation")
	}
}

func TestBzip2DecompressInvalidPayload(t *testing.T) {
	t.Parallel()

	if _, err := DecompressBzip2([]byte("not-a-valid-bzip2-payload")); err == nil {
		t.Fatal("DecompressBzip2 expected error for invalid payload")
	}
}

// TestDecompressBzip2WithLimitRejectsBomb verifies the decompression-bomb guard
// (Python Resource.assemble, Resource.py:690-696): a compressed payload that
// decompresses past maxLen is rejected with ErrDecompressedTooLarge without
// allocating the full decompressed size (no OOM). A 1 MiB repetitive payload
// compresses small but decompresses well past a 64 KiB cap.
func TestDecompressBzip2WithLimitRejectsBomb(t *testing.T) {
	t.Parallel()

	bomb := bytes.Repeat([]byte("A"), 1024*1024)
	compressed, err := CompressBzip2(bomb, 9)
	if err != nil {
		t.Fatalf("CompressBzip2 error: %v", err)
	}
	// Sanity: the compressed form is far smaller than the decompressed bomb.
	if len(compressed) >= len(bomb) {
		t.Fatalf("compressed %v >= decompressed %v; payload is not actually compressible", len(compressed), len(bomb))
	}

	const maxLen = 64 * 1024
	out, err := DecompressBzip2WithLimit(compressed, maxLen)
	if !errors.Is(err, ErrDecompressedTooLarge) {
		t.Fatalf("DecompressBzip2WithLimit err = %v, want ErrDecompressedTooLarge", err)
	}
	if out != nil {
		t.Fatalf("DecompressBzip2WithLimit returned non-nil output on bomb rejection: len=%v", len(out))
	}
}

// TestDecompressBzip2WithLimitAllowsUnderLimit verifies a payload that
// decompresses within maxLen round-trips exactly through the limited helper.
func TestDecompressBzip2WithLimitAllowsUnderLimit(t *testing.T) {
	t.Parallel()

	want := bytes.Repeat([]byte("reticulum-limit-parity-"), 64) // ~1.3 KiB
	compressed, err := CompressBzip2(want, 9)
	if err != nil {
		t.Fatalf("CompressBzip2 error: %v", err)
	}

	out, err := DecompressBzip2WithLimit(compressed, len(want))
	if err != nil {
		t.Fatalf("DecompressBzip2WithLimit error: %v", err)
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("under-limit round-trip mismatch: got %v bytes, want %v", len(out), len(want))
	}
}
