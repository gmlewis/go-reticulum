// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const checkIdentityParityPy = `import RNS
import sys
import os

def check_identity(path):
    try:
        if not os.path.exists(path):
            print(f"File not found: {path}")
            sys.exit(1)
        
        identity = RNS.Identity.from_file(path)
        if identity is None:
            print("Failed to load identity")
            sys.exit(1)
        
        print(f"Public Key: {identity.get_public_key().hex()}")
        print(f"Hash: {identity.hash.hex()}")
        sys.exit(0)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: check_identity_parity.py <path_to_private_key>")
        sys.exit(1)
    
    check_identity(sys.argv[1])
`

const generateIdentityParityPy = `import RNS
import sys

def generate_identity(path):
    try:
        identity = RNS.Identity()
        identity.to_file(path)
        print(f"Public Key: {identity.get_public_key().hex()}")
        print(f"Hash: {identity.hash.hex()}")
        sys.exit(0)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: generate_identity_parity.py <path_to_save_private_key>")
        sys.exit(1)
    
    generate_identity(sys.argv[1])
`

func TestIdentityParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-parity-*")
	mustTest(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	scriptPath := filepath.Join(tmpDir, "check_identity_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkIdentityParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	id := mustTestNewIdentity(t, true)

	idPath := filepath.Join(tmpDir, "id")
	if err := id.ToFile(idPath); err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, idPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	// Parse Python output
	lines := strings.Split(string(out), "\n")
	var pyPubKey, pyHash string
	for _, line := range lines {
		if strings.HasPrefix(line, "Public Key: ") {
			pyPubKey = strings.TrimPrefix(line, "Public Key: ")
		} else if strings.HasPrefix(line, "Hash: ") {
			pyHash = strings.TrimPrefix(line, "Hash: ")
		}
	}

	if pyPubKey != fmt.Sprintf("%x", id.GetPublicKey()) {
		t.Errorf("Public key mismatch!\nGo: %x\nPy: %v", id.GetPublicKey(), pyPubKey)
	}

	if pyHash != fmt.Sprintf("%x", id.Hash) {
		t.Errorf("Hash mismatch!\nGo: %x\nPy: %v", id.Hash, pyHash)
	}
}

func TestIdentityPythonToGoParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-parity-*")
	mustTest(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	scriptPath := filepath.Join(tmpDir, "generate_identity_parity.py")
	if err := os.WriteFile(scriptPath, []byte(generateIdentityParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	idPath := filepath.Join(tmpDir, "py_id")

	// Generate identity with Python
	cmd := exec.Command("python3", scriptPath, idPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python generation failed: %v\nOutput: %v", err, string(out))
	}

	// Parse Python output
	lines := strings.Split(string(out), "\n")
	var pyPubKey, pyHash string
	for _, line := range lines {
		if strings.HasPrefix(line, "Public Key: ") {
			pyPubKey = strings.TrimPrefix(line, "Public Key: ")
		} else if strings.HasPrefix(line, "Hash: ") {
			pyHash = strings.TrimPrefix(line, "Hash: ")
		}
	}

	// Load with Go
	id, err := FromFile(idPath)
	if err != nil {
		t.Fatalf("Failed to load identity from file: %v", err)
	}

	if pyPubKey != fmt.Sprintf("%x", id.GetPublicKey()) {
		t.Errorf("Public key mismatch!\nGo: %x\nPy: %v", id.GetPublicKey(), pyPubKey)
	}

	if pyHash != fmt.Sprintf("%x", id.Hash) {
		t.Errorf("Hash mismatch!\nGo: %x\nPy: %v", id.Hash, pyHash)
	}
}
