// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newHandlerNode builds a pageNode wrapping a reticulumGitNode that owns one
// group "g" with one readable bare repo "repo.git" (seeded with README.md,
// src/a.txt, two commits, and a v1.0 tag). Stats are disabled. It returns
// the pageNode, the bare repo path, and the HEAD commit hash.
func newHandlerNode(t *testing.T) (*pageNode, string, string) {
	t.Helper()
	repoPath, headHash := seedRichRepo(t)
	base := testutils.TempDir(t, "gorngit-handler-")
	owner := &reticulumGitNode{
		nodeName: "TestNode",
		destination: &rns.Destination{Hash: bytes.Repeat([]byte{0xab}, 16),
			HexHash: "abababababababababababababababab"},
		groups: map[string]*groupInfo{
			"g": {
				name: "g",
				path: base,
				repositories: map[string]*repositoryInfo{
					"repo.git": {name: "repo.git", group: "g", path: repoPath, perms: permissionLists{}},
				},
				perms: permissionLists{read: [][]byte{permTargetAllBytes}},
			},
		},
		blockedIdentities: map[string]bool{},
		identityAliases:   map[string]string{},
		stats:             map[any]any{"pages": map[any]any{"front": map[any]any{}}, "groups": map[any]any{}},
		statsEnabled:      false,
		statsIgnored:      map[string]bool{},
	}
	pn, err := newPageNode(owner)
	if err != nil {
		t.Fatalf("newPageNode: %v", err)
	}
	return pn, repoPath, headHash
}

// packVars packs a var_<name> request body, mirroring the nomadnet page
// request encoding the handlers unpack.
func packVars(t *testing.T, vars map[string]any) []byte {
	t.Helper()
	m := map[any]any{}
	for k, v := range vars {
		m["var_"+k] = v
	}
	b, err := msgpack.Pack(m)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return b
}

// pageBytes calls a handler and returns its []byte response.
func pageBytes(t *testing.T, resp any) []byte {
	t.Helper()
	b, ok := resp.([]byte)
	if !ok {
		t.Fatalf("handler returned %T, want []byte", resp)
	}
	return b
}

func TestServeFrontPage(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveFrontPage(pagePathIndex, nil, nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("/page/group.mu`g=g]`!")) {
		t.Errorf("front page missing group link\n%q", out)
	}
	if !bytes.Contains(out, []byte("(1 repository)")) {
		t.Errorf("front page missing repo count\n%q", out)
	}
	if !bytes.Contains(out, []byte("> TestNode")) {
		t.Errorf("front page missing node name\n%q", out)
	}
}

func TestServeFrontPageNoGroups(t *testing.T) {
	t.Parallel()
	owner := &reticulumGitNode{
		nodeName:          "Empty",
		groups:            map[string]*groupInfo{},
		blockedIdentities: map[string]bool{},
		identityAliases:   map[string]string{},
		stats:             map[any]any{"pages": map[any]any{"front": map[any]any{}}, "groups": map[any]any{}},
		statsEnabled:      false,
		statsIgnored:      map[string]bool{},
	}
	pn, err := newPageNode(owner)
	if err != nil {
		t.Fatalf("newPageNode: %v", err)
	}
	out := pageBytes(t, pn.serveFrontPage(pagePathIndex, nil, nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("No groups available")) {
		t.Errorf("empty front page should say no groups\n%q", out)
	}
}

func TestServeGroupPage(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveGroupPage(pagePathGroup, packVars(t, map[string]any{"g": "g"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("/page/repo.mu`g=g|r=repo.git]`!")) {
		t.Errorf("group page missing repo link\n%q", out)
	}
	if !bytes.Contains(out, []byte("> Repositories")) {
		t.Errorf("group page missing Repositories heading\n%q", out)
	}
}

func TestServeGroupPageNotFound(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveGroupPage(pagePathGroup, packVars(t, map[string]any{"g": "missing"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Group Not Found")) {
		t.Errorf("missing group should report not found\n%q", out)
	}
}

func TestServeGroupPageNoVar(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveGroupPage(pagePathGroup, packVars(t, nil), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Invalid request")) {
		t.Errorf("missing var_g should be invalid request\n%q", out)
	}
}

func TestServeTreePage(t *testing.T) {
	t.Parallel()
	pn, repoPath, _ := newHandlerNode(t)
	_ = repoPath
	out := pageBytes(t, pn.serveTreePage(pagePathTree, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Contents: HEAD")) {
		t.Errorf("tree page missing contents heading\n%q", out)
	}
	if !bytes.Contains(out, []byte("README.md")) {
		t.Errorf("tree page missing README.md\n%q", out)
	}
	if !bytes.Contains(out, []byte("src/")) {
		t.Errorf("tree page missing src dir\n%q", out)
	}
	if !bytes.Contains(out, []byte("/page/blob.mu`g=g|r=repo.git|ref=HEAD|path=README.md]")) {
		t.Errorf("tree page missing blob link\n%q", out)
	}
}

func TestServeTreePageSubdir(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveTreePage(pagePathTree, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "path": "src"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("a.txt")) {
		t.Errorf("src tree missing a.txt\n%q", out)
	}
	if !bytes.Contains(out, []byte("b.txt")) {
		t.Errorf("src tree missing b.txt\n%q", out)
	}
	if !bytes.Contains(out, []byte("/page/tree.mu`g=g|r=repo.git|ref=HEAD|path=")) {
		t.Errorf("src tree missing parent link\n%q", out)
	}
}

func TestServeTreePageNotFound(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveTreePage(pagePathTree, packVars(t, map[string]any{"g": "g", "r": "nope.git"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Not Found")) {
		t.Errorf("missing repo tree should be not found\n%q", out)
	}
}

func TestServeTreePageBadRef(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveTreePage(pagePathTree, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "nonexistent"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("does not exist in this repository")) {
		t.Errorf("bad ref should report it\n%q", out)
	}
}

func TestServeBlobPageMarkdown(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveBlobPage(pagePathBlob, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "path": "README.md"}), nil, nil, nil, dummyTime()))
	// Blob heading includes the file path.
	if !bytes.Contains(out, []byte(">>README.md")) {
		t.Errorf("blob page missing heading\n%q", out)
	}
	// Rendered markdown content includes the title text.
	if !bytes.Contains(out, []byte("Title")) {
		t.Errorf("blob page missing rendered title\n%q", out)
	}
}

func TestServeBlobPageRawText(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveBlobPage(pagePathBlob, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "path": "src/a.txt", "raw": "y"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("alpha")) {
		t.Errorf("raw blob missing content\n%q", out)
	}
}

func TestServeBlobPageTreeRedirect(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	// Requesting the src directory as a blob redirects to the tree page.
	out := pageBytes(t, pn.serveBlobPage(pagePathBlob, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "path": "src"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Contents: HEAD")) {
		t.Errorf("blob→tree redirect missing tree contents\n%q", out)
	}
}

func TestServeBlobPageMissingPath(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveBlobPage(pagePathBlob, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Invalid Path")) {
		t.Errorf("missing path should be invalid\n%q", out)
	}
}

func TestServeCommitsPage(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveCommitsPage(pagePathCommits, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Commits")) {
		t.Errorf("commits page missing heading\n%q", out)
	}
	if !bytes.Contains(out, []byte("second commit")) {
		t.Errorf("commits page missing second commit\n%q", out)
	}
	if !bytes.Contains(out, []byte("first commit")) {
		t.Errorf("commits page missing first commit\n%q", out)
	}
	if !bytes.Contains(out, []byte("/page/commit.mu`g=g|r=repo.git|ref=HEAD|h=")) {
		t.Errorf("commits page missing commit links\n%q", out)
	}
}

func TestServeCommitPage(t *testing.T) {
	t.Parallel()
	pn, _, head := newHandlerNode(t)
	out := pageBytes(t, pn.serveCommitPage(pagePathCommit, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "h": head}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte(">>Commit "+head)) {
		t.Errorf("commit page missing commit heading\n%q", out)
	}
	if !bytes.Contains(out, []byte("Author     :")) {
		t.Errorf("commit page missing author label\n%q", out)
	}
	if !bytes.Contains(out, []byte("Tester")) {
		t.Errorf("commit page missing author name\n%q", out)
	}
	if !bytes.Contains(out, []byte("second commit")) {
		t.Errorf("commit page missing message\n%q", out)
	}
	if !bytes.Contains(out, []byte("Changes")) {
		t.Errorf("commit page missing changes heading\n%q", out)
	}
	if !bytes.Contains(out, []byte("src/b.txt")) {
		t.Errorf("commit page missing changed file\n%q", out)
	}
	if !bytes.Contains(out, []byte("Diff")) {
		t.Errorf("commit page missing diff heading\n%q", out)
	}
}

func TestServeCommitPageBadHash(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveCommitPage(pagePathCommit, packVars(t, map[string]any{"g": "g", "r": "repo.git", "ref": "HEAD", "h": "xyz"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("No valid commit hash specified")) {
		t.Errorf("short hash should be rejected\n%q", out)
	}
}

func TestServeRefsPage(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveRefsPage(pagePathRefs, packVars(t, map[string]any{"g": "g", "r": "repo.git"}), nil, nil, nil, dummyTime()))
	if !bytes.Contains(out, []byte("Branches (1)")) {
		t.Errorf("refs page missing branch count\n%q", out)
	}
	if !bytes.Contains(out, []byte("main")) {
		t.Errorf("refs page missing main branch\n%q", out)
	}
	if !bytes.Contains(out, []byte("(default)")) {
		t.Errorf("refs page missing default marker\n%q", out)
	}
	if !bytes.Contains(out, []byte("Tags (1)")) {
		t.Errorf("refs page missing tag count\n%q", out)
	}
	if !bytes.Contains(out, []byte("v1.0")) {
		t.Errorf("refs page missing v1.0 tag\n%q", out)
	}
}

func TestServeRefsPageTagsOnly(t *testing.T) {
	t.Parallel()
	pn, _, _ := newHandlerNode(t)
	out := pageBytes(t, pn.serveRefsPage(pagePathRefs, packVars(t, map[string]any{"g": "g", "r": "repo.git", "type": "tags"}), nil, nil, nil, dummyTime()))
	if bytes.Contains(out, []byte("Branches (1)")) {
		t.Errorf("tags-only refs page should not list branches\n%q", out)
	}
	if !bytes.Contains(out, []byte("Tags (1)")) {
		t.Errorf("tags-only refs page should list tags\n%q", out)
	}
}

// dummyTime returns a fixed requested-at for handler calls (unused by the
// browsing handlers but required by the signature).
func dummyTime() time.Time { return time.Time{} }
