// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// pages-git.go implements the git subprocess plumbing used by the
// nomadnetwork page-node handlers, mirroring the get_* helpers of
// NomadNetworkNode (pages.py:1842-2377). Each helper shells out to git and
// parses stdout into a typed result, returning a zero/nil value on error or
// timeout exactly as the Python originals log and fall back.

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// refInfo is a short ref summary, mirroring the dict built by
// get_repository_refs (pages.py:1880-1882).
type refInfo struct {
	name      string
	hash      string
	shortHash string
}

// refInfoDetailed extends refInfo with commit metadata, mirroring the dict
// built by get_refs_info (pages.py:2110-2116).
type refInfoDetailed struct {
	refInfo
	commitSubject string
	isDefault     bool
	isAnnotated   bool
	tagMessage    string
}

// treeEntry is one row of a directory listing, mirroring the dict built by
// get_tree_entries (pages.py:1947-1951).
type treeEntry struct {
	name       string
	typ        string // "blob", "tree", "commit" (submodule), or "link"
	mode       string
	size       int64
	linkTarget string // empty unless typ == "link"
}

// blobInfo describes a path at a ref, mirroring the dict built by
// get_blob_info (pages.py:2045-2049).
type blobInfo struct {
	size          int64
	isTree        bool
	isBinary      bool
	isSymlink     bool
	symlinkTarget string
}

// commitInfo is a commit's metadata plus changed files, mirroring the dict
// built by get_commit_info (pages.py:2205-2215).
type commitInfo struct {
	parents        []string
	authorName     string
	authorEmail    string
	authorDate     string
	committerName  string
	committerEmail string
	committerDate  string
	message        string
	files          []commitFile
	diff           string
}

// commitFile is one changed file in a commit, mirroring the dict built by
// get_commit_info (pages.py:2263-2266).
type commitFile struct {
	path      string
	status    string // "A", "D", "M", "R"
	additions int64
	deletions int64
}

// commitSigStatus is the (stubbed) result of get_commit_signature
// (pages.py:2285-2361). Commit-signature validation needs rnid+commitsigs,
// which are deferred; the page-node always reports "Not signed".
type commitSigStatus struct {
	signed      bool
	valid       bool
	signerHash  string
	authorMatch bool
	message     string
}

// gitRun executes git args in repoPath with the page-node command timeout
// and returns stdout (text) and whether the command succeeded. It mirrors
// the subprocess.run(..., timeout=GIT_COMMAND_TIMEOUT, check=False) pattern
// used throughout pages.py.
func gitRun(repoPath string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), false
	}
	return stdout.String(), true
}

// gitRunBytes is the binary counterpart to gitRun, returning raw stdout.
func gitRunBytes(repoPath string, args ...string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), false
	}
	return stdout.Bytes(), true
}

// getRepositoryDescription returns the repo description, mirroring
// get_repository_description (pages.py:1842-1859). Returns "" when no
// description is configured.
func (p *pageNode) getRepositoryDescription(repoPath string) string {
	if out, ok := gitRun(repoPath, "config", "--get", "repository.description"); ok {
		if s := strings.TrimSpace(out); s != "" {
			return s
		}
	}
	descPath := repoPath + ".description"
	if b, err := os.ReadFile(descPath); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return ""
}

// getRepositoryRefs returns the heads and tags of a repo, mirroring
// get_repository_refs (pages.py:1861-1889).
func (p *pageNode) getRepositoryRefs(repoPath string) (heads, tags []refInfo) {
	heads = []refInfo{}
	tags = []refInfo{}
	out, ok := gitRun(repoPath, "for-each-ref",
		"--format=%(objectname) %(refname) %(refname:short)", "refs/heads", "refs/tags")
	if !ok {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 2 {
			continue
		}
		fullHash := parts[0]
		refName := parts[1]
		shortName := refName
		if len(parts) > 2 {
			shortName = parts[2]
		}
		ri := refInfo{name: shortName, hash: fullHash, shortHash: fullHash[:7]}
		switch {
		case strings.HasPrefix(refName, "refs/heads/"):
			heads = append(heads, ri)
		case strings.HasPrefix(refName, "refs/tags/"):
			tags = append(tags, ri)
		}
	}
	return
}

// resolveRef resolves ref to a full lowercased hash, mirroring resolve_ref
// (pages.py:1891-1903). Returns "" on failure or timeout.
func (p *pageNode) resolveRef(repoPath, ref string) string {
	out, ok := gitRun(repoPath, "rev-parse", "--verify", ref)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(out))
}

// getTreeEntries lists the tree at ref/path, mirroring get_tree_entries
// (pages.py:1905-1974). Returns nil on error, a non-nil empty slice for a
// valid empty tree.
func (p *pageNode) getTreeEntries(repoPath, ref, path string) []treeEntry {
	treePath := strings.Trim(path, "/")
	lsArg := ref
	if treePath != "" {
		lsArg = ref + ":" + treePath
	}
	out, ok := gitRun(repoPath, "ls-tree", "-l", lsArg)
	if !ok {
		// Distinguish "not a tree" from "empty tree".
		catOut, catOK := gitRun(repoPath, "cat-file", "-t", lsArg)
		if !catOK || strings.TrimSpace(catOut) != "tree" {
			return nil
		}
		return []treeEntry{}
	}
	entries := []treeEntry{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		meta := strings.Fields(parts[0])
		name := parts[1]
		if len(meta) < 3 {
			continue
		}
		mode := meta[0]
		objType := meta[1]
		var size int64
		if len(meta) >= 4 {
			if n, err := strconv.ParseInt(meta[3], 10, 64); err == nil {
				size = n
			}
		}
		entry := treeEntry{name: name, typ: objType, mode: mode, size: size}
		if mode == "120000" {
			entry.typ = "link"
			showArg := ref + ":" + treePath + "/" + name
			if treePath == "" {
				showArg = ref + ":" + name
			}
			if tgt, ok := gitRun(repoPath, "show", showArg); ok {
				entry.linkTarget = strings.TrimSpace(tgt)
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// getBlobInfo describes a path at a ref, mirroring get_blob_info
// (pages.py:1976-2057). Returns nil on error.
func (p *pageNode) getBlobInfo(repoPath, ref, path string) *blobInfo {
	filePath := strings.Trim(path, "/")
	sizeOut, ok := gitRun(repoPath, "cat-file", "-s", ref+":"+filePath)
	if !ok {
		return nil
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)

	checkTreeArg := ref + ":" + filePath
	if filePath == "" {
		checkTreeArg = ref
	}
	_, treeOK := gitRun(repoPath, "ls-tree", checkTreeArg)
	isTree := treeOK

	// Symlink detection via the parent directory listing.
	parentDir := dirOf(filePath)
	filename := baseOf(filePath)
	lsTreeArg := ref + ":" + parentDir
	if parentDir == "" {
		lsTreeArg = ref
	}
	lsOut, lsOK := gitRun(repoPath, "ls-tree", lsTreeArg)
	isSymlink := false
	var symlinkTarget string
	if lsOK {
		for _, line := range strings.Split(strings.TrimSpace(lsOut), "\n") {
			if strings.HasPrefix(line, "120000") && strings.Contains(line, "\t"+filename) {
				isSymlink = true
				break
			}
		}
	}
	if isSymlink {
		if tgt, ok := gitRun(repoPath, "show", ref+":"+filePath); ok {
			symlinkTarget = strings.TrimSpace(tgt)
		}
	}

	isBinary := false
	if !isSymlink {
		if sample, ok := gitRunBytes(repoPath, "show", ref+":"+filePath); ok {
			limit := 8192
			if len(sample) < limit {
				limit = len(sample)
			}
			if bytes.IndexByte(sample[:limit], 0) >= 0 {
				isBinary = true
			}
		}
	}

	return &blobInfo{
		size:          size,
		isTree:        isTree,
		isBinary:      isBinary,
		isSymlink:     isSymlink,
		symlinkTarget: symlinkTarget,
	}
}

// getBlobContent returns a blob's text content, mirroring get_blob_content
// (pages.py:2059-2072). Returns "" on failure.
func (p *pageNode) getBlobContent(repoPath, ref, path string) string {
	filePath := strings.Trim(path, "/")
	out, ok := gitRun(repoPath, "show", ref+":"+filePath)
	if !ok {
		return ""
	}
	return out
}

// getBlobStream returns a blob's raw bytes, mirroring get_blob_stream
// (pages.py:2074-2085) which returns a pipe. The Go port returns the full
// bytes since streaming a subprocess pipe over an RNS request response is
// handled by the caller reading the slice.
func (p *pageNode) getBlobStream(repoPath, ref, path string) ([]byte, bool) {
	filePath := strings.Trim(path, "/")
	return gitRunBytes(repoPath, "show", ref+":"+filePath)
}

// getRefsInfo returns detailed head/tag info, mirroring get_refs_info
// (pages.py:2087-2142).
func (p *pageNode) getRefsInfo(repoPath, defaultBranch string) (heads, tags []refInfoDetailed) {
	heads = []refInfoDetailed{}
	tags = []refInfoDetailed{}
	out, ok := gitRun(repoPath, "for-each-ref",
		"--format=%(objectname)|%(refname)|%(refname:short)|%(subject)", "refs/heads", "refs/tags")
	if !ok {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 3 {
			continue
		}
		fullHash := parts[0]
		refName := parts[1]
		shortName := parts[2]
		subject := ""
		if len(parts) > 3 {
			subject = parts[3]
		}
		ri := refInfoDetailed{
			refInfo:       refInfo{name: shortName, hash: fullHash, shortHash: fullHash[:7]},
			commitSubject: subject,
			isDefault:     shortName == defaultBranch,
			isAnnotated:   false,
		}
		switch {
		case strings.HasPrefix(refName, "refs/heads/"):
			heads = append(heads, ri)
		case strings.HasPrefix(refName, "refs/tags/"):
			if tagOut, tagOK := gitRun(repoPath, "for-each-ref",
				"--format=%(objecttype)|%(contents:subject)", refName); tagOK {
				tagParts := strings.SplitN(strings.TrimSpace(tagOut), "|", 2)
				if len(tagParts) >= 2 && tagParts[0] == "tag" {
					ri.isAnnotated = true
					ri.tagMessage = tagParts[1]
				}
			}
			tags = append(tags, ri)
		}
	}
	return
}

// getCommitCount returns the commit count at ref, mirroring get_commit_count
// (pages.py:2144-2155).
func (p *pageNode) getCommitCount(repoPath, ref string) int {
	if ref == "" {
		return 0
	}
	out, ok := gitRun(repoPath, "rev-list", "--count", ref)
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// commitListEntry is one row of get_commits (pages.py:2174-2178).
type commitListEntry struct {
	hash        string
	subject     string
	author      string
	authorEmail string
	timestamp   int64
}

// getCommits returns a page of commits, mirroring get_commits
// (pages.py:2157-2188). Returns nil on error.
func (p *pageNode) getCommits(repoPath, ref, filePath string, skip, limit int) []commitListEntry {
	sep := "|_SEP_|"
	cmd := []string{"log", "--format=%H" + sep + "%s" + sep + "%an" + sep + "%ae" + sep + "%at",
		"--skip", strconv.Itoa(skip), "-n", strconv.Itoa(limit), ref}
	if filePath != "" {
		cmd = append(cmd, "--", filePath)
	}
	out, ok := gitRun(repoPath, cmd...)
	if !ok {
		return nil
	}
	commits := []commitListEntry{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, sep, 5)
		if len(parts) < 5 {
			continue
		}
		ts, _ := strconv.ParseInt(parts[4], 10, 64)
		commits = append(commits, commitListEntry{
			hash: parts[0], subject: parts[1], author: parts[2],
			authorEmail: parts[3], timestamp: ts,
		})
	}
	return commits
}

// getCommitInfo returns a commit's metadata and changed files, mirroring
// get_commit_info (pages.py:2190-2283). The diff is only populated when
// showDiff is true (Python SHOW_DIFF_BY_DEFAULT).
func (p *pageNode) getCommitInfo(repoPath, hash string, showDiff bool) *commitInfo {
	formatStr := "%P%n%an%n%ae%n%aI%n%cn%n%ce%n%cI%n%B"
	out, ok := gitRun(repoPath, "show", "--no-patch", "--format="+formatStr, hash)
	if !ok {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 7 {
		return nil
	}
	var parents []string
	if strings.TrimSpace(lines[0]) != "" {
		parents = strings.Fields(lines[0])
	}
	info := &commitInfo{
		parents:        parents,
		authorName:     lines[1],
		authorEmail:    lines[2],
		authorDate:     lines[3],
		committerName:  lines[4],
		committerEmail: lines[5],
		committerDate:  lines[6],
		message:        strings.TrimSpace(strings.Join(lines[7:], "\n")),
	}

	statsOut, statsOK := gitRun(repoPath, "diff-tree", "--numstat", "-r", hash)
	if statsOK {
		for _, line := range strings.Split(strings.TrimSpace(statsOut), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 3 {
				continue
			}
			additions, deletions := parts[0], parts[1]
			filePath := parts[2]
			status := "M"
			if additions == "-" && deletions == "-" {
				status = "R"
				additions, deletions = "0", "0"
			}
			if len(info.parents) > 0 {
				parent := info.parents[0]
				if _, parentOK := gitRun(repoPath, "cat-file", "-e", parent+":"+filePath); !parentOK {
					status = "A"
				} else if _, curOK := gitRun(repoPath, "cat-file", "-e", hash+":"+filePath); !curOK {
					status = "D"
				}
			}
			addN, _ := strconv.ParseInt(safeInt(additions), 10, 64)
			delN, _ := strconv.ParseInt(safeInt(deletions), 10, 64)
			info.files = append(info.files, commitFile{
				path: filePath, status: status, additions: addN, deletions: delN,
			})
		}
	}

	if showDiff {
		if diffOut, diffOK := gitRun(repoPath, "show", "--format=", hash); diffOK {
			info.diff = diffOut
		}
	}
	return info
}

// safeInt returns s unchanged, or "0" when s is the numstat binary sentinel
// "-".
func safeInt(s string) string {
	if s == "-" {
		return "0"
	}
	return s
}

// getCommitSignature is a stub mirroring get_commit_signature
// (pages.py:2285-2361). Commit-signature validation requires rnid +
// commitsigs, which are deferred; the page-node always reports "Not signed".
func (p *pageNode) getCommitSignature(repoPath, hash string) commitSigStatus {
	return commitSigStatus{signed: false, valid: false, message: "Not signed"}
}

// readmeCandidate is one (name, isMarkdown) probe tried by
// get_readme_content (pages.py:2364-2366).
type readmeCandidate struct {
	name       string
	isMarkdown bool
}

var readmeCandidates = []readmeCandidate{
	{"README.mu", false}, {"Readme.mu", false}, {"readme.mu", false}, {"README", false},
	{"readme", false}, {"README.md", true}, {"readme.md", true}, {"README.rst", false},
	{"README.txt", false}, {"readme.rst", false}, {"readme.txt", false},
}

// getReadmeContent returns the README content at HEAD and whether it is
// markdown, mirroring get_readme_content (pages.py:2363-2377). found is false
// when no README is present.
func (p *pageNode) getReadmeContent(repoPath string) (content string, isMarkdown, found bool) {
	for _, c := range readmeCandidates {
		if out, ok := gitRun(repoPath, "show", "HEAD:"+c.name); ok {
			return out, c.isMarkdown, true
		}
	}
	return "", false, false
}

// mirrorSynced returns the last upstream-sync timestamp for a mirror/fork
// repo, mirroring last_upstream_sync + __mirror_synced (server.py:2763,
// 2744-2756). Returns 0 when unset.
func (p *pageNode) mirrorSynced(repoPath string) int64 {
	out, ok := gitRun(repoPath, "config", "repository.rngit.upstream.sync")
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// dirOf returns the directory portion of a slash path (everything before
// the last "/"), mirroring the Python "/".join(file_path.split("/")[:-1]).
func dirOf(filePath string) string {
	idx := strings.LastIndex(filePath, "/")
	if idx < 0 {
		return ""
	}
	return filePath[:idx]
}

// baseOf returns the final component of a slash path, mirroring the Python
// file_path.split("/")[-1].
func baseOf(filePath string) string {
	idx := strings.LastIndex(filePath, "/")
	if idx < 0 {
		return filePath
	}
	return filePath[idx+1:]
}
