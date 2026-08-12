// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

// work-int_test.go implements a two-node integration test for the gorngit
// /mgmt/work wire path over paired UDP interfaces on 127.0.0.1.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestIntegrationWorkRoundTrip verifies the full work-document lifecycle
// over RNS: propose, list, view (assert signature valid), comment, edit,
// complete, activate, delete — asserting wire responses and on-disk state.
func TestIntegrationWorkRoundTrip(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-workrt-")

	// Seed a bare repo on the server side.
	nodeConfigDir := testutils.TempDir(t, "gorngit-workrt-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-workrt-repos-")
	repoName := "workrepo.git"
	repoPath := filepath.Join(repoRoot, repoName)
	createEmptyBareRepo(t, repoPath)
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	// Connect a client.
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %v", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-workrt-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %v", err)
	}
	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %v", err)
	}
	defer client.teardown()

	repoPathStr := "main/" + repoName
	workPath := repoPath + ".work"

	// 1. Propose a work document.
	content := "Proposed work document body."
	signature, err := client.identity.Sign([]byte(content))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	proposeData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "propose",
		"title":              "Proposal",
		"content":            content,
		"format":             "markdown",
		"signature":          signature,
	}
	packed, err := msgpack.Pack(proposeData)
	if err != nil {
		t.Fatalf("pack propose: %v", err)
	}
	resp, _, err := client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("propose request: %v", err)
	}
	respBytes, ok := resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("propose response code = %x, want resOK", firstByte(respBytes))
	}
	proposeUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack propose: %v", err)
	}
	proposeMap, ok := proposeUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("propose response is %T, want map", proposeUnpacked)
	}
	if proposeMap["scope"] != "proposed" {
		t.Errorf("propose scope = %v, want proposed", proposeMap["scope"])
	}
	docID, _ := proposeMap["id"].(int64)
	if docID == 0 {
		t.Fatal("propose returned id=0")
	}
	t.Logf("Proposed work document #%d", docID)

	// Verify the .allowed file was written on the server.
	allowedPath := filepath.Join(workPath, fmt.Sprintf("%d.allowed", docID))
	if _, err := os.Stat(allowedPath); err != nil {
		t.Fatalf("server .allowed %s missing: %v", allowedPath, err)
	}

	// 2. List (scope=all) — the proposed document should appear.
	listData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "list",
		"scope":              "all",
	}
	packed, err = msgpack.Pack(listData)
	if err != nil {
		t.Fatalf("pack list: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || respBytes[0] != resOK {
		t.Fatalf("list response code = %x, want resOK", firstByte(respBytes))
	}
	listUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack list: %v", err)
	}
	listMap, ok := listUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("list response is %T, want map", listUnpacked)
	}
	proposed, _ := listMap["proposed"].([]any)
	if len(proposed) != 1 {
		t.Fatalf("list proposed len=%d, want 1", len(proposed))
	}
	first, _ := proposed[0].(map[any]any)
	if first["id"] != docID {
		t.Errorf("list proposed id=%v, want %d", first["id"], docID)
	}
	if first["title"] != "Proposal" {
		t.Errorf("list proposed title=%v, want Proposal", first["title"])
	}
	t.Logf("List returned proposed doc #%v", first["id"])

	// 3. View — assert the signature is present and valid.
	viewData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "view",
		"doc_id":             docID,
		"scope":              "all",
	}
	packed, err = msgpack.Pack(viewData)
	if err != nil {
		t.Fatalf("pack view: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("view request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || respBytes[0] != resOK {
		t.Fatalf("view response code = %x, want resOK", firstByte(respBytes))
	}
	viewUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack view: %v", err)
	}
	viewDoc, ok := viewUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("view response is %T, want map", viewUnpacked)
	}
	if viewDoc["content"] != content {
		t.Errorf("view content=%v, want %q", viewDoc["content"], content)
	}
	viewMeta, _ := viewDoc["meta"].(map[any]any)
	sig, _ := viewMeta["signature"].([]byte)
	if len(sig) != signatureLength {
		t.Fatalf("view signature len=%d, want %d", len(sig), signatureLength)
	}
	pub, _ := viewMeta["identity"].([]byte)
	if len(pub) != rns.IdentityKeySize/8 {
		t.Fatalf("view identity len=%d, want %d", len(pub), rns.IdentityKeySize/8)
	}
	// Validate the signature locally (mirrors work_view client logic).
	verifyID, err := rns.NewIdentity(false, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if err := verifyID.LoadPublicKey(pub); err != nil {
		t.Fatalf("LoadPublicKey: %v", err)
	}
	if !verifyID.Verify(sig, []byte(viewDoc["content"].(string))) {
		t.Fatal("work document signature is NOT valid")
	}
	t.Logf("Work document signature validated, signed by %x", verifyID.Hash)

	// 4. Comment (update).
	commentBody := "An update from the client."
	commentData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "comment",
		"doc_id":             docID,
		"scope":              "proposed",
		"content":            commentBody,
		"format":             "markdown",
	}
	packed, err = msgpack.Pack(commentData)
	if err != nil {
		t.Fatalf("pack comment: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, workRequestTimeout)
	if err != nil {
		t.Fatalf("comment request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("comment response code = %x, want resOK", firstByte(respBytes))
	}
	commentUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack comment: %v", err)
	}
	commentMap, ok := commentUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("comment response is %T, want map", commentUnpacked)
	}
	commentID, _ := commentMap["id"].(int64)
	if commentID != 1 {
		t.Errorf("comment id=%d, want 1", commentID)
	}
	t.Logf("Comment #%d added", commentID)

	// 5. Edit — author-only (client identity is the author).
	newContent := "Edited proposal body."
	newSig, err := client.identity.Sign([]byte(newContent))
	if err != nil {
		t.Fatalf("sign edit: %v", err)
	}
	editData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "edit",
		"doc_id":             docID,
		"scope":              "proposed",
		"content":            newContent,
		"title":              "Proposal",
		"signature":          newSig,
	}
	packed, err = msgpack.Pack(editData)
	if err != nil {
		t.Fatalf("pack edit: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, workRequestTimeout)
	if err != nil {
		t.Fatalf("edit request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("edit response code = %x, want resOK", firstByte(respBytes))
	}
	t.Logf("Edit applied")

	// 6. Complete — author-only. Since the doc is in proposed/, complete
	// only moves active→completed, so first we activate it to active, then
	// complete it. But activate moves completed→active, so we must move the
	// proposed doc to active manually... Actually the Python _work_complete
	// only handles active→completed. For a proposed doc, complete returns
	// "Document not found" (active dir missing). So instead, re-create a doc
	// in active scope and exercise complete/activate on that.
	activeContent := "Active work document body."
	activeSig, err := client.identity.Sign([]byte(activeContent))
	if err != nil {
		t.Fatalf("sign active: %v", err)
	}
	createData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "create",
		"title":              "Active",
		"content":            activeContent,
		"format":             "markdown",
		"signature":          activeSig,
	}
	packed, err = msgpack.Pack(createData)
	if err != nil {
		t.Fatalf("pack create: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("create response code = %x, want resOK", firstByte(respBytes))
	}
	createUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack create: %v", err)
	}
	createMap, ok := createUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("create response is %T, want map", createUnpacked)
	}
	activeDocID, _ := createMap["id"].(int64)
	if activeDocID == 0 {
		t.Fatal("create returned id=0")
	}
	if createMap["scope"] != "active" {
		t.Errorf("create scope=%v, want active", createMap["scope"])
	}
	t.Logf("Created active work document #%d", activeDocID)

	// 7. Complete the active doc → completed.
	completeData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "complete",
		"doc_id":             activeDocID,
	}
	packed, err = msgpack.Pack(completeData)
	if err != nil {
		t.Fatalf("pack complete: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("complete request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("complete response code = %x, want resOK", firstByte(respBytes))
	}
	completeUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack complete: %v", err)
	}
	completeMap, _ := completeUnpacked.(map[any]any)
	if completeMap["scope"] != "completed" {
		t.Errorf("complete scope=%v, want completed", completeMap["scope"])
	}
	// Assert on-disk state: active/<id> gone, completed/<id> present.
	if isDir(filepath.Join(workPath, "active", fmt.Sprintf("%d", activeDocID))) {
		t.Errorf("active/%d still exists after complete", activeDocID)
	}
	if !isDir(filepath.Join(workPath, "completed", fmt.Sprintf("%d", activeDocID))) {
		t.Errorf("completed/%d missing after complete", activeDocID)
	}
	t.Logf("Completed work document #%d", activeDocID)

	// 8. Activate the completed doc → active.
	activateData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "activate",
		"doc_id":             activeDocID,
	}
	packed, err = msgpack.Pack(activateData)
	if err != nil {
		t.Fatalf("pack activate: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("activate request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("activate response code = %x, want resOK", firstByte(respBytes))
	}
	activateUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack activate: %v", err)
	}
	activateMap, _ := activateUnpacked.(map[any]any)
	if activateMap["scope"] != "active" {
		t.Errorf("activate scope=%v, want active", activateMap["scope"])
	}
	if !isDir(filepath.Join(workPath, "active", fmt.Sprintf("%d", activeDocID))) {
		t.Errorf("active/%d missing after activate", activeDocID)
	}
	t.Logf("Activated work document #%d", activeDocID)

	// 9. Delete the proposed doc.
	deleteData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "delete",
		"doc_id":             docID,
		"scope":              "proposed",
	}
	packed, err = msgpack.Pack(deleteData)
	if err != nil {
		t.Fatalf("pack delete: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("delete response code = %x, want resOK", firstByte(respBytes))
	}
	proposedDir := filepath.Join(workPath, "proposed", fmt.Sprintf("%d", docID))
	if isDir(proposedDir) {
		t.Errorf("proposed/%d still exists after delete", docID)
	}
	if _, err := os.Stat(allowedPath); !os.IsNotExist(err) {
		t.Errorf(".allowed %s still exists after delete", allowedPath)
	}
	t.Logf("Deleted work document #%d", docID)

	// 10. Perms get on the active doc (created via create, no .allowed file
	// so content is empty).
	permsGetData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "perms",
		"doc_id":             activeDocID,
		"step":               "get",
	}
	packed, err = msgpack.Pack(permsGetData)
	if err != nil {
		t.Fatalf("pack perms get: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("perms get request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("perms get response code = %x, want resOK", firstByte(respBytes))
	}
	t.Logf("Perms get succeeded")

	// 11. Perms set with valid content.
	permsSetData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "perms",
		"doc_id":             activeDocID,
		"step":               "set",
		"content":            "# comment\nr:all\nw:none\n",
	}
	packed, err = msgpack.Pack(permsSetData)
	if err != nil {
		t.Fatalf("pack perms set: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("perms set request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("perms set response code = %x, want resOK", firstByte(respBytes))
	}
	allowedActive := filepath.Join(workPath, fmt.Sprintf("%d.allowed", activeDocID))
	got, err := os.ReadFile(allowedActive)
	if err != nil {
		t.Fatalf("read .allowed: %v", err)
	}
	if string(got) != "# comment\nr:all\nw:none\n" {
		t.Errorf(".allowed=%q, want the submitted content", string(got))
	}
	t.Logf("Perms set succeeded")

	// 12. Perms set with an invalid line is rejected.
	permsBadData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "perms",
		"doc_id":             activeDocID,
		"step":               "set",
		"content":            "r:all\nbadperm\n",
	}
	packed, err = msgpack.Pack(permsBadData)
	if err != nil {
		t.Fatalf("pack perms bad: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("perms bad request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resInvalidReq {
		t.Fatalf("perms bad response code = %x, want resInvalidReq", firstByte(respBytes))
	}
	wantMsg := "Invalid permission \"badperm\" on line 2"
	if string(respBytes[1:]) != wantMsg {
		t.Errorf("perms bad msg=%q, want %q", string(respBytes[1:]), wantMsg)
	}
	t.Logf("Perms invalid line rejected as expected")

	// 13. Delete the active doc to clean up.
	deleteActiveData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "delete",
		"doc_id":             activeDocID,
		"scope":              "active",
	}
	packed, err = msgpack.Pack(deleteActiveData)
	if err != nil {
		t.Fatalf("pack delete active: %v", err)
	}
	resp, _, err = client.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		t.Fatalf("delete active request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("delete active response code = %x, want resOK", firstByte(respBytes))
	}
	t.Logf("Deleted active work document #%d", activeDocID)

	// Allow the server a moment to flush. The test is done; the deferred
	// node cleanup will terminate the subprocess.
	_ = time.Millisecond
}
