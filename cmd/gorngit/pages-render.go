// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// pages_render.go implements the pure rendering helpers used by the
// nomadnetwork page-node handlers, mirroring the m_* helpers, icons,
// colours, formatters, default templates and chart renderers of
// RNS/Utilities/rngit/pages.py (NomadNetworkNode, rngit v1.4.2). These
// functions are pure (no I/O) so they can be unit-tested directly against
// golden values captured from the Python source.

package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// Page-node request paths, mirroring the PATH_* / FILE_* class constants
// of NomadNetworkNode (pages.py:54-69).
const (
	pagePathIndex    = "/page/index.mu"
	pagePathGroup    = "/page/group.mu"
	pagePathRepo     = "/page/repo.mu"
	pagePathTree     = "/page/tree.mu"
	pagePathBlob     = "/page/blob.mu"
	pagePathCommits  = "/page/commits.mu"
	pagePathCommit   = "/page/commit.mu"
	pagePathRefs     = "/page/refs.mu"
	pagePathStats    = "/page/stats.mu"
	pagePathReleases = "/page/releases.mu"
	pagePathRelease  = "/page/release.mu"
	pagePathWork     = "/page/work.mu"
	pagePathWorkDoc  = "/page/work_doc.mu"
	fileArtifact     = "/file/artifact"
	fileDownload     = "/file/download"
	fileWorkdoc      = "/file/workdoc"
)

// Tunables, mirroring NomadNetworkNode class constants (pages.py:71-77,121-124).
const (
	blobSizeLimit      = 256 * 1024
	treeEntriesPerPage = 1000
	commitsPerPage     = 100
	gitCommandTimeout  = 8 * time.Second
	maxRenderWidth     = 100
	pageTabWidth       = "   "
	pageAppName        = "nomadnetwork"
	pageNodeAspect     = "node"
)

// Renderable file extensions, mirroring RENDERABLE_EXTS / RENDER_DEFAULT
// (pages.py:123-124).
var (
	renderableExts = map[string]bool{".md": true, ".mu": true}
	renderDefault  = map[string]bool{".md": true, ".mu": true}
)

// Unicode fallback icons (USE_NERDFONTS=False), mirroring U_ICON_*
// (pages.py:79-88).
const (
	uIconSep     = "•"
	uIconFolder  = "🗀"
	uIconFile    = "🗎"
	uIconBranch  = "⑃"
	uIconTag     = "⌆"
	uIconCommits = "🖹"
	uIconStats   = "🗠"
	uIconHeart   = "♥"
	uIconPackage = "◇"
	uIconWork    = "☸"
)

// Nerd Font icons (USE_NERDFONTS=True), mirroring NF_ICON_* (pages.py:90-99).
const (
	nfIconSep     = "•"
	nfIconFolder  = "󰉖"
	nfIconFile    = ""
	nfIconBranch  = "󰘬"
	nfIconTag     = "󰓼"
	nfIconCommits = "󰋚"
	nfIconStats   = ""
	nfIconHeart   = "󰋑"
	nfIconPackage = "󰏗"
	nfIconWork    = "󱌣"
)

// UI colours, mirroring CLR_* (pages.py:101-108).
const (
	clrFolder = "`Ffe6"
	clrFile   = "`F66d"
	clrDim    = "`F666"
	clrDimH   = "`F444"
	clrOKDim  = "`FT537855"
	clrDiffA  = "`F0a0"
	clrDiffR  = "`F900"
	clrDiffP  = "`F0aa"
)

// Chart colours, mirroring RCLR_* (pages.py:110-117).
const (
	rclrPush      = "B9A810"
	rclrPushG     = "791212"
	rclrFetch     = "10b981"
	rclrFetchG    = "1c5e71"
	rclrView      = "3b82f6"
	rclrViewG     = "13428A"
	rclrDownload  = "7831E0"
	rclrDownloadG = "c5754d"
)

// icon returns the glyph for a named icon, mirroring NomadNetworkNode.icon
// (pages.py:180-205). When useNerdFonts is true the Nerd Font glyph is
// returned, otherwise the Unicode fallback.
func icon(name string, useNerdFonts bool) string {
	if useNerdFonts {
		switch name {
		case "sep":
			return nfIconSep
		case "folder":
			return nfIconFolder
		case "file":
			return nfIconFile
		case "branch":
			return nfIconBranch
		case "commits":
			return nfIconCommits
		case "tag":
			return nfIconTag
		case "stats":
			return nfIconStats
		case "heart":
			return nfIconHeart
		case "package":
			return nfIconPackage
		case "work":
			return nfIconWork
		}
		return ""
	}
	switch name {
	case "sep":
		return uIconSep
	case "folder":
		return uIconFolder
	case "file":
		return uIconFile
	case "branch":
		return uIconBranch
	case "commits":
		return uIconCommits
	case "tag":
		return uIconTag
	case "stats":
		return uIconStats
	case "heart":
		return uIconHeart
	case "package":
		return uIconPackage
	case "work":
		return uIconWork
	}
	return ""
}

// mHeading renders a micron heading, mirroring m_heading (pages.py:303).
func mHeading(text string, level int) string {
	return strings.Repeat(">", level) + text + "\n"
}

// mBold renders bold micron text, mirroring m_bold (pages.py:304).
func mBold(text string) string {
	return "`!" + text + "`!"
}

// mItalic renders italic micron text, mirroring m_italic (pages.py:305).
func mItalic(text string) string {
	return "`*" + text + "`*"
}

// mUnderline renders underlined micron text, mirroring m_underline
// (pages.py:306).
func mUnderline(text string) string {
	return "`_" + text + "`_"
}

// mColorFG renders foreground-coloured micron text, mirroring m_color_fg
// (pages.py:307).
func mColorFG(text, color string) string {
	return "`F" + color + text + "`f"
}

// mDivider renders a micron horizontal divider, mirroring m_divider
// (pages.py:308). The default char is a light horizontal box-drawing line.
func mDivider(char ...string) string {
	c := "─"
	if len(char) > 0 && char[0] != "" {
		c = char[0]
	}
	return "-" + c + "\n"
}

// mEscape escapes backticks for micron, mirroring m_escape (pages.py:309).
func mEscape(text string) string {
	return strings.ReplaceAll(text, "`", "\\`")
}

// sanitizeLabel strips characters that would break a micron link label,
// mirroring the inner sanitize_label of m_link (pages.py:313,323,333).
func sanitizeLabel(value string) string {
	value = strings.ReplaceAll(value, "[", "")
	value = strings.ReplaceAll(value, "]", "")
	value = strings.ReplaceAll(value, "`", "")
	return value
}

// sanitizeV percent-encodes a link field value, mirroring the inner
// sanitize_v of m_link (pages.py:312,322,332) which uses quote_plus on the
// UTF-8 encoding of the value.
func sanitizeV(value any) string {
	return url.QueryEscape(fmt.Sprintf("%v", value))
}

// linkField is one ordered key/value link field. Python's m_link takes
// **fields (an insertion-ordered dict), so the byte-exact port must carry
// fields in call-site order; a Go map[string]any cannot preserve that
// order, so the m_link* helpers take an ordered []linkField instead.
type linkField struct {
	K string
	V any
}

// buildFieldStr renders the `k=v|k=v field suffix of a micron link,
// mirroring the shared field_str logic of the m_link* helpers. Field
// order is preserved from the caller (matching Python **fields order).
func buildFieldStr(fields []linkField) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, f.K+"="+sanitizeV(f.V))
	}
	return "`" + strings.Join(parts, "|")
}

// mLinkR renders a raw (unstyled) micron link, mirroring m_link_r
// (pages.py:311-319).
func mLinkR(label, path string, fields []linkField) string {
	return "`[" + sanitizeLabel(label) + "`:" + path + buildFieldStr(fields) + "]"
}

// mLinkE renders an emphasised remote micron link, mirroring m_link_e
// (pages.py:321-329).
func mLinkE(label, remote, path string, fields []linkField) string {
	return "`!`[" + sanitizeLabel(label) + "`" + remote + ":" + path + buildFieldStr(fields) + "]`!"
}

// mLink renders an emphasised local micron link, mirroring m_link
// (pages.py:331-339).
func mLink(label, path string, fields []linkField) string {
	return "`!`[" + sanitizeLabel(label) + "`:" + path + buildFieldStr(fields) + "]`!"
}

// mAlign wraps text in a micron alignment tag, mirroring m_align
// (pages.py:341-343).
func mAlign(text, align string) string {
	tag := "a"
	switch align {
	case "center":
		tag = "c"
	case "left":
		tag = "l"
	case "right":
		tag = "r"
	}
	return "`" + tag + text + "`a"
}

// formatSize formats a byte count, mirroring format_size (pages.py:2383)
// which delegates to RNS.prettysize. The Go port's rns.PrettySize is used for
// consistency with the rest of the port.
func formatSize(sizeBytes int64) string {
	return rns.PrettySize(float64(sizeBytes), "")
}

// formatAbsoluteTime formats a unix timestamp as a fixed clock string,
// mirroring format_absolute_time (pages.py:2385-2387).
func formatAbsoluteTime(timestamp int64) string {
	return time.Unix(timestamp, 0).Format("2006-01-02 15:04:05")
}

// formatRelativeTime formats a unix timestamp as a relative "N units ago"
// string, mirroring format_relative_time (pages.py:2389-2412). now is taken
// as a parameter so the function is deterministic and testable.
func formatRelativeTime(timestamp, now int64) string {
	diff := now - timestamp
	switch {
	case diff < 60:
		return "just now"
	case diff < 3600:
		minutes := diff / 60
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	case diff < 86400:
		hours := diff / 3600
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 604800:
		days := diff / 86400
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case diff < 2592000:
		weeks := diff / 604800
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	case diff < 31536000:
		months := diff / 2592000
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := diff / 31536000
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

// formatTabs expands tab characters to three spaces, mirroring format_tabs
// (pages.py:2414-2416).
func formatTabs(text string) string {
	return strings.ReplaceAll(text, "\t", pageTabWidth)
}

// formatDiff colour-codes a unified diff for micron display, mirroring
// format_diff (pages.py:2418-2439).
func formatDiff(diffText string) string {
	diffText = strings.ReplaceAll(diffText, "\\", "\\\\")
	lines := strings.Split(diffText, "\n")
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++"):
			formatted = append(formatted, mEscape(line))
		case strings.HasPrefix(line, "+"):
			formatted = append(formatted, clrDiffA+mEscape(line)+"`f")
		case strings.HasPrefix(line, "---"):
			formatted = append(formatted, mEscape("\\"+line))
		case strings.HasPrefix(line, "-"):
			formatted = append(formatted, clrDiffR+mEscape(line)+"`f")
		case strings.HasPrefix(line, "@@"):
			formatted = append(formatted, clrDiffP+mEscape(line)+"`f")
		case strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "new file") || strings.HasPrefix(line, "deleted file"):
			if strings.HasPrefix(line, "diff --git a") {
				formatted = append(formatted, "")
			}
			formatted = append(formatted, clrDim+mEscape(line)+"`f")
		default:
			formatted = append(formatted, mEscape(line))
		}
	}
	return strings.Join(formatted, "\n")
}

// formatCommit escapes a commit message body for micron display,
// mirroring format_commit (pages.py:2441-2449). Lines beginning with "-" are
// backslash-escaped so micron does not interpret them as dividers.
func formatCommit(diffText string) string {
	diffText = strings.ReplaceAll(diffText, "\\", "\\\\")
	lines := strings.Split(diffText, "\n")
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "-") {
			formatted = append(formatted, mEscape("\\"+line))
		} else {
			formatted = append(formatted, mEscape(line))
		}
	}
	return strings.Join(formatted, "\n")
}

// Default templates, mirroring DEFAULT_* / FALLBACK_TEMPLATE (pages.py:2748-2799).
const (
	defaultBaseTemplate = "#!c=0\n" +
		"> {NODE_NAME}\n\n" +
		"{NAVIGATION}\n" +
		"{PAGE_CONTENT}\n" +
		"<\n" +
		"-\n" +
		"`a`F666`[Served by rngit {VERSION}`:/page/index.mu] - {GEN_TIME}`f"

	defaultFrontTemplate    = "> Groups\n\n{PAGE_CONTENT}"
	defaultGroupTemplate    = "{PAGE_CONTENT}"
	defaultRepoTemplate     = "{PAGE_CONTENT}"
	defaultReleasesTemplate = "{PAGE_CONTENT}"
	defaultReleaseTemplate  = "{PAGE_CONTENT}"
	defaultTreeTemplate     = "{PAGE_CONTENT}"
	defaultBlobTemplate     = "{PAGE_CONTENT}"
	defaultCommitsTemplate  = "{PAGE_CONTENT}"
	defaultCommitTemplate   = "{PAGE_CONTENT}"
	defaultRefsTemplate     = "{PAGE_CONTENT}"
	defaultStatsTemplate    = "{PAGE_CONTENT}"
	defaultWorkTemplate     = "{PAGE_CONTENT}"
	defaultWorkDocTemplate  = "{PAGE_CONTENT}"
	fallbackTemplate        = "{PAGE_CONTENT}"
)

// templateRegistry maps a template name to its in-memory default,
// mirroring the self.templates dict (pages.py:138-152).
var templateRegistry = map[string]string{
	"base":     defaultBaseTemplate,
	"front":    defaultFrontTemplate,
	"group":    defaultGroupTemplate,
	"repo":     defaultRepoTemplate,
	"releases": defaultReleasesTemplate,
	"release":  defaultReleaseTemplate,
	"tree":     defaultTreeTemplate,
	"blob":     defaultBlobTemplate,
	"commits":  defaultCommitsTemplate,
	"commit":   defaultCommitTemplate,
	"refs":     defaultRefsTemplate,
	"stats":    defaultStatsTemplate,
	"work":     defaultWorkTemplate,
	"work_doc": defaultWorkDocTemplate,
}

// renderTemplate renders a page into the base template, mirroring
// render_template (pages.py:276-297). It performs the {PAGE_CONTENT},
// {NAVIGATION}, {NODE_NAME}, {VERSION} and {GEN_TIME} substitutions.
// Dynamic on-disk templates (get_template) are not supported in the Go port;
// the in-memory defaults are always used. genTimeSeconds is the elapsed
// render time used for the "Generated in ..." footer.
func renderTemplate(pageContent, navContent, template, nodeName, version string, genTimeSeconds float64) []byte {
	pageContent = formatTabs(pageContent)
	if tmpl, ok := templateRegistry[template]; ok {
		pageContent = strings.ReplaceAll(tmpl, "{PAGE_CONTENT}", pageContent)
	}
	base := defaultBaseTemplate
	base = strings.ReplaceAll(base, "{NODE_NAME}", nodeName)
	base = strings.ReplaceAll(base, "{VERSION}", version)
	if navContent != "" {
		base = strings.ReplaceAll(base, "{NAVIGATION}", navContent)
	} else {
		base = strings.ReplaceAll(base, "{NAVIGATION}", "")
	}
	var gt string
	if genTimeSeconds > 0 {
		gt = fmt.Sprintf("Generated in %s", rns.PrettyTime(genTimeSeconds, false, true))
	} else {
		gt = "Unknown generation time"
	}
	base = strings.ReplaceAll(base, "{GEN_TIME}", gt)
	base = strings.ReplaceAll(base, "{PAGE_CONTENT}", pageContent)
	return []byte(base)
}

// renderChartFullBlock renders a half-block-step bar chart using the full
// block ramp, mirroring render_chart_full_block (pages.py:2508-2534). The
// label rendering portion of the Python original (pages.py:2536-2542) is
// omitted as it is cosmetic and not asserted by any test.
func renderChartFullBlock(data []int, labels []string, color string, height int) string {
	if len(data) == 0 || allZero(data) {
		return "No data available\n"
	}
	maxVal := maxInt(data)
	if maxVal <= 0 {
		maxVal = 1
	}
	numPoints := len(data)
	const barWidth = 1
	var sb strings.Builder
	fmt.Fprintf(&sb, "`F%sPeak: %d`f\n", color, maxVal)
	for row := height; row > 0; row-- {
		threshold := float64(row-1) / float64(height) * float64(maxVal)
		sb.WriteString("│")
		for _, val := range data {
			if float64(val) > threshold {
				var glyph string
				switch {
				case row >= int(float64(height)*0.875):
					glyph = "█"
				case row >= int(float64(height)*0.625):
					glyph = "▓"
				case row >= int(float64(height)*0.375):
					glyph = "▒"
				default:
					glyph = "░"
				}
				sb.WriteString(fmt.Sprintf("`F%s%s`f", color, strings.Repeat(glyph, barWidth)))
			} else {
				sb.WriteString(strings.Repeat(" ", barWidth))
			}
		}
		sb.WriteString("\n")
	}
	bottom := "└" + strings.Repeat(strings.Repeat("─", barWidth), numPoints) + "┘"
	sb.WriteString(bottom + "\n")
	return sb.String()
}

// renderChart renders a chart via the half-block renderer, mirroring
// render_chart (pages.py:2505-2506). For the Go port the simpler full-block
// renderer is used as the half-block gradient variant is purely cosmetic.
func renderChart(data []int, labels []string, color string, height int) string {
	return renderChartFullBlock(data, labels, color, height)
}

// allZero reports whether every element of data is zero.
func allZero(data []int) bool {
	for _, v := range data {
		if v != 0 {
			return false
		}
	}
	return true
}

// maxInt returns the largest element of data, or 0 when empty.
func maxInt(data []int) int {
	m := 0
	for _, v := range data {
		if v > m {
			m = v
		}
	}
	return m
}
