// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// permissions.go implements the gorngit permissions core: parsing .allowed
// files, resolving permissions against group/repo/doc perm lists, and the
// /mgmt/perms request handler. It mirrors parse_permission,
// permissions_from_allowed_input, resolve_permission, resolve_group_permission,
// resolve_doc_permission, and handle_perms in
// RNS/Utilities/rngit/server.py (rngit v1.4.2).

package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// permTargetNoneBytes is the single-byte []byte form of TGT_NONE (0x01),
// used as an entry in perm lists to deny a permission to everyone.
var permTargetNoneBytes = []byte{permTargetNone}

// permTargetAllBytes is the single-byte []byte form of TGT_ALL (0x02),
// used as an entry in perm lists to grant a permission to everyone.
var permTargetAllBytes = []byte{permTargetAll}

// allPerms is the ordered list of permission names, mirroring ALL_PERMS
// (server.py). Used by config loading to clear/iterate perm lists.
var allPerms = []string{"read", "write", "create", "stats", "release", "interact", "propose", "admin"}

// permissionLists holds the eight parsed permission target lists for a
// group or repository, mirroring the per-group/repo "read"/"write"/...
// lists in server.py. Each list entry is either permTargetNoneBytes,
// permTargetAllBytes, or a 16-byte identity hash.
type permissionLists struct {
	read     [][]byte
	write    [][]byte
	create   [][]byte
	stats    [][]byte
	release  [][]byte
	interact [][]byte
	propose  [][]byte
	admin    [][]byte
}

// listFor returns the perm list for the given perm byte, mirroring the
// if/elif ladder in resolve_permission (server.py). Returns nil for an
// unknown perm.
func (p *permissionLists) listFor(perm byte) [][]byte {
	switch perm {
	case permRead:
		return p.read
	case permWrite:
		return p.write
	case permCreate:
		return p.create
	case permStats:
		return p.stats
	case permRelease:
		return p.release
	case permInteract:
		return p.interact
	case permPropose:
		return p.propose
	case permAdmin:
		return p.admin
	}
	return nil
}

// permListContains reports whether list contains an entry bytes-equal to
// target. TGT_NONE/TGT_ALL are single-byte entries; identity hashes are
// 16-byte entries, so there is no collision between the two forms.
func permListContains(list [][]byte, target []byte) bool {
	for _, e := range list {
		if bytes.Equal(e, target) {
			return true
		}
	}
	return false
}

// resolveIdentityAlias resolves an alias to its hex-hash form, mirroring
// __resolve_identity_alias (server.py). ALL_TGTS mnemonics and valid
// 32-hex-char hashes are returned unchanged; otherwise the alias is looked
// up in identityAliases and its hex hash returned (or the input returned
// unchanged when the alias is unknown).
func (n *reticulumGitNode) resolveIdentityAlias(alias string) string {
	lower := strings.ToLower(alias)
	if isAllTargetMnemonic(lower) {
		return alias
	}
	if len(alias) == rns.TruncatedHashLength/8*2 {
		if _, err := hex.DecodeString(alias); err == nil {
			return alias
		}
	}
	if hexHash, ok := n.identityAliases[alias]; ok {
		return hexHash
	}
	return alias
}

// isAllTargetMnemonic reports whether s is one of the ALL_TGTS
// mnemonics (n/none/nobody/a/all/everyone), case-insensitive.
func isAllTargetMnemonic(s string) bool {
	switch s {
	case "n", "none", "nobody", "a", "all", "everyone":
		return true
	}
	return false
}

// parsePermissionLine parses a single "perm:target" permission line,
// mirroring parse_permission (server.py). It returns the perm byte (0
// when unknown) and the target as a []byte: permTargetNoneBytes,
// permTargetAllBytes, or a 16-byte identity hash. Returns (0, nil) on
// any validation failure. Alias resolution uses the node's
// identityAliases map.
func (n *reticulumGitNode) parsePermissionLine(permissionString string) (byte, []byte) {
	comps := strings.Split(permissionString, ":")
	if len(comps) != 2 {
		return 0, nil
	}
	permStr := strings.ToLower(comps[0])
	target := comps[1]
	target = n.resolveIdentityAlias(target)

	var perm byte
	switch permStr {
	case "r", "read":
		perm = permRead
	case "w", "write":
		perm = permWrite
	case "rw", "readwrite":
		perm = permReadWrite
	case "c", "create":
		perm = permCreate
	case "s", "stats":
		perm = permStats
	case "rel", "release":
		perm = permRelease
	case "i", "interact":
		perm = permInteract
	case "p", "propose":
		perm = permPropose
	case "adm", "admin":
		perm = permAdmin
	default:
		return 0, nil
	}

	targetLower := strings.ToLower(target)
	switch targetLower {
	case "n", "none", "nobody":
		return perm, permTargetNoneBytes
	case "a", "all", "everyone":
		return perm, permTargetAllBytes
	}
	if len(target) == rns.TruncatedHashLength/8*2 {
		hashBytes, err := hex.DecodeString(target)
		if err != nil {
			return 0, nil
		}
		return perm, hashBytes
	}
	return 0, nil
}

// permissionsFromAllowedInput parses .allowed file text into eight perm
// lists, mirroring permissions_from_allowed_input (server.py). Blank
// lines and lines starting with "#" are skipped. PERM_READWRITE adds the
// target to both read and write lists. Entries are deduplicated per list.
func (n *reticulumGitNode) permissionsFromAllowedInput(allowedInput string) permissionLists {
	var perms permissionLists
	for _, entry := range strings.Split(allowedInput, "\n") {
		stripped := strings.TrimSpace(entry)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		perm, target := n.parsePermissionLine(stripped)
		if perm == 0 || target == nil {
			continue
		}
		addRead := perm == permRead || perm == permReadWrite
		addWrite := perm == permWrite || perm == permReadWrite
		if addRead && !permListContains(perms.read, target) {
			perms.read = append(perms.read, target)
		}
		if addWrite && !permListContains(perms.write, target) {
			perms.write = append(perms.write, target)
		}
		if perm == permCreate && !permListContains(perms.create, target) {
			perms.create = append(perms.create, target)
		}
		if perm == permStats && !permListContains(perms.stats, target) {
			perms.stats = append(perms.stats, target)
		}
		if perm == permRelease && !permListContains(perms.release, target) {
			perms.release = append(perms.release, target)
		}
		if perm == permInteract && !permListContains(perms.interact, target) {
			perms.interact = append(perms.interact, target)
		}
		if perm == permPropose && !permListContains(perms.propose, target) {
			perms.propose = append(perms.propose, target)
		}
		if perm == permAdmin && !permListContains(perms.admin, target) {
			perms.admin = append(perms.admin, target)
		}
	}
	return perms
}

// applyPermissionEntry adds a single parsed (perm, target) entry to the
// appropriate list(s) in perms, with dedup, mirroring the per-perm
// append block in update_group_permissions / permissions_from_allowed_input
// (server.py). PERM_READWRITE adds to both read and write.
func applyPermissionEntry(perms *permissionLists, perm byte, target []byte) {
	if perm == 0 || target == nil {
		return
	}
	if (perm == permRead || perm == permReadWrite) && !permListContains(perms.read, target) {
		perms.read = append(perms.read, target)
	}
	if (perm == permWrite || perm == permReadWrite) && !permListContains(perms.write, target) {
		perms.write = append(perms.write, target)
	}
	if perm == permCreate && !permListContains(perms.create, target) {
		perms.create = append(perms.create, target)
	}
	if perm == permStats && !permListContains(perms.stats, target) {
		perms.stats = append(perms.stats, target)
	}
	if perm == permRelease && !permListContains(perms.release, target) {
		perms.release = append(perms.release, target)
	}
	if perm == permInteract && !permListContains(perms.interact, target) {
		perms.interact = append(perms.interact, target)
	}
	if perm == permPropose && !permListContains(perms.propose, target) {
		perms.propose = append(perms.propose, target)
	}
	if perm == permAdmin && !permListContains(perms.admin, target) {
		perms.admin = append(perms.admin, target)
	}
}

// readAllowedInput reads the .allowed file at allowedPath, honoring the
// executable-aware behavior: when the file has the executable bit set it
// is run as a subprocess and its stdout is parsed, mirroring
// update_group_permissions / load_repository (server.py). Returns the
// parsed text and nil on success, or "" and an error when the file
// cannot be read or the subprocess fails.
func readAllowedInput(allowedPath string) (string, error) {
	fi, err := os.Stat(allowedPath)
	if err != nil {
		return "", err
	}
	if fi.Mode()&0o111 != 0 {
		cmd := exec.Command(allowedPath)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("could not run allowed script %s: %w", allowedPath, err)
		}
		return stdout.String(), nil
	}
	data, err := os.ReadFile(allowedPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// resolvePermission reports whether remoteIdentity holds perm on
// group/repo, mirroring resolve_permission (server.py). The precedence is:
// blocked → false; missing group/repo → false; repo-level TGT_NONE → false;
// repo-level TGT_ALL → true; repo-level hash match → true; repo-admin hash
// match → true; non-empty repo perms without match → false (blocks group
// fallback); then group-level TGT_NONE/ALL/hash/admin ladder; else false.
func (n *reticulumGitNode) resolvePermission(remoteIdentity *rns.Identity, groupName, repoName string, perm byte) bool {
	if remoteIdentity == nil {
		return false
	}
	remoteHash := remoteIdentity.Hash
	if n.isBlocked(remoteHash) {
		return false
	}
	group, ok := n.groups[groupName]
	if !ok {
		return false
	}
	repo, ok := group.repositories[repoName]
	if !ok {
		return false
	}
	repoPerms := repo.perms.listFor(perm)
	groupPerms := group.perms.listFor(perm)
	repoAdmins := repo.perms.admin
	groupAdmins := group.perms.admin

	if permListContains(repoPerms, permTargetNoneBytes) {
		return false
	}
	if permListContains(repoPerms, permTargetAllBytes) {
		return true
	}
	if permListContains(repoPerms, remoteHash) {
		return true
	}
	if permListContains(repoAdmins, remoteHash) {
		return true
	}
	if len(repoPerms) > 0 {
		return false
	}
	if permListContains(groupPerms, permTargetNoneBytes) {
		return false
	}
	if permListContains(groupPerms, permTargetAllBytes) {
		return true
	}
	if permListContains(groupPerms, remoteHash) {
		return true
	}
	if permListContains(groupAdmins, remoteHash) {
		return true
	}
	return false
}

// resolveGroupPermission reports whether remoteIdentity holds perm at the
// group level (no repo), mirroring resolve_group_permission (server.py).
// No blocked-identity check is applied (matching Python). Precedence:
// missing group → false; TGT_NONE → false; TGT_ALL → true; hash match →
// true; group-admin hash match → true; else false.
func (n *reticulumGitNode) resolveGroupPermission(remoteIdentity *rns.Identity, groupName string, perm byte) bool {
	if remoteIdentity == nil {
		return false
	}
	remoteHash := remoteIdentity.Hash
	group, ok := n.groups[groupName]
	if !ok {
		return false
	}
	groupPerms := group.perms.listFor(perm)
	groupAdmins := group.perms.admin

	if permListContains(groupPerms, permTargetNoneBytes) {
		return false
	}
	if permListContains(groupPerms, permTargetAllBytes) {
		return true
	}
	if permListContains(groupPerms, remoteHash) {
		return true
	}
	if permListContains(groupAdmins, remoteHash) {
		return true
	}
	return false
}

// resolveDocPermission reports whether remoteIdentity holds perm on the
// work document docID under group/repo, mirroring resolve_doc_permission
// (server.py). It reads <repoPath>.work/<docID>.allowed for doc-level
// perms, then falls back to repo-level, then group-level, with the same
// non-empty-repo-perms-blocks-group rule. Supports READ/WRITE/CREATE/
// INTERACT/PROPOSE/ADMIN (no STATS/RELEASE at doc level).
func (n *reticulumGitNode) resolveDocPermission(remoteIdentity *rns.Identity, groupName, repoName string, docID int, perm byte) bool {
	if remoteIdentity == nil {
		return false
	}
	remoteHash := remoteIdentity.Hash
	group, ok := n.groups[groupName]
	if !ok {
		return false
	}
	repo, ok := group.repositories[repoName]
	if !ok {
		return false
	}
	workPath := repo.path + ".work"
	docAllowedPath := workPath + "/" + fmt.Sprintf("%d.allowed", docID)

	var docPerms permissionLists
	if isFile(docAllowedPath) {
		if input, err := readAllowedInput(docAllowedPath); err == nil {
			docPerms = n.permissionsFromAllowedInput(input)
		}
	}

	repoPerms := repo.perms.listFor(perm)
	groupPerms := group.perms.listFor(perm)
	docPermList := docPerms.listFor(perm)
	repoAdmins := repo.perms.admin
	groupAdmins := group.perms.admin
	docAdmins := docPerms.admin

	if permListContains(docPermList, permTargetNoneBytes) {
		return false
	}
	if permListContains(docPermList, permTargetAllBytes) {
		return true
	}
	if permListContains(docPermList, remoteHash) {
		return true
	}
	if permListContains(docAdmins, remoteHash) {
		return true
	}
	if permListContains(repoPerms, permTargetNoneBytes) {
		return false
	}
	if permListContains(repoPerms, permTargetAllBytes) {
		return true
	}
	if permListContains(repoPerms, remoteHash) {
		return true
	}
	if permListContains(repoAdmins, remoteHash) {
		return true
	}
	if len(repoPerms) > 0 {
		return false
	}
	if permListContains(groupPerms, permTargetNoneBytes) {
		return false
	}
	if permListContains(groupPerms, permTargetAllBytes) {
		return true
	}
	if permListContains(groupPerms, remoteHash) {
		return true
	}
	if permListContains(groupAdmins, remoteHash) {
		return true
	}
	return false
}

// isBlocked reports whether hash is in the node's blocked_identities map,
// mirroring the `remote_hash in self.blocked_identities` check in
// resolve_permission (server.py).
func (n *reticulumGitNode) isBlocked(hash []byte) bool {
	if n.blockedIdentities == nil || len(hash) == 0 {
		return false
	}
	return n.blockedIdentities[hex.EncodeToString(hash)]
}

// repoCreatePermsTemplate is the initial .allowed content for a newly
// created/forked/mirrored repo, making the creator sole admin, mirroring
// REPO_CREATE_PERMS_TEMPLATE (server.py).
const repoCreatePermsTemplate = "adm:{IDENTITY_HASH}"

// writeRepoCreatePermissions writes the initial .allowed file at
// <repoPath>.allowed granting admin to creatorHex, mirroring the
// tmp+rename block in handle_create / _handle_remote_clone (server.py).
func writeRepoCreatePermissions(repoPath, creatorHex string) error {
	allowedPath := repoPath + ".allowed"
	tmpPath := allowedPath + ".tmp"
	content := strings.Replace(repoCreatePermsTemplate, "{IDENTITY_HASH}", creatorHex, 1)
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, allowedPath)
}

// handlePerms is the /mgmt/perms request handler, mirroring handle_perms
// (server.py). It dispatches to group perms (gperms) or repository perms
// (rperms) based on the "operation" key.
func (n *reticulumGitNode) handlePerms(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	if remoteIdentity == nil {
		return append([]byte{resDisallowed}, []byte("Not identified")...)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	operationVal, _ := getMapValue(m, "operation")
	operation, _ := operationVal.(string)
	if operation == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	switch operation {
	case "gperms":
		groupVal, ok := getMapValue(m, idxGroup)
		if !ok {
			return append([]byte{resInvalidReq}, []byte("No group specified")...)
		}
		groupName, _ := groupVal.(string)
		groupName = parseRequestGroupPath(groupName)
		return n.groupPermissionsHandler(groupName, m, remoteIdentity)
	case "rperms":
		repoVal, ok := getMapValue(m, idxRepository)
		if !ok {
			return append([]byte{resInvalidReq}, []byte("No repository specified")...)
		}
		repoPath, _ := repoVal.(string)
		groupName, repoName := parseRequestRepositoryPath(repoPath)
		return n.repositoryPermissionsHandler(groupName, repoName, m, remoteIdentity)
	default:
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
}

// parseRequestGroupPath validates a single-component group path, mirroring
// parse_request_group_path (server.py). Returns the group name or "" when
// the path is not a single component or exceeds the length limit.
func parseRequestGroupPath(path string) string {
	components := strings.Split(path, "/")
	if len(components) != 1 {
		return ""
	}
	if len(components[0]) > requestPathLimit {
		return ""
	}
	return components[0]
}

// groupPermissionsHandler handles the gperms operation, mirroring
// _group_permissions (server.py). Requires group READ + ADMIN.
func (n *reticulumGitNode) groupPermissionsHandler(groupName string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	readAccess := n.resolveGroupPermission(remoteIdentity, groupName, permRead)
	adminAccess := n.resolveGroupPermission(remoteIdentity, groupName, permAdmin)
	if !readAccess {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	if !adminAccess {
		return append([]byte{resDisallowed}, []byte("Not allowed")...)
	}
	stepVal, _ := getMapValue(data, "step")
	step, _ := stepVal.(string)
	if step == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	switch step {
	case "get":
		return n.groupGetPermissions(groupName)
	case "set":
		return n.groupSetPermissions(groupName, data, remoteIdentity)
	default:
		return append([]byte{resInvalidReq}, []byte("Invalid step")...)
	}
}

// groupGetPermissions handles gperms step "get", mirroring
// _group_get_permissions (server.py). Returns resOK + msgpack({"content":...}).
func (n *reticulumGitNode) groupGetPermissions(groupName string) []byte {
	group, ok := n.groups[groupName]
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	allowedPath := group.path + ".allowed"
	content := ""
	if isFile(allowedPath) {
		data, err := os.ReadFile(allowedPath)
		if err != nil {
			return append([]byte{resRemoteFail}, []byte("Error getting permissions")...)
		}
		content = string(data)
	}
	packed, err := msgpack.Pack(map[any]any{"content": content})
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Error getting permissions")...)
	}
	return append([]byte{resOK}, packed...)
}

// groupSetPermissions handles gperms step "set", mirroring
// _group_set_permissions (server.py). It validates each non-# line via
// parsePermissionLine, then writes the .allowed file atomically and
// reloads the in-memory perm lists. Returns resOK alone on success.
func (n *reticulumGitNode) groupSetPermissions(groupName string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	group, ok := n.groups[groupName]
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	content, _ := mapVal(data, "content").(string)
	if err := validateAllowedContent(n, content); err != nil {
		return append([]byte{resInvalidReq}, []byte(err.Error())...)
	}
	allowedPath := group.path + ".allowed"
	tmpPath := allowedPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return append([]byte{resRemoteFail}, []byte("Error setting permissions")...)
	}
	if err := os.Rename(tmpPath, allowedPath); err != nil {
		return append([]byte{resRemoteFail}, []byte("Error setting permissions")...)
	}
	n.updateGroupPermissions(groupName)
	return []byte{resOK}
}

// repositoryPermissionsHandler handles the rperms operation, mirroring
// _repository_permissions (server.py). Requires repo READ + ADMIN.
func (n *reticulumGitNode) repositoryPermissionsHandler(groupName, repoName string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	readAccess := n.resolvePermission(remoteIdentity, groupName, repoName, permRead)
	adminAccess := n.resolvePermission(remoteIdentity, groupName, repoName, permAdmin)
	if !readAccess {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	if !adminAccess {
		return append([]byte{resDisallowed}, []byte("Not allowed")...)
	}
	stepVal, _ := getMapValue(data, "step")
	step, _ := stepVal.(string)
	if step == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	switch step {
	case "get":
		return n.repositoryGetPermissions(groupName, repoName)
	case "set":
		return n.repositorySetPermissions(groupName, repoName, data, remoteIdentity)
	default:
		return append([]byte{resInvalidReq}, []byte("Invalid step")...)
	}
}

// repositoryGetPermissions handles rperms step "get", mirroring
// _repository_get_permissions (server.py). Returns resOK +
// msgpack({"content":...}).
func (n *reticulumGitNode) repositoryGetPermissions(groupName, repoName string) []byte {
	repo, ok := n.lookupRepository(groupName, repoName)
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	allowedPath := repo.path + ".allowed"
	content := ""
	if isFile(allowedPath) {
		data, err := os.ReadFile(allowedPath)
		if err != nil {
			return append([]byte{resRemoteFail}, []byte("Error getting permissions")...)
		}
		content = string(data)
	}
	packed, err := msgpack.Pack(map[any]any{"content": content})
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Error getting permissions")...)
	}
	return append([]byte{resOK}, packed...)
}

// repositorySetPermissions handles rperms step "set", mirroring
// _repository_set_permissions (server.py). It validates each non-# line,
// writes the .allowed file atomically, and reloads the in-memory perm
// lists. Returns resOK alone on success.
func (n *reticulumGitNode) repositorySetPermissions(groupName, repoName string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	repo, ok := n.lookupRepository(groupName, repoName)
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	content, _ := mapVal(data, "content").(string)
	if err := validateAllowedContent(n, content); err != nil {
		return append([]byte{resInvalidReq}, []byte(err.Error())...)
	}
	allowedPath := repo.path + ".allowed"
	tmpPath := allowedPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return append([]byte{resRemoteFail}, []byte("Error setting permissions")...)
	}
	if err := os.Rename(tmpPath, allowedPath); err != nil {
		return append([]byte{resRemoteFail}, []byte("Error setting permissions")...)
	}
	n.loadRepositoryPermissions(repo)
	return []byte{resOK}
}

// validateAllowedContent validates each non-blank, non-# line in content
// via parsePermissionLine, mirroring the validation loop in
// _group_set_permissions / _repository_set_permissions (server.py).
// Returns an error in the Python format
// `Invalid permission "<line>" on line <n>` when a line is invalid.
func validateAllowedContent(n *reticulumGitNode, content string) error {
	for i, line := range strings.Split(content, "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		perm, target := n.parsePermissionLine(stripped)
		if perm == 0 || target == nil {
			return fmt.Errorf("Invalid permission %q on line %d", stripped, i+1)
		}
	}
	return nil
}

// permissionsTemplate is the editor template for the perms client,
// matching PERMISSIONS_TEMPLATE (server.py).
const permissionsTemplate = "# No permissions are currently defined for this entity. Add them below, and save and exit when you are done."