// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the launches command: what is going up soon, and what just
// went up. It reads The Space Devs Launch Library 2 through the same provider
// plumbing the weather command uses (operator-supplied template, validated
// substitution, one sanitized line, a short cache).
//
// The cache is not a nicety here: Launch Library 2 allows 15 anonymous calls per
// hour per IP, so a busy room must not turn one command into one request per
// asker. The provider's own text is somebody else's data, so every field is
// stripped and shortened before it is posted.

package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// launchesUsage is the usage line for the launches command.
	launchesUsage = "launches [upcoming|past] [1-5]"
	// launchesNotConfiguredLine is the answer when the operator has set no
	// launch_url.
	launchesNotConfiguredLine = "launches is not configured: set launch_url in config.toml to enable it"
	// launchesFailedLine is the answer to a provider failure. It never quotes the
	// provider's error, which may name a private host.
	launchesFailedLine = "launches: provider unreachable, try again later"
	// launchesUnreadableLine is the answer to an answer the bot cannot parse.
	launchesUnreadableLine = "launches: could not read the provider's answer"
	// launchesEmptyLine is the answer to a window with nothing in it.
	launchesEmptyLine = "no launches reported for that window"
	// maxLaunchCount is the largest list one ask can request.
	maxLaunchCount = 5
	// defaultLaunchCount is how many launches a bare ask lists.
	defaultLaunchCount = 3
	// launchFetchSlack is how many extra launches the bot asks the provider for
	// beyond the number it will list. The provider's upcoming window keeps a
	// launch whose time has already passed until it updates the window, and it
	// orders by time ascending, so those stale entries occupy the head of the
	// answer; the margin lets the reply still hold the requested number of
	// launches that are genuinely ahead.
	launchFetchSlack = maxLaunchCount
	// maxLaunchBodyBytes bounds the launch answer the bot will read. The
	// provider's detailed mode is the useful one and runs about 11 KB per
	// launch, so this leaves room for a full list with slack.
	maxLaunchBodyBytes = 192 << 10
	// maxLaunchFieldBytes bounds one field of one launch line, which is
	// provider-supplied text.
	maxLaunchFieldBytes = 48
	// launchTimeLayout renders a launch time unambiguously: UTC, to the minute.
	launchTimeLayout = "2006-01-02 15:04Z"
)

// launchTokens are the placeholders a launch_url template may use.
var launchTokens = []string{"{mode}", "{limit}"}

// launchRequest is one validated launches ask: which window, and how many.
type launchRequest struct {
	// mode is the provider's window name: "upcoming" or "previous".
	mode string
	// label is the word the answer uses for that window.
	label string
	// count is how many launches to list.
	count int
}

// runLaunches reports the launches in one window: the next few by default, or the
// most recent past ones.
func (c *commandContext) runLaunches() []string {
	template := strings.TrimSpace(c.reg.bot.cfg.LaunchURL)
	if template == "" {
		return []string{launchesNotConfiguredLine}
	}
	req, rejected := parseLaunchArgs(c.Args)
	if rejected != nil {
		return rejected
	}
	// The upcoming window leads with launches the provider has not yet removed
	// after they flew, so ask for a margin and drop the stale ones after
	// parsing. The past window needs no margin: everything in it has flown.
	fetchCount := req.count
	if req.mode == "upcoming" {
		fetchCount += launchFetchSlack
	}
	fetchURL, err := providerURLValues(template, map[string]string{
		"{mode}":  req.mode,
		"{limit}": strconv.Itoa(fetchCount),
	})
	if err != nil {
		// The operator's template is at fault: the detail goes to the log, which
		// only the operator reads, and never into the room.
		logf("launches: %v", err)
		return []string{"launches is misconfigured: the operator must fix launch_url"}
	}
	now := c.req.Now
	if now.IsZero() {
		now = time.Now()
	}
	render := func(body []byte) ([]string, error) {
		launches, err := parseLaunches(body, req.mode == "previous", now)
		if err != nil {
			return nil, err
		}
		if len(launches) == 0 {
			return []string{launchesEmptyLine}, nil
		}
		return launchLines(req, launches, now, c.replyBudget()), nil
	}
	lines, err := c.reg.providerAnswer(fetchURL, now, maxLaunchBodyBytes, render)
	switch {
	case err == nil:
		return lines
	case errors.Is(err, errLaunchUnreadable):
		return []string{launchesUnreadableLine}
	case errors.Is(err, errProviderBodyTooLarge):
		logf("launches: %v", err)
		return []string{"launches is misconfigured: the operator must fix launch_url"}
	default:
		return []string{launchesFailedLine}
	}
}

// errLaunchUnreadable reports a provider answer that is not the JSON this command
// can read, which is a different failure from an unreachable provider.
var errLaunchUnreadable = errors.New("launches: the provider's answer is unreadable")

// parseLaunchArgs validates the window and the count. A rejected argument gets the
// usage line and never reaches the provider.
func parseLaunchArgs(args string) (launchRequest, []string) {
	req := launchRequest{mode: "upcoming", label: "upcoming", count: defaultLaunchCount}
	fields := strings.Fields(args)
	if len(fields) > 2 {
		return launchRequest{}, []string{"Usage: " + launchesUsage}
	}
	if len(fields) >= 1 {
		switch strings.ToLower(fields[0]) {
		case "upcoming", "next":
			req.mode, req.label = "upcoming", "upcoming"
		case "past", "previous":
			req.mode, req.label = "previous", "past"
		default:
			return launchRequest{}, []string{"Usage: " + launchesUsage}
		}
	}
	if len(fields) == 2 {
		n, err := strconv.Atoi(fields[1])
		if err != nil || n < 1 || n > maxLaunchCount {
			return launchRequest{}, []string{"Usage: " + launchesUsage}
		}
		req.count = n
	}
	return req, nil
}

// launch is one launch as the answer needs it: already reduced to plain strings,
// so a missing or surprising provider field cannot reach the reply.
type launch struct {
	// at is the launch time, zero when the provider's time did not parse.
	at time.Time
	// name is the launch name, e.g. "Vega-C | Sentinel-3C & FLEX".
	name string
	// status is the provider's status abbreviation, e.g. "Go".
	status string
	// provider is the launch service provider's name.
	provider string
	// pad is the launch pad's name.
	pad string
}

// parseLaunches reads the provider's answer. It tolerates every field being
// absent, because a provider may add or rename fields at any time, and it reports
// only a body it cannot parse at all.
func parseLaunches(body []byte, newestFirst bool, now time.Time) ([]launch, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("%w: %w", errLaunchUnreadable, err)
	}
	results, ok := jsonArray(root, "results")
	if !ok {
		return nil, nil
	}
	out := make([]launch, 0, len(results))
	for i := range results {
		item, ok := results[i].(map[string]any)
		if !ok {
			continue
		}
		at, _ := parseLaunchTime(jsonField(item, "net"))
		if at.IsZero() {
			at, _ = parseLaunchTime(jsonField(item, "window_start"))
		}
		if !newestFirst && !at.IsZero() && at.Before(now) {
			// An answer about what is next must not list what just went up: the
			// provider keeps a launch in the upcoming window until it updates
			// that window, and orders by time ascending, so the already-flown
			// ones sit at the head.
			continue
		}
		out = append(out, launch{
			at:       at,
			name:     safeEcho(jsonField(item, "name"), maxLaunchFieldBytes),
			status:   safeEcho(jsonField(item, "status.abbrev"), maxLaunchFieldBytes),
			provider: safeEcho(jsonField(item, "launch_service_provider.name"), maxLaunchFieldBytes),
			pad:      safeEcho(jsonField(item, "pad.name"), maxLaunchFieldBytes),
		})
	}
	// The provider's own ordering is not trusted. The upcoming window is
	// ascending (soonest first) and holds only launches still ahead, or ones
	// whose time did not parse; the past window is newest first. A launch with
	// no parsable time sorts last rather than first, so it never displaces a
	// dated one.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].at.IsZero() || out[j].at.IsZero() {
			return out[j].at.IsZero() && !out[i].at.IsZero()
		}
		if newestFirst {
			return out[i].at.After(out[j].at)
		}
		return out[i].at.Before(out[j].at)
	})
	return out, nil
}

// launchLines renders the answer: a counted header, one line per launch, and an
// honest trailer when the reply budget cuts the list short. The count in the
// header is what is actually shown, never the provider's own total.
func launchLines(req launchRequest, launches []launch, now time.Time, budget int) []string {
	shown := launches
	if len(shown) > req.count {
		shown = shown[:req.count]
	}
	lines := []string{fmt.Sprintf("%v launches (%v):", req.label, len(shown))}
	// One line is the header, and a reply budget of zero must not slice backwards.
	room := max(0, min(budget-1, len(shown)))
	for _, l := range shown[:room] {
		lines = append(lines, launchLine(l, now))
	}
	if room < len(shown) {
		// "not shown" rather than "more not shown": with a budget of one, none
		// of them were shown at all.
		lines = append(lines, fmt.Sprintf("… %v not shown", len(shown)-room))
	}
	return lines
}

// launchLine renders one launch: when, then the parts of it the provider gave.
func launchLine(l launch, now time.Time) string {
	parts := make([]string, 0, 5)
	if l.name != "" {
		parts = append(parts, l.name)
	}
	if l.status != "" {
		parts = append(parts, l.status)
	}
	switch {
	case l.provider != "" && l.pad != "":
		parts = append(parts, l.provider+" @ "+l.pad)
	case l.provider != "":
		parts = append(parts, l.provider)
	case l.pad != "":
		parts = append(parts, l.pad)
	}
	when := "at an unannounced time"
	if !l.at.IsZero() {
		when = l.at.UTC().Format(launchTimeLayout) + " " + formatCountdown(l.at.Sub(now))
	}
	return when + " | " + strings.Join(parts, " | ")
}

// formatCountdown renders how far away a moment is, at the precision a launch
// watcher cares about: minutes for the last hour, hours and minutes within two
// days, whole days beyond that. A moment in the past reads as an age instead, so
// the past window is not reported as a countdown.
func formatCountdown(d time.Duration) string {
	if d < 0 {
		return countdownMagnitude(-d) + " ago"
	}
	return "in " + countdownMagnitude(d)
}

// countdownMagnitude renders a positive duration at the coarseness a countdown
// needs.
func countdownMagnitude(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%vs", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%vm", int(d.Minutes()))
	case d < 48*time.Hour:
		if minutes := int(d.Minutes()) % 60; minutes != 0 {
			return fmt.Sprintf("%vh%vm", int(d.Hours()), minutes)
		}
		return fmt.Sprintf("%vh", int(d.Hours()))
	default:
		return fmt.Sprintf("%vd", int(d.Hours()/24))
	}
}

// parseLaunchTime parses the RFC3339 timestamps the provider sends. Anything else
// is reported as unparsed, so the answer says "an unannounced time" instead of
// inventing a date.
func parseLaunchTime(text string) (time.Time, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, false
	}
	return at.UTC(), true
}

// jsonArray returns the array at a top-level key of a decoded JSON object.
func jsonArray(root any, key string) ([]any, bool) {
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, false
	}
	arr, ok := obj[key].([]any)
	return arr, ok
}

// jsonField reads a string out of a decoded JSON object by path. A path is a
// dot-separated list of keys, each optionally followed by "[n]" for one element or
// "[]" for the first element: "name", "status.abbrev", "results[0].name",
// "results[].name". A missing key, an index out of range, and a value that is not
// a string all return "", so a provider's field rename degrades one field rather
// than the whole answer.
func jsonField(node any, path string) string {
	if path == "" {
		return ""
	}
	for segment := range strings.SplitSeq(path, ".") {
		key, indexes := splitJSONSegment(segment)
		obj, ok := node.(map[string]any)
		if !ok {
			return ""
		}
		value, ok := obj[key]
		if !ok {
			return ""
		}
		for _, index := range indexes {
			arr, ok := value.([]any)
			if !ok || len(arr) == 0 {
				return ""
			}
			if index < 0 {
				index = 0
			}
			if index >= len(arr) {
				return ""
			}
			value = arr[index]
		}
		node = value
	}
	return jsonScalar(node)
}

// splitJSONSegment splits one path segment into its key and its array indexes. An
// "[n]" index is that element and "[]" is index 0, which is the element a
// collection path means.
func splitJSONSegment(segment string) (key string, indexes []int) {
	open := strings.IndexByte(segment, '[')
	if open < 0 {
		return segment, nil
	}
	key = segment[:open]
	for part := range strings.SplitSeq(segment[open:], "[") {
		if part == "" {
			continue
		}
		digits := strings.TrimSuffix(part, "]")
		if digits == "" {
			indexes = append(indexes, 0)
			continue
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			// An unparsable index is not a match, which -1 expresses.
			indexes = append(indexes, -1)
			continue
		}
		indexes = append(indexes, n)
	}
	return key, indexes
}

// jsonScalar renders a decoded JSON leaf as text: strings as they are, numbers in
// their shortest exact form, booleans as true/false. Anything else, including a
// nested object, is not a field.
func jsonScalar(node any) string {
	switch v := node.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}
