// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

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
import hashlib
import hmac

import RNS
from RNS.Cryptography import Ed25519PublicKey, X25519PrivateKey, X25519PublicKey, hkdf, Token, PKCS7, AES

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

def check_token(token_key_hex, ciphertext_hex, expected_plaintext_hex):
    try:
        token_key = bytes.fromhex(token_key_hex)
        ciphertext = bytes.fromhex(ciphertext_hex)
        expected_plaintext = bytes.fromhex(expected_plaintext_hex)

        token = Token(token_key)
        plaintext = token.decrypt(ciphertext)

        if plaintext == expected_plaintext:
            print("Token Valid: Yes")
            sys.exit(0)
        else:
            print(f"Token Valid: No, got {plaintext.hex()}")
            sys.exit(1)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

def check_sha256(data_hex, expected_hex):
    data = bytes.fromhex(data_hex)
    digest = hashlib.sha256(data).digest()
    if digest.hex() == expected_hex:
        print("SHA256 Valid: Yes")
    else:
        print(f"SHA256 Valid: No, got {digest.hex()}")

def check_sha512(data_hex, expected_hex):
    data = bytes.fromhex(data_hex)
    digest = hashlib.sha512(data).digest()
    if digest.hex() == expected_hex:
        print("SHA512 Valid: Yes")
    else:
        print(f"SHA512 Valid: No, got {digest.hex()}")

def check_pkcs7_pad(data_hex, block_size, expected_hex):
    data = bytes.fromhex(data_hex)
    padded = PKCS7.pad(data, block_size)
    if padded.hex() == expected_hex:
        print("PKCS7 Pad Valid: Yes")
    else:
        print(f"PKCS7 Pad Valid: No, got {padded.hex()}")

def check_pkcs7_unpad(data_hex, expected_hex):
    data = bytes.fromhex(data_hex)
    unpadded = PKCS7.unpad(data)
    if unpadded.hex() == expected_hex:
        print("PKCS7 Unpad Valid: Yes")
    else:
        print(f"PKCS7 Unpad Valid: No, got {unpadded.hex()}")

def check_aes_encrypt(mode, key_hex, iv_hex, plaintext_hex, expected_hex):
    from RNS.Cryptography.AES import AES_128_CBC, AES_256_CBC
    key = bytes.fromhex(key_hex)
    iv = bytes.fromhex(iv_hex)
    plaintext = bytes.fromhex(plaintext_hex)
    if mode == "aes128":
        ciphertext = AES_128_CBC.encrypt(plaintext, key, iv)
    else:
        ciphertext = AES_256_CBC.encrypt(plaintext, key, iv)
    
    if ciphertext.hex() == expected_hex:
        print(f"{mode.upper()} Encrypt Valid: Yes")
    else:
        print(f"{mode.upper()} Encrypt Valid: No, got {ciphertext.hex()}")

def check_aes_decrypt(mode, key_hex, iv_hex, ciphertext_hex, expected_hex):
    from RNS.Cryptography.AES import AES_128_CBC, AES_256_CBC
    key = bytes.fromhex(key_hex)
    iv = bytes.fromhex(iv_hex)
    ciphertext = bytes.fromhex(ciphertext_hex)
    if mode == "aes128":
        plaintext = AES_128_CBC.decrypt(ciphertext, key, iv)
    else:
        plaintext = AES_256_CBC.decrypt(ciphertext, key, iv)
    
    if plaintext.hex() == expected_hex:
        print(f"{mode.upper()} Decrypt Valid: Yes")
    else:
        print(f"{mode.upper()} Decrypt Valid: No, got {plaintext.hex()}")

def check_hmac(key_hex, data_hex, expected_hex):
    import RNS.Cryptography.HMAC as HMAC
    key = bytes.fromhex(key_hex)
    data = bytes.fromhex(data_hex)
    digest = HMAC.digest(key, data, hashlib.sha256)
    if digest.hex() == expected_hex:
        print("HMAC Valid: Yes")
    else:
        print(f"HMAC Valid: No, got {digest.hex()}")

if __name__ == "__main__":
    mode = sys.argv[1]
    if mode == "ed25519":
        check_ed25519(sys.argv[2], sys.argv[3], sys.argv[4])
    elif mode == "x25519":
        check_x25519(sys.argv[2], sys.argv[3], sys.argv[4])
    elif mode == "hkdf":
        check_hkdf(sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5])
    elif mode == "token":
        check_token(sys.argv[2], sys.argv[3], sys.argv[4])
    elif mode == "sha256":
        check_sha256(sys.argv[2], sys.argv[3])
    elif mode == "sha512":
        check_sha512(sys.argv[2], sys.argv[3])
    elif mode == "pkcs7_pad":
        check_pkcs7_pad(sys.argv[2], int(sys.argv[3]), sys.argv[4])
    elif mode == "pkcs7_unpad":
        check_pkcs7_unpad(sys.argv[2], sys.argv[3])
    elif mode == "aes128_encrypt":
        check_aes_encrypt("aes128", sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5])
    elif mode == "aes128_decrypt":
        check_aes_decrypt("aes128", sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5])
    elif mode == "aes256_encrypt":
        check_aes_encrypt("aes256", sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5])
    elif mode == "aes256_decrypt":
        check_aes_decrypt("aes256", sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5])
    elif mode == "hmac":
        check_hmac(sys.argv[2], sys.argv[3], sys.argv[4])
`

func TestHMACParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("secret-key")
	data := []byte("hello hmac parity")
	digest := HMAC(key, data)

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "hmac", fmt.Sprintf("%x", key), fmt.Sprintf("%x", data), fmt.Sprintf("%x", digest))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "HMAC Valid: Yes") {
		t.Errorf("Python reported HMAC mismatch\nOutput: %v", string(out))
	}
}

func TestEd25519Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	prv, err := GenerateEd25519PrivateKey()
	mustTest(t, err)
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
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	prv, err := GenerateX25519PrivateKey()
	mustTest(t, err)

	// Create a second key pair to act as peer
	peerPrv, _ := GenerateX25519PrivateKey()
	peerPub := peerPrv.PublicKey()

	shared, err := prv.Exchange(peerPub)
	mustTest(t, err)

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
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	deriveFrom := []byte("input material")
	salt := []byte("salty")
	length := 32

	derived, err := HKDF(length, deriveFrom, salt, nil)
	mustTest(t, err)

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

func TestTokenParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("01234567890123456789012345678901") // 32 bytes
	token := mustTestNewToken(t, key)

	plaintext := []byte("secret message for token parity")
	ciphertext, err := token.Encrypt(plaintext)
	mustTest(t, err)

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "token", fmt.Sprintf("%x", key), fmt.Sprintf("%x", ciphertext), fmt.Sprintf("%x", plaintext))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "Token Valid: Yes") {
		t.Errorf("Python reported Token decryption failure or mismatch\nOutput: %v", string(out))
	}
}

func TestSHA256Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	data := []byte("hello sha256 parity")
	digest := SHA256(data)

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "sha256", fmt.Sprintf("%x", data), fmt.Sprintf("%x", digest))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "SHA256 Valid: Yes") {
		t.Errorf("Python reported SHA256 mismatch\nOutput: %v", string(out))
	}
}

func TestSHA512Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	data := []byte("hello sha512 parity")
	digest := SHA512(data)

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, "sha512", fmt.Sprintf("%x", data), fmt.Sprintf("%x", digest))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "SHA512 Valid: Yes") {
		t.Errorf("Python reported SHA512 mismatch\nOutput: %v", string(out))
	}
}

func TestPKCS7Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	data := []byte("pkcs7 parity")
	blockSize := 16
	padded := PKCS7Pad(data, blockSize)

	// Verify padding with Python
	cmd := exec.Command("python3", scriptPath, "pkcs7_pad", fmt.Sprintf("%x", data), fmt.Sprintf("%v", blockSize), fmt.Sprintf("%x", padded))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed (pad): %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "PKCS7 Pad Valid: Yes") {
		t.Errorf("Python reported PKCS7 padding mismatch\nOutput: %v", string(out))
	}

	// Verify unpadding with Python
	unpadded, err := PKCS7Unpad(padded)
	mustTest(t, err)

	cmd = exec.Command("python3", scriptPath, "pkcs7_unpad", fmt.Sprintf("%x", padded), fmt.Sprintf("%x", unpadded))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed (unpad): %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "PKCS7 Unpad Valid: Yes") {
		t.Errorf("Python reported PKCS7 unpadding mismatch\nOutput: %v", string(out))
	}
}

func TestAES128Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("0123456789012345") // 16 bytes
	iv := []byte("0123456789012345")  // 16 bytes
	plaintext := PKCS7Pad([]byte("aes128 parity message"), 16)

	ciphertext, err := AES128CBCEncrypt(plaintext, key, iv)
	mustTest(t, err)

	// Verify encryption with Python
	cmd := exec.Command("python3", scriptPath, "aes128_encrypt", fmt.Sprintf("%x", key), fmt.Sprintf("%x", iv), fmt.Sprintf("%x", plaintext), fmt.Sprintf("%x", ciphertext))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed (encrypt): %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "AES128 Encrypt Valid: Yes") {
		t.Errorf("Python reported AES128 encryption mismatch\nOutput: %v", string(out))
	}

	// Verify decryption with Python
	decrypted, err := AES128CBCDecrypt(ciphertext, key, iv)
	mustTest(t, err)

	cmd = exec.Command("python3", scriptPath, "aes128_decrypt", fmt.Sprintf("%x", key), fmt.Sprintf("%x", iv), fmt.Sprintf("%x", ciphertext), fmt.Sprintf("%x", decrypted))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed (decrypt): %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "AES128 Decrypt Valid: Yes") {
		t.Errorf("Python reported AES128 decryption mismatch\nOutput: %v", string(out))
	}
}

func TestAES256Parity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-crypto-parity-*")
	mustTest(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
		}
	})

	scriptPath := filepath.Join(tmpDir, "check_crypto_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkCryptoParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("01234567890123456789012345678901") // 32 bytes
	iv := []byte("0123456789012345")                  // 16 bytes
	plaintext := PKCS7Pad([]byte("aes256 parity message"), 16)

	ciphertext, err := AES256CBCEncrypt(plaintext, key, iv)
	mustTest(t, err)

	// Verify encryption with Python
	cmd := exec.Command("python3", scriptPath, "aes256_encrypt", fmt.Sprintf("%x", key), fmt.Sprintf("%x", iv), fmt.Sprintf("%x", plaintext), fmt.Sprintf("%x", ciphertext))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed (encrypt): %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "AES256 Encrypt Valid: Yes") {
		t.Errorf("Python reported AES256 encryption mismatch\nOutput: %v", string(out))
	}

	// Verify decryption with Python
	decrypted, err := AES256CBCDecrypt(ciphertext, key, iv)
	mustTest(t, err)

	cmd = exec.Command("python3", scriptPath, "aes256_decrypt", fmt.Sprintf("%x", key), fmt.Sprintf("%x", iv), fmt.Sprintf("%x", ciphertext), fmt.Sprintf("%x", decrypted))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed (decrypt): %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "AES256 Decrypt Valid: Yes") {
		t.Errorf("Python reported AES256 decryption mismatch\nOutput: %v", string(out))
	}
}
