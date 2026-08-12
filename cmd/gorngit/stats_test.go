// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newStatsTestNode builds a node with group "g" + repo "r.git" whose group
// perms grant PERM_STATS to everyone, plus an identity for stats calls.
// The stats subsystem is enabled and statsPath points into a temp dir.
func newStatsTestNode(t *testing.T) (*reticulumGitNode, *rns.Identity, string) {
	t.Helper()
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	base := testutils.TempDir(t, "gorngit-stats-")
	repoPath := filepath.Join(base, "r.git")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	group := &groupInfo{
		name:         "g",
		path:         base,
		repositories: map[string]*repositoryInfo{},
		perms:        permissionLists{stats: [][]byte{permTargetAllBytes}},
	}
	group.repositories["r.git"] = &repositoryInfo{
		name:  "r.git",
		group: "g",
		path:  repoPath,
		perms: permissionLists{},
	}
	n := &reticulumGitNode{
		identity:          id,
		groups:            map[string]*groupInfo{"g": group},
		blockedIdentities: map[string]bool{},
		identityAliases:   map[string]string{},
		stats:             map[any]any{"pages": map[any]any{"front": map[any]any{}}, "groups": map[any]any{}},
		statsEnabled:      true,
		statsIgnored:      map[string]bool{},
		statsPath:         filepath.Join(base, "stats"),
	}
	return n, id, base
}

// seedStats places day->count maps into n.stats for group/repo.
func seedStats(n *reticulumGitNode, group, repo string, view, fetch, push, download, releaseDL map[any]any) {
	groups := n.stats["groups"].(map[any]any)
	g := statsInitGroup()
	groups[group] = g
	repos := g["repositories"].(map[any]any)
	repos[repo] = map[any]any{
		"view":             view,
		"fetch":            fetch,
		"push":             push,
		"download":         download,
		"release_download": releaseDL,
	}
}

// dayCount builds a day->count map from string keys.
func dayCount(m map[string]int64) map[any]any {
	out := map[any]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestRepositoryStatsAtModerate(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	seedStats(n, "g", "r.git",
		dayCount(map[string]int64{"2026-01-14": 5, "2026-01-15": 3}),
		dayCount(map[string]int64{"2026-01-15": 2}),
		dayCount(map[string]int64{"2026-01-13": 1}),
		dayCount(map[string]int64{"2026-01-15": 4}),
		dayCount(map[string]int64{"2026-01-15": 1}),
	)

	got := n.repositoryStatsAt(id, "g", "r.git", 3, now)
	if got == nil {
		t.Fatal("repositoryStatsAt = nil, want a stats map")
	}
	if g := got["group"].(string); g != "g" {
		t.Errorf("group = %q, want g", g)
	}
	if r := got["repository"].(string); r != "r.git" {
		t.Errorf("repository = %q, want r.git", r)
	}
	if lb := got["lookback_days"].(int64); lb != 3 {
		t.Errorf("lookback_days = %d, want 3", lb)
	}
	wantDays := []string{"2026-01-13", "2026-01-14", "2026-01-15"}
	if d := got["days"].([]string); !reflect.DeepEqual(d, wantDays) {
		t.Errorf("days = %v, want %v", d, wantDays)
	}
	wantLabels := []string{"Jan 13", "Jan 14", "Jan 15"}
	if l := got["day_labels"].([]string); !reflect.DeepEqual(l, wantLabels) {
		t.Errorf("day_labels = %v, want %v", l, wantLabels)
	}
	if dr := got["date_range"].(string); dr != "Jan 13 - Jan 15" {
		t.Errorf("date_range = %q, want 'Jan 13 - Jan 15'", dr)
	}
	wantTL := []string{"3 days ago", "Today"}
	if tl := got["timeline_labels"].([]string); !reflect.DeepEqual(tl, wantTL) {
		t.Errorf("timeline_labels = %v, want %v", tl, wantTL)
	}

	// views: daily [0,5,3], total 8, peak 5, peak_day 2026-01-14
	v := got["views"].(map[any]any)
	if daily := v["daily"].([]any); !reflect.DeepEqual(daily, []any{int64(0), int64(5), int64(3)}) {
		t.Errorf("views.daily = %v, want [0 5 3]", daily)
	}
	if v["total"] != int64(8) {
		t.Errorf("views.total = %v, want 8", v["total"])
	}
	if v["peak"] != int64(5) {
		t.Errorf("views.peak = %v, want 5", v["peak"])
	}
	if v["peak_day"] != "2026-01-14" {
		t.Errorf("views.peak_day = %v, want 2026-01-14", v["peak_day"])
	}

	// fetches: daily [0,0,2], total 2, peak 2
	f := got["fetches"].(map[any]any)
	if daily := f["daily"].([]any); !reflect.DeepEqual(daily, []any{int64(0), int64(0), int64(2)}) {
		t.Errorf("fetches.daily = %v, want [0 0 2]", daily)
	}
	if f["total"] != int64(2) {
		t.Errorf("fetches.total = %v, want 2", f["total"])
	}

	// pushes: daily [1,0,0], total 1, peak 1, peak_day 2026-01-13
	pu := got["pushes"].(map[any]any)
	if daily := pu["daily"].([]any); !reflect.DeepEqual(daily, []any{int64(1), int64(0), int64(0)}) {
		t.Errorf("pushes.daily = %v, want [1 0 0]", daily)
	}
	if pu["total"] != int64(1) {
		t.Errorf("pushes.total = %v, want 1", pu["total"])
	}
	if pu["peak_day"] != "2026-01-13" {
		t.Errorf("pushes.peak_day = %v, want 2026-01-13", pu["peak_day"])
	}

	// downloads: daily [0,0,4], total 4
	dl := got["downloads"].(map[any]any)
	if dl["total"] != int64(4) {
		t.Errorf("downloads.total = %v, want 4", dl["total"])
	}

	// release_downloads: daily [0,0,1], total 1
	rdl := got["release_downloads"].(map[any]any)
	if rdl["total"] != int64(1) {
		t.Errorf("release_downloads.total = %v, want 1", rdl["total"])
	}

	// downloads_combined: daily [0,0,5], total 5
	dc := got["downloads_combined"].(map[any]any)
	if daily := dc["daily"].([]any); !reflect.DeepEqual(daily, []any{int64(0), int64(0), int64(5)}) {
		t.Errorf("downloads_combined.daily = %v, want [0 0 5]", daily)
	}
	if dc["total"] != int64(5) {
		t.Errorf("downloads_combined.total = %v, want 5", dc["total"])
	}

	// activity_score = int(view_total*0.2 + fetch_total*2 + push_total*5)
	// view_total = 8 + 4 + 1 = 13; 13*0.2 + 2*2 + 1*5 = 2.6+4+5 = 11.6 -> 11
	if got["activity_score"] != int64(11) {
		t.Errorf("activity_score = %v, want 11", got["activity_score"])
	}
	// actual_days: earliest activity 2026-01-13, span=2.5 days -> int(2)+1 = 3
	if got["actual_days"] != int64(3) {
		t.Errorf("actual_days = %v, want 3", got["actual_days"])
	}
	// daily_score = 11.6/3 = 3.87 -> moderate
	if got["activity_level"] != "moderate" {
		t.Errorf("activity_level = %v, want moderate", got["activity_level"])
	}
}

func TestRepositoryStatsAtInactive(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	// No stats seeded: everything zero.
	got := n.repositoryStatsAt(id, "g", "r.git", 3, now)
	if got == nil {
		t.Fatal("repositoryStatsAt = nil")
	}
	if got["activity_score"] != int64(0) {
		t.Errorf("activity_score = %v, want 0", got["activity_score"])
	}
	if got["activity_level"] != "inactive" {
		t.Errorf("activity_level = %v, want inactive", got["activity_level"])
	}
	v := got["views"].(map[any]any)
	if v["peak_day"] != "" {
		t.Errorf("views.peak_day = %v, want empty", v["peak_day"])
	}
	if v["total"] != int64(0) {
		t.Errorf("views.total = %v, want 0", v["total"])
	}
	// actual_days falls back to lookback when no activity.
	if got["actual_days"] != int64(3) {
		t.Errorf("actual_days = %v, want 3", got["actual_days"])
	}
}

func TestRepositoryStatsAtHigh(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	// 10 pushes today -> total_score 50, actual_days 1, daily_score 50 -> high.
	seedStats(n, "g", "r.git",
		dayCount(map[string]int64{}),
		dayCount(map[string]int64{}),
		dayCount(map[string]int64{"2026-01-15": 10}),
		dayCount(map[string]int64{}),
		dayCount(map[string]int64{}),
	)
	got := n.repositoryStatsAt(id, "g", "r.git", 3, now)
	if got == nil {
		t.Fatal("repositoryStatsAt = nil")
	}
	if got["activity_score"] != int64(50) {
		t.Errorf("activity_score = %v, want 50", got["activity_score"])
	}
	if got["actual_days"] != int64(1) {
		t.Errorf("actual_days = %v, want 1", got["actual_days"])
	}
	if got["activity_level"] != "high" {
		t.Errorf("activity_level = %v, want high", got["activity_level"])
	}
}

func TestRepositoryStatsAtLow(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	// 1 push today -> total_score 5, actual_days 1, daily_score 5 -> moderate
	// (5 < 10). Use 1 fetch today -> total_score 2, daily_score 2 -> low.
	seedStats(n, "g", "r.git",
		dayCount(map[string]int64{}),
		dayCount(map[string]int64{"2026-01-15": 1}),
		dayCount(map[string]int64{}),
		dayCount(map[string]int64{}),
		dayCount(map[string]int64{}),
	)
	got := n.repositoryStatsAt(id, "g", "r.git", 3, now)
	if got == nil {
		t.Fatal("repositoryStatsAt = nil")
	}
	if got["activity_score"] != int64(2) {
		t.Errorf("activity_score = %v, want 2", got["activity_score"])
	}
	if got["activity_level"] != "low" {
		t.Errorf("activity_level = %v, want low", got["activity_level"])
	}
}

func TestRepositoryStatsAtDisallowed(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	// Strip stats perms -> resolvePermission(permStats) false -> nil.
	n.groups["g"].perms = permissionLists{}
	if got := n.repositoryStatsAt(id, "g", "r.git", 3, now); got != nil {
		t.Errorf("repositoryStatsAt = %v, want nil when stats perm denied", got)
	}
}

func TestRecordRepoStatAndPersist(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	n.recordRepoStat("g", "r.git", "view")
	n.recordRepoStat("g", "r.git", "view")
	n.recordRepoStat("g", "r.git", "fetch")
	today := time.Now().Format("2006-01-02")

	n.statsMu.Lock()
	g := n.stats["groups"].(map[any]any)["g"].(map[any]any)
	repos := g["repositories"].(map[any]any)
	repo := repos["r.git"].(map[any]any)
	views := repo["view"].(map[any]any)
	fetches := repo["fetch"].(map[any]any)
	n.statsMu.Unlock()

	if views[today] != int64(2) {
		t.Errorf("view[%s] = %v, want 2", today, views[today])
	}
	if fetches[today] != int64(1) {
		t.Errorf("fetch[%s] = %v, want 1", today, fetches[today])
	}
	// persistStats wrote the file.
	if _, err := os.Stat(n.statsPath); err != nil {
		t.Errorf("stats file not persisted: %v", err)
	}
	// Sanity: id unused so far.
	_ = id
}

func TestRecordGroupAndPageView(t *testing.T) {
	t.Parallel()
	n, _, _ := newStatsTestNode(t)
	n.recordGroupView("g")
	n.recordPageView()
	today := time.Now().Format("2006-01-02")

	n.statsMu.Lock()
	g := n.stats["groups"].(map[any]any)["g"].(map[any]any)
	gv := g["view"].(map[any]any)
	front := n.stats["pages"].(map[any]any)["front"].(map[any]any)
	n.statsMu.Unlock()

	if gv[today] != int64(1) {
		t.Errorf("group view[%s] = %v, want 1", today, gv[today])
	}
	if front[today] != int64(1) {
		t.Errorf("front view[%s] = %v, want 1", today, front[today])
	}
}

func TestStatsDisabledNoop(t *testing.T) {
	t.Parallel()
	n, _, _ := newStatsTestNode(t)
	n.statsEnabled = false
	n.recordRepoStat("g", "r.git", "view")
	n.recordGroupView("g")
	n.recordPageView()
	// groups should not have been created.
	if g, ok := n.stats["groups"].(map[any]any); ok && len(g) > 0 {
		t.Errorf("stats mutated while disabled: %v", g)
	}
}

func TestLoadStatsRoundTrip(t *testing.T) {
	t.Parallel()
	n, _, _ := newStatsTestNode(t)
	seedStats(n, "g", "r.git",
		dayCount(map[string]int64{"2026-01-14": 5}),
		dayCount(map[string]int64{"2026-01-15": 2}),
		dayCount(map[string]int64{"2026-01-13": 1}),
		dayCount(map[string]int64{}),
		dayCount(map[string]int64{}),
	)
	n.persistStats()

	// Fresh node loads the persisted file.
	n2, _, _ := newStatsTestNode(t)
	n2.statsPath = n.statsPath
	n2.loadStats()
	g := n2.stats["groups"].(map[any]any)["g"].(map[any]any)
	repos := g["repositories"].(map[any]any)
	repo := repos["r.git"].(map[any]any)
	views := repo["view"].(map[any]any)
	if views["2026-01-14"] != int64(5) {
		t.Errorf("after load view[2026-01-14] = %v, want 5", views["2026-01-14"])
	}
	fetches := repo["fetch"].(map[any]any)
	if fetches["2026-01-15"] != int64(2) {
		t.Errorf("after load fetch[2026-01-15] = %v, want 2", fetches["2026-01-15"])
	}
}

func TestStatsDayFormat(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 7, 8, 30, 0, 0, time.UTC)
	if d := statsDay(now); d != "2026-03-07" {
		t.Errorf("statsDay = %q, want 2026-03-07", d)
	}
}

func TestLoadStatsIgnored(t *testing.T) {
	t.Parallel()
	n, _, _ := newStatsTestNode(t)
	n.identityAliases["alice"] = "d09285e660cfe27cee6d9a0beb58b7e0"
	cfg := &nodeConfig{
		statsIgnored: []string{
			"d09285e660cfe27cee6d9a0beb58b7e0", // raw 32-hex
			"alice",                            // alias
			"tooshort",                         // ignored: not 32 hex
			"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", // ignored: not hex
		},
	}
	n.loadStatsIgnored(cfg)
	if !n.statsIgnored["d09285e660cfe27cee6d9a0beb58b7e0"] {
		t.Error("raw hex hash not ignored")
	}
	// alias resolved to the same hash -> already set
	if len(n.statsIgnored) != 1 {
		t.Errorf("statsIgnored = %v, want exactly 1 entry", n.statsIgnored)
	}
}

func TestStatsIgnoredRemote(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	// Mark id's hash as ignored.
	n.statsIgnored[statsHashHex(id)] = true
	if !n.statsIgnoredRemote(id) {
		t.Error("statsIgnoredRemote should be true for ignored id")
	}
	// viewSucceeded on an ignored identity records nothing.
	pre := snapshotCounts(n)
	n.viewSucceeded(new("g"), new("r.git"), id)
	if !countsUnchanged(pre, snapshotCounts(n)) {
		t.Error("viewSucceeded recorded despite ignored remote")
	}
}

func TestViewSucceededLevels(t *testing.T) {
	t.Parallel()
	n, id, _ := newStatsTestNode(t)
	today := time.Now().Format("2006-01-02")
	// Front page view.
	n.viewSucceeded(nil, nil, id)
	// Group view.
	n.viewSucceeded(new("g"), nil, id)
	// Repo view.
	n.viewSucceeded(new("g"), new("r.git"), id)

	n.statsMu.Lock()
	front := n.stats["pages"].(map[any]any)["front"].(map[any]any)
	g := n.stats["groups"].(map[any]any)["g"].(map[any]any)
	gv := g["view"].(map[any]any)
	repo := g["repositories"].(map[any]any)["r.git"].(map[any]any)
	rv := repo["view"].(map[any]any)
	n.statsMu.Unlock()

	if front[today] != int64(1) {
		t.Errorf("front[%s] = %v, want 1", today, front[today])
	}
	if gv[today] != int64(1) {
		t.Errorf("group view[%s] = %v, want 1", today, gv[today])
	}
	if rv[today] != int64(1) {
		t.Errorf("repo view[%s] = %v, want 1", today, rv[today])
	}
}

// statsHashHex returns the lowercase hex of an identity hash.
func statsHashHex(id *rns.Identity) string {
	return fmt.Sprintf("%x", id.Hash)
}

// strPtr returns a pointer to s (convenience for viewSucceeded's *string args).
//
//go:fix inline
func strPtr(s string) *string { return new(s) }

// snapshotCounts returns a deep-enough copy of the stats counters to detect
// whether a recorder mutated anything.
func snapshotCounts(n *reticulumGitNode) map[string]int64 {
	n.statsMu.Lock()
	defer n.statsMu.Unlock()
	out := map[string]int64{}
	pages := asAnyMap(n.stats["pages"])
	if pages != nil {
		front := asAnyMap(pages["front"])
		for k, v := range front {
			out["front:"+k.(string)] = toInt(v)
		}
	}
	groups := asAnyMap(n.stats["groups"])
	for gn, gv := range groups {
		g := asAnyMap(gv)
		gvMap := asAnyMap(g["view"])
		for k, v := range gvMap {
			out["group:"+gn.(string)+":"+k.(string)] = toInt(v)
		}
		repos := asAnyMap(g["repositories"])
		for rn, rv := range repos {
			repo := asAnyMap(rv)
			for _, kind := range []string{"view", "fetch", "push", "download", "release_download"} {
				km := asAnyMap(repo[kind])
				for k, v := range km {
					out["repo:"+gn.(string)+":"+rn.(string)+":"+kind+":"+k.(string)] = toInt(v)
				}
			}
		}
	}
	return out
}

func countsUnchanged(a, b map[string]int64) bool {
	return reflect.DeepEqual(a, b)
}
