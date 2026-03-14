// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"io"

	vendoredbzip2 "github.com/gmlewis/go-reticulum/compress/bzip2"
)

func CompressBzip2(input []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	w, err := vendoredbzip2.NewWriter(&buf, &vendoredbzip2.WriterConfig{Level: level})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(input); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecompressBzip2(input []byte) ([]byte, error) {
	r, err := vendoredbzip2.NewReader(bytes.NewReader(input), nil)
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(r)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	if err := r.Close(); err != nil {
		return nil, err
	}
	return out, nil
}
