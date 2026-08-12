// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// pages-handlers.go implements the nomadnetwork page-node request handlers,
// mirroring the serve_* methods of NomadNetworkNode
// (RNS/Utilities/rngit/pages.py, rngit v1.4.2). Each handler unpacks the
// umsgpack request body (whose variables arrive as var_<name> keys), reads
// the requested group/repo/ref/path, builds a micron page, and returns the
// rendered []byte for the rns request-response layer to pack and send.
//
// The git-protocol-independent browsing handlers (front, group, tree, blob,
// commits, commit, refs) live here; the repo/stats/releases/release/work/
// work_doc handlers and the three file handlers live in pages-handlers2.go.
// Handlers register via registerRequestHandlers (pages-handlers2.go).

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/micron"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// showDiffByDefault mirrors SHOW_DIFF_BY_DEFAULT (pages.py:74). The
// pagePath*/file* path constants and the render tunables
// (blobSizeLimit, treeEntriesPerPage, commitsPerPage, renderableExts, …)
// live in pages-render.go.
const showDiffByDefault = true

// unpackPageVars unpacks the umsgpack request body into the var_<name> map
// used by every page handler, mirroring the `data.get("var_g", "")` pattern.
// An empty/nil/unparseable body yields an empty map (matching the Python
// `if not data: data = {}` guard).
func unpackPageVars(data []byte) map[any]any {
	if len(data) == 0 {
		return map[any]any{}
	}
	u, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return map[any]any{}
	}
	if m, ok := u.(map[any]any); ok {
		return m
	}
	return map[any]any{}
}

// vstr reads a var_<key> string field, defaulting to "".
func vstr(m map[any]any, key string) string {
	if s, ok := m["var_"+key].(string); ok {
		return s
	}
	return ""
}

// vint reads a var_<key> integer field, defaulting to def on missing/bad.
func vint(m map[any]any, key string, def int) int {
	if n, ok := m["var_"+key].(int64); ok {
		return int(n)
	}
	if n, ok := m["var_"+key].(float64); ok {
		return int(n)
	}
	if s, ok := m["var_"+key].(string); ok {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return def
}

// vbool reads a var_<key> field as a truthy flag, mirroring
// `True if data.get("var_x", "") else False` (pages.py:428 etc.).
func vbool(m map[any]any, key string) bool {
	v, ok := m["var_"+key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case string:
		return t != ""
	case bool:
		return t
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return true
}

// render is the per-pageNode render_template wrapper, mirroring
// render_template (pages.py:276-297). It substitutes the node name and the
// rns version, and computes the generation time from start.
func (p *pageNode) render(template, pageContent, navContent string, start time.Time) []byte {
	gen := 0.0
	if !start.IsZero() {
		gen = time.Since(start).Seconds()
	}
	return renderTemplate(pageContent, navContent, template, p.nodeName, rns.VERSION, gen)
}

// sortedKeys returns the keys of m sorted ascending.
func sortedKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// repoCountOf returns the number of accessible repositories in an
// getAccessibleGroups entry.
func repoCountOf(group map[string]any) int {
	if repos, ok := group["repositories"].(map[string]*accessibleRepo); ok {
		return len(repos)
	}
	return 0
}

// serveFrontPage mirrors serve_front_page (pages.py:350-375).
func (p *pageNode) serveFrontPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	contentParts := []string{}
	navParts := []string{}

	accessible := p.getAccessibleGroups(remoteIdentity)
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " /"
	navParts = append(navParts, breadcrumb+"\n")

	if len(accessible) == 0 {
		contentParts = append(contentParts, ">>\nNo groups available\n")
	} else {
		for _, groupName := range sortedKeys(accessible) {
			repoCount := repoCountOf(accessible[groupName])
			repoWord := "repositories"
			if repoCount == 1 {
				repoWord = "repository"
			}
			link := mLink("  "+micron.Bullet+" "+groupName, pagePathGroup, []linkField{{"g", groupName}})
			contentParts = append(contentParts, fmt.Sprintf("%s (%d %s)\n", link, repoCount, repoWord))
		}
	}

	p.owner.viewSucceeded(nil, nil, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	navContent := strings.Join(navParts, "")
	return p.render("front", pageContent, navContent, start)
}

// serveGroupPage mirrors serve_group_page (pages.py:377-418).
func (p *pageNode) serveGroupPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")

	if groupName == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("group", content, "", start)
	}

	navParts := []string{}
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " / " + groupName
	navParts = append(navParts, breadcrumb+"\n")
	navContent := strings.Join(navParts, "")

	accessible := p.getAccessibleRepositories(remoteIdentity, groupName)
	if _, ok := p.owner.groups[groupName]; !ok || len(accessible) == 0 {
		content := mHeading("Group Not Found", 2) + "\nThe requested group was not found\n"
		return p.render("group", content, navContent, start)
	}

	contentParts := []string{}
	contentParts = append(contentParts, mHeading(" Repositories", 1))
	contentParts = append(contentParts, "\n")

	repoNames := make([]string, 0, len(accessible))
	for n := range accessible {
		repoNames = append(repoNames, n)
	}
	sort.Strings(repoNames)
	for _, repoName := range repoNames {
		repo := accessible[repoName]
		description := p.getRepositoryDescription(repo.path)
		link := mLink("  "+micron.Bullet+" "+repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}})
		contentParts = append(contentParts, link)
		if description != "" {
			contentParts = append(contentParts, fmt.Sprintf(" - %s\n", description))
		} else {
			contentParts = append(contentParts, "\n")
		}
	}

	p.owner.viewSucceeded(&groupName, nil, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	return p.render("group", pageContent, navContent, start)
}

// serveTreePage mirrors serve_tree_page (pages.py:539-678).
func (p *pageNode) serveTreePage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	ref := vstr(vars, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	treePath := vstr(vars, "path")
	treePath, _ = url.QueryUnescape(treePath)
	pageNum := vint(vars, "page", 0)
	if pageNum < 0 {
		pageNum = 0
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Not Found", 1) + "\n\nThe requested repository does not exist or you do not have access to it.\n"
		return p.render("tree", content, "", start)
	}
	repoPath := repo.path

	resolvedRef := p.resolveRef(repoPath, ref)
	if resolvedRef == "" {
		content := mHeading("Error", 2) + fmt.Sprintf("\n\nThe ref '%s' does not exist in this repository.\n", ref)
		content += "\n" + mLink("View All Refs", pagePathRefs, []linkField{{"g", groupName}, {"r", repoName}}) + "\n"
		return p.render("tree", content, "", start)
	}

	contentParts := []string{}
	navParts := []string{}

	breadcrumbParts := []string{
		mLink("Node", pagePathIndex, nil),
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}),
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}),
		mLink("files", pagePathTree, []linkField{{"g", groupName}, {"r", repoName}}),
	}
	if treePath != "" {
		pathComponents := strings.Split(strings.Trim(treePath, "/"), "/")
		currentPath := ""
		for i, component := range pathComponents {
			if currentPath != "" {
				currentPath = currentPath + "/" + component
			} else {
				currentPath = component
			}
			if i == len(pathComponents)-1 {
				breadcrumbParts = append(breadcrumbParts, component)
			} else {
				breadcrumbParts = append(breadcrumbParts, mLink(component, pagePathTree,
					[]linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", currentPath}}))
			}
		}
	} else {
		breadcrumbParts = append(breadcrumbParts, "")
	}
	breadcrumb := strings.Join(breadcrumbParts, " / ")
	navParts = append(navParts, ">>\n"+breadcrumb+"\n")

	entries := p.getTreeEntries(repoPath, resolvedRef, treePath)
	if entries == nil {
		contentParts = append(contentParts, "Error reading directory contents.\n")
	} else if len(entries) == 0 {
		contentParts = append(contentParts, "Empty directory.\n")
	} else {
		iFile := p.icon("file")
		iFolder := p.icon("folder")
		sort.SliceStable(entries, func(i, j int) bool {
			iDir := entries[i].typ == "tree" || entries[i].typ == "commit"
			jDir := entries[j].typ == "tree" || entries[j].typ == "commit"
			if iDir != jDir {
				return iDir
			}
			return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
		})

		totalEntries := len(entries)
		startIdx := pageNum * treeEntriesPerPage
		endIdx := startIdx + treeEntriesPerPage
		if startIdx > totalEntries {
			startIdx = totalEntries
		}
		var pageEntries []treeEntry
		if startIdx < endIdx && startIdx < totalEntries {
			if endIdx > totalEntries {
				endIdx = totalEntries
			}
			pageEntries = entries[startIdx:endIdx]
		}

		contentParts = append(contentParts, mHeading(fmt.Sprintf("Contents: %s (%s)", ref, safeShort(resolvedRef, 8)), 2))
		contentParts = append(contentParts, "\n")

		if totalEntries > treeEntriesPerPage {
			contentParts = append(contentParts, fmt.Sprintf("%sShowing %d-%d of %d entries`f\n\n",
				clrDim, startIdx+1, minInt(endIdx, totalEntries), totalEntries))
		}

		if treePath != "" {
			pp := parentPathOf(treePath)
			ilink := mLinkR(iFolder, pagePathTree, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", pp}})
			parentLink := mLinkR(" ../", pagePathTree, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", pp}})
			contentParts = append(contentParts, fmt.Sprintf("%s%s`f%s\n", clrFolder, ilink, parentLink))
		}

		for _, entry := range pageEntries {
			entryName := entry.name
			entryType := entry.typ

			if entryType == "tree" {
				subpath := entryName
				if treePath != "" {
					subpath = treePath + "/" + entryName
				}
				ilink := mLinkR(iFolder, pagePathTree, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", subpath}})
				link := mLinkR(" "+entryName+"/", pagePathTree, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", subpath}})
				contentParts = append(contentParts, fmt.Sprintf("%s%s`f%s\n", clrFolder, ilink, link))
			} else if entryType == "commit" {
				contentParts = append(contentParts, fmt.Sprintf("%s⧉`f %s `F666(submodule)`f\n", clrFolder, entryName))
			} else if entryType == "link" {
				target := entry.linkTarget
				if target == "" {
					target = "unknown"
				}
				contentParts = append(contentParts, fmt.Sprintf("%s↳`f %s `F666→ %s`f\n", clrFile, entryName, mEscape(target)))
			} else {
				sizeStr := formatSize(entry.size)
				subpath := entryName
				if treePath != "" {
					subpath = treePath + "/" + entryName
				}
				ilink := mLinkR(iFile, pagePathBlob, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", subpath}})
				link := mLinkR(" "+entryName, pagePathBlob, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", subpath}})
				contentParts = append(contentParts, fmt.Sprintf("%s%s`f%s `F666(%s)`f\n", clrFile, ilink, link, sizeStr))
			}
		}

		contentParts = append(contentParts, "\n")

		if totalEntries > treeEntriesPerPage {
			var navLinks []string
			if pageNum > 0 {
				navLinks = append(navLinks, mLink("« Previous", pagePathTree,
					[]linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", treePath}, {"page", pageNum - 1}}))
			}
			totalPages := (totalEntries + treeEntriesPerPage - 1) / treeEntriesPerPage
			navLinks = append(navLinks, fmt.Sprintf("Page %d of %d", pageNum+1, totalPages))
			if endIdx < totalEntries {
				navLinks = append(navLinks, mLink("Next »", pagePathTree,
					[]linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", treePath}, {"page", pageNum + 1}}))
			}
			contentParts = append(contentParts, strings.Join(navLinks, " | ")+"\n")
		}
	}

	if len(contentParts) > 0 && contentParts[len(contentParts)-1] == "\n" {
		contentParts[len(contentParts)-1] = ""
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	navContent := strings.Join(navParts, "")
	return p.render("tree", pageContent, navContent, start)
}

// parentPathOf mirrors the Python `"/".join(tree_path.rstrip("/").split("/")[:-1])`
// (pages.py:623): the parent directory of treePath, "" at root.
func parentPathOf(treePath string) string {
	trimmed := strings.TrimRight(treePath, "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return strings.Join(parts[:len(parts)-1], "/")
}

// safeShort returns s truncated to n chars (or s if shorter).
func safeShort(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// serveBlobPage mirrors serve_blob_page (pages.py:680-806).
func (p *pageNode) serveBlobPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	ref := vstr(vars, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	filePath := vstr(vars, "path")
	filePath, _ = url.QueryUnescape(filePath)
	render := vbool(vars, "render")
	raw := vbool(vars, "raw")

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Not Found", 1) + "\n\nThe requested repository does not exist or you do not have access to it.\n"
		return p.render("blob", content, "", start)
	}
	repoPath := repo.path

	resolvedRef := p.resolveRef(repoPath, ref)
	if resolvedRef == "" {
		content := mHeading("Ref Not Found", 1) + fmt.Sprintf("\n\nThe ref '%s' does not exist in this repository.\n", ref)
		return p.render("blob", content, "", start)
	}

	if filePath == "" {
		content := mHeading("Invalid Path", 1) + "\n\nNo file path specified.\n"
		return p.render("blob", content, "", start)
	}

	filePath = strings.TrimLeft(filePath, "./")
	filePath = strings.ReplaceAll(filePath, "/./", "/")
	fileExt := strings.ToLower(filepath.Ext(filePath))
	renderable := false
	if _, ok := renderableExts[fileExt]; ok {
		renderable = true
	}
	if !renderable {
		raw = true
		render = false
	} else {
		if raw {
			render = false
		} else if !render {
			if _, ok := renderDefault[fileExt]; ok {
				render = true
				raw = false
			}
		}
	}

	contentParts := []string{}
	navParts := []string{}

	breadcrumbParts := []string{
		mLink("Node", pagePathIndex, nil),
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}),
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}),
		mLink("files", pagePathTree, []linkField{{"g", groupName}, {"r", repoName}}),
	}
	pathComponents := strings.Split(strings.Trim(filePath, "/"), "/")
	currentPath := ""
	for i, component := range pathComponents {
		if currentPath != "" {
			currentPath = currentPath + "/" + component
		} else {
			currentPath = component
		}
		if i == len(pathComponents)-1 {
			breadcrumbParts = append(breadcrumbParts, component)
		} else {
			breadcrumbParts = append(breadcrumbParts, mLink(component, pagePathTree,
				[]linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", currentPath}}))
		}
	}
	breadcrumb := strings.Join(breadcrumbParts, " / ")
	navParts = append(navParts, ">>\n"+breadcrumb+"\n")
	sep := p.icon("sep")

	dlLink := mLink("Download", fileDownload, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", filePath}})
	if !renderable {
		navParts = append(navParts, fmt.Sprintf("\nDisplaying Raw %s %s\n", sep, dlLink))
	} else {
		rndLink := mLink("View rendered", pagePathBlob, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", filePath}, {"render", "y"}})
		rawLink := mLink("View raw", pagePathBlob, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", filePath}, {"raw", "y"}})
		var renderControls string
		if render {
			renderControls = fmt.Sprintf("Displaying Rendered %s %s", sep, rawLink)
		} else {
			renderControls = fmt.Sprintf("Displaying Raw %s %s", sep, rndLink)
		}
		navParts = append(navParts, fmt.Sprintf("\n%s %s %s\n", renderControls, sep, dlLink))
	}

	blobInfo := p.getBlobInfo(repoPath, resolvedRef, filePath)
	if blobInfo == nil {
		contentParts = append(contentParts, "File not found at this ref.\n")
	} else {
		if blobInfo.isTree {
			return p.serveTreePage(path, data, requestID, linkID, remoteIdentity, requestedAt)
		}
		typeStr := "Text"
		if blobInfo.isBinary {
			typeStr = "Binary"
		}
		sizeStr := rns.PrettySize(float64(blobInfo.size), "")
		symlinkStr := ""
		if blobInfo.isSymlink {
			t := blobInfo.symlinkTarget
			if t == "" {
				t = "unknown"
			}
			symlinkStr = fmt.Sprintf(" | Symlink → %s", mEscape(t))
		}
		contentParts = append(contentParts, mHeading(fmt.Sprintf("%s %s%s (%s) %s, %s%s\n",
			filePath, clrDimH, ref, safeShort(resolvedRef, 8), typeStr, sizeStr, symlinkStr), 2))

		switch {
		case blobInfo.isSymlink:
			t := blobInfo.symlinkTarget
			if t == "" {
				t = "unknown"
			}
			contentParts = append(contentParts, fmt.Sprintf("`*%s`*\n", mEscape(t)))
		case blobInfo.isBinary:
			contentParts = append(contentParts, "This file appears to be binary and cannot be displayed as text.\n")
		case blobInfo.size > blobSizeLimit:
			contentParts = append(contentParts, fmt.Sprintf("This file is %s, which exceeds the display limit of %s.\n",
				rns.PrettySize(float64(blobInfo.size), ""), rns.PrettySize(float64(blobSizeLimit), "")))
		default:
			content := p.getBlobContent(repoPath, resolvedRef, filePath)
			if content != "" {
				if renderable && render {
					switch fileExt {
					case ".mu":
						contentParts = append(contentParts, strings.TrimRight(content, " \t\r\n")+"\n")
					case ".md":
						comps := strings.Split(strings.Trim(filePath, "/"), "/")
						urlPath := ""
						if len(comps) > 1 {
							urlPath = strings.Join(comps[:len(comps)-1], "/") + "/"
						}
						urlScope := fmt.Sprintf(":/page/blob.mu`g=%s|r=%s|ref=%s|path=%s", groupName, repoName, ref, urlPath)
						mdc := micron.NewConverter(micron.WithMaxWidth(maxRenderWidth), micron.WithHighlighter(p.highlighter), micron.WithURLScope(urlScope))
						converted := mdc.FormatBlock(content)
						contentParts = append(contentParts, strings.TrimRight(converted, " \t\r\n")+"\n")
					default:
						contentParts = append(contentParts, fmt.Sprintf("`=\n%s\n`=", content))
					}
				} else {
					if p.highlightSyntax {
						highlighted, _ := p.highlighter.Highlight(content, filePath, "")
						contentParts = append(contentParts, strings.TrimRight(highlighted, " \t\r\n")+"\n")
					} else {
						contentParts = append(contentParts, fmt.Sprintf("`=\n%s\n`=", content))
					}
				}
			} else {
				contentParts = append(contentParts, "Error reading file content.\n")
			}
		}
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	navContent := strings.Join(navParts, "")
	return p.render("blob", pageContent, navContent, start)
}

// serveCommitsPage mirrors serve_commits_page (pages.py:808-889).
func (p *pageNode) serveCommitsPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	ref := vstr(vars, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	filePath := vstr(vars, "path")
	filePath, _ = url.QueryUnescape(filePath)
	pageNum := vint(vars, "page", 0)
	if pageNum < 0 {
		pageNum = 0
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Not Found", 1) + "\n\nThe requested repository does not exist or you do not have access to it.\n"
		return p.render("commits", content, "", start)
	}
	repoPath := repo.path

	resolvedRef := p.resolveRef(repoPath, ref)
	if resolvedRef == "" {
		content := mHeading("Ref Not Found", 1) + fmt.Sprintf("\n\nThe ref '%s' does not exist in this repository.\n", ref)
		return p.render("commits", content, "", start)
	}

	contentParts := []string{}
	navParts := []string{}

	breadcrumbParts := []string{
		mLink("Node", pagePathIndex, nil),
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}),
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}),
		"commits",
	}
	if filePath != "" {
		// insert at index 3
		breadcrumbParts = append(breadcrumbParts[:3], append([]string{mEscape(filePath)}, breadcrumbParts[3:]...)...)
	}
	breadcrumb := strings.Join(breadcrumbParts, " / ")
	navParts = append(navParts, ">>\n"+breadcrumb+"\n")

	titleSuffix := ""
	if filePath != "" {
		titleSuffix = " for " + filePath
	}

	skip := pageNum * commitsPerPage
	commits := p.getCommits(repoPath, resolvedRef, filePath, skip, commitsPerPage)
	if commits == nil {
		contentParts = append(contentParts, "Error reading commit history.\n")
	} else if len(commits) == 0 {
		contentParts = append(contentParts, "No commits found.\n")
	} else {
		contentParts = append(contentParts, mHeading(fmt.Sprintf("Commits%s %s%s (%s)`f", titleSuffix, clrDimH, ref, safeShort(resolvedRef, 8)), 2))
		contentParts = append(contentParts, "\n")
		now := time.Now().Unix()
		for _, commit := range commits {
			shortHash := safeShort(commit.hash, 7)
			date := formatAbsoluteTime(commit.timestamp) + " - " + formatRelativeTime(commit.timestamp, now)
			hashLink := mLink(shortHash, pagePathCommit, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"h", commit.hash}})
			contentParts = append(contentParts, fmt.Sprintf("`F66d%s`f %s %s%s`f\n", hashLink, mEscape(commit.author), clrDim, date))
			contentParts = append(contentParts, fmt.Sprintf("%s\n\n", mEscape(commit.subject)))
		}
		hasMore := len(commits) == commitsPerPage
		if pageNum > 0 || hasMore {
			var navLinks []string
			if pageNum > 0 {
				navLinks = append(navLinks, mLink("« Newer", pagePathCommits,
					[]linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", filePath}, {"page", pageNum - 1}}))
			}
			navLinks = append(navLinks, fmt.Sprintf("Page %d", pageNum+1))
			if hasMore {
				navLinks = append(navLinks, mLink("Older »", pagePathCommits,
					[]linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"path", filePath}, {"page", pageNum + 1}}))
			}
			contentParts = append(contentParts, strings.Join(navLinks, " | ")+"\n")
		}
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	navContent := strings.Join(navParts, "")
	return p.render("commits", pageContent, navContent, start)
}

// serveCommitPage mirrors serve_commit_page (pages.py:891-1041).
func (p *pageNode) serveCommitPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	ref := vstr(vars, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	commitHash := vstr(vars, "h")

	if groupName == "" || repoName == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("commit", content, "", start)
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Error", 2) + "\nThe requested repository was not found.\n"
		return p.render("commit", content, "", start)
	}
	repoPath := repo.path

	resolvedRef := p.resolveRef(repoPath, ref)
	if resolvedRef == "" {
		content := mHeading("Ref Not Found", 1) + fmt.Sprintf("\n\nThe ref '%s' does not exist in this repository.\n", ref)
		return p.render("commit", content, "", start)
	}

	if len(commitHash) < 7 {
		content := mHeading("Error", 2) + "\nNo valid commit hash specified.\n"
		return p.render("commit", content, "", start)
	}

	resolvedHash := p.resolveRef(repoPath, commitHash)
	if resolvedHash == "" {
		content := mHeading("Error", 2) + fmt.Sprintf("\nThe commit %s does not exist in this repository.\n", commitHash)
		return p.render("commit", content, "", start)
	}

	navParts := []string{}
	breadcrumb := mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " +
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}) + " / " +
		mLink("commits", pagePathCommits, []linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}}) + " / " +
		safeShort(resolvedHash, 7)
	navParts = append(navParts, ">>\n"+breadcrumb+"\n")
	navContent := strings.Join(navParts, "")

	// Verify it is a commit object.
	typeResult, ok := gitRun(repoPath, "cat-file", "-t", resolvedHash)
	if !ok || strings.TrimSpace(typeResult) != "commit" {
		content := mHeading("Error", 2) + fmt.Sprintf("\nThe hash %s does not refer to a commit.\n", commitHash)
		return p.render("commit", content, "", start)
	}

	contentParts := []string{}
	commitInfo := p.getCommitInfo(repoPath, resolvedHash, showDiffByDefault)
	if commitInfo == nil {
		contentParts = append(contentParts, mHeading("Error", 2)+"\n\nCould not retrieve commit information.\n")
		return p.render("commit", strings.Join(contentParts, ""), navContent, start)
	}

	contentParts = append(contentParts, mHeading("Commit "+resolvedHash, 2))
	contentParts = append(contentParts, "\n")

	iFolder := p.icon("folder")
	contentParts = append(contentParts, mLink(iFolder+" Browse tree at this commit", pagePathTree,
		[]linkField{{"g", groupName}, {"r", repoName}, {"ref", resolvedHash}})+"\n\n")

	showSig := false
	sigStatus := p.getCommitSignature(repoPath, resolvedHash)
	sigText := "Not signed"
	if sigStatus.signed {
		if sigStatus.valid && sigStatus.authorMatch {
			sigText = "`FT66BB85Valid, signed by author`f"
			showSig = true
		} else if sigStatus.valid {
			sigText = fmt.Sprintf("`Faa0%s`f", mEscape(sigStatus.message))
			showSig = true
		} else {
			sigText = fmt.Sprintf("`F900%s`f", mEscape(sigStatus.message))
			showSig = true
		}
	}

	if len(commitInfo.parents) > 0 {
		var parentLinks []string
		for _, parentHash := range commitInfo.parents {
			parentLinks = append(parentLinks, mLink(safeShort(parentHash, 7), pagePathCommit,
				[]linkField{{"g", groupName}, {"r", repoName}, {"ref", ref}, {"h", parentHash}}))
		}
		contentParts = append(contentParts, fmt.Sprintf("Parents    : %s\n", strings.Join(parentLinks, " ")))
	}
	contentParts = append(contentParts, fmt.Sprintf("Author     : %s <%s>\n", mEscape(commitInfo.authorName), mEscape(commitInfo.authorEmail)))
	if showSig {
		contentParts = append(contentParts, "Signature  : "+sigText+"\n")
	}
	contentParts = append(contentParts, fmt.Sprintf("Date       : %s\n", commitInfo.authorDate))
	if commitInfo.committerName != commitInfo.authorName {
		contentParts = append(contentParts, fmt.Sprintf("Committer : %s <%s>\n", mEscape(commitInfo.committerName), mEscape(commitInfo.committerEmail)))
		contentParts = append(contentParts, fmt.Sprintf("Date      : %s\n", commitInfo.committerDate))
	}
	contentParts = append(contentParts, "\n")

	if commitInfo.message != "" {
		contentParts = append(contentParts, formatCommit(commitInfo.message)+"\n")
		contentParts = append(contentParts, "\n")
	}

	if len(commitInfo.files) > 0 {
		contentParts = append(contentParts, mHeading("Changes", 2))
		contentParts = append(contentParts, "\n")
		var totalAdditions, totalDeletions int64
		for _, f := range commitInfo.files {
			totalAdditions += f.additions
			totalDeletions += f.deletions
		}
		contentParts = append(contentParts, fmt.Sprintf("  %d files changed, %d insertions(+), %d deletions(-)\n\n",
			len(commitInfo.files), totalAdditions, totalDeletions))
		statusIndicators := map[string]string{
			"A": "`F0a0A`f", "D": "`F900D`f", "M": "`Faa0M`f", "R": "`F0aaR`f",
		}
		for _, f := range commitInfo.files {
			status := f.status
			if status == "" {
				status = "M"
			}
			statusDisplay, ok := statusIndicators[status]
			if !ok {
				statusDisplay = status
			}
			fileLink := mLink(mEscape(f.path), pagePathBlob,
				[]linkField{{"g", groupName}, {"r", repoName}, {"ref", resolvedHash}, {"path", f.path}})
			var stats []string
			if f.additions > 0 {
				stats = append(stats, fmt.Sprintf("`F0a0+%d`f", f.additions))
			}
			if f.deletions > 0 {
				stats = append(stats, fmt.Sprintf("`F900-%d`f", f.deletions))
			}
			statsStr := strings.Join(stats, " ")
			contentParts = append(contentParts, fmt.Sprintf("  %s %s %s\n", statusDisplay, fileLink, statsStr))
		}
		contentParts = append(contentParts, "\n")
	}

	if showDiffByDefault && commitInfo.diff != "" {
		contentParts = append(contentParts, mHeading("Diff", 2))
		contentParts = append(contentParts, "\n")
		contentParts = append(contentParts, strings.TrimLeft(formatDiff(commitInfo.diff), " \t"))
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	return p.render("commit", pageContent, navContent, start)
}

// serveRefsPage mirrors serve_refs_page (pages.py:1043-1145).
func (p *pageNode) serveRefsPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	refType := vstr(vars, "type")

	contentParts := []string{}
	navParts := []string{}

	breadcrumb := mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " +
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}) + " / refs"
	navParts = append(navParts, ">>\n"+breadcrumb+"\n")
	navContent := strings.Join(navParts, "")

	if groupName == "" || repoName == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("refs", content, "", start)
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Error", 2) + "\nThe requested repository was not found.\n"
		return p.render("refs", content, navContent, start)
	}
	repoPath := repo.path

	iSep := p.icon("sep")
	filterLinks := []string{
		mLink("All", pagePathRefs, []linkField{{"g", groupName}, {"r", repoName}}),
		mLink("Branches only", pagePathRefs, []linkField{{"g", groupName}, {"r", repoName}, {"type", "heads"}}),
		mLink("Tags only", pagePathRefs, []linkField{{"g", groupName}, {"r", repoName}, {"type", "tags"}}),
	}
	contentParts = append(contentParts, strings.Join(filterLinks, " "+iSep+" ")+"\n\n")

	defaultBranch := ""
	if out, ok := gitRun(repoPath, "symbolic-ref", "HEAD"); ok {
		defaultBranch = strings.ReplaceAll(strings.TrimSpace(out), "refs/heads/", "")
	}

	showHeads := refType == "" || refType == "heads"
	showTags := refType == "" || refType == "tags"

	heads, tags := p.getRefsInfo(repoPath, defaultBranch)

	if showHeads && len(heads) > 0 {
		contentParts = append(contentParts, mHeading(fmt.Sprintf("Branches (%d)", len(heads)), 2))
		contentParts = append(contentParts, "\n")
		for _, refInfo := range heads {
			branchName := refInfo.name
			shortHash := refInfo.shortHash
			isDefault := refInfo.isDefault
			commitSubject := refInfo.commitSubject
			var nameDisplay string
			if isDefault {
				nameDisplay = fmt.Sprintf("`F0a0%s`f", branchName)
			} else {
				nameDisplay = branchName
			}
			defaultMarker := ""
			if isDefault {
				defaultMarker = " `F0a0(default)`f"
			}
			treeLink := mLink("tree", pagePathTree, []linkField{{"g", groupName}, {"r", repoName}, {"ref", branchName}})
			commitsLink := mLink("commits", pagePathCommits, []linkField{{"g", groupName}, {"r", repoName}, {"ref", branchName}})
			contentParts = append(contentParts, fmt.Sprintf("%s%s [%s] [%s]\n", nameDisplay, defaultMarker, treeLink, commitsLink))
			contentParts = append(contentParts, fmt.Sprintf("%s: %s\n\n", shortHash, mEscape(commitSubject)))
		}
	}

	if showTags && len(tags) > 0 {
		contentParts = append(contentParts, mHeading(fmt.Sprintf("Tags (%d)", len(tags)), 2))
		contentParts = append(contentParts, "\n")
		for i := len(tags) - 1; i >= 0; i-- {
			refInfo := tags[i]
			tagName := refInfo.name
			shortHash := refInfo.shortHash
			isAnnotated := refInfo.isAnnotated
			tagMessage := refInfo.tagMessage
			commitSubject := refInfo.commitSubject
			annotatedMarker := ""
			if isAnnotated {
				annotatedMarker = " `Faa0(annotated)`f"
			}
			treeLink := mLink("tree", pagePathTree, []linkField{{"g", groupName}, {"r", repoName}, {"ref", tagName}})
			commitsLink := mLink("commits", pagePathCommits, []linkField{{"g", groupName}, {"r", repoName}, {"ref", tagName}})
			contentParts = append(contentParts, fmt.Sprintf("%s%s %s%s`f [%s] [%s]\n", tagName, annotatedMarker, clrDim, shortHash, treeLink, commitsLink))
			if isAnnotated && tagMessage != "" {
				contentParts = append(contentParts, mEscape(safeShort(tagMessage, 512))+"\n\n")
			} else {
				contentParts = append(contentParts, mEscape(commitSubject)+"\n\n")
			}
		}
	}

	if showHeads && len(heads) == 0 && showTags && len(tags) == 0 {
		contentParts = append(contentParts, "No refs found in this repository.\n")
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.TrimRight(strings.Join(contentParts, ""), " \t\r\n") + "\n"
	return p.render("refs", pageContent, navContent, start)
}

// icon returns the glyph for name using the pageNode's font preference,
// mirroring NomadNetworkNode.icon (pages.py:180-205).
func (p *pageNode) icon(name string) string {
	return icon(name, p.useNerdFonts)
}

// keep referenced imports when helpers are pruned.
var (
	_ = os.Stat
	_ = filepath.Clean
)

// minInt returns the smaller of a and b.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
