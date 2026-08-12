// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newStatsHandlerNode is like newHandlerNode but grants the group the stats
// permission (group.perms.stats = TGT_ALL) so the stats page can read.
func newStatsHandlerNode(t *testing.T) (*pageNode, string, string) {
	t.Helper()
	repoPath, headHash := seedRichRepo(t)
	base := testutils.TempDir(t, "gorngit-statshandler-")
	owner := &reticulumGitNode{
		nodeName:    "TestNode",
		destination: &rns.Destination{Hash: bytes.Repeat([]byte{0xab}, 16)},
		groups: map[string]*groupInfo{
			"g": {
				name: "g",
				path: base,
				repositories: map[string]*repositoryInfo{
					"repo.git": {name: "repo.git", group: "g", path: repoPath, perms: permissionLists{}},
				},
				perms: permissionLists{
					read:  [][]byte{permTargetAllBytes},
					stats: [][]byte{permTargetAllBytes},
				},
			},
		},
		blockedIdentities: map[string]bool{},
		identityAliases:   map[string]string{},
		stats:             map[any]any{"pages": map[any]any{"front": map[any]any{}}, "groups": map[any]any{}},
		statsEnabled:      true,
		statsIgnored:      map[string]bool{},
	}
	pn, err := newPageNode(owner)
	if err != nil {
		t.Fatalf("newPageNode: %v", err)
	}
	return pn, repoPath, headHash
}

// seedWorkDoc writes a work document root at repoPath/.work/<scope>/<id>/root
// and returns the document directory. A created timestamp of 1700000000
// (2023-11-14 UTC) is used.
func seedWorkDoc(t *testing.T, repoPath, scope string, id int, title, content string) string {
	t.Helper()
	docDir := filepath.Join(repoPath+".work", scope, strconv.Itoa(id))
	rootPath := filepath.Join(docDir, "root")
	doc := map[any]any{
		"content": content,
		"meta": map[any]any{
			"format":  "markdown",
			"title":   title,
			"created": int64(1700000000),
		},
	}
	if !workSaveDocument(rootPath, doc) {
		t.Fatalf("seedWorkDoc: could not save %s", rootPath)
	}
	return docDir
}

// seedWorkComment writes a comment work document at docDir/<id>.
func seedWorkComment(t *testing.T, docDir string, id int, content string) {
	t.Helper()
	commentPath := filepath.Join(docDir, strconv.Itoa(id))
	doc := map[any]any{
		"content": content,
		"meta": map[any]any{
			"created": int64(1700000100),
		},
	}
	if !workSaveDocument(commentPath, doc) {
		t.Fatalf("seedWorkComment: could not save %s", commentPath)
	}
}

// fileResponse extracts the [content, {"name": nameBytes}] shape returned by
// serveArtifact / serveDownload.
func fileResponse(t *testing.T, resp any) (content []byte, name string) {
	t.Helper()
	arr, ok := resp.([]any)
	if !ok || len(arr) < 2 {
		t.Fatalf("expected file response []any, got %T: %v", resp, resp)
	}
	content, _ = arr[0].([]byte)
	if m, ok := arr[1].(map[any]any); ok {
		if b, ok := m["name"].([]byte); ok {
			name = string(b)
		}
	}
	return
}

func TestServeRepoPage(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveRepoPage(pagePathRepo, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD"}), nil, nil, nil, dummyTime()))
	s := string(out)
	if !bytes.Contains(out, []byte("rns://abababababababababababababababab/g/repo.git")) {
		t.Errorf("repo page missing rns:// breadcrumb url\n%q", s)
	}
	for _, want := range []string{"Files", "Work (0)", "Commits (2)", "Branches (1)", "Tags (1)", "Thanks (0)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("repo page missing %q\n%q", want, s)
		}
	}
	// README.md is rendered as markdown (its "# Title" becomes a heading).
	if !bytes.Contains(out, []byte("Title")) {
		t.Errorf("repo page missing rendered README\n%q", s)
	}
}

func TestServeRepoPageNotFound(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveRepoPage(pagePathRepo, packVars(t, map[string]any{"g": "g", "r": "missing.git"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("The requested repository was not found")) {
		t.Errorf("missing repo should report not found\n%q", out)
	}
}

func TestServeRepoPageThanks(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	linkID := []byte("link-1")
	out := pageBytes(t, pn.serveRepoPage(pagePathRepo, packVars(t, map[string]any{"g": "g", "r": "repo.git", "thanks": "y"}), nil, linkID, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Thanks (1)")) {
		t.Errorf("first thanks should increment to 1\n%q", out)
	}
	// Re-requesting with the same link_id is de-duplicated.
	out = pageBytes(t, pn.serveRepoPage(pagePathRepo, packVars(t, map[string]any{"g": "g", "r": "repo.git", "thanks": "y"}), nil, linkID, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Thanks (1)")) {
		t.Errorf("duplicate thanks should stay at 1\n%q", out)
	}
}

func TestServeStatsPage(t *testing.T) {
	t.Parallel()
	pn, _, _ := newStatsHandlerNode(t)
	out := pageBytes(t, pn.serveStatsPage(pagePathStats, packVars(t, map[string]any{"g": "g", "r": "repo.git"}), nil, nil, nil, dummyTime()))
	s := string(out)
	for _, want := range []string{"Stats for repo.git", "Fetches", "Pushes", "Views", "Downloads", "Activity"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("stats page missing %q\n%q", want, s)
		}
	}
	// No seeded activity -> the "no development activity" fallback.
	if !bytes.Contains(out, []byte("No development activity recorded")) {
		t.Errorf("stats page should show no-activity fallback\n%q", s)
	}
	if !bytes.Contains(out, []byte("over the last 90 days")) {
		t.Errorf("stats page should report the 90-day lookback\n%q", s)
	}
}

func TestServeStatsPageDenied(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveStatsPage(pagePathStats, packVars(t, map[string]any{"g": "g", "r": "repo.git"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("The requested repository was not found")) {
		t.Errorf("stats without perm should be not found\n%q", out)
	}
}

func TestServeStatsPageNoVar(t *testing.T) {
	t.Parallel()
	pn, _, _ := newStatsHandlerNode(t)
	out := pageBytes(t, pn.serveStatsPage(pagePathStats, packVars(t, nil), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Invalid request")) {
		t.Errorf("missing vars should be invalid request\n%q", out)
	}
}

func TestServeReleasesPage(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	releasesPath := repoPath + ".releases"
	buildReleaseDir(t, releasesPath, "v1.0", "published", "1700000000", map[string][]byte{"app.bin": {0xAA, 0xBB}})
	out := pageBytes(t, pn.serveReleasesPage(pagePathReleases, packVars(t, map[string]any{"g": "g", "r": "repo.git"}), nil, nil, nil, dummyTime()))
	s := string(out)
	for _, want := range []string{"Releases (1)", "v1.0", "1 artifact", "notes body"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("releases page missing %q\n%q", want, s)
		}
	}
}

func TestServeReleasesPageEmpty(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveReleasesPage(pagePathReleases, packVars(t, map[string]any{"g": "g", "r": "repo.git"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("No releases available")) {
		t.Errorf("empty releases page should say so\n%q", out)
	}
}

func TestServeReleasePage(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	releasesPath := repoPath + ".releases"
	buildReleaseDir(t, releasesPath, "v1.0", "published", "1700000000", map[string][]byte{"app.bin": {0xAA, 0xBB}})
	out := pageBytes(t, pn.serveReleasePage(pagePathRelease, packVars(t, map[string]any{"g": "g", "r": "repo.git", "t": "v1.0"}), nil, nil, nil, dummyTime()))
	s := string(out)
	for _, want := range []string{">>Release v1.0", "Artifacts (1)", "app.bin", "Thanks (0)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("release page missing %q\n%q", want, s)
		}
	}
}

func TestServeReleasePageNotFound(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveReleasePage(pagePathRelease, packVars(t, map[string]any{"g": "g", "r": "repo.git", "t": "v9.9"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("does not exist")) {
		t.Errorf("missing release should report does not exist\n%q", out)
	}
}

func TestServeReleasePageLatest(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	releasesPath := repoPath + ".releases"
	buildReleaseDir(t, releasesPath, "v1.0", "published", "1700000000", nil)
	if err := os.WriteFile(filepath.Join(releasesPath, "latest"), []byte("v1.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := pageBytes(t, pn.serveReleasePage(pagePathRelease, packVars(t, map[string]any{"g": "g", "r": "repo.git", "t": "latest"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte(">>Release v1.0")) {
		t.Errorf("latest should resolve to v1.0\n%q", out)
	}
}

func TestServeWorkPage(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	seedWorkDoc(t, repoPath, "active", 1, "My Doc", "Hello world")
	out := pageBytes(t, pn.serveWorkPage(pagePathWork, packVars(t, map[string]any{"g": "g", "r": "repo.git", "scope": "active"}), nil, nil, nil, dummyTime()))
	s := string(out)
	for _, want := range []string{"Active (1)", "My Doc", "#1", "by unknown"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("work page missing %q\n%q", want, s)
		}
	}
}

func TestServeWorkPageEmpty(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveWorkPage(pagePathWork, packVars(t, map[string]any{"g": "g", "r": "repo.git", "scope": "active"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("No active work documents")) {
		t.Errorf("empty work page should say no active docs\n%q", out)
	}
}

func TestServeWorkPageCommentCount(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	docDir := seedWorkDoc(t, repoPath, "active", 1, "My Doc", "Hello world")
	seedWorkComment(t, docDir, 1, "Update one")
	out := pageBytes(t, pn.serveWorkPage(pagePathWork, packVars(t, map[string]any{"g": "g", "r": "repo.git", "scope": "active"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("1 updates")) {
		t.Errorf("work page should report 1 update\n%q", out)
	}
}

func TestServeWorkDocPage(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	seedWorkDoc(t, repoPath, "active", 1, "My Doc", "Hello world")
	out := pageBytes(t, pn.serveWorkDocPage(pagePathWorkDoc, packVars(t, map[string]any{"g": "g", "r": "repo.git", "id": 1, "scope": "active"}), nil, nil, nil, dummyTime()))
	s := string(out)
	for _, want := range []string{"My Doc", "Author    : Unknown", "Signature : Document not signed", "Status    : Active", "Hello world", "#1", "Download"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("work doc page missing %q\n%q", want, s)
		}
	}
}

func TestServeWorkDocPageComments(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	docDir := seedWorkDoc(t, repoPath, "active", 1, "My Doc", "Hello world")
	seedWorkComment(t, docDir, 1, "Update one")
	out := pageBytes(t, pn.serveWorkDocPage(pagePathWorkDoc, packVars(t, map[string]any{"g": "g", "r": "repo.git", "id": 1, "scope": "active"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Updates (1)")) {
		t.Errorf("work doc page missing updates heading\n%q", out)
	}
	if !bytes.Contains(out, []byte("#1 by")) {
		t.Errorf("work doc page missing comment header\n%q", out)
	}
	if !bytes.Contains(out, []byte("Update one")) {
		t.Errorf("work doc page missing comment content\n%q", out)
	}
}

func TestServeWorkDocPageNotFound(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveWorkDocPage(pagePathWorkDoc, packVars(t, map[string]any{"g": "g", "r": "repo.git", "id": 999, "scope": "active"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("was not found")) {
		t.Errorf("missing work doc should report not found\n%q", out)
	}
}

func TestServeArtifact(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	releasesPath := repoPath + ".releases"
	buildReleaseDir(t, releasesPath, "v1.0", "published", "1700000000", map[string][]byte{"app.bin": {0xAA, 0xBB}})
	resp := pn.serveArtifact(fileArtifact, packVars(t, map[string]any{"g": "g", "r": "repo.git", "t": "v1.0", "a": "app.bin"}), nil, nil, nil, dummyTime())
	content, name := fileResponse(t, resp)
	if name != "app.bin" {
		t.Errorf("artifact name = %q, want app.bin", name)
	}
	if !bytes.Equal(content, []byte{0xAA, 0xBB}) {
		t.Errorf("artifact content = %v, want 0xAA 0xBB", content)
	}
}

func TestServeArtifactUnpublished(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	releasesPath := repoPath + ".releases"
	buildReleaseDir(t, releasesPath, "v1.0", "unpublished", "1700000000", map[string][]byte{"app.bin": {0xAA, 0xBB}})
	resp := pn.serveArtifact(fileArtifact, packVars(t, map[string]any{"g": "g", "r": "repo.git", "t": "v1.0", "a": "app.bin"}), nil, nil, nil, dummyTime())
	if resp != nil {
		t.Errorf("unpublished artifact should return nil, got %v", resp)
	}
}

func TestServeArtifactMissing(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	releasesPath := repoPath + ".releases"
	buildReleaseDir(t, releasesPath, "v1.0", "published", "1700000000", nil)
	resp := pn.serveArtifact(fileArtifact, packVars(t, map[string]any{"g": "g", "r": "repo.git", "t": "v1.0", "a": "nope.bin"}), nil, nil, nil, dummyTime())
	if resp != nil {
		t.Errorf("missing artifact file should return nil, got %v", resp)
	}
}

func TestServeDownload(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	resp := pn.serveDownload(fileDownload, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "path": "README.md"}), nil, nil, nil, dummyTime())
	content, name := fileResponse(t, resp)
	if name != "README.md" {
		t.Errorf("download name = %q, want README.md", name)
	}
	if !bytes.Contains(content, []byte("# Title")) {
		t.Errorf("download content = %q, want README contents", content)
	}
}

func TestServeDownloadMissingFile(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	resp := pn.serveDownload(fileDownload, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "path": "nope.txt"}), nil, nil, nil, dummyTime())
	if resp != nil {
		t.Errorf("missing download should return nil, got %v", resp)
	}
}

func TestServeWdDownload(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	seedWorkDoc(t, repoPath, "active", 1, "My Doc", "Hello world")
	resp := pn.serveWdDownload(fileWorkdoc, packVars(t, map[string]any{"g": "g", "r": "repo.git", "id": 1, "scope": "active"}), nil, nil, nil, dummyTime())
	arr, ok := resp.([]any)
	if !ok || len(arr) < 2 {
		t.Fatalf("wd download resp = %v", resp)
	}
	fileName, _ := arr[0].(string)
	content, _ := arr[1].([]byte)
	if fileName != "My Doc.md" {
		t.Errorf("wd download filename = %q, want My Doc.md", fileName)
	}
	if string(content) != "Hello world" {
		t.Errorf("wd download content = %q, want Hello world", content)
	}
}

func TestServeWdDownloadMissing(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	resp := pn.serveWdDownload(fileWorkdoc, packVars(t, map[string]any{"g": "g", "r": "repo.git", "id": 999, "scope": "active"}), nil, nil, nil, dummyTime())
	if resp != nil {
		t.Errorf("missing wd download should return nil, got %v", resp)
	}
}

func TestRepositoryThanks(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	if c := pn.repositoryThanks(repoPath, false, nil); c != 0 {
		t.Errorf("initial thanks (no add) = %d, want 0", c)
	}
	if c := pn.repositoryThanks(repoPath, true, []byte("a")); c != 1 {
		t.Errorf("first add = %d, want 1", c)
	}
	if c := pn.repositoryThanks(repoPath, true, []byte("a")); c != 1 {
		t.Errorf("duplicate add = %d, want 1 (dedup)", c)
	}
	if c := pn.repositoryThanks(repoPath, true, []byte("b")); c != 2 {
		t.Errorf("second distinct add = %d, want 2", c)
	}
}

func TestReleaseThanks(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	releasesPath := repoPath + ".releases"
	releaseDir := buildReleaseDir(t, releasesPath, "v1.0", "published", "1700000000", nil)
	if c := pn.releaseThanks(releaseDir, true, []byte("a")); c != 1 {
		t.Errorf("first release thanks = %d, want 1", c)
	}
	if c := pn.releaseThanks(releaseDir, true, []byte("a")); c != 1 {
		t.Errorf("duplicate release thanks = %d, want 1", c)
	}
}

func TestLastUpstreamSync(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	owner := pn.owner
	if c := owner.lastUpstreamSync(repoPath); c != 0 {
		t.Errorf("unset lastUpstreamSync = %d, want 0", c)
	}
	if _, ok := gitRun(repoPath, "config", "repository.rngit.upstream.sync", "1234567890"); !ok {
		t.Fatal("could not set upstream sync")
	}
	if c := owner.lastUpstreamSync(repoPath); c != 1234567890 {
		t.Errorf("lastUpstreamSync = %d, want 1234567890", c)
	}
}

func TestRenderCombinedChartNoData(t *testing.T) {
	t.Parallel()
	if got := renderCombinedChart([]int{0, 0}, []int{0}, []int{0}, []int{0}, []string{"a", "b"}); got != "No data available\n" {
		t.Errorf("all-zero combined chart = %q, want No data available", got)
	}
	if got := renderCombinedChart(nil, nil, nil, nil, nil); got != "No data available\n" {
		t.Errorf("empty combined chart = %q, want No data available", got)
	}
}

func TestRenderCombinedChartWithData(t *testing.T) {
	t.Parallel()
	got := renderCombinedChart([]int{1, 0}, []int{0, 1}, []int{2, 0}, []int{0, 0}, []string{"90 days ago", "Today"})
	if !bytes.Contains([]byte(got), []byte("Pushes")) {
		t.Errorf("combined chart missing legend\n%q", got)
	}
	if !bytes.Contains([]byte(got), []byte("│")) {
		t.Errorf("combined chart missing plot rows\n%q", got)
	}
}

func TestFirstLine(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"only", "only"},
		{"first\nsecond", "first"},
		{"a\r\nb", "a"},
	}
	for _, c := range cases {
		if got := firstLine(c.in); got != c.want {
			t.Errorf("firstLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncateEllipsis(t *testing.T) {
	t.Parallel()
	if got := truncateEllipsis("short", 92); got != "short" {
		t.Errorf("truncateEllipsis short = %q", got)
	}
	if got := truncateEllipsis("abcdefghij", 5); got != "abcde…" {
		t.Errorf("truncateEllipsis long = %q, want abcde…", got)
	}
}
