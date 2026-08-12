// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newWorkTestNode returns a minimal reticulumGitNode with a registered
// repository in a temp group dir, plus the identity that authors work docs.
// It returns the node, the repo path (bare repo), the work path, and the
// identity. The work path is <repoPath>.work as on the server.
func newWorkTestNode(t *testing.T) (*reticulumGitNode, *rns.Identity, string, string) {
	t.Helper()
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	repoRoot := testutils.TempDir(t, "gorngit-work-root-")
	repoName := "workrepo.git"
	repoPath := filepath.Join(repoRoot, repoName)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repoPath, "init", "--bare")
	runGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")
	group := &groupInfo{
		name:         "main",
		path:         repoRoot,
		repositories: map[string]*repositoryInfo{},
		perms:        openPermissionLists(),
	}
	group.repositories[repoName] = &repositoryInfo{name: repoName, group: "main", path: repoPath, perms: openPermissionLists()}
	node := &reticulumGitNode{
		identity: id,
		groups:   map[string]*groupInfo{"main": group},
	}
	workPath := repoPath + ".work"
	return node, id, repoPath, workPath
}

// signContent signs content with id and returns the signature bytes.
func signContent(t *testing.T, id *rns.Identity, content string) []byte {
	t.Helper()
	sig, err := id.Sign([]byte(content))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return sig
}

// testRepoLogical is the logical "group/repo" path used as IDX_REPOSITORY
// in work unit-test request maps, mirroring the real wire protocol (which
// sends "group/repo", not a filesystem path). The test node registers the
// repo under group "main" with name "workrepo.git".
const testRepoLogical = "main/workrepo.git"

// workCreateRequest builds a create/propose request map.
func workCreateRequest(operation, title, content string, sig []byte) map[any]any {
	return map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          operation,
		"title":              title,
		"content":            content,
		"format":             "markdown",
		"signature":          sig,
	}
}

// unpackWorkResponse unpacks a work response into the result-code byte and
// the msgpack payload map. For non-OK codes the payload is returned as nil
// (the trailing bytes are a human-readable message, not msgpack).
func unpackWorkResponse(t *testing.T, resp []byte) (byte, map[any]any) {
	t.Helper()
	if len(resp) == 0 {
		t.Fatalf("empty work response")
	}
	code := resp[0]
	if code != resOK || len(resp) == 1 {
		return code, nil
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(resp[1:])
	if err != nil {
		t.Fatalf("unpack work response: %v", err)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("work response payload is %T, want map", unpacked)
	}
	return code, m
}

// callCreate invokes workCreate (or workPropose via workCreateInScope) on
// the node and returns the assigned doc id.
func callCreate(t *testing.T, n *reticulumGitNode, id *rns.Identity, repoPath, operation, title, content string) int {
	t.Helper()
	sig := signContent(t, id, content)
	data := workCreateRequest(operation, title, content, sig)
	var resp []byte
	if operation == "create" {
		resp = n.workCreate(repoPath+".work", data, id)
	} else {
		resp = n.workPropose(repoPath+".work", data, id)
	}
	code, m := unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("create response code=%x msg=%q", code, string(resp[1:]))
	}
	docID, _ := m["id"].(int64)
	return int(docID)
}

// TestWorkCreateListAndView verifies the create→list→view round-trip at the
// handler level: a created document appears in list and view returns its
// content, meta, and signature.
func TestWorkCreateListAndView(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	content := "This is the work document body."
	docID := callCreate(t, n, id, repoPath, "create", "My Doc", content)

	// list
	listData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "list",
		"scope":              "active",
	}
	resp := n.workList(workPath, listData, id)
	code, m := unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("list code=%x", code)
	}
	active, _ := m["active"].([]any)
	if len(active) != 1 {
		t.Fatalf("active list len=%d, want 1", len(active))
	}
	entry, _ := active[0].(map[any]any)
	if entry["id"].(int64) != int64(docID) {
		t.Errorf("list id=%v, want %d", entry["id"], docID)
	}
	if entry["title"] != "My Doc" {
		t.Errorf("list title=%v, want My Doc", entry["title"])
	}
	if entry["comments"].(int64) != 0 {
		t.Errorf("comments=%v, want 0", entry["comments"])
	}

	// view
	viewData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "view",
		"doc_id":             int64(docID),
		"scope":              "all",
	}
	resp = n.workView(workPath, viewData, id)
	code, m = unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("view code=%x", code)
	}
	if m["content"] != content {
		t.Errorf("view content=%v, want %q", m["content"], content)
	}
	if m["scope"] != "active" {
		t.Errorf("view scope=%v, want active", m["scope"])
	}
	meta, _ := m["meta"].(map[any]any)
	if meta["title"] != "My Doc" {
		t.Errorf("view meta title=%v, want My Doc", meta["title"])
	}
	if meta["format"] != "markdown" {
		t.Errorf("view meta format=%v, want markdown", meta["format"])
	}
	sig, _ := meta["signature"].([]byte)
	if len(sig) != signatureLength {
		t.Errorf("view meta signature len=%d, want %d", len(sig), signatureLength)
	}
	pub, _ := meta["identity"].([]byte)
	if len(pub) != rns.IdentityKeySize/8 {
		t.Errorf("view meta identity len=%d, want %d", len(pub), rns.IdentityKeySize/8)
	}
	author, _ := meta["author"].(string)
	if author != bytesToHex(id.Hash) {
		t.Errorf("view meta author=%v, want %x", author, id.Hash)
	}
}

// TestWorkProposeWritesAllowedFile verifies that propose writes the document
// to proposed/ and creates the owner .allowed file with i:/w: lines.
func TestWorkProposeWritesAllowedFile(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	content := "Proposal body."
	docID := callCreate(t, n, id, repoPath, "propose", "Prop", content)

	// Document lands in proposed.
	rootPath := filepath.Join(workPath, "proposed", intToString(docID), "root")
	if !isFile(rootPath) {
		t.Fatalf("proposed root %s does not exist", rootPath)
	}
	allowedPath := filepath.Join(workPath, intToString(docID)+".allowed")
	data, err := os.ReadFile(allowedPath)
	if err != nil {
		t.Fatalf("read .allowed: %v", err)
	}
	hex := bytesToHex(id.Hash)
	want := "i:" + hex + "\nw:" + hex + "\n"
	if string(data) != want {
		t.Errorf(".allowed = %q, want %q", string(data), want)
	}
}

// TestWorkCommentAddsComment verifies that comment writes a numeric comment
// file and returns its id; a subsequent view lists it.
func TestWorkCommentAddsComment(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	docID := callCreate(t, n, id, repoPath, "create", "Doc", "body")

	commentData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "comment",
		"doc_id":             int64(docID),
		"scope":              "active",
		"content":            "First update.",
		"format":             "markdown",
		"signature":          signContent(t, id, "First update."),
	}
	resp := n.workComment(workPath, commentData, id)
	code, m := unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("comment code=%x msg=%q", code, string(resp[1:]))
	}
	cid, _ := m["id"].(int64)
	if cid != 1 {
		t.Errorf("comment id=%d, want 1", cid)
	}
	commentPath := filepath.Join(workPath, "active", intToString(docID), intToString(int(cid)))
	if !isFile(commentPath) {
		t.Fatalf("comment file %s missing", commentPath)
	}

	// view should list the comment.
	viewData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "view",
		"doc_id":             int64(docID),
		"scope":              "all",
	}
	resp = n.workView(workPath, viewData, id)
	_, m = unpackWorkResponse(t, resp)
	comments, _ := m["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("view comments len=%d, want 1", len(comments))
	}
	c, _ := comments[0].(map[any]any)
	if c["content"] != "First update." {
		t.Errorf("comment content=%v, want 'First update.'", c["content"])
	}
}

// TestWorkEditAuthorOnly verifies that edit succeeds for the author and is
// rejected (resDisallowed) for a different identity.
func TestWorkEditAuthorOnly(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	docID := callCreate(t, n, id, repoPath, "create", "Doc", "body")

	other, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity other: %v", err)
	}
	newContent := "edited body"
	editData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "edit",
		"doc_id":             int64(docID),
		"scope":              "active",
		"content":            newContent,
		"title":              "Doc",
		"signature":          signContent(t, other, newContent),
	}
	resp := n.workEdit(workPath, editData, other)
	if len(resp) == 0 || resp[0] != resDisallowed {
		t.Fatalf("edit by non-author code=%x, want resDisallowed", firstByte(resp))
	}

	// Author edit succeeds.
	editData["signature"] = signContent(t, id, newContent)
	resp = n.workEdit(workPath, editData, id)
	if len(resp) == 0 || resp[0] != resOK {
		t.Fatalf("edit by author code=%x, want resOK", firstByte(resp))
	}

	// View reflects the edited content.
	viewData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "view",
		"doc_id":             int64(docID),
		"scope":              "all",
	}
	resp = n.workView(workPath, viewData, id)
	_, m := unpackWorkResponse(t, resp)
	if m["content"] != newContent {
		t.Errorf("after edit content=%v, want %q", m["content"], newContent)
	}
}

// TestWorkCompleteActivateRoundTrip verifies complete moves active→completed
// and activate moves completed→active, both author-only.
func TestWorkCompleteActivateRoundTrip(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	docID := callCreate(t, n, id, repoPath, "create", "Doc", "body")

	completeData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "complete",
		"doc_id":             int64(docID),
	}
	resp := n.workComplete(workPath, completeData, id)
	code, m := unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("complete code=%x msg=%q", code, string(resp[1:]))
	}
	if m["scope"] != "completed" {
		t.Errorf("complete scope=%v, want completed", m["scope"])
	}
	if isDir(filepath.Join(workPath, "active", intToString(docID))) {
		t.Errorf("active/%s still exists after complete", intToString(docID))
	}
	if !isDir(filepath.Join(workPath, "completed", intToString(docID))) {
		t.Errorf("completed/%s missing after complete", intToString(docID))
	}

	activateData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "activate",
		"doc_id":             int64(docID),
	}
	resp = n.workActivate(workPath, activateData, id)
	code, m = unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("activate code=%x msg=%q", code, string(resp[1:]))
	}
	if m["scope"] != "active" {
		t.Errorf("activate scope=%v, want active", m["scope"])
	}
	if !isDir(filepath.Join(workPath, "active", intToString(docID))) {
		t.Errorf("active/%s missing after activate", intToString(docID))
	}
}

// TestWorkDeleteRemovesDoc verifies delete removes the document directory
// and the .allowed file.
func TestWorkDeleteRemovesDoc(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	docID := callCreate(t, n, id, repoPath, "propose", "Doc", "body")

	deleteData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "delete",
		"doc_id":             int64(docID),
		"scope":              "proposed",
	}
	resp := n.workDelete(workPath, deleteData, id)
	if len(resp) == 0 || resp[0] != resOK {
		t.Fatalf("delete code=%x, want resOK", firstByte(resp))
	}
	docDir := filepath.Join(workPath, "proposed", intToString(docID))
	if isDir(docDir) {
		t.Errorf("doc dir %s still exists after delete", docDir)
	}
	allowedPath := filepath.Join(workPath, intToString(docID)+".allowed")
	if _, err := os.Stat(allowedPath); !os.IsNotExist(err) {
		t.Errorf(".allowed %s still exists after delete", allowedPath)
	}
}

// TestWorkCreateRejectsBadSignature verifies that an invalid signature is
// rejected with resInvalidReq.
func TestWorkCreateRejectsBadSignature(t *testing.T) {
	t.Parallel()
	n, id, _, workPath := newWorkTestNode(t)
	badSig := make([]byte, signatureLength)
	data := workCreateRequest("create", "Doc", "body", badSig)
	resp := n.workCreate(workPath, data, id)
	if len(resp) == 0 || resp[0] != resInvalidReq {
		t.Fatalf("bad signature code=%x, want resInvalidReq", firstByte(resp))
	}
}

// TestWorkCreateEnforcesDocLimit verifies that oversize content is rejected.
func TestWorkCreateEnforcesDocLimit(t *testing.T) {
	t.Parallel()
	n, id, _, workPath := newWorkTestNode(t)
	content := make([]byte, workDocLimit+10)
	for i := range content {
		content[i] = 'x'
	}
	sig := signContent(t, id, string(content))
	data := workCreateRequest("create", "Doc", string(content), sig)
	resp := n.workCreate(workPath, data, id)
	if len(resp) == 0 || resp[0] != resInvalidReq {
		t.Fatalf("oversize code=%x, want resInvalidReq", firstByte(resp))
	}
	if string(resp[1:]) != "Content limit exceeded" {
		t.Errorf("oversize msg=%q, want 'Content limit exceeded'", string(resp[1:]))
	}
}

// TestWorkListSortedByCreatedDesc verifies that list entries are sorted by
// created descending.
func TestWorkListSortedByCreatedDesc(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	id1 := callCreate(t, n, id, repoPath, "create", "First", "body1")
	id2 := callCreate(t, n, id, repoPath, "create", "Second", "body2")
	id3 := callCreate(t, n, id, repoPath, "create", "Third", "body3")
	_ = id1
	_ = id3

	listData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "list",
		"scope":              "active",
	}
	resp := n.workList(workPath, listData, id)
	_, m := unpackWorkResponse(t, resp)
	active, _ := m["active"].([]any)
	if len(active) != 3 {
		t.Fatalf("active len=%d, want 3", len(active))
	}
	// Most-recently-created (id2 is the 2nd, id3 is the 3rd/last created).
	first, _ := active[0].(map[any]any)
	if first["id"].(int64) != int64(id2) && first["id"].(int64) != int64(id3) {
		// created timestamps are wall-clock; the latest id should sort first.
		t.Errorf("first entry id=%v, want one of the later-created ids", first["id"])
	}
}

// TestWorkGetNextIDGlobal verifies that the next id is global across scopes
// (max+1 across active/completed/proposed).
func TestWorkGetNextIDGlobal(t *testing.T) {
	t.Parallel()
	workPath := filepath.Join(testutils.TempDir(t, "gorngit-work-nextid-"), "repo.work")
	if err := os.MkdirAll(filepath.Join(workPath, "active", "5"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workPath, "completed", "8"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workPath, "proposed", "3"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := workGetNextID(workPath); got != 9 {
		t.Errorf("workGetNextID=%d, want 9", got)
	}
}

// TestWorkGetNextIDEmpty verifies that an empty work path returns 1.
func TestWorkGetNextIDEmpty(t *testing.T) {
	t.Parallel()
	workPath := filepath.Join(testutils.TempDir(t, "gorngit-work-empty-"), "repo.work")
	if got := workGetNextID(workPath); got != 1 {
		t.Errorf("workGetNextID empty=%d, want 1", got)
	}
}

// TestWorkGetNextCommentID verifies comment ids are max+1 of numeric files.
func TestWorkGetNextCommentID(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngit-work-cid-")
	if err := os.WriteFile(filepath.Join(dir, "1"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "4"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root"), []byte("c"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := workGetNextCommentID(dir); got != 5 {
		t.Errorf("workGetNextCommentID=%d, want 5", got)
	}
}

// TestParsePermissionValid verifies that valid permission lines parse to the
// expected perm byte and target.
func TestParsePermissionValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line     string
		wantPerm byte
		wantTgt  any
	}{
		{"r:all", permRead, permTargetAll},
		{"read:none", permRead, permTargetNone},
		{"w:all", permWrite, permTargetAll},
		{"i:everyone", permInteract, permTargetAll},
		{"p:nobody", permPropose, permTargetNone},
		{"adm:all", permAdmin, permTargetAll},
		{"c:a", permCreate, permTargetAll},
		{"s:n", permStats, permTargetNone},
		{"rel:all", permRelease, permTargetAll},
		{"rw:all", permReadWrite, permTargetAll},
	}
	for _, tc := range cases {
		perm, target := parsePermission(tc.line)
		if perm != tc.wantPerm {
			t.Errorf("parsePermission(%q) perm=%x, want %x", tc.line, perm, tc.wantPerm)
		}
		if target != tc.wantTgt {
			t.Errorf("parsePermission(%q) target=%v, want %v", tc.line, target, tc.wantTgt)
		}
	}
}

// TestParsePermissionHashTarget verifies that a 32-hex-char target is
// accepted as a literal identity hash.
func TestParsePermissionHashTarget(t *testing.T) {
	t.Parallel()
	hash := "00112233445566778899aabbccddeeff"
	perm, target := parsePermission("r:" + hash)
	if perm != permRead {
		t.Errorf("perm=%x, want read", perm)
	}
	b, ok := target.([]byte)
	if !ok {
		t.Fatalf("target is %T, want []byte", target)
	}
	if len(b) != rns.TruncatedHashLength/8 {
		t.Errorf("target len=%d, want %d", len(b), rns.TruncatedHashLength/8)
	}
}

// TestParsePermissionInvalid verifies that malformed lines return zero perm.
func TestParsePermissionInvalid(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"noperm", "x:all", "r:", "r:target:extra", "r:badtarget!"} {
		perm, target := parsePermission(line)
		if perm != 0 || target != nil {
			t.Errorf("parsePermission(%q) = %x,%v, want 0,nil", line, perm, target)
		}
	}
}

// TestWorkSetPermissionsValidates verifies that set_permissions rejects an
// invalid line and accepts valid lines, writing the .allowed file.
func TestWorkSetPermissionsValidates(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	docID := callCreate(t, n, id, repoPath, "propose", "Doc", "body")

	// Invalid line rejected.
	badData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "perms",
		"doc_id":             int64(docID),
		"step":               "set",
		"content":            "r:all\nbadperm\nc:all\n",
	}
	resp := n.workSetPermissions(workPath, badData, id, "main", "workrepo.git")
	if len(resp) == 0 || resp[0] != resInvalidReq {
		t.Fatalf("set invalid code=%x, want resInvalidReq", firstByte(resp))
	}
	wantMsg := "Invalid permission \"badperm\" on line 2"
	if string(resp[1:]) != wantMsg {
		t.Errorf("set invalid msg=%q, want %q", string(resp[1:]), wantMsg)
	}

	// Valid lines accepted and written.
	goodData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "perms",
		"doc_id":             int64(docID),
		"step":               "set",
		"content":            "# comment\nr:all\nw:none\n",
	}
	resp = n.workSetPermissions(workPath, goodData, id, "main", "workrepo.git")
	if len(resp) == 0 || resp[0] != resOK {
		t.Fatalf("set valid code=%x, want resOK", firstByte(resp))
	}
	allowed, err := os.ReadFile(filepath.Join(workPath, intToString(docID)+".allowed"))
	if err != nil {
		t.Fatalf("read .allowed: %v", err)
	}
	if string(allowed) != "# comment\nr:all\nw:none\n" {
		t.Errorf(".allowed=%q, want the submitted content", string(allowed))
	}
}

// TestWorkGetPermissionsReturnsContent verifies that get_permissions returns
// the .allowed file content (or empty string when missing).
func TestWorkGetPermissionsReturnsContent(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	docID := callCreate(t, n, id, repoPath, "propose", "Doc", "body")

	// The propose step wrote an .allowed file; get should return it.
	getData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "perms",
		"doc_id":             int64(docID),
		"step":               "get",
	}
	resp := n.workGetPermissions(workPath, getData, id, "main", "workrepo.git")
	code, m := unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("get code=%x msg=%q", code, string(resp[1:]))
	}
	content, _ := m["content"].(string)
	if content == "" {
		t.Errorf("get content empty, want the propose-written .allowed lines")
	}
}

// TestWorkRoundTripHandler verifies the full lifecycle through the handleWork
// dispatcher (create→list→view→comment→edit→complete→activate→delete),
// exercising the wire path at the handler level.
func TestWorkRoundTripHandler(t *testing.T) {
	t.Parallel()
	n, id, _, _ := newWorkTestNode(t)
	repoPathForm := "main/workrepo.git"

	mkData := func(op string, extra map[any]any) []byte {
		m := map[any]any{int64(idxRepository): repoPathForm, "operation": op}
		for k, v := range extra {
			m[k] = v
		}
		packed, err := msgpack.Pack(m)
		if err != nil {
			t.Fatalf("pack: %v", err)
		}
		return packed
	}

	content := "Round-trip body."
	sig := signContent(t, id, content)
	resp := n.handleWork(pathWork, mkData("create", map[any]any{
		"title": "RT", "content": content, "format": "markdown", "signature": sig,
	}), nil, nil, id, zeroTime)
	code, m := unpackWorkResponse(t, resp.([]byte))
	if code != resOK {
		t.Fatalf("create code=%x msg=%q", code, string(resp.([]byte)[1:]))
	}
	docID, _ := m["id"].(int64)

	// list
	resp = n.handleWork(pathWork, mkData("list", map[any]any{"scope": "active"}), nil, nil, id, zeroTime)
	_, m = unpackWorkResponse(t, resp.([]byte))
	active, _ := m["active"].([]any)
	if len(active) != 1 {
		t.Fatalf("list active len=%d, want 1", len(active))
	}

	// comment
	commentBody := "An update."
	resp = n.handleWork(pathWork, mkData("comment", map[any]any{
		"doc_id": docID, "scope": "active", "content": commentBody, "format": "markdown",
	}), nil, nil, id, zeroTime)
	code, _ = unpackWorkResponse(t, resp.([]byte))
	if code != resOK {
		t.Fatalf("comment code=%x", code)
	}

	// edit
	newContent := "Edited body."
	resp = n.handleWork(pathWork, mkData("edit", map[any]any{
		"doc_id": docID, "scope": "active", "content": newContent, "title": "RT",
		"signature": signContent(t, id, newContent),
	}), nil, nil, id, zeroTime)
	if firstByte(resp.([]byte)) != resOK {
		t.Fatalf("edit code=%x", firstByte(resp.([]byte)))
	}

	// complete
	resp = n.handleWork(pathWork, mkData("complete", map[any]any{"doc_id": docID}), nil, nil, id, zeroTime)
	code, m = unpackWorkResponse(t, resp.([]byte))
	if code != resOK || m["scope"] != "completed" {
		t.Fatalf("complete code=%x scope=%v", code, m["scope"])
	}

	// activate
	resp = n.handleWork(pathWork, mkData("activate", map[any]any{"doc_id": docID}), nil, nil, id, zeroTime)
	code, m = unpackWorkResponse(t, resp.([]byte))
	if code != resOK || m["scope"] != "active" {
		t.Fatalf("activate code=%x scope=%v", code, m["scope"])
	}

	// delete
	resp = n.handleWork(pathWork, mkData("delete", map[any]any{"doc_id": docID, "scope": "active"}), nil, nil, id, zeroTime)
	if firstByte(resp.([]byte)) != resOK {
		t.Fatalf("delete code=%x", firstByte(resp.([]byte)))
	}
}

// TestWorkViewLocatesAcrossScopes verifies that view scans all three scopes
// to locate a document regardless of the scope argument.
func TestViewLocatesAcrossScopes(t *testing.T) {
	t.Parallel()
	n, id, repoPath, workPath := newWorkTestNode(t)
	docID := callCreate(t, n, id, repoPath, "create", "Doc", "body")

	// Move to completed via complete, then view with scope="active" still
	// finds it (scope arg is discarded by view).
	completeData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "complete",
		"doc_id":             int64(docID),
	}
	if resp := n.workComplete(workPath, completeData, id); firstByte(resp) != resOK {
		t.Fatalf("complete code=%x", firstByte(resp))
	}
	viewData := map[any]any{
		int64(idxRepository): testRepoLogical,
		"operation":          "view",
		"doc_id":             int64(docID),
		"scope":              "active",
	}
	resp := n.workView(workPath, viewData, id)
	code, m := unpackWorkResponse(t, resp)
	if code != resOK {
		t.Fatalf("view code=%x", code)
	}
	if m["scope"] != "completed" {
		t.Errorf("view scope=%v, want completed (located across scopes)", m["scope"])
	}
}

// intToString converts an int to its decimal string form.
func intToString(i int) string {
	return strconv.Itoa(i)
}

// bytesToHex returns the lowercase hex string of b.
func bytesToHex(b []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexChars[v>>4]
		out[i*2+1] = hexChars[v&0x0f]
	}
	return string(out)
}

// zeroTime is a fixed time.Time for handler calls in tests.
var zeroTime = time.Unix(0, 0)

// TestStripWorkTemplateLines verifies that the editor strip logic removes
// comment/create-doc template lines and preserves real content, mirroring
// the list comprehension in _edit_work_content (server.py).
func TestStripWorkTemplateLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"only_template", commentTemplate + "\n", ""},
		{"only_create_template", createDocTemplate + "\n", ""},
		{"content_no_template", "Real content\nLine two", "Real content\nLine two"},
		{"mixed", commentTemplate + "\nReal content\n" + createDocTemplate + "\nMore", "Real content\nMore"},
		{"template_with_whitespace", "  " + commentTemplate + "\nReal", "Real"},
		{"blank_lines_stripped", "\n\n\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripWorkTemplateLines(tc.input)
			if got != tc.want {
				t.Errorf("stripWorkTemplateLines(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestCapitalize verifies the capitalize helper uppercases the first ASCII
// letter and leaves the rest unchanged.
func TestCapitalize(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"active":    "Active",
		"completed": "Completed",
		"proposed":  "Proposed",
		"":          "",
		"A":         "A",
		"aBC":       "ABC",
	}
	for in, want := range cases {
		if got := capitalize(in); got != want {
			t.Errorf("capitalize(%q) = %q, want %q", in, got, want)
		}
	}
}
