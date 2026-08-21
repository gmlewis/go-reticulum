// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newPermTestNode builds an in-memory node with one group "g" and one repo
// "r" in a temp directory, plus two identities. The repo and group perms
// start empty (caller sets them per-test). It returns the node, the repo
// filesystem path, and the two identities.
func newPermTestNode(t *testing.T) (*reticulumGitNode, string, *rns.Identity, *rns.Identity) {
	t.Helper()
	idA, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity A: %v", err)
	}
	idB, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity B: %v", err)
	}
	base := testutils.TempDir(t, "gorngit-perm-")
	repoPath := filepath.Join(base, "r.git")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	// The group .allowed file lives as a SIBLING of the group path (the
	// permission resolver reads/writes <group.path>.allowed), so it is
	// outside the TempDir and would leak in /tmp. Register an explicit
	// cleanup, mirroring prepareGorngitNodeConfig in gorngit-int_test.go.
	t.Cleanup(func() { _ = os.Remove(base + ".allowed") })
	group := &groupInfo{
		name:         "g",
		path:         base,
		repositories: map[string]*repositoryInfo{},
		perms:        permissionLists{},
	}
	group.repositories["r.git"] = &repositoryInfo{
		name:  "r.git",
		group: "g",
		path:  repoPath,
		perms: permissionLists{},
	}
	n := &reticulumGitNode{
		identity:          idA,
		groups:            map[string]*groupInfo{"g": group},
		blockedIdentities: map[string]bool{},
		identityAliases:   map[string]string{},
	}
	return n, repoPath, idA, idB
}

// TestParsePermissionLineValid exercises every valid perm/target form.
func TestParsePermissionLineValid(t *testing.T) {
	t.Parallel()
	n := &reticulumGitNode{identityAliases: map[string]string{}}
	hashHex := "9710b86ba12c42d1d8f30f74fe509286"
	hashBytes, _ := hex.DecodeString(hashHex)
	cases := []struct {
		line       string
		wantPerm   byte
		wantTarget []byte
	}{
		{"r:all", permRead, permTargetAllBytes},
		{"read:everyone", permRead, permTargetAllBytes},
		{"w:nobody", permWrite, permTargetNoneBytes},
		{"write:none", permWrite, permTargetNoneBytes},
		{"rw:all", permReadWrite, permTargetAllBytes},
		{"readwrite:a", permReadWrite, permTargetAllBytes},
		{"c:all", permCreate, permTargetAllBytes},
		{"s:all", permStats, permTargetAllBytes},
		{"rel:all", permRelease, permTargetAllBytes},
		{"i:all", permInteract, permTargetAllBytes},
		{"p:all", permPropose, permTargetAllBytes},
		{"adm:all", permAdmin, permTargetAllBytes},
		{"r:" + hashHex, permRead, hashBytes},
		{"adm:" + hashHex, permAdmin, hashBytes},
	}
	for _, c := range cases {
		perm, target := n.parsePermissionLine(c.line)
		if perm != c.wantPerm {
			t.Errorf("%q: perm=%x want %x", c.line, perm, c.wantPerm)
		}
		if string(target) != string(c.wantTarget) {
			t.Errorf("%q: target=%x want %x", c.line, target, c.wantTarget)
		}
	}
}

// TestParsePermissionLineInvalid verifies rejection of malformed lines.
func TestParsePermissionLineInvalid(t *testing.T) {
	t.Parallel()
	n := &reticulumGitNode{identityAliases: map[string]string{}}
	invalid := []string{
		"r",           // no colon
		"r:a:b",       // too many colons
		"x:all",       // unknown perm
		"r:zzzz",      // not a mnemonic, not 32 hex
		"r:9710b86ba", // wrong length hash
		"",            // empty
		"r:GHIJKL",    // non-hex, non-mnemonic
	}
	for _, line := range invalid {
		perm, target := n.parsePermissionLine(line)
		if perm != 0 || target != nil {
			t.Errorf("parsePermissionLine(%q) = (%x, %v), want (0, nil)", line, perm, target)
		}
	}
}

// TestParsePermissionLineAlias verifies alias resolution expands the target
// to the configured 32-hex hash.
func TestParsePermissionLineAlias(t *testing.T) {
	t.Parallel()
	hashHex := "d09285e660cfe27cee6d9a0beb58b7e0"
	n := &reticulumGitNode{identityAliases: map[string]string{"alice": hashHex}}
	hashBytes, _ := hex.DecodeString(hashHex)
	perm, target := n.parsePermissionLine("w:alice")
	if perm != permWrite {
		t.Errorf("perm=%x want %x", perm, permWrite)
	}
	if string(target) != string(hashBytes) {
		t.Errorf("target=%x want %x", target, hashBytes)
	}
}

// TestParsePermissionLineAliasUnknown verifies an unknown alias falls
// through to hash-length validation and is rejected.
func TestParsePermissionLineAliasUnknown(t *testing.T) {
	t.Parallel()
	n := &reticulumGitNode{identityAliases: map[string]string{}}
	perm, target := n.parsePermissionLine("r:unknown_alias")
	if perm != 0 || target != nil {
		t.Errorf("unknown alias = (%x, %v), want (0, nil)", perm, target)
	}
}

// TestPermissionsFromAllowedInput verifies multi-line parsing, comment and
// blank skipping, PERM_READWRITE adding to both read+write, and dedup.
func TestPermissionsFromAllowedInput(t *testing.T) {
	t.Parallel()
	n := &reticulumGitNode{identityAliases: map[string]string{}}
	hashA := "9710b86ba12c42d1d8f30f74fe509286"
	hashB := "d09285e660cfe27cee6d9a0beb58b7e0"
	input := "# comment\n" +
		"r:all\n" +
		"\n" +
		"r:all\n" + // dedup with the first r:all
		"rw:" + hashA + "\n" +
		"w:" + hashB + "\n" +
		"adm:" + hashA + "\n"
	perms := n.permissionsFromAllowedInput(input)
	if len(perms.read) != 2 {
		t.Errorf("read len=%d want 2 (all + hashA)", len(perms.read))
	}
	if len(perms.write) != 2 {
		t.Errorf("write len=%d want 2 (hashA + hashB)", len(perms.write))
	}
	if len(perms.admin) != 1 {
		t.Errorf("admin len=%d want 1", len(perms.admin))
	}
	if !permListContains(perms.read, permTargetAllBytes) {
		t.Errorf("read missing TGT_ALL")
	}
	hashABytes, _ := hex.DecodeString(hashA)
	if !permListContains(perms.read, hashABytes) {
		t.Errorf("read missing hashA (from rw)")
	}
	if !permListContains(perms.write, hashABytes) {
		t.Errorf("write missing hashA (from rw)")
	}
	hashBBytes, _ := hex.DecodeString(hashB)
	if !permListContains(perms.write, hashBBytes) {
		t.Errorf("write missing hashB")
	}
	if !permListContains(perms.admin, hashABytes) {
		t.Errorf("admin missing hashA")
	}
}

// TestResolvePermissionTargetAll verifies a repo TGT_ALL grants all perms,
// and an empty repo falls through to group TGT_ALL.
func TestResolvePermissionTargetAll(t *testing.T) {
	t.Parallel()
	n, _, idA, _ := newPermTestNode(t)
	group := n.groups["g"]
	repo := group.repositories["r.git"]
	repo.perms = openPermissionLists()
	for _, perm := range []byte{permRead, permWrite, permCreate, permRelease, permInteract, permPropose, permAdmin} {
		if !n.resolvePermission(idA, "g", "r.git", perm) {
			t.Errorf("repo TGT_ALL perm %x: expected true", perm)
		}
	}
	// Empty repo perms fall through to group TGT_ALL.
	repo.perms = permissionLists{}
	group.perms = openPermissionLists()
	if !n.resolvePermission(idA, "g", "r.git", permRead) {
		t.Errorf("group TGT_ALL fallback: expected true")
	}
}

// TestResolvePermissionTargetNone verifies TGT_NONE denies.
func TestResolvePermissionTargetNone(t *testing.T) {
	t.Parallel()
	n, _, idA, _ := newPermTestNode(t)
	repo := n.groups["g"].repositories["r.git"]
	repo.perms.read = [][]byte{permTargetNoneBytes}
	if n.resolvePermission(idA, "g", "r.git", permRead) {
		t.Errorf("repo TGT_NONE read: expected false")
	}
}

// TestResolvePermissionHashMatch verifies an explicit hash match grants.
func TestResolvePermissionHashMatch(t *testing.T) {
	t.Parallel()
	n, _, idA, idB := newPermTestNode(t)
	repo := n.groups["g"].repositories["r.git"]
	repo.perms.read = [][]byte{idA.Hash}
	if !n.resolvePermission(idA, "g", "r.git", permRead) {
		t.Errorf("hash match read: expected true")
	}
	if n.resolvePermission(idB, "g", "r.git", permRead) {
		t.Errorf("non-matching hash read: expected false")
	}
}

// TestResolvePermissionAdminFallback verifies a hash in the repo admin list
// grants every perm even when the specific perm list is empty.
func TestResolvePermissionAdminFallback(t *testing.T) {
	t.Parallel()
	n, _, idA, idB := newPermTestNode(t)
	repo := n.groups["g"].repositories["r.git"]
	repo.perms.admin = [][]byte{idA.Hash}
	if !n.resolvePermission(idA, "g", "r.git", permRead) {
		t.Errorf("admin fallback read: expected true")
	}
	if n.resolvePermission(idB, "g", "r.git", permRead) {
		t.Errorf("non-admin read: expected false")
	}
}

// TestResolvePermissionRepoBlocksGroup verifies a non-empty repo perm list
// (that does not match) blocks the group fallback.
func TestResolvePermissionRepoBlocksGroup(t *testing.T) {
	t.Parallel()
	n, _, idA, idB := newPermTestNode(t)
	group := n.groups["g"]
	repo := group.repositories["r.git"]
	// Repo read has idB (not idA); group read has TGT_ALL. Because the
	// repo read list is non-empty and idA is not in it, the group fallback
	// is blocked and idA is denied.
	repo.perms.read = [][]byte{idB.Hash}
	group.perms.read = [][]byte{permTargetAllBytes}
	if n.resolvePermission(idA, "g", "r.git", permRead) {
		t.Errorf("non-empty repo list should block group TGT_ALL for idA")
	}
}

// TestResolvePermissionBlocked verifies a blocked identity is denied even
// when perms would otherwise grant.
func TestResolvePermissionBlocked(t *testing.T) {
	t.Parallel()
	n, _, idA, _ := newPermTestNode(t)
	n.blockedIdentities[hex.EncodeToString(idA.Hash)] = true
	n.groups["g"].repositories["r.git"].perms = openPermissionLists()
	if n.resolvePermission(idA, "g", "r.git", permRead) {
		t.Errorf("blocked identity should be denied")
	}
}

// TestResolvePermissionMissing verifies a missing group or repo denies, and
// a nil remote identity denies.
func TestResolvePermissionMissing(t *testing.T) {
	t.Parallel()
	n, _, idA, _ := newPermTestNode(t)
	if n.resolvePermission(idA, "nope", "r.git", permRead) {
		t.Errorf("missing group should deny")
	}
	if n.resolvePermission(idA, "g", "nope.git", permRead) {
		t.Errorf("missing repo should deny")
	}
	if n.resolvePermission(nil, "g", "r.git", permRead) {
		t.Errorf("nil identity should deny")
	}
}

// TestResolveGroupPermission verifies the group-only ladder (no blocked
// check, no repo).
func TestResolveGroupPermission(t *testing.T) {
	t.Parallel()
	n, _, idA, idB := newPermTestNode(t)
	group := n.groups["g"]
	group.perms.read = [][]byte{permTargetAllBytes}
	group.perms.admin = [][]byte{idA.Hash}
	if !n.resolveGroupPermission(idA, "g", permRead) {
		t.Errorf("group TGT_ALL read: expected true")
	}
	if !n.resolveGroupPermission(idA, "g", permAdmin) {
		t.Errorf("group admin hash: expected true")
	}
	if n.resolveGroupPermission(idB, "g", permAdmin) {
		t.Errorf("non-admin group admin: expected false")
	}
	// Blocked identities are NOT checked at the group level.
	n.blockedIdentities[hex.EncodeToString(idA.Hash)] = true
	if !n.resolveGroupPermission(idA, "g", permRead) {
		t.Errorf("group perm should ignore blocked list")
	}
}

// TestResolveDocPermissionDocAllowed verifies a doc .allowed with an
// explicit hash grants that hash. With no repo/group perms, a non-matching
// identity is denied (the doc level does not block, but the empty repo and
// group lists deny the fallback).
func TestResolveDocPermissionDocAllowed(t *testing.T) {
	t.Parallel()
	n, repoPath, idA, idB := newPermTestNode(t)
	workPath := repoPath + ".work"
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	docID := 1
	allowedPath := filepath.Join(workPath, "1.allowed")
	if err := os.WriteFile(allowedPath, []byte("r:"+hex.EncodeToString(idA.Hash)+"\n"), 0o644); err != nil {
		t.Fatalf("write doc allowed: %v", err)
	}
	// Repo and group perms left empty: the doc .allowed is the only grant.
	if !n.resolveDocPermission(idA, "g", "r.git", docID, permRead) {
		t.Errorf("doc hash match read: expected true")
	}
	if n.resolveDocPermission(idB, "g", "r.git", docID, permRead) {
		t.Errorf("doc non-matching hash with empty repo/group: expected false")
	}
}

// TestResolveDocPermissionFallthrough verifies an empty doc perm list
// (missing .allowed) falls through to the repo level.
func TestResolveDocPermissionFallthrough(t *testing.T) {
	t.Parallel()
	n, repoPath, idA, _ := newPermTestNode(t)
	workPath := repoPath + ".work"
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	docID := 2
	// No doc .allowed: falls through to repo TGT_ALL.
	n.groups["g"].repositories["r.git"].perms = openPermissionLists()
	if !n.resolveDocPermission(idA, "g", "r.git", docID, permRead) {
		t.Errorf("missing doc allowed should fall through to repo")
	}
}

// TestIsBlocked verifies the hex-keyed blocked map.
func TestIsBlocked(t *testing.T) {
	t.Parallel()
	n, _, idA, _ := newPermTestNode(t)
	n.blockedIdentities[hex.EncodeToString(idA.Hash)] = true
	if !n.isBlocked(idA.Hash) {
		t.Errorf("idA should be blocked")
	}
	if n.isBlocked(nil) {
		t.Errorf("nil hash should not be blocked")
	}
	if n.isBlocked([]byte{1, 2, 3}) {
		t.Errorf("unregistered hash should not be blocked")
	}
}

// TestResolveIdentityAlias verifies alias lookup and pass-through.
func TestResolveIdentityAlias(t *testing.T) {
	t.Parallel()
	hashHex := "d09285e660cfe27cee6d9a0beb58b7e0"
	n := &reticulumGitNode{identityAliases: map[string]string{"alice": hashHex}}
	if got := n.resolveIdentityAlias("alice"); got != hashHex {
		t.Errorf("alias alice = %q want %q", got, hashHex)
	}
	if got := n.resolveIdentityAlias("all"); got != "all" {
		t.Errorf("mnemonic all = %q want %q", got, "all")
	}
	if got := n.resolveIdentityAlias(hashHex); got != hashHex {
		t.Errorf("hash passthrough = %q want %q", got, hashHex)
	}
}

// TestWriteRepoCreatePermissions verifies the initial .allowed content is
// "adm:<hex>" and is written to <repoPath>.allowed.
func TestWriteRepoCreatePermissions(t *testing.T) {
	t.Parallel()
	base := testutils.TempDir(t, "gorngit-writeperm-")
	repoPath := filepath.Join(base, "r.git")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	creatorHex := "9710b86ba12c42d1d8f30f74fe509286"
	if err := writeRepoCreatePermissions(repoPath, creatorHex); err != nil {
		t.Fatalf("writeRepoCreatePermissions: %v", err)
	}
	data, err := os.ReadFile(repoPath + ".allowed")
	if err != nil {
		t.Fatalf("read .allowed: %v", err)
	}
	want := "adm:" + creatorHex
	if string(data) != want {
		t.Errorf(".allowed = %q want %q", string(data), want)
	}
}

// TestValidateAllowedContent verifies valid content passes and an invalid
// line returns the Python-format error.
func TestValidateAllowedContent(t *testing.T) {
	t.Parallel()
	n := &reticulumGitNode{identityAliases: map[string]string{}}
	if err := validateAllowedContent(n, "# header\nr:all\nw:9710b86ba12c42d1d8f30f74fe509286\n\n"); err != nil {
		t.Errorf("valid content: unexpected error: %v", err)
	}
	err := validateAllowedContent(n, "r:all\nbogus:all\n")
	if err == nil {
		t.Fatal("invalid content: expected error")
	}
	want := `Invalid permission "bogus:all" on line 2`
	if err.Error() != want {
		t.Errorf("error = %q want %q", err.Error(), want)
	}
}

// TestHandlePermsGroupGetSet verifies the gperms get/set round-trip at the
// handler level: a set writes the .allowed file and a get returns it.
func TestHandlePermsGroupGetSet(t *testing.T) {
	n, _, idA, _ := newPermTestNode(t)
	group := n.groups["g"]
	// Grant group READ + ADMIN to idA so handlePerms admits it.
	group.perms.read = [][]byte{permTargetAllBytes}
	group.perms.admin = [][]byte{idA.Hash}

	setData := map[any]any{
		int64(idxGroup): "g",
		"operation":     "gperms",
		"step":          "set",
		"content":       "r:all\nw:all\nadm:all\n",
	}
	setPacked := packPermRequest(t, setData)
	resp := n.handlePerms(pathPerms, setPacked, nil, nil, idA, timeZero())
	if firstByte(resp.([]byte)) != resOK {
		t.Fatalf("set code=%x want resOK", firstByte(resp.([]byte)))
	}
	// The .allowed file should now hold the set content.
	data, err := os.ReadFile(group.path + ".allowed")
	if err != nil {
		t.Fatalf("read group .allowed: %v", err)
	}
	if string(data) != "r:all\nw:all\nadm:all\n" {
		t.Errorf("group .allowed = %q want %q", string(data), "r:all\nw:all\nadm:all\n")
	}

	getData := map[any]any{
		int64(idxGroup): "g",
		"operation":     "gperms",
		"step":          "get",
	}
	getPacked := packPermRequest(t, getData)
	resp = n.handlePerms(pathPerms, getPacked, nil, nil, idA, timeZero())
	b := resp.([]byte)
	if firstByte(b) != resOK {
		t.Fatalf("get code=%x want resOK", firstByte(b))
	}
	content := unpackPermContent(t, b[1:])
	if content != "r:all\nw:all\nadm:all\n" {
		t.Errorf("get content=%q want %q", content, "r:all\nw:all\nadm:all\n")
	}
}

// TestHandlePermsRepoGetSet verifies the rperms get/set round-trip at the
// handler level.
func TestHandlePermsRepoGetSet(t *testing.T) {
	n, repoPath, idA, _ := newPermTestNode(t)
	repo := n.groups["g"].repositories["r.git"]
	repo.perms.read = [][]byte{permTargetAllBytes}
	repo.perms.admin = [][]byte{idA.Hash}

	setData := map[any]any{
		int64(idxRepository): "g/r.git",
		"operation":          "rperms",
		"step":               "set",
		"content":            "r:all\nw:all\nadm:all\n",
	}
	setPacked := packPermRequest(t, setData)
	resp := n.handlePerms(pathPerms, setPacked, nil, nil, idA, timeZero())
	if firstByte(resp.([]byte)) != resOK {
		t.Fatalf("set code=%x want resOK", firstByte(resp.([]byte)))
	}
	data, err := os.ReadFile(repoPath + ".allowed")
	if err != nil {
		t.Fatalf("read repo .allowed: %v", err)
	}
	if string(data) != "r:all\nw:all\nadm:all\n" {
		t.Errorf("repo .allowed = %q want %q", string(data), "r:all\nw:all\nadm:all\n")
	}

	getData := map[any]any{
		int64(idxRepository): "g/r.git",
		"operation":          "rperms",
		"step":               "get",
	}
	getPacked := packPermRequest(t, getData)
	resp = n.handlePerms(pathPerms, getPacked, nil, nil, idA, timeZero())
	b := resp.([]byte)
	if firstByte(b) != resOK {
		t.Fatalf("get code=%x want resOK", firstByte(b))
	}
	content := unpackPermContent(t, b[1:])
	if content != "r:all\nw:all\nadm:all\n" {
		t.Errorf("get content=%q want %q", content, "r:all\nw:all\nadm:all\n")
	}
}

// TestHandlePermsInvalidLine verifies a set with an invalid permission line
// is rejected with resInvalidReq.
func TestHandlePermsInvalidLine(t *testing.T) {
	n, _, idA, _ := newPermTestNode(t)
	group := n.groups["g"]
	group.perms.read = [][]byte{permTargetAllBytes}
	group.perms.admin = [][]byte{idA.Hash}
	setData := map[any]any{
		int64(idxGroup): "g",
		"operation":     "gperms",
		"step":          "set",
		"content":       "bogus:all\n",
	}
	setPacked := packPermRequest(t, setData)
	resp := n.handlePerms(pathPerms, setPacked, nil, nil, idA, timeZero())
	if firstByte(resp.([]byte)) != resInvalidReq {
		t.Errorf("invalid line code=%x want resInvalidReq", firstByte(resp.([]byte)))
	}
}

// TestHandlePermsDenied verifies a non-admin client gets resDisallowed (it
// has READ but not ADMIN, so existence is not hidden).
func TestHandlePermsDenied(t *testing.T) {
	n, _, idA, idB := newPermTestNode(t)
	group := n.groups["g"]
	group.perms.read = [][]byte{permTargetAllBytes}
	group.perms.admin = [][]byte{idA.Hash} // only idA is admin
	getData := map[any]any{
		int64(idxGroup): "g",
		"operation":     "gperms",
		"step":          "get",
	}
	getPacked := packPermRequest(t, getData)
	resp := n.handlePerms(pathPerms, getPacked, nil, nil, idB, timeZero())
	b := resp.([]byte)
	if firstByte(b) != resDisallowed {
		t.Errorf("non-admin get code=%x want resDisallowed", firstByte(b))
	}
}

// TestHandlePermsNoReadHidesExistence verifies a client without READ gets
// resNotFound (existence hidden).
func TestHandlePermsNoReadHidesExistence(t *testing.T) {
	n, _, _, idB := newPermTestNode(t)
	// No perms set: idB has neither READ nor ADMIN.
	getData := map[any]any{
		int64(idxGroup): "g",
		"operation":     "gperms",
		"step":          "get",
	}
	getPacked := packPermRequest(t, getData)
	resp := n.handlePerms(pathPerms, getPacked, nil, nil, idB, timeZero())
	b := resp.([]byte)
	if firstByte(b) != resNotFound {
		t.Errorf("no-read get code=%x want resNotFound", firstByte(b))
	}
}

// packPermRequest packs a perms request map for handler invocation.
func packPermRequest(t *testing.T, m map[any]any) []byte {
	t.Helper()
	p, err := msgpack.Pack(m)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return p
}

// unpackPermContent extracts the "content" string from a perms get
// response payload (the bytes after the status byte).
func unpackPermContent(t *testing.T, payload []byte) string {
	t.Helper()
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(payload)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("expected map, got %T", unpacked)
	}
	s, _ := m["content"].(string)
	return s
}

// timeZero returns a zero time.Time for handler signatures that require it.
func timeZero() time.Time { return time.Time{} }
