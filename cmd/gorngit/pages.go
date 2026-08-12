// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// pages.go implements the nomadnetwork page-node: a separate RNS
// destination (aspect "nomadnetwork.node") that serves repository browsing
// pages over the micron page protocol, mirroring NomadNetworkNode
// (RNS/Utilities/rngit/pages.py, rngit v1.4.2). The markdown->micron
// converter and syntax highlighter live in the micron package (already a
// byte-exact port); this file wires the page-node destination, its request
// handlers, and the accessible-group/repository lookups. The pure rendering
// helpers live in pages-render.go and the git subprocess plumbing in
// pages-git.go.

package main

import (
	"container/list"
	"sync"
	"sync/atomic"

	"github.com/gmlewis/go-reticulum/micron"
	"github.com/gmlewis/go-reticulum/rns"
)

// pageNode is the nomadnetwork page-node, mirroring NomadNetworkNode
// (pages.py:50). It owns a dedicated RNS destination (aspect "node" under
// app "nomadnetwork"), a markdown->micron converter, a syntax highlighter,
// and a thanks de-duplication deque. owner is the hosting reticulumGitNode
// (the git-protocol node), used for permission resolution, stats, and
// release/work data.
type pageNode struct {
	owner            *reticulumGitNode
	identity         *rns.Identity
	destination      *rns.Destination
	nodeName         string
	announceInterval int64 // nanoseconds; 0 disables periodic announce
	useNerdFonts     bool
	highlightSyntax  bool
	highlighter      *micron.Highlighter
	mdc              *micron.Converter
	thanksDeque      *list.List
	thanksMu         sync.Mutex
	nullIdent        *rns.Identity
	lastAnnounce     int64       // unix seconds of the last announce (pages.py:135)
	shouldRun        atomic.Bool // mirrors _should_run (pages.py:130)
}

// newPageNode constructs a pageNode owned by the given reticulumGitNode,
// mirroring NomadNetworkNode.__init__ (pages.py:126-176) minus the RNS
// destination creation (which requires a live transport and is performed by
// serve). The null identity is an identity built from 64 zero bytes, used
// for unauthenticated page requests (pages.py:136).
func newPageNode(owner *reticulumGitNode) (*pageNode, error) {
	hl := micron.NewHighlighter()
	mdc := micron.NewConverter(micron.WithMaxWidth(maxRenderWidth), micron.WithHighlighter(hl))
	nullIdent, err := rns.FromBytes(make([]byte, 64), nil)
	if err != nil {
		return nil, err
	}
	nerdFonts := true
	if owner.config != nil && owner.config.unicodeIcons {
		nerdFonts = false
	}
	return &pageNode{
		owner:            owner,
		identity:         owner.identity,
		nodeName:         owner.nodeName,
		announceInterval: int64(owner.announceInterval),
		useNerdFonts:     nerdFonts,
		highlightSyntax:  true,
		highlighter:      hl,
		mdc:              mdc,
		thanksDeque:      list.New(),
		nullIdent:        nullIdent,
	}, nil
}

// resolvePermission mirrors NomadNetworkNode.resolve_permission (pages.py:222)
// - the page protocol does not require authentication, so a missing remote
// is replaced with the null identity before delegating to the owner.
func (p *pageNode) resolvePermission(remoteIdentity *rns.Identity, groupName, repoName string, perm byte) bool {
	if remoteIdentity == nil {
		remoteIdentity = p.nullIdent
	}
	return p.owner.resolvePermission(remoteIdentity, groupName, repoName, perm)
}

// resolveDocPermission mirrors NomadNetworkNode.resolve_doc_permission
// (pages.py:228) with the same null-identity substitution.
func (p *pageNode) resolveDocPermission(remoteIdentity *rns.Identity, groupName, repoName string, docID int, perm byte) bool {
	if remoteIdentity == nil {
		remoteIdentity = p.nullIdent
	}
	return p.owner.resolveDocPermission(remoteIdentity, groupName, repoName, docID, perm)
}

// accessibleRepo is a resolved accessible repository, mirroring the dict
// returned by get_accessible_repository (pages.py:2735-2746).
type accessibleRepo struct {
	name   string
	group  string
	path   string
	fork   string
	mirror string
}

// getAccessibleGroups returns the groups the remote can read at least one
// repository of, mirroring get_accessible_groups (pages.py:2711-2720).
func (p *pageNode) getAccessibleGroups(remoteIdentity *rns.Identity) map[string]map[string]any {
	accessible := map[string]map[string]any{}
	for groupName, g := range p.owner.groups {
		repos := p.getAccessibleRepositories(remoteIdentity, groupName)
		if len(repos) == 0 {
			continue
		}
		accessible[groupName] = map[string]any{"path": g.path, "repositories": repos}
	}
	return accessible
}

// getAccessibleRepositories returns the readable repositories in a group,
// mirroring get_accessible_repositories (pages.py:2722-2733).
func (p *pageNode) getAccessibleRepositories(remoteIdentity *rns.Identity, groupName string) map[string]*accessibleRepo {
	g, ok := p.owner.groups[groupName]
	if !ok {
		return map[string]*accessibleRepo{}
	}
	out := map[string]*accessibleRepo{}
	for repoName, r := range g.repositories {
		if p.resolvePermission(remoteIdentity, groupName, repoName, permRead) {
			out[repoName] = &accessibleRepo{
				name: r.name, group: r.group, path: r.path,
				fork: r.forkSource, mirror: r.mirrorSource,
			}
		}
	}
	return out
}

// getAccessibleRepository returns one readable repository or nil, mirroring
// get_accessible_repository (pages.py:2735-2746).
func (p *pageNode) getAccessibleRepository(remoteIdentity *rns.Identity, groupName, repoName string) *accessibleRepo {
	g, ok := p.owner.groups[groupName]
	if !ok {
		return nil
	}
	r, ok := g.repositories[repoName]
	if !ok {
		return nil
	}
	if !p.resolvePermission(remoteIdentity, groupName, repoName, permRead) {
		return nil
	}
	return &accessibleRepo{
		name: r.name, group: r.group, path: r.path,
		fork: r.forkSource, mirror: r.mirrorSource,
	}
}
