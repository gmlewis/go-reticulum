// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// work_client.go implements the gorngit client work-document operations,
// mirroring the work_list/work_view/work_create/work_propose/work_edit/
// work_delete/work_comment/work_complete/work_activate/work_permissions
// methods of ReticulumGitClient (RNS/Utilities/rngit/server.py, rngit v1.4.2).

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// commentTemplate is the editor template for a work comment (update),
// matching COMMENT_TEMPLATE (server.py). The trailing "abort abort" is a
// verbatim typo preserved for parity.
const commentTemplate = "# Remove this line and enter your update. Save and exit when done, or save an empty document to abort abort."

// createDocTemplate is the editor template for a new work document,
// matching CREATE_DOC_TEMPLATE (server.py). The trailing "abort abort" is
// a verbatim typo preserved for parity.
const createDocTemplate = "# Remove this line and enter your document content. Save and exit when done, or save an empty document to abort abort."

// permissionsTemplate is the editor template for the permissions editor,
// matching PERMISSIONS_TEMPLATE (server.py).
const permissionsTemplate = "# No permissions are currently defined for this entity. Add them below, and save and exit when you are done."

// workRequestTimeout is the timeout for work requests that may involve
// editor interaction on the client side, mirroring the Python 600s timeout
// for create/propose/edit/comment.
const workRequestTimeout = 600 * time.Second

// workList sends a work "list" request and prints the document table,
// mirroring work_list (server.py).
func (c *reticulumGitClient) workList(scope string) error {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "list",
		"scope":              scope,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack list request: %w", err)
	}
	response, _, err := c.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	result := map[any]any{"active": []any{}, "completed": []any{}, "proposed": []any{}}
	if len(respBytes) > 1 {
		unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
		if err != nil {
			return fmt.Errorf("could not unpack list: %w", err)
		}
		if m, ok := unpacked.(map[any]any); ok {
			result = m
		}
	}

	scopesToShow := []string{"active", "completed", "proposed"}
	if scope != "all" {
		scopesToShow = []string{scope}
	}
	for _, s := range scopesToShow {
		docs, _ := result[s].([]any)
		if len(docs) > 0 {
			hdr := fmt.Sprintf("\n%s documents", capitalize(s))
			fmt.Println(hdr)
			fmt.Println(strings.Repeat("=", len(hdr)))
			fmt.Println()
			fmt.Printf("%-4s %-30s %-17s %-18s %s\n", "ID", "Title", "Author", "Created", "Comments")
			fmt.Println(strings.Repeat("-", 80))
			for _, doc := range docs {
				dm, _ := doc.(map[any]any)
				docID := dm["id"]
				title, _ := dm["title"].(string)
				if len(title) > 29 {
					title = title[:29] + "…"
				}
				author, _ := dm["author"].(string)
				if len(author) > 16 {
					author = author[:16] + "…"
				}
				createdTs, _ := dm["created"].(float64)
				created := "unknown"
				if createdTs > 0 {
					created = time.Unix(int64(createdTs), 0).Format("2006-01-02 15:04")
				}
				comments, _ := dm["comments"].(int64)
				fmt.Printf("%-4v %-30s %-17s %-18s %d\n", docID, title, author, created, comments)
			}
			fmt.Println()
		} else if scope != "all" {
			fmt.Printf("No %s work documents found.\n", s)
		}
	}
	if scope == "all" {
		active, _ := result["active"].([]any)
		completed, _ := result["completed"].([]any)
		proposed, _ := result["proposed"].([]any)
		if len(active) == 0 && len(completed) == 0 && len(proposed) == 0 {
			fmt.Println("No work documents found.")
		}
	}
	return nil
}

// workView sends a work "view" request and prints the document detail,
// validating the signature locally, mirroring work_view (server.py).
func (c *reticulumGitClient) workView(docID int, scope string) error {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "view",
		"doc_id":             int64(docID),
		"scope":              scope,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack view request: %w", err)
	}
	response, _, err := c.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	if len(respBytes) <= 1 {
		return errors.New("Empty response from remote")
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		return fmt.Errorf("could not unpack view: %w", err)
	}
	doc, ok := unpacked.(map[any]any)
	if !ok {
		return errors.New("Invalid document data from remote")
	}
	meta, _ := doc["meta"].(map[any]any)
	authorStr := fmt.Sprintf("%v (not locally validated)", meta["author"])
	signatureStr := "Document not signed"
	signature, _ := meta["signature"].([]byte)
	pubkey, _ := meta["identity"].([]byte)
	content, _ := doc["content"].(string)
	if len(signature) == signatureLength && len(pubkey) == rns.IdentityKeySize/8 {
		signatureStr = "Not valid"
		identity, err := rns.NewIdentity(false, nil)
		if err == nil {
			if err := identity.LoadPublicKey(pubkey); err == nil {
				if identity.Verify(signature, []byte(content)) {
					signatureStr = "Valid"
					authorStr = rns.PrettyHex(identity.Hash)
				}
			}
		}
	}

	docScope, _ := doc["scope"].(string)
	title := metaString(meta, "title", "Untitled")
	dt := fmt.Sprintf("%s (#%v)", title, doc["id"])
	fmt.Println(dt)
	fmt.Println(strings.Repeat("=", len(dt)))
	fmt.Printf("Author    : %s\n", authorStr)
	fmt.Printf("Signature : %s\n", signatureStr)
	fmt.Printf("Status    : %s\n", capitalize(docScope))
	created, _ := meta["created"].(float64)
	if created > 0 {
		fmt.Printf("Created   : %s\n", time.Unix(int64(created), 0).Format("2006-01-02 15:04:05"))
	}
	edited, _ := meta["edited"].(float64)
	if edited > 0 {
		fmt.Printf("Edited    : %s\n", time.Unix(int64(edited), 0).Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("Format    : %s\n", metaString(meta, "format", "markdown"))
	comments, _ := doc["comments"].([]any)
	fmt.Printf("Updates   : %d\n", len(comments))
	fmt.Println()
	fmt.Println(content)

	if len(comments) > 0 {
		fmt.Println("\nUpdates")
		fmt.Println("=======")
		for _, cm := range comments {
			c, _ := cm.(map[any]any)
			cCreated, _ := c["created"].(float64)
			ts := fmt.Sprintf("#%v by %v at %s", c["id"], c["author"], time.Unix(int64(cCreated), 0).Format("2006-01-02 15:04:05"))
			fmt.Printf("\n%s\n", ts)
			fmt.Println(strings.Repeat("-", len(ts)))
			fmt.Println(c["content"])
		}
	}
	fmt.Println()
	return nil
}

// workCreate sends a work "create" request after opening the editor,
// mirroring work_create (server.py).
func (c *reticulumGitClient) workCreate(title string) error {
	return c.workCreateInScope(title, "create", "active")
}

// workPropose sends a work "propose" request after opening the editor,
// mirroring work_propose (server.py).
func (c *reticulumGitClient) workPropose(title string) error {
	return c.workCreateInScope(title, "propose", "proposed")
}

// workCreateInScope is the shared create/propose client flow, mirroring
// work_create / work_propose (server.py). opName is "create" or "propose";
// scopeResult is the expected scope in the server response.
func (c *reticulumGitClient) workCreateInScope(title, opName, scopeResult string) error {
	content, err := editWorkContent(title, "", false)
	if err != nil {
		return err
	}
	if content == "" {
		fmt.Println("Creation cancelled")
		return nil
	}
	signature, err := c.identity.Sign([]byte(content))
	if err != nil {
		return errors.New("Could not sign work document")
	}
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          opName,
		"title":              title,
		"content":            content,
		"format":             "markdown",
		"signature":          signature,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack %s request: %w", opName, err)
	}
	response, _, err := c.sendRequest(pathWork, packed, workRequestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Server error: %s", string(respBytes[1:]))
	}
	if len(respBytes) > 1 {
		unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
		if err == nil {
			if m, ok := unpacked.(map[any]any); ok {
				fmt.Printf("Work document created as %s #%v\n", m["scope"], m["id"])
				return nil
			}
		}
	}
	fmt.Println("Work document created")
	return nil
}

// workEdit sends a work "edit" request, first viewing the current document
// to fetch its content, then opening the editor, mirroring work_edit
// (server.py).
func (c *reticulumGitClient) workEdit(docID int, title, scope string) error {
	// First view to fetch current content.
	viewData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "view",
		"doc_id":             int64(docID),
		"scope":              scope,
	}
	packed, err := msgpack.Pack(viewData)
	if err != nil {
		return fmt.Errorf("could not pack view request: %w", err)
	}
	response, _, err := c.sendRequest(pathWork, packed, workRequestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		return fmt.Errorf("could not unpack view: %w", err)
	}
	doc, ok := unpacked.(map[any]any)
	if !ok {
		return errors.New("Invalid document data from remote")
	}
	currentContent, _ := doc["content"].(string)
	currentTitle := metaString(mapVal(doc, "meta").(map[any]any), "title", "Untitled")

	content, err := editWorkContent(currentTitle, currentContent, false)
	if err != nil {
		return err
	}
	if content == "" {
		fmt.Println("Edit cancelled")
		return nil
	}
	signature, err := c.identity.Sign([]byte(content))
	if err != nil {
		return errors.New("Could not sign work document")
	}
	if title == "" {
		title = currentTitle
	}
	editData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "edit",
		"doc_id":             int64(docID),
		"scope":              scope,
		"content":            content,
		"title":              title,
		"signature":          signature,
	}
	packed, err = msgpack.Pack(editData)
	if err != nil {
		return fmt.Errorf("could not pack edit request: %w", err)
	}
	response, _, err = c.sendRequest(pathWork, packed, workRequestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok = response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	fmt.Printf("Work document %s #%d updated\n", scope, docID)
	return nil
}

// workDelete sends a work "delete" request after confirming, mirroring
// work_delete (server.py).
func (c *reticulumGitClient) workDelete(docID int, scope string) error {
	fmt.Printf("Are you sure you want to delete %s work document #%d? [y/N]: ", scope, docID)
	var confirm string
	_, _ = fmt.Scanln(&confirm)
	if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
		fmt.Println("Deletion cancelled")
		return nil
	}
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "delete",
		"doc_id":             int64(docID),
		"scope":              scope,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack delete request: %w", err)
	}
	response, _, err := c.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	fmt.Printf("Work document %s #%d deleted\n", scope, docID)
	return nil
}

// workComment sends a work "comment" (update) request after opening the
// editor, mirroring work_comment (server.py). No signature is sent.
func (c *reticulumGitClient) workComment(docID int, scope string) error {
	content, err := editWorkContent(fmt.Sprintf("Update on document #%d", docID), "", true)
	if err != nil {
		return err
	}
	if content == "" {
		fmt.Println("Update cancelled")
		return nil
	}
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "comment",
		"doc_id":             int64(docID),
		"scope":              scope,
		"content":            content,
		"format":             "markdown",
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack comment request: %w", err)
	}
	response, _, err := c.sendRequest(pathWork, packed, workRequestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	if len(respBytes) > 1 {
		unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
		if err == nil {
			if m, ok := unpacked.(map[any]any); ok {
				fmt.Printf("Update #%v added to %s document #%d\n", m["id"], scope, docID)
				return nil
			}
		}
	}
	fmt.Println("Update added")
	return nil
}

// workComplete sends a work "complete" request, mirroring work_complete
// (server.py).
func (c *reticulumGitClient) workComplete(docID int) error {
	return c.workMove(docID, "complete", "completed", "completed")
}

// workActivate sends a work "activate" request, mirroring work_activate
// (server.py).
func (c *reticulumGitClient) workActivate(docID int) error {
	return c.workMove(docID, "activate", "active", "activated")
}

// workMove is the shared complete/activate client flow, mirroring
// work_complete / work_activate (server.py).
func (c *reticulumGitClient) workMove(docID int, opName, scopeResult, doneVerb string) error {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          opName,
		"doc_id":             int64(docID),
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack %s request: %w", opName, err)
	}
	response, _, err := c.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	if len(respBytes) > 1 {
		unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
		if err == nil {
			if m, ok := unpacked.(map[any]any); ok {
				fmt.Printf("Work document #%v %s\n", m["id"], doneVerb)
				return nil
			}
		}
	}
	fmt.Printf("Work document %s\n", doneVerb)
	return nil
}

// workPermissions sends a work "perms" get+set request pair, mirroring
// work_permissions (server.py). It fetches the current .allowed content,
// opens the editor, and submits the edited content.
func (c *reticulumGitClient) workPermissions(docID int) error {
	getData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "perms",
		"doc_id":             int64(docID),
		"step":               "get",
	}
	packed, err := msgpack.Pack(getData)
	if err != nil {
		return fmt.Errorf("could not pack perms get: %w", err)
	}
	response, _, err := c.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	currentContent := ""
	if len(respBytes) > 1 {
		unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
		if err == nil {
			if m, ok := unpacked.(map[any]any); ok {
				currentContent, _ = m["content"].(string)
			}
		}
	}
	content, err := editPermissions(currentContent)
	if err != nil {
		return err
	}
	if content == "" {
		fmt.Println("Edit cancelled")
		return nil
	}
	setData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "perms",
		"doc_id":             int64(docID),
		"step":               "set",
		"content":            content,
	}
	packed, err = msgpack.Pack(setData)
	if err != nil {
		return fmt.Errorf("could not pack perms set: %w", err)
	}
	response, _, err = c.sendRequest(pathWork, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok = response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	fmt.Printf("Permissions updated for work document #%d\n", docID)
	return nil
}

// editWorkContent opens the editor on a temp file with the work template (or
// the existing content when non-empty), strips comment/create-doc template
// lines, and returns the stripped content. Returns the empty string when
// cancelled, mirroring _edit_work_content (server.py).
func editWorkContent(title, content string, isComment bool) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		for _, fallback := range []string{"nano", "vim", "vi"} {
			if _, err := exec.LookPath(fallback); err == nil {
				editor = fallback
				break
			}
		}
	}
	if editor == "" {
		fmt.Println("No editor found. Please set $EDITOR environment variable.")
		return "", nil
	}

	template := createDocTemplate
	if isComment {
		template = commentTemplate
	}
	if content != "" {
		template = content
	}

	tmp, err := os.CreateTemp("", "*.md")
	if err != nil {
		return "", fmt.Errorf("could not create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(template); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("could not write template: %w", err)
	}
	_ = tmp.Close()

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	edited, err := os.ReadFile(tmpPath)
	_ = os.Remove(tmpPath)
	if err != nil {
		return "", fmt.Errorf("could not read edited content: %w", err)
	}
	return stripWorkTemplateLines(string(edited)), nil
}

// stripWorkTemplateLines removes lines whose stripped form starts with the
// comment or create-doc template string, mirroring the list comprehension in
// _edit_work_content (server.py). The result is stripped of leading/trailing
// whitespace and returned empty when nothing remains.
func stripWorkTemplateLines(edited string) string {
	var lines []string
	for _, line := range strings.Split(edited, "\n") {
		stripped := strings.TrimSpace(line)
		if strings.HasPrefix(stripped, commentTemplate) || strings.HasPrefix(stripped, createDocTemplate) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// editPermissions opens the editor on a temp file with the permissions
// template (or the existing content when non-empty), mirroring
// _edit_permissions (server.py). Returns the edited content verbatim
// (template lines are NOT stripped) and the empty string when cancelled.
func editPermissions(content string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		for _, fallback := range []string{"nano", "vim", "vi"} {
			if _, err := exec.LookPath(fallback); err == nil {
				editor = fallback
				break
			}
		}
	}
	if editor == "" {
		fmt.Println("No editor found. Please set $EDITOR environment variable.")
		return "", nil
	}

	template := permissionsTemplate
	if content != "" {
		template = content
	}

	tmp, err := os.CreateTemp("", "*.txt")
	if err != nil {
		return "", fmt.Errorf("could not create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(template); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("could not write template: %w", err)
	}
	_ = tmp.Close()

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	edited, err := os.ReadFile(tmpPath)
	_ = os.Remove(tmpPath)
	if err != nil {
		return "", fmt.Errorf("could not read edited content: %w", err)
	}
	return string(edited), nil
}

// capitalize returns s with its first letter uppercased, mirroring
// Python's str.capitalize for the leading ASCII letter.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}
