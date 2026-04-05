// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

type bootstrapChecksumSigner interface {
	Sign([]byte) ([]byte, error)
}

type rsaBootstrapSigner struct {
	privateKey *rsa.PrivateKey
}

func (s rsaBootstrapSigner) Sign(message []byte) ([]byte, error) {
	checksum := sha256.Sum256(message)
	return rsa.SignPSS(rand.Reader, s.privateKey, crypto.SHA256, checksum[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256})
}

func loadBootstrapSigner(configDir string) (bootstrapChecksumSigner, error) {
	path := filepath.Join(configDir, "firmware", "signing.key")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Could not load EEPROM signing key (did you run \"gornodeconf --key\"?): %w", err)
	}
	privateKey, err := x509.ParsePKCS8PrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("Could not deserialize signing key: %w", err)
	}
	rsaKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("Could not deserialize signing key: unexpected private key type")
	}
	return rsaBootstrapSigner{privateKey: rsaKey}, nil
}
