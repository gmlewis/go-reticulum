// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// writeTestNodeConfig writes a raw config text to configDir/config.
func writeTestNodeConfig(t *testing.T, configDir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseNodeConfigPages(t *testing.T) {
	t.Parallel()
	text := "[rngit]\nnode_name = N\n" +
		"[pages]\nserve_nomadnet = yes\nunicode_icons = yes\n"
	cfg, err := parseNodeConfig(text)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.serveNomadnet {
		t.Error("serve_nomadnet = yes should set serveNomadnet")
	}
	if !cfg.unicodeIcons {
		t.Error("unicode_icons = yes should set unicodeIcons")
	}
}

func TestParseNodeConfigPagesDisabled(t *testing.T) {
	t.Parallel()
	text := "[pages]\nserve_nomadnet = no\n"
	cfg, err := parseNodeConfig(text)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.serveNomadnet {
		t.Error("serve_nomadnet = no should leave serveNomadnet false")
	}
	if cfg.unicodeIcons {
		t.Error("unicode_icons absent should leave unicodeIcons false")
	}
}

func TestParseNodeConfigPagesAbsent(t *testing.T) {
	t.Parallel()
	cfg, err := parseNodeConfig("[rngit]\nnode_name = N\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.serveNomadnet || cfg.unicodeIcons {
		t.Error("absent [pages] section should leave both flags false")
	}
}

func TestDefaultConfigWritesPagesSection(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngit-cfgdefault-")
	if err := writeDefaultNodeConfig(filepath.Join(dir, "config")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("[pages]")) {
		t.Error("default config should include a [pages] section")
	}
	if !bytes.Contains(data, []byte("serve_nomadnet")) {
		t.Error("default config should mention serve_nomadnet")
	}
	if !bytes.Contains(data, []byte("unicode_icons")) {
		t.Error("default config should mention unicode_icons")
	}
}

// TestStartPageServer verifies the serve() wiring creates the
// "nomadnetwork.node" destination, registers handlers, announces, sets the
// default app data, and starts the jobs loop. It uses an interface-less
// transport so no packets leave the process.
func TestStartPageServer(t *testing.T) {
	t.Parallel()
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)

	configDir := testutils.TempDir(t, "gorngit-pagecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-pagerepos-")
	writeTestNodeConfig(t, configDir, "[rngit]\nnode_name = PageNodeTest\nannounce_interval = 0\n"+
		"[repositories]\ng = "+repoRoot+"\n"+
		"[pages]\nserve_nomadnet = yes\n")

	node, err := newReticulumGitNode(configDir, logger)
	if err != nil {
		t.Fatalf("newReticulumGitNode: %v", err)
	}
	if !node.config.serveNomadnet {
		t.Fatal("config did not enable serve_nomadnet")
	}

	ts := rns.NewTransportSystem(logger)
	if err := node.startPageServer(ts, logger); err != nil {
		t.Fatalf("startPageServer: %v", err)
	}
	defer node.pageServer.shouldRun.Store(false)

	pn := node.pageServer
	if pn == nil {
		t.Fatal("pageServer not set")
	}
	if pn.destination == nil {
		t.Error("page destination not set")
	}
	if !pn.shouldRun.Load() {
		t.Error("shouldRun should be true after startPageServer")
	}
	if pn.lastAnnounce <= 0 {
		t.Error("announce should have recorded lastAnnounce")
	}

	wantHash := rns.CalculateHash(node.identity, pageAppName, "node")
	if got := pn.destination.Hash; !bytes.Equal(got, wantHash) {
		t.Errorf("page dest hash = %x, want %x", got, wantHash)
	}
	if got, want := pn.destination.HexHash, hex.EncodeToString(wantHash); got != want {
		t.Errorf("page dest hexhash = %q, want %q", got, want)
	}

	if appData := pn.destination.DefaultAppData(); !bytes.Equal(appData, []byte("PageNodeTest")) {
		t.Errorf("default app data = %q, want %q", appData, "PageNodeTest")
	}

	// serve_nomadnet disabled -> newReticulumGitNode leaves no page server.
	offConfigDir := testutils.TempDir(t, "gorngit-pageoff-")
	offRepoRoot := testutils.TempDir(t, "gorngit-pageoffrepos-")
	writeTestNodeConfig(t, offConfigDir, "[rngit]\nnode_name = Off\n"+
		"[repositories]\ng = "+offRepoRoot+"\n"+
		"[pages]\nserve_nomadnet = no\n")
	offNode, err := newReticulumGitNode(offConfigDir, logger)
	if err != nil {
		t.Fatalf("newReticulumGitNode (off): %v", err)
	}
	if offNode.config.serveNomadnet {
		t.Fatal("off node should not have serveNomadnet set")
	}
	if offNode.pageServer != nil {
		t.Error("off node should not have a page server")
	}
}
