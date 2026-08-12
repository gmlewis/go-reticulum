// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// pages-handlers2.go implements the remaining nomadnetwork page-node request
// handlers, mirroring the serve_* methods of NomadNetworkNode
// (RNS/Utilities/rngit/pages.py, rngit v1.4.2): the repo, stats, releases,
// release, work, and work_doc page handlers plus the three file handlers
// (artifact, download, workdoc). It also implements the thanks counters,
// the upstream-sync lookup, the chart renderers used by the stats page, and
// the destination lifecycle helpers (registerRequestHandlers, announce,
// jobs, getAnnounceAppData) that the serve() wiring (Task 5) calls once the
// page destination exists.

package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/micron"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// pageJobsInterval mirrors JOBS_INTERVAL (pages.py:53), the seconds between
// periodic page-node jobs (announce cadence checks).
const pageJobsInterval = 5

// thanksDequeMax mirrors deque(maxlen=256) (pages.py:158): the bound on the
// in-memory thanks de-duplication deque.
const thanksDequeMax = 256

// lastUpstreamSync returns the last upstream-sync timestamp of a mirror/fork
// repo, mirroring last_upstream_sync (server.py:2763, __mirror_synced
// 2744-2756). Returns 0 when unset. The page-node repo handler calls this on
// its owner (the git-protocol node) rather than on itself, matching
// pages.py:468.
func (n *reticulumGitNode) lastUpstreamSync(repoPath string) int64 {
	out, ok := gitRun(repoPath, "config", "repository.rngit.upstream.sync")
	if !ok {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// thanksSeenOrAdd reports whether hash is already in the thanks
// de-duplication deque, inserting it (evicting the oldest past
// thanksDequeMax) when it is new. Returns true when the hash was already
// present, mirroring the `if thanks_hash in self.thanks_deque` guard
// (pages.py:2453-2455, 2478-2480). caller holds thanksMu via this method.
func (p *pageNode) thanksSeenOrAdd(hash []byte) bool {
	p.thanksMu.Lock()
	defer p.thanksMu.Unlock()
	for e := p.thanksDeque.Front(); e != nil; e = e.Next() {
		if b, ok := e.Value.([]byte); ok && bytes.Equal(b, hash) {
			return true
		}
	}
	p.thanksDeque.PushBack(hash)
	if p.thanksDeque.Len() > thanksDequeMax {
		p.thanksDeque.Remove(p.thanksDeque.Front())
	}
	return false
}

// repositoryThanks reads and optionally increments a repository's thanks
// counter, mirroring repository_thanks (pages.py:2451-2474). The counter is
// persisted as msgpack {"count": n} at {repoPath}.thanks. add deduplicates
// per link_id+repo_path via the in-memory deque.
func (p *pageNode) repositoryThanks(repoPath string, add bool, linkID []byte) int64 {
	if add {
		hash := rns.FullHash(bytes.Join([][]byte{linkID, []byte(repoPath)}, nil))
		if p.thanksSeenOrAdd(hash) {
			add = false
		}
	}
	thanksPath := repoPath + ".thanks"
	if !isFile(thanksPath) {
		count := int64(0)
		if add {
			count = 1
		}
		packed, err := msgpack.Pack(map[any]any{"count": count})
		if err != nil {
			return 0
		}
		if err := os.WriteFile(thanksPath, packed, 0o644); err != nil {
			return 0
		}
		return count
	}
	data, err := os.ReadFile(thanksPath)
	if err != nil {
		return 0
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return 0
	}
	tm, ok := unpacked.(map[any]any)
	if !ok {
		return 0
	}
	count := toInt(tm["count"])
	if add {
		count++
		packed, perr := msgpack.Pack(map[any]any{"count": count})
		if perr != nil {
			return 0
		}
		if err := os.WriteFile(thanksPath, packed, 0o644); err != nil {
			return 0
		}
	}
	return count
}

// releaseThanks reads and optionally increments a release's thanks counter,
// mirroring release_thanks (pages.py:2476-2499). The counter is persisted as
// msgpack {"count": n} at {releasePath}/THANKS.
func (p *pageNode) releaseThanks(releasePath string, add bool, linkID []byte) int64 {
	if add {
		hash := rns.FullHash(bytes.Join([][]byte{linkID, []byte(releasePath)}, nil))
		if p.thanksSeenOrAdd(hash) {
			add = false
		}
	}
	thanksPath := filepath.Join(releasePath, "THANKS")
	if !isFile(thanksPath) {
		count := int64(0)
		if add {
			count = 1
		}
		packed, err := msgpack.Pack(map[any]any{"count": count})
		if err != nil {
			return 0
		}
		if err := os.WriteFile(thanksPath, packed, 0o644); err != nil {
			return 0
		}
		return count
	}
	data, err := os.ReadFile(thanksPath)
	if err != nil {
		return 0
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(data)
	if err != nil {
		return 0
	}
	tm, ok := unpacked.(map[any]any)
	if !ok {
		return 0
	}
	count := toInt(tm["count"])
	if add {
		count++
		packed, perr := msgpack.Pack(map[any]any{"count": count})
		if perr != nil {
			return 0
		}
		if err := os.WriteFile(thanksPath, packed, 0o644); err != nil {
			return 0
		}
	}
	return count
}

// ownerDestHex returns the lowercase hex of the owner's repositories
// destination hash, mirroring RNS.hexrep(self.owner.destination.hash,
// delimit=False) (pages.py:438). Returns "" when the owner has no
// destination yet (e.g. before the serve wiring runs).
func (p *pageNode) ownerDestHex() string {
	if p.owner == nil || p.owner.destination == nil {
		return ""
	}
	return fmt.Sprintf("%x", p.owner.destination.Hash)
}

// serveRepoPage mirrors serve_repo_page (pages.py:420-537).
func (p *pageNode) serveRepoPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	ref := vstr(vars, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	thanks := vbool(vars, "thanks")

	if groupName == "" || repoName == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("repo", content, "", start)
	}

	navParts := []string{}
	repoURL := clrDim + "rns://" + p.ownerDestHex() + "/" + groupName + "/" + repoName + "`f"
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " + repoName + " " + repoURL
	navParts = append(navParts, breadcrumb+"\n")

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Not Found", 1) + "\nThe requested repository was not found.\n"
		return p.render("repo", content, strings.Join(navParts, ""), start)
	}

	if repo.fork != "" || repo.mirror != "" {
		var sourceType, sourceURL string
		switch {
		case repo.fork != "":
			sourceType, sourceURL = "fork", repo.fork
		case repo.mirror != "":
			sourceType, sourceURL = "mirror", repo.mirror
		}
		sourceLink := ""
		if strings.HasPrefix(strings.ToLower(sourceURL), "rns://") {
			// Recalling the upstream page destination requires a live
			// transport; without one the raw rns:// URL is shown as-is
			// (matching the Python except branch that sets "").
			comps := strings.Split(sourceURL, "/")
			if len(comps) == 5 && len(comps[2]) == rns.TruncatedHashLength/8*2 {
				sourceLink = ""
			}
		}
		synced := max(time.Now().Unix()-p.owner.lastUpstreamSync(repo.path), 0)
		syncTime, _, _ := strings.Cut(rns.PrettyTime(float64(synced), false, true), " ")
		syncStr := " `*" + clrDimH + "synced " + syncTime + " ago`f`*\n"
		sourceDesc := sourceType + "ed from"
		indentLen := max(len("Node / "+groupName+" / "+repoName)-len(sourceDesc), 0)
		if sourceLink != "" {
			sourceURL = sourceLink
		}
		desc := strings.ToUpper(sourceDesc[:1]) + sourceDesc[1:]
		navParts = append(navParts, clrDim+desc+strings.Repeat(" ", indentLen)+" "+sourceURL+"`f"+syncStr+"\n")
	}

	description := p.getRepositoryDescription(repo.path)
	if description != "" {
		description = description + "\n\n"
	}

	thanksCount := p.repositoryThanks(repo.path, thanks, linkID)

	contentParts := []string{description}

	heads, tags := p.getRepositoryRefs(repo.path)
	resolvedRef := p.resolveRef(repo.path, ref)
	commitsCount := 0
	if resolvedRef != "" {
		commitsCount = p.getCommitCount(repo.path, resolvedRef)
	}
	branchCount := len(heads)
	tagCount := len(tags)

	activeWorkDir := repo.path + ".work/active"
	workCount := 0
	if isDir(activeWorkDir) {
		if entries, err := os.ReadDir(activeWorkDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				if _, err := strconv.Atoi(e.Name()); err == nil {
					workCount++
				}
			}
		}
	}

	releasesPath := repo.path + ".releases"
	releases, _ := p.owner.releasesListData(releasesPath)
	releasesCount := 0
	for _, r := range releases {
		if s, _ := r["status"].(string); s == "published" {
			releasesCount++
		}
	}

	sep := p.icon("sep")
	contentParts = append(contentParts,
		mLinkR(p.icon("folder")+" Files", pagePathTree, []linkField{{"g", groupName}, {"r", repoName}, {"ref", "HEAD"}})+" "+sep+" ")
	if releasesCount > 0 {
		contentParts = append(contentParts,
			mLinkR(p.icon("package")+fmt.Sprintf(" Releases (%d)", releasesCount), pagePathReleases, []linkField{{"g", groupName}, {"r", repoName}})+" "+sep+" ")
	}
	contentParts = append(contentParts,
		mLinkR(p.icon("work")+fmt.Sprintf(" Work (%d)", workCount), pagePathWork, []linkField{{"g", groupName}, {"r", repoName}})+" "+sep+" ")
	contentParts = append(contentParts,
		mLinkR(p.icon("commits")+fmt.Sprintf(" Commits (%d)", commitsCount), pagePathCommits, []linkField{{"g", groupName}, {"r", repoName}, {"ref", "HEAD"}})+" "+sep+" ")
	contentParts = append(contentParts,
		mLinkR(p.icon("branch")+fmt.Sprintf(" Branches (%d)", branchCount), pagePathRefs, []linkField{{"g", groupName}, {"r", repoName}, {"type", "heads"}})+" "+sep+" ")
	contentParts = append(contentParts,
		mLinkR(p.icon("tag")+fmt.Sprintf(" Tags (%d)", tagCount), pagePathRefs, []linkField{{"g", groupName}, {"r", repoName}, {"type", "tags"}})+" "+sep+" ")
	contentParts = append(contentParts,
		mLinkR(p.icon("heart")+fmt.Sprintf(" Thanks (%d)", thanksCount), pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}, {"thanks", "y"}}))
	if p.resolvePermission(remoteIdentity, groupName, repoName, permStats) {
		contentParts = append(contentParts, " "+sep+" "+
			mLinkR(p.icon("stats")+" Stats", pagePathStats, []linkField{{"g", groupName}, {"r", repoName}}))
	}
	contentParts = append(contentParts, "\n\n<")

	readmeContent, readmeIsMarkdown, readmeFound := p.getReadmeContent(repo.path)
	if readmeFound {
		trimmed := strings.TrimLeft(readmeContent, " \t\n\r")
		if !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, ">") {
			contentParts = append(contentParts, mDivider())
		}
		if readmeIsMarkdown {
			urlScope := fmt.Sprintf(":/page/blob.mu`g=%s|r=%s|ref=%s|path=", groupName, repoName, ref)
			mdc := micron.NewConverter(micron.WithMaxWidth(maxRenderWidth), micron.WithHighlighter(p.highlighter), micron.WithURLScope(urlScope))
			contentParts = append(contentParts, mdc.FormatBlock(readmeContent))
		} else {
			contentParts = append(contentParts, "\n"+strings.TrimRight(readmeContent, " \t\n\r")+"\n")
		}
	} else {
		contentParts = append(contentParts, mDivider(), "\n", mItalic("No README file found in this repository."), "\n")
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	navContent := strings.Join(navParts, "")
	return p.render("repo", pageContent, navContent, start)
}

// statsSeries returns the per-metric series map from a repository_stats
// result, or nil when absent.
func statsSeries(stats map[any]any, key string) map[any]any {
	return asAnyMap(stats[key])
}

// seriesTotal returns a series "total", mirroring stats["views"]["total"].
func seriesTotal(s map[any]any) int64 { return toInt(s["total"]) }

// seriesPeak returns a series "peak".
func seriesPeak(s map[any]any) int64 { return toInt(s["peak"]) }

// seriesToday returns the last daily value, mirroring stats["views"]["daily"][-1].
func seriesToday(s map[any]any) int64 {
	if daily, ok := s["daily"].([]any); ok && len(daily) > 0 {
		return toInt(daily[len(daily)-1])
	}
	return 0
}

// seriesDailyInts returns a series "daily" as []int for the chart renderers.
func seriesDailyInts(s map[any]any) []int {
	daily, _ := s["daily"].([]any)
	out := make([]int, 0, len(daily))
	for _, v := range daily {
		out = append(out, int(toInt(v)))
	}
	return out
}

// statsTimelineLabels returns the timeline_labels of a repository_stats
// result as strings.
func statsTimelineLabels(stats map[any]any) []string {
	raw, _ := stats["timeline_labels"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// serveStatsPage mirrors serve_stats_page (pages.py:1147-1245).
func (p *pageNode) serveStatsPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")

	if groupName == "" || repoName == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("stats", content, "", start)
	}

	navParts := []string{}
	repoLink := mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}})
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " + repoLink + " / stats"
	navParts = append(navParts, breadcrumb+"\n")
	navContent := strings.Join(navParts, "")

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	statsPermission := p.resolvePermission(remoteIdentity, groupName, repoName, permStats)
	if repo == nil || !statsPermission {
		content := mHeading("Error", 2) + "\nThe requested repository was not found.\n"
		return p.render("stats", content, navContent, start)
	}

	remote := remoteIdentity
	if remote == nil {
		remote = p.nullIdent
	}
	stats := p.owner.repositoryStats(remote, groupName, repoName, 90)
	if stats == nil {
		content := mHeading("Stats Unavailable", 2) + "\nCould not retrieve statistics for this repository.\n"
		return p.render("stats", content, navContent, start)
	}

	activityColors := map[string][2]string{
		"inactive": {"`F666", "No activity"},
		"low":      {"`F66d", "Low activity"},
		"moderate": {"`Faa0", "Moderate activity"},
		"high":     {"`F0a0", "High activity"},
	}
	activityLevel, _ := stats["activity_level"].(string)
	ac, ok := activityColors[activityLevel]
	if !ok {
		ac = [2]string{"`F666", "Unknown"}
	}
	actColor, actLabel := ac[0], ac[1]

	contentParts := []string{mHeading("Stats for "+repoName, 2)}

	views := statsSeries(stats, "views")
	fetches := statsSeries(stats, "fetches")
	pushes := statsSeries(stats, "pushes")
	downloads := statsSeries(stats, "downloads_combined")

	vTotal, vPeak, vTday := seriesTotal(views), seriesPeak(views), seriesToday(views)
	fTotal, fPeak, fTday := seriesTotal(fetches), seriesPeak(fetches), seriesToday(fetches)
	pTotal, pPeak, pTday := seriesTotal(pushes), seriesPeak(pushes), seriesToday(pushes)
	dTotal, dPeak, dTday := seriesTotal(downloads), seriesPeak(downloads), seriesToday(downloads)

	contentParts = append(contentParts,
		fmt.Sprintf("\n`FT%sFetches`f   : %5d  total %s  today: %3d  peak: %3d \n`f", rclrFetch, fTotal, clrDim, fTday, fPeak),
		fmt.Sprintf("`FT%sPushes`f    : %5d  total %s  today: %3d  peak: %3d \n`f", rclrPush, pTotal, clrDim, pTday, pPeak),
		fmt.Sprintf("`FT%sViews`f     : %5d  total %s  today: %3d  peak: %3d `f\n", rclrView, vTotal, clrDim, vTday, vPeak),
		fmt.Sprintf("`FT%sDownloads`f : %5d  total %s  today: %3d  peak: %3d `f\n", rclrDownload, dTotal, clrDim, dTday, dPeak),
		fmt.Sprintf("`F0aaActivity`f  : %5d points\n\n", toInt(stats["activity_score"])),
		fmt.Sprintf("%s%s`f over the last %d days (%s)\n\n", actColor, actLabel, toInt(stats["actual_days"]), fmt.Sprint(stats["date_range"])),
	)

	timelineLabels := statsTimelineLabels(stats)
	if fTotal > 0 {
		contentParts = append(contentParts, mHeading("Fetches", 2), "\n",
			renderChart(seriesDailyInts(fetches), timelineLabels, rclrFetch, 10), "\n")
	}
	if pTotal > 0 {
		contentParts = append(contentParts, mHeading("Pushes", 2), "\n",
			renderChart(seriesDailyInts(pushes), timelineLabels, rclrPush, 10), "\n")
	}
	if vTotal > 0 {
		contentParts = append(contentParts, mHeading("Views", 2), "\n",
			renderChart(seriesDailyInts(views), timelineLabels, rclrView, 10), "\n")
	}
	if dTotal > 0 {
		contentParts = append(contentParts, mHeading("Downloads", 2), "\n",
			renderChart(seriesDailyInts(downloads), timelineLabels, rclrDownload, 10), "\n")
	}
	if toInt(stats["activity_score"]) > 0 {
		contentParts = append(contentParts, mHeading("Combined Activity", 2), "\n",
			renderCombinedChart(seriesDailyInts(views), seriesDailyInts(fetches), seriesDailyInts(pushes), seriesDailyInts(downloads), timelineLabels))
	} else {
		contentParts = append(contentParts, mItalic("\nNo development activity recorded for this repository in the selected time period.\n\n"))
	}

	pageContent := strings.Join(contentParts, "")
	return p.render("stats", pageContent, navContent, start)
}

// serveReleasesPage mirrors serve_releases_page (pages.py:1247-1308).
func (p *pageNode) serveReleasesPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")

	if groupName == "" || repoName == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("releases", content, "", start)
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Error", 2) + "\nThe requested repository was not found.\n"
		return p.render("releases", content, "", start)
	}

	navParts := []string{}
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " +
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}) + " / releases"
	navParts = append(navParts, breadcrumb+"\n")
	navContent := strings.Join(navParts, "")

	releasesPath := repo.path + ".releases"
	releases, latestRelease := p.owner.releasesListData(releasesPath)
	if len(releases) == 0 {
		contentParts := []string{mHeading("Releases", 2), "\nNo releases available for this repository.\n"}
		return p.render("releases", strings.Join(contentParts, ""), navContent, start)
	}

	var published []map[any]any
	for _, r := range releases {
		if s, _ := r["status"].(string); s == "published" {
			published = append(published, r)
		}
	}

	contentParts := []string{mHeading(fmt.Sprintf("Releases (%d)", len(published)), 2), "\n"}
	sep := p.icon("sep")
	for _, rel := range published {
		tag, _ := rel["tag"].(string)
		if tag == "" {
			tag = "unknown"
		}
		created := toInt(rel["created"])
		dateStr := "unknown"
		if created > 0 {
			dateStr = time.Unix(created, 0).Format("2006-01-02")
		}
		artifacts := toInt(rel["artifacts"])
		relFormat, _ := rel["format"].(string)
		if relFormat == "" {
			relFormat = "markdown"
		}
		previewStr, _ := rel["preview"].(string)
		preview := truncateEllipsis(firstLine(previewStr), 2048)

		link := mLink(tag, pagePathRelease, []linkField{{"g", groupName}, {"r", repoName}, {"t", tag}})
		latestStr := ""
		if tag == latestRelease {
			latestStr = " " + sep + " " + clrOKDim + "`*Latest`*`f"
		}
		noun := "artifacts"
		if artifacts == 1 {
			noun = "artifact"
		}
		artifactsStr := fmt.Sprintf("`*%d %s`*", artifacts, noun)
		contentParts = append(contentParts, fmt.Sprintf("%s %s%s %s %s%s`f\n", link, clrDim, dateStr, sep, artifactsStr, latestStr))
		if preview != "" {
			switch relFormat {
			case "markdown":
				contentParts = append(contentParts, p.mdc.FormatBlock(preview)+"\n")
			case "micron":
				contentParts = append(contentParts, preview+"\n")
			default:
				contentParts = append(contentParts, mEscape(preview)+"\n")
			}
		}
		contentParts = append(contentParts, "\n")
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.TrimRight(strings.Join(contentParts, ""), " \t\n\r") + "\n"
	return p.render("releases", pageContent, navContent, start)
}

// serveReleasePage mirrors serve_release_page (pages.py:1310-1409).
func (p *pageNode) serveReleasePage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	tag := vstr(vars, "t")

	if groupName == "" || repoName == "" || tag == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("release", content, "", start)
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Error", 2) + "\nThe requested repository was not found.\n"
		return p.render("release", content, "", start)
	}

	releasesPath := repo.path + ".releases"
	if tag == "latest" {
		releases, latestRelease := p.owner.releasesListData(releasesPath)
		if len(releases) == 0 {
			content := mHeading("Release Not Found", 2) + "\nNo releases exist.\n"
			return p.render("release", content, "", start)
		}
		if latestRelease == "" {
			sort.SliceStable(releases, func(i, j int) bool {
				return toInt(releases[i]["created"]) > toInt(releases[j]["created"])
			})
			t, _ := releases[0]["tag"].(string)
			tag = t
		} else {
			tag = latestRelease
		}
	}

	navParts := []string{}
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " +
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}) + " / " +
		mLink("releases", pagePathReleases, []linkField{{"g", groupName}, {"r", repoName}}) + " / " + tag
	navParts = append(navParts, breadcrumb+"\n")
	navContent := strings.Join(navParts, "")

	releaseDir := filepath.Join(releasesPath, tag)
	if !isDir(releaseDir) {
		content := mHeading("Release Not Found", 2) + "\nThe release " + tag + " does not exist.\n"
		return p.render("release", content, navContent, start)
	}
	releaseInfo := p.owner.releaseData(releaseDir, tag)
	if releaseInfo == nil {
		content := mHeading("Error", 2) + "\nCould not load release data.\n"
		return p.render("release", content, navContent, start)
	}
	if s, _ := releaseInfo["status"].(string); s != "published" {
		content := mHeading("Release Not Found", 2) + "\nThe release " + tag + " does not exist.\n"
		return p.render("release", content, navContent, start)
	}

	sep := p.icon("sep")
	thanks := vbool(vars, "thanks")
	thanksCount := p.releaseThanks(releaseDir, thanks, linkID)
	contentParts := []string{
		mLinkR(p.icon("heart")+fmt.Sprintf(" Thanks (%d)", thanksCount), pagePathRelease, []linkField{{"g", groupName}, {"r", repoName}, {"t", tag}, {"thanks", "y"}}) + "\n\n",
	}

	created := toInt(releaseInfo["created"])
	tsStr := ""
	if created > 0 {
		tsStr = " " + sep + " " + time.Unix(created, 0).Format("2006-01-02 15:04:05")
	}
	contentParts = append(contentParts, mHeading("Release "+tag+tsStr, 2), "\n")

	notes, _ := releaseInfo["notes"].(string)
	if notes != "" {
		notesFormat, _ := releaseInfo["notes_format"].(string)
		switch notesFormat {
		case "micron":
			contentParts = append(contentParts, notes+"\n")
		case "markdown":
			contentParts = append(contentParts, p.mdc.FormatBlock(notes)+"\n")
		default:
			contentParts = append(contentParts, "`="+notes+"`=\n")
		}
		contentParts = append(contentParts, "\n")
	}

	artifacts, _ := releaseInfo["artifacts"].([]map[any]any)
	if len(artifacts) > 0 {
		contentParts = append(contentParts, mHeading(fmt.Sprintf("Artifacts (%d)", len(artifacts)), 2), "\n")
		sort.SliceStable(artifacts, func(i, j int) bool {
			ni, _ := artifacts[i]["name"].(string)
			nj, _ := artifacts[j]["name"].(string)
			return ni < nj
		})
		for _, art := range artifacts {
			name, _ := art["name"].(string)
			if name == "" {
				name = "unknown"
			}
			size := toInt(art["size"])
			sizeStr := "0 B"
			if size > 0 {
				sizeStr = rns.PrettySize(float64(size), "B")
			}
			lstr1 := p.icon("file") + " " + mEscape(name)
			lstr2 := "(" + sizeStr + ")"
			link1 := mLinkR(lstr1, fileArtifact, []linkField{{"g", groupName}, {"r", repoName}, {"t", tag}, {"a", name}})
			link2 := mLinkR(lstr2, fileArtifact, []linkField{{"g", groupName}, {"r", repoName}, {"t", tag}, {"a", name}})
			contentParts = append(contentParts, link1+" "+clrDim+link2+"`f\n")
		}
	} else {
		contentParts = append(contentParts, mHeading("Artifacts", 2), "\n`*No artifacts for this release`*\n")
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	return p.render("release", pageContent, navContent, start)
}

// serveWorkPage mirrors serve_work_page (pages.py:1411-1508).
func (p *pageNode) serveWorkPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	scope := vstr(vars, "scope")
	if scope == "" {
		scope = "active"
	}
	switch scope {
	case "active", "completed", "proposed", "all":
	default:
		scope = "active"
	}

	if groupName == "" || repoName == "" {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("work", content, "", start)
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Error", 2) + "\nThe requested repository was not found.\n"
		return p.render("work", content, "", start)
	}

	navParts := []string{}
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " +
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}) + " / work"
	navParts = append(navParts, breadcrumb+"\n")
	navContent := strings.Join(navParts, "")

	sep := p.icon("sep")
	scopeActive := ""
	if scope == "active" {
		scopeActive = "`_"
	}
	scopeCmplt := ""
	if scope == "completed" {
		scopeCmplt = "`_"
	}
	scopePrpsd := ""
	if scope == "proposed" {
		scopePrpsd = "`_"
	}
	scopeAll := ""
	if scope == "all" {
		scopeAll = "`_"
	}
	filterLinks := []string{
		scopeActive + mLink("Active", pagePathWork, []linkField{{"g", groupName}, {"r", repoName}, {"scope", "active"}}) + scopeActive,
		scopeCmplt + mLink("Completed", pagePathWork, []linkField{{"g", groupName}, {"r", repoName}, {"scope", "completed"}}) + scopeCmplt,
		scopePrpsd + mLink("Proposed", pagePathWork, []linkField{{"g", groupName}, {"r", repoName}, {"scope", "proposed"}}) + scopePrpsd,
		scopeAll + mLink("All", pagePathWork, []linkField{{"g", groupName}, {"r", repoName}, {"scope", "all"}}) + scopeAll,
	}

	contentParts := []string{strings.Join(filterLinks, " "+sep+" ") + "\n\n"}

	workPath := repo.path + ".work"
	var scopesToShow []string
	if scope == "all" {
		scopesToShow = []string{"active", "completed", "proposed"}
	} else {
		scopesToShow = []string{scope}
	}

	for _, s := range scopesToShow {
		folderPath := filepath.Join(workPath, s)
		type workDoc struct {
			id       int
			title    string
			created  int64
			edited   int64
			author   []byte
			comments int
		}
		var docs []workDoc
		if isDir(folderPath) {
			if entries, err := os.ReadDir(folderPath); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					docID, err := strconv.Atoi(entry.Name())
					if err != nil {
						continue
					}
					if !p.resolveDocPermission(remoteIdentity, groupName, repoName, docID, permRead) {
						continue
					}
					docDir := filepath.Join(folderPath, entry.Name())
					rootPath := filepath.Join(docDir, "root")
					if !isFile(rootPath) {
						continue
					}
					doc := workLoadDocument(rootPath)
					if doc == nil {
						continue
					}
					meta := asAnyMap(doc["meta"])
					commentCount := 0
					if commentEntries, err := os.ReadDir(docDir); err == nil {
						for _, ce := range commentEntries {
							if !ce.Type().IsRegular() {
								continue
							}
							if _, err := strconv.Atoi(ce.Name()); err == nil {
								commentCount++
							}
						}
					}
					docs = append(docs, workDoc{
						id:       docID,
						title:    metaString(meta, "title", "Untitled"),
						created:  int64(metaFloat(meta, "created")),
						edited:   int64(metaFloat(meta, "edited")),
						author:   metaBytes(meta, "author"),
						comments: commentCount,
					})
				}
			}
		}
		sort.SliceStable(docs, func(i, j int) bool {
			return max64(docs[i].created, docs[i].edited) > max64(docs[j].created, docs[j].edited)
		})

		if len(docs) == 0 {
			contentParts = append(contentParts, mHeading(fmt.Sprintf("%s (%d)", capitalize(s), len(docs)), 2)+
				fmt.Sprintf("\n`*No %s work documents`*\n", s), "\n")
		} else {
			contentParts = append(contentParts, mHeading(fmt.Sprintf("%s (%d)", capitalize(s), len(docs)), 2), "\n")
			for _, doc := range docs {
				docTitle := truncateEllipsis(doc.title, 92)
				titleLink := mLink(p.icon("file")+" "+docTitle, pagePathWorkDoc, []linkField{{"g", groupName}, {"r", repoName}, {"id", int64(doc.id)}, {"scope", s}})
				authorStr := "unknown"
				if len(doc.author) > 0 {
					authorStr = rns.PrettyHexRep(doc.author)
				}
				dateStr := ""
				if doc.created > 0 {
					dateStr = time.Unix(doc.created, 0).Format("2006-01-02")
				}
				contentParts = append(contentParts,
					fmt.Sprintf("%s %s#%d`f\n", titleLink, clrDim, doc.id),
					fmt.Sprintf("%s%s by %s`f\n", clrDim, dateStr, authorStr),
				)
				if doc.comments > 0 {
					contentParts = append(contentParts, fmt.Sprintf("%s%d updates`f\n", clrDim, doc.comments))
				}
				contentParts = append(contentParts, "\n")
			}
		}
	}

	if len(contentParts) > 0 && contentParts[len(contentParts)-1] == "\n" {
		contentParts[len(contentParts)-1] = ""
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	return p.render("work", pageContent, navContent, start)
}

// serveWorkDocPage mirrors serve_work_doc_page (pages.py:1510-1656).
func (p *pageNode) serveWorkDocPage(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	start := time.Now()
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	docID := vint(vars, "id", -1)
	scope := vstr(vars, "scope")
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "active", "completed", "proposed", "all":
	default:
		scope = "active"
	}

	if groupName == "" || repoName == "" || docID < 0 {
		content := mHeading("Error", 2) + "\nInvalid request\n"
		return p.render("work_doc", content, "", start)
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		content := mHeading("Error", 2) + "\nThe requested repository was not found\n"
		return p.render("work_doc", content, "", start)
	}

	if !p.resolveDocPermission(remoteIdentity, groupName, repoName, docID, permRead) {
		content := mHeading("Error", 2) + "\nThe requested work document was not found\n"
		return p.render("work_doc", content, "", start)
	}

	workPath := repo.path + ".work"
	activeDir := filepath.Join(workPath, "active", strconv.Itoa(docID))
	completedDir := filepath.Join(workPath, "completed", strconv.Itoa(docID))
	proposedDir := filepath.Join(workPath, "proposed", strconv.Itoa(docID))
	var docDir string
	switch scope {
	case "active":
		docDir = activeDir
	case "completed":
		docDir = completedDir
	case "proposed":
		docDir = proposedDir
	case "all":
		if isDir(activeDir) {
			docDir = activeDir
			scope = "active"
		} else if isDir(completedDir) {
			docDir = completedDir
			scope = "completed"
		} else {
			docDir = proposedDir
			scope = "proposed"
		}
	}

	rootPath := filepath.Join(docDir, "root")
	if !isFile(rootPath) {
		content := mHeading("Not Found", 2) + "\nThe requested work document was not found\n"
		return p.render("work_doc", content, "", start)
	}

	doc := workLoadDocument(rootPath)
	if doc == nil {
		content := mHeading("Error", 2) + "\nCould not load work document\n"
		return p.render("work_doc", content, "", start)
	}

	// Breadcrumb navigation
	dlLink := mLink("Download", fileWorkdoc, []linkField{{"g", groupName}, {"r", repoName}, {"id", int64(docID)}})
	breadcrumb := ">>\n" + mLink("Node", pagePathIndex, nil) + " / " +
		mLink(groupName, pagePathGroup, []linkField{{"g", groupName}}) + " / " +
		mLink(repoName, pagePathRepo, []linkField{{"g", groupName}, {"r", repoName}}) + " / " +
		mLink("work", pagePathWork, []linkField{{"g", groupName}, {"r", repoName}}) + " / #" + strconv.Itoa(docID)
	navContent := breadcrumb + "\n\n" + dlLink + "\n"

	meta := asAnyMap(doc["meta"])
	title := metaString(meta, "title", "Untitled")
	docTitle := truncateEllipsis(title, 256)
	author := metaBytes(meta, "author")
	authorStr := "Unknown"
	if len(author) > 0 {
		authorStr = rns.PrettyHexRep(author)
	}
	signature := metaBytes(meta, "signature")
	pubkey := metaBytes(meta, "identity")
	created := int64(metaFloat(meta, "created"))
	edited := int64(metaFloat(meta, "edited"))
	fmtStr := metaString(meta, "format", "markdown")
	content, _ := doc["content"].(string)

	signatureStr := "Document not signed"
	if len(signature) == signatureLength && len(pubkey) == rns.IdentityKeySize/8 {
		signatureStr = "Not valid"
		id, err := rns.NewIdentity(false, nil)
		if err == nil {
			if err := id.LoadPublicKey(pubkey); err == nil {
				if id.Verify(signature, []byte(content)) {
					signatureStr = "Valid"
				}
			}
		}
	}

	contentParts := []string{
		mHeading(docTitle, 2),
		fmt.Sprintf("\n%sAuthor    : %s`f\n", clrDim, authorStr),
		fmt.Sprintf("%sSignature : %s`f\n", clrDim, signatureStr),
	}
	createdStr := "unknown"
	if created > 0 {
		createdStr = time.Unix(created, 0).Format("2006-01-02 15:04")
	}
	contentParts = append(contentParts, fmt.Sprintf("%sCreated   : %s`f\n", clrDim, createdStr))
	if edited > 0 && edited != created {
		contentParts = append(contentParts, fmt.Sprintf("%sEdited    : %s`f\n", clrDim, time.Unix(edited, 0).Format("2006-01-02 15:04")))
	}
	contentParts = append(contentParts, fmt.Sprintf("%sStatus    : %s`f\n\n", clrDim, capitalize(scope)))

	stripped := strings.TrimSpace(content)
	if stripped != "" {
		if fmtStr == "micron" {
			contentParts = append(contentParts, stripped)
		} else {
			contentParts = append(contentParts, p.mdc.FormatBlock(stripped))
		}
		contentParts = append(contentParts, "\n")
	}

	// Comments
	type commentEntry struct {
		id      int
		format  string
		content string
		created int64
		author  []byte
	}
	var comments []commentEntry
	if isDir(docDir) {
		if entries, err := os.ReadDir(docDir); err == nil {
			for _, entry := range entries {
				if _, err := strconv.Atoi(entry.Name()); err != nil {
					continue
				}
				commentPath := filepath.Join(docDir, entry.Name())
				if !isFile(commentPath) {
					continue
				}
				commentID, err := strconv.Atoi(entry.Name())
				if err != nil {
					continue
				}
				comment := workLoadDocument(commentPath)
				if comment == nil {
					continue
				}
				cmeta := asAnyMap(comment["meta"])
				cFmt, _ := comment["format"].(string)
				if cFmt == "" {
					cFmt = "markdown"
				}
				cContent, _ := comment["content"].(string)
				comments = append(comments, commentEntry{
					id:      commentID,
					format:  cFmt,
					content: cContent,
					created: int64(metaFloat(cmeta, "created")),
					author:  metaBytes(cmeta, "author"),
				})
			}
		}
	}
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].id < comments[j].id })

	if len(comments) > 0 {
		contentParts = append(contentParts, "\n"+mHeading(fmt.Sprintf("Updates (%d)", len(comments)), 2))
		for _, c := range comments {
			var rendered string
			if c.format == "markdown" {
				rendered = p.mdc.FormatBlock(c.content)
			} else {
				rendered = c.content
			}
			cAuthorStr := "Unknown"
			if len(c.author) > 0 {
				cAuthorStr = rns.PrettyHexRep(c.author)
			}
			cDate := "unknown"
			if c.created > 0 {
				cDate = time.Unix(c.created, 0).Format("2006-01-02 15:04")
			}
			contentParts = append(contentParts,
				fmt.Sprintf("\n%s#%d by %s on %s`f\n", clrDim, c.id, cAuthorStr, cDate),
				rendered+"\n",
			)
		}
	}

	p.owner.viewSucceeded(&groupName, &repoName, remoteIdentity)
	pageContent := strings.Join(contentParts, "")
	return p.render("work_doc", pageContent, navContent, start)
}

// serveArtifact mirrors serve_artifact (pages.py:1658-1716), a file handler
// returning [contentBytes, {"name": nameBytes}].
func (p *pageNode) serveArtifact(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	tag := vstr(vars, "t")
	artifact := vstr(vars, "a")
	if unq, err := url.QueryUnescape(artifact); err == nil {
		artifact = unq
	}
	if strings.Contains(artifact, "/") {
		return nil
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		return nil
	}

	releasesPath := repo.path + ".releases"
	if tag == "latest" {
		releases, latestRelease := p.owner.releasesListData(releasesPath)
		if len(releases) == 0 {
			return nil
		}
		if latestRelease == "" {
			sort.SliceStable(releases, func(i, j int) bool {
				return toInt(releases[i]["created"]) > toInt(releases[j]["created"])
			})
			t, _ := releases[0]["tag"].(string)
			tag = t
		} else {
			tag = latestRelease
		}
	}

	releaseDir := filepath.Join(releasesPath, tag)
	artifactsDir := filepath.Join(releaseDir, "artifacts")
	artifactPath := filepath.Join(artifactsDir, artifact)

	releaseInfo := p.owner.releaseData(releaseDir, tag)
	if releaseInfo == nil {
		return nil
	}
	if s, _ := releaseInfo["status"].(string); s != "published" {
		return nil
	}
	if !isDir(releaseDir) || !isDir(artifactsDir) || !isFile(artifactPath) {
		return nil
	}

	content, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil
	}
	p.owner.releaseDownloadSucceeded(groupName, repoName, remoteIdentity)
	return []any{content, map[any]any{"name": []byte(artifact)}}
}

// serveDownload mirrors serve_download (pages.py:1718-1761), a file handler
// returning [contentBytes, {"name": nameBytes}].
func (p *pageNode) serveDownload(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	ref := vstr(vars, "ref")
	if ref == "" {
		ref = "HEAD"
	}
	filePath := vstr(vars, "path")
	if unq, err := url.QueryUnescape(filePath); err == nil {
		filePath = unq
	}
	fileName := baseOf(filePath)

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		return nil
	}
	resolvedRef := p.resolveRef(repo.path, ref)
	if resolvedRef == "" {
		return nil
	}
	if filePath == "" {
		return nil
	}
	blobInfo := p.getBlobInfo(repo.path, resolvedRef, filePath)
	if blobInfo == nil {
		return nil
	}
	stream, ok := p.getBlobStream(repo.path, resolvedRef, filePath)
	if !ok {
		return nil
	}
	p.owner.downloadSucceeded(groupName, repoName, remoteIdentity)
	return []any{stream, map[any]any{"name": []byte(fileName)}}
}

// serveWdDownload mirrors serve_wd_download (pages.py:1763-1836), a file
// handler returning [fileName (string), contentBytes]. Unlike the artifact
// and download handlers, the workdoc handler returns the filename as the
// first element and the content as the second, matching the Python source.
func (p *pageNode) serveWdDownload(path string, data []byte, requestID, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	vars := unpackPageVars(data)
	groupName := vstr(vars, "g")
	repoName := vstr(vars, "r")
	docID := vint(vars, "id", -1)
	scope := vstr(vars, "scope")
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "active", "completed", "all":
	default:
		scope = "active"
	}

	if groupName == "" || repoName == "" || docID < 0 {
		return nil
	}

	repo := p.getAccessibleRepository(remoteIdentity, groupName, repoName)
	if repo == nil {
		return nil
	}
	if !p.resolveDocPermission(remoteIdentity, groupName, repoName, docID, permRead) {
		return nil
	}

	workPath := repo.path + ".work"
	activeDir := filepath.Join(workPath, "active", strconv.Itoa(docID))
	completedDir := filepath.Join(workPath, "completed", strconv.Itoa(docID))
	proposedDir := filepath.Join(workPath, "proposed", strconv.Itoa(docID))
	var docDir string
	switch scope {
	case "active":
		docDir = activeDir
	case "completed":
		docDir = completedDir
	case "all":
		if isDir(activeDir) {
			docDir = activeDir
		} else if isDir(completedDir) {
			docDir = completedDir
		} else {
			docDir = proposedDir
		}
	}

	rootPath := filepath.Join(docDir, "root")
	if !isFile(rootPath) {
		return nil
	}
	doc := workLoadDocument(rootPath)
	if doc == nil {
		return nil
	}
	meta := asAnyMap(doc["meta"])
	fmtStr := metaString(meta, "format", "markdown")
	title := truncateEllipsis(metaString(meta, "title", "Untitled"), 256)
	content, _ := doc["content"].(string)
	stripped := strings.TrimSpace(content)
	if stripped == "" {
		return nil
	}
	fileName := title + ".md"
	if fmtStr == "micron" {
		fileName = title + ".mu"
	}
	p.owner.downloadSucceeded(groupName, repoName, remoteIdentity)
	return []any{fileName, []byte(stripped)}
}

// getAnnounceAppData mirrors get_announce_app_data (pages.py:215): the
// node name is the announce app-data payload.
func (p *pageNode) getAnnounceAppData() []byte {
	return []byte(p.nodeName)
}

// announce mirrors announce (pages.py:217-220).
func (p *pageNode) announce() {
	if p.destination == nil {
		return
	}
	p.lastAnnounce = time.Now().Unix()
	_ = p.destination.Announce(p.getAnnounceAppData())
}

// jobs mirrors jobs (pages.py:207-213): a periodic loop that re-announces
// the page destination when the announce interval has elapsed. It runs until
// shouldRun is cleared.
func (p *pageNode) jobs() {
	for p.shouldRun.Load() {
		time.Sleep(time.Duration(pageJobsInterval) * time.Second)
		if !p.shouldRun.Load() {
			break
		}
		if p.announceInterval > 0 {
			now := time.Now().Unix()
			if now > p.lastAnnounce+p.announceInterval/int64(time.Second) {
				p.announce()
			}
		}
	}
}

// registerRequestHandlers mirrors register_request_handlers
// (pages.py:235-251): it binds all 16 page/file paths to their handlers on
// the page destination with ALLOW_ALL. It is a no-op until the destination
// is wired up by the serve() setup (Task 5).
func (p *pageNode) registerRequestHandlers() {
	if p.destination == nil {
		return
	}
	d := p.destination
	d.RegisterRequestHandler(pagePathIndex, p.serveFrontPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathGroup, p.serveGroupPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathRepo, p.serveRepoPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathTree, p.serveTreePage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathBlob, p.serveBlobPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathCommits, p.serveCommitsPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathCommit, p.serveCommitPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathRefs, p.serveRefsPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathStats, p.serveStatsPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathReleases, p.serveReleasesPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathRelease, p.serveReleasePage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathWork, p.serveWorkPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(pagePathWorkDoc, p.serveWorkDocPage, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(fileArtifact, p.serveArtifact, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(fileDownload, p.serveDownload, rns.AllowAll, nil, false)
	d.RegisterRequestHandler(fileWorkdoc, p.serveWdDownload, rns.AllowAll, nil, false)
}

// firstLine returns the first line of s (up to the first \n or \r), mirroring
// Python str.splitlines()[0]. Returns "" for an empty string.
func firstLine(s string) string {
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		return s[:i]
	}
	return s
}

// truncateEllipsis truncates s to n Unicode code points, appending an
// ellipsis when it was longer, mirroring `s[:n] + ("…" if len(s)>n else "")`.
func truncateEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// max64 returns the larger of two int64 values.
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// renderCombinedChart renders a stacked activity chart, mirroring
// render_combined_chart (pages.py:2605-2689). The four series are stacked
// per day in the order pushes, fetches, views, downloads. Returns
// "No data available\n" when every series is empty or all-zero.
func renderCombinedChart(views, fetches, pushes, downloads []int, labels []string) string {
	if len(views) == 0 || allZero(views) && allZero(fetches) && allZero(pushes) && allZero(downloads) {
		return "No data available\n"
	}
	const height = 6
	categories := []string{"pushes", "fetches", "views", "downloads"}
	catData := [][]int{pushes, fetches, views, downloads}
	catColors := map[string]string{
		"views":     gradientColorStr(rclrView, "000000", 0.87),
		"fetches":   gradientColorStr(rclrFetch, "000000", 0.87),
		"pushes":    gradientColorStr(rclrPush, "000000", 0.87),
		"downloads": gradientColorStr(rclrDownload, "000000", 0.87),
	}

	numPoints := len(views)
	legendParts := make([]string, 0, len(categories))
	for _, cat := range categories {
		col := catColors[cat]
		legendParts = append(legendParts, fmt.Sprintf("`FT%s`BT%s██`f`b %s", col, col, capitalize(cat)))
	}
	legend := strings.Join(legendParts, "  ")
	lines := []string{legend + "\n\n"}

	for row := height; row > 0; row-- {
		lowerMin := float64(row-1) / float64(height)
		lowerMax := (float64(row) - 0.5) / float64(height)
		upperMin := (float64(row) - 0.5) / float64(height)
		upperMax := float64(row) / float64(height)

		var line strings.Builder
		line.WriteString("│")
		for i := range numPoints {
			total := 0
			for _, d := range catData {
				if i < len(d) {
					total += d[i]
				}
			}
			if total == 0 {
				line.WriteString(" ")
				continue
			}
			cumsum := 0
			catRanges := map[string][2]float64{}
			for ci, cat := range categories {
				start := float64(cumsum) / float64(total)
				if ci < len(catData) && i < len(catData[ci]) {
					cumsum += catData[ci][i]
				}
				end := float64(cumsum) / float64(total)
				catRanges[cat] = [2]float64{start, end}
			}
			upperCat := pixelToCat(catRanges, categories, upperMin, upperMax)
			lowerCat := pixelToCat(catRanges, categories, lowerMin, lowerMax)
			switch {
			case upperCat == "" && lowerCat == "":
				line.WriteString(" ")
			case upperCat == lowerCat && upperCat != "":
				col := catColors[upperCat]
				line.WriteString(fmt.Sprintf("`FT%s`BT%s█`f`b", col, col))
			case upperCat != "" && lowerCat != "":
				line.WriteString(fmt.Sprintf("`FT%s`BT%s▀`f`b", catColors[upperCat], catColors[lowerCat]))
			case upperCat != "":
				col := catColors[upperCat]
				line.WriteString(fmt.Sprintf("`FT%s▀`f", col))
			default:
				col := catColors[lowerCat]
				line.WriteString(fmt.Sprintf("`FT%s▄`f", col))
			}
		}
		lines = append(lines, line.String()+"\n")
	}

	bottom := "└" + strings.Repeat("─", numPoints) + "┘"
	lines = append(lines, bottom+"\n")
	if len(labels) > 0 {
		first := firstLine(fmt.Sprint(labels[0]))
		if len([]rune(first)) > 12 {
			first = string([]rune(first)[:12])
		}
		last := firstLine(fmt.Sprint(labels[len(labels)-1]))
		if len([]rune(last)) > 12 {
			last = string([]rune(last)[:12])
		}
		midSpace := max(len(bottom)-len(first)-len(last), 0)
		lines = append(lines, clrDim+first+strings.Repeat(" ", midSpace)+last+"`f\n")
	}
	return strings.Join(lines, "")
}

// pixelToCat returns the category whose range overlaps [pmin, pmax),
// mirroring the pixel_to_cat closure (pages.py:2652-2658). Returns "" when
// none overlap.
func pixelToCat(ranges map[string][2]float64, categories []string, pmin, pmax float64) string {
	for _, cat := range categories {
		r := ranges[cat]
		if pmin < r[1] && pmax > r[0] {
			return cat
		}
	}
	return ""
}

// gradientColorStr returns the 6-hex gradient of color toward black by
// factor f, mirroring the gradient_color closure (pages.py:2616).
func gradientColorStr(color, toward string, f float64) string {
	primary := expandHexColor(color)
	secondary := expandHexColor(toward)
	pr := hexToRGB(primary)
	sr := hexToRGB(secondary)
	var b strings.Builder
	for i := range 3 {
		c := float64(pr[i]) + (float64(sr[i])-float64(pr[i]))*f
		if c < 0 {
			c = 0
		}
		if c > 255 {
			c = 255
		}
		fmt.Fprintf(&b, "%02x", int(c))
	}
	return b.String()
}

// expandHexColor expands a 3-digit hex color to 6 digits, mirroring the
// expand closure (pages.py:2555, 2614).
func expandHexColor(c string) string {
	if len(c) == 3 {
		var b strings.Builder
		for i := range 3 {
			b.WriteByte(c[i])
			b.WriteByte(c[i])
		}
		return b.String()
	}
	if len(c) >= 6 {
		return c[:6]
	}
	return c
}

// hexToRGB parses a 6-digit hex color into three bytes, mirroring hex_to_rgb
// (pages.py:2554, 2615).
func hexToRGB(h string) [3]int {
	var out [3]int
	for i := range 3 {
		if 2*i+2 <= len(h) {
			v, _ := strconv.ParseInt(h[2*i:2*i+2], 16, 32)
			out[i] = int(v)
		}
	}
	return out
}
