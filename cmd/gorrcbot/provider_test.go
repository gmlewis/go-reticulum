// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestSanitizePlaceAcceptsRealPlaces asserts the allowlist admits the shapes a
// person actually types, and that runs of spaces collapse.
func TestSanitizePlaceAcceptsRealPlaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "one word", in: "London", want: "London"},
		{name: "two words", in: "Ann Arbor", want: "Ann Arbor"},
		{name: "padded and repeated spaces", in: "  Ann   Arbor  ", want: "Ann Arbor"},
		{name: "city and country", in: "Paris, France", want: "Paris, France"},
		{name: "city and state abbreviation", in: "Orlando, FL", want: "Orlando, FL"},
		{name: "city and state without a space", in: "Orlando,FL", want: "Orlando,FL"},
		{name: "city and state, padded and doubled", in: "  Orlando ,   FL  ", want: "Orlando , FL"},
		{name: "city and abbreviated district", in: "Washington, D.C.", want: "Washington, D.C."},
		{name: "three words and a state", in: "Salt Lake City, UT", want: "Salt Lake City, UT"},
		{name: "abbreviation", in: "St. Louis", want: "St. Louis"},
		{name: "apostrophe", in: "O'Fallon", want: "O'Fallon"},
		{name: "hyphen", in: "Baden-Wuerttemberg", want: "Baden-Wuerttemberg"},
		{name: "postal code", in: "90210", want: "90210"},
		{name: "mixed case and digits", in: "Route 66", want: "Route 66"},
		{name: "latin diacritics", in: "Z\u00fcrich", want: "Z\u00fcrich"},
		{name: "combining mark", in: "Zu\u0308rich", want: "Zu\u0308rich"},
		{name: "cedilla and tilde", in: "S\u00e3o Paulo", want: "S\u00e3o Paulo"},
		{name: "cyrillic", in: "\u041c\u043e\u0441\u043a\u0432\u0430", want: "\u041c\u043e\u0441\u043a\u0432\u0430"},
		{name: "japanese", in: "\u4eac\u90fd", want: "\u4eac\u90fd"},
		{name: "greek", in: "\u0391\u03b8\u03ae\u03bd\u03b1", want: "\u0391\u03b8\u03ae\u03bd\u03b1"},
		{name: "polish", in: "Wroc\u0142aw", want: "Wroc\u0142aw"},
		{name: "typographic apostrophe", in: "O\u2019Fallon", want: "O\u2019Fallon"},
		{name: "accents and spaces together", in: "  S\u00e3o   Paulo  ", want: "S\u00e3o Paulo"},
		{name: "at the length cap", in: strings.Repeat("a", maxPlaceBytes), want: strings.Repeat("a", maxPlaceBytes)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitizePlace(tt.in)
			if err != nil {
				t.Fatalf("sanitizePlace(%q) = error %v, want %q", tt.in, err, tt.want)
			}
			if got != tt.want {
				t.Errorf("sanitizePlace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSanitizePlaceRejectsInjection asserts every attempt to reach the URL
// structure, a shell, a terminal, or another host is rejected before a URL
// exists. The list is the threat model in test form: path traversal, query and
// fragment separators, userinfo, percent escapes, control characters, bidi
// tricks, shell metacharacters and non-ASCII bytes.
func TestSanitizePlaceRejectsInjection(t *testing.T) {
	t.Parallel()

	rejected := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "spaces only", in: "   "},
		{name: "punctuation only", in: "..."},
		{name: "comma only", in: ","},
		{name: "path separator", in: "a/b"},
		{name: "path traversal", in: "../../etc/passwd"},
		{name: "absolute path", in: "/etc/passwd"},
		{name: "backslash", in: `a\b`},
		{name: "query separator", in: "a?x=1"},
		{name: "fragment", in: "a#frag"},
		{name: "query joiner", in: "a&b=2"},
		{name: "userinfo", in: "a@evil.example"},
		{name: "scheme separator", in: "http://evil.example"},
		{name: "absolute URL", in: "https://evil.example/x"},
		{name: "file URL", in: "file:///etc/passwd"},
		{name: "percent escape", in: "a%2e%2e"},
		{name: "null escape", in: "%00"},
		{name: "command substitution", in: "$(reboot)"},
		{name: "backticks", in: "`reboot`"},
		{name: "pipe", in: "a|b"},
		{name: "semicolon", in: "a;b"},
		{name: "redirection", in: "a>b"},
		{name: "double quote", in: `a"b`},
		{name: "braces", in: "a{b}"},
		{name: "newline", in: "Ann Arbor\n"},
		{name: "leading newline", in: "\nLondon"},
		{name: "tab", in: "London\t"},
		{name: "carriage return", in: "London\r"},
		{name: "escape", in: "London\x1b[31m"},
		{name: "non-breaking space", in: "Ann\u00a0Arbor"},
		{name: "zero width space", in: "Ann\u200bArbor"},
		{name: "over the length cap", in: strings.Repeat("a", maxPlaceBytes+1)},
		{name: "over the byte cap in multibyte runes", in: strings.Repeat("\u4eac", maxPlaceBytes/3+1)},
		{name: "diacritics with a path separator", in: "Z\u00fcrich/../../etc"},
		{name: "diacritics with a query", in: "Z\u00fcrich?x=1"},
		{name: "diacritics with a scheme", in: "https://evil.example/Z\u00fcrich"},
		{name: "percent escape around a multibyte rune", in: "Z%c3%bcrich"},
		{name: "emoji", in: "London \U0001f30d"},
		{name: "bidi override", in: "London\u202egnp.exe"},
		{name: "currency symbol", in: "London \u00a3"},
		{name: "mathematical symbol", in: "London \u00b1"},
		{name: "single angle bracket", in: "London<x"},
		{name: "square bracket", in: "London[0]"},
		{name: "colon", in: "London:Ontario"},
		{name: "equals", in: "London=1"},
	}

	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitizePlace(tt.in)
			if !errors.Is(err, errPlaceRejected) {
				t.Errorf("sanitizePlace(%q) = (%q, %v), want errPlaceRejected", tt.in, got, err)
			}
		})
	}
}

// TestSanitizePlaceRejectsNonASCII documents a deliberate restriction: the
// allowlist is ASCII, so a place spelled with a diacritic, a Cyrillic name or a
// CJK name is refused rather than encoded. It keeps the accepted alphabet small
// enough to reason about, and the operator can pick a provider whose spelling is
// ASCII.
// TestSanitizePlaceIsAboutScriptsNotBytes asserts the rule the operator asked
// for: a place is a human name in any script, so any script's letters, digits and
// combining marks are accepted as given, while symbols, emoji, invisible
// separators and the bidi overrides that could spoof the line around a place are
// still refused.
func TestSanitizePlaceIsAboutScriptsNotBytes(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"Z\u00fcrich",
		"S\u00e3o Paulo",
		"\u041c\u043e\u0441\u043a\u0432\u0430",
		"\u4eac\u90fd",
		"\u0391\u03b8\u03ae\u03bd\u03b1",
		"\u01cehk\u00e1n",
		"K\u00f8benhavn",
		"\u0130stanbul",
		"\u062f\u0645\u0634\u0642",
		"\u05d9\u05e8\u05d5\u05e9\u05dc\u05d9\u05dd",
	}
	for _, in := range accepted {
		got, err := sanitizePlace(in)
		if err != nil {
			t.Errorf("sanitizePlace(%q) = error %v, want it accepted", in, err)
			continue
		}
		if got != in {
			t.Errorf("sanitizePlace(%q) = %q, want it unchanged", in, got)
		}
	}

	rejected := []string{
		"Z\u00fcrich/../etc",
		"Z\u00fcrich?x=1",
		"\u4eac\u90fd\n",
		"London \U0001f30d",
		"London\u202egnp.exe",
		"London\u200b",
		"Z\u00fcrich\u00a0x",
		"London\u20ac",
		"\u0660\u0661/\u0662",
	}
	for _, in := range rejected {
		if got, err := sanitizePlace(in); err == nil {
			t.Errorf("sanitizePlace(%q) = (%q, nil), want it rejected", in, got)
		}
	}
}

// TestProviderURLSubstitutesTheEscapedPlace asserts the place lands in the URL
// percent-encoded, in a path or in a query, and that the template's own text is
// otherwise untouched.
func TestProviderURLSubstitutesTheEscapedPlace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		place    string
		want     string
	}{
		{
			name:     "path form",
			template: "https://wttr.in/{place}?format=%l:+%C+%t+%w",
			place:    "Ann Arbor",
			want:     "https://wttr.in/Ann%20Arbor?format=%l:+%C+%t+%w",
		},
		{
			name:     "comma and space escaped",
			template: "https://wttr.in/{place}?0",
			place:    "Paris, France",
			want:     "https://wttr.in/Paris%2C%20France?0",
		},
		{
			name:     "query form",
			template: "https://api.example.invalid/wx?q={place}&units=metric",
			place:    "Ann Arbor",
			want:     "https://api.example.invalid/wx?q=Ann%20Arbor&units=metric",
		},
		{
			name:     "plain http is allowed",
			template: "http://example.invalid/{place}",
			place:    "London",
			want:     "http://example.invalid/London",
		},
		{
			name:     "utf-8 place is percent-encoded, host untouched",
			template: "https://wttr.in/{place}?format=3",
			place:    "Z\u00fcrich",
			want:     "https://wttr.in/Z%C3%BCrich?format=3",
		},
		{
			name:     "cjk place is percent-encoded, query untouched",
			template: "https://api.example.invalid/wx?q={place}&units=metric",
			place:    "\u4eac\u90fd",
			want:     "https://api.example.invalid/wx?q=%E4%BA%AC%E9%83%BD&units=metric",
		},
		{
			name:     "no escaping needed",
			template: "https://example.invalid/{place}",
			place:    "90210",
			want:     "https://example.invalid/90210",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := providerURL(tt.template, tt.place)
			if err != nil {
				t.Fatalf("providerURL(%q, %q) = error %v", tt.template, tt.place, err)
			}
			if got != tt.want {
				t.Errorf("providerURL(%q, %q) = %q, want %q", tt.template, tt.place, got, tt.want)
			}
		})
	}
}

// TestEscapeProviderValueIsDataInEitherPosition asserts the invariant the escaper
// exists for: an escaped value round-trips back to exactly what it was, both as a
// path segment and as a query value, so a template may put its placeholder
// anywhere without the value becoming structure.
//
// The characters in this table are the whole reason the escaper is not just
// url.PathEscape: the last four are legal in a path, so PathEscape leaves them
// alone, and every one of them means something in a query.
func TestEscapeProviderValueIsDataInEitherPosition(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"Ann Arbor",
		"Paris, France",
		"Z\u00fcrich",
		"\u4eac\u90fd",
		"a&b",
		"a=b",
		"a+b",
		"a;b",
		"a?b",
		"a#b",
		"50%",
		"a/b",
		"a\\b",
		`a"b`,
		"a<b>c",
		"a:b@c",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			escaped := escapeProviderValue(value)

			// As a path segment: what the server would route on.
			u, err := url.Parse("https://example.invalid/" + escaped)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", escaped, err)
			}
			if got := strings.TrimPrefix(u.Path, "/"); got != value {
				t.Errorf("path decoded to %q, want the original %q", got, value)
			}

			// As a query value: the parameter must keep its value and gain no
			// sibling, which is what "&" and "=" would do if they were left raw.
			query, err := url.ParseQuery("q=" + escaped)
			if err != nil {
				t.Fatalf("url.ParseQuery(%q): %v", escaped, err)
			}
			if len(query) != 1 {
				t.Fatalf("query %v from %q has %v parameters, want exactly one", query, escaped, len(query))
			}
			if got := query.Get("q"); got != value {
				t.Errorf("query value decoded to %q, want the original %q", got, value)
			}
		})
	}
}

// TestProviderURLValueCannotAddAQueryParameter asserts the concrete attack the
// escaper closes: a value that looks like a parameter must not become one, so an
// operator's "?q={place}&units=metric" keeps its own two parameters whatever the
// argument says.
func TestProviderURLValueCannotAddAQueryParameter(t *testing.T) {
	t.Parallel()

	const template = "https://api.example.invalid/wx?q={place}&units=metric"
	for _, place := range []string{"London&units=imperial", "London&x=1&y=2", "London=other", "London;units=imperial"} {
		t.Run(place, func(t *testing.T) {
			t.Parallel()
			got, err := providerURL(template, place)
			if err != nil {
				t.Fatalf("providerURL: %v", err)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", got, err)
			}
			query, err := url.ParseQuery(parsed.RawQuery)
			if err != nil {
				t.Fatalf("url.ParseQuery(%q): %v", parsed.RawQuery, err)
			}
			if len(query) != 2 {
				t.Fatalf("query %v from %q has %v parameters, want the template's own two", query, got, len(query))
			}
			if query.Get("q") != place {
				t.Errorf("q = %q, want the whole argument %q as data", query.Get("q"), place)
			}
			if query.Get("units") != "metric" {
				t.Errorf("units = %q, want the template's own value untouched", query.Get("units"))
			}
		})
	}
}

// TestProviderURLRefusesUnsafeTemplates asserts a template that is not an
// absolute http(s) URL with no credentials, or that lets the place stand in for
// the host, is refused rather than fetched.
func TestProviderURLRefusesUnsafeTemplates(t *testing.T) {
	t.Parallel()

	rejected := []struct {
		name     string
		template string
	}{
		{name: "empty", template: ""},
		{name: "blank", template: "   "},
		{name: "no placeholder", template: "https://wttr.in/London"},
		{name: "no scheme", template: "wttr.in/{place}"},
		{name: "relative", template: "/wx/{place}"},
		{name: "file scheme", template: "file:///etc/{place}"},
		{name: "ftp scheme", template: "ftp://example.invalid/{place}"},
		{name: "gopher scheme", template: "gopher://example.invalid/{place}"},
		{name: "javascript scheme", template: "javascript:alert({place})"},
		{name: "data scheme", template: "data:text/plain,{place}"},
		{name: "credentials", template: "https://user:secret@example.invalid/{place}"},
		{name: "place is the host", template: "https://{place}/wx"},
		{name: "place in the userinfo", template: "https://{place}@example.invalid/wx"},
	}

	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := providerURL(tt.template, "London")
			if !errors.Is(err, errProviderTemplate) {
				t.Errorf("providerURL(%q, ...) = (%q, %v), want errProviderTemplate", tt.template, got, err)
			}
		})
	}
}

// TestValidateProviderTemplateExplainsItself asserts the operator-facing reason
// names the actual problem, because it is the only feedback an operator gets at
// startup.
func TestValidateProviderTemplateExplainsItself(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		want     string
	}{
		{name: "empty", template: "", want: "empty"},
		{name: "no placeholder", template: "https://wttr.in/London", want: placeToken},
		{name: "bad scheme", template: "ftp://example.invalid/{place}", want: "http or https"},
		{name: "no host", template: "https:///{place}", want: "no host"},
		{name: "credentials", template: "https://u:p@example.invalid/{place}", want: "credentials"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateProviderTemplate(tt.template)
			if err == nil {
				t.Fatalf("validateProviderTemplate(%q) = nil, want a reason mentioning %q", tt.template, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("validateProviderTemplate(%q) = %q, want it to mention %q", tt.template, err, tt.want)
			}
		})
	}

	for _, ok := range []string{"https://wttr.in/{place}?format=3", "http://example.invalid/{place}"} {
		if err := validateProviderTemplate(ok); err != nil {
			t.Errorf("validateProviderTemplate(%q) = %v, want nil", ok, err)
		}
	}
}

// TestStripEscapes asserts ANSI sequences are removed whole, so no residue of a
// terminal control sequence can reach someone else's terminal.
func TestStripEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text", in: "London: Sunny", want: "London: Sunny"},
		{name: "colour sequence", in: "\x1b[31mSunny\x1b[0m", want: "Sunny"},
		{name: "unterminated CSI", in: "Sunny\x1b[31", want: "Sunny"},
		{name: "OSC title with BEL", in: "\x1b]0;evil\aSunny", want: "Sunny"},
		{name: "OSC title with ST", in: "\x1b]0;evil\x1b\\Sunny", want: "Sunny"},
		{name: "unterminated OSC", in: "Sunny\x1b]0;evil", want: "Sunny"},
		{name: "bare escape consumes the next character", in: "Sun\x1bny", want: "Suny"},
		{name: "trailing escape", in: "Sunny\x1b", want: "Sunny"},
		{name: "no escape at all", in: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := stripEscapes(tt.in); got != tt.want {
				t.Errorf("stripEscapes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSanitizeProviderLineCleansOneLine asserts the line the bot would post
// carries no control or format character, collapses whitespace, is trimmed and
// is truncated visibly.
func TestSanitizeProviderLineCleansOneLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "kept as is", in: "London: Sunny +20C", want: "London: Sunny +20C"},
		{name: "trimmed", in: "   London: Sunny   ", want: "London: Sunny"},
		{name: "runs of spaces collapse", in: "London:   Sunny", want: "London: Sunny"},
		{name: "tabs collapse", in: "London:\tSunny", want: "London: Sunny"},
		{name: "carriage return dropped", in: "London:\rSunny", want: "London: Sunny"},
		{name: "NUL dropped", in: "London:\x00Sunny", want: "London:Sunny"},
		{name: "escape sequence removed", in: "\x1b[31mLondon\x1b[0m: Sunny", want: "London: Sunny"},
		{name: "bidi override dropped", in: "London\u202e: Sunny", want: "London: Sunny"},
		{name: "zero width space dropped", in: "Lon\u200bdon", want: "London"},
		{name: "nbsp becomes a space", in: "London:\u00a0Sunny", want: "London: Sunny"},
		{name: "only spaces", in: "   ", want: ""},
		{name: "only controls", in: "\x1b[2J\x00", want: ""},
		{
			name: "truncated at the cap",
			in:   strings.Repeat("a", maxProviderLineBytes+50),
			want: strings.Repeat("a", maxProviderLineBytes) + "…",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitizeProviderLine(tt.in)
			if tt.want == "" {
				if !errors.Is(err, errEmptyProviderLine) {
					t.Fatalf("sanitizeProviderLine(%q) = (%q, %v), want errEmptyProviderLine", tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeProviderLine(%q) = error %v, want %q", tt.in, err, tt.want)
			}
			if got != tt.want {
				t.Errorf("sanitizeProviderLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSanitizeProviderLineNeverLeavesAControlCharacter is the property that
// matters for a line posted into a room: whatever goes in, no C0/C1 control or
// format character comes out.
func TestSanitizeProviderLineNeverLeavesAControlCharacter(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for r := range rune(0x3100) {
		b.WriteRune(r)
	}
	got, err := sanitizeProviderLine(b.String())
	if err != nil {
		t.Fatalf("sanitizeProviderLine(control sweep) = error %v", err)
	}
	for _, r := range got {
		if r < ' ' || r == 0x7f {
			t.Errorf("sanitized line still carries control character %#v: %q", r, got)
		}
	}
}

// TestProviderLineFromBodySkipsUnusableLines asserts a body whose first line is
// only an escape sequence or whitespace still yields the first line a person can
// read, and that a body with nothing readable reports the sentinel.
func TestProviderLineFromBodySkipsUnusableLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "first line wins", body: "London: Sunny\nParis: Rain\n", want: "London: Sunny"},
		{name: "leading blank lines", body: "\n\n  \nLondon: Sunny\n", want: "London: Sunny"},
		{name: "leading escape only line", body: "\x1b]0;title\a\nLondon: Sunny\n", want: "London: Sunny"},
		{name: "CRLF body", body: "London: Sunny\r\nParis: Rain\r\n", want: "London: Sunny"},
		{name: "no newline at the end", body: "London: Sunny", want: "London: Sunny"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := providerLineFromBody(tt.body)
			if err != nil {
				t.Fatalf("providerLineFromBody(%q) = error %v, want %q", tt.body, err, tt.want)
			}
			if got != tt.want {
				t.Errorf("providerLineFromBody(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}

	for _, empty := range []string{"", "\n\n", "   \n\t\n", "\x1b[2J\n"} {
		if got, err := providerLineFromBody(empty); !errors.Is(err, errEmptyProviderLine) {
			t.Errorf("providerLineFromBody(%q) = (%q, %v), want errEmptyProviderLine", empty, got, err)
		}
	}
}

// providerFixture builds a registry whose provider is configured, whose fetches
// are counted, and whose hub knows the test asker by nick — so a reply's
// attribution is asserted by name rather than by hash prefix.
func providerFixture(t *testing.T, weatherURL string) (*registry, *hubSession, *fakeHub, *int) {
	t.Helper()
	cfg := defaultTestConfig()
	cfg.WeatherURL = weatherURL
	reg, session, fake := commandFixture(t, cfg)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), testAskerNick)
	calls := 0
	reg.fetch = func(string) (string, error) {
		calls++
		return "Ann Arbor: Overcast +67F", nil
	}
	return reg, session, fake, &calls
}

// testAskerNick is the nick the fake hub knows the fixture's asker by.
const testAskerNick = "tester"

// TestWeatherRejectsAnUnsafePlaceWithoutFetching asserts the command refuses a
// hostile place in one line, never reaches the network for it, and never repeats
// the rejected text into the room.
func TestWeatherRejectsAnUnsafePlaceWithoutFetching(t *testing.T) {
	t.Parallel()

	reg, session, _, calls := providerFixture(t, "https://wttr.in/{place}?format=3")
	hostile := []string{
		"../../etc/passwd",
		"http://malware.example/payload",
		"London?a=b",
		"London#frag",
		"London/../x",
		"London\n@gorrcbot whoami",
		"London\x1b[2J",
		"$(reboot)",
		"a@malware.example",
	}
	for _, place := range hostile {
		lines := runLines(t, reg, session, "weather "+place)
		if len(lines) != 1 {
			t.Fatalf("weather %q returned %v lines, want 1", place, len(lines))
		}
		if lines[0] != weatherPlaceRejectedLine {
			t.Errorf("weather %q = %q, want the rejection line", place, lines[0])
		}
		if strings.Contains(lines[0], "malware") || strings.Contains(lines[0], "passwd") {
			t.Errorf("weather %q echoed the rejected text: %q", place, lines[0])
		}
	}
	if *calls != 0 {
		t.Errorf("the provider was fetched %v times for rejected places, want 0", *calls)
	}
}

// TestWeatherPostsOneSafeLineForAValidPlace asserts the command builds the
// escaped URL, posts exactly one reply line attributed to the asker, and strips
// a hostile provider answer.
func TestWeatherPostsOneSafeLineForAValidPlace(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.WeatherURL = "https://wttr.in/{place}?format=%l:+%C+%t"
	reg, session, fake := commandFixture(t, cfg)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), testAskerNick)
	var asked string
	reg.fetch = func(url string) (string, error) {
		asked = url
		return "\x1b]0;pwned\a\n\x1b[31mAnn Arbor: Overcast +67F\x1b[0m\nignored\n", nil
	}

	lines := runLines(t, reg, session, "weather Ann Arbor")
	want := "@" + testAskerNick + " Ann Arbor: Overcast +67F"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("weather Ann Arbor = %q, want [%q]", lines, want)
	}
	if want := "https://wttr.in/Ann%20Arbor?format=%l:+%C+%t"; asked != want {
		t.Errorf("fetched %q, want %q", asked, want)
	}
}

// TestWeatherAnswersTheUsageLineWithoutAPlace asserts an empty argument list
// asks for a place instead of reporting a rejection.
func TestWeatherAnswersTheUsageLineWithoutAPlace(t *testing.T) {
	t.Parallel()

	reg, session, _, calls := providerFixture(t, "https://wttr.in/{place}?format=3")
	for _, line := range []string{"weather", "weather   ", "wx"} {
		got := runLines(t, reg, session, line)
		if len(got) != 1 || got[0] != "Usage: "+weatherUsage {
			t.Errorf("%q = %q, want [%q]", line, got, "Usage: "+weatherUsage)
		}
	}
	if *calls != 0 {
		t.Errorf("the provider was fetched %v times without a place, want 0", *calls)
	}
}

// TestWeatherReportsAnUnusableTemplateWithoutEchoingIt asserts an operator's
// broken template produces one line, never a fetch, and never a copy of the
// template — which may name a private host or carry a key.
func TestWeatherReportsAnUnusableTemplateWithoutEchoingIt(t *testing.T) {
	t.Parallel()

	reg, session, _, calls := providerFixture(t, "https://user:secret@example.invalid/{place}")
	lines := runLines(t, reg, session, "weather London")
	if len(lines) != 1 || lines[0] != weatherMisconfiguredLine {
		t.Fatalf("weather with a credentialled template = %q, want the misconfiguration line", lines)
	}
	if strings.Contains(lines[0], "secret") || strings.Contains(lines[0], "example.invalid") {
		t.Errorf("the misconfiguration line echoed the template: %q", lines[0])
	}
	if *calls != 0 {
		t.Errorf("an unusable template was fetched %v times, want 0", *calls)
	}
}

// TestWeatherFailureIsTheOfficialWording asserts a provider error is reported in
// the captured oracle's wording, attributed to the asker, so a client that knows
// the official bot sees what it expects.
func TestWeatherFailureIsTheOfficialWording(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.WeatherURL = "https://wttr.in/{place}?format=3"
	reg, session, fake := commandFixture(t, cfg)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), testAskerNick)
	reg.fetch = func(string) (string, error) { return "", errors.New("dial failed") }

	lines := runLines(t, reg, session, "weather London")
	want := fmt.Sprintf(weatherFailedLine, testAskerNick)
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("weather with a failing provider = %q, want [%q]", lines, want)
	}

	// An answer with nothing readable is the same failure, not an empty reply.
	reg.fetch = func(string) (string, error) { return "\x1b[2J\n \n", nil }
	reg.cache = nil
	lines = runLines(t, reg, session, "weather London")
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("weather with an unreadable answer = %q, want [%q]", lines, want)
	}
}

// TestWeatherCachesSuccessfulAnswers asserts one request per place per TTL
// reaches the provider, that a failure is never cached, and that a nil cache is
// a working disabled cache.
func TestWeatherCachesSuccessfulAnswers(t *testing.T) {
	t.Parallel()

	reg, session, _, calls := providerFixture(t, "https://wttr.in/{place}?format=3")
	now := time.Now()
	first := runLinesAt(t, reg, session, "weather London", now)
	if *calls != 1 {
		t.Fatalf("first lookup fetched %v times, want 1", *calls)
	}
	for i := range 3 {
		again := runLinesAt(t, reg, session, "weather London", now.Add(time.Duration(i)*time.Minute))
		if len(again) != 1 || len(first) != 1 || again[0] != first[0] {
			t.Errorf("cached reply %v = %q, want %q", i, again, first)
		}
	}
	if *calls != 1 {
		t.Errorf("warm cache fetched %v times, want 1", *calls)
	}

	// A second place is a second request, and the TTL expiry refetches.
	runLinesAt(t, reg, session, "weather Paris", now)
	if *calls != 2 {
		t.Errorf("a different place fetched %v times, want 2", *calls)
	}
	runLinesAt(t, reg, session, "weather London", now.Add(providerCacheTTL+time.Second))
	if *calls != 3 {
		t.Errorf("after the TTL the provider was fetched %v times, want 3", *calls)
	}
}

// TestProviderCacheMissesFailuresAndIsBounded asserts the failure and eviction
// rules directly, including that a nil cache is safe to use.
func TestProviderCacheMissesFailuresAndIsBounded(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cache := newProviderCache(time.Minute, 2)
	cache.put("a", "line-a", now)
	cache.put("b", "line-b", now)
	if line, ok := cache.get("a", now); !ok || line != "line-a" {
		t.Errorf("get(a) = (%q, %v), want (line-a, true)", line, ok)
	}
	cache.put("c", "line-c", now)
	if _, ok := cache.get("a", now); ok {
		t.Error("the oldest entry survived a full cache, want it evicted")
	}
	if line, ok := cache.get("c", now); !ok || line != "line-c" {
		t.Errorf("get(c) = (%q, %v), want (line-c, true)", line, ok)
	}
	if _, ok := cache.get("b", now.Add(2*time.Minute)); ok {
		t.Error("an expired entry was reused, want a miss")
	}
	// Re-putting a key refreshes it instead of growing the order.
	cache.put("c", "line-c2", now)
	if line, _ := cache.get("c", now); line != "line-c2" {
		t.Errorf("get(c) after refresh = %q, want line-c2", line)
	}
	if len(cache.order) != len(cache.entries) {
		t.Errorf("order has %v entries for %v keys, want them equal", len(cache.order), len(cache.entries))
	}

	var disabled *providerCache
	if _, ok := disabled.get("a", now); ok {
		t.Error("a nil cache reported a hit")
	}
	disabled.put("a", "line-a", now)
	if line, ok := disabled.get("a", now); ok || line != "" {
		t.Error("a nil cache stored a value")
	}
}

// runLinesAt runs one command line with a fixed clock, so the cache TTL is
// exercised without waiting.
func runLinesAt(t *testing.T, reg *registry, session *hubSession, line string, now time.Time) []string {
	t.Helper()
	return reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot "+line, peerHashFor(0x11)),
		Room:    "general",
		Command: line,
		Nick:    "gorrcbot",
		Now:     now,
	})
}

// TestWeatherAcceptsAUTF8Place asserts a place in any script survives the round
// trip: it is validated, percent-encoded into the template, and the provider's
// answer (which echoes the name back) is posted with the name intact.
func TestWeatherAcceptsAUTF8Place(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.WeatherURL = "https://wttr.in/{place}?format=%l:+%C+%t"
	reg, session, fake := commandFixture(t, cfg)
	fake.setKnownPeer(hexString(peerHashFor(0x11)), testAskerNick)
	var asked string
	reg.fetch = func(url string) (string, error) {
		asked = url
		return "Z\u00fcrich: Clear +61F", nil
	}

	assertLines(t, runLines(t, reg, session, "weather Z\u00fcrich"), []string{
		"@" + testAskerNick + " Z\u00fcrich: Clear +61F",
	})
	if want := "https://wttr.in/Z%C3%BCrich?format=%l:+%C+%t"; asked != want {
		t.Errorf("fetched %q, want %q", asked, want)
	}

	// A structural rune hidden after a non-ASCII name is still refused, and
	// nothing is fetched for it.
	asked = ""
	assertLines(t, runLines(t, reg, session, "weather Z\u00fcrich/../etc"), []string{
		weatherPlaceRejectedLine,
	})
	if asked != "" {
		t.Errorf("fetched %q for a rejected place, want no fetch", asked)
	}
}

// TestHTTPFetchReadsMoreThanOneLine asserts the shared fetch does not cut a
// provider's answer short: a JSON body is kilobytes, and a truncated body is
// invalid JSON that the command reports as a provider failure. The regression it
// guards against is an 8 KB fetch limit that a weather answer never noticed.
func TestHTTPFetchReadsMoreThanOneLine(t *testing.T) {
	t.Parallel()

	const padding = 20 << 10
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "{\"count\": 1, \"results\": [\"%v\"]}", strings.Repeat("x", padding))
	}))
	defer server.Close()

	body, err := httpFetch(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(body, `"]}`) {
		t.Errorf("body of %v bytes was cut short: it must end with the closing brace", len(body))
	}
	if err := json.Unmarshal([]byte(body), new(any)); err != nil {
		t.Errorf("the fetched body is not valid JSON: %v", err)
	}
}

// TestHTTPFetchRefusesANonOKAnswer asserts a provider error is an error, not a
// body that later parses as gibberish.
func TestHTTPFetchRefusesANonOKAnswer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	if _, err := httpFetch(server.URL); err == nil {
		t.Fatal("a 429 answer must be an error")
	}
}
