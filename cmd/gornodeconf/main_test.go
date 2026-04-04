// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestHelpOutputMatchesPythonSnapshot(t *testing.T) {
	t.Parallel()
	out, err := runGornodeconf("--help")
	if err != nil {
		t.Fatalf("gornodeconf --help failed: %v\n%v", err, out)
	}
	if strings.TrimSpace(out) != strings.TrimSpace(helpText) {
		t.Fatalf("help output mismatch:\n--- got ---\n%v\n--- want ---\n%v", out, helpText)
	}
}

func TestNoPortPrintsHelpAndExitsZero(t *testing.T) {
	t.Parallel()
	out, err := runGornodeconf()
	if err != nil {
		t.Fatalf("gornodeconf without port failed: %v\n%v", err, out)
	}
	if strings.TrimSpace(out) != strings.TrimSpace(helpText) {
		t.Fatalf("no-port output mismatch:\n--- got ---\n%v\n--- want ---\n%v", out, helpText)
	}
}

func TestUnknownFlagPrintsHelpText(t *testing.T) {
	t.Parallel()
	out, err := runGornodeconf("-v")
	if err == nil {
		t.Fatal("expected gornodeconf -v to fail")
	}
	if strings.Contains(out, "Usage of gornodeconf:") {
		t.Fatalf("unexpected foreign usage text: %v", out)
	}
	if !strings.Contains(out, "usage: gornodeconf") {
		t.Fatalf("missing gornodeconf usage text: %v", out)
	}
}

func TestPositionalPortIsAcceptedWithFlags(t *testing.T) {
	t.Parallel()
	out, err := runGornodeconf("-i", tempSerialPort(t))
	if err != nil {
		t.Fatalf("gornodeconf positional port failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "gornodeconf utility started") {
		t.Fatalf("unexpected output: %v", out)
	}
}

func TestVersionUsesSharedGoVersion(t *testing.T) {
	t.Parallel()
	out, err := runGornodeconf("--version")
	if err != nil {
		t.Fatalf("gornodeconf --version failed: %v\n%v", err, out)
	}
	want := "gornodeconf " + rns.VERSION
	if strings.TrimSpace(out) != want {
		t.Fatalf("version output mismatch: got %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestSignWithoutPortAutoDetectsDiscoveredPort(t *testing.T) {
	home := tempTrustKeyHome(t)
	firmwareDir := filepath.Join(home, ".config", "rnodeconf", "firmware")
	if err := os.MkdirAll(firmwareDir, 0o755); err != nil {
		t.Fatalf("mkdir firmware dir: %v", err)
	}
	deviceSigner, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("create device signer: %v", err)
	}
	if err := deviceSigner.ToFile(filepath.Join(firmwareDir, "device.key")); err != nil {
		t.Fatalf("write device.key: %v", err)
	}

	rt := cliRuntime{
		discoverPort: func() (string, []string, error) {
			return "/dev/ttyACM0", []string{"/dev/ttyACM0"}, nil
		},
	}
	serial := &liveSignSerial{reads: append(validRnodeEEPROMFrame(), []byte{
		kissFend, rnodeKISSCommandFWVersion, 0x02, 0x05, kissFend,
		kissFend, rnodeKISSCommandDevHash,
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c,
		0x0d, 0x0e, 0x0f, 0x10,
		0x11, 0x12, 0x13, 0x14,
		0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c,
		0x1d, 0x1e, 0x1f, 0x20, kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x01,
		0xa1, 0xa2, 0xa3, 0xa4,
		0xa5, 0xa6, 0xa7, 0xa8,
		0xa9, 0xaa, 0xab, 0xac,
		0xad, 0xae, 0xaf, 0xb0,
		0xb1, 0xb2, 0xb3, 0xb4,
		0xb5, 0xb6, 0xb7, 0xb8,
		0xb9, 0xba, 0xbb, 0xbc,
		0xbd, 0xbe, 0xbf, kissFesc, kissTfend, kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x02,
		0xc1, 0xc2, 0xc3, 0xc4,
		0xc5, 0xc6, 0xc7, 0xc8,
		0xc9, 0xca, 0xcb, 0xcc,
		0xcd, 0xce, 0xcf, 0xd0,
		0xd1, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8,
		0xd9, 0xda, kissFesc, kissTfesc, 0xdc, 0xdd, 0xde, 0xdf, 0xe0, kissFend,
	}...)}
	rt.openSerial = func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}
	t.Setenv("HOME", home)

	port, err := rt.resolveLivePort("", options{sign: true})
	if err != nil {
		t.Fatalf("resolveLivePort failed: %v", err)
	}
	var out bytes.Buffer
	if err := rt.runDeviceSigning(&out, port); err != nil {
		t.Fatalf("runDeviceSigning failed: %v\n%v", err, out.String())
	}
	if !strings.Contains(out.String(), "Device signed") {
		t.Fatalf("unexpected output: %v", out.String())
	}
}

func TestParseArgsAcceptsPythonStyleLongFlags(t *testing.T) {
	t.Parallel()

	opts, port, err := parseArgs([]string{"--sign", "--firmware-hash", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "ttyUSB0"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !opts.sign {
		t.Fatal("expected --sign to set sign option")
	}
	if opts.firmwareHash != "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" {
		t.Fatalf("firmware hash mismatch: got %q", opts.firmwareHash)
	}
	if port != "ttyUSB0" {
		t.Fatalf("port mismatch: got %q", port)
	}
}

func runGornodeconf(args ...string) (string, error) {
	taskArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", taskArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}
