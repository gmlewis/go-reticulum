// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package pluginstore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "pluginstore-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestNewValidatesPluginName verifies the plugin-name sanitation: only safe
// name characters are accepted, path separators and traversal are rejected,
// and the scoped directory is created.
func TestNewValidatesPluginName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plugin  string
		wantErr bool
	}{
		{"simple", "echo", false},
		{"dots and dashes", "my-plugin.v2", false},
		{"underscore", "my_plugin", false},
		{"empty", "", true},
		{"dot", ".", true},
		{"dotdot", "..", true},
		{"slash", "a/b", true},
		{"backslash", `a\b`, true},
		{"traversal", "a/../b", true},
		{"nul", "a\x00b", true},
		{"space", "my plugin", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := filepath.Join(tempDir(t), "data")
			s, err := New(base, tt.plugin)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New(%q) succeeded, want an error", tt.plugin)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q): %v", tt.plugin, err)
			}
			info, err := os.Stat(s.Dir())
			if err != nil || !info.IsDir() {
				t.Fatalf("scoped dir %q missing: %v", s.Dir(), err)
			}
			if filepath.Base(s.Dir()) != tt.plugin {
				t.Errorf("scoped dir = %q, want it to end with %q", s.Dir(), tt.plugin)
			}
		})
	}
}

// TestSetGetRoundTrip verifies the basic store/load cycle.
func TestSetGetRoundTrip(t *testing.T) {
	t.Parallel()

	base := filepath.Join(tempDir(t), "data")
	s, err := New(base, "echo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Set("greeting", []byte("hello world")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := s.Get("greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || string(got) != "hello world" {
		t.Fatalf("Get(greeting) = (%q, %v), want (hello world, true)", got, ok)
	}

	// Overwrite semantics.
	if err := s.Set("greeting", []byte("hi")); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	got, ok, err = s.Get("greeting")
	if err != nil || !ok || string(got) != "hi" {
		t.Fatalf("Get after overwrite = (%q, %v, %v), want (hi, true, nil)", got, ok, err)
	}
}

// TestGetMissing verifies that a missing key reports found=false with no
// error.
func TestGetMissing(t *testing.T) {
	t.Parallel()

	base := filepath.Join(tempDir(t), "data")
	s, err := New(base, "echo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, ok, err := s.Get("nope")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok || len(got) != 0 {
		t.Fatalf("Get(missing) = (%q, %v), want (empty, false)", got, ok)
	}
}

// TestSetRejectsBadKeys verifies key sanitation: separators, traversal, and
// oversized keys are rejected.
func TestSetRejectsBadKeys(t *testing.T) {
	t.Parallel()

	base := filepath.Join(tempDir(t), "data")
	s, err := New(base, "echo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, key := range []string{"", "..", ".", "a/b", `a\b`, "a\x00b", strings.Repeat("k", 129)} {
		if err := s.Set(key, []byte("v")); err == nil {
			t.Errorf("Set(%q) succeeded, want an error", key)
		}
	}
}

// TestQuotaExceeded verifies the per-plugin total-size cap.
func TestQuotaExceeded(t *testing.T) {
	t.Parallel()

	base := filepath.Join(tempDir(t), "data")
	s, err := New(base, "echo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.quota = 100

	if err := s.Set("a", make([]byte, 60)); err != nil {
		t.Fatalf("Set(a): %v", err)
	}
	if err := s.Set("b", make([]byte, 60)); err == nil {
		t.Fatal("Set(b) succeeded past the quota, want an error")
	}
	// The first entry is untouched by the failed write.
	got, ok, err := s.Get("a")
	if err != nil || !ok || len(got) != 60 {
		t.Fatalf("Get(a) after failed Set = (%v, %v, %v), want the original", got, ok, err)
	}
}

// TestQuotaAccountsOverwrite verifies that overwriting a key does not double
// count its old bytes.
func TestQuotaAccountsOverwrite(t *testing.T) {
	t.Parallel()

	base := filepath.Join(tempDir(t), "data")
	s, err := New(base, "echo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.quota = 100

	if err := s.Set("a", make([]byte, 80)); err != nil {
		t.Fatalf("Set(a): %v", err)
	}
	// 80 old bytes are replaced, not added: 80 fits again.
	if err := s.Set("a", make([]byte, 80)); err != nil {
		t.Fatalf("Set(a) overwrite: %v", err)
	}
	// 80 used; a 21-byte write would exceed 100.
	if err := s.Set("b", make([]byte, 21)); err == nil {
		t.Fatal("Set(b) succeeded past the quota, want an error")
	}
	if err := s.Set("b", make([]byte, 20)); err != nil {
		t.Fatalf("Set(b) at the limit: %v", err)
	}
}

// TestDefaultQuota verifies that a fresh store carries the documented 10 MiB
// quota.
func TestDefaultQuota(t *testing.T) {
	t.Parallel()

	base := filepath.Join(tempDir(t), "data")
	s, err := New(base, "echo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.quota != MaxStoreBytes {
		t.Errorf("default quota = %v, want %v", s.quota, MaxStoreBytes)
	}
	if MaxStoreBytes != 10<<20 {
		t.Errorf("MaxStoreBytes = %v, want 10 MiB", MaxStoreBytes)
	}
}
