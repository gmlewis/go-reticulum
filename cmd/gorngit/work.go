// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// work.go implements the gorngit /mgmt/work request handler and its
// sub-operations, mirroring handle_work and the _work_* helpers in
// RNS/Utilities/rngit/server.py (rngit v1.4.2).

package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// workDocLimit is the maximum combined size of a work document's
// title+content+format fields, mirroring WORK_DOC_LIMIT (server.py).
const workDocLimit = 256 * 1024

// workScopes is the ordered list of work-document scopes, mirroring the
// iteration order used by _work_list / _work_view (server.py).
var workScopes = []string{"active", "completed", "proposed"}

// signatureLength is the byte length of an Ed25519 signature, mirroring
// RNS.Identity.SIGLENGTH//8 (server.py). IdentityKeySize is 512 bits, so
// the signature length is 64 bytes.
const signatureLength = rns.IdentityKeySize / 8

// handleWork is the /mgmt/work request handler, mirroring handle_work
// (server.py). It resolves repository + per-document permissions, computes
// the read/comment/propose/manage/admin access tiers, and dispatches to the
// matching _work_* sub-operation.
func (n *reticulumGitNode) handleWork(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
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
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	interactAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permInteract)
	proposeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permPropose)
	adminAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permAdmin)

	if !readAccess {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	// Per-document permission augmentation (mirrors handle_work). doc_id is
	// only resolved when present and truthy (non-zero), matching Python's
	// `if data.get("doc_id", None):` truthiness check.
	docID, docIDOK := resolveDocID(m)
	if operation == "read" || operation == "view" || operation == "comment" || operation == "edit" || operation == "delete" || operation == "perms" {
		if docIDOK {
			docRead := n.resolveDocPermission(remoteIdentity, "", docID, permRead)
			if docRead || adminAccess {
				readAccess = true
			} else {
				readAccess = false
			}
			if !readAccess {
				return append([]byte{resNotFound}, []byte("Document not found")...)
			}
		}
	}
	if operation == "comment" && docIDOK {
		docInteract := n.resolveDocPermission(remoteIdentity, "", docID, permInteract)
		interactAccess = interactAccess || docInteract
	}
	if operation == "edit" && docIDOK {
		docInteract := n.resolveDocPermission(remoteIdentity, "", docID, permInteract)
		docWrite := n.resolveDocPermission(remoteIdentity, "", docID, permWrite)
		interactAccess = interactAccess || docInteract
		writeAccess = writeAccess || docWrite
	}

	commentAccess := interactAccess && (readAccess || writeAccess)
	manageAccess := interactAccess && writeAccess

	access := false
	switch {
	case (operation == "list" || operation == "view") && readAccess:
		access = true
	case operation == "comment" && commentAccess:
		access = true
	case operation == "propose" && proposeAccess:
		access = true
	case (operation == "create" || operation == "edit" || operation == "delete") && manageAccess:
		access = true
	case (operation == "complete" || operation == "activate") && manageAccess:
		access = true
	case operation == "perms" && adminAccess:
		access = true
	}
	if !access {
		return append([]byte{resDisallowed}, []byte("Not allowed")...)
	}

	repo, ok := n.lookupRepository(groupName, repositoryName)
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	workPath := repo.path + ".work"

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
		result = n.workList(workPath, m, remoteIdentity)
	case "view":
		result = n.workView(workPath, m, remoteIdentity)
	case "comment":
		result = n.workComment(workPath, m, remoteIdentity)
	case "create":
		result = n.workCreate(workPath, m, remoteIdentity)
	case "propose":
		result = n.workPropose(workPath, m, remoteIdentity)
	case "edit":
		result = n.workEdit(workPath, m, remoteIdentity)
	case "delete":
		result = n.workDelete(workPath, m, remoteIdentity)
	case "complete":
		result = n.workComplete(workPath, m, remoteIdentity)
	case "activate":
		result = n.workActivate(workPath, m, remoteIdentity)
	case "perms":
		result = n.workPerms(workPath, m, remoteIdentity)
	default:
		result = append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	return result
}

// resolveDocID extracts a truthy doc_id from the request map, mirroring
// Python's `data.get("doc_id", None)` followed by `if value:`. Returns the
// int value and ok=true when doc_id is present and non-zero.
func resolveDocID(m map[any]any) (int, bool) {
	val, ok := getMapValue(m, "doc_id")
	if !ok || val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case int:
		if v == 0 {
			return 0, false
		}
		return v, true
	case int64:
		if v == 0 {
			return 0, false
		}
		return int(v), true
	case uint64:
		if v == 0 {
			return 0, false
		}
		return int(v), true
	case string:
		id, err := strconv.Atoi(v)
		if err != nil || id == 0 {
			return 0, false
		}
		return id, true
	}
	return 0, false
}

// workGetNextID returns the next global work-document id across all three
// scopes, mirroring _work_get_next_id (server.py). IDs are max+1 across
// active/completed/proposed and default to 1 when a scope is empty.
func workGetNextID(workPath string) int {
	maxID := 0
	for _, scope := range workScopes {
		basePath := filepath.Join(workPath, scope)
		entries, err := os.ReadDir(basePath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			id, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			if id > maxID {
				maxID = id
			}
		}
	}
	return maxID + 1
}

// workGetNextCommentID returns the next comment id within a document
// directory, mirroring _work_get_next_comment_id (server.py). It is max+1
// of the numeric regular files in baseDir and defaults to 1 when empty.
func workGetNextCommentID(baseDir string) int {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return 1
	}
	maxID := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if id > maxID {
			maxID = id
		}
	}
	return maxID + 1
}

// workLoadDocument reads and unpacks a msgpack work document or comment,
// mirroring _work_load_document (server.py). Returns nil on any error.
func workLoadDocument(docPath string) map[any]any {
	data, err := os.ReadFile(docPath)
	if err != nil {
		return nil
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return nil
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		return nil
	}
	return m
}

// workSaveDocument atomically writes a msgpack work document or comment,
// mirroring _work_save_document (server.py). It writes docPath+".tmp" then
// os.Rename into place. Returns false on error.
func workSaveDocument(docPath string, document any) bool {
	dirPath := filepath.Dir(docPath)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return false
	}
	packed, err := msgpack.Pack(document)
	if err != nil {
		return false
	}
	tmpPath := docPath + ".tmp"
	if err := os.WriteFile(tmpPath, packed, 0o644); err != nil {
		return false
	}
	return os.Rename(tmpPath, docPath) == nil
}

// workList handles operation "list", mirroring _work_list (server.py). The
// response is resOK + msgpack({"active":[...], "completed":[...],
// "proposed":[...]}), each entry summarising a document. Lists are sorted
// by created descending.
func (n *reticulumGitNode) workList(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	groupName, repositoryName := parseRequestRepositoryPath(mustString(data, idxRepository))
	scopeVal, _ := getMapValue(data, "scope")
	scope, _ := scopeVal.(string)
	if scope == "" {
		scope = "active"
	}

	result := map[any]any{
		"active":    []any{},
		"completed": []any{},
		"proposed":  []any{},
	}
	for _, folderName := range workScopes {
		if scope != folderName && scope != "all" {
			continue
		}
		folderPath := filepath.Join(workPath, folderName)
		entries, err := os.ReadDir(folderPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			docID, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			if !n.resolveDocPermission(remoteIdentity, workPath, docID, permRead) {
				continue
			}
			docDir := filepath.Join(folderPath, entry.Name())
			rootPath := filepath.Join(docDir, "root")
			if !isFile(rootPath) {
				continue
			}
			doc := workLoadDocument(rootPath)
			if doc == nil {
				continue
			}
			meta, _ := mapVal(doc, "meta").(map[any]any)
			commentCount := countNumericFiles(docDir)
			result[folderName] = append(result[folderName].([]any), map[any]any{
				"id":       int64(docID),
				"title":    metaString(meta, "title", "Untitled"),
				"created":  metaFloat(meta, "created"),
				"edited":   metaFloat(meta, "edited"),
				"author":   metaHexAuthor(meta),
				"format":   metaString(meta, "format", "markdown"),
				"comments": int64(commentCount),
			})
		}
	}
	_ = groupName
	_ = repositoryName
	for _, key := range workScopes {
		docs, _ := result[key].([]any)
		sort.SliceStable(docs, func(i, j int) bool {
			ci := docCreatedFloat(docs[i])
			cj := docCreatedFloat(docs[j])
			return ci > cj
		})
		result[key] = docs
	}
	packed, err := msgpack.Pack(result)
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Error listing documents")...)
	}
	return append([]byte{resOK}, packed...)
}

// workView handles operation "view", mirroring _work_view (server.py). The
// scope argument is discarded; all three scopes are scanned to locate the
// document. The response is resOK + msgpack({id, scope, content, comments,
// meta:{title,created,edited,author,identity,signature,format}}).
func (n *reticulumGitNode) workView(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	scopeVal, _ := getMapValue(data, "scope")
	scope, _ := scopeVal.(string)
	if scope == "" {
		scope = "all"
	}
	if scope != "active" && scope != "completed" && scope != "proposed" && scope != "all" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}

	foundScope, docDir := locateWorkDoc(workPath, docID)
	if docDir == "" {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	rootPath := filepath.Join(docDir, "root")
	if !isFile(rootPath) {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}

	comments := []any{}
	entries, err := os.ReadDir(docDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			commentID, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			commentPath := filepath.Join(docDir, entry.Name())
			if !isFile(commentPath) {
				continue
			}
			comment := workLoadDocument(commentPath)
			if comment == nil {
				continue
			}
			cmeta, _ := mapVal(comment, "meta").(map[any]any)
			comments = append(comments, map[any]any{
				"id":      int64(commentID),
				"content": docString(comment, "content", ""),
				"created": metaFloat(cmeta, "created"),
				"edited":  metaFloat(cmeta, "edited"),
				"author":  metaHexAuthor(cmeta),
				"format":  metaString(cmeta, "format", "markdown"),
			})
		}
	}
	sort.SliceStable(comments, func(i, j int) bool {
		ci, _ := comments[i].(map[any]any)["id"].(int64)
		cj, _ := comments[j].(map[any]any)["id"].(int64)
		return ci < cj
	})

	meta, _ := mapVal(doc, "meta").(map[any]any)
	result := map[any]any{
		"id":       int64(docID),
		"scope":    foundScope,
		"content":  docString(doc, "content", ""),
		"comments": comments,
		"meta": map[any]any{
			"title":     metaString(meta, "title", "Untitled"),
			"created":   metaFloat(meta, "created"),
			"edited":    metaFloat(meta, "edited"),
			"author":    metaHexAuthor(meta),
			"identity":  metaBytes(meta, "identity"),
			"signature": metaBytes(meta, "signature"),
			"format":    metaString(meta, "format", "markdown"),
		},
	}
	packed, err := msgpack.Pack(result)
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}
	return append([]byte{resOK}, packed...)
}

// workCreate handles operation "create", mirroring _work_create
// (server.py). It validates the signature over the content, enforces the
// WORK_DOC_LIMIT, writes the document to active/<id>/root, and returns
// resOK + msgpack({"id":id, "scope":"active"}).
func (n *reticulumGitNode) workCreate(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	return n.workCreateInScope(workPath, data, remoteIdentity, "active")
}

// workPropose handles operation "propose", mirroring _work_propose
// (server.py). It is create but writes to proposed/<id>/root and writes the
// owner .allowed file. Returns resOK + msgpack({"id":id, "scope":"proposed"}).
func (n *reticulumGitNode) workPropose(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	return n.workCreateInScope(workPath, data, remoteIdentity, "proposed")
}

// workCreateInScope is the shared create/propose body, mirroring
// _work_create / _work_propose (server.py). scopeDir is "active" or
// "proposed". For "proposed" it also writes the owner .allowed file.
func (n *reticulumGitNode) workCreateInScope(workPath string, data map[any]any, remoteIdentity *rns.Identity, scopeDir string) []byte {
	title := strings.TrimSpace(mustString(data, "title"))
	content := strings.TrimSpace(mustString(data, "content"))
	formatType, _ := mapVal(data, "format").(string)
	if formatType == "" {
		formatType = "markdown"
	}
	signature := dataBytes(data, "signature")
	signedData := []byte(content)

	if len(signature) == 0 {
		return append([]byte{resInvalidReq}, []byte("No signature provided")...)
	}
	if len(signature) != signatureLength {
		return append([]byte{resInvalidReq}, []byte("Invalid signature length")...)
	}
	if !remoteIdentity.Verify(signature, signedData) {
		return append([]byte{resInvalidReq}, []byte("Invalid signature")...)
	}
	if len(title)+len(content)+len(formatType) > workDocLimit {
		return append([]byte{resInvalidReq}, []byte("Content limit exceeded")...)
	}
	if title == "" {
		return append([]byte{resInvalidReq}, []byte("Title is required")...)
	}
	if content == "" {
		return append([]byte{resInvalidReq}, []byte("Content is required")...)
	}

	if formatType != "markdown" && formatType != "micron" {
		formatType = "markdown"
	}

	scopePath := filepath.Join(workPath, scopeDir)
	docID := workGetNextID(workPath)
	docDir := filepath.Join(scopePath, strconv.Itoa(docID))
	now := float64(time.Now().UnixNano()) / 1e9
	document := map[any]any{
		"content": content,
		"meta": map[any]any{
			"format":    formatType,
			"title":     title,
			"created":   now,
			"edited":    now,
			"author":    append([]byte{}, remoteIdentity.Hash...),
			"signature": append([]byte{}, signature...),
			"identity":  append([]byte{}, remoteIdentity.GetPublicKey()...),
		},
	}
	rootPath := filepath.Join(docDir, "root")
	if !workSaveDocument(rootPath, document) {
		return append([]byte{resRemoteFail}, []byte("Error saving document")...)
	}

	if scopeDir == "proposed" {
		hexHash := fmt.Sprintf("%x", remoteIdentity.Hash)
		ownerPermissions := fmt.Sprintf("i:%s\nw:%s\n", hexHash, hexHash)
		allowedPath := filepath.Join(workPath, fmt.Sprintf("%d.allowed", docID))
		tmpPath := allowedPath + ".tmp"
		if err := os.WriteFile(tmpPath, []byte(ownerPermissions), 0o644); err != nil {
			return append([]byte{resRemoteFail}, []byte("Error setting document ownership")...)
		}
		if err := os.Rename(tmpPath, allowedPath); err != nil {
			return append([]byte{resRemoteFail}, []byte("Error setting document ownership")...)
		}
	}

	packed, err := msgpack.Pack(map[any]any{"id": int64(docID), "scope": scopeDir})
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return append([]byte{resOK}, packed...)
}

// workEdit handles operation "edit", mirroring _work_edit (server.py). It
// is author-only (doc.meta.author == remoteIdentity.hash), validates the
// signature over the new content, patches title/content/edited/signature/
// identity, and returns resOK alone.
func (n *reticulumGitNode) workEdit(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	scopeVal, _ := getMapValue(data, "scope")
	scope, _ := scopeVal.(string)
	if scope == "" {
		scope = "active"
	}
	if scope != "active" && scope != "completed" && scope != "proposed" && scope != "all" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	content, _ := mapVal(data, "content").(string)
	title, _ := mapVal(data, "title").(string)
	signature := dataBytes(data, "signature")
	signedData := []byte(content)

	size := 0
	if title != "" {
		size += len(title)
	}
	if content != "" {
		size += len(content)
	}
	if len(signature) == 0 {
		return append([]byte{resInvalidReq}, []byte("No signature provided")...)
	}
	if len(signature) != signatureLength {
		return append([]byte{resInvalidReq}, []byte("Invalid signature length")...)
	}
	if !remoteIdentity.Verify(signature, signedData) {
		return append([]byte{resInvalidReq}, []byte("Invalid signature")...)
	}
	if size > workDocLimit {
		return append([]byte{resInvalidReq}, []byte("Content limit exceeded")...)
	}
	if content == "" && title == "" {
		return append([]byte{resInvalidReq}, []byte("No changes specified")...)
	}
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}

	_, docDir := locateWorkDoc(workPath, docID)
	if docDir == "" {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	rootPath := filepath.Join(docDir, "root")
	if !isFile(rootPath) {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}
	meta, _ := mapVal(doc, "meta").(map[any]any)
	author, _ := meta["author"].([]byte)
	if !bytes.Equal(author, remoteIdentity.Hash) {
		return append([]byte{resDisallowed}, []byte("No access, not author")...)
	}

	if title != "" {
		meta["title"] = strings.TrimSpace(title)
	}
	if content != "" {
		doc["content"] = strings.TrimSpace(content)
	}
	meta["edited"] = float64(time.Now().UnixNano()) / 1e9
	meta["signature"] = append([]byte{}, signature...)
	meta["identity"] = append([]byte{}, remoteIdentity.GetPublicKey()...)
	doc["meta"] = meta

	if !workSaveDocument(rootPath, doc) {
		return append([]byte{resRemoteFail}, []byte("Error saving document")...)
	}
	return []byte{resOK}
}

// workDelete handles operation "delete", mirroring _work_delete
// (server.py). It is author OR PERM_ADMIN (doc-level). It removes the
// .allowed file then deletes the document directory. Returns resOK alone.
func (n *reticulumGitNode) workDelete(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	groupName, repositoryName := parseRequestRepositoryPath(mustString(data, idxRepository))
	scopeVal, _ := getMapValue(data, "scope")
	scope, _ := scopeVal.(string)
	if scope == "" {
		scope = "active"
	}
	if scope != "active" && scope != "completed" && scope != "proposed" && scope != "all" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}

	_, docDir := locateWorkDoc(workPath, docID)
	if docDir == "" {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	rootPath := filepath.Join(docDir, "root")
	if !isFile(rootPath) {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}
	meta, _ := mapVal(doc, "meta").(map[any]any)
	author, _ := meta["author"].([]byte)
	isAuthor := bytes.Equal(author, remoteIdentity.Hash)
	adminAccess := n.resolveDocPermission(remoteIdentity, workPath, docID, permAdmin)
	if !isAuthor && !adminAccess {
		return append([]byte{resDisallowed}, []byte("No access, not author")...)
	}
	_ = groupName
	_ = repositoryName

	allowedPath := filepath.Join(workPath, fmt.Sprintf("%d.allowed", docID))
	if err := os.Remove(allowedPath); err != nil && !os.IsNotExist(err) {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	if err := os.RemoveAll(docDir); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return []byte{resOK}
}

// workComment handles operation "comment", mirroring _work_comment
// (server.py). It stores the comment signature but does NOT validate it
// (only a size check). Writes doc_dir/<comment_id>. Returns resOK +
// msgpack({"id": comment_id}).
func (n *reticulumGitNode) workComment(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	scopeVal, _ := getMapValue(data, "scope")
	scope, _ := scopeVal.(string)
	if scope == "" {
		scope = "active"
	}
	if scope != "active" && scope != "completed" && scope != "proposed" && scope != "all" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	content := strings.TrimSpace(mustString(data, "content"))
	formatType, _ := mapVal(data, "format").(string)
	if formatType == "" {
		formatType = "markdown"
	}
	signature := dataBytes(data, "signature")
	if len(content) > workDocLimit {
		return append([]byte{resInvalidReq}, []byte("Content limit exceeded")...)
	}
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}
	if content == "" {
		return append([]byte{resInvalidReq}, []byte("Content is required")...)
	}

	_, docDir := locateWorkDoc(workPath, docID)
	if docDir == "" {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	rootPath := filepath.Join(docDir, "root")
	if !isFile(rootPath) {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}

	if formatType != "markdown" && formatType != "micron" {
		formatType = "markdown"
	}
	commentID := workGetNextCommentID(docDir)
	now := float64(time.Now().UnixNano()) / 1e9
	comment := map[any]any{
		"content": content,
		"meta": map[any]any{
			"format":    formatType,
			"title":     nil,
			"created":   now,
			"edited":    now,
			"signature": append([]byte{}, signature...),
			"author":    append([]byte{}, remoteIdentity.Hash...),
		},
	}
	commentPath := filepath.Join(docDir, strconv.Itoa(commentID))
	if !workSaveDocument(commentPath, comment) {
		return append([]byte{resRemoteFail}, []byte("Error saving comment")...)
	}
	packed, err := msgpack.Pack(map[any]any{"id": int64(commentID)})
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return append([]byte{resOK}, packed...)
}

// workComplete handles operation "complete", mirroring _work_complete
// (server.py). It is author-only. It moves active/<id> to completed/<id>
// and returns resOK + msgpack({"id":id, "scope":"completed"}).
func (n *reticulumGitNode) workComplete(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}
	activeDir := filepath.Join(workPath, "active", strconv.Itoa(docID))
	if !isDir(activeDir) {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}
	rootPath := filepath.Join(activeDir, "root")
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}
	meta, _ := mapVal(doc, "meta").(map[any]any)
	author, _ := meta["author"].([]byte)
	if !bytes.Equal(author, remoteIdentity.Hash) {
		return append([]byte{resDisallowed}, []byte("No access, not author")...)
	}
	completedDir := filepath.Join(workPath, "completed", strconv.Itoa(docID))
	if err := os.MkdirAll(filepath.Dir(completedDir), 0o755); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	if err := os.Rename(activeDir, completedDir); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	packed, err := msgpack.Pack(map[any]any{"id": int64(docID), "scope": "completed"})
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return append([]byte{resOK}, packed...)
}

// workActivate handles operation "activate", mirroring _work_activate
// (server.py). It is author-only. It moves completed/<id> to active/<id>
// and returns resOK + msgpack({"id":id, "scope":"active"}).
func (n *reticulumGitNode) workActivate(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}
	completedDir := filepath.Join(workPath, "completed", strconv.Itoa(docID))
	if !isDir(completedDir) {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}
	rootPath := filepath.Join(completedDir, "root")
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}
	meta, _ := mapVal(doc, "meta").(map[any]any)
	author, _ := meta["author"].([]byte)
	if !bytes.Equal(author, remoteIdentity.Hash) {
		return append([]byte{resDisallowed}, []byte("No access, not author")...)
	}
	activeDir := filepath.Join(workPath, "active", strconv.Itoa(docID))
	if err := os.MkdirAll(filepath.Dir(activeDir), 0o755); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	if err := os.Rename(completedDir, activeDir); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	packed, err := msgpack.Pack(map[any]any{"id": int64(docID), "scope": "active"})
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	return append([]byte{resOK}, packed...)
}

// workPerms handles operation "perms", mirroring _work_perms (server.py).
// It requires manage_access and dispatches to get/set based on "step".
func (n *reticulumGitNode) workPerms(workPath string, data map[any]any, remoteIdentity *rns.Identity) []byte {
	stepVal, _ := getMapValue(data, "step")
	step, _ := stepVal.(string)

	groupName, repositoryName := parseRequestRepositoryPath(mustString(data, idxRepository))
	readAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRead)
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	interactAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permInteract)
	manageAccess := interactAccess && writeAccess

	if !readAccess {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	if !manageAccess {
		return append([]byte{resDisallowed}, []byte("Not allowed")...)
	}
	if step == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	switch step {
	case "get":
		return n.workGetPermissions(workPath, data, remoteIdentity, groupName, repositoryName)
	case "set":
		return n.workSetPermissions(workPath, data, remoteIdentity, groupName, repositoryName)
	default:
		return append([]byte{resInvalidReq}, []byte("Invalid step")...)
	}
}

// workGetPermissions handles step "get", mirroring _work_get_permissions
// (server.py). It requires (is_author and manage_access) or admin_access.
// Returns resOK + msgpack({"content": <allowed file text>}).
func (n *reticulumGitNode) workGetPermissions(workPath string, data map[any]any, remoteIdentity *rns.Identity, groupName, repositoryName string) []byte {
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}
	_, docDir := locateWorkDoc(workPath, docID)
	if docDir == "" {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}
	rootPath := filepath.Join(docDir, "root")
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}
	meta, _ := mapVal(doc, "meta").(map[any]any)
	author, _ := meta["author"].([]byte)
	isAuthor := bytes.Equal(author, remoteIdentity.Hash)
	interactAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permInteract)
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	manageAccess := interactAccess && writeAccess
	adminAccess := n.resolveDocPermission(remoteIdentity, workPath, docID, permAdmin)
	if !((isAuthor && manageAccess) || adminAccess) {
		return append([]byte{resDisallowed}, []byte("Not allowed")...)
	}
	allowedPath := filepath.Join(workPath, fmt.Sprintf("%d.allowed", docID))
	content := ""
	if data, err := os.ReadFile(allowedPath); err == nil {
		content = string(data)
	}
	packed, err := msgpack.Pack(map[any]any{"content": content})
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Error getting permissions")...)
	}
	return append([]byte{resOK}, packed...)
}

// workSetPermissions handles step "set", mirroring _work_set_permissions
// (server.py). It requires (is_author and manage_access) or admin_access.
// It validates each non-# line via parsePermission and writes the .allowed
// file atomically. Returns resOK alone.
func (n *reticulumGitNode) workSetPermissions(workPath string, data map[any]any, remoteIdentity *rns.Identity, groupName, repositoryName string) []byte {
	docID, ok := resolveDocID(data)
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No document ID specified")...)
	}
	content, _ := mapVal(data, "content").(string)
	_, docDir := locateWorkDoc(workPath, docID)
	if docDir == "" {
		return append([]byte{resNotFound}, []byte("Document not found")...)
	}
	rootPath := filepath.Join(docDir, "root")
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return append([]byte{resRemoteFail}, []byte("Error loading document")...)
	}
	meta, _ := mapVal(doc, "meta").(map[any]any)
	author, _ := meta["author"].([]byte)
	isAuthor := bytes.Equal(author, remoteIdentity.Hash)
	interactAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permInteract)
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	manageAccess := interactAccess && writeAccess
	adminAccess := n.resolveDocPermission(remoteIdentity, workPath, docID, permAdmin)
	if !((isAuthor && manageAccess) || adminAccess) {
		return append([]byte{resDisallowed}, []byte("Not allowed")...)
	}

	for lineNum, line := range strings.Split(content, "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		perm, target := parsePermission(stripped)
		if perm == 0 || target == nil {
			return append([]byte{resInvalidReq}, []byte(fmt.Sprintf("Invalid permission %q on line %d", stripped, lineNum+1))...)
		}
	}

	allowedPath := filepath.Join(workPath, fmt.Sprintf("%d.allowed", docID))
	tmpPath := allowedPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return append([]byte{resRemoteFail}, []byte("Error setting permissions")...)
	}
	if err := os.Rename(tmpPath, allowedPath); err != nil {
		return append([]byte{resRemoteFail}, []byte("Error setting permissions")...)
	}
	return []byte{resOK}
}

// parsePermission parses a single "perm:target" permission line, mirroring
// parse_permission (server.py). It returns the perm byte (0 when unknown)
// and the target (permTargetNone, permTargetAll, or a []byte identity hash;
// nil when invalid). The identity-alias resolution is deferred (the full
// permissions subsystem is a later task); a 32-hex-char target is accepted
// as a literal identity hash, matching the Python length check.
func parsePermission(permissionString string) (byte, any) {
	comps := strings.Split(permissionString, ":")
	if len(comps) != 2 {
		return 0, nil
	}
	permStr := strings.ToLower(comps[0])
	target := comps[1]

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

	switch strings.ToLower(target) {
	case "n", "none", "nobody":
		return perm, permTargetNone
	case "a", "all", "everyone":
		return perm, permTargetAll
	}
	// A 32-hex-char target (TruncatedHashLength/8*2) is accepted as a
	// literal identity hash.
	if len(target) == rns.TruncatedHashLength/8*2 {
		hashBytes, err := hexDecode(target)
		if err != nil {
			return 0, nil
		}
		return perm, hashBytes
	}
	return 0, nil
}

// locateWorkDoc scans the three scopes for the document directory named
// docID, mirroring the scan loop in _work_view / _work_edit / _work_delete
// (server.py). Returns the scope and docDir, or ("", "") when not found.
func locateWorkDoc(workPath string, docID int) (string, string) {
	name := strconv.Itoa(docID)
	for _, s := range workScopes {
		d := filepath.Join(workPath, s, name)
		if isDir(d) {
			return s, d
		}
	}
	return "", ""
}

// countNumericFiles returns the number of regular files in dir whose name
// is all digits, mirroring the comment-count expression in _work_list
// (server.py).
func countNumericFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err == nil {
			count++
		}
	}
	return count
}

// mustString fetches a string value from the map, returning "" when absent.
func mustString(m map[any]any, key any) string {
	val, ok := getMapValue(m, key)
	if !ok {
		return ""
	}
	s, _ := val.(string)
	return s
}

// mapVal returns the value for key in m (or nil when absent), as a single
// value so it can be used in type-assertion context
// (e.g. mapVal(m, "meta").(map[any]any)).
func mapVal(m map[any]any, key any) any {
	v, _ := getMapValue(m, key)
	return v
}

// dataBytes fetches a byte-slice value from the map, returning nil when
// absent. Accepts both []byte and string (coerced), mirroring the Python
// acceptance of str/bytes for signature fields.
func dataBytes(m map[any]any, key string) []byte {
	val, ok := getMapValue(m, key)
	if !ok || val == nil {
		return nil
	}
	if b, ok := val.([]byte); ok {
		return b
	}
	if s, ok := val.(string); ok {
		return []byte(s)
	}
	return nil
}

// docString fetches a string value from a document map with a default.
func docString(doc map[any]any, key, def string) string {
	val, ok := doc[key]
	if !ok || val == nil {
		return def
	}
	s, _ := val.(string)
	if s == "" {
		return def
	}
	return s
}

// metaString fetches a string value from a meta map with a default.
func metaString(meta map[any]any, key, def string) string {
	if meta == nil {
		return def
	}
	val, ok := meta[key]
	if !ok || val == nil {
		return def
	}
	s, _ := val.(string)
	if s == "" {
		return def
	}
	return s
}

// metaFloat fetches a numeric value from a meta map as float64, defaulting
// to 0. It accepts int64, uint64, int, and float64 (msgpack unpack forms).
func metaFloat(meta map[any]any, key string) float64 {
	if meta == nil {
		return 0
	}
	val, ok := meta[key]
	if !ok || val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case uint64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

// metaBytes fetches a byte-slice value from a meta map, returning nil when
// absent. The nil is preserved through msgpack so the wire shape matches
// Python's meta.get("identity", None).
func metaBytes(meta map[any]any, key string) []byte {
	if meta == nil {
		return nil
	}
	val, ok := meta[key]
	if !ok || val == nil {
		return nil
	}
	b, _ := val.([]byte)
	return b
}

// metaHexAuthor returns the hex-string form of the meta author hash,
// mirroring RNS.hexrep(meta.get("author", b""), delimit=False). Returns ""
// when the author is missing or empty.
func metaHexAuthor(meta map[any]any) string {
	if meta == nil {
		return ""
	}
	val, ok := meta["author"]
	if !ok || val == nil {
		return ""
	}
	b, _ := val.([]byte)
	if len(b) == 0 {
		return ""
	}
	return fmt.Sprintf("%x", b)
}

// docCreatedFloat returns the "created" field of a list entry as float64.
func docCreatedFloat(entry any) float64 {
	m, _ := entry.(map[any]any)
	return metaFloat(m, "created")
}

// hexDecode decodes a hex string into bytes, returning an error on invalid
// input. Used by parsePermission for identity-hash targets.
func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}
