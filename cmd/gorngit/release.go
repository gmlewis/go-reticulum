// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// release.go implements the gorngit /mgmt/release request handler and its
// sub-operations, mirroring handle_release and the _release_* helpers in
// RNS/Utilities/rngit/server.py (rngit v1.4.2).

package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// Permission constants (server.py PERM_*). The full permissions subsystem
// is wired in a later task; resolvePermission currently grants every
// permission to match the rns.AllowAll registration.
const (
	permRead       byte = 0x01
	permWrite      byte = 0x02
	permReadWrite  byte = 0x03
	permCreate     byte = 0x04
	permStats      byte = 0x05
	permRelease    byte = 0x06
	permInteract   byte = 0x07
	permPropose    byte = 0x08
	permAdmin      byte = 0xFE
	permTargetNone byte = 0x01
	permTargetAll  byte = 0x02
)

// resolvePermission reports whether remoteIdentity holds perm on
// group/repo. The full permissions subsystem (.allowed files, identity
// aliases, blocked identities) is wired in a later task; until then every
// identified remote is granted every permission to match the
// rns.AllowAll registration used by the other handlers.
func (n *reticulumGitNode) resolvePermission(remoteIdentity *rns.Identity, groupName, repoName string, perm byte) bool {
	return true
}

// resolveDocPermission reports whether remoteIdentity holds perm on the
// work document docID under workPath. The full per-document permissions
// subsystem (.allowed files, identity aliases) is wired in a later task;
// until then every identified remote is granted every permission to match
// the rns.AllowAll registration used by the other handlers.
func (n *reticulumGitNode) resolveDocPermission(remoteIdentity *rns.Identity, workPath string, docID int, perm byte) bool {
	return true
}

// handleRelease is the /mgmt/release request handler, mirroring
// handle_release (server.py). It dispatches to the list/view/fetch/
// create/delete/latest sub-operations based on the "operation" key.
func (n *reticulumGitNode) handleRelease(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
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
	repoPathVal, ok := getMapValue(m, idxRepository)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No repository specified")...)
	}
	repoPath, ok := repoPathVal.(string)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}

	operationVal, _ := getMapValue(m, "operation")
	operation, _ := operationVal.(string)
	if operation == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}

	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	readAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRead)
	releaseAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRelease)

	if !readAccess {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	access := false
	switch operation {
	case "create", "delete", "latest":
		access = releaseAccess && readAccess
	case "list", "view", "fetch":
		access = readAccess
	}
	if !access {
		return append([]byte{resDisallowed}, []byte("Not allowed")...)
	}

	repo, ok := n.lookupRepository(groupName, repositoryName)
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	releasesPath := repo.path + ".releases"

	var result any
	defer func() {
		if r := recover(); r != nil {
			// Mirrors the Python try/except around the dispatch: any
			// panic in a sub-operation yields RES_REMOTE_FAIL.
			result = append([]byte{resRemoteFail}, []byte("Remote error")...)
		}
	}()

	switch operation {
	case "list":
		result = n.releaseList(releasesPath)
	case "view":
		result = n.releaseView(releasesPath, m)
	case "fetch":
		result = n.releaseFetch(releasesPath, m)
	case "create":
		result = n.releaseCreate(releasesPath, repo.path, m, remoteIdentity)
	case "delete":
		result = n.releaseDelete(releasesPath, m)
	case "latest":
		result = n.releaseLatest(releasesPath, m)
	default:
		result = append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	return result
}

// releasesListData enumerates the releases under releasesPath, mirroring
// releases_list_data (server.py). It returns the list of release summaries
// (sorted by created descending) and the latest published release tag.
// When releasesPath does not exist it returns an empty list and nil.
func (n *reticulumGitNode) releasesListData(releasesPath string) ([]map[any]any, string) {
	releases := []map[any]any{}
	latestRelease := ""
	if !isDir(releasesPath) {
		return releases, latestRelease
	}

	entries, err := os.ReadDir(releasesPath)
	if err != nil {
		return releases, latestRelease
	}
	tags := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		releaseDir := filepath.Join(releasesPath, entry.Name())
		metaPath := filepath.Join(releaseDir, "META")
		if !isFile(metaPath) {
			continue
		}
		meta, err := readReleaseMETA(metaPath)
		if err != nil {
			continue
		}
		releaseTag := meta["tag"]
		if releaseTag == "" {
			releaseTag = entry.Name()
		}
		releaseStatus := meta["status"]
		if releaseStatus == "" {
			releaseStatus = "unknown"
		}
		created, _ := strconv.ParseInt(meta["created"], 10, 64)
		info := map[any]any{
			"tag":        releaseTag,
			"hash":       meta["hash"],
			"created":    created,
			"status":     releaseStatus,
			"created_by": meta["created_by"],
		}

		notesPreview := ""
		notesFormat := "markdown"
		for _, notesFile := range []string{"RELEASE.md", "RELEASE.mu", "RELEASE.txt"} {
			notesPath := filepath.Join(releaseDir, notesFile)
			if !isFile(notesPath) {
				continue
			}
			content, err := os.ReadFile(notesPath)
			if err != nil {
				break
			}
			var sb strings.Builder
			for _, line := range strings.Split(string(content), "\n") {
				if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") {
					continue
				}
				sb.WriteString(line)
				sb.WriteByte('\n')
			}
			notesPreview = strings.TrimSpace(sb.String())
			if strings.HasSuffix(notesFile, ".mu") {
				notesFormat = "micron"
			} else if strings.HasSuffix(notesFile, ".txt") {
				notesFormat = "text"
			}
			break
		}
		info["preview"] = notesPreview
		info["format"] = notesFormat

		artifactsDir := filepath.Join(releaseDir, "artifacts")
		info["artifacts"] = countFiles(artifactsDir)

		releases = append(releases, info)
		tags[releaseTag] = releaseStatus == "published"
	}

	// latest file: only honoured when that tag's status is "published".
	latestPath := filepath.Join(releasesPath, "latest")
	if data, err := os.ReadFile(latestPath); err == nil {
		latestTag := strings.TrimSpace(string(data))
		if published, ok := tags[latestTag]; ok && published {
			latestRelease = latestTag
		}
	}

	sort.SliceStable(releases, func(i, j int) bool {
		ci, _ := releases[i]["created"].(int64)
		cj, _ := releases[j]["created"].(int64)
		return ci > cj
	})
	return releases, latestRelease
}

// releaseList handles operation "list", mirroring _release_list
// (server.py). The response is resOK + msgpack({"releases":[...],
// "latest":...}).
func (n *reticulumGitNode) releaseList(releasesPath string) []byte {
	if !isDir(releasesPath) {
		packed, _ := msgpack.Pack([]any{})
		return append([]byte{resOK}, packed...)
	}
	releases, latest := n.releasesListData(releasesPath)
	releaseData := map[any]any{
		"releases": releases,
		"latest":   latest,
	}
	packed, err := msgpack.Pack(releaseData)
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Error listing releases")...)
	}
	return append([]byte{resOK}, packed...)
}

// releaseData builds the full detail map for a single release, mirroring
// release_data (server.py). Returns nil on a fatal error reading META.
func (n *reticulumGitNode) releaseData(releaseDir, tag string) map[any]any {
	metaPath := filepath.Join(releaseDir, "META")
	if !isFile(metaPath) {
		return nil
	}
	meta, err := readReleaseMETA(metaPath)
	if err != nil {
		return nil
	}
	releaseTag := meta["tag"]
	if releaseTag == "" {
		releaseTag = tag
	}
	created, _ := strconv.ParseInt(meta["created"], 10, 64)
	status := meta["status"]
	if status == "" {
		status = "unknown"
	}
	info := map[any]any{
		"tag":        releaseTag,
		"hash":       meta["hash"],
		"created":    created,
		"status":     status,
		"created_by": meta["created_by"],
	}

	notesContent := ""
	notesFormat := "text"
	for _, pair := range []struct{ file, format string }{
		{"RELEASE.md", "markdown"},
		{"RELEASE.mu", "micron"},
	} {
		notesPath := filepath.Join(releaseDir, pair.file)
		if !isFile(notesPath) {
			continue
		}
		content, err := os.ReadFile(notesPath)
		if err != nil {
			break
		}
		notesContent = string(content)
		notesFormat = pair.format
		break
	}
	info["notes"] = notesContent
	info["notes_format"] = notesFormat

	artifacts := []map[any]any{}
	artifactsDir := filepath.Join(releaseDir, "artifacts")
	if entries, err := os.ReadDir(artifactsDir); err == nil {
		for _, artifact := range entries {
			artifactPath := filepath.Join(artifactsDir, artifact.Name())
			if !isFile(artifactPath) {
				continue
			}
			fi, err := os.Stat(artifactPath)
			if err != nil {
				continue
			}
			artifacts = append(artifacts, map[any]any{
				"name": artifact.Name(),
				"size": fi.Size(),
			})
		}
	}
	info["artifacts"] = artifacts

	thanksCount := int64(0)
	thanksPath := filepath.Join(releaseDir, "THANKS")
	if isFile(thanksPath) {
		if data, err := os.ReadFile(thanksPath); err == nil {
			if unpacked, err := msgpack.UnpackPreserveBinMapKeys(data); err == nil {
				if tm, ok := unpacked.(map[any]any); ok {
					if c, ok := getMapValue(tm, "count"); ok {
						switch v := c.(type) {
						case int64:
							thanksCount = v
						case uint64:
							thanksCount = int64(v)
						case int:
							thanksCount = int64(v)
						}
					}
				}
			}
		}
	}
	info["thanks"] = thanksCount

	return info
}

// releaseView handles operation "view", mirroring _release_view
// (server.py). The tag may be "latest" to resolve the latest published
// release. The response is resOK + msgpack(release_info).
func (n *reticulumGitNode) releaseView(releasesPath string, data map[any]any) []byte {
	tagVal, _ := getMapValue(data, "tag")
	tag, _ := tagVal.(string)
	if strings.Contains(tag, "/") || tag == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid tag specified")...)
	}
	tag = filepath.Base(tag)

	if tag == "latest" {
		latest, ok := readLatestTag(releasesPath)
		if !ok {
			return append([]byte{resNotFound}, []byte("No latest release found")...)
		}
		tag = latest
	}

	releaseDir := filepath.Join(releasesPath, tag)
	if !isDir(releaseDir) {
		return append([]byte{resNotFound}, []byte("Release not found")...)
	}
	info := n.releaseData(releaseDir, tag)
	if info == nil {
		return append([]byte{resRemoteFail}, []byte("Error getting release data")...)
	}
	packed, err := msgpack.Pack(info)
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Error getting release data")...)
	}
	return append([]byte{resOK}, packed...)
}

// releaseFetch handles operation "fetch", mirroring _release_fetch
// (server.py). It returns the artifact file bytes preceded by resOK.
func (n *reticulumGitNode) releaseFetch(releasesPath string, data map[any]any) []byte {
	tagVal, _ := getMapValue(data, "tag")
	tag, _ := tagVal.(string)
	if strings.Contains(tag, "/") || tag == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid tag specified")...)
	}
	artifactVal, _ := getMapValue(data, "artifact")
	artifact, _ := artifactVal.(string)
	if strings.Contains(artifact, "/") || artifact == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid artifact specified")...)
	}
	tag = filepath.Base(tag)
	artifact = filepath.Base(artifact)

	if tag == "latest" {
		latest, ok := readLatestTag(releasesPath)
		if !ok {
			return append([]byte{resNotFound}, []byte("No latest release found")...)
		}
		tag = latest
	}

	releaseDir := filepath.Join(releasesPath, tag)
	if !isDir(releaseDir) {
		return append([]byte{resNotFound}, []byte("Release not found")...)
	}
	artifactPath := filepath.Join(releaseDir, "artifacts", artifact)
	if !isFile(artifactPath) {
		return append([]byte{resNotFound}, []byte("Artifact not found")...)
	}
	fileBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	result := make([]byte, 0, 1+len(fileBytes))
	result = append(result, resOK)
	result = append(result, fileBytes...)
	return result
}

// releaseCreate handles operation "create", dispatching to the init/
// artifact/finalize steps, mirroring _release_create (server.py).
func (n *reticulumGitNode) releaseCreate(releasesPath, repositoryPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	stepVal, _ := getMapValue(data, "step")
	step, _ := stepVal.(string)
	if step == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	switch step {
	case "init":
		return n.releaseCreateInit(releasesPath, repositoryPath, data, remoteIdentity)
	case "artifact":
		return n.releaseCreateArtifact(releasesPath, data)
	case "finalize":
		return n.releaseCreateFinalize(releasesPath, data)
	default:
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
}

// releaseCreateInit creates a new draft release directory, mirroring
// _release_create_init (server.py). It verifies the tag exists in the
// repository, creates the release dir + artifacts dir, writes META and
// THANKS, and optionally writes the release notes.
func (n *reticulumGitNode) releaseCreateInit(releasesPath, repositoryPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	tagVal, _ := getMapValue(data, "tag")
	tag, _ := tagVal.(string)
	commitHashVal, _ := getMapValue(data, "hash")
	commitHash, _ := commitHashVal.(string)
	notesVal, _ := getMapValue(data, "notes")
	notes, _ := notesVal.(string)
	notesFormatVal, _ := getMapValue(data, "notes_format")
	notesFormat, _ := notesFormatVal.(string)
	if notesFormat == "" {
		notesFormat = "markdown"
	}

	if tag == "" || strings.Contains(tag, "/") {
		return append([]byte{resInvalidReq}, []byte("Invalid tag specified")...)
	}
	tag = filepath.Base(tag)
	if tag == "" || tag == "." || tag == ".." {
		return append([]byte{resInvalidReq}, []byte("Invalid tag name")...)
	}

	// Verify the tag exists in the repository.
	check := exec.Command("git", "rev-parse", "--verify", "refs/tags/"+tag)
	check.Dir = repositoryPath
	check.Stdout = nil
	check.Stderr = nil
	if check.Run() != nil {
		return append([]byte{resInvalidReq}, []byte(fmt.Sprintf("Tag '%s' does not exist in repository", tag))...)
	}

	if err := os.MkdirAll(releasesPath, 0o755); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	releaseDir := filepath.Join(releasesPath, tag)
	if isDir(releaseDir) {
		return append([]byte{resDisallowed}, []byte("Release already exists")...)
	}
	if err := os.Mkdir(releaseDir, 0o755); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	if err := os.Mkdir(filepath.Join(releaseDir, "artifacts"), 0o755); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}

	meta := map[string]string{
		"tag":        tag,
		"created":    strconv.FormatInt(time.Now().Unix(), 10),
		"status":     "draft",
		"created_by": hex.EncodeToString(remoteIdentity.Hash),
	}
	if commitHash != "" {
		meta["hash"] = commitHash
	}
	if err := writeReleaseMETA(filepath.Join(releaseDir, "META"), meta); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}

	if notes != "" {
		notesFilename := "RELEASE.md"
		if notesFormat == "micron" {
			notesFilename = "RELEASE.mu"
		}
		if err := os.WriteFile(filepath.Join(releaseDir, notesFilename), []byte(notes), 0o644); err != nil {
			return append([]byte{resRemoteFail}, []byte("Remote error")...)
		}
	}

	thanksPath := filepath.Join(releaseDir, "THANKS")
	packed, _ := msgpack.Pack(map[any]any{"count": int64(0)})
	if err := os.WriteFile(thanksPath, packed, 0o644); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}

	return []byte{resOK}
}

// releaseCreateArtifact uploads a single artifact into a draft release,
// mirroring _release_create_artifact (server.py).
func (n *reticulumGitNode) releaseCreateArtifact(releasesPath string, data map[any]any) []byte {
	tagVal, _ := getMapValue(data, "tag")
	tag, _ := tagVal.(string)
	artifactNameVal, _ := getMapValue(data, "artifact_name")
	artifactName, _ := artifactNameVal.(string)
	artifactDataVal, hasData := getMapValue(data, "artifact_data")

	if tag == "" || artifactName == "" {
		return append([]byte{resInvalidReq}, []byte("Missing tag or artifact name")...)
	}
	if strings.Contains(tag, "/") {
		return append([]byte{resInvalidReq}, []byte("Invalid tag specified")...)
	}
	if !hasData || artifactDataVal == nil {
		return append([]byte{resInvalidReq}, []byte("No artifact data")...)
	}
	tag = filepath.Base(tag)
	artifactName = filepath.Base(artifactName)

	releaseDir := filepath.Join(releasesPath, tag)
	if !isDir(releaseDir) {
		return append([]byte{resNotFound}, []byte("Release not found")...)
	}
	meta, err := readReleaseMETA(filepath.Join(releaseDir, "META"))
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	if meta["status"] != "draft" {
		return append([]byte{resDisallowed}, []byte("Release was finalized and is not writable")...)
	}

	artifactsDir := filepath.Join(releaseDir, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	artifactBytes, ok := parseBundleData(artifactDataVal)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No artifact data")...)
	}
	if err := os.WriteFile(filepath.Join(artifactsDir, artifactName), artifactBytes, 0o644); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return []byte{resOK}
}

// releaseCreateFinalize marks a draft release as published and records it
// as the latest, mirroring _release_create_finalize (server.py).
func (n *reticulumGitNode) releaseCreateFinalize(releasesPath string, data map[any]any) []byte {
	tagVal, _ := getMapValue(data, "tag")
	tag, _ := tagVal.(string)
	if tag == "" || strings.Contains(tag, "/") {
		return append([]byte{resInvalidReq}, []byte("Invalid tag specified")...)
	}
	tag = filepath.Base(tag)

	releaseDir := filepath.Join(releasesPath, tag)
	if !isDir(releaseDir) {
		return append([]byte{resNotFound}, []byte("Release not found")...)
	}
	metaPath := filepath.Join(releaseDir, "META")
	meta, err := readReleaseMETA(metaPath)
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	if meta["status"] != "draft" {
		return append([]byte{resDisallowed}, []byte("Release was finalized and is not writable")...)
	}
	meta["status"] = "published"
	meta["published_at"] = strconv.FormatInt(time.Now().Unix(), 10)
	if err := writeReleaseMETA(metaPath, meta); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}

	if err := writeLatestTag(releasesPath, tag); err != nil {
		// Best-effort, mirroring Python's try/except.
	}
	return []byte{resOK}
}

// releaseDelete removes a release directory, mirroring _release_delete
// (server.py).
func (n *reticulumGitNode) releaseDelete(releasesPath string, data map[any]any) []byte {
	tagVal, _ := getMapValue(data, "tag")
	tag, _ := tagVal.(string)
	if tag == "" || strings.Contains(tag, "/") {
		return append([]byte{resInvalidReq}, []byte("Invalid tag specified")...)
	}
	tag = filepath.Base(tag)
	releaseDir := filepath.Join(releasesPath, tag)
	if !isDir(releaseDir) {
		return append([]byte{resNotFound}, []byte("Release not found")...)
	}
	if err := os.RemoveAll(releaseDir); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return []byte{resOK}
}

// releaseLatest sets a release as the latest, mirroring _release_latest
// (server.py).
func (n *reticulumGitNode) releaseLatest(releasesPath string, data map[any]any) []byte {
	tagVal, _ := getMapValue(data, "tag")
	tag, _ := tagVal.(string)
	if tag == "" || strings.Contains(tag, "/") {
		return append([]byte{resInvalidReq}, []byte("Invalid tag specified")...)
	}
	tag = filepath.Base(tag)
	releaseDir := filepath.Join(releasesPath, tag)
	if !isDir(releaseDir) {
		return append([]byte{resNotFound}, []byte("Release not found")...)
	}
	if err := writeLatestTag(releasesPath, tag); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return []byte{resOK}
}

// readLatestTag reads the "latest" file under releasesPath and returns
// the trimmed tag. Returns ok=false when the file is missing.
func readLatestTag(releasesPath string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(releasesPath, "latest"))
	if err != nil {
		return "", false
	}
	tag := strings.TrimSpace(string(data))
	if tag == "" {
		return "", false
	}
	return tag, true
}

// writeLatestTag atomically writes tag to the "latest" file under
// releasesPath, mirroring the tmp+rename in _release_create_finalize.
func writeLatestTag(releasesPath, tag string) error {
	latestPath := filepath.Join(releasesPath, "latest")
	tmpPath := latestPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(tag), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, latestPath)
}

// readReleaseMETA reads a flat key=value INI-style META file and returns
// the key/value map. Lines without "=" are ignored. Values are trimmed of
// surrounding whitespace.
func readReleaseMETA(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	meta := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		meta[key] = val
	}
	return meta, nil
}

// writeReleaseMETA writes a flat key=value INI-style META file. Keys are
// written in the order returned by meta.Iter, which is deterministic for
// map[string]string iteration (non-deterministic, but META is
// server-local storage so round-trip consistency is sufficient).
func writeReleaseMETA(path string, meta map[string]string) error {
	var sb strings.Builder
	for key, val := range meta {
		sb.WriteString(key)
		sb.WriteString(" = ")
		sb.WriteString(val)
		sb.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// isFile reports whether path exists and is a regular file.
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// countFiles returns the number of regular files in dir, or 0 when the
// directory does not exist.
func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		count++
	}
	return count
}
