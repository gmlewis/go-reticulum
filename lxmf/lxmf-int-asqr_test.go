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
	"strconv"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/qr"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// lxmfAsQRParityPy generates a QR from a URI read from a file using Python's
// qrcode library with the same settings Python's LXMessage.as_qr uses
// (error_correction=ERROR_CORRECT_L, border=1, box_size=10) and prints the
// module count, QR version, and rendered image pixel dimensions. The Go side
// encodes the identical URI with the vendored rsc.io/qr encoder and compares.
const lxmfAsQRParityPy = `import sys
import qrcode

def main():
    if len(sys.argv) != 2:
        print("ERROR: missing uri file arg")
        sys.exit(1)
    with open(sys.argv[1], "r") as f:
        uri = f.read()
    qr = qrcode.QRCode(
        error_correction=qrcode.constants.ERROR_CORRECT_L,
        border=1,
        box_size=10,
    )
    qr.add_data(uri)
    qr.make(fit=True)
    img = qr.make_image()
    print("MODULES:%d" % qr.modules_count)
    print("VERSION:%d" % qr.version)
    print("PIXEL_W:%d" % img.size[0])
    print("PIXEL_H:%d" % img.size[1])

if __name__ == "__main__":
    main()
`

// TestIntegrationAsQRPythonMatrixParity verifies the Go as_qr / vendored QR
// encoder produces a matrix of the same version/module-count and the same
// rendered pixel dimensions as Python's qrcode library for the identical
// paper-message URI. Because Identity.encrypt uses a fresh ephemeral X25519
// key per call, the URI itself is non-deterministic across runs, so we capture
// the Go-generated URI at runtime and feed the exact same string to Python.
// Both encoders use byte mode for base64url data at ECL L, so the QR version
// (and thus module count) is determined solely by the URI length and must
// match. This is the cross-process parity gate for Phase L.2.
func TestIntegrationAsQRPythonMatrixParity(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	// Build a packed paper-method message and capture its URI.
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	m := mustTestNewMessage(t, destination, source, "paper parity content", "paper parity title", nil)
	m.DesiredMethod = MethodPaper
	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	uri, err := m.AsURI(false)
	if err != nil {
		t.Fatalf("AsURI(false): %v", err)
	}

	tmpDir := testutils.TempDir(t, tempDirPrefix)
	scriptPath := filepath.Join(tmpDir, "asqr_parity.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfAsQRParityPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}
	uriPath := filepath.Join(tmpDir, "uri.txt")
	if err := os.WriteFile(uriPath, []byte(uri), 0o644); err != nil {
		t.Fatalf("write uri file: %v", err)
	}

	cmd := exec.Command("python3", scriptPath, uriPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python asqr parity script failed: %v\n%s", err, out)
	}

	pyModules, pyVersion, pyPixelW, pyPixelH := parseAsQRParityOut(t, string(out))

	// Go vendored encoder matrix for the identical URI.
	goCode, err := qr.Encode(uri, qr.L)
	if err != nil {
		t.Fatalf("qr.Encode: %v", err)
	}
	if goCode.Size != pyModules {
		t.Errorf("Go QR module count=%d, Python qrcode modules_count=%d (uri len=%d)", goCode.Size, pyModules, len(uri))
	}
	if pyVersion < 1 {
		t.Errorf("Python QR version=%d, want >=1", pyVersion)
	}

	// Go AsQR image pixel dimensions must match Python's PIL image (both use
	// border=1 and box_size=10).
	img, err := m.AsQR()
	if err != nil {
		t.Fatalf("AsQR: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != pyPixelW || bounds.Dy() != pyPixelH {
		t.Errorf("Go AsQR image=%dx%d, Python PIL=%dx%d", bounds.Dx(), bounds.Dy(), pyPixelW, pyPixelH)
	}

	// Sanity: both encoders agree the URI fits in a single QR (<= QRMaxStore).
	if len(uri) > QRMaxStore {
		t.Errorf("uri length %d exceeds QRMaxStore %d", len(uri), QRMaxStore)
	}
}

func parseAsQRParityOut(t *testing.T, out string) (modules, version, pixelW, pixelH int) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "MODULES:"):
			modules = atoiOrFatal(t, line, "MODULES:")
		case strings.HasPrefix(line, "VERSION:"):
			version = atoiOrFatal(t, line, "VERSION:")
		case strings.HasPrefix(line, "PIXEL_W:"):
			pixelW = atoiOrFatal(t, line, "PIXEL_W:")
		case strings.HasPrefix(line, "PIXEL_H:"):
			pixelH = atoiOrFatal(t, line, "PIXEL_H:")
		}
	}
	if modules == 0 || pixelW == 0 || pixelH == 0 {
		t.Fatalf("incomplete python output:\n%s", out)
	}
	return modules, version, pixelW, pixelH
}

func atoiOrFatal(t *testing.T, line, prefix string) int {
	t.Helper()
	v, err := strconv.Atoi(strings.TrimPrefix(line, prefix))
	if err != nil {
		t.Fatalf("parse %q: %v", line, err)
	}
	return v
}
