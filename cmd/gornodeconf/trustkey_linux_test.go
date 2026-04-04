// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const trustKeyFixtureHex = "30819f300d06092a864886f70d010101050003818d0030818902818100c511e59807af9fbcfce46f8617949c46ded93fffcf5c20cd826eea42dabe69f9b9e5a65232a46941a5e57c9daf387b9db40fe92f45ee863b0d7073169eded685ff9b78907090539b9eb47d1012f0a813a0bb79889b42afb56ae073cb8814e2c34d403b097951d3a862c20a6798e8e89ad583e55ae3e7c308dd0274d26ae0db6b0203010001"

const trustKeyFixtureHash = "cbc2cf229adc279096ade6bba0c26176fa70adcf1d59d926253e6f0bea73aa46"

func TestTrustKeyWritesTrustedKeyFile(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	home := tempTrustKeyHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--trust-key", "00")
	if err != nil {
		t.Fatalf("gornodeconf --trust-key invalid der failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Could not create public key from supplied data. Check that the key format is valid.") {
		t.Fatalf("unexpected output: %v", out)
	}
}

func runGornodeconfWithEnv(extraEnv map[string]string, args ...string) (string, error) {
	taskArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", taskArgs...)
	cmd.Dir = "."
	cmd.Env = os.Environ()
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tempTrustKeyHome(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "gornodeconf-trustkey-*")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func mustDecodeHex(t *testing.T, hexStr string) []byte {
	t.Helper()

	data, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return data
}
