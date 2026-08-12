// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// release_client.go implements the gorngit client release operations,
// mirroring the list_releases/view_release/fetch_release/create_release/
// delete_release/latest_release methods of ReticulumGitClient
// (RNS/Utilities/rngit/server.py, rngit v1.4.2).

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/rsg"
)

// releaseNotesTemplate is the editor template for release notes,
// matching RELEASE_NOTES_TEMPLATE (server.py).
const releaseNotesTemplate = `# Enter release notes for {TAG}.
# Lines starting with '#' will be ignored.
# Save and exit the editor when done, or exit without saving to abort.
`

// listReleases sends a release "list" request and prints the release
// table, mirroring list_releases (server.py).
func (c *reticulumGitClient) listReleases() error {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "list",
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack list request: %w", err)
	}
	response, _, err := c.sendRequest(pathRelease, packed, requestTimeout)
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
	var unpacked any
	if len(respBytes) > 1 {
		unpacked, err = msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
		if err != nil {
			return fmt.Errorf("could not unpack release list: %w", err)
		}
	} else {
		unpacked = []any{}
	}

	var releases []any
	var latestRelease string
	switch v := unpacked.(type) {
	case []any:
		releases = v
	case map[any]any:
		releases, _ = v["releases"].([]any)
		latestRelease, _ = v["latest"].(string)
	default:
		return errors.New("Invalid release data format from remote")
	}

	if len(releases) == 0 {
		fmt.Println("No releases for this repository")
		return nil
	}
	fmt.Printf("%-10s %-10s %-17s %-5s Notes\n", "Tag", "Status", "Created", "Objs")
	fmt.Println(strings.Repeat("-", 80))
	for _, rel := range releases {
		m, _ := rel.(map[any]any)
		tag, _ := m["tag"].(string)
		if len(tag) > 10 {
			tag = tag[:10]
		}
		status, _ := m["status"].(string)
		if len(status) > 9 {
			status = status[:9]
		}
		createdTs, _ := m["created"].(int64)
		created := "unknown"
		if createdTs > 0 {
			created = time.Unix(createdTs, 0).Format("2006-01-02 15:04")
		}
		artifacts := fmt.Sprintf("%v", m["artifacts"])
		preview, _ := m["preview"].(string)
		previewLine := ""
		if lines := strings.Split(preview, "\n"); len(lines) > 0 {
			previewLine = lines[0]
		}
		if len(previewLine) > 34 {
			previewLine = previewLine[:34]
		}
		fmt.Printf("%-10s %-10s %-17s %-5s %s\n", tag, status, created, artifacts, previewLine)
	}
	if latestRelease != "" {
		fmt.Printf("\nThe latest release is: %s\n", latestRelease)
	}
	return nil
}

// viewRelease sends a release "view" request and prints the release
// detail, mirroring view_release (server.py).
func (c *reticulumGitClient) viewRelease(target string) error {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "view",
		"tag":                target,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack view request: %w", err)
	}
	response, _, err := c.sendRequest(pathRelease, packed, 300*time.Second)
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
		return fmt.Errorf("could not unpack release: %w", err)
	}
	release, ok := unpacked.(map[any]any)
	if !ok {
		return errors.New("Invalid release data from remote")
	}
	tag, _ := release["tag"].(string)
	if tag == "" {
		tag = target
	}
	fmt.Printf("Release : %s\n", tag)
	status, _ := release["status"].(string)
	if status == "" {
		status = "unknown"
	}
	fmt.Printf("Status  : %s\n", status)
	createdTs, _ := release["created"].(int64)
	if createdTs > 0 {
		fmt.Printf("Created : %s\n", time.Unix(createdTs, 0).Format("2006-01-02 15:04:05"))
	}
	thanks, _ := release["thanks"].(int64)
	fmt.Printf("Thanks  : %d\n", thanks)
	notes, _ := release["notes"].(string)
	if notes != "" {
		fmt.Println("\nRelease Notes")
		fmt.Println("=============")
		fmt.Println()
		fmt.Print(notes)
	}
	artifacts, _ := release["artifacts"].([]any)
	if len(artifacts) > 0 {
		hdr := fmt.Sprintf("Artifacts (%d)", len(artifacts))
		fmt.Printf("\n%s\n", hdr)
		fmt.Println(strings.Repeat("=", len(hdr)))
		for _, a := range artifacts {
			am, _ := a.(map[any]any)
			name, _ := am["name"].(string)
			if name == "" {
				name = "unknown"
			}
			size, _ := am["size"].(int64)
			fmt.Printf(" - %s (%s)\n", name, prettySize(size))
		}
	}
	fmt.Println()
	return nil
}

// fetchRelease sends a release "fetch" request for a tag:artifact spec
// and validates the downloaded manifest + artifacts, mirroring
// fetch_release (server.py). When offline is true it validates a local
// manifest without contacting the server. signerHex is an optional
// required signer identity hash (hex).
func (c *reticulumGitClient) fetchRelease(target, signerHex string, offline bool) error {
	if offline && target == "" {
		target = "latest:all"
	}
	if target == "" {
		return errors.New("No target specified")
	}
	var signerHash []byte
	if signerHex != "" {
		var err error
		signerHash, err = hex.DecodeString(signerHex)
		if err != nil {
			return fmt.Errorf("Invalid required signer identity hash: %w", err)
		}
		if len(signerHash) != rns.TruncatedHashLength/8 {
			return errors.New("Invalid required signer identity hash length")
		}
	}

	// Offline mode requires no link. Online mode requires a connection.
	if !offline {
		if !c.linkReady {
			return errors.New("link not ready")
		}
	}

	parts := strings.SplitN(target, ":", 2)
	if len(parts) < 2 {
		return errors.New("Invalid release specification")
	}
	tag := parts[0]
	artifactSpec := parts[1]

	// Fetch the manifest.
	manifestPath, err := c.fetchArtifact(tag, "manifest.rsm", offline)
	if err != nil {
		return err
	}
	rsgData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("could not read manifest: %w", err)
	}
	signedData, err := rsg.ExtractSignedData(rsgData)
	if err != nil {
		return errors.New("No embedded message in release manifest")
	}
	message, _ := signedData["message"].([]byte)
	if message == nil {
		return errors.New("No embedded message in release manifest")
	}
	signingID, err := rsg.Validate(rsgData, message, signerHash)
	if err != nil {
		if signerHash != nil {
			return fmt.Errorf("Release manifest not signed by %x, aborting", signerHash)
		}
		return errors.New("Could not validate release manifest signature")
	}
	fmt.Printf("Release manifest validated, signed by %x\n", signingID.Hash)
	if err := rsg.CheckReleaseRSMStructure(signedData); err != nil {
		return err
	}
	meta, _ := signedData["meta"].(map[any]any)
	if meta == nil {
		return errors.New("No release metadata in manifest")
	}
	releaseName, _ := meta["name"].(string)
	releaseVersion, _ := meta["version"].(string)
	manifestOut := fmt.Sprintf("%s_%s.%s", releaseName, releaseVersion, msgExt)
	if err := os.WriteFile(manifestOut, rsgData, 0o644); err != nil {
		return fmt.Errorf("could not write manifest: %w", err)
	}

	artifacts, _ := meta["artifacts"].([]any)
	if len(artifacts) == 0 {
		return errors.New("Release manifest contains no artifacts")
	}
	var fetchArtifacts []map[any]any
	if artifactSpec == "all" {
		for _, a := range artifacts {
			if am, ok := a.(map[any]any); ok {
				fetchArtifacts = append(fetchArtifacts, am)
			}
		}
	} else {
		for _, a := range artifacts {
			am, ok := a.(map[any]any)
			if !ok {
				continue
			}
			name, _ := am["name"].(string)
			if matchSimple(name, artifactSpec) {
				fetchArtifacts = append(fetchArtifacts, am)
			}
		}
	}
	if len(fetchArtifacts) == 0 {
		return errors.New("No available artifacts specified for fetch")
	}
	op := "Fetching"
	if offline {
		op = "Validating"
	}
	ms := ""
	if len(fetchArtifacts) != 1 {
		ms = "s"
	}
	fmt.Printf("%s %d artifact%s...\n", op, len(fetchArtifacts), ms)

	validCount := 0
	for _, artifact := range fetchArtifacts {
		name, _ := artifact["name"].(string)
		name = filepath.Base(name)
		rsgBytes, _ := artifact["rsg"].([]byte)
		artifactPath, err := c.fetchArtifact(tag, name, offline)
		if err != nil {
			if offline {
				fmt.Printf("  File %s from manifest does not exist locally, cannot validate\n", name)
				continue
			}
			return err
		}
		fileBytes, err := os.ReadFile(artifactPath)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", name, err)
		}
		if _, err := rsg.Validate(rsgBytes, fileBytes, signerHash); err != nil {
			if offline {
				fmt.Printf("  File %s does not match manifest\n", name)
				continue
			}
			return fmt.Errorf("Fetched file %s does not match manifest, aborting", name)
		}
		validCount++
		if !offline {
			if err := os.Rename(artifactPath, name); err != nil {
				return fmt.Errorf("could not save %s: %w", name, err)
			}
		} else {
			fmt.Printf("  File %s validated against manifest\n", name)
		}
	}

	if offline {
		if validCount == len(fetchArtifacts) {
			fmt.Println("\nAll files validated")
			return errOfflineOK
		}
		fmt.Println("\nRelease is not valid")
		return errors.New("Release is not valid")
	}
	return nil
}

// errOfflineOK is a sentinel returned by fetchRelease in offline mode
// when all files validate successfully; the caller maps it to exit code 0.
var errOfflineOK = errors.New("offline validation succeeded")

// fetchArtifact fetches a single artifact by name. In offline mode it
// returns the local path without contacting the server.
func (c *reticulumGitClient) fetchArtifact(tag, name string, offline bool) (string, error) {
	if offline {
		// In offline mode the manifest directory is not tracked here;
		// return the name relative to the cwd.
		return name, nil
	}
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "fetch",
		"tag":                tag,
		"artifact":           name,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return "", fmt.Errorf("could not pack fetch request: %w", err)
	}
	response, _, err := c.sendRequest(pathRelease, packed, fetchPushTimeout)
	if err != nil {
		return "", err
	}
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return "", errors.New("No response from remote")
	}
	code := respBytes[0]
	if code != resOK {
		msg := string(respBytes[1:])
		if msg == "" {
			msg = "Unknown error"
		}
		return "", fmt.Errorf("Remote error: %s", msg)
	}
	tmpDir, err := os.MkdirTemp("", "gorngit-fetch-art-")
	if err != nil {
		return "", fmt.Errorf("could not create temp dir: %w", err)
	}
	artifactPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(artifactPath, respBytes[1:], 0o644); err != nil {
		return "", fmt.Errorf("could not write artifact: %w", err)
	}
	return artifactPath, nil
}

// createRelease signs artifacts, builds the RSM manifest, and uploads it
// to the remote node via the 3-step init/artifact/finalize protocol,
// mirroring create_release (server.py).
func (c *reticulumGitClient) createRelease(target string, signer *rns.Identity, name string, noUpload bool) error {
	if target == "" {
		return errors.New("No target specified")
	}
	if signer == nil {
		return errors.New("No signer identity available")
	}
	releaseTime := time.Now().Unix()
	releaseTimeISO := time.Unix(releaseTime, 0).UTC().Format("2006-01-02T15:04:05Z")

	parts := strings.SplitN(target, ":", 2)
	if len(parts) < 2 {
		return errors.New("Invalid release specification\nDid you provide both a tag and artifacts path such as \"1.0.0:./dist\"?")
	}
	tag := parts[0]
	artifactsPath, err := filepath.Abs(expandUser(parts[1]))
	if err != nil {
		return fmt.Errorf("could not resolve artifacts path: %w", err)
	}
	commitHash := commitHashFromTag(tag, ".")
	if commitHash == "" {
		fmt.Printf("Could not get commit hash for tag %s. Does the tag exist in the local repository?\n", tag)
	}
	if !isDir(artifactsPath) {
		return errors.New("Specified artifacts directory does not exist")
	}
	entries, err := os.ReadDir(artifactsPath)
	if err != nil {
		return fmt.Errorf("could not list artifacts: %w", err)
	}
	var artifacts []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		artifacts = append(artifacts, entry.Name())
	}
	if len(artifacts) == 0 {
		return errors.New("No files found in specified artifact directory")
	}

	fmt.Printf("Creating release %s\n", tag)
	notes, err := editReleaseNotes(tag)
	if err != nil {
		return err
	}
	if notes == "" {
		fmt.Println("Release creation cancelled")
		return nil
	}

	packageName := name
	if packageName == "" {
		packageName = c.repoName
	}

	// manifest_meta keys IN THIS ORDER (Python dict insertion order):
	// name, version, released, timestamp, origin, path, commit, artifacts.
	manifestMeta := msgpack.OrderedMap{
		{Key: "name", Value: packageName},
		{Key: "version", Value: tag},
		{Key: "released", Value: releaseTimeISO},
		{Key: "timestamp", Value: uint64(releaseTime)},
		{Key: "origin", Value: c.destinationHash},
		{Key: "path", Value: c.repoPath},
		{Key: "commit", Value: commitHash},
	}

	manifestPath := filepath.Join(artifactsPath, "manifest."+msgExt)
	var rsgs []string
	var artifactEntries []msgpack.OrderedMap
	for _, artifact := range artifacts {
		if strings.HasSuffix(artifact, "."+sigExt) || strings.HasSuffix(artifact, "."+msgExt) {
			continue
		}
		artifactPath := filepath.Join(artifactsPath, artifact)
		signaturePath := artifactPath + "." + sigExt
		artifactMeta := msgpack.OrderedMap{
			{Key: "timestamp", Value: uint64(releaseTime)},
		}
		fmt.Printf("Signing %s with %x\n", artifactPath, signer.Hash)
		fh, err := os.ReadFile(artifactPath)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", artifactPath, err)
		}
		rsgBytes, err := rsg.CreateWithOptions(signer, fh, rsg.Options{Meta: artifactMeta})
		if err != nil {
			return fmt.Errorf("Could not create signature for %s: %w", artifactPath, err)
		}
		if err := os.WriteFile(signaturePath, rsgBytes, 0o644); err != nil {
			return fmt.Errorf("could not write signature: %w", err)
		}
		artifactEntries = append(artifactEntries, msgpack.OrderedMap{
			{Key: "name", Value: artifact},
			{Key: "rsg", Value: rsgBytes},
		})
		rsgs = append(rsgs, artifact+"."+sigExt)
	}
	manifestMeta = manifestMeta.Set("artifacts", artifactEntries)

	manifest, err := rsg.CreateWithOptions(signer, []byte(notes), rsg.Options{Embed: true, Meta: manifestMeta})
	if err != nil {
		return fmt.Errorf("Release manifest generation failed: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		return fmt.Errorf("could not write manifest: %w", err)
	}
	artifacts = append(artifacts, rsgs...)
	artifacts = append(artifacts, "manifest."+msgExt)

	if noUpload {
		fmt.Printf("Local release %s:%s generated successfully in %s\n", packageName, tag, target)
		return nil
	}

	if !c.linkReady {
		return errors.New("Failed to establish link")
	}

	// Step 1: init
	fmt.Println("Initializing release on remote...")
	initData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "create",
		"step":               "init",
		"tag":                tag,
		"hash":               commitHash,
		"notes":              notes,
		"notes_format":       "markdown",
	}
	packed, err := msgpack.Pack(initData)
	if err != nil {
		return fmt.Errorf("could not pack init: %w", err)
	}
	resp, _, err := c.sendRequest(pathRelease, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		msg := "Unknown error"
		if len(respBytes) > 1 {
			msg = string(respBytes[1:])
		}
		return fmt.Errorf("Server error during init: %s", msg)
	}
	fmt.Println("Release initialized")

	// Step 2: upload artifacts
	ms := ""
	if len(artifacts) != 1 {
		ms = "s"
	}
	fmt.Printf("\nSending %d artifact%s...\n", len(artifacts), ms)
	for _, artifact := range artifacts {
		artifactPath := filepath.Join(artifactsPath, artifact)
		artifactData, err := os.ReadFile(artifactPath)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", artifact, err)
		}
		artData := map[any]any{
			int64(idxRepository): c.repoPath,
			"operation":          "create",
			"step":               "artifact",
			"tag":                tag,
			"artifact_name":      artifact,
			"artifact_data":      artifactData,
		}
		packed, err := msgpack.Pack(artData)
		if err != nil {
			return fmt.Errorf("could not pack artifact: %w", err)
		}
		resp, _, err := c.sendRequest(pathRelease, packed, fetchPushTimeout)
		if err != nil {
			return err
		}
		respBytes, ok := resp.([]byte)
		if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
			msg := "Unknown error"
			if len(respBytes) > 1 {
				msg = string(respBytes[1:])
			}
			fmt.Printf("  Failed to send %s: %s\n", artifact, msg)
		} else {
			fmt.Printf("  %s (%s) transferred\n", artifact, prettySize(int64(len(artifactData))))
		}
	}

	// Step 3: finalize
	fmt.Println("\nFinalizing release...")
	finData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "create",
		"step":               "finalize",
		"tag":                tag,
	}
	packed, err = msgpack.Pack(finData)
	if err != nil {
		return fmt.Errorf("could not pack finalize: %w", err)
	}
	resp, _, err = c.sendRequest(pathRelease, packed, 300*time.Second)
	if err != nil {
		return err
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote during finalize")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Server error during finalize: %s", string(respBytes[1:]))
	}
	fmt.Printf("Release %s published\n", tag)
	return nil
}

// deleteRelease sends a release "delete" request after confirming,
// mirroring delete_release (server.py).
func (c *reticulumGitClient) deleteRelease(target string) error {
	if target == "" {
		return errors.New("No target specified")
	}
	fmt.Printf("Are you sure you want to delete release %s? [y/N]: ", target)
	var confirm string
	_, _ = fmt.Scanln(&confirm)
	if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
		fmt.Println("Deletion cancelled")
		return nil
	}
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "delete",
		"tag":                target,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack delete request: %w", err)
	}
	resp, _, err := c.sendRequest(pathRelease, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := resp.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	fmt.Printf("Release %s deleted\n", target)
	return nil
}

// latestRelease sends a release "latest" request after confirming,
// mirroring latest_release (server.py).
func (c *reticulumGitClient) latestRelease(target string) error {
	if target == "" {
		return errors.New("No target specified")
	}
	fmt.Printf("Are you sure you want to set %s as the latest release? [y/N]: ", target)
	var confirm string
	_, _ = fmt.Scanln(&confirm)
	if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
		fmt.Println("Update cancelled")
		return nil
	}
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "latest",
		"tag":                target,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack latest request: %w", err)
	}
	resp, _, err := c.sendRequest(pathRelease, packed, requestTimeout)
	if err != nil {
		return err
	}
	respBytes, ok := resp.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	fmt.Printf("Release %s set as latest\n", target)
	return nil
}

// editReleaseNotes opens the editor on a temp file with the release notes
// template, strips lines starting with "#", and returns the stripped
// notes. Returns the empty string when the editor yields no content
// (cancelled), mirroring _edit_release_notes (server.py).
func editReleaseNotes(tag string) (string, error) {
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

	template := strings.ReplaceAll(releaseNotesTemplate, "{TAG}", tag)
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

	content, err := os.ReadFile(tmpPath)
	_ = os.Remove(tmpPath)
	if err != nil {
		return "", fmt.Errorf("could not read notes: %w", err)
	}
	var lines []string
	for line := range strings.SplitSeq(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, line)
	}
	notes := strings.TrimSpace(strings.Join(lines, "\n"))
	return notes, nil
}

// commitHashFromTag resolves a tag to its commit hash via
// `git rev-list -n 1 <tag>`, mirroring __commit_hash_from_tag
// (server.py). Returns the empty string when the tag cannot be resolved.
func commitHashFromTag(tag, repoPath string) string {
	cmd := exec.Command("git", "rev-list", "-n", "1", tag)
	cmd.Dir = repoPath
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// matchSimple is a minimal fnmatch-style matcher supporting "*" as a
// wildcard, mirroring the fnmatch.fnmatch usage in fetch_release
// (server.py). It is case-sensitive on the whole string.
func matchSimple(name, pattern string) bool {
	if pattern == "" {
		return name == ""
	}
	// Convert the glob pattern to a simple recursive match.
	pi, ni := 0, 0
	starPi, starNi := -1, 0
	for ni < len(name) {
		if pi < len(pattern) && (pattern[pi] == name[ni]) {
			pi++
			ni++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			starPi = pi
			starNi = ni
			pi++
		} else if starPi != -1 {
			pi = starPi + 1
			starNi++
			ni = starNi
		} else {
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// prettySize formats a byte count in human-readable form, mirroring
// RNS.prettysize (server.py).
func prettySize(size int64) string {
	if size <= 0 {
		return "0 B"
	}
	const unit = 1024
	div, exp := int64(unit), 0
	for n := size / unit; n >= 1; n /= unit {
		div *= unit
		exp++
	}
	units := "BKMGTPE"
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div/unit), units[exp])
}

// expandUser replaces a leading "~" with the user's home directory.
func expandUser(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			if strings.HasPrefix(path, "~/") {
				return filepath.Join(home, path[2:])
			}
		}
	}
	return path
}

// sigExt and msgExt are the RSG/RSM file extensions (server.py SIG_EXT /
// MSG_EXT).
const (
	sigExt = "rsg"
	msgExt = "rsm"
)
