// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the severe-weather alert reporter: the wxalert command and
// the decoder behind it. It answers the one question that matters before a
// party moves — is there a warning in force where they are going — from
// whichever CAP or NWS endpoint the operator has configured.
//
// Provider answers come in two shapes. The NWS active-alerts API, and anything
// else built on CAP, is JSON whose alert objects carry an event name and a
// severity; a plain-text endpoint answers with a headline. Both are accepted:
// the JSON is decoded into one line per alert, and anything else falls back to
// the sanitized text, because an alert that does not decode is still an alert.
//
// Alerts are cached for fifteen minutes: a warning changes on the scale of
// tens of minutes, and a mesh node should not spend a scarce link rediscovering
// the same tornado warning for every asker.

package bot

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Weather-alert wordings and bounds.
const (
	// wxalertUsage is the usage line for the command.
	wxalertUsage = "wxalert <place|area>"
	// wxalertNotConfiguredLine is the answer when the operator has set no
	// provider.
	wxalertNotConfiguredLine = "wxalert is not configured: set weather_alert_url in config.toml to enable it"
	// wxalertMisconfiguredLine is the answer when weather_alert_url itself is
	// unusable. It never quotes the template, which may name a private host or
	// carry a key.
	wxalertMisconfiguredLine = "wxalert is misconfigured: the operator must fix weather_alert_url"
	// wxalertPlaceRejectedLine is the answer to a place outside the allowlist.
	wxalertPlaceRejectedLine = "wxalert: place may only contain letters, digits, spaces, commas, periods, hyphens or apostrophes (up to 64 bytes)"
	// wxalertFailedLine is the answer when the provider cannot be reached.
	wxalertFailedLine = "wxalert lookup failed (network error). Try again later."
	// wxalertCacheTTL is how long one alert answer is reused.
	wxalertCacheTTL = 15 * time.Minute
	// maxWxAlertBodyBytes bounds the provider response the bot will read.
	maxWxAlertBodyBytes = 256 << 10
	// maxWxAlertRows bounds how many alerts one answer lists, so a
	// continent-wide feed cannot produce an unbounded reply.
	maxWxAlertRows = 8
	// maxWxAlertFieldBytes bounds one field of one alert line.
	maxWxAlertFieldBytes = 90
)

// wxAlert is one active warning.
type wxAlert struct {
	// Event is the warning's name, such as "Tornado Warning".
	Event string
	// Severity is the CAP severity, or the urgency when no severity is given.
	Severity string
	// Area is the area the warning covers.
	Area string
	// Expires is when the warning ends, as published.
	Expires string
}

// runWxalert answers with the warnings in force where the asker asked.
func (c *commandContext) runWxalert() []string {
	template := strings.TrimSpace(c.reg.weatherAlertURL())
	if template == "" {
		return []string{wxalertNotConfiguredLine}
	}
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + wxalertUsage}
	}
	place, err := sanitizePlace(c.Args)
	if err != nil {
		return []string{wxalertPlaceRejectedLine}
	}
	fetchURL, err := providerURL(template, place)
	if err != nil {
		logf("wxalert: %v", err)
		return []string{wxalertMisconfiguredLine}
	}
	lines, err := c.reg.alertAnswer(fetchURL, place, c.now())
	if err != nil {
		logf("wxalert: lookup for %v failed: %v", place, err)
		return []string{wxalertFailedLine}
	}
	return lines
}

// alertAnswer returns the cached answer for a URL, or fetches and renders one.
// The cache is separate from the general provider cache because alerts are
// reused for longer than a plain weather line.
func (r *registry) alertAnswer(fetchURL, place string, now time.Time) ([]string, error) {
	if cached, ok := r.alerts.get(fetchURL, now); ok && cached != "" {
		return strings.Split(cached, "\n"), nil
	}
	body, err := r.fetchBounded(fetchURL, maxWxAlertBodyBytes)
	if err != nil {
		return nil, err
	}
	lines, err := renderAlertAnswer(body, place)
	if err != nil {
		return nil, err
	}
	if len(lines) > 0 {
		r.alerts.put(fetchURL, strings.Join(lines, "\n"), now)
	}
	return lines, nil
}

// renderAlertAnswer renders a provider answer. A JSON answer is decoded into
// one line per alert; anything else is treated as a plain-text headline.
func renderAlertAnswer(body, place string) ([]string, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, fmt.Errorf("wxalert: the provider answered nothing")
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var document any
		if err := json.Unmarshal([]byte(trimmed), &document); err == nil {
			alerts := collectWxAlerts(document)
			if len(alerts) == 0 {
				return []string{fmt.Sprintf("No active weather alerts for %v.", place)}, nil
			}
			return wxAlertLines(place, alerts), nil
		}
		// A body that looked like JSON but did not decode falls through to the
		// text path, which is what a provider's plain-text headline needs.
	}
	if strings.HasPrefix(trimmed, "<") {
		// Markup is what a provider returns when it is broken, not a warning.
		return nil, fmt.Errorf("wxalert: the provider answered markup, not a report")
	}
	line, err := sanitizeProviderLine(strings.ReplaceAll(trimmed, "\n", " "))
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, fmt.Errorf("wxalert: the provider answer carried no usable text")
	}
	return []string{line}, nil
}

// wxAlertLines renders the alerts, most severe first, bounded in number.
func wxAlertLines(place string, alerts []wxAlert) []string {
	sort.SliceStable(alerts, func(i, j int) bool {
		left, right := wxSeverityRank(alerts[i].Severity), wxSeverityRank(alerts[j].Severity)
		if left != right {
			return left < right
		}
		return alerts[i].Event < alerts[j].Event
	})
	if len(alerts) > maxWxAlertRows {
		alerts = alerts[:maxWxAlertRows]
	}
	lines := []string{fmt.Sprintf("Active weather alerts for %v: %v", place,
		pluralCount(len(alerts), "alert", "alerts"))}
	for _, alert := range alerts {
		// The severity leads the event rather than becoming a column of its
		// own, so a row reads as one warning: "[Extreme] Tornado Warning".
		event := safeEcho(alert.Event, maxWxAlertFieldBytes)
		if severity := safeEcho(alert.Severity, maxWxAlertFieldBytes); severity != "" {
			event = "[" + severity + "] " + event
		}
		parts := make([]string, 0, 4)
		parts = append(parts, event)
		if area := safeEcho(alert.Area, maxWxAlertFieldBytes); area != "" {
			parts = append(parts, area)
		}
		if expires := safeEcho(alert.Expires, maxWxAlertFieldBytes); expires != "" {
			parts = append(parts, "until "+expires)
		}
		lines = append(lines, strings.Join(parts, " | "))
	}
	return lines
}

// wxSeverityRank orders the CAP severities, most severe first, with anything
// unrecognized in the middle rather than at either extreme.
func wxSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "extreme":
		return 0
	case "severe":
		return 1
	case "moderate":
		return 2
	case "minor":
		return 3
	case "":
		return 5
	default:
		return 4
	}
}

// collectWxAlerts walks a decoded JSON document and returns every object that
// looks like an alert: a map carrying a non-empty "event" field, which is what
// CAP and the NWS API both publish.
func collectWxAlerts(node any) []wxAlert {
	var out []wxAlert
	seen := map[string]bool{}
	walkWxAlerts(node, &out, seen)
	return out
}

// walkWxAlerts recurses through a decoded document collecting alert objects.
func walkWxAlerts(node any, out *[]wxAlert, seen map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		fields := map[string]string{}
		for key, child := range value {
			if text, ok := child.(string); ok {
				fields[normalizeAlertKey(key)] = text
			}
		}
		if event := strings.TrimSpace(fields["event"]); event != "" {
			alert := wxAlert{
				Event:    event,
				Severity: firstAlertField(fields, "severity", "urgency"),
				Area:     firstAlertField(fields, "areadesc", "area", "areas", "affectedzones"),
				Expires:  firstAlertField(fields, "ends", "expires", "endtime", "expirationtime"),
			}
			key := alert.Event + "\x00" + alert.Area + "\x00" + alert.Expires
			if !seen[key] {
				seen[key] = true
				*out = append(*out, alert)
			}
		}
		for _, child := range value {
			walkWxAlerts(child, out, seen)
		}
	case []any:
		for _, child := range value {
			walkWxAlerts(child, out, seen)
		}
	}
}

// firstAlertField returns the first of a set of candidate keys that is present
// and non-empty.
func firstAlertField(fields map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(fields[key]); value != "" {
			return value
		}
	}
	return ""
}

// normalizeAlertKey reduces a published field name to lowercase letters and
// digits, so "areaDesc", "area_desc", and "Area Desc" are the same key.
func normalizeAlertKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// weatherAlertURL returns the configured severe-weather provider template.
func (r *registry) weatherAlertURL() string {
	if r.bot == nil || r.bot.cfg == nil {
		return ""
	}
	return r.bot.cfg.WeatherAlertURL
}
