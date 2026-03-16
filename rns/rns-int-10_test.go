// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func runPythonRatchetEncrypt(t *testing.T, initScriptPath, pyStorage string, destHash, pubKey, msg []byte, pyListenPort, goListenPort int, announceFn func() error) ([]byte, []byte, string) {
	t.Helper()

	cmd := exec.Command("python3", initScriptPath, pyStorage, fmt.Sprintf("%x", destHash), fmt.Sprintf("%x", pubKey), fmt.Sprintf("%x", msg), strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())

	announceErr := make(chan error, 1)
	go func() {
		var err error
		for i := 0; i < 3; i++ {
			time.Sleep(50 * time.Millisecond)
			if e := announceFn(); e != nil {
				err = e
				break
			}
		}
		announceErr <- err
	}()

	out, err := cmd.CombinedOutput()
	if announceErrVal := <-announceErr; announceErrVal != nil {
		t.Fatalf("failed to announce during Python initiator run: %v", announceErrVal)
	}
	if err != nil {
		t.Fatalf("Python initiator failed: %v\nOutput: %v", err, string(out))
	}

	var encryptedHex, ratchetIDHex string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Encrypted: ") {
			encryptedHex = strings.TrimPrefix(line, "Encrypted: ")
		}
		if strings.HasPrefix(line, "RatchetID: ") {
			ratchetIDHex = strings.TrimPrefix(line, "RatchetID: ")
		}
	}

	if encryptedHex == "" {
		t.Fatalf("failed to get encrypted data from Python output: %v", string(out))
	}
	encrypted, _ := HexToBytes(encryptedHex)

	var ratchetID []byte
	if ratchetIDHex != "" {
		ratchetID, _ = HexToBytes(ratchetIDHex)
	}

	return encrypted, ratchetID, string(out)
}

const ratchetParityPy = `import RNS
import sys
import os
import time

def start_ratchet_receiver(storage_path, ratchets_path, id_path, listen_port, forward_port):
    try:
        config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
		listen_port = {listen_port}
    forward_ip = 127.0.0.1
		forward_port = {forward_port}
"""
        with open(os.path.join(storage_path, "config"), "w") as f:
            f.write(config_content)

        reticulum = RNS.Reticulum(configdir=storage_path, loglevel=RNS.LOG_DEBUG)
        RNS.logdest = RNS.LOG_STDOUT

        # Use fixed seed for identity to make it reproducible
        identity = RNS.Identity(create_keys=False)
        identity.load_private_key(b"\x01" * 64)
        identity.to_file(id_path)

        destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "ratchet_test", "parity")
        destination.enable_ratchets(ratchets_path)

        print(f"Destination Hash: {destination.hash.hex()}")
        print(f"Identity Public Key: {identity.get_public_key().hex()}")
        sys.stdout.flush()

        # Rotate and share ratchet
        destination.announce()
        if destination.ratchets:
            r = destination.ratchets[0]
            print(f"Python: Ratchet Pub: {RNS.Identity._ratchet_public_bytes(r).hex()}")
            print(f"Python: Name Hash Len: {len(destination.name_hash)}")
            sys.stdout.flush()

        # Keep running until done signal
        done_file = os.path.join(storage_path, "done")
        timeout = time.time() + 20
        while not os.path.exists(done_file) and time.time() < timeout:
            time.sleep(0.5)

        print("Receiver exiting")
        sys.stdout.flush()

    except Exception as e:
        print(f"Error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)

def decrypt_with_ratchet(storage_path, ratchets_path, id_path, ciphertext_hex):
    try:
        reticulum = RNS.Reticulum(configdir=storage_path, loglevel=RNS.LOG_DEBUG)

        identity = RNS.Identity.from_file(id_path)
        print(f"Python: Identity Hash: {identity.hash.hex()}")
        sys.stdout.flush()

        destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "ratchet_test", "parity")
        destination.enable_ratchets(ratchets_path)

        ciphertext = bytes.fromhex(ciphertext_hex)
        plaintext = destination.decrypt(ciphertext)

        if plaintext:
            print(f"Decrypted: {plaintext.hex()}")
            sys.exit(0)
        else:
            print("Decryption failed")
            sys.exit(1)
    except Exception as e:
        print(f"Error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)

if __name__ == "__main__":
    mode = sys.argv[1]
    if mode == "receiver":
        start_ratchet_receiver(sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5]), int(sys.argv[6]))
    elif mode == "decrypt":
        decrypt_with_ratchet(sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5])
`

const ratchetInitiatorPy = `import RNS
import sys
import os
import time

def start_ratchet_initiator(storage_path, dest_hash_hex, pub_key_hex, msg_hex, listen_port, forward_port):
    try:
        config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
	[[UDP Interface]]
		type = UDPInterface
		enabled = True
		listen_ip = 127.0.0.1
		listen_port = {listen_port}
		forward_ip = 127.0.0.1
		forward_port = {forward_port}
"""
        with open(os.path.join(storage_path, "config"), "w") as f:
            f.write(config_content)

        reticulum = RNS.Reticulum(configdir=storage_path, loglevel=RNS.LOG_DEBUG)
        RNS.logdest = RNS.LOG_STDOUT

        dest_hash = bytes.fromhex(dest_hash_hex)
        pub_key = bytes.fromhex(pub_key_hex)

        # Wait for announce with ratchet
        timeout = time.time() + 10
        while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
            time.sleep(0.5)

        remote_identity = RNS.Identity(create_keys=False)
        remote_identity.load_public_key(pub_key)
        destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "ratchet_test", "parity")
        print(f"Python Initiator: Dest Hash: {destination.hash.hex()}")
        sys.stdout.flush()

        # Encrypt and print
        msg = bytes.fromhex(msg_hex)
        encrypted = destination.encrypt(msg)
        print(f"Encrypted: {encrypted.hex()}")
        if destination.latest_ratchet_id:
            print(f"RatchetID: {destination.latest_ratchet_id.hex()}")
        sys.stdout.flush()

    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    start_ratchet_initiator(sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5]), int(sys.argv[6]))
`

const ratchetEnforcePy = `import RNS
import sys

def encrypt_identity(pub_key_hex, msg_hex):
    remote_identity = RNS.Identity(create_keys=False)
    remote_identity.load_public_key(bytes.fromhex(pub_key_hex))
    encrypted = remote_identity.encrypt(bytes.fromhex(msg_hex))
    print(f"Encrypted: {encrypted.hex()}")

def encrypt_ratchet(pub_key_hex, ratchet_pub_hex, msg_hex):
    remote_identity = RNS.Identity(create_keys=False)
    remote_identity.load_public_key(bytes.fromhex(pub_key_hex))
    encrypted = remote_identity.encrypt(bytes.fromhex(msg_hex), ratchet=bytes.fromhex(ratchet_pub_hex))
    print(f"Encrypted: {encrypted.hex()}")

if __name__ == "__main__":
    mode = sys.argv[1]
    if mode == "identity":
        encrypt_identity(sys.argv[2], sys.argv[3])
    elif mode == "ratchet":
        encrypt_ratchet(sys.argv[2], sys.argv[3], sys.argv[4])
`

const ratchetFileInteropPy = `import RNS
import sys
import os

def ensure_config(storage_path):
	if not os.path.exists(storage_path):
		os.makedirs(storage_path)

	config_content = """
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
"""
	with open(os.path.join(storage_path, "config"), "w") as f:
		f.write(config_content)

def load_decrypt(storage_path, ratchets_path, id_path, ciphertext_hex, expected_hex):
	ensure_config(storage_path)
	reticulum = RNS.Reticulum(configdir=storage_path, loglevel=RNS.LOG_ERROR)
	RNS.logdest = RNS.LOG_STDOUT

	identity = RNS.Identity.from_file(id_path)
	destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "ratchet_test", "parity")
	destination.enable_ratchets(ratchets_path)

	plaintext = destination.decrypt(bytes.fromhex(ciphertext_hex))
	if plaintext == bytes.fromhex(expected_hex):
		print("DecryptOK")
		sys.exit(0)

	print("DecryptMismatch")
	sys.exit(1)

def generate(storage_path, ratchets_path, id_path):
	ensure_config(storage_path)
	reticulum = RNS.Reticulum(configdir=storage_path, loglevel=RNS.LOG_ERROR)
	RNS.logdest = RNS.LOG_STDOUT

	identity = RNS.Identity.from_file(id_path)
	destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "ratchet_test", "parity")
	destination.enable_ratchets(ratchets_path)
	destination.rotate_ratchets()

	ratchet_pub = RNS.Identity._ratchet_public_bytes(destination.ratchets[0])
	print(f"RatchetPub: {ratchet_pub.hex()}")
	sys.exit(0)

def expect_load_fail(storage_path, ratchets_path, id_path):
	ensure_config(storage_path)
	reticulum = RNS.Reticulum(configdir=storage_path, loglevel=RNS.LOG_ERROR)
	RNS.logdest = RNS.LOG_STDOUT

	identity = RNS.Identity.from_file(id_path)
	destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "ratchet_test", "parity")

	try:
		destination.enable_ratchets(ratchets_path)
		print("UnexpectedLoadSuccess")
		sys.exit(1)
	except Exception:
		print("LoadFailOK")
		sys.exit(0)

if __name__ == "__main__":
	mode = sys.argv[1]
	if mode == "load_decrypt":
		load_decrypt(sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5], sys.argv[6])
	elif mode == "generate":
		generate(sys.argv[2], sys.argv[3], sys.argv[4])
	elif mode == "expect_load_fail":
		expect_load_fail(sys.argv[2], sys.argv[3], sys.argv[4])
`

func runPythonDirectEncrypt(t *testing.T, scriptPath, mode string, args ...string) []byte {
	t.Helper()

	cmdArgs := []string{scriptPath, mode}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("python3", cmdArgs...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python direct encrypt failed: %v\nOutput: %v", err, string(out))
	}

	var encryptedHex string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Encrypted: ") {
			encryptedHex = strings.TrimPrefix(line, "Encrypted: ")
			break
		}
	}
	if encryptedHex == "" {
		t.Fatalf("missing encrypted payload in python output: %v", string(out))
	}
	encrypted, _ := HexToBytes(encryptedHex)
	return encrypted
}

func runPythonInteropCmd(t *testing.T, scriptPath, mode string, args ...string) string {
	t.Helper()

	cmdArgs := []string{scriptPath, mode}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("python3", cmdArgs...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python interop command failed: %v\nOutput: %v", err, string(out))
	}
	return string(out)
}

func TestRatchetGoToPythonParity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-ratchet-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ResetTransport()
	pyListenPort, goListenPort := allocateUDPPortPair(t)

	pyStorage := filepath.Join(tmpDir, "py_rns")
	os.MkdirAll(pyStorage, 0700)
	pyRatchets := filepath.Join(tmpDir, "py_ratchets")
	pyIdPath := filepath.Join(tmpDir, "py_id")

	scriptPath := filepath.Join(tmpDir, "ratchet_parity.py")
	if err := os.WriteFile(scriptPath, []byte(ratchetParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	// Start Python receiver to get its announce with ratchet
	pyCmd := exec.Command("python3", scriptPath, "receiver", pyStorage, pyRatchets, pyIdPath, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	// Initialize Go Reticulum to receive announce
	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)

	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	// Wait for Python destination info and announce
	pyScanner := bufio.NewScanner(pyStdout)
	var destHashHex, pyPubHex, pyRatchetHex string
	for pyScanner.Scan() {
		line := pyScanner.Text()
		fmt.Printf("[Python] %v\n", line)
		if strings.HasPrefix(line, "Destination Hash: ") {
			destHashHex = strings.TrimPrefix(line, "Destination Hash: ")
		} else if strings.HasPrefix(line, "Identity Public Key: ") {
			pyPubHex = strings.TrimPrefix(line, "Identity Public Key: ")
		} else if strings.HasPrefix(line, "Python: Ratchet Pub: ") {
			pyRatchetHex = strings.TrimPrefix(line, "Python: Ratchet Pub: ")
		}

		if destHashHex != "" && pyPubHex != "" && pyRatchetHex != "" {
			break
		}
	}

	if destHashHex == "" || pyPubHex == "" || pyRatchetHex == "" {
		t.Fatal("Failed to get Python destination info")
	}
	destHash, _ := HexToBytes(destHashHex)
	pyPub, _ := HexToBytes(pyPubHex)
	pyRatchet, _ := HexToBytes(pyRatchetHex)

	// Wait for ratchet to be learned via announce
	timeout := time.Now().Add(10 * time.Second)
	var ratchetPub []byte
	for time.Now().Before(timeout) {
		ratchetPub = GetRatchet(destHash)
		if ratchetPub != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if ratchetPub == nil {
		t.Fatal("Go failed to learn ratchet from Python announce")
	}
	if !bytes.Equal(ratchetPub, pyRatchet) {
		t.Fatalf("Go learned ratchet mismatch!\nGo: %x\nPy: %x", ratchetPub, pyRatchet)
	}

	// Send encrypted packet using ratchet
	remoteId := mustTestNewIdentity(t, false)
	remoteId.LoadPublicKey(pyPub)
	fmt.Printf("Go: Remote Identity Hash: %x\n", remoteId.Hash)

	remoteDest := mustTestNewDestination(t, remoteId, DestinationOut, DestinationSingle, "ratchet_test", "parity")

	msg := []byte("ratchet secret message")
	encrypted, err := remoteDest.Encrypt(msg)
	if err != nil {
		t.Fatalf("Go failed to encrypt with ratchet: %v", err)
	}

	// Signal Python to exit receiver and then try to decrypt
	os.WriteFile(filepath.Join(pyStorage, "done"), []byte("done"), 0o644)
	pyCmd.Wait()

	// Verify Python can decrypt using stored ratchet
	verifyCmd := exec.Command("python3", scriptPath, "decrypt", pyStorage, pyRatchets, pyIdPath, fmt.Sprintf("%x", encrypted))
	verifyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := verifyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python decryption failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "Decrypted: "+fmt.Sprintf("%x", msg)) {
		t.Errorf("Decrypted message mismatch!\nExpected: %x\nGot: %v", msg, string(out))
	}
}

func TestRatchetPythonToGoParity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-ratchet-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ResetTransport()
	pyListenPort, goListenPort := allocateUDPPortPair(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)
	goRatchets := filepath.Join(goConfigDir, "ratchets_file")

	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	// Create Go destination with ratchets
	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "ratchet_test", "parity")
	if err := dest.EnableRatchets(goRatchets); err != nil {
		t.Fatal(err)
	}

	// Start periodic announces
	stopAnnounce := make(chan bool)
	go func() {
		for {
			select {
			case <-stopAnnounce:
				return
			default:
				dest.Announce(nil)
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()
	defer close(stopAnnounce)

	// Start Python initiator
	msg := []byte("secret from python")
	pyStorage := filepath.Join(tmpDir, "py_rns")
	os.MkdirAll(pyStorage, 0700)

	initScriptPath := filepath.Join(tmpDir, "ratchet_initiator.py")
	os.WriteFile(initScriptPath, []byte(ratchetInitiatorPy), 0o644)

	pyCmd := exec.Command("python3", initScriptPath, pyStorage, fmt.Sprintf("%x", dest.Hash), fmt.Sprintf("%x", id.GetPublicKey()), fmt.Sprintf("%x", msg), strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := pyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python initiator failed: %v\nOutput: %v", err, string(out))
	}

	// Parse encrypted data from Python
	var pyEncryptedHex string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Encrypted: ") {
			pyEncryptedHex = strings.TrimPrefix(line, "Encrypted: ")
			break
		}
	}

	if pyEncryptedHex == "" {
		t.Fatalf("Failed to get encrypted data from Python: %v", string(out))
	}
	pyEncrypted, _ := HexToBytes(pyEncryptedHex)

	// Decrypt in Go
	decrypted, err := dest.Decrypt(pyEncrypted)
	if err != nil {
		t.Fatalf("Go failed to decrypt Python's ratchet-encrypted packet: %v", err)
	}

	if !bytes.Equal(msg, decrypted) {
		t.Errorf("Decrypted message mismatch!\nExpected: %v\nGot: %v", string(msg), string(decrypted))
	}
}

func TestRatchetRotationParity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-ratchet-rotation-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ResetTransport()
	pyListenPort, goListenPort := allocateUDPPortPair(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)
	goRatchets := filepath.Join(goConfigDir, "ratchets_file")

	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "ratchet_test", "parity")
	if err := dest.EnableRatchets(goRatchets); err != nil {
		t.Fatal(err)
	}

	dest.ratchetInterval = 24 * time.Hour

	if err := dest.RotateRatchets(); err != nil {
		t.Fatalf("failed initial ratchet rotation: %v", err)
	}
	oldRatchetPub := append([]byte(nil), dest.ratchets[0].PublicKey().PublicBytes()...)
	oldRatchetID := RatchetID(oldRatchetPub)

	pyStorage := filepath.Join(tmpDir, "py_rns")
	os.MkdirAll(pyStorage, 0700)
	initScriptPath := filepath.Join(tmpDir, "ratchet_initiator.py")
	os.WriteFile(initScriptPath, []byte(ratchetInitiatorPy), 0o644)

	oldMsg := []byte("rotation-old-message")
	oldCipher, pyOldRatchetID, _ := runPythonRatchetEncrypt(t, initScriptPath, pyStorage, dest.Hash, id.GetPublicKey(), oldMsg, pyListenPort, goListenPort, func() error {
		return dest.Announce(nil)
	})
	if len(pyOldRatchetID) == 0 {
		t.Fatal("python did not report ratchet id for old ratchet encryption")
	}
	if !bytes.Equal(pyOldRatchetID, oldRatchetID) {
		t.Fatalf("old ratchet ID mismatch\nGo: %x\nPy: %x", oldRatchetID, pyOldRatchetID)
	}

	dest.latestRatchetTime = time.Time{}
	if err := dest.RotateRatchets(); err != nil {
		t.Fatalf("failed second ratchet rotation: %v", err)
	}
	newRatchetPub := append([]byte(nil), dest.ratchets[0].PublicKey().PublicBytes()...)
	newRatchetID := RatchetID(newRatchetPub)
	if bytes.Equal(oldRatchetPub, newRatchetPub) {
		t.Fatal("ratchet did not rotate; old and new public keys are identical")
	}

	newMsg := []byte("rotation-new-message")
	newCipher, pyNewRatchetID, out := runPythonRatchetEncrypt(t, initScriptPath, pyStorage, dest.Hash, id.GetPublicKey(), newMsg, pyListenPort, goListenPort, func() error {
		return dest.Announce(nil)
	})
	if len(pyNewRatchetID) == 0 {
		t.Fatalf("python did not report ratchet id for new ratchet encryption\nOutput: %v", out)
	}
	if !bytes.Equal(pyNewRatchetID, newRatchetID) {
		t.Fatalf("new ratchet ID mismatch\nGo: %x\nPy: %x", newRatchetID, pyNewRatchetID)
	}

	oldDecrypted, err := dest.Decrypt(oldCipher)
	if err != nil {
		t.Fatalf("failed to decrypt old-ratchet ciphertext after rotation: %v", err)
	}
	if !bytes.Equal(oldMsg, oldDecrypted) {
		t.Fatalf("old-ratchet plaintext mismatch\nExpected: %v\nGot: %v", string(oldMsg), string(oldDecrypted))
	}

	newDecrypted, err := dest.Decrypt(newCipher)
	if err != nil {
		t.Fatalf("failed to decrypt new-ratchet ciphertext: %v", err)
	}
	if !bytes.Equal(newMsg, newDecrypted) {
		t.Fatalf("new-ratchet plaintext mismatch\nExpected: %v\nGot: %v", string(newMsg), string(newDecrypted))
	}
}

func TestRatchetRetentionWindowParity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-ratchet-retention-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ResetTransport()
	pyListenPort, goListenPort := allocateUDPPortPair(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)
	goRatchets := filepath.Join(goConfigDir, "ratchets_file")

	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "ratchet_test", "parity")
	if err := dest.EnableRatchets(goRatchets); err != nil {
		t.Fatal(err)
	}

	dest.ratchetInterval = 24 * time.Hour
	dest.retainedRatchets = 2

	forceRotate := func() {
		dest.latestRatchetTime = time.Time{}
		if err := dest.RotateRatchets(); err != nil {
			t.Fatalf("failed to rotate ratchets: %v", err)
		}
	}

	pyStorage := filepath.Join(tmpDir, "py_rns")
	os.MkdirAll(pyStorage, 0700)
	initScriptPath := filepath.Join(tmpDir, "ratchet_initiator.py")
	os.WriteFile(initScriptPath, []byte(ratchetInitiatorPy), 0o644)

	forceRotate()
	ratchetAID := RatchetID(dest.ratchets[0].PublicKey().PublicBytes())
	msgA := []byte("retention-msg-a")
	cipherA, pyRatchetAID, _ := runPythonRatchetEncrypt(t, initScriptPath, pyStorage, dest.Hash, id.GetPublicKey(), msgA, pyListenPort, goListenPort, func() error {
		return dest.Announce(nil)
	})
	if !bytes.Equal(pyRatchetAID, ratchetAID) {
		t.Fatalf("ratchet A ID mismatch\nGo: %x\nPy: %x", ratchetAID, pyRatchetAID)
	}

	forceRotate()
	ratchetBID := RatchetID(dest.ratchets[0].PublicKey().PublicBytes())
	msgB := []byte("retention-msg-b")
	cipherB, pyRatchetBID, _ := runPythonRatchetEncrypt(t, initScriptPath, pyStorage, dest.Hash, id.GetPublicKey(), msgB, pyListenPort, goListenPort, func() error {
		return dest.Announce(nil)
	})
	if !bytes.Equal(pyRatchetBID, ratchetBID) {
		t.Fatalf("ratchet B ID mismatch\nGo: %x\nPy: %x", ratchetBID, pyRatchetBID)
	}

	forceRotate()
	ratchetCID := RatchetID(dest.ratchets[0].PublicKey().PublicBytes())
	msgC := []byte("retention-msg-c")
	cipherC, pyRatchetCID, _ := runPythonRatchetEncrypt(t, initScriptPath, pyStorage, dest.Hash, id.GetPublicKey(), msgC, pyListenPort, goListenPort, func() error {
		return dest.Announce(nil)
	})
	if !bytes.Equal(pyRatchetCID, ratchetCID) {
		t.Fatalf("ratchet C ID mismatch\nGo: %x\nPy: %x", ratchetCID, pyRatchetCID)
	}

	if len(dest.ratchets) != 2 {
		t.Fatalf("retained ratchet count mismatch: expected 2, got %v", len(dest.ratchets))
	}

	if gotB, err := dest.Decrypt(cipherB); err != nil || !bytes.Equal(gotB, msgB) {
		t.Fatalf("failed to decrypt retained-window ciphertext B: err=%v got=%q", err, string(gotB))
	}
	if gotC, err := dest.Decrypt(cipherC); err != nil || !bytes.Equal(gotC, msgC) {
		t.Fatalf("failed to decrypt retained-window ciphertext C: err=%v got=%q", err, string(gotC))
	}

	if gotA, err := dest.Decrypt(cipherA); err == nil {
		t.Fatalf("expected pruned ratchet ciphertext A to fail decryption, got plaintext: %q", string(gotA))
	}
}

func TestRatchetEnforceParity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-ratchet-enforce-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ResetTransport()

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "ratchet_test", "parity")
	if err := dest.EnableRatchets(filepath.Join(tmpDir, "ratchets_file")); err != nil {
		t.Fatal(err)
	}

	dest.latestRatchetTime = time.Time{}
	if err := dest.RotateRatchets(); err != nil {
		t.Fatalf("failed ratchet rotation: %v", err)
	}
	ratchetPub := dest.ratchets[0].PublicKey().PublicBytes()

	scriptPath := filepath.Join(tmpDir, "ratchet_enforce.py")
	if err := os.WriteFile(scriptPath, []byte(ratchetEnforcePy), 0o644); err != nil {
		t.Fatal(err)
	}

	identityMsg := []byte("enforce-identity-fallback")
	ratchetMsg := []byte("enforce-ratchet-accepted")

	identityCipher := runPythonDirectEncrypt(t, scriptPath, "identity", fmt.Sprintf("%x", id.GetPublicKey()), fmt.Sprintf("%x", identityMsg))
	ratchetCipher := runPythonDirectEncrypt(t, scriptPath, "ratchet", fmt.Sprintf("%x", id.GetPublicKey()), fmt.Sprintf("%x", ratchetPub), fmt.Sprintf("%x", ratchetMsg))

	// Enforcement disabled: both identity-encrypted and ratchet-encrypted payloads should decrypt.
	dest.enforceRatchets = false
	if got, err := dest.Decrypt(identityCipher); err != nil || !bytes.Equal(got, identityMsg) {
		t.Fatalf("enforcement disabled: identity ciphertext decrypt failed: err=%v got=%q", err, string(got))
	}
	if got, err := dest.Decrypt(ratchetCipher); err != nil || !bytes.Equal(got, ratchetMsg) {
		t.Fatalf("enforcement disabled: ratchet ciphertext decrypt failed: err=%v got=%q", err, string(got))
	}

	// Enforcement enabled: identity-encrypted payloads should fail, ratchet-encrypted should still pass.
	dest.enforceRatchets = true
	if got, err := dest.Decrypt(identityCipher); err == nil {
		t.Fatalf("enforcement enabled: expected identity ciphertext to fail, got plaintext: %q", string(got))
	}
	if got, err := dest.Decrypt(ratchetCipher); err != nil || !bytes.Equal(got, ratchetMsg) {
		t.Fatalf("enforcement enabled: ratchet ciphertext decrypt failed: err=%v got=%q", err, string(got))
	}
}

func TestRatchetFileInteropParity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-ratchet-file-interop-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ResetTransport()

	scriptPath := filepath.Join(tmpDir, "ratchet_file_interop.py")
	if err := os.WriteFile(scriptPath, []byte(ratchetFileInteropPy), 0o644); err != nil {
		t.Fatal(err)
	}

	// Part 1: Go-generated ratchet file can be loaded/decrypted by Python.
	goID := mustTestNewIdentity(t, true)
	goIDPath := filepath.Join(tmpDir, "go_id")
	if err := goID.ToFile(goIDPath); err != nil {
		t.Fatalf("failed to persist go identity: %v", err)
	}

	goRatchetsPath := filepath.Join(tmpDir, "go_ratchets")
	goDest := mustTestNewDestination(t, goID, DestinationIn, DestinationSingle, "ratchet_test", "parity")
	if err := goDest.EnableRatchets(goRatchetsPath); err != nil {
		t.Fatal(err)
	}
	goDest.latestRatchetTime = time.Time{}
	if err := goDest.RotateRatchets(); err != nil {
		t.Fatalf("failed to rotate go ratchets: %v", err)
	}

	goRatchetPub := goDest.ratchets[0].PublicKey().PublicBytes()
	goToPyMsg := []byte("go-ratchet-file-to-python")
	goCipher, err := goID.Encrypt(goToPyMsg, goRatchetPub)
	if err != nil {
		t.Fatalf("failed to encrypt go->python ratchet message: %v", err)
	}

	pyStorage1 := filepath.Join(tmpDir, "py_storage_1")
	pyOut := runPythonInteropCmd(t, scriptPath, "load_decrypt", pyStorage1, goRatchetsPath, goIDPath, fmt.Sprintf("%x", goCipher), fmt.Sprintf("%x", goToPyMsg))
	if !strings.Contains(pyOut, "DecryptOK") {
		t.Fatalf("python failed go-ratchet-file decrypt interop: %v", pyOut)
	}

	// Part 2: Python-generated ratchet file can be loaded/decrypted by Go.
	pyID := mustTestNewIdentity(t, true)
	pyIDPath := filepath.Join(tmpDir, "py_id")
	if err := pyID.ToFile(pyIDPath); err != nil {
		t.Fatalf("failed to persist python-side identity: %v", err)
	}
	pyRatchetsPath := filepath.Join(tmpDir, "py_ratchets")
	pyStorage2 := filepath.Join(tmpDir, "py_storage_2")

	genOut := runPythonInteropCmd(t, scriptPath, "generate", pyStorage2, pyRatchetsPath, pyIDPath)
	var pyRatchetPubHex string
	for _, line := range strings.Split(genOut, "\n") {
		if strings.HasPrefix(line, "RatchetPub: ") {
			pyRatchetPubHex = strings.TrimPrefix(line, "RatchetPub: ")
			break
		}
	}
	if pyRatchetPubHex == "" {
		t.Fatalf("python did not emit ratchet pub: %v", genOut)
	}
	pyRatchetPub, _ := HexToBytes(pyRatchetPubHex)

	goLoadedID, err := FromFile(pyIDPath)
	if err != nil {
		t.Fatalf("failed loading python identity in go: %v", err)
	}
	goLoadDest := mustTestNewDestination(t, goLoadedID, DestinationIn, DestinationSingle, "ratchet_test", "parity")
	if err := goLoadDest.EnableRatchets(pyRatchetsPath); err != nil {
		t.Fatalf("go failed loading python ratchet file: %v", err)
	}

	pyToGoMsg := []byte("python-ratchet-file-to-go")
	pyCipher, err := goLoadedID.Encrypt(pyToGoMsg, pyRatchetPub)
	if err != nil {
		t.Fatalf("failed to encrypt python->go ratchet message: %v", err)
	}
	if got, err := goLoadDest.Decrypt(pyCipher); err != nil || !bytes.Equal(got, pyToGoMsg) {
		t.Fatalf("go failed decrypt with python ratchet file: err=%v got=%q", err, string(got))
	}

	// Part 3: Corrupted ratchet files are safely rejected.
	corruptPath := filepath.Join(tmpDir, "py_ratchets_corrupt")
	data, err := os.ReadFile(pyRatchetsPath)
	if err != nil {
		t.Fatalf("failed reading python ratchet file: %v", err)
	}
	if len(data) < 2 {
		t.Fatalf("python ratchet file unexpectedly short: %v", len(data))
	}
	data[len(data)-1] ^= 0x01
	if err := os.WriteFile(corruptPath, data, 0600); err != nil {
		t.Fatalf("failed writing corrupt ratchet file: %v", err)
	}

	goCorruptDest, err := NewDestination(goLoadedID, DestinationIn, DestinationSingle, "ratchet_test", "parity")
	if err != nil {
		t.Fatal(err)
	}
	if err := goCorruptDest.EnableRatchets(corruptPath); err == nil {
		t.Fatal("expected go to reject corrupted ratchet file, but load succeeded")
	}

	pyStorage3 := filepath.Join(tmpDir, "py_storage_3")
	pyFailOut := runPythonInteropCmd(t, scriptPath, "expect_load_fail", pyStorage3, corruptPath, pyIDPath)
	if !strings.Contains(pyFailOut, "LoadFailOK") {
		t.Fatalf("python did not reject corrupted ratchet file as expected: %v", pyFailOut)
	}
}
