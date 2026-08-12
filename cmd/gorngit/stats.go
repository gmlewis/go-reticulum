// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// stats.go implements the repository statistics subsystem for the gorngit
// node, mirroring the stats recorders and reader of ReticulumGitNode
// (server.py:4598-4860). Stats are persisted to <configdir>/stats as a
// msgpack map matching the Python layout (pages/front, groups/<g>/view,
// groups/<g>/repositories/<r>/{view,fetch,push,download,release_download}
// day->count maps) so a stats file is interchangeable with a Python node's.

package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// statsInitRepo mirrors STATS_INIT_REPO (server.py:4598).
func statsInitRepo() map[any]any {
	return map[any]any{
		"view":             map[any]any{},
		"fetch":            map[any]any{},
		"push":             map[any]any{},
		"download":         map[any]any{},
		"release_download": map[any]any{},
	}
}

// statsInitGroup mirrors STATS_INIT_GROUP (server.py:4599).
func statsInitGroup() map[any]any {
	return map[any]any{"view": map[any]any{}, "repositories": map[any]any{}}
}

// loadStatsIgnored resolves [rngit] stats_ignore_identities (hex or alias)
// into n.statsIgnored (hex-string keyed), mirroring __apply_config
// (server.py:2208-2214). Aliases are resolved before use.
func (n *reticulumGitNode) loadStatsIgnored(cfg *nodeConfig) {
	if cfg == nil {
		return
	}
	for _, hexHash := range cfg.statsIgnored {
		resolved := n.resolveIdentityAlias(hexHash)
		if len(resolved) != rns.TruncatedHashLength/8*2 {
			continue
		}
		if _, err := hex.DecodeString(resolved); err != nil {
			continue
		}
		n.statsIgnored[resolved] = true
	}
}

// statsDay returns the local-date key for now, mirroring _get_day
// (server.py:4744-4747).
func statsDay(now time.Time) string {
	return now.Format("2006-01-02")
}

// loadStats loads the persisted stats map from n.statsPath, mirroring
// __load_stats (server.py:2074-2086). When no stats file exists the
// in-memory default is left in place (and not persisted here).
func (n *reticulumGitNode) loadStats() {
	n.statsMu.Lock()
	defer n.statsMu.Unlock()
	data, err := os.ReadFile(n.statsPath)
	if err != nil {
		return
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return
	}
	if m, ok := unpacked.(map[any]any); ok {
		n.stats = m
	}
}

// persistStats atomically writes the stats map to n.statsPath, mirroring
// __persist_stats (server.py:2087-2094).
func (n *reticulumGitNode) persistStats() {
	n.statsMu.Lock()
	stats := n.stats
	n.statsMu.Unlock()
	data, err := msgpack.Pack(stats)
	if err != nil {
		return
	}
	tmpPath := n.statsPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmpPath, n.statsPath)
}

// repoDayMap returns the day->count map for the given group/repo/kind,
// creating intermediate group/repo/kind maps as needed, mirroring the
// lazy-init pattern of record_repository_view et al. (server.py:4778-4794).
// caller must hold n.statsMu.
func (n *reticulumGitNode) repoDayMap(group, repo, kind string) map[any]any {
	groups := asAnyMap(n.stats["groups"])
	if groups == nil {
		groups = map[any]any{}
		n.stats["groups"] = groups
	}
	g := asAnyMap(groups[group])
	if g == nil {
		g = statsInitGroup()
		groups[group] = g
	}
	repos := asAnyMap(g["repositories"])
	if repos == nil {
		repos = map[any]any{}
		g["repositories"] = repos
	}
	r := asAnyMap(repos[repo])
	if r == nil {
		r = statsInitRepo()
		repos[repo] = r
	}
	m := asAnyMap(r[kind])
	if m == nil {
		m = map[any]any{}
		r[kind] = m
	}
	return m
}

// groupDayMap returns the day->count map for a group view, mirroring
// record_group_view (server.py:4762-4773).
func (n *reticulumGitNode) groupDayMap(group string) map[any]any {
	groups := asAnyMap(n.stats["groups"])
	if groups == nil {
		groups = map[any]any{}
		n.stats["groups"] = groups
	}
	g := asAnyMap(groups[group])
	if g == nil {
		g = statsInitGroup()
		groups[group] = g
	}
	m := asAnyMap(g["view"])
	if m == nil {
		m = map[any]any{}
		g["view"] = m
	}
	return m
}

// recordRepoStat increments the per-day counter for group/repo/kind and
// persists, mirroring record_repository_view / record_fetch / record_push /
// record_download / record_release_download (server.py:4778-4860). Unlike
// the Python originals which spawn a thread per record, this records
// synchronously under the stats mutex; request handlers run in their own
// goroutines so callers are not blocked on the network.
func (n *reticulumGitNode) recordRepoStat(group, repo, kind string) {
	if !n.statsEnabled {
		return
	}
	now := time.Now()
	day := statsDay(now)
	n.statsMu.Lock()
	m := n.repoDayMap(group, repo, kind)
	count, _ := m[day].(int64)
	m[day] = count + 1
	n.statsMu.Unlock()
	n.persistStats()
}

// recordGroupView increments a group's view counter, mirroring
// record_group_view (server.py:4762-4773).
func (n *reticulumGitNode) recordGroupView(group string) {
	if !n.statsEnabled {
		return
	}
	now := time.Now()
	day := statsDay(now)
	n.statsMu.Lock()
	m := n.groupDayMap(group)
	count, _ := m[day].(int64)
	m[day] = count + 1
	n.statsMu.Unlock()
	n.persistStats()
}

// recordPageView increments the front-page view counter, mirroring
// record_page_view (server.py:4751-4760).
func (n *reticulumGitNode) recordPageView() {
	if !n.statsEnabled {
		return
	}
	now := time.Now()
	day := statsDay(now)
	n.statsMu.Lock()
	pages := asAnyMap(n.stats["pages"])
	if pages == nil {
		pages = map[any]any{}
		n.stats["pages"] = pages
	}
	front := asAnyMap(pages["front"])
	if front == nil {
		front = map[any]any{}
		pages["front"] = front
	}
	count, _ := front[day].(int64)
	front[day] = count + 1
	n.statsMu.Unlock()
	n.persistStats()
}

// statsIgnoredRemote reports whether the remote identity is on the
// stats_ignore_identities list, mirroring the `remote_identity.hash in
// self.stats_ignored` guard (server.py:4720).
func (n *reticulumGitNode) statsIgnoredRemote(remote *rns.Identity) bool {
	if remote == nil || remote.Hash == nil {
		return false
	}
	return n.statsIgnored[fmt.Sprintf("%x", remote.Hash)]
}

// viewSucceeded records a view event, mirroring view_succeeded
// (server.py:4719-4723). A nil repo records a group view; both nil records
// a front-page view.
func (n *reticulumGitNode) viewSucceeded(group, repo *string, remote *rns.Identity) {
	if n.statsIgnoredRemote(remote) {
		return
	}
	if !n.statsEnabled {
		return
	}
	if group == nil && repo == nil {
		n.recordPageView()
		return
	}
	if repo == nil {
		n.recordGroupView(*group)
		return
	}
	n.recordRepoStat(*group, *repo, "view")
}

// fetchSucceeded records a fetch event, mirroring fetch_succeeded
// (server.py:4725-4727).
func (n *reticulumGitNode) fetchSucceeded(group, repo string, remote *rns.Identity) {
	if !n.statsEnabled {
		return
	}
	if group != "" && repo != "" {
		n.recordRepoStat(group, repo, "fetch")
	}
}

// pushSucceeded records a push event, mirroring push_succeeded
// (server.py:4729-4731).
func (n *reticulumGitNode) pushSucceeded(group, repo string, remote *rns.Identity) {
	if !n.statsEnabled {
		return
	}
	if group != "" && repo != "" {
		n.recordRepoStat(group, repo, "push")
	}
}

// downloadSucceeded records a blob download, mirroring download_succeeded
// (server.py:4735-4738).
func (n *reticulumGitNode) downloadSucceeded(group, repo string, remote *rns.Identity) {
	if n.statsIgnoredRemote(remote) {
		return
	}
	if !n.statsEnabled {
		return
	}
	if group != "" && repo != "" {
		n.recordRepoStat(group, repo, "download")
	}
}

// releaseDownloadSucceeded records a release-artifact download, mirroring
// release_download_succeeded (server.py:4740-4743).
func (n *reticulumGitNode) releaseDownloadSucceeded(group, repo string, remote *rns.Identity) {
	if n.statsIgnoredRemote(remote) {
		return
	}
	if !n.statsEnabled {
		return
	}
	if group != "" && repo != "" {
		n.recordRepoStat(group, repo, "release_download")
	}
}

// statSeries is one metric's daily counts and aggregates, mirroring the
// per-metric dicts built by repository_stats (server.py:4607-4684).
type statSeries struct {
	daily   []int64
	total   int64
	peak    int64
	peakDay string
}

// repositoryStats returns the aggregated stats for a repo over the
// lookback window, mirroring repository_stats (server.py:4601-4700). It
// returns nil when the remote lacks PERM_STATS. now is taken as time.Now.
func (n *reticulumGitNode) repositoryStats(remote *rns.Identity, group, repo string, lookbackDays int) map[any]any {
	return n.repositoryStatsAt(remote, group, repo, lookbackDays, time.Now())
}

// repositoryStatsAt is the deterministic, now-injected form of
// repositoryStats, used by tests. It mirrors server.py:4601-4700 exactly
// (day boundary math, activity score, activity level).
func (n *reticulumGitNode) repositoryStatsAt(remote *rns.Identity, group, repo string, lookbackDays int, now time.Time) map[any]any {
	if !n.resolvePermission(remote, group, repo, permStats) {
		return nil
	}
	const daySeconds = 86400

	var days []string
	var dayLabels []string
	for i := lookbackDays - 1; i >= 0; i-- {
		dayTS := now.Add(-time.Duration(i) * time.Second * daySeconds)
		dayStr := dayTS.Format("2006-01-02")
		days = append(days, dayStr)
		dayLabels = append(dayLabels, dayTS.Format("Jan 02"))
	}

	result := map[any]any{
		"group":              group,
		"repository":         repo,
		"lookback_days":      int64(lookbackDays),
		"date_range":         fmt.Sprintf("%s - %s", dayLabels[0], dayLabels[len(dayLabels)-1]),
		"days":               days,
		"day_labels":         dayLabels,
		"timeline_labels":    []string{fmt.Sprintf("%d days ago", lookbackDays), "Today"},
		"views":              seriesMap(),
		"fetches":            seriesMap(),
		"pushes":             seriesMap(),
		"downloads":          seriesMap(),
		"release_downloads":  seriesMap(),
		"downloads_combined": seriesMap(),
	}

	n.statsMu.Lock()
	groups := asAnyMap(n.stats["groups"])
	repoData := map[any]any{}
	if groups != nil {
		if g := asAnyMap(groups[group]); g != nil {
			repos := asAnyMap(g["repositories"])
			if repos != nil {
				repoData = asAnyMap(repos[repo])
			}
		}
	}
	n.statsMu.Unlock()

	viewStats := asAnyMap(repoData["view"])
	fetchStats := asAnyMap(repoData["fetch"])
	pushStats := asAnyMap(repoData["push"])
	downloadStats := asAnyMap(repoData["download"])
	releaseDLStats := asAnyMap(repoData["release_download"])

	result["views"] = buildSeries(viewStats, days)
	result["fetches"] = buildSeries(fetchStats, days)
	result["pushes"] = buildSeries(pushStats, days)
	result["downloads"] = buildSeries(downloadStats, days)
	result["release_downloads"] = buildSeries(releaseDLStats, days)
	result["downloads_combined"] = buildSeriesCombined(downloadStats, releaseDLStats, days)

	v := result["views"].(map[any]any)
	f := result["fetches"].(map[any]any)
	pu := result["pushes"].(map[any]any)
	dl := result["downloads"].(map[any]any)
	rdl := result["release_downloads"].(map[any]any)

	viewTotal := toInt(v["total"]) + toInt(dl["total"]) + toInt(rdl["total"])
	fetchTotal := toInt(f["total"])
	pushTotal := toInt(pu["total"])
	totalScore := float64(viewTotal)*0.2 + float64(fetchTotal)*2.0 + float64(pushTotal)*5
	result["activity_score"] = int64(totalScore)

	// actual_days: span from the earliest day with any activity.
	allActivityDays := map[string]bool{}
	for _, sm := range []map[any]any{viewStats, fetchStats, pushStats} {
		for k, v := range sm {
			if toInt(v) > 0 {
				if ks, ok := k.(string); ok {
					allActivityDays[ks] = true
				}
			}
		}
	}
	actualDays := lookbackDays
	if len(allActivityDays) > 0 {
		earliest := ""
		for d := range allActivityDays {
			if earliest == "" || d < earliest {
				earliest = d
			}
		}
		if earliestTS, err := time.ParseInLocation("2006-01-02", earliest, now.Location()); err == nil {
			spanSeconds := now.Sub(earliestTS).Seconds()
			actualDays = int(spanSeconds/float64(daySeconds)) + 1
			if actualDays < 1 {
				actualDays = 1
			}
		}
	}
	if actualDays > lookbackDays {
		actualDays = lookbackDays
	}
	dailyScore := 0.0
	if actualDays > 0 {
		dailyScore = totalScore / float64(actualDays)
	}
	result["actual_days"] = int64(actualDays)

	switch {
	case dailyScore == 0:
		result["activity_level"] = "inactive"
	case dailyScore < 3:
		result["activity_level"] = "low"
	case dailyScore < 10:
		result["activity_level"] = "moderate"
	default:
		result["activity_level"] = "high"
	}
	return result
}

// seriesMap returns a fresh per-metric dict, mirroring the initial
// {"daily": [], "total": 0, "peak": 0, "peak_day": None} (server.py:4607).
func seriesMap() map[any]any {
	return map[any]any{"daily": []any{}, "total": int64(0), "peak": int64(0), "peak_day": ""}
}

// buildSeries aggregates a day->count map over the day list, mirroring the
// per-metric loop in repository_stats (server.py:4623-4640).
func buildSeries(dayMap map[any]any, days []string) map[any]any {
	s := seriesMap()
	var daily []any
	for _, day := range days {
		count := int64(0)
		if dayMap != nil {
			count = toInt(dayMap[day])
		}
		daily = append(daily, count)
		s["total"] = toInt(s["total"]) + count
		if count > toInt(s["peak"]) {
			s["peak"] = count
			s["peak_day"] = day
		}
	}
	s["daily"] = daily
	return s
}

// buildSeriesCombined aggregates two day->count maps (downloads +
// release_downloads) summed per day, mirroring the downloads_combined loop
// (server.py:4676-4684).
func buildSeriesCombined(a, b map[any]any, days []string) map[any]any {
	s := seriesMap()
	var daily []any
	for _, day := range days {
		count := toInt(a[day]) + toInt(b[day])
		daily = append(daily, count)
		s["total"] = toInt(s["total"]) + count
		if count > toInt(s["peak"]) {
			s["peak"] = count
			s["peak_day"] = day
		}
	}
	s["daily"] = daily
	return s
}

// toInt coerces a msgpack-unpacked numeric value to int64.
func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case uint64:
		return int64(n)
	}
	return 0
}

// asAnyMap coerces a value to a map[any]any, returning nil for any other
// type. msgpack round-trips nested maps as map[any]any when unpacked with
// UnpackPreserveBinMapKeys.
func asAnyMap(v any) map[any]any {
	if m, ok := v.(map[any]any); ok {
		return m
	}
	return nil
}
