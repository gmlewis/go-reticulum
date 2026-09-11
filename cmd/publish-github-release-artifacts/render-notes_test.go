// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNestedModuleDir verifies that the release builder detects which
// programs are their own Go modules (their own go.mod) and where those
// modules live, since a nested module is built in its own directory.
func TestNestedModuleDir(t *testing.T) {
	t.Parallel()

	repoRoot := "../../"
	dir, ok := nestedModuleDir(repoRoot, "gorrcd")
	if !ok {
		t.Fatal("nestedModuleDir(gorrcd) = false, want true (cmd/gorrcd/go.mod exists)")
	}
	if want := filepath.Join("cmd", "gorrcd"); !strings.HasSuffix(dir, want) {
		t.Errorf("nestedModuleDir(gorrcd) dir = %q, want it to end with %q", dir, want)
	}
	dir, ok = nestedModuleDir(repoRoot, "golxmd")
	if !ok {
		t.Fatal("nestedModuleDir(golxmd) = false, want true (cmd/golxmd/go.mod exists)")
	}
	if want := filepath.Join("cmd", "golxmd"); !strings.HasSuffix(dir, want) {
		t.Errorf("nestedModuleDir(golxmd) dir = %q, want it to end with %q", dir, want)
	}
	if _, ok := nestedModuleDir(repoRoot, "gornsd"); ok {
		t.Error("nestedModuleDir(gornsd) = true, want false (no nested go.mod)")
	}
	if _, ok := nestedModuleDir(repoRoot, "no-such-program"); ok {
		t.Error("nestedModuleDir(no-such-program) = true, want false")
	}
}

// TestWagoSupportedTarget pins the platform matrix that links the wago
// in-process wasm runtime: Linux, Darwin, or Windows on amd64 or arm64.
func TestWagoSupportedTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		goos, goarch string
		want         bool
	}{
		{"linux", "amd64", true},
		{"linux", "arm64", true},
		{"darwin", "amd64", true},
		{"darwin", "arm64", true},
		{"windows", "amd64", true},
		{"windows", "arm64", true},
		// Everything else keeps the stub: no wago runtime linked.
		{"linux", "arm", false},
		{"linux", "riscv64", false},
		{"freebsd", "amd64", false},
		{"freebsd", "arm64", false},
		{"js", "wasm", false},
		{"plan9", "amd64", false},
	}
	for _, c := range cases {
		if got := wagoSupportedTarget(c.goos, c.goarch); got != c.want {
			t.Errorf("wagoSupportedTarget(%q, %q) = %v, want %v", c.goos, c.goarch, got, c.want)
		}
	}
}

// TestBuildTagsWithWago verifies the tag merge: wago is appended only when
// missing, and the pocket tags stay intact.
func TestBuildTagsWithWago(t *testing.T) {
	t.Parallel()

	cases := []struct{ tags, want string }{
		{"", "wago"},
		{"pocket_terminal", "pocket_terminal,wago"},
		{"pocket_terminal,wago", "pocket_terminal,wago"},
		{"wago", "wago"},
	}
	for _, c := range cases {
		if got := buildTagsWithWago(c.tags); got != c.want {
			t.Errorf("buildTagsWithWago(%q) = %q, want %q", c.tags, got, c.want)
		}
	}
}

// TestPlatformBlacklist guards the set of programs we refuse to ship on
// platforms where they compile but misbehave at runtime. If you intentionally
// add or remove an entry, update this test alongside platformBlacklist.
func TestPlatformBlacklist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		goos, binary string
		want         bool
	}{
		// Non-functional on Windows: gornodeconf (serial/flash/EEPROM all
		// return "not supported") and gornsh (PTY unsupported, /bin/sh shell).
		{"windows", "gornodeconf", true},
		{"windows", "gornsh", true},
		// Same programs ship fine on Unix where the platform code exists.
		{"linux", "gornodeconf", false},
		{"linux", "gornsh", false},
		{"darwin", "gornsh", false},
		// Unrelated programs are never blacklisted.
		{"windows", "gornsd", false},
		{"windows", "golxmd", false},
	}
	for _, c := range cases {
		if got := blacklisted(c.goos, c.binary); got != c.want {
			t.Errorf("blacklisted(%q, %q) = %v, want %v", c.goos, c.binary, got, c.want)
		}
	}
}

// TestRenderReleaseNotes renders the release notes for a handful of fake,
// deliberately-unsorted artifacts and logs them so you can eyeball the sorted
// table and the post-download setup section. Run with:
//
//	GOCACHE=/tmp/go-cache go test ./cmd/publish-github-release-artifacts/ -run TestRenderReleaseNotes -v
func TestRenderReleaseNotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Build the assets in a deliberately non-alphabetical order so the test
	// proves buildReleaseNotes sorts the table rows by filename.
	names := []string{
		"gorrcd-0.2.0-windows-arm64.exe",
		"gorrcd-0.2.0-linux-amd64",
		"gorrcd-0.2.0-darwin-arm64",
		"gorrcd-0.2.0-linux-arm64",
		"gorrcd-0.2.0-pocket_terminal-linux-arm64",
		"gornsd-0.2.0-pocket_communicator-linux-arm64",
		"gorrcd-0.2.0-pocket_hub-linux-arm64",
	}
	var assets []string
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
			t.Fatalf("write %v: %v", n, err)
		}
		assets = append(assets, p)
	}

	notes := buildReleaseNotes("0.2.0", "gmlewis/go-reticulum", assets)
	t.Log("\n" + notes)

	if !strings.Contains(notes, "Hardware Projects & Pre-built Artifacts") {
		t.Error("release notes missing Hardware Projects section")
	}
	if !strings.Contains(notes, "pocket_terminal") {
		t.Error("release notes missing pocket_terminal reference")
	}
	if !strings.Contains(notes, "pocket_communicator") {
		t.Error("release notes missing pocket_communicator reference")
	}
	if !strings.Contains(notes, "pocket_hub") {
		t.Error("release notes missing pocket_hub reference")
	}
}
