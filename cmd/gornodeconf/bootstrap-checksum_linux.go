// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"crypto/md5"
	"encoding/binary"
)

// checksumInfoHash returns the Python bootstrap checksum over the provisioned
// identity fields.
func checksumInfoHash(product, model, hwRev byte, serialno, timestamp uint32) []byte {
	infoChunk := []byte{product, model, hwRev}
	serialBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(serialBytes, serialno)
	timeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(timeBytes, timestamp)
	infoChunk = append(infoChunk, serialBytes...)
	infoChunk = append(infoChunk, timeBytes...)
	digest := md5.Sum(infoChunk)
	return append([]byte(nil), digest[:]...)
}
