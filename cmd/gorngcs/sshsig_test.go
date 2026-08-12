// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSSHStringRoundTrip(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		{},
		[]byte("ssh-ed25519"),
		bytes.Repeat([]byte{0xAB}, 300), // length > 255
	}
	for _, in := range cases {
		enc := sshString(in)
		if len(enc) != 4+len(in) {
			t.Errorf("sshString(%v) len=%d want %d", in, len(enc), 4+len(in))
		}
		out, off, err := readSSHString(enc, 0)
		if err != nil {
			t.Fatalf("readSSHString: %v", err)
		}
		if !bytes.Equal(out, in) {
			t.Errorf("readSSHString round-trip mismatch: got %x want %x", out, in)
		}
		if off != len(enc) {
			t.Errorf("readSSHString offset=%d want %d", off, len(enc))
		}
	}
}

func TestReadSSHStringErrors(t *testing.T) {
	t.Parallel()
	if _, _, err := readSSHString([]byte{0, 0, 0}, 0); err == nil {
		t.Fatal("readSSHString truncated length should error")
	}
	// Length prefix claims more data than present.
	if _, _, err := readSSHString([]byte{0, 0, 0, 5, 1, 2}, 0); err == nil {
		t.Fatal("readSSHString overlong content should error")
	}
	// Non-zero offset past end.
	if _, _, err := readSSHString([]byte{0, 0, 0, 1, 1}, 10); err == nil {
		t.Fatal("readSSHString offset past end should error")
	}
}

func TestCreateParseSSHSigRoundTrip(t *testing.T) {
	t.Parallel()
	pubKeyWire := append(sshString([]byte("ssh-ed25519")), sshString(bytes.Repeat([]byte{0x42}, 32))...)
	sigData := bytes.Repeat([]byte{0x55}, 64)
	blob := createSSHSig(pubKeyWire, []byte("git"), []byte(""), []byte("sha256"), sigData)

	if !bytes.HasPrefix(blob, []byte("SSHSIG")) {
		t.Fatalf("blob missing magic: %x", blob[:8])
	}

	parsed, err := parseSSHSig(blob)
	if err != nil {
		t.Fatalf("parseSSHSig: %v", err)
	}
	if parsed.Version != 1 {
		t.Errorf("version=%d want 1", parsed.Version)
	}
	if !bytes.Equal(parsed.PublicKey, pubKeyWire) {
		t.Errorf("public key mismatch")
	}
	if !bytes.Equal(parsed.Namespace, []byte("git")) {
		t.Errorf("namespace=%q want %q", parsed.Namespace, "git")
	}
	if len(parsed.Reserved) != 0 {
		t.Errorf("reserved=%x want empty", parsed.Reserved)
	}
	if !bytes.Equal(parsed.HashAlgorithm, []byte("sha256")) {
		t.Errorf("hash algo=%q want sha256", parsed.HashAlgorithm)
	}
	if !bytes.Equal(parsed.SignatureData, sigData) {
		t.Errorf("signature data mismatch")
	}
}

func TestParseSSHSigBadMagic(t *testing.T) {
	t.Parallel()
	blob := append([]byte("XXXXXX"), make([]byte, 20)...)
	if _, err := parseSSHSig(blob); err == nil {
		t.Fatal("parseSSHSig with bad magic should error")
	}
}

func TestParseSSHSigWrongVersion(t *testing.T) {
	t.Parallel()
	pubKeyWire := append(sshString([]byte("ssh-ed25519")), sshString(bytes.Repeat([]byte{0x42}, 32))...)
	// Manually build a v2 blob.
	var b []byte
	b = append(b, []byte("SSHSIG")...)
	b = append(b, 0, 0, 0, 2) // version 2
	b = append(b, sshString(pubKeyWire)...)
	b = append(b, sshString([]byte("git"))...)
	b = append(b, sshString([]byte(""))...)
	b = append(b, sshString([]byte("sha256"))...)
	b = append(b, sshString(bytes.Repeat([]byte{0x55}, 64))...)
	if _, err := parseSSHSig(b); err == nil {
		t.Fatal("parseSSHSig with version 2 should error")
	}
}

func TestParseSSHSigTruncated(t *testing.T) {
	t.Parallel()
	if _, err := parseSSHSig([]byte("SSHSIG")); err == nil {
		t.Fatal("parseSSHSig magic-only should error")
	}
	if _, err := parseSSHSig(append([]byte("SSHSIG"), 0, 0, 0, 1, 0)); err == nil {
		t.Fatal("parseSSHSig truncated body should error")
	}
}

func TestArmorUnarmorRoundTrip(t *testing.T) {
	t.Parallel()
	blob := []byte("SSHSIG\x00\x00\x00\x01 some signature payload here")
	armored := armorSSHSig(blob)
	if !strings.HasPrefix(armored, "-----BEGIN SSH SIGNATURE-----\n") {
		t.Errorf("armor missing begin header: %q", armored)
	}
	if !strings.HasSuffix(armored, "-----END SSH SIGNATURE-----\n") {
		t.Errorf("armor missing end header: %q", armored)
	}
	out, err := unarmorSSHSig(armored)
	if err != nil {
		t.Fatalf("unarmorSSHSig: %v", err)
	}
	if !bytes.Equal(out, blob) {
		t.Errorf("unarmor mismatch: got %x want %x", out, blob)
	}
}

func TestUnarmorSSHSigNoData(t *testing.T) {
	t.Parallel()
	armored := "-----BEGIN SSH SIGNATURE-----\n-----END SSH SIGNATURE-----\n"
	if _, err := unarmorSSHSig(armored); err == nil {
		t.Fatal("unarmorSSHSig with no data should error")
	}
}

func TestArmorBase64Content(t *testing.T) {
	t.Parallel()
	blob := []byte("payload")
	armored := armorSSHSig(blob)
	// Extract base64 between headers and compare.
	lines := strings.Split(strings.TrimRight(armored, "\n"), "\n")
	var b64 string
	for _, l := range lines {
		if l == "-----BEGIN SSH SIGNATURE-----" || l == "-----END SSH SIGNATURE-----" || l == "" {
			continue
		}
		b64 += l
	}
	dec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !bytes.Equal(dec, blob) {
		t.Errorf("armored base64 mismatch: got %x want %x", dec, blob)
	}
}
