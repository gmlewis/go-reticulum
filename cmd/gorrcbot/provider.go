// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the provider plumbing for the commands that reach the
// network: one user-supplied argument is substituted into an
// operator-supplied URL template, the answer is reduced to a single line, and
// the line is posted into a room. Both halves are untrusted — a room
// participant is untrusted input, and so is whatever a provider answers with —
// so the rules here are deliberately strict:
//
//   - The argument is validated against a strict allowlist before any URL
//     exists. It cannot carry a path separator, a query or fragment
//     separator, a userinfo section, a percent escape, a control character or
//     a format character into the template. Letters, digits and combining
//     marks of any script are allowed, because a place is a human name and
//     "Zürich" or "京都" is as real as "London"; everything that could
//     restructure the URL instead of naming a place is not.
//   - The template must be an absolute http:// or https:// URL with no
//     credentials, and the built URL is re-parsed and required to address the
//     same scheme and host, so a substitution can never retarget a request at
//     another host or scheme.
//   - The answer is stripped of control and format characters and truncated
//     before it is posted. A NOTICE is rendered by other people's terminal
//     emulators, and an escape sequence in it would run there.
//   - Neither a rejected argument nor a configured template is ever echoed
//     into a room: the first is attacker-chosen text published under the bot's
//     name, and the second may carry a private hostname or an API key.
//   - Successful answers are cached briefly, because the reply cooldown is per
//     requester, not per provider: without a cache a busy room turns one
//     command into one request per asker.

package main

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// placeToken is the literal a provider template uses for the requested place.
const placeToken = "{place}"

// escapeRune is the escape character that opens an ANSI sequence.
const escapeRune = '\x1b'

// Provider input and output limits.
const (
	// maxPlaceBytes bounds a requested place. Real place names are far
	// shorter, and the cap bounds the URL and every log line built from it.
	maxPlaceBytes = 64
	// maxProviderLineBytes bounds the provider line the bot will post. One
	// NOTICE is one MTU-sized envelope anyway, so a longer answer is
	// truncated visibly rather than dropped.
	maxProviderLineBytes = 240
	// providerCacheTTL is how long one provider answer is reused.
	providerCacheTTL = 10 * time.Minute
	// providerCacheMaxEntries bounds the answer cache.
	providerCacheMaxEntries = 64
)

// Errors returned by the provider helpers. They are sentinels so the command
// layer renders one fixed line per failure class and never repeats the text
// that caused it.
var (
	// errPlaceRejected reports a requested place outside the allowlist.
	errPlaceRejected = errors.New("place rejected")
	// errProviderTemplate reports a template the bot refuses to use.
	errProviderTemplate = errors.New("provider template is unusable")
	// errEmptyProviderLine reports an answer with no usable text.
	errEmptyProviderLine = errors.New("provider line is empty")
	// errProviderBodyTooLarge reports an answer larger than the bot will read.
	errProviderBodyTooLarge = errors.New("provider answer is too large")
)

// providerStatusError reports a provider answer that arrived with a status other
// than 200. It carries the code, because the difference matters to a lookup
// command: a 400 means "that is not a number I publish", while a 5xx means the
// provider is broken, and answering the first when the second happened would be a
// lie. The message names no URL, which may carry a private host or a key.
type providerStatusError struct {
	// code is the HTTP status the provider answered with.
	code int
	// status is the provider's own status text, e.g. "400 Bad Request".
	status string
}

// Error renders the status alone, never the URL it was fetched from.
func (e *providerStatusError) Error() string {
	return fmt.Sprintf("provider answered %v", e.status)
}

// placeAllowed reports whether r may appear in a requested place: any letter,
// digit or combining mark in any script, plus the space, comma, period, hyphen
// and apostrophe that real place names use ("Ann Arbor", "Paris, France", "St.
// Louis", "O'Fallon") and the typographic apostrophe that keyboards produce.
//
// UTF-8 is the point: a place is a human name, and "Zürich", "São Paulo",
// "Москва" and "京都" are as real as "London". What is rejected is everything
// that could restructure a URL or a line rather than name a place — slashes,
// query and fragment punctuation, percent signs, quotes, angle brackets,
// backslashes, emoji, and every control or format character (the format range
// includes the bidi overrides that would let a place spoof the text around it).
// The allowlist is what makes the substitution in providerURL safe before any
// escaping is considered.
func placeAllowed(r rune) bool {
	switch r {
	case ' ', ',', '.', '-', '\'', '\u2019':
		return true
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
		return true
	}
	return false
}

// placeHasSubstance reports whether a place names something rather than being
// punctuation or marks alone, which would not be a place.
func placeHasSubstance(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// sanitizePlace validates and normalizes one requested place: runs of spaces
// collapse to one and the ends are trimmed, so "  Ann   Arbor " and "Ann Arbor"
// are the same request. A place must contain at least one letter or digit, so
// punctuation alone is not a request.
func sanitizePlace(raw string) (string, error) {
	var (
		b           strings.Builder
		pendingGap  bool
		hasAlphanum bool
	)
	for _, r := range raw {
		if !placeAllowed(r) {
			return "", errPlaceRejected
		}
		if r == ' ' {
			pendingGap = b.Len() > 0
			continue
		}
		if pendingGap {
			b.WriteByte(' ')
			pendingGap = false
		}
		b.WriteRune(r)
		if b.Len() > maxPlaceBytes {
			return "", errPlaceRejected
		}
		if !hasAlphanum {
			hasAlphanum = placeHasSubstance(r)
		}
	}
	place := b.String()
	if place == "" || !hasAlphanum {
		return "", errPlaceRejected
	}
	return place, nil
}

// validateProviderTemplate reports why a provider URL template cannot be used, or
// nil when it can, given the tokens the caller will substitute. With no tokens it
// checks the single-place command's placeholder, so a template for the weather
// command is validated by the same rule it always was. The message is
// operator-facing: it is logged at startup and never posted into a room.
func validateProviderTemplate(template string, tokens ...string) error {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return errors.New("it is empty")
	}
	if len(tokens) == 0 {
		tokens = []string{placeToken}
	}
	found := false
	for _, token := range tokens {
		if strings.Contains(trimmed, token) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("it does not contain any of the placeholders %v", strings.Join(tokens, ", "))
	}
	return validateProviderURL(trimmed)
}

// validateProviderURL checks that a provider URL is one the bot is willing to
// fetch: an absolute http:// or https:// URL with a host and no credentials. It
// is the part of the check that applies to a fixed URL as well as to a
// template, so a provider that needs no substitution — a published JSON
// product, say — is validated exactly as strictly as a templated one.
func validateProviderURL(template string) error {
	u, err := url.Parse(strings.TrimSpace(template))
	if err != nil {
		return fmt.Errorf("it is not a URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("its scheme %q is not http or https", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("it has no host")
	}
	if u.User != nil {
		return errors.New("it must not carry credentials")
	}
	return nil
}

// providerURL validates template for place and returns the URL to fetch. place
// must already be sanitized. The built URL is re-parsed and required to address
// the template's own scheme and host, so no argument can retarget the request.
func providerURL(template, place string) (string, error) {
	return providerURLValues(template, map[string]string{placeToken: place})
}

// providerValueEscaper percent-encodes the characters url.PathEscape deliberately
// leaves alone but that are structure in a query string. They are legal in a path
// segment, which is what PathEscape escapes for; in a query, "&" starts the next
// parameter, "=" separates a key from its value, "+" means a space to many
// servers, and ";" is a separator to some. A value that reached a query with any
// of them raw could add or rewrite a parameter of the operator's request.
var providerValueEscaper = strings.NewReplacer(
	"&", "%26",
	"=", "%3D",
	"+", "%2B",
	";", "%3B",
)

// escapeProviderValue escapes one already-validated value so that it is harmless
// in either place an operator's template may put it: a path segment or a query
// value. The characters that differ between the two positions are the ones
// escaped on top of url.PathEscape.
//
// This is the second line of defence, not the first: every command validates its
// own argument against an allowlist that is stricter than either escaper (a place
// may not contain "&", "=", "+" or "%", and a flight number is letters and digits
// only), so no reachable input depends on this today. An escaper that only works
// while every allowlist stays tight is a trap for the next command, though, so it
// is made safe for the position rather than for the argument.
//
// A value can never reach the authority (a template with a placeholder in its
// host or userinfo is refused), and colon and at-sign are data in both a path and
// a query, so those are left as url.PathEscape produces them.
func escapeProviderValue(value string) string {
	return providerValueEscaper.Replace(url.PathEscape(value))
}

// providerURLValues substitutes already-validated values into an
// operator-supplied template and returns the URL to fetch. Every value must have
// been validated by its own command before it gets here, and every value is
// escaped before it is substituted — never after, which would corrupt the
// template's own scheme, path and separators. Escaping is not validation: the two
// are done separately and for different reasons.
//
// The built URL is re-parsed and required to address the template's own scheme
// and host. That invariant is what makes substitution safe: no value can retarget
// the request, whatever the template looks like.
func providerURLValues(template string, values map[string]string) (string, error) {
	if err := validateProviderTemplate(template, valueTokens(values)...); err != nil {
		return "", fmt.Errorf("%w: %w", errProviderTemplate, err)
	}
	want, err := url.Parse(strings.TrimSpace(template))
	if err != nil {
		return "", fmt.Errorf("%w: %w", errProviderTemplate, err)
	}

	built := template
	for token, value := range values {
		built = strings.ReplaceAll(built, token, escapeProviderValue(value))
	}
	got, err := url.Parse(built)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errProviderTemplate, err)
	}
	if got.Scheme != want.Scheme || got.Host != want.Host {
		return "", fmt.Errorf("%w: a value changed the request's scheme or host", errProviderTemplate)
	}
	return built, nil
}

// valueTokens returns the tokens a substitution will fill, so the template check
// knows which placeholders are legitimate.
func valueTokens(values map[string]string) []string {
	tokens := make([]string, 0, len(values))
	for token := range values {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

// stripEscapes removes ANSI and other escape sequences from s, so no residue of
// one can reach a room. A NOTICE is rendered by someone else's terminal
// emulator, where a surviving sequence would be interpreted: CSI (ESC '[' …
// final byte) and OSC (ESC ']' … BEL or ESC '\') are removed whole, and any
// other ESC consumes the single character that follows it.
func stripEscapes(s string) string {
	if !strings.ContainsRune(s, escapeRune) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != escapeRune {
			b.WriteString(s[i : i+size])
			i += size
			continue
		}
		i += size
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[':
			i = skipCSISequence(s, i+1)
		case ']':
			i = skipOSCSequence(s, i+1)
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
	}
	return b.String()
}

// skipCSISequence returns the index just past a CSI sequence's final byte, or
// len(s) when the sequence is unterminated. The final byte is the first one in
// the range 0x40-0x7E.
func skipCSISequence(s string, i int) int {
	for ; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

// skipOSCSequence returns the index just past an OSC sequence's terminator
// (BEL, or the two bytes of ST: ESC '\'), or len(s) when it is unterminated.
func skipOSCSequence(s string, i int) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case '\a':
			return i + 1
		case escapeRune:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
	}
	return len(s)
}

// sanitizeProviderLine reduces one line of a provider answer to the line the
// bot may post: escape sequences are removed, control characters (CR, LF, NUL,
// BEL among them), format characters (a right-to-left override or a zero-width
// joiner among them) and a non-breaking space are dropped, whitespace runs
// collapse to one space, and the result is trimmed and truncated. It reports
// errEmptyProviderLine when nothing usable is left.
func sanitizeProviderLine(line string) (string, error) {
	var (
		b          strings.Builder
		pendingGap bool
	)
	for _, r := range stripEscapes(line) {
		if unicode.IsSpace(r) {
			pendingGap = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			continue
		}
		if pendingGap {
			b.WriteByte(' ')
			pendingGap = false
		}
		b.WriteRune(r)
	}
	cleaned := b.String()
	if cleaned == "" {
		return "", errEmptyProviderLine
	}
	return truncateUTF8Bytes(cleaned, maxProviderLineBytes), nil
}

// providerLineFromBody returns the first line of a provider body that survives
// sanitization. A leading line that is only an escape sequence or whitespace is
// skipped rather than reported as the answer, and a body with no usable line
// reports errEmptyProviderLine.
func providerLineFromBody(body string) (string, error) {
	for line := range strings.SplitSeq(body, "\n") {
		if cleaned, err := sanitizeProviderLine(line); err == nil {
			return cleaned, nil
		}
	}
	return "", errEmptyProviderLine
}

// providerEntry is one cached answer.
type providerEntry struct {
	// line is the sanitized reply line, so a cached answer is exactly as safe
	// as a fresh one.
	line string
	// expiresAt is when the entry stops being reused.
	expiresAt time.Time
}

// providerCache remembers recent provider answers so one request per place per
// TTL reaches the provider instead of one per asker. Only successful answers
// are stored, so a failure is never remembered as an answer. A nil cache is a
// disabled cache, which is what an unconfigured provider is.
type providerCache struct {
	mu      sync.Mutex
	entries map[string]providerEntry
	// order holds the cached keys oldest first, for eviction.
	order []string
	ttl   time.Duration
	max   int
}

// newProviderCache builds a cache for at most max answers, each reused for ttl.
func newProviderCache(ttl time.Duration, max int) *providerCache {
	return &providerCache{
		entries: make(map[string]providerEntry),
		ttl:     ttl,
		max:     max,
	}
}

// get returns the cached line for key when it is still fresh.
func (c *providerCache) get(key string, now time.Time) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expiresAt) {
		return "", false
	}
	return entry.line, true
}

// put stores one successful line for key, evicting the oldest entry when the
// cache is full.
func (c *providerCache) put(key, line string, now time.Time) {
	if c == nil || c.max <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		c.entries[key] = providerEntry{line: line, expiresAt: now.Add(c.ttl)}
		return
	}
	for len(c.entries) >= c.max && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.entries[key] = providerEntry{line: line, expiresAt: now.Add(c.ttl)}
	c.order = append(c.order, key)
}

// providerAnswer returns the lines for url: the cached answer when a fresh one is
// stored, and otherwise one bounded fetch that render turns into lines and caches.
// It is the same cache as providerLine, so one command's fetch also benefits the
// other when they share a URL.
func (r *registry) providerAnswer(key string, now time.Time, maxBytes int, render func([]byte) ([]string, error)) ([]string, error) {
	if cached, ok := r.cache.get(key, now); ok {
		return strings.Split(cached, "\n"), nil
	}
	body, err := r.fetchBounded(key, maxBytes)
	if err != nil {
		return nil, err
	}
	lines, err := render([]byte(body))
	if err != nil {
		return nil, err
	}
	if len(lines) > 0 {
		r.cache.put(key, strings.Join(lines, "\n"), now)
	}
	return lines, nil
}

// fetchBounded performs one provider fetch and refuses a body larger than
// maxBytes, so a provider cannot fill the bot's memory with one answer.
func (r *registry) fetchBounded(url string, maxBytes int) (string, error) {
	body, err := r.fetch(url)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(body) > maxBytes {
		return "", fmt.Errorf("%w: the answer is %v bytes, over the %v byte limit",
			errProviderBodyTooLarge, len(body), maxBytes)
	}
	return body, nil
}

// providerLine returns the one reply line for url: the cached line when a fresh
// one is stored, and otherwise one fetch reduced to a single safe line and
// cached. key is the cache key, which is the URL itself because it determines
// the answer.
func (r *registry) providerLine(key string, now time.Time) (string, error) {
	if line, ok := r.cache.get(key, now); ok {
		return line, nil
	}
	body, err := r.fetch(key)
	if err != nil {
		return "", err
	}
	line, err := providerLineFromBody(body)
	if err != nil {
		return "", err
	}
	r.cache.put(key, line, now)
	return line, nil
}
