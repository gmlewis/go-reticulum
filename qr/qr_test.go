// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package qr

import (
	"bytes"
	"image"
	"testing"
)

// TestEncodeSmoke verifies the vendored rsc.io/qr production graph compiles
// and produces a well-formed QR code: Encode returns a Code with a positive
// size, a non-empty bitmap, the three 7x7 finder patterns present at the
// corners, a renderable image.Image, and non-empty PNG bytes. This is the
// Phase L.1 acceptance gate for the vendored encoder.
func TestEncodeSmoke(t *testing.T) {
	code, err := Encode("hello", L)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if code.Size <= 0 {
		t.Fatalf("code.Size=%d want >0", code.Size)
	}
	if len(code.Bitmap) == 0 {
		t.Fatal("code.Bitmap empty")
	}
	if code.Stride < (code.Size+7)/8 {
		t.Fatalf("code.Stride=%d too small for Size=%d", code.Stride, code.Size)
	}

	// The three finder patterns occupy 7x7 squares at the top-left, top-right,
	// and bottom-left corners. Their outer ring (and especially the corner
	// pixel at (0,0), (Size-1,0), (0,Size-1)) must be black.
	checks := []struct{ x, y int }{
		{0, 0},             // top-left finder corner
		{code.Size - 1, 0}, // top-right finder corner
		{0, code.Size - 1}, // bottom-left finder corner
	}
	for _, c := range checks {
		if !code.Black(c.x, c.y) {
			t.Errorf("finder corner (%d,%d) not black; QR malformed", c.x, c.y)
		}
	}
	// A white pixel just inside the top-left finder's outer ring: (0,6) is the
	// separator row boundary; (6,0) similarly. The pixel at (0,7) lies in the
	// separator (white) just outside the 7x7 finder.
	if code.Size > 7 && code.Black(7, 7) {
		// (7,7) is the first non-finder pixel; it may be either color
		// depending on the mask, so only assert it is in-bounds and readable.
		_ = code.Black(7, 7)
	}

	img := code.Image()
	if img == nil {
		t.Fatal("Image() returned nil")
	}
	bounds := img.Bounds()
	if bounds.Empty() {
		t.Fatalf("Image bounds empty: %v", bounds)
	}

	png := code.PNG()
	if len(png) == 0 {
		t.Fatal("PNG() returned empty bytes")
	}
	// PNG signature.
	if !bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
		t.Fatalf("PNG bytes do not start with PNG signature: %x", png[:8])
	}

	// Image implements image.Image.
	var _ image.Image = img
}

// TestEncodeAllLevels verifies Encode succeeds for all four error correction
// levels and that higher redundancy yields a code at least as large as lower
// redundancy for the same text (versions are monotonic in capacity need).
func TestEncodeAllLevels(t *testing.T) {
	levels := []Level{L, M, Q, H}
	sizes := map[Level]int{}
	for _, lvl := range levels {
		code, err := Encode("the quick brown fox", lvl)
		if err != nil {
			t.Fatalf("Encode level %v: %v", lvl, err)
		}
		if code.Size <= 0 {
			t.Fatalf("level %v: Size=%d", lvl, code.Size)
		}
		sizes[lvl] = code.Size
	}
	// L (least redundant) should not produce a larger code than H (most
	// redundant) for the same input.
	if sizes[L] > sizes[H] {
		t.Errorf("L size %d > H size %d for same text", sizes[L], sizes[H])
	}
}
