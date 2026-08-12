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

// newPageNodeTest returns a pageNode with a nil owner; the git plumbing
// helpers only use git subprocesses and do not touch the owner.
func newPageNodeTest(t *testing.T) *pageNode {
	t.Helper()
	return &pageNode{}
}

// seedRichRepo creates a bare repo with a README.md, a src/ subtree, a
// second commit, and a lightweight tag, returning the bare repo path and the
// HEAD commit hash.
func seedRichRepo(t *testing.T) (repoPath, headHash string) {
	t.Helper()
	repoPath = filepath.Join(testutils.TempDir(t, "gorngit-pagesgit-"), "repo.git")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	runGit(t, repoPath, "init", "--bare")
	runGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")

	work := testutils.TempDir(t, "gorngit-pagesgit-work-")
	runGit(t, work, "init")
	runGit(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# Title\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(work, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "config", "user.email", "test@example.com")
	runGit(t, work, "config", "user.name", "Tester")
	runGit(t, work, "commit", "-m", "first commit")
	runGit(t, work, "push", repoPath, "refs/heads/main")

	if err := os.WriteFile(filepath.Join(work, "src", "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "second commit")
	runGit(t, work, "push", repoPath, "refs/heads/main")
	runGit(t, work, "tag", "v1.0")
	runGit(t, work, "push", repoPath, "refs/tags/v1.0")

	headHash = strings.TrimSpace(runGit(t, repoPath, "rev-parse", "refs/heads/main"))
	return
}

func TestGetRepositoryDescription(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, _ := seedRichRepo(t)
	if d := p.getRepositoryDescription(repoPath); d != "" {
		t.Errorf("description before set = %q, want empty", d)
	}
	// Set via git config and verify.
	if _, ok := gitRun(repoPath, "config", "repository.description", "My repo"); !ok {
		t.Fatal("could not set description")
	}
	if d := p.getRepositoryDescription(repoPath); d != "My repo" {
		t.Errorf("description = %q, want %q", d, "My repo")
	}
}

func TestGetRepositoryRefs(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, _ := seedRichRepo(t)
	heads, tags := p.getRepositoryRefs(repoPath)
	if len(heads) != 1 || heads[0].name != "main" {
		t.Errorf("heads = %+v, want one 'main'", heads)
	}
	if len(tags) != 1 || tags[0].name != "v1.0" {
		t.Errorf("tags = %+v, want one 'v1.0'", tags)
	}
	if len(heads[0].shortHash) != 7 {
		t.Errorf("shortHash len = %d, want 7", len(heads[0].shortHash))
	}
}

func TestResolveRef(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, head := seedRichRepo(t)
	if got := p.resolveRef(repoPath, "HEAD"); got != head {
		t.Errorf("resolveRef HEAD = %q, want %q", got, head)
	}
	if got := p.resolveRef(repoPath, "v1.0"); got == "" {
		t.Errorf("resolveRef v1.0 = empty, want hash")
	}
	if got := p.resolveRef(repoPath, "nope"); got != "" {
		t.Errorf("resolveRef nope = %q, want empty", got)
	}
}

func TestGetTreeEntries(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, _ := seedRichRepo(t)
	root := p.getTreeEntries(repoPath, "HEAD", "")
	if root == nil {
		t.Fatal("root entries = nil")
	}
	names := map[string]string{}
	for _, e := range root {
		names[e.name] = e.typ
	}
	if names["README.md"] != "blob" {
		t.Errorf("README.md type = %q, want blob", names["README.md"])
	}
	if names["src"] != "tree" {
		t.Errorf("src type = %q, want tree", names["src"])
	}
	sub := p.getTreeEntries(repoPath, "HEAD", "src")
	if sub == nil {
		t.Fatal("src entries = nil")
	}
	if len(sub) != 2 {
		t.Fatalf("src entries = %d, want 2", len(sub))
	}
	// Empty/non-tree returns nil.
	if got := p.getTreeEntries(repoPath, "HEAD", "does/not/exist"); got != nil {
		t.Errorf("missing path = %v, want nil", got)
	}
}

func TestGetBlobInfoAndContent(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, _ := seedRichRepo(t)
	info := p.getBlobInfo(repoPath, "HEAD", "README.md")
	if info == nil {
		t.Fatal("blob info = nil")
	}
	if info.isTree {
		t.Error("README.md reported as tree")
	}
	if info.isBinary {
		t.Error("README.md reported as binary")
	}
	if content := p.getBlobContent(repoPath, "HEAD", "README.md"); !strings.Contains(content, "# Title") {
		t.Errorf("blob content = %q, want # Title", content)
	}
	// A directory is a tree.
	treeInfo := p.getBlobInfo(repoPath, "HEAD", "src")
	if treeInfo == nil || !treeInfo.isTree {
		t.Errorf("src should be a tree, got %+v", treeInfo)
	}
}

func TestGetCommitCountAndCommits(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, _ := seedRichRepo(t)
	if n := p.getCommitCount(repoPath, "HEAD"); n != 2 {
		t.Errorf("commit count = %d, want 2", n)
	}
	commits := p.getCommits(repoPath, "HEAD", "", 0, 10)
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	if commits[0].subject != "second commit" {
		t.Errorf("first commit subject = %q, want 'second commit'", commits[0].subject)
	}
	if commits[0].author != "Tester" {
		t.Errorf("author = %q, want Tester", commits[0].author)
	}
	if commits[0].timestamp == 0 {
		t.Error("timestamp = 0, want nonzero")
	}
}

func TestGetCommitInfo(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, head := seedRichRepo(t)
	info := p.getCommitInfo(repoPath, head, true)
	if info == nil {
		t.Fatal("commit info = nil")
	}
	if info.authorName != "Tester" {
		t.Errorf("author name = %q, want Tester", info.authorName)
	}
	if info.message != "second commit" {
		t.Errorf("message = %q, want 'second commit'", info.message)
	}
	if len(info.parents) != 1 {
		t.Errorf("parents = %v, want one", info.parents)
	}
	// Second commit added b.txt (status A) and modified nothing else.
	foundAdd := false
	for _, f := range info.files {
		if f.path == "src/b.txt" && f.status == "A" {
			foundAdd = true
		}
	}
	if !foundAdd {
		t.Errorf("expected src/b.txt added in files %+v", info.files)
	}
	if info.diff == "" {
		t.Error("diff should be populated when showDiff=true")
	}
	// showDiff=false omits the diff.
	info2 := p.getCommitInfo(repoPath, head, false)
	if info2.diff != "" {
		t.Error("diff should be empty when showDiff=false")
	}
}

func TestGetCommitSignatureStub(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, head := seedRichRepo(t)
	sig := p.getCommitSignature(repoPath, head)
	if sig.signed {
		t.Error("stub should report not signed")
	}
	if sig.message != "Not signed" {
		t.Errorf("message = %q, want 'Not signed'", sig.message)
	}
}

func TestGetReadmeContent(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, _ := seedRichRepo(t)
	content, isMarkdown, found := p.getReadmeContent(repoPath)
	if !found {
		t.Fatal("readme not found")
	}
	if !isMarkdown {
		t.Error("README.md should be markdown")
	}
	if !strings.Contains(content, "# Title") {
		t.Errorf("content = %q, want # Title", content)
	}
}

func TestMirrorSynced(t *testing.T) {
	t.Parallel()
	p := newPageNodeTest(t)
	repoPath, _ := seedRichRepo(t)
	if n := p.mirrorSynced(repoPath); n != 0 {
		t.Errorf("unset mirrorSynced = %d, want 0", n)
	}
	if _, ok := gitRun(repoPath, "config", "repository.rngit.upstream.sync", "1234567890"); !ok {
		t.Fatal("could not set sync")
	}
	if n := p.mirrorSynced(repoPath); n != 1234567890 {
		t.Errorf("mirrorSynced = %d, want 1234567890", n)
	}
}

func TestDirAndBaseOf(t *testing.T) {
	t.Parallel()
	if d := dirOf("a/b/c"); d != "a/b" {
		t.Errorf("dirOf = %q, want a/b", d)
	}
	if d := dirOf("a"); d != "" {
		t.Errorf("dirOf(a) = %q, want empty", d)
	}
	if b := baseOf("a/b/c"); b != "c" {
		t.Errorf("baseOf = %q, want c", b)
	}
	if b := baseOf("a"); b != "a" {
		t.Errorf("baseOf(a) = %q, want a", b)
	}
}
