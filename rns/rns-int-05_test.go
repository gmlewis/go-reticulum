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
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	vendoredbzip2 "github.com/gmlewis/go-reticulum/compress/bzip2"
)

func runPythonBufferTransform(t *testing.T, script string, args ...string) string {
	t.Helper()

	cmd := exec.Command("python3", append([]string{"-c", script}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python buffer transform failed: %v\noutput: %v", err, string(out))
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		t.Fatal("python buffer transform returned empty output")
	}
	return result
}

func TestStreamDataMessageCompressedGoToPythonParity(t *testing.T) {
	plaintext := bytes.Repeat([]byte("stream-parity-go-to-python-"), 128)
	compressed, err := CompressBzip2(plaintext, vendoredbzip2.DefaultCompression)
	if err != nil {
		t.Fatalf("CompressBzip2 error: %v", err)
	}

	wire, err := (&StreamDataMessage{StreamID: 77, Data: compressed, Compressed: true, EOF: true}).Pack()
	if err != nil {
		t.Fatalf("pack error: %v", err)
	}

	pythonScript := `import binascii, sys
from RNS.Buffer import StreamDataMessage
raw = binascii.unhexlify(sys.argv[1])
m = StreamDataMessage()
m.unpack(raw)
print(f"{m.stream_id}:{1 if m.compressed else 0}:{1 if m.eof else 0}:{binascii.hexlify(m.data).decode()}")`

	out := runPythonBufferTransform(t, pythonScript, hex.EncodeToString(wire))
	parts := strings.SplitN(out, ":", 4)
	if len(parts) != 4 {
		t.Fatalf("unexpected python output: %q", out)
	}

	streamID, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("invalid python stream id %q: %v", parts[0], err)
	}
	if streamID != 77 {
		t.Fatalf("stream id mismatch: got %v want %v", streamID, 77)
	}
	if parts[1] != "1" {
		t.Fatalf("expected compressed flag from python unpack, got %q", parts[1])
	}
	if parts[2] != "1" {
		t.Fatalf("expected eof flag from python unpack, got %q", parts[2])
	}

	decoded, err := hex.DecodeString(parts[3])
	if err != nil {
		t.Fatalf("failed to decode python payload hex: %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("go->python stream payload mismatch")
	}
}

func TestStreamDataMessageCompressedPythonToGoParity(t *testing.T) {
	plaintext := bytes.Repeat([]byte{0x00, 0x01, 0xFE, 0xFF, 0x20, 0x30, 0x40}, 256)
	plaintextHex := hex.EncodeToString(plaintext)

	pythonScript := `import binascii, bz2, sys
from RNS.Buffer import StreamDataMessage
plain = binascii.unhexlify(sys.argv[1])
m = StreamDataMessage(stream_id=91, data=bz2.compress(plain), eof=False, compressed=True)
print(binascii.hexlify(m.pack()).decode())`

	wireHex := runPythonBufferTransform(t, pythonScript, plaintextHex)
	wire, err := hex.DecodeString(wireHex)
	if err != nil {
		t.Fatalf("failed to decode python wire hex: %v", err)
	}

	var msg StreamDataMessage
	if err := msg.Unpack(wire); err != nil {
		t.Fatalf("go unpack error: %v", err)
	}

	if msg.StreamID != 91 {
		t.Fatalf("stream id mismatch: got %v want %v", msg.StreamID, 91)
	}
	if !msg.Compressed {
		t.Fatal("expected compressed flag to be set")
	}
	if msg.EOF {
		t.Fatal("expected eof flag to be unset")
	}
	if !bytes.Equal(msg.Data, plaintext) {
		t.Fatalf("python->go stream payload mismatch")
	}
}
