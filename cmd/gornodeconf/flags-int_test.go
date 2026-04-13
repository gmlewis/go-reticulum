// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package main

import (
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
	if strings.TrimSpace(out) != strings.TrimSpace(usageText) {
		t.Fatalf("help output mismatch:\n--- got ---\n%v\n--- want ---\n%v", out, usageText)
	}
}

func TestNoPortPrintsHelpAndExitsZero(t *testing.T) {
	t.Parallel()
	out, err := runGornodeconf()
	if err != nil {
		t.Fatalf("gornodeconf without port failed: %v\n%v", err, out)
	}
	if strings.TrimSpace(out) != strings.TrimSpace(usageText) {
		t.Fatalf("no-port output mismatch:\n--- got ---\n%v\n--- want ---\n%v", out, usageText)
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

func TestParseArgsAcceptsLongAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		check func(*testing.T, options)
	}{
		{
			name: "key",
			args: []string{"--key"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if !opts.key {
					t.Fatal("expected --key to set key option")
				}
			},
		},
		{
			name: "info",
			args: []string{"--info"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if !opts.info {
					t.Fatal("expected --info to set info option")
				}
			},
		},
		{
			name: "wifi",
			args: []string{"--wifi", "AP"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if opts.wifi != "AP" {
					t.Fatalf("wifi mismatch: got %q", opts.wifi)
				}
			},
		},
		{
			name: "display",
			args: []string{"--display", "7"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if opts.display != 7 {
					t.Fatalf("display mismatch: got %v", opts.display)
				}
			},
		},
		{
			name: "timeout",
			args: []string{"--timeout", "9"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if opts.timeout != 9 {
					t.Fatalf("timeout mismatch: got %v", opts.timeout)
				}
			},
		},
		{
			name: "rotation",
			args: []string{"--rotation", "2"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if opts.rotation != 2 {
					t.Fatalf("rotation mismatch: got %v", opts.rotation)
				}
			},
		},
		{
			name: "force-update",
			args: []string{"--force-update"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if !opts.forceUpdate {
					t.Fatal("expected --force-update to set forceUpdate option")
				}
			},
		},
		{
			name: "target-hash",
			args: []string{"--get-target-firmware-hash"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if !opts.getTargetFirmwareHash {
					t.Fatal("expected --get-target-firmware-hash to set getTargetFirmwareHash option")
				}
			},
		},
		{
			name: "firmware-hash",
			args: []string{"--get-firmware-hash"},
			check: func(t *testing.T, opts options) {
				t.Helper()
				if !opts.getFirmwareHash {
					t.Fatal("expected --get-firmware-hash to set getFirmwareHash option")
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			opts, _, err := parseArgs(test.args)
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			test.check(t, opts)
		})
	}
}
