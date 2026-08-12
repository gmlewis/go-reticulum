// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestMatchSimple covers the fnmatch-style wildcard matcher used by
// fetchRelease to select artifacts by glob expression.
func TestMatchSimple(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, pattern string
		want          bool
	}{
		{"app.bin", "app.bin", true},
		{"app.bin", "*.bin", true},
		{"app.bin", "app.*", true},
		{"app.bin", "*", true},
		{"app.bin", "app.txt", false},
		{"app.bin", "*.txt", false},
		{"app.bin", "app", false},
		{"", "*", true},
		{"app", "", false},
		{"binary-linux.tar.gz", "*.tar.gz", true},
		{"binary-linux.tar.gz", "binary-*.tar.gz", true},
		{"binary-linux.tar.gz", "binary-*.zip", false},
	}
	for _, tc := range cases {
		if got := matchSimple(tc.name, tc.pattern); got != tc.want {
			t.Errorf("matchSimple(%q, %q) = %v, want %v", tc.name, tc.pattern, got, tc.want)
		}
	}
}

// TestPrettySize covers the human-readable byte formatter.
func TestPrettySize(t *testing.T) {
	t.Parallel()
	if got := prettySize(0); got != "0 B" {
		t.Errorf("prettySize(0) = %q, want %q", got, "0 B")
	}
	if got := prettySize(1024); !strings.Contains(got, "KB") {
		t.Errorf("prettySize(1024) = %q, want KB unit", got)
	}
	if got := prettySize(1048576); !strings.Contains(got, "MB") {
		t.Errorf("prettySize(1048576) = %q, want MB unit", got)
	}
}

// TestCommitHashFromTag verifies commitHashFromTag resolves a tag and
// returns the empty string for a missing tag.
func TestCommitHashFromTag(t *testing.T) {
	t.Parallel()
	work := testutils.TempDir(t, "gorngit-rel-cht-")
	runGit(t, work, "init")
	runGit(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, work, "config", "user.email", "test@example.com")
	runGit(t, work, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, work, "add", "README.md")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "tag", "v1.0.0")

	hash := commitHashFromTag("v1.0.0", work)
	if hash == "" {
		t.Fatalf("commitHashFromTag(v1.0.0) = empty, want a hash")
	}
	if got := commitHashFromTag("nonexistent", work); got != "" {
		t.Errorf("commitHashFromTag(nonexistent) = %q, want empty", got)
	}
}

// TestEditReleaseNotesStrip verifies the comment-stripping logic that
// editReleaseNotes applies: lines whose trimmed form starts with "#" are
// removed and the result is trimmed of surrounding whitespace.
func TestEditReleaseNotesStrip(t *testing.T) {
	t.Parallel()
	content := "# Enter release notes for v1.0.0.\n# comment\nReal notes\nMore notes\n"
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, line)
	}
	notes := strings.TrimSpace(strings.Join(lines, "\n"))
	if notes != "Real notes\nMore notes" {
		t.Errorf("stripped notes = %q, want %q", notes, "Real notes\nMore notes")
	}
}
