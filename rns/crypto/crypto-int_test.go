//go:build integration
// +build integration

package crypto

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func getPythonPath() string {
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	log.Fatalf("missing required environment variable: ORIGINAL_RETICULUM_REPO_DIR")
	return "" // unreachable
}

const checkCryptoParityPy = `import sys
import os

import RNS
from RNS.Cryptography import Ed25519PublicKey, X25519PrivateKey, X25519PublicKey, hkdf, Token

def check_ed25519(message_hex, signature_hex, pub_hex):
    try:
        message = bytes.fromhex(message_hex)
        signature = bytes.fromhex(signature_hex)
        pub_bytes = bytes.fromhex(pub_hex)

        pub = Ed25519PublicKey.from_public_bytes(pub_bytes)
        pub.verify(signature, message)
        print("Ed25519 Valid: Yes")
        sys.exit(0)
    except Exception as e:
        print(f"Ed25519 Valid: No, {e}")
        sys.exit(1)

def check_x25519(prv_hex, peer_pub_hex, expected_shared_hex):
    try:
        prv_bytes = bytes.fromhex(prv_hex)
        peer_pub_bytes = bytes.fromhex(peer_pub_hex)
        expected_shared = bytes.fromhex(expected_shared_hex)

        prv = X25519PrivateKey.from_private_bytes(prv_bytes)
        peer_pub = X25519PublicKey.from_public_bytes(peer_pub_bytes)
        shared = prv.exchange(peer_pub)

        if shared == expected_shared:
            print("X25519 Valid: Yes")
            sys.exit(0)
        else:
            print(f"X25519 Valid: No, got {shared.hex()}")
            sys.exit(1)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

def check_hkdf(derive_from_hex, salt_hex, length, expected_hex):
    try:
        derive_from = bytes.fromhex(derive_from_hex)
        salt = bytes.fromhex(salt_hex) if salt_hex else None
        expected = bytes.fromhex(expected_hex)

        derived = hkdf(length, derive_from, salt)

        if derived == expected:
            print("HKDF Valid: Yes")
            sys.exit(0)
        else:
            print(f"HKDF Valid: No, got {derived.hex()}")
            sys.exit(1)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

def check_aes(token_key_hex, ciphertext_hex, expected_plaintext_hex):
    try:
        token_key = bytes.fromhex(token_key_hex)
        ciphertext = bytes.fromhex(ciphertext_hex)
        expected_plaintext = bytes.fromhex(expected_plaintext_hex)

        token = Token(token_key)
        plaintext = token.decrypt(ciphertext)

        if plaintext == expected_plaintext:
            print("AES Valid: Yes")
            sys.exit(0)
        else:
            print(f"AES Valid: No, got {plaintext.hex()}")
            sys.exit(1)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    mode = sys.argv[1]
    if mode == "ed25519":
        check_ed25519(sys.argv[2], sys.argv[3], sys.argv[4])
    elif mode == "x25519":
        check_x25519(sys.argv[2], sys.argv[3], sys.argv[4])
    elif mode == "hkdf":
        check_hkdf(sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5])
    elif mode == "aes":
        check_aes(sys.argv[2], sys.argv[3], sys.argv[4])
`

func TestEd25519Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	prv, err := GenerateEd25519PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := prv.PublicKey()
	message := []byte("hello cryptography parity")
	signature := prv.Sign(message)

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "ed25519", fmt.Sprintf("%x", message), fmt.Sprintf("%x", signature), fmt.Sprintf("%x", pub.PublicBytes()))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "Ed25519 Valid: Yes") {
		t.Errorf("Python reported Ed25519 signature as INVALID\nOutput: %v", string(out))
	}
}

func TestX25519Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	prv, err := GenerateX25519PrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Create a second key pair to act as peer
	peerPrv, _ := GenerateX25519PrivateKey()
	peerPub := peerPrv.PublicKey()

	shared, err := prv.Exchange(peerPub)
	if err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "x25519", fmt.Sprintf("%x", prv.PrivateBytes()), fmt.Sprintf("%x", peerPub.PublicBytes()), fmt.Sprintf("%x", shared))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "X25519 Valid: Yes") {
		t.Errorf("Python reported X25519 shared secret mismatch\nOutput: %v", string(out))
	}
}

func TestHKDFParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	deriveFrom := []byte("input material")
	salt := []byte("salty")
	length := 32

	derived, err := HKDF(length, deriveFrom, salt, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "hkdf", fmt.Sprintf("%x", deriveFrom), fmt.Sprintf("%x", salt), fmt.Sprintf("%v", length), fmt.Sprintf("%x", derived))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "HKDF Valid: Yes") {
		t.Errorf("Python reported HKDF mismatch\nOutput: %v", string(out))
	}
}

func TestAESParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	key := []byte("01234567890123456789012345678901") // 32 bytes
	token, err := NewToken(key)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("secret message for aes parity")
	ciphertext, err := token.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "aes", fmt.Sprintf("%x", key), fmt.Sprintf("%x", ciphertext), fmt.Sprintf("%x", plaintext))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "AES Valid: Yes") {
		t.Errorf("Python reported AES decryption failure or mismatch\nOutput: %v", string(out))
	}
}
