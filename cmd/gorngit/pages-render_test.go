// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestMLinkGolden verifies the m_link family against values captured from
// pages.py NomadNetworkNode.m_link / m_link_r / m_link_e.
func TestMLinkGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, want string
		got        string
	}{
		{"link_plain", "`!`[Node`:/page/index.mu]`!", mLink("Node", pagePathIndex, nil)},
		{"link_fields", "`!`[repo`:/page/repo.mu`g=main|r=repo.git]`!",
			mLink("repo", pagePathRepo, []linkField{{"g", "main"}, {"r", "repo.git"}})},
		{"link_label_unescaped", "`!`[with space? &/x`:/page/repo.mu`g=main]`!",
			mLink("with space? &/x", pagePathRepo, []linkField{{"g", "main"}})},
		{"link_r", "`[text`:/page/tree.mu`g=g|r=r|ref=HEAD|path=dir%2Fsub]",
			mLinkR("text", pagePathTree, []linkField{{"g", "g"}, {"r", "r"}, {"ref", "HEAD"}, {"path", "dir/sub"}})},
		{"link_r_order", "`[text`:/page/tree.mu`ref=HEAD|path=dir%2Fsub|page=2]",
			mLinkR("text", pagePathTree, []linkField{{"ref", "HEAD"}, {"path", "dir/sub"}, {"page", 2}})},
		{"link_e", "`!`[link`abc123:/page/repo.mu`g=main|r=repo]`!",
			mLinkE("link", "abc123", pagePathRepo, []linkField{{"g", "main"}, {"r", "repo"}})},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestMicronHelpers verifies the simple m_* micron helpers against golden
// values captured from pages.py.
func TestMicronHelpers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, got, want string
	}{
		{"mHeading", mHeading("Hi", 2), ">>Hi\n"},
		{"mHeading1", mHeading("X", 1), ">X\n"},
		{"mBold", mBold("b"), "`!b`!"},
		{"mItalic", mItalic("i"), "`*i`*"},
		{"mUnderline", mUnderline("u"), "`_u`_"},
		{"mEscape", mEscape("a`b"), "a\\`b"},
		{"mDivider", mDivider(), "-─\n"},
		{"mDividerEq", mDivider("="), "-=\n"},
		{"mColorFG", mColorFG("hi", "666"), "`F666hi`f"},
		{"mAlignCenter", mAlign("txt", "center"), "`ctxt`a"},
		{"mAlignLeft", mAlign("txt", "left"), "`ltxt`a"},
		{"mAlignRight", mAlign("txt", "right"), "`rtxt`a"},
		{"mAlignDefault", mAlign("txt", "weird"), "`atxt`a"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestIconGolden verifies icon() against glyphs captured from pages.py.
func TestIconGolden(t *testing.T) {
	t.Parallel()
	if g := icon("folder", true); g != "󰉖" {
		t.Errorf("nerd folder = %q, want 󰉖", g)
	}
	if g := icon("folder", false); g != "🗀" {
		t.Errorf("unicode folder = %q, want 🗎", g)
	}
	if g := icon("sep", false); g != "•" {
		t.Errorf("unicode sep = %q, want •", g)
	}
	if g := icon("unknown", true); g != "" {
		t.Errorf("unknown icon = %q, want empty", g)
	}
}

// TestFormatRelativeTime verifies the branch thresholds of
// format_relative_time (pages.py:2389-2412) using an explicit now.
func TestFormatRelativeTime(t *testing.T) {
	t.Parallel()
	const now int64 = 1000000
	cases := []struct {
		ts   int64
		want string
	}{
		{now - 59, "just now"},
		{now - 60, "1 minute ago"},
		{now - 120, "2 minutes ago"},
		{now - 3600, "1 hour ago"},
		{now - 3601, "1 hour ago"},
		{now - 7200, "2 hours ago"},
		{now - 86400, "1 day ago"},
		{now - 172800, "2 days ago"},
		{now - 604800, "1 week ago"},
		{now - 700000, "1 week ago"},
		{now - 2592000, "1 month ago"},
		{now - 3000000, "1 month ago"},
		{now - 31536000, "1 year ago"},
		{now - 63072000, "2 years ago"},
		{now - 378432000, "12 years ago"},
		{now - 42136000, "1 year ago"},
	}
	for _, c := range cases {
		if g := formatRelativeTime(c.ts, now); g != c.want {
			t.Errorf("relative_time(%d) = %q, want %q", now-c.ts, g, c.want)
		}
	}
}

// TestFormatAbsoluteTimeShape verifies formatAbsoluteTime emits the
// %Y-%m-%d %H:%M:%S layout (local time, matching Python's
// datetime.fromtimestamp).
func TestFormatAbsoluteTimeShape(t *testing.T) {
	t.Parallel()
	got := formatAbsoluteTime(0)
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`).MatchString(got) {
		t.Errorf("formatAbsoluteTime(0) = %q, want YYYY-MM-DD HH:MM:SS", got)
	}
}

// TestFormatTabs verifies tab expansion to three spaces (TAB_WIDTH).
func TestFormatTabs(t *testing.T) {
	t.Parallel()
	if g := formatTabs("a\tb"); g != "a   b" {
		t.Errorf("formatTabs = %q, want %q", g, "a   b")
	}
}

// TestFormatDiff verifies colour-coded diff rendering against the golden
// value captured from pages.py format_diff.
func TestFormatDiff(t *testing.T) {
	t.Parallel()
	in := "+added\n-removed\n@@ -1 +1 @@\ndiff --git a/x b/x\nindex abc..def\n--- a/x\n+++ b/x\nnormal"
	want := "`F0a0+added`f\n`F900-removed`f\n`F0aa@@ -1 +1 @@`f\n\n`F666diff --git a/x b/x`f\n`F666index abc..def`f\n\\--- a/x\n+++ b/x\nnormal"
	if g := formatDiff(in); g != want {
		t.Errorf("formatDiff =\n%q\nwant\n%q", g, want)
	}
}

// TestFormatCommit verifies commit-message escaping against the golden
// value captured from pages.py format_commit.
func TestFormatCommit(t *testing.T) {
	t.Parallel()
	in := "title\n-body\n+add\nnormal"
	want := "title\n\\-body\n+add\nnormal"
	if g := formatCommit(in); g != want {
		t.Errorf("formatCommit = %q, want %q", g, want)
	}
}

// TestRenderTemplate verifies the {PAGE_CONTENT}/{NAVIGATION}/{NODE_NAME}/
// {VERSION}/{GEN_TIME} substitutions and template selection.
func TestRenderTemplate(t *testing.T) {
	t.Parallel()
	out := renderTemplate("BODY", "NAV", "front", "mynode", "1.4.2", 0.001)
	s := string(out)
	if !strings.Contains(s, "> mynode\n") {
		t.Errorf("missing NODE_NAME substitution in %q", s)
	}
	if !strings.Contains(s, "NAV\n") {
		t.Errorf("missing NAVIGATION substitution in %q", s)
	}
	if !strings.Contains(s, "> Groups\n\nBODY") {
		t.Errorf("front template should wrap BODY, got %q", s)
	}
	if !strings.Contains(s, "1.4.2") {
		t.Errorf("missing VERSION substitution in %q", s)
	}
	if !strings.Contains(s, "Generated in") {
		t.Errorf("missing GEN_TIME in %q", s)
	}

	// Empty nav removes the placeholder.
	out2 := renderTemplate("BODY", "", "repo", "n", "v", 0)
	if strings.Contains(string(out2), "{NAVIGATION}") {
		t.Errorf("NAVIGATION placeholder not removed: %q", out2)
	}

	// Unknown template falls back to base with raw page content.
	out3 := renderTemplate("RAW", "", "nonexistent", "n", "v", 0)
	if !strings.Contains(string(out3), "RAW") {
		t.Errorf("unknown template should still embed page content: %q", out3)
	}
}
