// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

const lxmfDecodeDirectPy = `import LXMF
import sys

def main():
    if len(sys.argv) != 2:
        print("ERROR: missing input path")
        sys.exit(1)

    in_path = sys.argv[1]
    with open(in_path, "rb") as f:
        raw = f.read()

    message = LXMF.LXMessage.unpack_from_bytes(raw, LXMF.LXMessage.DIRECT)
    print(f"TITLE:{message.title_as_string()}")
    print(f"CONTENT:{message.content_as_string()}")

if __name__ == "__main__":
    main()
`

const lxmfGenerateDirectPy = `import LXMF
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
        content="py-direct-content",
        title="py-direct-title",
        desired_method=LXMF.LXMessage.DIRECT,
    )
    message.pack()

    with open(out_path, "wb") as f:
        f.write(message.packed)

    print("TITLE:py-direct-title")
    print("CONTENT:py-direct-content")

if __name__ == "__main__":
    main()
`

func requirePythonInteropPaths(t *testing.T) (string, string) {
	t.Helper()
	lxmfPath := os.Getenv("ORIGINAL_LXMF_REPO_DIR")
	if lxmfPath == "" {
		t.Fatal("missing required environment variable ORIGINAL_LXMF_REPO_DIR (set by scripts/test-integration.sh)")
	}
	reticulumPath := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if reticulumPath == "" {
		t.Fatal("missing required environment variable ORIGINAL_RETICULUM_REPO_DIR (set by scripts/test-integration.sh)")
	}
	return lxmfPath, reticulumPath
}

func pythonPathEnv(lxmfPath, reticulumPath string) string {
	if lxmfPath == reticulumPath {
		return lxmfPath
	}
	return lxmfPath + string(os.PathListSeparator) + reticulumPath
}

func TestIntegrationDirectGoToPython(t *testing.T) {
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir := tempDir(t)
	decodeScriptPath := filepath.Join(tmpDir, "decode_direct.py")
	if err := os.WriteFile(decodeScriptPath, []byte(lxmfDecodeDirectPy), 0o644); err != nil {
		t.Fatalf("write python decode script: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destinationID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destinationDest, err := rns.NewDestination(destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}

	message, err := NewMessage(destinationDest, sourceDest, "go-direct-content", "go-direct-title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	packedPath := filepath.Join(tmpDir, "go_direct_message.bin")
	if err := os.WriteFile(packedPath, message.Packed, 0o644); err != nil {
		t.Fatalf("write packed message: %v", err)
	}

	cmd := exec.Command("python3", decodeScriptPath, packedPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python decode failed: %v output=%v", err, string(out))
	}

	output := string(out)
	if !strings.Contains(output, "TITLE:go-direct-title") {
		t.Fatalf("python output missing title, output=%v", output)
	}
	if !strings.Contains(output, "CONTENT:go-direct-content") {
		t.Fatalf("python output missing content, output=%v", output)
	}
}

func TestIntegrationDirectPythonToGo(t *testing.T) {
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir := tempDir(t)
	generateScriptPath := filepath.Join(tmpDir, "generate_direct.py")
	if err := os.WriteFile(generateScriptPath, []byte(lxmfGenerateDirectPy), 0o644); err != nil {
		t.Fatalf("write python generate script: %v", err)
	}

	packedPath := filepath.Join(tmpDir, "python_direct_message.bin")
	cmd := exec.Command("python3", generateScriptPath, packedPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python generation failed: %v output=%v", err, string(out))
	}

	packed, err := os.ReadFile(packedPath)
	if err != nil {
		t.Fatalf("read python packed message: %v", err)
	}

	message, err := UnpackMessageFromBytes(packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}

	if got := message.TitleString(); got != "py-direct-title" {
		t.Fatalf("title=%v want=%v", got, "py-direct-title")
	}
	if got := message.ContentString(); got != "py-direct-content" {
		t.Fatalf("content=%v want=%v", got, "py-direct-content")
	}
	if message.Method != MethodDirect {
		t.Fatalf("method=%v want=%v", message.Method, MethodDirect)
	}
}
