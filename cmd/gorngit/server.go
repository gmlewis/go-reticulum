// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// server.go implements the gorngit repository node serve loop and the
// /git/list and /git/create request handlers, mirroring
// RNS/Utilities/rngit/server.py (ReticulumGitNode, rngit v1.4.2).

package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// Wire-protocol result codes (server.py RES_*).
const (
	resOK         byte = 0x00
	resDisallowed byte = 0x01
	resInvalidReq byte = 0x02
	resNotFound   byte = 0x03
	resRemoteFail byte = 0xFF
)

// Wire-protocol map keys (server.py IDX_*).
const (
	idxRepository = 0x00
	idxResultCode = 0x01
	idxGroup      = 0x02
)

// Request handler paths (server.py PATH_*).
const (
	pathList    = "/git/list"
	pathFetch   = "/git/fetch"
	pathPush    = "/git/push"
	pathCreate  = "/git/create"
	pathFork    = "/git/fork"
	pathSync    = "/git/sync"
	pathMirror  = "/git/mirror"
	pathDelete  = "/git/delete"
	pathRelease = "/mgmt/release"
	pathWork    = "/mgmt/work"
	pathPerms   = "/mgmt/perms"
)

// repoAspect is the RNS destination aspect for the repositories endpoint.
const repoAspect = "repositories"

// requestPathLimit is the maximum length of a group or repository name in a
// request path, mirroring parse_request_repository_path (server.py).
const requestPathLimit = 256

// repositoryInfo holds runtime metadata about a loaded bare repository.
// forkSource and mirrorSource carry the upstream source URL when the repo was
// created as a fork or mirror (mirroring repo["fork"] / repo["mirror"] in
// server.py load_repository); both are empty for a plain repository. perms
// holds the parsed .allowed permission lists for this repo.
type repositoryInfo struct {
	name         string
	group        string
	path         string
	forkSource   string
	mirrorSource string
	perms        permissionLists
}

// groupInfo holds runtime metadata about a repository group (a directory of
// bare repos). perms holds the parsed <groupPath>.allowed permission lists
// plus any config [access] entries applied on top, mirroring
// update_group_permissions (server.py).
type groupInfo struct {
	name         string
	path         string
	repositories map[string]*repositoryInfo
	perms        permissionLists
}

// reticulumGitNode is the gorngit server, mirroring server.py
// ReticulumGitNode. blockedIdentities maps hex identity hashes to true for
// identities blocked by [rngit] blocked_identities. identityAliases maps
// alias names to hex identity hashes from [aliases].
type reticulumGitNode struct {
	config            *nodeConfig
	identity          *rns.Identity
	destination       *rns.Destination
	groups            map[string]*groupInfo
	blockedIdentities map[string]bool
	identityAliases   map[string]string
	ready             bool
	shouldRun         bool
	announceInterval  time.Duration
	nodeName          string
	configDir         string
	stats             map[any]any
	statsMu           sync.Mutex
	statsEnabled      bool
	statsIgnored      map[string]bool
	statsPath         string
	pageServer        *pageNode
}

// newReticulumGitNode loads the node config and identity from configDir and
// returns a node ready to register handlers and serve. It mirrors
// ReticulumGitNode.__init__ + __apply_config (server.py).
func newReticulumGitNode(configDir string, logger *rns.Logger) (*reticulumGitNode, error) {
	cfg, err := loadNodeConfig(configDir)
	if err != nil {
		return nil, fmt.Errorf("could not load node config: %w", err)
	}

	identityPath := filepath.Join(configDir, "repositories_identity")
	identity, err := loadOrCreateIdentity(identityPath, logger)
	if err != nil {
		return nil, fmt.Errorf("could not load repositories identity: %w", err)
	}

	node := &reticulumGitNode{
		config:            cfg,
		identity:          identity,
		groups:            make(map[string]*groupInfo),
		blockedIdentities: make(map[string]bool),
		identityAliases:   make(map[string]string),
		announceInterval:  cfg.announceInterval,
		nodeName:          cfg.nodeName,
		configDir:         configDir,
		stats:             map[any]any{"pages": map[any]any{"front": map[any]any{}}, "groups": map[any]any{}},
		statsEnabled:      cfg.recordStats,
		statsIgnored:      make(map[string]bool),
		statsPath:         filepath.Join(configDir, "stats"),
	}

	node.loadAliasesAndBlocked(cfg)
	node.loadStatsIgnored(cfg)

	for groupName, groupPath := range cfg.groups {
		if _, err := os.Stat(groupPath); err != nil {
			logger.Warning("Repository group %q path %q does not exist, skipping: %v", groupName, groupPath, err)
			continue
		}
		node.loadRepositoryGroup(groupName, groupPath, logger)
	}

	return node, nil
}

// loadRepositoryGroup scans groupPath for bare git repositories and registers
// them, mirroring load_repository_group + load_repository (server.py). It
// also parses <groupPath>.allowed and config [access] into the group perm
// lists via updateGroupPermissions, and <repoPath>.allowed into each repo.
func (n *reticulumGitNode) loadRepositoryGroup(groupName, groupPath string, logger *rns.Logger) {
	group, ok := n.groups[groupName]
	if !ok {
		group = &groupInfo{
			name:         groupName,
			path:         groupPath,
			repositories: make(map[string]*repositoryInfo),
		}
		n.groups[groupName] = group
	}
	if group.path != groupPath {
		logger.Warning("Repository group %q path mismatch, aborting load", groupName)
		return
	}

	n.updateGroupPermissions(groupName)

	entries, err := os.ReadDir(groupPath)
	if err != nil {
		logger.Warning("Could not list repository group %q at %q: %v", groupName, groupPath, err)
		return
	}

	loaded := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoPath := filepath.Join(groupPath, entry.Name())
		if strings.HasSuffix(entry.Name(), ".work") || strings.HasSuffix(entry.Name(), ".releases") {
			continue
		}
		if !isBareGitRepository(repoPath) {
			logger.Warning("Directory %q is not a bare git repository, skipping", repoPath)
			continue
		}
		repoType, upstreamSource := repoUpstreamType(repoPath)
		info := &repositoryInfo{
			name:  entry.Name(),
			group: groupName,
			path:  repoPath,
		}
		switch repoType {
		case "fork":
			info.forkSource = upstreamSource
		case "mirror":
			info.mirrorSource = upstreamSource
		}
		n.loadRepositoryPermissions(info)
		group.repositories[entry.Name()] = info
		loaded++
	}
	logger.Verbose("Loaded %d repositories for group %q", loaded, groupName)
}

// updateGroupPermissions clears and re-parses the group perm lists from
// <groupPath>.allowed and config [access], mirroring
// update_group_permissions (server.py). The .allowed file is
// executable-aware (run as subprocess when X_OK). Config [access] entries
// are appended on top of the .allowed entries.
func (n *reticulumGitNode) updateGroupPermissions(groupName string) {
	group, ok := n.groups[groupName]
	if !ok {
		return
	}
	group.perms = permissionLists{}

	allowedPath := group.path + ".allowed"
	if isFile(allowedPath) {
		input, err := readAllowedInput(allowedPath)
		if err == nil {
			group.perms = n.permissionsFromAllowedInput(input)
		}
	}

	if n.config != nil && len(n.config.access) > 0 {
		for _, entry := range n.config.access[groupName] {
			perm, target := n.parsePermissionLine(entry)
			if perm == 0 || target == nil {
				continue
			}
			applyPermissionEntry(&group.perms, perm, target)
		}
	}
}

// loadRepositoryPermissions parses <repoPath>.allowed into repo.perms,
// mirroring the per-repo .allowed loading in load_repository (server.py).
// The .allowed file is executable-aware (run as subprocess when X_OK).
func (n *reticulumGitNode) loadRepositoryPermissions(repo *repositoryInfo) {
	repo.perms = permissionLists{}
	allowedPath := repo.path + ".allowed"
	if isFile(allowedPath) {
		input, err := readAllowedInput(allowedPath)
		if err == nil {
			repo.perms = n.permissionsFromAllowedInput(input)
		}
	}
}

// loadAliasesAndBlocked populates identityAliases and blockedIdentities from
// the config [aliases] and [rngit] blocked_identities sections, mirroring
// __apply_config (server.py). Aliases are resolved before blocking.
func (n *reticulumGitNode) loadAliasesAndBlocked(cfg *nodeConfig) {
	if cfg == nil {
		return
	}
	for alias, hexHash := range cfg.aliases {
		if isAllTargetMnemonic(alias) {
			continue
		}
		if len(hexHash) != rns.TruncatedHashLength/8*2 {
			continue
		}
		if _, err := hex.DecodeString(hexHash); err != nil {
			continue
		}
		if _, exists := n.identityAliases[alias]; exists {
			continue
		}
		n.identityAliases[alias] = hexHash
	}
	for _, hexHash := range cfg.blockedIdentities {
		resolved := n.resolveIdentityAlias(hexHash)
		if len(resolved) != rns.TruncatedHashLength/8*2 {
			continue
		}
		if _, err := hex.DecodeString(resolved); err != nil {
			continue
		}
		n.blockedIdentities[resolved] = true
	}
}

// registerRequestHandlers installs the /git/list and /git/create handlers and
// stubs the remaining paths, mirroring register_request_handlers (server.py).
func (n *reticulumGitNode) registerRequestHandlers(logger *rns.Logger) {
	n.destination.RegisterRequestHandler(pathList, n.handleList, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathCreate, n.handleCreate, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathFetch, n.handleFetch, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathPush, n.handlePush, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathDelete, n.handleDelete, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathFork, n.handleFork, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathSync, n.handleSync, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathMirror, n.handleMirror, rns.AllowAll, nil, false)
	// Stub handlers for follow-up tasks.
	n.destination.RegisterRequestHandler(pathRelease, n.handleRelease, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathWork, n.handleWork, rns.AllowAll, nil, false)
	n.destination.RegisterRequestHandler(pathPerms, n.handlePerms, rns.AllowAll, nil, false)
}

// stubHandler returns a request handler that logs and rejects an
// unimplemented path with RES_INVALID_REQ. Release/work/perms are
// follow-up tasks.
func stubHandler(path string, logger *rns.Logger) func(string, []byte, []byte, []byte, *rns.Identity, time.Time) any {
	return func(p string, _ []byte, _ []byte, _ []byte, remoteIdentity *rns.Identity, _ time.Time) any {
		logger.Warning("Handler %q not yet implemented (remote %v)", p, remoteIdentity)
		return []byte{resInvalidReq}
	}
}

// remoteConnected is the link-established callback, mirroring
// remote_connected (server.py).
func (n *reticulumGitNode) remoteConnected(link *rns.Link, logger *rns.Logger) {
	logger.Debug("Peer connected to repositories destination")
	link.SetLinkClosedCallback(func(l *rns.Link) {
		logger.Debug("Peer disconnected from repositories destination")
	})
}

// announce announces the repositories destination, mirroring announce
// (server.py).
func (n *reticulumGitNode) announce(logger *rns.Logger) {
	logger.Verbose("Announcing repositories destination")
	if err := n.destination.Announce(nil); err != nil {
		logger.Warning("Announce failed: %v", err)
	}
}

// serve runs the node serve loop: announce, register handlers, and wait for
// a termination signal. Mirrors program_setup + start (server.py).
func (n *reticulumGitNode) serve(ts rns.Transport, logger *rns.Logger) error {
	destination, err := rns.NewDestination(ts, n.identity, rns.DestinationIn, rns.DestinationSingle, appName, repoAspect)
	if err != nil {
		return fmt.Errorf("could not create repositories destination: %w", err)
	}
	n.destination = destination
	destination.SetLinkEstablishedCallback(func(link *rns.Link) {
		n.remoteConnected(link, logger)
	})
	n.registerRequestHandlers(logger)

	logger.Notice("Reticulum Git Node listening on %v", rns.PrettyHex(destination.Hash))

	n.announce(logger)
	n.shouldRun = true

	// Optionally start the nomadnet-compatible page node (server.py:2063),
	// a separate "nomadnetwork.node" destination that serves repository
	// browsing pages over the micron page protocol.
	if n.config.serveNomadnet {
		if err := n.startPageServer(ts, logger); err != nil {
			return err
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Periodic announce if configured.
	stopAnnounce := make(chan struct{})
	if n.announceInterval > 0 {
		go func() {
			ticker := time.NewTicker(n.announceInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stopAnnounce:
					return
				case <-ticker.C:
					n.announce(logger)
				}
			}
		}()
	}
	defer close(stopAnnounce)

	<-sigCh
	n.shouldRun = false
	if n.pageServer != nil {
		n.pageServer.shouldRun.Store(false)
	}
	logger.Info("Shutting down")
	return nil
}

// startPageServer creates the "nomadnetwork.node" destination, builds the
// pageNode, registers its request handlers, announces, and starts its jobs
// loop, mirroring NomadNetworkNode.__init__ + start (pages.py:168-176,
// server.py:2063). It is called from serve when [pages] serve_nomadnet is
// enabled.
func (n *reticulumGitNode) startPageServer(ts rns.Transport, logger *rns.Logger) error {
	pageDest, err := rns.NewDestination(ts, n.identity, rns.DestinationIn, rns.DestinationSingle, pageAppName, "node")
	if err != nil {
		return fmt.Errorf("could not create nomadnet destination: %w", err)
	}
	pn, err := newPageNode(n)
	if err != nil {
		return fmt.Errorf("could not create page node: %w", err)
	}
	pn.destination = pageDest
	pageDest.SetLinkEstablishedCallback(func(link *rns.Link) {
		logger.Debug("Peer connected to nomadnet destination")
		link.SetLinkClosedCallback(func(l *rns.Link) {
			logger.Debug("Peer disconnected from nomadnet destination")
		})
	})
	pageDest.SetDefaultAppData(pn.getAnnounceAppData())
	pn.registerRequestHandlers()
	n.pageServer = pn

	logger.Notice("Git Nomad Network Node listening on %v", rns.PrettyHex(pageDest.Hash))

	pn.announce()
	pn.shouldRun.Store(true)
	go pn.jobs()
	return nil
}

// handleList is the /git/list request handler, mirroring handle_list
// (server.py). The response is a byte slice: result-code byte prefix +
// ref-list text.
func (n *reticulumGitNode) handleList(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	if remoteIdentity == nil {
		return []byte{resDisallowed}
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return []byte{resInvalidReq}
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPathVal, ok := getMapValue(m, idxRepository)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPath, ok := repoPathVal.(string)
	if !ok {
		return []byte{resInvalidReq}
	}

	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	if groupName == "" || repositoryName == "" {
		return []byte{resInvalidReq}
	}

	forPushVal, _ := getMapValue(m, "for_push")
	forPush, _ := forPushVal.(bool)

	readAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRead)
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	access := readAccess
	if forPush {
		access = writeAccess
	}
	if !access {
		if readAccess {
			return append([]byte{resNotFound}, []byte("Not allowed")...)
		}
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	repo, ok := n.lookupRepository(groupName, repositoryName)
	if !ok {
		return []byte{resNotFound}
	}

	return listRepositoryRefs(repo.path)
}

// listRepositoryRefs runs git for-each-ref and builds the ref-list response,
// mirroring the body of handle_list (server.py).
func listRepositoryRefs(repoPath string) []byte {
	headRef := readHeadSymref(repoPath)

	cmd := exec.Command("git", "for-each-ref", "--format", "%(objectname) %(refname)")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return []byte{resRemoteFail}
	}
	return formatRefList(string(out), headRef)
}

// handleCreate is the /git/create request handler, mirroring handle_create
// (server.py). It creates a bare git repo in the configured group directory.
func (n *reticulumGitNode) handleCreate(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	if remoteIdentity == nil {
		return []byte{resDisallowed}
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return []byte{resInvalidReq}
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPathVal, ok := getMapValue(m, idxRepository)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPath, ok := repoPathVal.(string)
	if !ok {
		return []byte{resInvalidReq}
	}

	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	if groupName == "" || repositoryName == "" {
		return []byte{resInvalidReq}
	}
	group, ok := n.groups[groupName]
	if !ok {
		return []byte{resNotFound}
	}
	if _, err := os.Stat(group.path); err != nil {
		return []byte{resNotFound}
	}

	readAccess := n.resolveGroupPermission(remoteIdentity, groupName, permRead)
	createAccess := n.resolveGroupPermission(remoteIdentity, groupName, permCreate)
	if !createAccess {
		if readAccess {
			return append([]byte{resDisallowed}, []byte("Not allowed")...)
		}
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	repositoryPath := filepath.Join(group.path, repositoryName)
	if _, exists := group.repositories[repositoryName]; exists {
		return []byte{resDisallowed}
	}
	if _, err := os.Stat(repositoryPath); err == nil {
		return []byte{resDisallowed}
	}

	if err := os.Mkdir(repositoryPath, 0o755); err != nil {
		return []byte{resRemoteFail}
	}
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = repositoryPath
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(repositoryPath)
		_ = out
		return []byte{resRemoteFail}
	}

	creatorHex := hex.EncodeToString(remoteIdentity.Hash)
	if err := writeRepoCreatePermissions(repositoryPath, creatorHex); err != nil {
		_ = os.RemoveAll(repositoryPath)
		return []byte{resRemoteFail}
	}

	info := &repositoryInfo{
		name:  repositoryName,
		group: groupName,
		path:  repositoryPath,
	}
	n.loadRepositoryPermissions(info)
	group.repositories[repositoryName] = info
	return []byte{resOK}
}

// lookupRepository finds a registered repository by group and name.
func (n *reticulumGitNode) lookupRepository(groupName, repositoryName string) (*repositoryInfo, bool) {
	group, ok := n.groups[groupName]
	if !ok {
		return nil, false
	}
	repo, ok := group.repositories[repositoryName]
	return repo, ok
}

// parseRequestRepositoryPath splits "group/repo" into its two components,
// mirroring parse_request_repository_path (server.py). Returns empty strings
// on any validation failure.
func parseRequestRepositoryPath(path string) (string, string) {
	components := strings.Split(path, "/")
	if len(components) != 2 {
		return "", ""
	}
	group := components[0]
	repositoryName := components[1]
	if len(group) > requestPathLimit || len(repositoryName) > requestPathLimit {
		return "", ""
	}
	return group, repositoryName
}

// readHeadSymref reads the HEAD symref from a bare repo, defaulting to
// "master" when missing or not a symref, mirroring handle_list (server.py).
func readHeadSymref(repoPath string) string {
	headPath := filepath.Join(repoPath, "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return "master"
	}
	const prefix = "ref: "
	if strings.HasPrefix(string(data), prefix) {
		return strings.TrimSpace(string(data[len(prefix):]))
	}
	return "master"
}

// formatRefList builds the /git/list response body from git for-each-ref
// output and the HEAD symref, mirroring handle_list (server.py). The result
// is prefixed with RES_OK (0x00), contains deduplicated ref lines, and ends
// with "@<headRef> HEAD\n".
func formatRefList(eachRefOutput, headRef string) []byte {
	seen := make(map[string]bool)
	var lines []string
	for line := range strings.SplitSeq(eachRefOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, " ", 2)
		if len(parts) != 2 {
			continue
		}
		refName := parts[1]
		if seen[refName] {
			continue
		}
		seen[refName] = true
		lines = append(lines, trimmed)
	}
	var sb strings.Builder
	sb.WriteByte(resOK)
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.WriteString("@")
	sb.WriteString(headRef)
	sb.WriteString(" HEAD\n")
	return []byte(sb.String())
}

// parseListResponse splits a /git/list response into its result-code byte and
// text payload. Returns ok=false when the response is too short to contain a
// result code.
func parseListResponse(response []byte) (code byte, payload string, ok bool) {
	if len(response) == 0 {
		return 0, "", false
	}
	return response[0], string(response[1:]), true
}

// getMapValue fetches a value from an unpacked msgpack map, coercing integer
// keys so that IDX_REPOSITORY (0x00) matches the int64/uint64 key produced by
// UnpackPreserveBinMapKeys.
func getMapValue(m map[any]any, key any) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	switch k := key.(type) {
	case int:
		if v, ok := m[int64(k)]; ok {
			return v, true
		}
		if v, ok := m[uint64(k)]; ok {
			return v, true
		}
	case int64:
		if v, ok := m[uint64(k)]; ok {
			return v, true
		}
		if v, ok := m[int(k)]; ok {
			return v, true
		}
	case uint64:
		if v, ok := m[int64(k)]; ok {
			return v, true
		}
	}
	return nil, false
}

// isBareGitRepository reports whether path is a bare git repository, mirroring
// __is_git_repository + __is_bare_repository (server.py).
func isBareGitRepository(path string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	if strings.TrimSpace(string(out)) != "." {
		return false
	}
	cmd = exec.Command("git", "config", "--bool", "core.bare")
	cmd.Dir = path
	out, err = cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// ensureGit reports whether the git command is available, mirroring
// _ensure_git (server.py).
func ensureGit() bool {
	cmd := exec.Command("git", "--version")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// fetchRefEntry mirrors a single entry in the Python fetch request "refs"
// list: {"sha": <sha>, "ref": <ref>, "have": <local_sha_or_empty>}. The
// server consumes only "ref" and "have"; "sha" is carried for client-side
// bookkeeping parity with client.py process_fetch_queue.
type fetchRefEntry struct {
	sha  string
	ref  string
	have string
}

// handleFetch is the /git/fetch request handler, mirroring handle_fetch
// (server.py). The client sends wanted refs plus refs it already has (for
// thin-bundle exclusion). The server runs `git bundle create` with `--not`
// exclusions for the have-refs that exist in the repo, and returns the bundle
// as the response body: a leading resOK byte followed by the bundle bytes.
// An empty bundle (all objects already on the client) returns resOK with no
// bundle data. The link layer transparently streams large responses as a
// Resource.
func (n *reticulumGitNode) handleFetch(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	if remoteIdentity == nil {
		return []byte{resDisallowed}
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return []byte{resInvalidReq}
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPathVal, ok := getMapValue(m, idxRepository)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPath, ok := repoPathVal.(string)
	if !ok {
		return []byte{resInvalidReq}
	}
	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	if groupName == "" || repositoryName == "" {
		return []byte{resInvalidReq}
	}
	readAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRead)
	if !readAccess {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	repo, ok := n.lookupRepository(groupName, repositoryName)
	if !ok {
		return []byte{resNotFound}
	}

	refsVal, ok := getMapValue(m, "refs")
	if !ok {
		return []byte{resInvalidReq}
	}
	refs, ok := parseFetchRefs(refsVal)
	if !ok || len(refs) == 0 {
		return []byte{resInvalidReq}
	}
	for _, r := range refs {
		if sanRef(r.ref) == "" {
			return []byte{resInvalidReq}
		}
		if r.have != "" && sanSHA(r.have) == "" {
			return []byte{resInvalidReq}
		}
	}

	var haves []string
	if haveVal, ok := getMapValue(m, "have"); ok {
		haves, ok = parseStringList(haveVal)
		if !ok {
			return []byte{resInvalidReq}
		}
		for _, sha := range haves {
			if sanSHA(sha) == "" {
				return []byte{resInvalidReq}
			}
		}
	}

	// Build the exclusion list: per-ref haves and global haves that the
	// server actually has objects for (mirroring handle_fetch's cat-file
	// existence check before appending ^<sha>).
	excluded := make([]string, 0, len(refs)+len(haves))
	for _, r := range refs {
		if r.have != "" && objectExists(repo.path, r.have) {
			excluded = append(excluded, r.have)
		}
	}
	for _, sha := range haves {
		if objectExists(repo.path, sha) {
			excluded = append(excluded, sha)
		}
	}

	tmpDir, err := os.MkdirTemp("", "gorngit-fetch-")
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	bundlePath := filepath.Join(tmpDir, "fetch.bundle")

	args := buildBundleCreateArgs(bundlePath, refs, excluded)
	cmd := exec.Command("git", args...)
	cmd.Dir = repo.path
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "empty bundle") {
			return []byte{resOK}
		}
		return append([]byte{resRemoteFail}, []byte("Could not fetch refs")...)
	}

	bundleData, err := os.ReadFile(bundlePath)
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	result := make([]byte, 0, 1+len(bundleData))
	result = append(result, resOK)
	result = append(result, bundleData...)
	return result
}

// handlePush is the /git/push request handler, mirroring handle_push
// (server.py). The client sends bundle bytes in the "bundle" key along with
// local_ref, remote_ref, and force. The server writes the bundle to a temp
// file, runs `git bundle verify`, then `git fetch <bundle> <local>:<remote>`
// (with --force when requested), updating the bare repo's ref. Returns resOK
// on success or resRemoteFail + message on failure.
func (n *reticulumGitNode) handlePush(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	if remoteIdentity == nil {
		return []byte{resDisallowed}
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return []byte{resInvalidReq}
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPathVal, ok := getMapValue(m, idxRepository)
	if !ok {
		return []byte{resInvalidReq}
	}
	repoPath, ok := repoPathVal.(string)
	if !ok {
		return []byte{resInvalidReq}
	}
	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	if groupName == "" || repositoryName == "" {
		return []byte{resInvalidReq}
	}
	readAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRead)
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	if !writeAccess {
		if readAccess {
			return append([]byte{resDisallowed}, []byte("Not allowed")...)
		}
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	repo, ok := n.lookupRepository(groupName, repositoryName)
	if !ok {
		return []byte{resNotFound}
	}

	localRefVal, _ := getMapValue(m, "local_ref")
	localRef, _ := localRefVal.(string)
	localRef = sanRef(localRef)
	remoteRefVal, _ := getMapValue(m, "remote_ref")
	remoteRef, _ := remoteRefVal.(string)
	remoteRef = sanRef(remoteRef)
	forceVal, _ := getMapValue(m, "force")
	force, _ := forceVal.(bool)

	bundleVal, ok := getMapValue(m, "bundle")
	if !ok {
		return []byte{resInvalidReq}
	}
	bundleData, ok := parseBundleData(bundleVal)
	if !ok {
		return []byte{resInvalidReq}
	}
	if localRef == "" || remoteRef == "" {
		return []byte{resInvalidReq}
	}

	tmpDir, err := os.MkdirTemp("", "gorngit-push-")
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	bundlePath := filepath.Join(tmpDir, "push.bundle")
	if err := os.WriteFile(bundlePath, bundleData, 0o644); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}

	verifyCmd := exec.Command("git", "bundle", "verify", bundlePath)
	verifyCmd.Dir = repo.path
	if out, err := verifyCmd.CombinedOutput(); err != nil {
		_ = out
		return append([]byte{resRemoteFail}, []byte("Could not verify bundle")...)
	}

	fetchArgs := []string{"fetch", bundlePath, localRef + ":" + remoteRef}
	if force {
		fetchArgs = append(fetchArgs, "--force")
	}
	fetchCmd := exec.Command("git", fetchArgs...)
	fetchCmd.Dir = repo.path
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		_ = out
		return append([]byte{resRemoteFail}, []byte("Could not fetch from bundle")...)
	}

	return []byte{resOK}
}

// parseFetchRefs normalizes the unpacked "refs" list from a fetch request
// into a slice of fetchRefEntry. Each element must be a map with at least a
// "ref" string key; "sha" and "have" are optional string keys. Returns
// ok=false when the value is not a list or any element is malformed.
func parseFetchRefs(val any) ([]fetchRefEntry, bool) {
	if val == nil {
		return nil, false
	}
	list, ok := val.([]any)
	if !ok {
		return nil, false
	}
	refs := make([]fetchRefEntry, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[any]any)
		if !ok {
			return nil, false
		}
		refVal, ok := m["ref"]
		if !ok {
			return nil, false
		}
		refStr, ok := refVal.(string)
		if !ok {
			return nil, false
		}
		entry := fetchRefEntry{ref: refStr}
		if shaVal, ok := m["sha"]; ok {
			if shaStr, ok := shaVal.(string); ok {
				entry.sha = shaStr
			}
		}
		if haveVal, ok := m["have"]; ok {
			if haveStr, ok := haveVal.(string); ok {
				entry.have = haveStr
			}
		}
		refs = append(refs, entry)
	}
	return refs, true
}

// parseStringList normalizes an unpacked msgpack array of strings. nil and
// empty arrays are accepted (returning an empty slice + ok=true); a non-list
// value or any non-string element yields ok=false.
func parseStringList(val any) ([]string, bool) {
	if val == nil {
		return nil, true
	}
	list, ok := val.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// parseBundleData extracts the "bundle" field from a push request, accepting
// either a msgpack bin ([]byte) or a str (coerced to bytes), mirroring
// handle_push's `isinstance(bundle_data, str): bundle_data.encode`.
func parseBundleData(val any) ([]byte, bool) {
	if val == nil {
		return nil, false
	}
	if b, ok := val.([]byte); ok {
		return b, true
	}
	if s, ok := val.(string); ok {
		return []byte(s), true
	}
	return nil, false
}

// parseFetchResponse splits a /git/fetch response body into its result-code
// byte and payload. For resOK the payload is the bundle data (possibly empty
// for an empty bundle); for error codes the payload is the message string.
// Returns ok=false when the response is too short to contain a result code.
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

// buildBundleCreateArgs constructs the argument list for `git bundle create`
// from the wanted refs and the exclusion (have) SHAs, mirroring the execv
// construction in handle_fetch (server.py). Each wanted ref is appended
// verbatim, and each excluded SHA is prefixed with "^" for git's revision
// exclusion syntax.
func buildBundleCreateArgs(bundlePath string, refs []fetchRefEntry, excluded []string) []string {
	args := []string{"bundle", "create", "--no-progress", bundlePath}
	for _, r := range refs {
		args = append(args, r.ref)
	}
	for _, sha := range excluded {
		args = append(args, "^"+sha)
	}
	return args
}

// buildFetchRequestMap constructs the msgpack request map for a /git/fetch
// request, mirroring client.py process_fetch_queue's request_data. The "refs"
// value is a list of string maps with "sha", "ref", and optional "have" keys;
// the "have" key (global have list) is included only when non-empty.
func buildFetchRequestMap(repoPath string, refs []fetchRefEntry, haves []string) map[any]any {
	refsList := make([]map[string]string, 0, len(refs))
	for _, r := range refs {
		entry := map[string]string{"sha": r.sha, "ref": r.ref}
		if r.have != "" {
			entry["have"] = r.have
		}
		refsList = append(refsList, entry)
	}
	m := map[any]any{
		int64(idxRepository): repoPath,
		"refs":               refsList,
	}
	if len(haves) > 0 {
		m["have"] = haves
	}
	return m
}

// buildPushRequestMap constructs the msgpack request map for a /git/push
// request, mirroring client.py process_push_queue's request_data for the
// bundle path: idxRepository, local_ref, remote_ref, force, and bundle bytes.
func buildPushRequestMap(repoPath, localRef, remoteRef string, force bool, bundleData []byte) map[any]any {
	return map[any]any{
		int64(idxRepository): repoPath,
		"local_ref":          localRef,
		"remote_ref":         remoteRef,
		"force":              force,
		"bundle":             bundleData,
	}
}

// objectExists reports whether the given SHA names an object in the repository
// at repoPath, mirroring handle_fetch's `git cat-file -t <sha>` existence
// check.
func objectExists(repoPath, sha string) bool {
	cmd := exec.Command("git", "cat-file", "-t", sha)
	cmd.Dir = repoPath
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// cloneProtos lists the URL schemes permitted as a fork/mirror source,
// mirroring CLONE_PROTOS (server.py).
var cloneProtos = map[string]bool{
	"rns":   true,
	"http":  true,
	"https": true,
	"ssh":   true,
}

// sourceSchemeAllowed reports whether sourceURL uses a scheme permitted for
// fork/mirror sources, mirroring _handle_remote_clone's
// `source_url.lower().split("://")[0] in self.CLONE_PROTOS` check (server.py).
// A URL with no "://" delimiter yields an empty scheme and is rejected.
func sourceSchemeAllowed(sourceURL string) bool {
	idx := strings.Index(strings.ToLower(sourceURL), "://")
	if idx < 0 {
		return false
	}
	return cloneProtos[strings.ToLower(sourceURL[:idx])]
}

// gitConfigGet reads a single git config value from the repository at repoPath,
// mirroring the `git config <key>` invocations in __is_fork / __is_mirror
// (server.py). Returns the trimmed value, or the empty string when the key is
// unset or git fails.
func gitConfigGet(repoPath, key string) string {
	cmd := exec.Command("git", "config", key)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoUpstreamType reads the rngit upstream metadata from a bare repository's
// git config, mirroring __is_fork / __is_mirror (server.py). It returns
// repoType ("fork", "mirror", or "") and the upstream source URL (empty when
// the repo is neither a fork nor a mirror, or when the source is unset).
func repoUpstreamType(repoPath string) (string, string) {
	repoType := gitConfigGet(repoPath, "repository.rngit.type")
	if repoType != "fork" && repoType != "mirror" {
		return "", ""
	}
	return repoType, gitConfigGet(repoPath, "repository.rngit.upstream.source")
}

// handleFork is the /git/fork request handler, mirroring handle_fork
// (server.py). It delegates to handleRemoteClone with repoType "fork".
func (n *reticulumGitNode) handleFork(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	return n.handleRemoteClone(path, data, requestID, linkID, remoteIdentity, requestedAt, "fork")
}

// handleMirror is the /git/mirror request handler, mirroring handle_mirror
// (server.py). It delegates to handleRemoteClone with repoType "mirror".
func (n *reticulumGitNode) handleMirror(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	return n.handleRemoteClone(path, data, requestID, linkID, remoteIdentity, requestedAt, "mirror")
}

// handleRemoteClone is the shared fork/mirror handler, mirroring
// _handle_remote_clone (server.py). It creates a bare clone of sourceURL at the
// requested group/repo path, records the rngit upstream metadata, and registers
// the repository. The clone is built in a temporary directory within the group
// path and renamed into place on success, so a failure never leaves a partial
// repository at the final path.
func (n *reticulumGitNode) handleRemoteClone(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time, repoType string) any {
	if repoType != "mirror" && repoType != "fork" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
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

	sourceVal, ok := getMapValue(m, "source")
	if !ok {
		return append([]byte{resInvalidReq}, []byte("No source specified")...)
	}
	sourceURL, ok := sourceVal.(string)
	if !ok || sourceURL == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid source URL")...)
	}
	if !sourceSchemeAllowed(sourceURL) {
		return append([]byte{resDisallowed}, []byte("Prohibited source URL")...)
	}

	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	if groupName == "" || repositoryName == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	group, ok := n.groups[groupName]
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}
	if _, err := os.Stat(group.path); err != nil {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	readAccess := n.resolveGroupPermission(remoteIdentity, groupName, permRead)
	createAccess := n.resolveGroupPermission(remoteIdentity, groupName, permCreate)
	if !createAccess {
		if readAccess {
			return append([]byte{resDisallowed}, []byte("Not allowed")...)
		}
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	finalRepositoryPath := filepath.Join(group.path, repositoryName)
	if _, exists := group.repositories[repositoryName]; exists {
		return append([]byte{resDisallowed}, []byte("Repository already exists")...)
	}
	if _, err := os.Stat(finalRepositoryPath); err == nil {
		return append([]byte{resDisallowed}, []byte("Repository already exists")...)
	}

	// Build the clone inside the group directory so the final rename is a
	// same-filesystem move (os.Rename does not cross devices).
	tmpDir, err := os.MkdirTemp(group.path, ".rngit-clone-")
	if err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	repoTempPath := filepath.Join(tmpDir, repositoryName)
	if err := os.Mkdir(repoTempPath, 0o755); err != nil {
		return append([]byte{resRemoteFail}, []byte("Remote error")...)
	}

	initCmd := exec.Command("git", "init", "--bare")
	initCmd.Dir = repoTempPath
	if out, err := initCmd.CombinedOutput(); err != nil {
		_ = out
		return append([]byte{resRemoteFail}, []byte("Failed to initialize repository")...)
	}

	fetchCmd := exec.Command("git", "fetch", sourceURL, "+refs/*:refs/*")
	fetchCmd.Dir = repoTempPath
	var fetchStderr bytes.Buffer
	fetchCmd.Stderr = &fetchStderr
	if err := fetchCmd.Run(); err != nil {
		msg := "Failed to fetch from source: " + strings.TrimSpace(fetchStderr.String())
		return append([]byte{resRemoteFail}, []byte(msg)...)
	}

	// Best-effort HEAD update; failures only log and do not abort the clone,
	// mirroring _handle_remote_clone's try/except around
	// __update_head_to_source_default (server.py).
	_ = updateHeadToSourceDefault(repoTempPath, sourceURL)
	// Continue; HEAD defaults to the existing symref.

	typeCmd := exec.Command("git", "config", "repository.rngit.type", repoType)
	typeCmd.Dir = repoTempPath
	if out, err := typeCmd.CombinedOutput(); err != nil {
		_ = out
		return append([]byte{resRemoteFail}, []byte("Failed to configure repository type")...)
	}
	srcCmd := exec.Command("git", "config", "repository.rngit.upstream.source", sourceURL)
	srcCmd.Dir = repoTempPath
	if out, err := srcCmd.CombinedOutput(); err != nil {
		_ = out
		return append([]byte{resRemoteFail}, []byte("Failed to configure repository upstream source")...)
	}
	if !setMirrorSynced(repoTempPath) {
		return append([]byte{resRemoteFail}, []byte("Failed to configure repository type")...)
	}

	// Write the initial .allowed file at the final path, making the creator
	// sole admin, mirroring _handle_remote_clone (server.py). Written at
	// finalRepositoryPath before the rename so the .allowed survives the move.
	creatorHex := hex.EncodeToString(remoteIdentity.Hash)
	if err := writeRepoCreatePermissions(finalRepositoryPath, creatorHex); err != nil {
		return append([]byte{resRemoteFail}, []byte("Could not initialize repository")...)
	}

	if err := os.Rename(repoTempPath, finalRepositoryPath); err != nil {
		return append([]byte{resRemoteFail}, []byte("Could not write repository")...)
	}

	info := &repositoryInfo{
		name:  repositoryName,
		group: groupName,
		path:  finalRepositoryPath,
	}
	if repoType == "fork" {
		info.forkSource = sourceURL
	} else {
		info.mirrorSource = sourceURL
	}
	n.loadRepositoryPermissions(info)
	group.repositories[repositoryName] = info

	return []byte{resOK}
}

// handleSync is the /git/sync request handler, mirroring handle_sync
// (server.py). It re-fetches a mirror or fork from its recorded upstream source.
func (n *reticulumGitNode) handleSync(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
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
	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	if groupName == "" || repositoryName == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}

	readAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRead)
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	if !writeAccess {
		if readAccess {
			return append([]byte{resDisallowed}, []byte("Not allowed")...)
		}
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	repo, ok := n.lookupRepository(groupName, repositoryName)
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	if repo.mirrorSource != "" {
		if err := syncMirror(repo.path, repo.mirrorSource); err != nil {
			return append([]byte{resRemoteFail}, []byte("Mirror sync failed")...)
		}
		return []byte{resOK}
	}
	if repo.forkSource != "" {
		if err := syncFork(repo.path, repo.forkSource); err != nil {
			return append([]byte{resRemoteFail}, []byte("Fork sync failed")...)
		}
		return []byte{resOK}
	}
	return append([]byte{resInvalidReq}, []byte("Repository is neither fork nor mirror")...)
}

// handleDelete is the /git/delete request handler, mirroring handle_delete
// (server.py). It deletes a single ref from the named repository via
// `git update-ref -d <ref>`.
func (n *reticulumGitNode) handleDelete(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
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
	groupName, repositoryName := parseRequestRepositoryPath(repoPath)
	if groupName == "" || repositoryName == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}

	readAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permRead)
	writeAccess := n.resolvePermission(remoteIdentity, groupName, repositoryName, permWrite)
	if !writeAccess {
		if readAccess {
			return append([]byte{resDisallowed}, []byte("Not allowed")...)
		}
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	repo, ok := n.lookupRepository(groupName, repositoryName)
	if !ok {
		return append([]byte{resNotFound}, []byte("Not found")...)
	}

	refVal, _ := getMapValue(m, "ref")
	refStr, _ := refVal.(string)
	refToDelete := sanRef(refStr)
	if refToDelete == "" {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}
	if !strings.HasPrefix(refToDelete, "refs/") {
		return append([]byte{resInvalidReq}, []byte("Invalid request")...)
	}

	cmd := exec.Command("git", "update-ref", "-d", refToDelete)
	cmd.Dir = repo.path
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = stderr.String()
		return append([]byte{resRemoteFail}, []byte("Could not delete ref")...)
	}

	// push_succeeded (stats/notification) is deferred.
	return []byte{resOK}
}

// syncMirror re-fetches a mirror from its upstream source, updates HEAD to the
// source default, and records the sync timestamp, mirroring __sync_mirror
// (server.py). The HEAD update is best-effort, mirroring the Python try/except.
func syncMirror(repoPath, sourceURL string) error {
	if err := gitFetchAll(repoPath, sourceURL); err != nil {
		return err
	}
	_ = updateHeadToSourceDefault(repoPath, sourceURL)
	if !setMirrorSynced(repoPath) {
		return nil
	}
	return nil
}

// syncFork re-fetches a fork from its upstream source and records the sync
// timestamp, mirroring __sync_fork (server.py). It intentionally does not reset
// HEAD, matching the Python comment about letting the fork maintainer decide.
func syncFork(repoPath, sourceURL string) error {
	if err := gitFetchAll(repoPath, sourceURL); err != nil {
		return err
	}
	_ = setMirrorSynced(repoPath)
	return nil
}

// gitFetchAll runs `git fetch <source> +refs/*:refs/*` in repoPath, mirroring
// the fetch invocation in __sync_mirror / __sync_fork / _handle_remote_clone
// (server.py).
func gitFetchAll(repoPath, sourceURL string) error {
	cmd := exec.Command("git", "fetch", sourceURL, "+refs/*:refs/*")
	cmd.Dir = repoPath
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fetch from %s failed: %s: %w", sourceURL, strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// setMirrorSynced records the current unix timestamp in
// repository.rngit.upstream.sync, mirroring __set_mirror_synced (server.py).
// Returns true on success.
func setMirrorSynced(repoPath string) bool {
	cmd := exec.Command("git", "config", "repository.rngit.upstream.sync", strconv.FormatInt(time.Now().Unix(), 10))
	cmd.Dir = repoPath
	return cmd.Run() == nil
}

// updateHeadToSourceDefault sets the bare repo HEAD symref to the source's
// default branch, mirroring __update_head_to_source_default (server.py). It
// queries `git ls-remote --symref <source> HEAD`, falls back to the first
// local branch when the remote default is unavailable, and finally runs
// `git symbolic-ref HEAD <branch>`. Errors are returned but the caller treats
// them as best-effort (non-fatal), matching the Python try/except usage.
func updateHeadToSourceDefault(repoPath, sourceURL string) error {
	targetBranch := ""
	lsCmd := exec.Command("git", "ls-remote", "--symref", sourceURL, "HEAD")
	var lsOut, lsErr bytes.Buffer
	lsCmd.Stdout = &lsOut
	lsCmd.Stderr = &lsErr
	if err := lsCmd.Run(); err == nil {
		targetBranch = parseLsRemoteSymref(lsOut.String())
	}
	if targetBranch != "" {
		check := exec.Command("git", "show-ref", "--verify", "--quiet", targetBranch)
		check.Dir = repoPath
		if check.Run() != nil {
			targetBranch = ""
		}
	}
	if targetBranch == "" {
		eachCmd := exec.Command("git", "for-each-ref", "--format=%(refname:short)", "refs/heads", "--count=1")
		eachCmd.Dir = repoPath
		out, err := eachCmd.Output()
		if err != nil {
			return fmt.Errorf("could not determine fallback branch: %w", err)
		}
		name := strings.TrimSpace(string(out))
		if name == "" {
			return fmt.Errorf("no local branches available for HEAD fallback")
		}
		targetBranch = "refs/heads/" + name
	}
	symCmd := exec.Command("git", "symbolic-ref", "HEAD", targetBranch)
	symCmd.Dir = repoPath
	if out, err := symCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update HEAD to %s: %s: %w", targetBranch, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// parseLsRemoteSymref extracts the target branch from `git ls-remote --symref`
// output, mirroring the parsing loop in __update_head_to_source_default
// (server.py). It looks for a line beginning "ref: refs/heads/" whose
// tab-separated value is "HEAD" and returns the referenced branch
// (e.g. "refs/heads/main"). Returns the empty string when no such line exists.
func parseLsRemoteSymref(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.HasPrefix(line, "ref: refs/heads/") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 && strings.TrimSpace(parts[1]) == "HEAD" {
			return strings.TrimSpace(parts[0][len("ref: "):])
		}
	}
	return ""
}

// buildRemoteCloneRequestMap constructs the msgpack request map for a
// /git/fork or /git/mirror request, mirroring _remote_clone_operation's
// request_data (server.py): idxRepository plus the source URL string.
func buildRemoteCloneRequestMap(repoPath, sourceURL string) map[any]any {
	return map[any]any{
		int64(idxRepository): repoPath,
		"source":             sourceURL,
	}
}

// buildSyncRequestMap constructs the msgpack request map for a /git/sync
// request, mirroring sync_repository's request_data (server.py).
func buildSyncRequestMap(repoPath string) map[any]any {
	return map[any]any{
		int64(idxRepository): repoPath,
	}
}

// buildDeleteRequestMap constructs the msgpack request map for a /git/delete
// request, mirroring handle_delete's data map (server.py): idxRepository plus
// the ref to delete.
func buildDeleteRequestMap(repoPath, ref string) map[any]any {
	return map[any]any{
		int64(idxRepository): repoPath,
		"ref":                ref,
	}
}
