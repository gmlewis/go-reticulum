// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// SSHSIG wire-format constants, matching commitsigs.py.
const (
	sshsigMagic    = "SSHSIG" // 6 bytes
	sshsigVersion  = uint32(1)
	namespaceGit   = "git"
	reservedEmpty  = ""
	hashAlgorithm  = "sha256"
	sshEd25519Name = "ssh-ed25519"
	armorBegin     = "-----BEGIN SSH SIGNATURE-----"
	armorEnd       = "-----END SSH SIGNATURE-----"
	armorLineLen   = 70
)

// sshString encodes data as an SSH wire-format string: a 4-byte
// big-endian length prefix followed by the data. It matches Python's
// struct.pack(">I", len(data)) + data.
func sshString(data []byte) []byte {
	out := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(out, uint32(len(data)))
	copy(out[4:], data)
	return out
}

// readSSHString reads a length-prefixed SSH string from data starting at
// offset. It returns the string payload, the new offset, and an error if
// the buffer is truncated. It matches Python's read_ssh_string.
func readSSHString(data []byte, offset int) ([]byte, int, error) {
	if offset+4 > len(data) {
		return nil, 0, errors.New("not enough data for string length")
	}
	length := binary.BigEndian.Uint32(data[offset : offset+4])
	end := offset + 4 + int(length)
	if end > len(data) {
		return nil, 0, errors.New("not enough data for string content")
	}
	return data[offset+4 : end], end, nil
}

// sshSig holds the parsed fields of an SSHSIG blob.
type sshSig struct {
	Version       uint32
	PublicKey     []byte
	Namespace     []byte
	Reserved      []byte
	HashAlgorithm []byte
	SignatureData []byte
}

// createSSHSig builds the SSHSIG wire-format blob:
//
//	SSHSIG (6) || version (uint32) || pubkey (ssh-string) ||
//	namespace (ssh-string) || reserved (ssh-string) ||
//	hash_algorithm (ssh-string) || signature (ssh-string)
//
// It matches Python's create_ssh_signature.
func createSSHSig(pubKeyWire, namespace, reserved, hashAlgo, sigData []byte) []byte {
	var b []byte
	b = append(b, []byte(sshsigMagic)...)
	var ver [4]byte
	binary.BigEndian.PutUint32(ver[:], sshsigVersion)
	b = append(b, ver[:]...)
	b = append(b, sshString(pubKeyWire)...)
	b = append(b, sshString(namespace)...)
	b = append(b, sshString(reserved)...)
	b = append(b, sshString(hashAlgo)...)
	b = append(b, sshString(sigData)...)
	return b
}

// parseSSHSig parses an SSHSIG wire-format blob. It matches Python's
// parse_ssh_signature and rejects blobs with bad magic, an unsupported
// version, or truncated fields.
func parseSSHSig(sigData []byte) (*sshSig, error) {
	if !bytes.HasPrefix(sigData, []byte(sshsigMagic)) {
		return nil, errors.New("invalid SSH signature: missing SSHSIG magic")
	}
	offset := len(sshsigMagic)
	if offset+4 > len(sigData) {
		return nil, errors.New("invalid SSH signature: truncated")
	}
	version := binary.BigEndian.Uint32(sigData[offset : offset+4])
	if version != sshsigVersion {
		return nil, fmt.Errorf("unsupported SSH signature version: %d", version)
	}
	offset += 4

	pubKey, off, err := readSSHString(sigData, offset)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH signature: pubkey: %w", err)
	}
	namespace, off2, err := readSSHString(sigData, off)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH signature: namespace: %w", err)
	}
	reserved, off3, err := readSSHString(sigData, off2)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH signature: reserved: %w", err)
	}
	hashAlgo, off4, err := readSSHString(sigData, off3)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH signature: hash_algorithm: %w", err)
	}
	sig, _, err := readSSHString(sigData, off4)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH signature: signature: %w", err)
	}

	return &sshSig{
		Version:       version,
		PublicKey:     pubKey,
		Namespace:     namespace,
		Reserved:      reserved,
		HashAlgorithm: hashAlgo,
		SignatureData: sig,
	}, nil
}

// armorSSHSig base64-encodes the SSHSIG blob and wraps it in the
// -----BEGIN/END SSH SIGNATURE----- armor, with 70-character lines. It
// matches Python's armor_ssh_signature.
func armorSSHSig(sigBlob []byte) string {
	b64 := base64.StdEncoding.EncodeToString(sigBlob)
	var lines []string
	for i := 0; i < len(b64); i += armorLineLen {
		end := i + armorLineLen
		if end > len(b64) {
			end = len(b64)
		}
		lines = append(lines, b64[i:end])
	}
	var sb strings.Builder
	sb.WriteString(armorBegin)
	sb.WriteByte('\n')
	sb.WriteString(strings.Join(lines, "\n"))
	sb.WriteByte('\n')
	sb.WriteString(armorEnd)
	sb.WriteByte('\n')
	return sb.String()
}

// unarmorSSHSig strips the -----BEGIN/END SSH SIGNATURE----- armor and
// base64-decodes the contained data. It matches Python's
// unarmor_ssh_signature.
func unarmorSSHSig(armored string) ([]byte, error) {
	var b64 strings.Builder
	inSig := false
	for _, line := range strings.Split(armored, "\n") {
		if strings.Contains(line, "BEGIN SSH SIGNATURE") {
			inSig = true
			continue
		}
		if strings.Contains(line, "END SSH SIGNATURE") {
			break
		}
		if inSig {
			b64.WriteString(strings.TrimSpace(line))
		}
	}
	if b64.Len() == 0 {
		return nil, errors.New("no signature data found in armored input")
	}
	return base64.StdEncoding.DecodeString(b64.String())
}
