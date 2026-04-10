// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const trustKeyFixtureHex = "30819f300d06092a864886f70d010101050003818d0030818902818100c511e59807af9fbcfce46f8617949c46ded93fffcf5c20cd826eea42dabe69f9b9e5a65232a46941a5e57c9daf387b9db40fe92f45ee863b0d7073169eded685ff9b78907090539b9eb47d1012f0a813a0bb79889b42afb56ae073cb8814e2c34d403b097951d3a862c20a6798e8e89ad583e55ae3e7c308dd0274d26ae0db6b0203010001"

const trustKeyFixtureHash = "cbc2cf229adc279096ade6bba0c26176fa70adcf1d59d926253e6f0bea73aa46"

func TestTrustKeyWritesTrustedKeyFile(t *testing.T) {

	home := tempTrustKeyHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--trust-key", trustKeyFixtureHex)
	if err != nil {
		t.Fatalf("gornodeconf --trust-key failed: %v\n%v", err, out)
	}

	if !strings.Contains(out, "Trusting key: "+trustKeyFixtureHash) {
		t.Fatalf("unexpected output: %v", out)
	}

	wantPath := filepath.Join(home, ".config", "rnodeconf", "trusted_keys", trustKeyFixtureHash+".pubkey")
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read trusted key file: %v", err)
	}
	if !bytes.Equal(got, mustDecodeHex(t, trustKeyFixtureHex)) {
		t.Fatalf("trusted key file mismatch:\n got: %x\nwant: %s", got, trustKeyFixtureHex)
	}
}

func TestTrustKeyRejectsInvalidHex(t *testing.T) {

	home := tempTrustKeyHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--trust-key", "not-hex")
	if err != nil {
		t.Fatalf("gornodeconf --trust-key invalid hex failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Invalid key data supplied") {
		t.Fatalf("unexpected output: %v", out)
	}
}

func TestTrustKeyRejectsInvalidDer(t *testing.T) {

	home := tempTrustKeyHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--trust-key", "00")
	if err != nil {
		t.Fatalf("gornodeconf --trust-key invalid der failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Could not create public key from supplied data. Check that the key format is valid.") {
		t.Fatalf("unexpected output: %v", out)
	}
}

func TestTrustKeyReportsWriteFailure(t *testing.T) {

	home := tempTrustKeyHome(t)
	trustedDir := filepath.Join(home, ".config", "rnodeconf", "trusted_keys")
	if err := os.MkdirAll(trustedDir, 0o755); err != nil {
		t.Fatalf("mkdir trusted_keys dir: %v", err)
	}
	if err := os.Chmod(trustedDir, 0o500); err != nil {
		t.Fatalf("chmod trusted_keys dir: %v", err)
	}

	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--trust-key", trustKeyFixtureHex)
	if err == nil {
		t.Fatal("expected gornodeconf --trust-key to fail")
	}
	wantPath := filepath.Join(trustedDir, trustKeyFixtureHash+".pubkey")
	if !strings.Contains(out, "Could not write trusted key file: open ") {
		t.Fatalf("missing wrapped write error: %v", out)
	}
	if !strings.Contains(out, wantPath) {
		t.Fatalf("missing trusted key path: %v", out)
	}
}
