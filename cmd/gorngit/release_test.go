// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newTestNode returns a minimal reticulumGitNode for unit-testing the
// release sub-operations, which only touch the filesystem (releasesPath)
// and do not require a running RNS stack.
func newTestNode(t *testing.T) *reticulumGitNode {
	t.Helper()
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	return &reticulumGitNode{identity: id}
}

// seedBareRepoWithTag creates a bare git repo at repoPath with one commit
// and one lightweight tag named tag. It returns the commit SHA.
func seedBareRepoWithTag(t *testing.T, repoPath, tag string) string {
	t.Helper()
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("MkdirAll repo: %v", err)
	}
	runGit(t, repoPath, "init", "--bare")
	runGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")

	work := testutils.TempDir(t, "gorngit-rel-work-")
	runGit(t, work, "init")
	runGit(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# rel\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, work, "add", "README.md")
	runGit(t, work, "config", "user.email", "test@example.com")
	runGit(t, work, "config", "user.name", "Test")
	runGit(t, work, "commit", "-m", "initial")
	sha := strings.TrimSpace(runGit(t, work, "rev-parse", "refs/heads/main"))
	runGit(t, work, "tag", tag)
	runGit(t, work, "push", repoPath, "refs/heads/main", "refs/tags/"+tag)
	return sha
}

// writeReleaseMETAFile writes a flat META file with the given keys.
func writeReleaseMETAFile(t *testing.T, path string, meta map[string]string) {
	t.Helper()
	var sb strings.Builder
	for k, v := range meta {
		sb.WriteString(k)
		sb.WriteString(" = ")
		sb.WriteString(v)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write META %s: %v", path, err)
	}
}

// buildReleaseDir creates a release directory under releasesPath with
// META, notes, artifacts, and THANKS, returning the release dir path.
func buildReleaseDir(t *testing.T, releasesPath, tag, status, created string, artifacts map[string][]byte) string {
	t.Helper()
	releaseDir := filepath.Join(releasesPath, tag)
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", releaseDir, err)
	}
	writeReleaseMETAFile(t, filepath.Join(releaseDir, "META"), map[string]string{
		"tag":        tag,
		"hash":       "abc123",
		"created":    created,
		"status":     status,
		"created_by": "deadbeef",
	})
	if err := os.WriteFile(filepath.Join(releaseDir, "RELEASE.md"), []byte("# Title\nnotes body\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	artifactsDir := filepath.Join(releaseDir, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll artifacts: %v", err)
	}
	for name, data := range artifacts {
		if err := os.WriteFile(filepath.Join(artifactsDir, name), data, 0o644); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
	}
	packed, _ := msgpack.Pack(map[any]any{"count": int64(0)})
	if err := os.WriteFile(filepath.Join(releaseDir, "THANKS"), packed, 0o644); err != nil {
		t.Fatalf("write THANKS: %v", err)
	}
	return releaseDir
}

// TestReleaseListEmpty verifies releaseList on a missing releases dir
// returns resOK + an empty msgpack list (matching _release_list).
func TestReleaseListEmpty(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	releasesPath := filepath.Join(testutils.TempDir(t, "gorngit-rel-listempty-"), "releases")
	resp := n.releaseList(releasesPath)
	if len(resp) == 0 || resp[0] != resOK {
		t.Fatalf("releaseList = %x, want resOK prefix", resp)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(resp[1:])
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	arr, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", unpacked)
	}
	if len(arr) != 0 {
		t.Fatalf("expected empty list, got %v", arr)
	}
}

// TestReleaseListWithData verifies releaseList returns the releases map
// with the "releases" and "latest" keys, sorted by created descending.
func TestReleaseListWithData(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-listdata-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v1.0.0", "published", "100", map[string][]byte{"a.bin": {1, 2}})
	buildReleaseDir(t, releasesPath, "v2.0.0", "published", "200", map[string][]byte{"b.bin": {3}})
	if err := os.WriteFile(filepath.Join(releasesPath, "latest"), []byte("v2.0.0"), 0o644); err != nil {
		t.Fatalf("write latest: %v", err)
	}

	resp := n.releaseList(releasesPath)
	if resp[0] != resOK {
		t.Fatalf("releaseList = %x, want resOK prefix", resp)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(resp[1:])
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("expected map, got %T", unpacked)
	}
	releases, _ := m["releases"].([]any)
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}
	// Sorted by created descending: v2.0.0 (200) first.
	first, _ := releases[0].(map[any]any)
	if first["tag"] != "v2.0.0" {
		t.Errorf("first release tag = %v, want v2.0.0", first["tag"])
	}
	if first["artifacts"] != int64(1) {
		t.Errorf("first artifacts = %v, want 1", first["artifacts"])
	}
	latest, _ := m["latest"].(string)
	if latest != "v2.0.0" {
		t.Errorf("latest = %v, want v2.0.0", latest)
	}
}

// TestReleaseListLatestOnlyPublished verifies the "latest" file is only
// honoured when the referenced tag's status is "published".
func TestReleaseListLatestOnlyPublished(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-listpub-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v1.0.0", "draft", "100", nil)
	if err := os.WriteFile(filepath.Join(releasesPath, "latest"), []byte("v1.0.0"), 0o644); err != nil {
		t.Fatalf("write latest: %v", err)
	}
	resp := n.releaseList(releasesPath)
	unpacked, _ := msgpack.UnpackPreserveBinMapKeys(resp[1:])
	m, _ := unpacked.(map[any]any)
	if latest, _ := m["latest"].(string); latest != "" {
		t.Errorf("latest = %q, want empty (draft not published)", latest)
	}
}

// TestReleaseView verifies releaseView returns the full release detail.
func TestReleaseView(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-view-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v1.0.0", "published", "100", map[string][]byte{"app.bin": {0xAA, 0xBB}})

	resp := n.releaseView(releasesPath, map[any]any{"tag": "v1.0.0"})
	if resp[0] != resOK {
		t.Fatalf("releaseView = %x, want resOK", resp)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(resp[1:])
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	info, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("expected map, got %T", unpacked)
	}
	if info["tag"] != "v1.0.0" {
		t.Errorf("tag = %v", info["tag"])
	}
	if info["status"] != "published" {
		t.Errorf("status = %v", info["status"])
	}
	if info["notes"] != "# Title\nnotes body\n" {
		t.Errorf("notes = %q", info["notes"])
	}
	if info["notes_format"] != "markdown" {
		t.Errorf("notes_format = %v", info["notes_format"])
	}
	artifacts, _ := info["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	art, _ := artifacts[0].(map[any]any)
	if art["name"] != "app.bin" {
		t.Errorf("artifact name = %v", art["name"])
	}
	if art["size"] != int64(2) {
		t.Errorf("artifact size = %v, want 2", art["size"])
	}
	if info["thanks"] != int64(0) {
		t.Errorf("thanks = %v, want 0", info["thanks"])
	}
}

// TestReleaseViewLatest verifies releaseView resolves "latest" to the
// published tag.
func TestReleaseViewLatest(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-viewlatest-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v3.0.0", "published", "300", nil)
	if err := os.WriteFile(filepath.Join(releasesPath, "latest"), []byte("v3.0.0"), 0o644); err != nil {
		t.Fatalf("write latest: %v", err)
	}
	resp := n.releaseView(releasesPath, map[any]any{"tag": "latest"})
	if resp[0] != resOK {
		t.Fatalf("releaseView latest = %x, want resOK", resp)
	}
	unpacked, _ := msgpack.UnpackPreserveBinMapKeys(resp[1:])
	info, _ := unpacked.(map[any]any)
	if info["tag"] != "v3.0.0" {
		t.Errorf("tag = %v, want v3.0.0", info["tag"])
	}
}

// TestReleaseViewInvalidTag verifies the "/" guard and empty-tag guard.
func TestReleaseViewInvalidTag(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	releasesPath := filepath.Join(testutils.TempDir(t, "gorngit-rel-viewinv-"), "releases")
	for _, tag := range []string{"a/b", ""} {
		resp := n.releaseView(releasesPath, map[any]any{"tag": tag})
		if resp[0] != resInvalidReq {
			t.Errorf("tag %q: code = %x, want resInvalidReq", tag, resp[0])
		}
	}
}

// TestReleaseViewNotFound verifies a missing release returns resNotFound.
func TestReleaseViewNotFound(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	releasesPath := filepath.Join(testutils.TempDir(t, "gorngit-rel-viewnf-"), "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	resp := n.releaseView(releasesPath, map[any]any{"tag": "nope"})
	if resp[0] != resNotFound {
		t.Errorf("code = %x, want resNotFound", resp[0])
	}
}

// TestReleaseFetch verifies releaseFetch returns resOK + artifact bytes.
func TestReleaseFetch(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-fetch-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	artifactData := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	buildReleaseDir(t, releasesPath, "v1.0.0", "published", "100", map[string][]byte{"pkg.bin": artifactData})

	resp := n.releaseFetch(releasesPath, map[any]any{"tag": "v1.0.0", "artifact": "pkg.bin"})
	if resp[0] != resOK {
		t.Fatalf("code = %x, want resOK", resp[0])
	}
	if !bytes.Equal(resp[1:], artifactData) {
		t.Errorf("payload = %x, want %x", resp[1:], artifactData)
	}
}

// TestReleaseFetchArtifactNotFound verifies a missing artifact returns
// resNotFound.
func TestReleaseFetchArtifactNotFound(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-fetchnf-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v1.0.0", "published", "100", nil)
	resp := n.releaseFetch(releasesPath, map[any]any{"tag": "v1.0.0", "artifact": "missing"})
	if resp[0] != resNotFound {
		t.Errorf("code = %x, want resNotFound", resp[0])
	}
}

// TestReleaseCreateInitAndFinalize verifies the init/finalize steps
// create a draft release, then publish it and set it as latest.
func TestReleaseCreateInitAndFinalize(t *testing.T) {
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-create-")
	repoPath := filepath.Join(base, "repo.git")
	seedBareRepoWithTag(t, repoPath, "v1.0.0")
	releasesPath := repoPath + ".releases"

	// init
	resp := n.releaseCreateInit(releasesPath, repoPath, map[any]any{
		"tag":          "v1.0.0",
		"hash":         "abc123",
		"notes":        "release notes",
		"notes_format": "markdown",
	}, n.identity)
	if resp[0] != resOK {
		t.Fatalf("init code = %x, want resOK", resp[0])
	}
	meta, err := readReleaseMETA(filepath.Join(releasesPath, "v1.0.0", "META"))
	if err != nil {
		t.Fatalf("read META: %v", err)
	}
	if meta["status"] != "draft" {
		t.Errorf("status = %q, want draft", meta["status"])
	}
	if meta["hash"] != "abc123" {
		t.Errorf("hash = %q, want abc123", meta["hash"])
	}
	notesBytes, _ := os.ReadFile(filepath.Join(releasesPath, "v1.0.0", "RELEASE.md"))
	if string(notesBytes) != "release notes" {
		t.Errorf("notes = %q, want %q", notesBytes, "release notes")
	}

	// init again -> already exists
	resp = n.releaseCreateInit(releasesPath, repoPath, map[any]any{"tag": "v1.0.0"}, n.identity)
	if resp[0] != resDisallowed {
		t.Errorf("second init code = %x, want resDisallowed", resp[0])
	}

	// init with nonexistent tag -> invalid
	resp = n.releaseCreateInit(releasesPath, repoPath, map[any]any{"tag": "nope"}, n.identity)
	if resp[0] != resInvalidReq {
		t.Errorf("bad tag init code = %x, want resInvalidReq", resp[0])
	}

	// artifact
	artifactData := []byte{1, 2, 3, 4}
	resp = n.releaseCreateArtifact(releasesPath, map[any]any{
		"tag":           "v1.0.0",
		"artifact_name": "pkg.bin",
		"artifact_data": artifactData,
	})
	if resp[0] != resOK {
		t.Fatalf("artifact code = %x, want resOK", resp[0])
	}
	written, _ := os.ReadFile(filepath.Join(releasesPath, "v1.0.0", "artifacts", "pkg.bin"))
	if !bytes.Equal(written, artifactData) {
		t.Errorf("artifact bytes = %x, want %x", written, artifactData)
	}

	// finalize
	resp = n.releaseCreateFinalize(releasesPath, map[any]any{"tag": "v1.0.0"})
	if resp[0] != resOK {
		t.Fatalf("finalize code = %x, want resOK", resp[0])
	}
	meta, _ = readReleaseMETA(filepath.Join(releasesPath, "v1.0.0", "META"))
	if meta["status"] != "published" {
		t.Errorf("status = %q, want published", meta["status"])
	}
	if meta["published_at"] == "" {
		t.Errorf("published_at should be set")
	}
	latest, _ := os.ReadFile(filepath.Join(releasesPath, "latest"))
	if strings.TrimSpace(string(latest)) != "v1.0.0" {
		t.Errorf("latest = %q, want v1.0.0", latest)
	}

	// artifact after finalize -> not writable
	resp = n.releaseCreateArtifact(releasesPath, map[any]any{
		"tag":           "v1.0.0",
		"artifact_name": "x.bin",
		"artifact_data": []byte{0},
	})
	if resp[0] != resDisallowed {
		t.Errorf("post-finalize artifact code = %x, want resDisallowed", resp[0])
	}
}

// TestReleaseCreateArtifactMissingData verifies the no-data guard.
func TestReleaseCreateArtifactMissingData(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-artnodata-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v1.0.0", "draft", "100", nil)
	resp := n.releaseCreateArtifact(releasesPath, map[any]any{
		"tag":           "v1.0.0",
		"artifact_name": "x.bin",
	})
	if resp[0] != resInvalidReq {
		t.Errorf("code = %x, want resInvalidReq", resp[0])
	}
}

// TestReleaseDelete verifies releaseDelete removes the release dir.
func TestReleaseDelete(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-del-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v1.0.0", "published", "100", nil)
	resp := n.releaseDelete(releasesPath, map[any]any{"tag": "v1.0.0"})
	if resp[0] != resOK {
		t.Fatalf("delete code = %x, want resOK", resp[0])
	}
	if isDir(filepath.Join(releasesPath, "v1.0.0")) {
		t.Errorf("release dir should be removed")
	}
}

// TestReleaseDeleteNotFound verifies deleting a missing release returns
// resNotFound.
func TestReleaseDeleteNotFound(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	releasesPath := filepath.Join(testutils.TempDir(t, "gorngit-rel-delnf-"), "releases")
	resp := n.releaseDelete(releasesPath, map[any]any{"tag": "nope"})
	if resp[0] != resNotFound {
		t.Errorf("code = %x, want resNotFound", resp[0])
	}
}

// TestReleaseLatest verifies releaseLatest writes the latest file.
func TestReleaseLatest(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-latest-")
	releasesPath := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buildReleaseDir(t, releasesPath, "v9.0.0", "published", "900", nil)
	resp := n.releaseLatest(releasesPath, map[any]any{"tag": "v9.0.0"})
	if resp[0] != resOK {
		t.Fatalf("latest code = %x, want resOK", resp[0])
	}
	latest, _ := os.ReadFile(filepath.Join(releasesPath, "latest"))
	if strings.TrimSpace(string(latest)) != "v9.0.0" {
		t.Errorf("latest = %q, want v9.0.0", latest)
	}
}

// TestReleaseLatestNotFound verifies setting latest for a missing release
// returns resNotFound.
func TestReleaseLatestNotFound(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	releasesPath := filepath.Join(testutils.TempDir(t, "gorngit-rel-latestnf-"), "releases")
	resp := n.releaseLatest(releasesPath, map[any]any{"tag": "nope"})
	if resp[0] != resNotFound {
		t.Errorf("code = %x, want resNotFound", resp[0])
	}
}

// TestHandleReleaseDispatch verifies the top-level dispatch: missing
// remote, bad data, unknown operation, and the list path.
func TestHandleReleaseDispatch(t *testing.T) {
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-dispatch-")
	repoPath := filepath.Join(base, "repo.git")
	seedBareRepoWithTag(t, repoPath, "v1.0.0")
	n.groups = map[string]*groupInfo{
		"main": {
			name:  "main",
			path:  base,
			perms: openPermissionLists(),
			repositories: map[string]*repositoryInfo{
				"repo.git": {name: "repo.git", group: "main", path: repoPath, perms: openPermissionLists()},
			},
		},
	}

	// no remote identity
	resp := n.handleRelease(pathRelease, []byte{}, nil, nil, nil, time.Now())
	if resp.([]byte)[0] != resDisallowed {
		t.Errorf("no remote: code = %x, want resDisallowed", resp.([]byte)[0])
	}

	id, _ := rns.NewIdentity(true, nil)
	// valid list request
	packed, _ := msgpack.Pack(map[any]any{
		int64(idxRepository): "main/repo.git",
		"operation":          "list",
	})
	resp = n.handleRelease(pathRelease, packed, nil, nil, id, time.Now())
	b, ok := resp.([]byte)
	if !ok || b[0] != resOK {
		t.Errorf("list: code = %v, want resOK", resp)
	}
}

// TestReleaseCreateInitTagSlashGuard verifies the tag "/" guard.
func TestReleaseCreateInitTagSlashGuard(t *testing.T) {
	t.Parallel()
	n := newTestNode(t)
	base := testutils.TempDir(t, "gorngit-rel-initslash-")
	repoPath := filepath.Join(base, "repo.git")
	seedBareRepoWithTag(t, repoPath, "v1.0.0")
	releasesPath := repoPath + ".releases"
	resp := n.releaseCreateInit(releasesPath, repoPath, map[any]any{"tag": "evil/path"}, n.identity)
	if resp[0] != resInvalidReq {
		t.Errorf("slash tag init code = %x, want resInvalidReq", resp[0])
	}
}
