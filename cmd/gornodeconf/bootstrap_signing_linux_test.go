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
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBootstrapSignerSignsChecksums(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "gornodeconf-bootstrap-signing-*")
	if err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	firmwareDir := filepath.Join(dir, "firmware")
	if err := os.MkdirAll(firmwareDir, 0o755); err != nil {
		t.Fatalf("mkdir firmware dir: %v", err)
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privateBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firmwareDir, "signing.key"), privateBytes, 0o600); err != nil {
		t.Fatalf("write signing.key: %v", err)
	}

	signer, err := loadBootstrapSigner(dir)
	if err != nil {
		t.Fatalf("loadBootstrapSigner returned error: %v", err)
	}
	signature, err := signer.Sign([]byte("bootstrap checksum"))
	if err != nil {
		t.Fatalf("sign returned error: %v", err)
	}
	if len(signature) != 128 {
		t.Fatalf("signature length mismatch: got %v want 128", len(signature))
	}
	message := []byte("bootstrap checksum")
	digest := sha256.Sum256(message)
	if err := rsa.VerifyPSS(&privateKey.PublicKey, crypto.SHA256, digest[:], signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256}); err != nil {
		t.Fatalf("signature verification failed: %v", err)
	}
}
