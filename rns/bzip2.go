// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"io"
	"log"

	vendoredbzip2 "github.com/gmlewis/go-reticulum/compress/bzip2"
)

// ErrDecompressedTooLarge is returned by DecompressBzip2WithLimit when the
// decompressed output exceeds the supplied max length. It mirrors Python's
// bz2.BZ2Decompressor.decompress(data, max_length=N) "not decompressor.eof"
// condition used by Resource.assemble's decompression-bomb guard
// (RNS/Resource.py:690-696).
var ErrDecompressedTooLarge = errors.New("decompressed data exceeds maximum allowed size")

// CompressBzip2 takes a byte slice and compresses it using the bzip2 algorithm at the specified compression level.
func CompressBzip2(input []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	w, err := vendoredbzip2.NewWriter(&buf, &vendoredbzip2.WriterConfig{Level: level})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(input); err != nil {
		if cerr := w.Close(); cerr != nil {
			log.Printf("Warning: Could not close bzip2 writer during error recovery: %v", cerr)
		}
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecompressBzip2 extracts the original byte sequence from a bzip2-compressed payload.
func DecompressBzip2(input []byte) ([]byte, error) {
	r, err := vendoredbzip2.NewReader(bytes.NewReader(input), nil)
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(r)
	if err != nil {
		if cerr := r.Close(); cerr != nil { // should not happen
			log.Printf("Warning: Could not close bzip2 reader during error recovery: %v", cerr)
		}
		return nil, err
	}
	if err := r.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

// DecompressBzip2WithLimit decompresses a bzip2 payload but caps the decompressed
// output at maxLen bytes, returning ErrDecompressedTooLarge when the stream
// decompresses past the cap. It is the Go port of Python's
// bz2.BZ2Decompressor.decompress(data, max_length=N) guard used in
// Resource.assemble (RNS/Resource.py:690-696) to reject decompression bombs
// without unbounded memory allocation: the bounded read materializes at most
// maxLen+1 bytes before the overflow is detected.
func DecompressBzip2WithLimit(input []byte, maxLen int) ([]byte, error) {
	if maxLen < 0 {
		maxLen = 0
	}
	r, err := vendoredbzip2.NewReader(bytes.NewReader(input), nil)
	if err != nil {
		return nil, err
	}
	// Read at most maxLen+1 bytes; if the stream yields more than maxLen, the
	// decompressed payload exceeds the cap.
	out, err := io.ReadAll(io.LimitReader(r, int64(maxLen)+1))
	if cerr := r.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if len(out) > maxLen {
		return nil, ErrDecompressedTooLarge
	}
	return out, nil
}
