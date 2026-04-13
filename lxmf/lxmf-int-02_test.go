// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

const lxmfDecodeOpportunisticPy = `import LXMF
import sys

def main():
    if len(sys.argv) != 3:
        print("ERROR: missing args")
        sys.exit(1)

    payload_path = sys.argv[1]
    destination_hash_hex = sys.argv[2]

    with open(payload_path, "rb") as f:
        payload = f.read()

    raw = bytes.fromhex(destination_hash_hex) + payload
    message = LXMF.LXMessage.unpack_from_bytes(raw, LXMF.LXMessage.OPPORTUNISTIC)
    print(f"TITLE:{message.title_as_string()}")
    print(f"CONTENT:{message.content_as_string()}")

if __name__ == "__main__":
    main()
`

const lxmfGenerateOpportunisticPy = `import LXMF
import RNS
import sys

def main():
    if len(sys.argv) != 2:
        print("ERROR: missing output path")
        sys.exit(1)

    out_path = sys.argv[1]
    source_identity = RNS.Identity()
    destination_identity = RNS.Identity()

    source_destination = RNS.Destination(source_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "delivery")
    destination_destination = RNS.Destination(destination_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "delivery")

    message = LXMF.LXMessage(
        destination=destination_destination,
        source=source_destination,
        content="py-opportunistic-content",
        title="py-opportunistic-title",
        desired_method=LXMF.LXMessage.OPPORTUNISTIC,
    )
    message.pack()

    with open(out_path, "wb") as f:
        f.write(message.packed[LXMF.LXMessage.DESTINATION_LENGTH:])

    print(f"DESTHASH:{message.destination_hash.hex()}")

if __name__ == "__main__":
    main()
`

func TestIntegrationOpportunisticGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	decodeScriptPath := filepath.Join(tmpDir, "decode_opportunistic.py")
	if err := os.WriteFile(decodeScriptPath, []byte(lxmfDecodeOpportunisticPy), 0o644); err != nil {
		t.Fatalf("write python decode script: %v", err)
	}

	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	ts := rns.NewTransportSystem(nil)
	sourceDest, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destinationDest, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}

	message := mustTestNewMessage(t, destinationDest, sourceDest, "go-opportunistic-content", "go-opportunistic-title", nil)
	message.DesiredMethod = MethodOpportunistic
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	payloadPath := filepath.Join(tmpDir, "go_opportunistic_payload.bin")
	if err := os.WriteFile(payloadPath, message.Packed[DestinationLength:], 0o644); err != nil {
		t.Fatalf("write opportunistic payload: %v", err)
	}

	cmd := exec.Command("python3", decodeScriptPath, payloadPath, hex.EncodeToString(destinationDest.Hash))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python opportunistic decode failed: %v output=%v", err, string(out))
	}

	output := string(out)
	if !strings.Contains(output, "TITLE:go-opportunistic-title") {
		t.Fatalf("python output missing title, output=%v", output)
	}
	if !strings.Contains(output, "CONTENT:go-opportunistic-content") {
		t.Fatalf("python output missing content, output=%v", output)
	}
}

func TestIntegrationOpportunisticPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	generateScriptPath := filepath.Join(tmpDir, "generate_opportunistic.py")
	if err := os.WriteFile(generateScriptPath, []byte(lxmfGenerateOpportunisticPy), 0o644); err != nil {
		t.Fatalf("write python generate script: %v", err)
	}

	payloadPath := filepath.Join(tmpDir, "python_opportunistic_payload.bin")
	cmd := exec.Command("python3", generateScriptPath, payloadPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python opportunistic generation failed: %v output=%v", err, string(out))
	}

	var destinationHashHex string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "DESTHASH:") {
			destinationHashHex = strings.TrimPrefix(line, "DESTHASH:")
			break
		}
	}
	if destinationHashHex == "" {
		t.Fatalf("python output missing destination hash, output=%v", string(out))
	}

	destinationHash, err := hex.DecodeString(destinationHashHex)
	if err != nil {
		t.Fatalf("decode destination hash: %v", err)
	}
	payload, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read python opportunistic payload: %v", err)
	}

	raw := append([]byte{}, destinationHash...)
	raw = append(raw, payload...)

	ts := rns.NewTransportSystem(nil)
	message, err := UnpackMessageFromBytes(ts, raw, MethodOpportunistic)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}

	if got := message.TitleString(); got != "py-opportunistic-title" {
		t.Fatalf("title=%v want=%v", got, "py-opportunistic-title")
	}
	if got := message.ContentString(); got != "py-opportunistic-content" {
		t.Fatalf("content=%v want=%v", got, "py-opportunistic-content")
	}
	if message.Method != MethodOpportunistic {
		t.Fatalf("method=%v want=%v", message.Method, MethodOpportunistic)
	}
}
