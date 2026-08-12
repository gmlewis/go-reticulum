// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"strings"
	"testing"
)

// TestParseFlagsSubcommandDispatch checks the rngit subcommand selection:
// a first argument matching a known subcommand selects it; anything else
// defaults to "node" (mirroring server.main's len(sys.argv)<2 fallback).
func TestParseFlagsSubcommandDispatch(t *testing.T) {
	t.Parallel()

	subs := []string{"node", "create", "release", "perms", "work", "fork", "sync", "mirror"}
	for _, sub := range subs {
		opts, err := parseFlags([]string{sub, "--config", "/c", "--rnsconfig", "/r"}, io.Discard)
		if err != nil {
			t.Fatalf("parseFlags(%q): %v", sub, err)
		}
		if string(opts.subcommand) != sub {
			t.Errorf("subcommand %q: got %q, want %q", sub, opts.subcommand, sub)
		}
		if opts.configDir != "/c" {
			t.Errorf("subcommand %q: configDir=%q, want /c", sub, opts.configDir)
		}
		if opts.rnsConfigDir != "/r" {
			t.Errorf("subcommand %q: rnsConfigDir=%q, want /r", sub, opts.rnsConfigDir)
		}
	}

	// No subcommand argument -> default to "node".
	opts, err := parseFlags([]string{"--print-identity"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags default: %v", err)
	}
	if opts.subcommand != "node" {
		t.Errorf("default subcommand = %q, want node", opts.subcommand)
	}
	if !opts.printIdentity {
		t.Errorf("printIdentity = false, want true")
	}

	// Unknown first token is treated as a node flag position, not a subcommand.
	// Python falls back to "node" whenever argv[1] is not a known subcommand.
	opts, err = parseFlags([]string{"--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags --version: %v", err)
	}
	if opts.subcommand != "node" {
		t.Errorf("--version subcommand = %q, want node", opts.subcommand)
	}
}

// TestParseFlagsNode checks the node (serve) subcommand flags.
func TestParseFlagsNode(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{"node", "-p", "-s", "-i", "-v", "-v", "-q"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags node: %v", err)
	}
	if !opts.printIdentity || !opts.service || !opts.interactive {
		t.Errorf("node flags: p=%v s=%v i=%v, want all true", opts.printIdentity, opts.service, opts.interactive)
	}
	if opts.verbose != 2 || opts.quiet != 1 {
		t.Errorf("node counts: verbose=%d quiet=%d, want 2/1", opts.verbose, opts.quiet)
	}
}

// TestParseFlagsCreate checks create's positional repository argument.
func TestParseFlagsCreate(t *testing.T) {
	t.Parallel()

	repo := "rns://00112233445566778899aabbccddeeff/group/repo"
	opts, err := parseFlags([]string{"create", "-i", "/path/id", repo}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags create: %v", err)
	}
	if opts.identityPath != "/path/id" {
		t.Errorf("create identityPath=%q, want /path/id", opts.identityPath)
	}
	if opts.repository != repo {
		t.Errorf("create repository=%q, want %q", opts.repository, repo)
	}
}

// TestParseFlagsRelease checks release's full flag set and positionals.
func TestParseFlagsRelease(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{
		"release", "-i", "/id", "-s", "/signer", "-n", "pkgname", "-L", "-o",
		"rns://00112233445566778899aabbccddeeff/g/r", "create", "v1.0:./artifacts",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags release: %v", err)
	}
	if opts.identityPath != "/id" || opts.signer != "/signer" || opts.name != "pkgname" {
		t.Errorf("release: identity=%q signer=%q name=%q, want /id//signer/pkgname", opts.identityPath, opts.signer, opts.name)
	}
	if !opts.local || !opts.offline {
		t.Errorf("release: local=%v offline=%v, want true/true", opts.local, opts.offline)
	}
	if opts.repository != "rns://00112233445566778899aabbccddeeff/g/r" {
		t.Errorf("release repository=%q", opts.repository)
	}
	if opts.operation != "create" || opts.target != "v1.0:./artifacts" {
		t.Errorf("release operation=%q target=%q, want create/v1.0:./artifacts", opts.operation, opts.target)
	}
}

// TestParseFlagsWork checks work's --scope, -t, -d and positionals.
func TestParseFlagsWork(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{
		"work", "--scope", "all", "-t", "My Title", "-d", "42",
		"rns://00112233445566778899aabbccddeeff/g/r", "view",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags work: %v", err)
	}
	if opts.scope != "all" || opts.title != "My Title" || opts.docID != 42 {
		t.Errorf("work: scope=%q title=%q docID=%d, want all/My Title/42", opts.scope, opts.title, opts.docID)
	}
	if opts.operation != "view" {
		t.Errorf("work operation=%q, want view", opts.operation)
	}
}

// TestParseFlagsForkSyncMirror checks the two-positional subcommands.
func TestParseFlagsForkSyncMirror(t *testing.T) {
	t.Parallel()

	src := "rns://00112233445566778899aabbccddeeff/g/src"
	tgt := "rns://00112233445566778899aabbccddeeff/g/tgt"

	opts, err := parseFlags([]string{"fork", src, tgt}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags fork: %v", err)
	}
	if opts.source != src || opts.target != tgt {
		t.Errorf("fork: source=%q target=%q", opts.source, opts.target)
	}

	opts, err = parseFlags([]string{"sync", src}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags sync: %v", err)
	}
	if opts.repository != src {
		t.Errorf("sync: repository=%q, want %q", opts.repository, src)
	}

	opts, err = parseFlags([]string{"mirror", "--scope", "completed", src, tgt}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags mirror: %v", err)
	}
	if opts.scope != "completed" || opts.source != src || opts.target != tgt {
		t.Errorf("mirror: scope=%q source=%q target=%q", opts.scope, opts.source, opts.target)
	}
}

// TestParseFlagsPerms checks the perms subcommand positional.
func TestParseFlagsPerms(t *testing.T) {
	t.Parallel()

	remote := "rns://00112233445566778899aabbccddeeff/g/r"
	opts, err := parseFlags([]string{"perms", remote}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags perms: %v", err)
	}
	if opts.remote != remote {
		t.Errorf("perms remote=%q, want %q", opts.remote, remote)
	}
}

// TestParseFlagsVerboseQuietCounters checks the -v/-q count flags.
func TestParseFlagsVerboseQuietCounters(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{"node", "-v", "-v", "-q", "-q"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.verbose != 2 || opts.quiet != 2 {
		t.Errorf("verbose=%d quiet=%d, want 2/2", opts.verbose, opts.quiet)
	}
}

// TestParseFlagsHelp requests help and expects errHelp.
func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	_, err := parseFlags([]string{"node", "-h"}, &buf)
	if err != errHelp {
		t.Fatalf("parseFlags -h: err=%v, want errHelp", err)
	}
	if buf.Len() == 0 {
		t.Fatal("help output was empty")
	}
}
