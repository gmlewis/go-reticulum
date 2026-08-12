// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// client.go implements the gorngit client wire-protocol library, mirroring
// RNS/Utilities/rngit/server.py ReticulumGitClient (the CLI client used by
// `rngit create`, `rngit fork`, etc.) and the list/create request logic from
// client.py handle_git_list / create_repository.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// pathTimeout is the default time to wait for a path to the remote
// destination, mirroring PATH_TIMEOUT (client.py / server.py).
const pathTimeout = 15 * time.Second

// linkTimeout is the default time to wait for link establishment, mirroring
// LINK_TIMEOUT (client.py / server.py).
const linkTimeout = 15 * time.Second

// requestTimeout is the default timeout for a single request/response cycle,
// mirroring send_request timeout (server.py).
const requestTimeout = 120 * time.Second

// fetchPushTimeout is the timeout for fetch/push requests, which may transfer
// large bundles as streamed Resources. It mirrors the Python client's
// send_request default timeout of 7200 seconds (client.py).
const fetchPushTimeout = 7200 * time.Second

// reticulumGitClient is the gorngit CLI client, mirroring
// server.py ReticulumGitClient. It connects to a remote repositories
// destination, identifies, and issues /git/list and /git/create requests.
type reticulumGitClient struct {
	transport      rns.Transport
	identity       *rns.Identity
	remoteIdentity *rns.Identity
	destination    *rns.Destination
	link           *rns.Link

	destinationHash []byte
	groupName       string
	repoName        string
	repoPath        string

	configDir    string
	identityPath string

	linkReady  bool
	linkFailed bool

	mu     sync.Mutex
	respCh chan any
}

// newReticulumGitClient prepares a client that will connect to the remote
// destination described by remoteURL (rns://<hash>/<group>/<repo>). It loads
// or creates a client identity from configDir/client_identity (or
// identityPath when non-empty). Mirrors ReticulumGitClient.__init__
// (server.py).
func newReticulumGitClient(ts rns.Transport, configDir, identityPath, remoteURL string, logger *rns.Logger) (*reticulumGitClient, error) {
	destHash, group, repo, err := parseRemoteURL(remoteURL)
	if err != nil {
		return nil, err
	}

	if identityPath == "" {
		identityPath = filepath.Join(configDir, "client_identity")
	}
	identity, err := loadOrCreateIdentity(identityPath, logger)
	if err != nil {
		return nil, fmt.Errorf("could not load client identity: %w", err)
	}

	return &reticulumGitClient{
		transport:       ts,
		identity:        identity,
		destinationHash: destHash,
		groupName:       group,
		repoName:        repo,
		repoPath:        group + "/" + repo,
		configDir:       configDir,
		identityPath:    identityPath,
	}, nil
}

// connect resolves the path to the remote destination, creates an outbound
// destination and link, and waits for link establishment. Mirrors
// connect_remote + link_established (server.py).
func (c *reticulumGitClient) connect(logger *rns.Logger) error {
	remoteIdentity, err := resolveRemoteIdentity(c.transport, c.destinationHash, pathTimeout)
	if err != nil {
		return err
	}
	c.remoteIdentity = remoteIdentity

	remoteDest, err := rns.NewDestination(c.transport, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, appName, repoAspect)
	if err != nil {
		return fmt.Errorf("could not create remote destination: %w", err)
	}
	c.destination = remoteDest

	link, err := rns.NewLink(c.transport, remoteDest)
	if err != nil {
		return fmt.Errorf("could not create link: %w", err)
	}
	c.link = link

	established := make(chan struct{}, 1)
	failed := make(chan struct{}, 1)
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		if err := l.Identify(c.identity); err != nil {
			logger.Warning("Identify failed: %v", err)
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

// sendRequest issues a synchronous request over the link and waits for the
// response, mirroring send_request (server.py). It returns the response
// ([]byte for inline responses) and metadata.
func (c *reticulumGitClient) sendRequest(path string, data any, timeout time.Duration) (any, any, error) {
	if !c.linkReady {
		return nil, nil, errors.New("link not ready at request time")
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
		return nil, nil, fmt.Errorf("could not send request: %w", err)
	}

	select {
	case val := <-ch:
		if val == nil {
			return nil, nil, errors.New("request failed or timed out")
		}
		return val, nil, nil
	case <-time.After(timeout):
		return nil, nil, errors.New("request timed out")
	}
}

// list sends a /git/list request and returns the ref-list text payload,
// mirroring handle_git_list (client.py). The returned string is the ref-list
// body (without the result-code prefix).
func (c *reticulumGitClient) list(forPush bool) (string, error) {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
		"for_push":           forPush,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return "", fmt.Errorf("could not pack list request: %w", err)
	}

	response, _, err := c.sendRequest(pathList, packed, requestTimeout)
	if err != nil {
		return "", err
	}

	respBytes, ok := response.([]byte)
	if !ok {
		return "", errors.New("invalid list response from server")
	}

	code, payload, ok := parseListResponse(respBytes)
	if !ok {
		return "", errors.New("empty list response from server")
	}
	if code != resOK {
		return "", fmt.Errorf("server refused list: %s", payload)
	}
	return payload, nil
}

// create sends a /git/create request, mirroring create_repository (server.py
// client side). Returns nil on success.
func (c *reticulumGitClient) create() error {
	requestData := map[any]any{
		int64(idxRepository): c.repoPath,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack create request: %w", err)
	}

	response, _, err := c.sendRequest(pathCreate, packed, requestTimeout)
	if err != nil {
		return err
	}

	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("no response from remote")
	}

	code := respBytes[0]
	payload := ""
	if len(respBytes) > 1 {
		payload = string(respBytes[1:])
	}

	switch code {
	case resOK:
		fmt.Printf("Repository %s created\n", c.repoPath)
		return nil
	case resInvalidReq:
		return errors.New("Remote error: Invalid request")
	case resNotFound:
		return errors.New("Not found")
	case resDisallowed:
		if payload == "" {
			payload = "Not allowed"
		}
		return errors.New(payload)
	default:
		if payload == "" {
			payload = "Unknown error"
		}
		return fmt.Errorf("Remote error: %s", payload)
	}
}

// fetch sends a /git/fetch request for the given wanted refs and optional
// global have-SHAs (for thin-bundle exclusion), mirroring
// process_fetch_queue (client.py). The server returns a bundle containing
// the objects reachable from the wanted refs but not from the have-SHAs.
// When all objects are already on the client, the server returns resOK with
// no bundle data; in that case fetch returns an empty (non-nil) slice. The
// returned bundle bytes can be verified with `git bundle verify` and unbundled
// with `git bundle unbundle`.
func (c *reticulumGitClient) fetch(refs []fetchRefEntry, haves []string) ([]byte, error) {
	requestData := buildFetchRequestMap(c.repoPath, refs, haves)
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return nil, fmt.Errorf("could not pack fetch request: %w", err)
	}

	response, _, err := c.sendRequest(pathFetch, packed, fetchPushTimeout)
	if err != nil {
		return nil, err
	}

	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return nil, errors.New("no response from server")
	}

	code, bundleData, msg, ok := parseFetchResponse(respBytes)
	if !ok {
		return nil, errors.New("invalid fetch response from server")
	}

	switch code {
	case resOK:
		return bundleData, nil
	case resInvalidReq:
		if msg == "" {
			msg = "Invalid request"
		}
		return nil, fmt.Errorf("Remote error: %s", msg)
	case resNotFound:
		return nil, errors.New("Not found")
	case resDisallowed:
		if msg == "" {
			msg = "Not allowed"
		}
		return nil, errors.New(msg)
	default:
		if msg == "" {
			msg = "Unknown error"
		}
		return nil, fmt.Errorf("Remote error: %s", msg)
	}
}

// push sends a /git/push request containing a pre-built bundle, mirroring
// process_push_queue's bundle path (client.py). The server verifies the
// bundle and runs `git fetch <bundle> <local_ref>:<remote_ref>` to update the
// remote bare repository's ref. Returns nil on success.
func (c *reticulumGitClient) push(localRef, remoteRef string, force bool, bundleData []byte) error {
	requestData := buildPushRequestMap(c.repoPath, localRef, remoteRef, force, bundleData)
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack push request: %w", err)
	}

	response, _, err := c.sendRequest(pathPush, packed, fetchPushTimeout)
	if err != nil {
		return err
	}

	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("no response from remote")
	}

	code := respBytes[0]
	msg := ""
	if len(respBytes) > 1 {
		msg = string(respBytes[1:])
	}

	switch code {
	case resOK:
		return nil
	case resInvalidReq:
		return errors.New("Remote error: Invalid request")
	case resNotFound:
		return errors.New("Not found")
	case resDisallowed:
		if msg == "" {
			msg = "Not allowed"
		}
		return errors.New(msg)
	default:
		if msg == "" {
			msg = "Unknown error"
		}
		return fmt.Errorf("Remote error: %s", msg)
	}
}

// fork sends a /git/fork request, asking the remote node to fork sourceURL
// into the client's target repository, mirroring fork_repository (server.py
// client side). Returns nil on success.
func (c *reticulumGitClient) fork(sourceURL string) error {
	return c.remoteCloneOperation(sourceURL, pathFork, "fork")
}

// mirror sends a /git/mirror request, asking the remote node to mirror
// sourceURL into the client's target repository, mirroring mirror_repository
// (server.py client side). Returns nil on success.
func (c *reticulumGitClient) mirror(sourceURL string) error {
	return c.remoteCloneOperation(sourceURL, pathMirror, "mirror")
}

// remoteCloneOperation is the shared fork/mirror client flow, mirroring
// _remote_clone_operation (server.py client side). It packs idxRepository and
// the source URL, sends the request, and reports the result. opName ("fork" or
// "mirror") is used in the success message.
func (c *reticulumGitClient) remoteCloneOperation(sourceURL, requestPath, opName string) error {
	requestData := buildRemoteCloneRequestMap(c.repoPath, sourceURL)
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack %s request: %w", opName, err)
	}

	response, _, err := c.sendRequest(requestPath, packed, fetchPushTimeout)
	if err != nil {
		return err
	}

	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("no response from remote")
	}

	code, msg := parseResultResponse(respBytes)
	switch code {
	case resOK:
		fmt.Printf("Repository %sed to %s\n", opName, c.repoPath)
		return nil
	case resInvalidReq:
		if msg == "" {
			msg = "Invalid request"
		}
		return errors.New(msg)
	case resNotFound:
		if msg == "" {
			msg = "Not found"
		}
		return errors.New(msg)
	case resDisallowed:
		if msg == "" {
			msg = "Not allowed"
		}
		return errors.New(msg)
	default:
		if msg == "" {
			msg = "Unknown error"
		}
		return fmt.Errorf("Server error: %s", msg)
	}
}

// sync sends a /git/sync request, asking the remote node to re-fetch the
// client's repository from its recorded upstream, mirroring sync_repository
// (server.py client side). Returns nil on success.
func (c *reticulumGitClient) sync() error {
	requestData := buildSyncRequestMap(c.repoPath)
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack sync request: %w", err)
	}

	response, _, err := c.sendRequest(pathSync, packed, fetchPushTimeout)
	if err != nil {
		return err
	}

	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("no response from remote")
	}

	code, msg := parseResultResponse(respBytes)
	switch code {
	case resOK:
		fmt.Println("Repository synced")
		return nil
	case resInvalidReq:
		if msg == "" {
			msg = "Invalid request"
		}
		return errors.New(msg)
	case resNotFound:
		if msg == "" {
			msg = "Not found"
		}
		return errors.New(msg)
	case resDisallowed:
		if msg == "" {
			msg = "Not allowed"
		}
		return errors.New(msg)
	default:
		if msg == "" {
			msg = "Unknown error"
		}
		return fmt.Errorf("Server error: %s", msg)
	}
}

// deleteRef sends a /git/delete request to remove a single ref from the
// client's remote repository, mirroring handle_delete's client-side flow
// (server.py wires /git/delete but exposes no CLI subcommand). Returns nil on
// success.
func (c *reticulumGitClient) deleteRef(ref string) error {
	requestData := buildDeleteRequestMap(c.repoPath, ref)
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		return fmt.Errorf("could not pack delete request: %w", err)
	}

	response, _, err := c.sendRequest(pathDelete, packed, requestTimeout)
	if err != nil {
		return err
	}

	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("no response from remote")
	}

	code, msg := parseResultResponse(respBytes)
	switch code {
	case resOK:
		return nil
	case resInvalidReq:
		if msg == "" {
			msg = "Invalid request"
		}
		return errors.New(msg)
	case resNotFound:
		if msg == "" {
			msg = "Not found"
		}
		return errors.New(msg)
	case resDisallowed:
		if msg == "" {
			msg = "Not allowed"
		}
		return errors.New(msg)
	default:
		if msg == "" {
			msg = "Unknown error"
		}
		return fmt.Errorf("Server error: %s", msg)
	}
}

// parseResultResponse splits a generic /git/* response body into its
// result-code byte and trailing message payload, mirroring the status-byte
// parsing shared by _remote_clone_operation / sync_repository (server.py
// client side). Returns ok=false when the response is empty.
func parseResultResponse(resp []byte) (code byte, msg string) {
	if len(resp) == 0 {
		return 0, ""
	}
	code = resp[0]
	if len(resp) > 1 {
		msg = string(resp[1:])
	}
	return code, msg
}

// teardown closes the link if open.
func (c *reticulumGitClient) teardown() {
	if c.link != nil {
		c.link.Teardown()
	}
}

// resolveRemoteIdentity resolves the identity for destHash, requesting a path
// if it is not already known, mirroring connect_remote + gornsh
// resolveRemoteIdentity.
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

// loadClientConfigDir ensures the client config directory exists and returns
// its path. When configDir is empty it defaults to ~/.rngit, mirroring
// ReticulumGitClient.__init__ (server.py).
func loadClientConfigDir(configDir string) (string, error) {
	if configDir != "" {
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			return "", fmt.Errorf("could not create config dir: %w", err)
		}
		return configDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home dir: %w", err)
	}
	dir := filepath.Join(home, ".rngit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create config dir: %w", err)
	}
	return dir, nil
}
