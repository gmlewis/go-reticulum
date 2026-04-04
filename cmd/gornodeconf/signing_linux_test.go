// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestGenerateKeysCreatesFirmwareKeyFiles(t *testing.T) {
	t.Parallel()

	home := tempTrustKeyHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "-k")
	if err != nil {
		t.Fatalf("gornodeconf --key failed: %v\n%v", err, out)
	}

	if !strings.Contains(out, "Generating a new device signing key...") {
		t.Fatalf("missing device-key output: %v", out)
	}
	if !strings.Contains(out, "Generating a new EEPROM signing key...") {
		t.Fatalf("missing signing-key output: %v", out)
	}

	firmwareDir := filepath.Join(home, ".config", "rnodeconf", "firmware")
	if _, err := os.Stat(filepath.Join(firmwareDir, "device.key")); err != nil {
		t.Fatalf("device.key missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(firmwareDir, "signing.key")); err != nil {
		t.Fatalf("signing.key missing: %v", err)
	}

	deviceSigner, err := rns.FromFile(filepath.Join(firmwareDir, "device.key"))
	if err != nil {
		t.Fatalf("load device.key: %v", err)
	}
	if got := len(deviceSigner.GetPrivateKey()); got != 64 {
		t.Fatalf("device.key private length mismatch: got %v want 64", got)
	}

	signingBytes, err := os.ReadFile(filepath.Join(firmwareDir, "signing.key"))
	if err != nil {
		t.Fatalf("read signing.key: %v", err)
	}
	privateKey, err := x509.ParsePKCS8PrivateKey(signingBytes)
	if err != nil {
		t.Fatalf("parse signing.key: %v", err)
	}
	if _, ok := privateKey.(*rsa.PrivateKey); !ok {
		t.Fatalf("signing.key is not RSA: %T", privateKey)
	}
}

func TestPublicDisplaysSigningKeyMaterial(t *testing.T) {
	t.Parallel()

	home := tempTrustKeyHome(t)
	firmwareDir := filepath.Join(home, ".config", "rnodeconf", "firmware")
	if err := os.MkdirAll(firmwareDir, 0o755); err != nil {
		t.Fatalf("mkdir firmware dir: %v", err)
	}

	deviceSigner, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("create device identity: %v", err)
	}
	if err := deviceSigner.ToFile(filepath.Join(firmwareDir, "device.key")); err != nil {
		t.Fatalf("write device.key: %v", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	privateBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal signing private key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firmwareDir, "signing.key"), privateBytes, 0o600); err != nil {
		t.Fatalf("write signing.key: %v", err)
	}
	publicBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal signing public key: %v", err)
	}

	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "-P")
	if err != nil {
		t.Fatalf("gornodeconf --public failed: %v\n%v", err, out)
	}

	if !strings.Contains(out, "EEPROM Signing Public key:") {
		t.Fatalf("missing EEPROM public-key label: %v", out)
	}
	if !strings.Contains(out, hex.EncodeToString(publicBytes)) {
		t.Fatalf("missing EEPROM public-key bytes: %v", out)
	}
	if !strings.Contains(out, "Device Signing Public key:") {
		t.Fatalf("missing device public-key label: %v", out)
	}
	if !strings.Contains(out, colonHex(deviceSigner.GetPublicKey()[32:])) {
		t.Fatalf("missing device public-key bytes: %v", out)
	}
}

func TestGenerateKeysDoesNotOverwriteExistingKeys(t *testing.T) {
	t.Parallel()

	home := tempTrustKeyHome(t)
	firmwareDir := filepath.Join(home, ".config", "rnodeconf", "firmware")
	if err := os.MkdirAll(firmwareDir, 0o755); err != nil {
		t.Fatalf("mkdir firmware dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firmwareDir, "device.key"), bytes.Repeat([]byte{0x01}, 64), 0o600); err != nil {
		t.Fatalf("write device.key fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firmwareDir, "signing.key"), []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write signing.key fixture: %v", err)
	}

	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "-k")
	if err != nil {
		t.Fatalf("gornodeconf --key with fixtures failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "EEPROM Signing key already exists, not overwriting!") {
		t.Fatalf("missing overwrite warning: %v", out)
	}
}
