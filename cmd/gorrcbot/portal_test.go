// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// portalFixture builds a portal over the real command registry and a GNSS
// source holding fix, which is the state a traveler's phone finds the device
// in. A zero fix leaves the portal with no receiver at all.
func portalFixture(t *testing.T, fix GPSFix) *PortalServer {
	t.Helper()
	reg, _, _ := commandFixture(t, nil)
	reader := NewGPSReader(nil)
	if fix.Valid {
		reader.SetFix(fix)
	}
	t.Cleanup(func() { _ = reader.Close() })
	// One receiver feeds both the dashboard and the command registry, exactly
	// as the running bot wires it.
	reg.gps = reader
	portal := newPortalServer("127.0.0.1:0", reader, reg)
	// The solar clock and the sunset countdown are pinned, so the dashboard
	// tests do not depend on the hour the suite happens to run at.
	portal.now = func() time.Time {
		return time.Date(2026, time.September, 16, 20, 45, 33, 0, time.UTC)
	}
	t.Cleanup(func() { _ = portal.Close() })
	return portal
}

// portalGet issues one GET against the portal's handler.
func portalGet(t *testing.T, portal *PortalServer, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	portal.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// portalPost issues one POST with a JSON body against the portal's handler.
func portalPost(t *testing.T, portal *PortalServer, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	portal.Handler().ServeHTTP(rec, req)
	return rec
}

// decodeQuery decodes one /api/query answer.
func decodeQuery(t *testing.T, rec *httptest.ResponseRecorder) portalQueryResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("query status = %v, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var got portalQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("query answer is not JSON: %v (%q)", err, rec.Body.String())
	}
	return got
}

// TestPortalCaptiveProbesRedirect asserts every operating system's captive
// network check gets the redirect that makes its native browser open the
// survival dashboard, which is the whole reason a traveler needs no app.
func TestPortalCaptiveProbesRedirect(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	for _, path := range []string{
		"/hotspot-detect.html", // Apple iOS and macOS
		"/generate_204",        // Android
		"/gen_204",             // Android, alternate
		"/ncsi.txt",            // Windows
		"/connecttest.txt",     // Windows, alternate
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			rec := portalGet(t, portal, path)
			if rec.Code != http.StatusFound {
				t.Fatalf("GET %v = %v, want 302 so the phone opens the portal", path, rec.Code)
			}
			if got := rec.Header().Get("Location"); got != "/" {
				t.Errorf("GET %v Location = %q, want %q", path, got, "/")
			}
			if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
				t.Errorf("GET %v Cache-Control = %q, want no-store so the probe is not cached", path, got)
			}
		})
	}
}

// TestPortalRootServesTheDashboard asserts the dashboard is server-rendered with
// the live Plus Code already in it, so the panel is readable before any script
// runs on a phone with a cold battery.
func TestPortalRootServesTheDashboard(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	rec := portalGet(t, portal, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %v, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want HTML", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		refPlus10,
		refGrid,
		"Where Am I",
		"Copy Plus Code",
		"SOS",
		"Field Assistant",
		"med hypothermia",
		"/api/whereami",
		"/api/query",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dashboard does not contain %q", want)
		}
	}
	// A captive portal has no internet by definition, so the page must not
	// depend on one: no external script, stylesheet, font, or image.
	for _, forbidden := range []string{"http://", "https://", "//cdn", "<link rel=\"stylesheet\""} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the dashboard references %q; it must be entirely self-contained", forbidden)
		}
	}
}

// TestPortalWhereAmIApi asserts the JSON endpoint carries the full geodetic
// synthesis, including the eleven-character code the card does not print.
func TestPortalWhereAmIApi(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	rec := portalGet(t, portal, "/api/whereami")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/whereami = %v, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var got portalWhereAmI
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("whereami answer is not JSON: %v (%q)", err, rec.Body.String())
	}
	if !got.Valid {
		t.Fatal("Valid = false for a live fix")
	}
	if got.PlusCode != refPlus10 {
		t.Errorf("plus_code = %q, want %q", got.PlusCode, refPlus10)
	}
	if got.PlusCode11 != refPlus11 {
		t.Errorf("plus_code_11 = %q, want %q", got.PlusCode11, refPlus11)
	}
	if got.Maidenhead != refGrid {
		t.Errorf("maidenhead = %q, want %q", got.Maidenhead, refGrid)
	}
	if got.Satellites != 9 || got.FixQuality != 1 {
		t.Errorf("satellites/quality = %v/%v, want 9/1", got.Satellites, got.FixQuality)
	}
	if got.AltitudeFt < 466 || got.AltitudeFt > 468 {
		t.Errorf("altitude_ft = %v, want about 467", got.AltitudeFt)
	}
	if got.Sunset == "" || got.SunsetCountdown == "" {
		t.Errorf("sunset = %q, countdown = %q, want both", got.Sunset, got.SunsetCountdown)
	}
	if len(got.Lines) == 0 {
		t.Error("the answer carries no card lines")
	}
}

// TestPortalWhereAmIApiWithoutAFix asserts a device with no receiver answers
// honestly instead of emitting a card full of zeroes.
func TestPortalWhereAmIApiWithoutAFix(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, GPSFix{})
	rec := portalGet(t, portal, "/api/whereami")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/whereami = %v, want 200", rec.Code)
	}
	var got portalWhereAmI
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("whereami answer is not JSON: %v", err)
	}
	if got.Valid {
		t.Error("Valid = true with no receiver")
	}
	if got.Message == "" {
		t.Error("the answer says nothing about why there is no fix")
	}
	if got.PlusCode != "" {
		t.Errorf("plus_code = %q with no fix, want empty", got.PlusCode)
	}
}

// TestPortalQueryRunsOfflineFieldCommands asserts the chat box runs the survival
// intelligence in-process: the first-aid card comes back with no radio hop, no
// airtime, and no network of any kind.
func TestPortalQueryRunsOfflineFieldCommands(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		want    string
	}{
		{"med hypothermia", "[HYPOTHERMIA]"},
		{"firstaid hypothermia", "[HYPOTHERMIA]"},
		{"rx bleed", "[BLEEDING]"},
		{"@gobot med hypothermia", "[HYPOTHERMIA]"},
		{"/med hypothermia", "[HYPOTHERMIA]"},
		{"morse SOS", "..."},
		{"conv 5km mi", "mi"},
	}
	portal := portalFixture(t, sfFix())
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(map[string]string{"command": tc.command})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := decodeQuery(t, portalPost(t, portal, "/api/query", string(body)))
			joined := strings.Join(got.Lines, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("%v = %v, want it to contain %q", tc.command, got.Lines, tc.want)
			}
		})
	}
}

// TestPortalQueryUsesTheGNSSFixForNearCommands asserts the dashboard inherits
// the operator's position the same way the radio commands do.
func TestPortalQueryUsesTheGNSSFixForNearCommands(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	for _, command := range []string{"tower near", "tide near", "whereami", "sun"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(map[string]string{"command": command})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := decodeQuery(t, portalPost(t, portal, "/api/query", string(body)))
			joined := strings.Join(got.Lines, "\n")
			if strings.Contains(joined, "Usage:") {
				t.Errorf("%v asked for an argument despite a live fix: %v", command, got.Lines)
			}
			if !strings.Contains(joined, refPlus10) {
				t.Errorf("%v = %v, want the fix's Plus Code", command, got.Lines)
			}
		})
	}
}

// TestPortalQueryRefusesCommandsThatNeedALink asserts the portal says exactly
// what is missing instead of failing obscurely when a command needs a hub.
func TestPortalQueryRefusesCommandsThatNeedALink(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	body, err := json.Marshal(map[string]string{"command": "dnotice me hello"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := decodeQuery(t, portalPost(t, portal, "/api/query", string(body)))
	if joined := strings.Join(got.Lines, "\n"); !strings.Contains(joined, "hub link") {
		t.Errorf("dnotice over the portal = %v, want it to explain it needs a link", got.Lines)
	}
}

// TestPortalQueryAnswersUnknownCommands asserts a typo in the chat box gets the
// list of what does work.
func TestPortalQueryAnswersUnknownCommands(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	got := decodeQuery(t, portalPost(t, portal, "/api/query", `{"command":"frobnicate"}`))
	joined := strings.Join(got.Lines, "\n")
	if !strings.Contains(joined, "unknown command") {
		t.Errorf("unknown command = %v, want it named as unknown", got.Lines)
	}
	if !strings.Contains(joined, "med") {
		t.Errorf("unknown command = %v, want the offline command list", got.Lines)
	}
}

// TestPortalQueryRejectsBadRequests asserts the endpoint never panics or
// half-executes on a malformed or empty request.
func TestPortalQueryRejectsBadRequests(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body string }{
		{"empty", ""},
		{"not json", "{not json"},
		{"empty command", `{"command":"   "}`},
		{"json array", `["med"]`},
	}
	portal := portalFixture(t, sfFix())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := portalPost(t, portal, "/api/query", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("POST %q = %v, want 400", tc.body, rec.Code)
			}
		})
	}
}

// TestPortalMethodHandling asserts each route accepts only the method it
// defines, so a stray GET cannot trigger a command and a POST cannot fetch a
// page.
func TestPortalMethodHandling(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	for _, path := range []string{"/api/query"} {
		rec := portalGet(t, portal, path)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %v = %v, want 405", path, rec.Code)
		}
	}
	for _, path := range []string{"/", "/api/whereami", "/hotspot-detect.html"} {
		rec := portalPost(t, portal, path, "{}")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %v = %v, want 405", path, rec.Code)
		}
	}
}

// TestPortalUnknownPathIsNotFound asserts a phone that probes an endpoint the
// portal does not know about gets a plain 404 rather than the dashboard.
func TestPortalUnknownPathIsNotFound(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	if rec := portalGet(t, portal, "/no-such-page"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /no-such-page = %v, want 404", rec.Code)
	}
}

// TestPortalStartAndClose asserts the server really binds, really answers, and
// really stops, and that Close is safe to call twice.
func TestPortalStartAndClose(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	reader := NewGPSReader(nil)
	reader.SetFix(sfFix())
	t.Cleanup(func() { _ = reader.Close() })
	portal := newPortalServer("127.0.0.1:0", reader, reg)
	if err := portal.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := portal.Addr()
	if addr == "" || strings.HasSuffix(addr, ":0") {
		t.Fatalf("Addr() = %q, want the bound address", addr)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/whereami")
	if err != nil {
		t.Fatalf("GET /api/whereami: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %v, want 200", resp.StatusCode)
	}
	if err := portal.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := portal.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := client.Get("http://" + addr + "/api/whereami"); err == nil {
		t.Error("the server still answers after Close")
	}
}

// TestPortalStartNeedsAnAddress asserts a portal with no configured address
// refuses to bind instead of silently listening somewhere unexpected.
func TestPortalStartNeedsAnAddress(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	portal := newPortalServer("", nil, reg)
	if err := portal.Start(); err == nil {
		_ = portal.Close()
		t.Fatal("Start with an empty address succeeded")
	}
	if err := portal.Close(); err != nil {
		t.Errorf("Close with no listener: %v", err)
	}
}

// TestPortalDashboardShowsNoFix asserts the page a phone opens on a device whose
// receiver has not locked yet says so in words rather than showing 0.00000.
func TestPortalDashboardShowsNoFix(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, GPSFix{})
	body := portalGet(t, portal, "/").Body.String()
	if !strings.Contains(body, "acquiring") {
		t.Errorf("the dashboard does not say it is acquiring a fix")
	}
}

// TestPortalWhereAmIApiReportsAnUnmeasuredAltitude asserts the JSON marks a fix
// with no measured height, so the dashboard shows a dash rather than 0 m.
func TestPortalWhereAmIApiReportsAnUnmeasuredAltitude(t *testing.T) {
	t.Parallel()

	fix := sfFix()
	fix.HasAltitude = false
	fix.AltitudeM = 0
	portal := portalFixture(t, fix)
	rec := portalGet(t, portal, "/api/whereami")
	var got portalWhereAmI
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("whereami answer is not JSON: %v", err)
	}
	if got.HasAltitude {
		t.Error("has_altitude = true for a fix that carried none")
	}
	if !strings.Contains(rec.Body.String(), `"has_altitude":false`) {
		t.Errorf("answer = %v, want has_altitude false on the wire", rec.Body.String())
	}
	body := portalGet(t, portal, "/").Body.String()
	if strings.Contains(body, "0 m (0 ft) MSL") {
		t.Error("the dashboard invented a sea-level altitude")
	}
}
