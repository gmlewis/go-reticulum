// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// client.go implements the standalone RNS client for git-remote-rns,
// mirroring RNS/Utilities/rngit/client.py ReticulumGitClient. It connects to
// a remote repositories destination, drives the /git/list, /git/fetch,
// /git/push, and /git/delete request handlers, and shells out to local git
// for bundle creation/verification/unbundling and ref resolution.
//
// The client implements helperBackend so the protocol state machine in
// protocol.go drives it without knowing anything about RNS. The wire format
// (request map keys, result codes, response byte layout) matches the server
// handlers in cmd/gorngit/server.go exactly.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// Wire-protocol result codes (mirroring cmd/gorngit/server.go res* consts).
const (
	resOK         byte = 0x00
	resDisallowed byte = 0x01
	resInvalidReq byte = 0x02
	resNotFound   byte = 0x03
	resRemoteFail byte = 0xFF
)

// Wire-protocol map keys (mirroring cmd/gorngit/server.go idx* consts).
const (
	idxRepository = 0x00
)

// Request handler paths (mirroring cmd/gorngit/server.go path* consts).
const (
	pathList   = "/git/list"
	pathFetch  = "/git/fetch"
	pathPush   = "/git/push"
	pathDelete = "/git/delete"
)

// RNS namespace constants (mirroring cmd/gorngit appName/repoAspect/protoSpec).
const (
	appName    = "git"
	repoAspect = "repositories"
	protoSpec  = "rns://"
)

// Timing and batching constants (mirroring client.py PATH/LINK_TIMEOUT and
// REF_BATCH_SIZE, plus the gorngit client requestTimeout/fetchPushTimeout).
const (
	destHexLen       = rns.TruncatedHashLength / 8 * 2
	pathTimeout      = 15 * time.Second
	linkTimeout      = 15 * time.Second
	requestTimeout   = 120 * time.Second
	fetchPushTimeout = 7200 * time.Second
	refBatchSize     = 25
)

// rnsClient is the git-remote-rns RNS client. It owns an RNS transport, a
// client identity, and (after connect) a link to the remote repositories
// destination. remoteRefs caches the ref list returned by the most recent
// /git/list call so fetch and push can build exclusion/have lists.
type rnsClient struct {
	transport rns.Transport
	identity  *rns.Identity
	link      *rns.Link

	destHash  []byte
	repoPath  string // "<group>/<repo>"
	configDir string

	linkReady  bool
	linkFailed bool

	remoteRefs map[string]string // ref name -> sha, populated by list

	// progress, when non-nil, is invoked with human-readable transfer
	// progress lines written to the helper's stderr. It mirrors the
	// sys.stderr.write progress reporting in client.py _on_progress.
	progress func(string)

	mu     sync.Mutex
	respCh chan any
}

// newRnsClient parses remoteURL (rns://<hash>/<group>/<repo>), loads or
// creates a client identity from configDir/client_identity, and returns a
// client ready to connect. It mirrors ReticulumGitClient.__init__
// (client.py).
func newRnsClient(ts rns.Transport, configDir, remoteURL string, logger *rns.Logger) (*rnsClient, error) {
	destHash, group, repo, err := parseRemoteURL(remoteURL)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, fmt.Errorf("could not create config dir: %w", err)
	}
	identityPath := filepath.Join(configDir, "client_identity")
	identity, err := loadOrCreateIdentity(identityPath, logger)
	if err != nil {
		return nil, fmt.Errorf("could not load client identity: %w", err)
	}

	return &rnsClient{
		transport:  ts,
		identity:   identity,
		destHash:   destHash,
		repoPath:   group + "/" + repo,
		configDir:  configDir,
		remoteRefs: make(map[string]string),
	}, nil
}

// connect resolves the path to the remote destination, creates an outbound
// destination and link, and waits for link establishment and identification,
// mirroring connect_server + link_established (client.py).
func (c *rnsClient) connect(logger *rns.Logger) error {
	remoteIdentity, err := resolveRemoteIdentity(c.transport, c.destHash, pathTimeout)
	if err != nil {
		return err
	}

	remoteDest, err := rns.NewDestination(c.transport, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, appName, repoAspect)
	if err != nil {
		return fmt.Errorf("could not create remote destination: %w", err)
	}

	link, err := rns.NewLink(c.transport, remoteDest)
	if err != nil {
		return fmt.Errorf("could not create link: %w", err)
	}
	c.link = link

	established := make(chan struct{}, 1)
	failed := make(chan struct{}, 1)
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		if err := l.Identify(c.identity); err != nil {
			logger.Warning("Identify failed: %s", err)
		}
		c.linkReady = true
		established <- struct{}{}
	})
	link.SetLinkClosedCallback(func(l *rns.Link) {
		if !c.linkReady {
			c.linkFailed = true
			failed <- struct{}{}
		}
	})

	if err := link.Establish(); err != nil {
		return fmt.Errorf("could not establish link: %w", err)
	}

	select {
	case <-established:
		return nil
	case <-failed:
		return errors.New("link establishment failed")
	case <-time.After(linkTimeout):
		return errors.New("link establishment timed out")
	}
}

// teardown closes the link if open, mirroring the link teardown in run()
// (client.py).
func (c *rnsClient) teardown() {
	if c.link != nil {
		c.link.Teardown()
	}
}

// sendRequest issues a synchronous request over the link and waits for the
// response, mirroring send_request (client.py). The response is returned as
// the raw []byte the server wrote (first byte is the result code).
func (c *rnsClient) sendRequest(path string, data any, timeout time.Duration) ([]byte, error) {
	if !c.linkReady {
		return nil, errors.New("link not ready at request time")
	}

	c.mu.Lock()
	c.respCh = make(chan any, 1)
	ch := c.respCh
	c.mu.Unlock()

	_, err := c.link.Request(
		path,
		data,
		func(rr *rns.RequestReceipt) {
			if ch != nil {
				ch <- rr.Response
			}
		},
		func(rr *rns.RequestReceipt) {
			if ch != nil {
				ch <- nil
			}
		},
		nil,
		timeout,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("could not send request: %w", err)
	}

	select {
	case val := <-ch:
		if val == nil {
			return nil, errors.New("request failed or timed out")
		}
		resp, ok := val.([]byte)
		if !ok {
			return nil, fmt.Errorf("unexpected response type %T", val)
		}
		return resp, nil
	case <-time.After(timeout):
		return nil, errors.New("request timed out")
	}
}

// list sends a /git/list request and returns the ref-list text payload,
// mirroring handle_git_list (client.py). It also caches the parsed refs in
// c.remoteRefs for later use by fetch and push. The returned string is the
// ref-list body (without the result-code prefix).
func (c *rnsClient) list(forPush bool) (string, error) {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"for_push":           forPush,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return "", fmt.Errorf("could not pack list request: %w", err)
	}

	resp, err := c.sendRequest(pathList, packed, requestTimeout)
	if err != nil {
		return "", err
	}

	code, payload, ok := parseListResponse(resp)
	if !ok {
		return "", errors.New("empty list response from server")
	}
	if code != resOK {
		return "", fmt.Errorf("server refused list: %s", payload)
	}

	c.cacheRemoteRefs(payload)
	return payload, nil
}

// cacheRemoteRefs parses the ref-list text and records ref->sha pairs in
// c.remoteRefs, mirroring the remote_refs bookkeeping in handle_git_list
// (client.py). The "@<ref> HEAD" symref line is ignored.
func (c *rnsClient) cacheRemoteRefs(refListText string) {
	c.remoteRefs = make(map[string]string)
	for _, line := range strings.Split(refListText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		sha, refName := parts[0], parts[1]
		if refName == "HEAD" {
			continue
		}
		if strings.HasPrefix(refName, "@") {
			continue
		}
		c.remoteRefs[refName] = sha
	}
}

// fetch fetches the requested refs and unbundles them locally, mirroring
// process_fetch_queue (client.py). For each batch it builds a per-ref "have"
// hint from the local ref SHA and a global have list from remote refs whose
// objects the client already has, requests a thin bundle from the server,
// verifies it, and runs `git bundle unbundle` to import the objects into the
// local repository.
func (c *rnsClient) fetch(refs []fetchRef) error {
	if len(refs) == 0 {
		return nil
	}

	haveSHAs := c.localHaves()
	batchStart := 0
	for batchStart < len(refs) {
		batchEnd := batchStart + refBatchSize
		if batchEnd > len(refs) {
			batchEnd = len(refs)
		}
		batch := refs[batchStart:batchEnd]
		batchStart = batchEnd

		refsList := make([]map[string]string, 0, len(batch))
		for _, fr := range batch {
			entry := map[string]string{"sha": fr.sha, "ref": fr.ref}
			if localSHA, ok := localRefSHA(fr.ref); ok && localSHA != fr.sha {
				entry["have"] = localSHA
			}
			refsList = append(refsList, entry)
		}

		requestData := map[any]any{
			int64(idxRepository): c.repoPath,
			"refs":               refsList,
		}
		if len(haveSHAs) > 0 {
			requestData["have"] = haveSHAs
		}
		packed, err := msgpack.Pack(requestData)
		if err != nil {
			return fmt.Errorf("could not pack fetch request: %w", err)
		}

		resp, err := c.sendRequest(pathFetch, packed, fetchPushTimeout)
		if err != nil {
			return err
		}
		code, bundleData, msg, ok := parseFetchResponse(resp)
		if !ok {
			return errors.New("invalid fetch response from server")
		}
		if code != resOK {
			return fmt.Errorf("fetch failed: %s", errorMessage(code, msg))
		}
		if len(bundleData) == 0 {
			// Empty bundle: all requested objects already exist locally.
			continue
		}
		if err := unbundleBytes(bundleData, c.progress); err != nil {
			return fmt.Errorf("could not unbundle fetched data: %w", err)
		}
	}
	return nil
}

// push pushes the requested refs and returns one status per ref, mirroring
// process_push_queue (client.py). For each ref it either requests deletion
// (/git/delete) when localRef is empty, or creates a local bundle excluding
// objects the remote already has and sends it via /git/push. When a bundle
// comes back empty (all objects already on the remote), it retries without
// exclusions so the server still updates the ref.
func (c *rnsClient) push(refs []pushRef) ([]pushStatus, error) {
	statuses := make([]pushStatus, 0, len(refs))
	for _, pr := range refs {
		if pr.deletion {
			statuses = append(statuses, c.pushDelete(pr))
			continue
		}
		statuses = append(statuses, c.pushBundle(pr))
	}
	return statuses, nil
}

// pushDelete sends a /git/delete request for one ref, mirroring the deletion
// branch of process_push_queue (client.py).
func (c *rnsClient) pushDelete(pr pushRef) pushStatus {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"ref":                pr.remoteRef,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: "Could not pack delete request"}
	}
	resp, err := c.sendRequest(pathDelete, packed, requestTimeout)
	if err != nil {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: err.Error()}
	}
	code, msg, ok := parseSimpleResponse(resp)
	if !ok {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: "No response from server"}
	}
	if code != resOK {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: errorMessage(code, msg)}
	}
	return pushStatus{remoteRef: pr.remoteRef, ok: true}
}

// pushBundle creates a local bundle for one ref and sends it via /git/push,
// mirroring the bundle branch of process_push_queue (client.py).
func (c *rnsClient) pushBundle(pr pushRef) pushStatus {
	if _, ok := localRefSHA(pr.localRef); !ok {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: fmt.Sprintf("Could not resolve local ref %s", pr.localRef)}
	}

	bundleData, empty, err := c.createPushBundle(pr.localRef)
	if err != nil {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: err.Error()}
	}
	if empty {
		// All reachable objects are already on the remote. Retry without
		// exclusions so the bundle carries the ref update even when no new
		// objects are needed.
		bundleData, empty, err = c.createPushBundleNoExclude(pr.localRef)
		if err != nil {
			return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: err.Error()}
		}
		if empty {
			return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: "Empty bundle for push"}
		}
	}

	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"local_ref":          pr.localRef,
		"remote_ref":         pr.remoteRef,
		"force":              pr.force,
		"bundle":             bundleData,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: "Could not pack push request"}
	}
	resp, err := c.sendRequest(pathPush, packed, fetchPushTimeout)
	if err != nil {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: err.Error()}
	}
	code, msg, ok := parseSimpleResponse(resp)
	if !ok {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: "No response from server"}
	}
	if code != resOK {
		return pushStatus{remoteRef: pr.remoteRef, ok: false, msg: errorMessage(code, msg)}
	}
	return pushStatus{remoteRef: pr.remoteRef, ok: true}
}

// createPushBundle runs `git bundle create` for localRef, excluding any
// remote-ref SHAs that the client already has objects for, mirroring the
// bundle-create logic in process_push_queue (client.py). It returns the
// bundle bytes, a flag reporting that git reported an empty bundle (all
// objects already excluded), or an error.
func (c *rnsClient) createPushBundle(localRef string) ([]byte, bool, error) {
	args := []string{"bundle", "create", "--no-progress", "-", localRef}
	for _, sha := range c.remoteRefs {
		if objectExistsLocally(sha) {
			args = append(args, "^"+sha)
		}
	}
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "empty bundle") {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("Bundle creation failed")
	}
	return stdout.Bytes(), false, nil
}

// createPushBundleNoExclude runs `git bundle create` for localRef without any
// ^<sha> exclusions. It is used when the excluded bundle is empty so the
// server still receives a ref update.
func (c *rnsClient) createPushBundleNoExclude(localRef string) ([]byte, bool, error) {
	cmd := exec.Command("git", "bundle", "create", "--no-progress", "-", localRef)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "empty bundle") {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("Bundle creation failed")
	}
	return stdout.Bytes(), false, nil
}

// localHaves returns the subset of c.remoteRefs SHAs whose objects the client
// already has locally, mirroring the have_shas construction in
// process_fetch_queue (client.py).
func (c *rnsClient) localHaves() []string {
	var haves []string
	for _, sha := range c.remoteRefs {
		if objectExistsLocally(sha) {
			haves = append(haves, sha)
		}
	}
	return haves
}

// localRefSHA resolves a local ref to its SHA via `git rev-parse`, mirroring
// the rev-parse calls in process_fetch_queue / process_push_queue (client.py).
func localRefSHA(ref string) (string, bool) {
	cmd := exec.Command("git", "rev-parse", ref)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(stdout.String()), true
}

// objectExistsLocally reports whether the given SHA names an object in the
// local repository, mirroring the `git cat-file -t <sha>` check used in
// process_fetch_queue and process_push_queue (client.py).
func objectExistsLocally(sha string) bool {
	cmd := exec.Command("git", "cat-file", "-t", sha)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// unbundleBytes writes the bundle to a temp file, verifies it, and runs
// `git bundle unbundle` to import its objects into the local repository,
// mirroring the verify+unbundle steps in process_fetch_queue (client.py).
func unbundleBytes(bundleData []byte, progress func(string)) error {
	tmpDir, err := os.MkdirTemp("", "gogit-remote-rns-fetch-")
	if err != nil {
		return fmt.Errorf("could not create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	bundlePath := filepath.Join(tmpDir, "fetch.bundle")
	if err := os.WriteFile(bundlePath, bundleData, 0o644); err != nil {
		return fmt.Errorf("could not write bundle: %w", err)
	}

	verify := exec.Command("git", "bundle", "verify", "-q", bundlePath)
	verify.Stdout = io.Discard
	verify.Stderr = io.Discard
	if err := verify.Run(); err != nil {
		return fmt.Errorf("bundle verification failed: %w", err)
	}

	unbundle := exec.Command("git", "bundle", "unbundle", bundlePath)
	unbundle.Stdout = io.Discard
	unbundle.Stderr = io.Discard
	if err := unbundle.Run(); err != nil {
		return fmt.Errorf("bundle unbundle failed: %w", err)
	}
	return nil
}

// resolveRemoteIdentity resolves the identity for destHash, requesting a path
// if it is not already known, mirroring connect_remote (client.py) and the
// gornsh resolveRemoteIdentity pattern.
func resolveRemoteIdentity(ts rns.Transport, destHash []byte, timeout time.Duration) (*rns.Identity, error) {
	remoteIdentity := rns.RecallIdentity(ts, destHash)
	if remoteIdentity != nil {
		return remoteIdentity, nil
	}
	if err := ts.RequestPath(destHash); err != nil {
		return nil, fmt.Errorf("could not request path to %x: %w", destHash, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		remoteIdentity = rns.RecallIdentity(ts, destHash)
		if remoteIdentity != nil {
			return remoteIdentity, nil
		}
	}
	return nil, fmt.Errorf("could not resolve remote identity for destination %x", destHash)
}

// loadOrCreateIdentity loads an identity from identityPath or creates and
// persists a new one, mirroring __apply_config identity loading (client.py).
func loadOrCreateIdentity(identityPath string, logger *rns.Logger) (*rns.Identity, error) {
	if identity, err := rns.FromFile(identityPath, logger); err == nil && identity != nil {
		logger.Verbose("Client identity loaded from %s", identityPath)
		return identity, nil
	}
	identity, err := rns.NewIdentity(true, logger)
	if err != nil {
		return nil, fmt.Errorf("could not create identity: %w", err)
	}
	if err := identity.ToFile(identityPath); err != nil {
		return nil, fmt.Errorf("could not persist identity to %s: %w", identityPath, err)
	}
	logger.Verbose("Client identity generated and persisted to %s", identityPath)
	return identity, nil
}

// parseRemoteURL parses an rns://<hash>/<group>/<repo> repository URL,
// mirroring client.py main()'s url[6:].split("/", 2) parsing and the
// shared parse_remote_url validation in server.py. The scheme match is
// case-insensitive.
func parseRemoteURL(remote string) ([]byte, string, string, error) {
	if !strings.HasPrefix(strings.ToLower(remote), protoSpec) {
		return nil, "", "", fmt.Errorf("Invalid protocol in remote URL")
	}
	components := strings.Split(remote[len(protoSpec):], "/")
	if len(components) != 3 {
		return nil, "", "", fmt.Errorf("Invalid number of URL components")
	}
	destHash, err := parseDestHash(components[0])
	if err != nil {
		return nil, "", "", err
	}
	return destHash, components[1], components[2], nil
}

// parseDestHash decodes and length-validates a destination hash component,
// mirroring the shared parseDestHash logic in cmd/gorngit/util.go.
func parseDestHash(hexHash string) ([]byte, error) {
	if len(hexHash) != destHexLen {
		return nil, fmt.Errorf("Invalid destination hash length")
	}
	dest, err := rns.HexToBytes(hexHash)
	if err != nil {
		return nil, fmt.Errorf("Invalid destination hash: %w", err)
	}
	return dest, nil
}

// parseListResponse splits a /git/list response into its result-code byte and
// text payload, mirroring cmd/gorngit/server.go parseListResponse. Returns
// ok=false when the response is too short to contain a result code.
func parseListResponse(response []byte) (code byte, payload string, ok bool) {
	if len(response) == 0 {
		return 0, "", false
	}
	return response[0], string(response[1:]), true
}

// parseFetchResponse splits a /git/fetch response body into its result-code
// byte and payload, mirroring cmd/gorngit/server.go parseFetchResponse. For
// resOK the payload is the bundle data (possibly empty); for error codes the
// payload is the message string.
func parseFetchResponse(resp []byte) (code byte, bundleData []byte, msg string, ok bool) {
	if len(resp) == 0 {
		return 0, nil, "", false
	}
	code = resp[0]
	if code == resOK {
		return code, resp[1:], "", true
	}
	return code, nil, string(resp[1:]), true
}

// parseSimpleResponse splits a /git/push or /git/delete response into its
// result-code byte and message payload, used for single-byte-status
// responses. Returns ok=false when the response is empty.
func parseSimpleResponse(resp []byte) (code byte, msg string, ok bool) {
	if len(resp) == 0 {
		return 0, "", false
	}
	code = resp[0]
	if len(resp) > 1 {
		msg = string(resp[1:])
	}
	return code, msg, true
}

// errorMessage maps a non-OK result code to a human-readable message,
// mirroring the error decoding in client.py process_fetch_queue /
// process_push_queue and cmd/gorngit/client.go.
func errorMessage(code byte, msg string) string {
	switch code {
	case resInvalidReq:
		if msg == "" {
			msg = "Invalid request"
		}
		return "Remote error: " + msg
	case resNotFound:
		return "Not found"
	case resDisallowed:
		if msg == "" {
			msg = "Not allowed"
		}
		return msg
	case resRemoteFail:
		if msg == "" {
			msg = "Unknown error"
		}
		return "Remote error: " + msg
	default:
		if msg == "" {
			msg = "Unknown error"
		}
		return "Remote error: " + msg
	}
}
