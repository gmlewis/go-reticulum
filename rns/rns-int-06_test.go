// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	vendoredbzip2 "github.com/gmlewis/go-reticulum/compress/bzip2"
)

func runPythonHexTransform(t *testing.T, script string, inputHex string) string {
	t.Helper()

	cmd := exec.Command("python3", "-c", script, inputHex)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python transform failed: %v\noutput: %v", err, string(out))
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		t.Fatal("python transform returned empty output")
	}
	return result
}

func TestBzip2ParityGoToPython(t *testing.T) {
	payload := bytes.Repeat([]byte("go-reticulum-bzip2-parity-"), 256)

	compressed, err := CompressBzip2(payload, vendoredbzip2.DefaultCompression)
	if err != nil {
		t.Fatalf("CompressBzip2 error: %v", err)
	}

	compressedHex := hex.EncodeToString(compressed)
	pythonScript := "import bz2,binascii,sys;data=binascii.unhexlify(sys.argv[1]);print(binascii.hexlify(bz2.decompress(data)).decode())"
	decompressedHex := runPythonHexTransform(t, pythonScript, compressedHex)

	decompressed, err := hex.DecodeString(decompressedHex)
	if err != nil {
		t.Fatalf("failed to decode python decompressed hex: %v", err)
	}

	if !bytes.Equal(decompressed, payload) {
		t.Fatalf("go->python bzip2 parity mismatch")
	}
}

func TestBzip2ParityPythonToGo(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x01, 0x02, 0xFE, 0xFF, 0x10, 0x20, 0x30}, 512)
	payloadHex := hex.EncodeToString(payload)

	pythonScript := "import bz2,binascii,sys;data=binascii.unhexlify(sys.argv[1]);print(binascii.hexlify(bz2.compress(data)).decode())"
	compressedHex := runPythonHexTransform(t, pythonScript, payloadHex)

	compressed, err := hex.DecodeString(compressedHex)
	if err != nil {
		t.Fatalf("failed to decode python compressed hex: %v", err)
	}

	decompressed, err := DecompressBzip2(compressed)
	if err != nil {
		t.Fatalf("DecompressBzip2 error: %v", err)
	}

	if !bytes.Equal(decompressed, payload) {
		t.Fatalf("python->go bzip2 parity mismatch: got %v bytes, want %v", len(decompressed), len(payload))
	}
}

func TestBzip2ParityRejectsInvalidPayload(t *testing.T) {
	invalid := []byte("not-a-bzip2-stream")
	_, err := DecompressBzip2(invalid)
	if err == nil {
		t.Fatal("expected invalid bzip2 payload to fail decompression")
	}
	if !strings.Contains(strings.ToLower(fmt.Sprint(err)), "bzip2") {
		t.Fatalf("expected bzip2-related error, got: %v", err)
	}
}
