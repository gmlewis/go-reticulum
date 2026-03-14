// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const lxmfDecodeResourcePy = `import LXMF
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
    print(f"CONTENTLEN:{len(message.content)}")

if __name__ == "__main__":
    main()
`

const lxmfGenerateResourcePy = `import LXMF
import RNS
import sys

def main():
    if len(sys.argv) != 3:
        print("ERROR: missing args")
        sys.exit(1)

    out_path = sys.argv[1]
    content_len = int(sys.argv[2])
    source_identity = RNS.Identity()
    destination_identity = RNS.Identity()

    source_destination = RNS.Destination(source_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "delivery")
    destination_destination = RNS.Destination(destination_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "delivery")

    message = LXMF.LXMessage(
        destination=destination_destination,
        source=source_destination,
        content=("R"*content_len),
        title="py-resource-title",
        desired_method=LXMF.LXMessage.DIRECT,
    )
    message.pack()

    with open(out_path, "wb") as f:
        f.write(message.packed)

    print(f"REPRESENTATION:{message.representation}")
    print(f"CONTENTLEN:{len(message.content)}")

if __name__ == "__main__":
    main()
`

func TestIntegrationResourceGoToPython(t *testing.T) {
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir := t.TempDir()
	decodeScriptPath := filepath.Join(tmpDir, "decode_resource.py")
	if err := os.WriteFile(decodeScriptPath, []byte(lxmfDecodeResourcePy), 0o644); err != nil {
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

	contentLen := rns.MDU * 3
	message, err := NewMessage(destinationDest, sourceDest, strings.Repeat("G", contentLen), "go-resource-title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.now = func() time.Time { return time.Unix(1700000000, 0) }

	var capturedPacked []byte
	router.sendResource = func(msg *Message) error {
		capturedPacked = append([]byte{}, msg.Packed...)
		return nil
	}

	if err := router.HandleOutbound(message); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if message.Representation != RepresentationResource {
		t.Fatalf("representation=%v want=%v", message.Representation, RepresentationResource)
	}
	if len(capturedPacked) == 0 {
		t.Fatal("expected packed bytes captured from resource send path")
	}

	packedPath := filepath.Join(tmpDir, "go_resource_message.bin")
	if err := os.WriteFile(packedPath, capturedPacked, 0o644); err != nil {
		t.Fatalf("write packed message: %v", err)
	}

	cmd := exec.Command("python3", decodeScriptPath, packedPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python decode failed: %v output=%v", err, string(out))
	}

	output := string(out)
	if !strings.Contains(output, "TITLE:go-resource-title") {
		t.Fatalf("python output missing title, output=%v", output)
	}
	if !strings.Contains(output, fmt.Sprintf("CONTENTLEN:%v", contentLen)) {
		t.Fatalf("python output missing content length, output=%v", output)
	}
}

func TestIntegrationResourcePythonToGo(t *testing.T) {
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir := t.TempDir()
	generateScriptPath := filepath.Join(tmpDir, "generate_resource.py")
	if err := os.WriteFile(generateScriptPath, []byte(lxmfGenerateResourcePy), 0o644); err != nil {
		t.Fatalf("write python generate script: %v", err)
	}

	contentLen := rns.MDU * 3
	packedPath := filepath.Join(tmpDir, "python_resource_message.bin")
	cmd := exec.Command("python3", generateScriptPath, packedPath, strconv.Itoa(contentLen))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python generation failed: %v output=%v", err, string(out))
	}

	output := string(out)
	if !strings.Contains(output, fmt.Sprintf("REPRESENTATION:%v", RepresentationResource)) {
		t.Fatalf("python output missing resource representation, output=%v", output)
	}

	packed, err := os.ReadFile(packedPath)
	if err != nil {
		t.Fatalf("read python packed message: %v", err)
	}

	message, err := UnpackMessageFromBytes(packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}

	if got := message.TitleString(); got != "py-resource-title" {
		t.Fatalf("title=%v want=%v", got, "py-resource-title")
	}
	if got := len(message.Content); got != contentLen {
		t.Fatalf("content length=%v want=%v", got, contentLen)
	}
	if message.Method != MethodDirect {
		t.Fatalf("method=%v want=%v", message.Method, MethodDirect)
	}
}
