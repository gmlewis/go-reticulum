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

const checkAnnounceParityPy = `import RNS
import sys
import os

def check_announce_packet(packet_path, identity_path):
    try:
        if not os.path.exists(packet_path):
            print(f"Packet file not found: {packet_path}")
            sys.exit(1)
        
        with open(packet_path, "rb") as f:
            raw = f.read()
        
        # We need a Reticulum instance to unpack
        reticulum = RNS.Reticulum(configdir="/tmp/rns_parity_test", loglevel=RNS.LOG_DEBUG)
        
        packet = RNS.Packet(None, raw)
        packet.unpack()
        
        if packet.packet_type != RNS.Packet.ANNOUNCE:
            print(f"Not an announce packet: {packet.packet_type}")
            sys.exit(1)
            
        # Verify announce
        if RNS.Identity.validate_announce(packet):
            print("Announce Valid: Yes")
            # Extract AppData
            # announce_data = public_key + name_hash + random_hash + ratchet + signature + app_data
            # Ed25519 pubkey = 32, X25519 pubkey = 32. Total = 64
            # name_hash = 10
            # random_hash = 10
            # ratchet = 32 if present, else 0
            # signature = 64
            if packet.context_flag == RNS.Packet.FLAG_SET:
                app_data = packet.data[180:]
            else:
                app_data = packet.data[148:]
            print(f"AppData: {app_data.hex()}")
            print(f"Destination Hash: {packet.destination_hash.hex()}")
            sys.exit(0)
        else:
            print("Announce Valid: No")
            sys.exit(1)
            
    except Exception as e:
        print(f"Error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: check_announce_parity.py <packet_path>")
        sys.exit(1)
    
    check_announce_packet(sys.argv[1], None)
`

const checkLinkProofParityPy = `import RNS
import sys
import os

def check_link_proof_packet(packet_path, link_id_hex, destination_pub_hex):
    try:
        if not os.path.exists(packet_path):
            print(f"Packet file not found: {packet_path}")
            sys.exit(1)
            
        with open(packet_path, "rb") as f:
            raw = f.read()
        
        reticulum = RNS.Reticulum(configdir="/tmp/rns_parity_test", loglevel=RNS.LOG_DEBUG)
        
        packet = RNS.Packet(None, raw)
        packet.unpack()
        
        if packet.packet_type != RNS.Packet.PROOF:
            print(f"Not a proof packet: {packet.packet_type}")
            sys.exit(1)
            
        if packet.context != RNS.Packet.LRPROOF:
            print(f"Not a link request proof: {packet.context}")
            sys.exit(1)
            
        # proof_data = signature (64) + pub_bytes (32)
        signature = packet.data[:64]
        peer_pub = packet.data[64:96]
        
        link_id = bytes.fromhex(link_id_hex)
        dest_pub = bytes.fromhex(destination_pub_hex)
        dest_sig_pub = dest_pub[32:64]
        
        signed_data = link_id + peer_pub + dest_sig_pub
        
        id = RNS.Identity(create_keys=False)
        id.load_public_key(dest_pub)
        
        if id.validate(signature, signed_data):
            print("Proof Valid: Yes")
            sys.exit(0)
        else:
            print("Proof Valid: No")
            sys.exit(1)
            
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) != 4:
        print("Usage: check_link_proof_parity.py <packet_path> <link_id_hex> <destination_pub_hex>")
        sys.exit(1)
    check_link_proof_packet(sys.argv[1], sys.argv[2], sys.argv[3])
`

const checkLinkRequestParityPy = `import RNS
import sys
import os

def check_link_request_packet(packet_path):
    try:
        if not os.path.exists(packet_path):
            print(f"Packet file not found: {packet_path}")
            sys.exit(1)
            
        with open(packet_path, "rb") as f:
            raw = f.read()
        
        reticulum = RNS.Reticulum(configdir="/tmp/rns_parity_test", loglevel=RNS.LOG_DEBUG)
        
        packet = RNS.Packet(None, raw)
        packet.unpack()
        
        if packet.packet_type != RNS.Packet.LINKREQUEST:
            print(f"Not a link request packet: {packet.packet_type}")
            sys.exit(1)
            
        # Data should be 32 (X25519) + 32 (Ed25519) = 64 bytes
        if len(packet.data) < 64:
            print(f"Link request data too short: {len(packet.data)}")
            sys.exit(1)
            
        x25519_pub = packet.data[:32]
        ed25519_pub = packet.data[32:64]
        
        print(f"X25519 Pub: {x25519_pub.hex()}")
        print(f"Ed25519 Pub: {ed25519_pub.hex()}")
        print(f"Destination Hash: {packet.destination_hash.hex()}")
        sys.exit(0)
            
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: check_link_request_parity.py <packet_path>")
        sys.exit(1)
    check_link_request_packet(sys.argv[1])
`

func TestAnnouncePacketParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_announce_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkAnnounceParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	id, err := NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}

	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "parityapp", "aspect1")
	if err != nil {
		t.Fatal(err)
	}

	appData := []byte("parity data")
	randomHash := []byte("fixedrandh") // 10 bytes

	// Manually construct announce packet to use fixed random hash
	// signed_data = destination_hash+public_key+name_hash+random_hash+ratchet+app_data
	signedData := make([]byte, 0, 256)
	signedData = append(signedData, dest.Hash...)
	signedData = append(signedData, dest.identity.GetPublicKey()...)
	signedData = append(signedData, dest.nameHash...)
	signedData = append(signedData, randomHash...)
	// No ratchet flag added to data if not enabled
	signedData = append(signedData, appData...)

	signature, err := dest.identity.Sign(signedData)
	if err != nil {
		t.Fatal(err)
	}

	// announce_data = public_key+name_hash+random_hash+ratchet+signature+app_data
	announceData := make([]byte, 0, 512)
	announceData = append(announceData, dest.identity.GetPublicKey()...)
	announceData = append(announceData, dest.nameHash...)
	announceData = append(announceData, randomHash...)
	// No ratchet flag added to data if not enabled
	announceData = append(announceData, signature...)
	announceData = append(announceData, appData...)

	p := NewPacket(dest, announceData)
	p.PacketType = PacketAnnounce
	if err := p.Pack(); err != nil {
		t.Fatal(err)
	}

	packetPath := filepath.Join(tmpDir, "announce_packet")
	if err := os.WriteFile(packetPath, p.Raw, 0644); err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, packetPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	// Parse Python output
	lines := strings.Split(string(out), "\n")
	valid := false
	var pyAppData, pyDestHash string
	for _, line := range lines {
		if strings.HasPrefix(line, "Announce Valid: Yes") {
			valid = true
		} else if strings.HasPrefix(line, "AppData: ") {
			pyAppData = strings.TrimPrefix(line, "AppData: ")
		} else if strings.HasPrefix(line, "Destination Hash: ") {
			pyDestHash = strings.TrimPrefix(line, "Destination Hash: ")
		}
	}

	if !valid {
		t.Errorf("Python reported announce packet as INVALID\nOutput: %v", string(out))
	}

	if pyAppData != fmt.Sprintf("%x", appData) {
		t.Errorf("AppData mismatch!\nGo: %x\nPy: %v", appData, pyAppData)
	}

	if pyDestHash != fmt.Sprintf("%x", dest.Hash) {
		t.Errorf("Destination Hash mismatch!\nGo: %x\nPy: %v", dest.Hash, pyDestHash)
	}
}

func TestLinkProofPacketParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_link_proof_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkLinkProofParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	id, err := NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}

	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "parityapp")
	if err != nil {
		t.Fatal(err)
	}

	l, err := NewLink(dest)
	if err != nil {
		t.Fatal(err)
	}
	l.initiator = false // Set as receiver side

	// Manually set linkID as if it came from a request
	l.linkID = []byte("linkid1234567890") // 16 bytes

	// signedData = self.link_id+self.pub_bytes+self.sig_pub_bytes
	signedData := make([]byte, 0, len(l.linkID)+len(l.pubBytes)+len(l.sigPubBytes))
	signedData = append(signedData, l.linkID...)
	signedData = append(signedData, l.pubBytes...)
	signedData = append(signedData, dest.identity.GetPublicKey()[32:64]...) // dest's sig_pub

	signature, err := dest.identity.Sign(signedData)
	if err != nil {
		t.Fatal(err)
	}

	// proofData = signature+self.pub_bytes
	proofData := append(signature, l.pubBytes...)

	p := NewPacket(l, proofData)
	p.PacketType = PacketProof
	p.Context = ContextLrproof
	if err := p.Pack(); err != nil {
		t.Fatal(err)
	}

	packetPath := filepath.Join(tmpDir, "link_proof_packet")
	if err := os.WriteFile(packetPath, p.Raw, 0644); err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, packetPath, fmt.Sprintf("%x", l.linkID), fmt.Sprintf("%x", dest.identity.GetPublicKey()))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	if !strings.Contains(string(out), "Proof Valid: Yes") {
		t.Errorf("Python reported link proof packet as INVALID\nOutput: %v", string(out))
	}
}

func TestLinkRequestPacketParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "go-reticulum-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "check_link_request_parity.py")
	if err := os.WriteFile(scriptPath, []byte(checkLinkRequestParityPy), 0644); err != nil {
		t.Fatal(err)
	}

	id, err := NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}

	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "parityapp")
	if err != nil {
		t.Fatal(err)
	}

	l, err := NewLink(dest)
	if err != nil {
		t.Fatal(err)
	}

	requestData := make([]byte, 0, len(l.pubBytes)+len(l.sigPubBytes))
	requestData = append(requestData, l.pubBytes...)
	requestData = append(requestData, l.sigPubBytes...)

	p := NewPacket(dest, requestData)
	p.PacketType = PacketLinkRequest
	if err := p.Pack(); err != nil {
		t.Fatal(err)
	}

	packetPath := filepath.Join(tmpDir, "link_request_packet")
	if err := os.WriteFile(packetPath, p.Raw, 0644); err != nil {
		t.Fatal(err)
	}

	// Verify with Python
	cmd := exec.Command("python3", scriptPath, packetPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python verification failed: %v\nOutput: %v", err, string(out))
	}

	// Parse Python output
	lines := strings.Split(string(out), "\n")
	var pyX25519, pyEd25519, pyDestHash string
	for _, line := range lines {
		if strings.HasPrefix(line, "X25519 Pub: ") {
			pyX25519 = strings.TrimPrefix(line, "X25519 Pub: ")
		} else if strings.HasPrefix(line, "Ed25519 Pub: ") {
			pyEd25519 = strings.TrimPrefix(line, "Ed25519 Pub: ")
		} else if strings.HasPrefix(line, "Destination Hash: ") {
			pyDestHash = strings.TrimPrefix(line, "Destination Hash: ")
		}
	}

	if pyX25519 != fmt.Sprintf("%x", l.pubBytes) {
		t.Errorf("X25519 Pub mismatch!\nGo: %x\nPy: %v", l.pubBytes, pyX25519)
	}

	if pyEd25519 != fmt.Sprintf("%x", l.sigPubBytes) {
		t.Errorf("Ed25519 Pub mismatch!\nGo: %x\nPy: %v", l.sigPubBytes, pyEd25519)
	}

	if pyDestHash != fmt.Sprintf("%x", dest.Hash) {
		t.Errorf("Destination Hash mismatch!\nGo: %x\nPy: %v", dest.Hash, pyDestHash)
	}
}
