// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func handleTrustKey(hexBytes string) error {
	publicBytes, err := hex.DecodeString(hexBytes)
	if err != nil {
		fmt.Println("Invalid key data supplied")
		return nil
	}

	if _, err := x509.ParsePKIXPublicKey(publicBytes); err != nil {
		fmt.Println("Could not create public key from supplied data. Check that the key format is valid.")
		fmt.Println(err)
		return nil
	}

	hash := sha256.Sum256(publicBytes)
	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}

	trustedDir := filepath.Join(configDir, "trusted_keys")
	if err := os.MkdirAll(trustedDir, 0o755); err != nil {
		return fmt.Errorf("Could not create trusted key directory: %w", err)
	}

	keyPath := filepath.Join(trustedDir, fmt.Sprintf("%x.pubkey", hash[:]))
	if err := os.WriteFile(keyPath, publicBytes, 0o644); err != nil {
		return fmt.Errorf("Could not write trusted key file: %w", err)
	}

	fmt.Printf("Trusting key: %x\n", hash[:])
	return nil
}

func rnodeconfConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "rnodeconf"), nil
}
