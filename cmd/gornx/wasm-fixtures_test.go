// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the embedded wasm test fixtures for the gornx command
// plugin host. The modules are minimal hand-assembled bytecode so the tests
// never need an external wasm compiler; the wago compiler validates the
// section sizes the first time each fixture is compiled.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// writeFixtureFile writes fixture bytes under dir with the given plugin file
// name (which also names the command it serves) and returns the path.
func writeFixtureFile(t *testing.T, dir, name string, wasm []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, wasm, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// MinWasmEchoCommandPlugin is the echo command plugin (mirroring the gorrcd
// fixture): exports wagoplugin_alloc and handle_command over one memory
// page; handle_command echoes its request bytes back, so a plugin-backed
// command's stdout is the serialized request.
var MinWasmEchoCommandPlugin = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// Type section: 0: () -> (), 1: (i32) -> (i32), 2: (i32, i32) -> (i32, i32), 3: () -> (i32, i32)
	0x01, 0x15, 0x04,
	0x60, 0x00, 0x00,
	0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x02, 0x7f, 0x7f,
	0x60, 0x00, 0x02, 0x7f, 0x7f,
	// Function section: 0: alloc, 1: manifest, 2: handle_command
	0x03, 0x04, 0x03, 0x01, 0x03, 0x02,
	// Memory section: 1 page, max 1 page
	0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
	// Export section (size 0x40 = 1 + 9 + 19 + 18 + 17)
	0x07, 0x40, 0x04,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x10, 'w', 'a', 'g', 'o', 'p', 'l', 'u', 'g', 'i', 'n', '_', 'a', 'l', 'l', 'o', 'c', 0x00, 0x00,
	0x0f, 'p', 'l', 'u', 'g', 'i', 'n', '_', 'm', 'a', 'n', 'i', 'f', 'e', 's', 't', 0x00, 0x01,
	0x0e, 'h', 'a', 'n', 'd', 'l', 'e', '_', 'c', 'o', 'm', 'm', 'a', 'n', 'd', 0x00, 0x02,
	// Code section (size 0x21 = 1 + 6 + 7 + 19)
	0x0a, 0x21, 0x03,
	// func 0 (alloc): returns the fixed offset 1024 (body: 00 41 80 08 0b)
	0x05, 0x00, 0x41, 0x80, 0x08, 0x0b,
	// func 1 (manifest): returns (offset 0, len 32) (body: 00 41 00 41 20 0b)
	0x06, 0x00, 0x41, 0x00, 0x41, 0x20, 0x0b,
	// func 2 (handle_command): memory.copy input to 1024, returns (1024, len)
	0x12, 0x00, 0x41, 0x80, 0x08, 0x20, 0x00, 0x20, 0x01, 0xfc, 0x0a, 0x00, 0x00, 0x41, 0x80, 0x08, 0x20, 0x01, 0x0b,
}

// MinWasmSpin is the infinite-loop module used for timeout and cancellation
// tests:
//
//	(module (func (export "spin") loop br 0 end)).
var MinWasmSpin = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x08, 0x01, 0x04, 's', 'p', 'i', 'n', 0x00, 0x00,
	// Code section: body size 7 (locals 00, loop 03 40, br 0c 00, end 0b, end 0b), section size 9.
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b,
}

// TestResolveRnxPluginsDir verifies the plugins-directory resolution order
// mirrors the allowed-identities candidates (/etc/rnx, ~/.config/rnx,
// ~/.rnx) with /plugins appended; a missing candidate is skipped.
func TestResolveRnxPluginsDir(t *testing.T) {
	t.Parallel()

	home := testutils.TempDir(t, "gornx-plugins")
	configRnx := filepath.Join(home, ".config", "rnx")
	if err := os.MkdirAll(filepath.Join(configRnx, "plugins"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got := resolveRnxPluginsDir(home)
	if want := filepath.Join(configRnx, "plugins"); got != want {
		t.Errorf("resolveRnxPluginsDir = %q, want %q", got, want)
	}

	// No rnx directories at all: no plugin dir.
	empty := testutils.TempDir(t, "gornx-plugins")
	if got := resolveRnxPluginsDir(empty); got != "" {
		t.Errorf("resolveRnxPluginsDir with no rnx dirs = %q, want empty", got)
	}
}
