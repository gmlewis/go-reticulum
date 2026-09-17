// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"encoding/json"
	"fmt"
	"math"
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
	return portalCompassFixture(t, fix, CompassHeading{})
}

// portalCompassFixture builds the same portal with a live compass reading
// behind it, which is the state a phone finds a device that carries a
// magnetometer as well as a receiver.
func portalCompassFixture(t *testing.T, fix GPSFix, heading CompassHeading) *PortalServer {
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
	if heading.Valid {
		compass := NewCompassReader(nil)
		compass.SetHeading(heading)
		t.Cleanup(func() { _ = compass.Close() })
		reg.compass = compass
	}
	portal := newPortalServer("127.0.0.1:0", reader, reg)
	// The solar clock and the sunset countdown are pinned, so the dashboard
	// tests do not depend on the hour the suite happens to run at.
	portal.now = func() time.Time {
		return time.Date(2026, time.September, 16, 20, 45, 33, 0, time.UTC)
	}
	t.Cleanup(func() { _ = portal.Close() })
	return portal
}

// portalReferenceHeading is the heading the Phase 0.6 brief prints on the card:
// 029° magnetic, 13° east variation, 042° true, in the north-east sector.
func portalReferenceHeading() CompassHeading {
	return CompassHeading{
		Valid: true, MagneticDeg: 29, TrueDeg: 42, DeclinationDeg: 13,
		HasDeclination: true, HasMagnetic: true, HasTrue: true, Cardinal: "NE",
		TimeUTC: time.Date(2026, time.September, 16, 20, 45, 33, 0, time.UTC),
	}
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

// decodeCompass decodes one /api/compass answer.
func decodeCompass(t *testing.T, rec *httptest.ResponseRecorder) portalCompass {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("compass status = %v, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var got portalCompass
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("compass answer is not JSON: %v (%q)", err, rec.Body.String())
	}
	return got
}

// TestPortalCompassApiServesTheLiveHeading asserts the compass endpoint answers
// the whole reading, including the nearest site the rose vectors toward.
func TestPortalCompassApiServesTheLiveHeading(t *testing.T) {
	t.Parallel()

	portal := portalCompassFixture(t, sfFix(), portalReferenceHeading())
	rec := portalGet(t, portal, "/api/compass")
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	got := decodeCompass(t, rec)
	if !got.Valid {
		t.Fatal("valid = false with a live compass")
	}
	if got.MagneticDeg != 29 || got.TrueDeg != 42 || got.DeclinationDeg != 13 {
		t.Errorf("headings = mag %v, true %v, var %v, want 29/42/13",
			got.MagneticDeg, got.TrueDeg, got.DeclinationDeg)
	}
	if !got.HasTrue || !got.HasMagnetic || !got.HasDeclination {
		t.Errorf("frame flags = %v/%v/%v, want all true",
			got.HasTrue, got.HasMagnetic, got.HasDeclination)
	}
	if got.Cardinal != "NE" {
		t.Errorf("cardinal = %q, want NE", got.Cardinal)
	}
	if got.Target == nil {
		t.Fatal("the compass carries no nearest site")
	}
	if got.Target.ID != "W6PW-2M" {
		t.Errorf("target = %q, want the nearest site W6PW-2M", got.Target.ID)
	}
	want := InitialBearing(sfFix().Position(), targetPoint(t, "W6PW-2M"))
	if math.Abs(got.Target.BearingDeg-want) > 0.5 {
		t.Errorf("target bearing = %v, want %v", got.Target.BearingDeg, want)
	}
	if got.Target.Steering == "" {
		t.Error("a target with a true heading carries no steering instruction")
	}
	if !strings.Contains(got.Target.Steering, "RIGHT") {
		t.Errorf("steering = %q, want a right turn toward the site", got.Target.Steering)
	}
}

// targetPoint returns the position of one catalog site.
func targetPoint(t *testing.T, id string) LatLng {
	t.Helper()
	record, ok := towerRecordByID(towerRecords, id)
	if !ok {
		t.Fatalf("the embedded catalog no longer holds %v", id)
	}
	return LatLng{Lat: record.Lat, Lng: record.Lng}
}

// TestPortalCompassApiWithoutACompass asserts a device with no compass says so
// instead of reporting a heading of zero, which would point the operator due
// north.
func TestPortalCompassApiWithoutACompass(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	got := decodeCompass(t, portalGet(t, portal, "/api/compass"))
	if got.Valid {
		t.Error("valid = true with no compass")
	}
	if got.Message == "" {
		t.Error("the answer says nothing about why there is no heading")
	}
	if got.Target != nil {
		t.Errorf("target = %+v with no compass, want none", got.Target)
	}
}

// TestPortalCompassApiWorksStandingStill asserts the one thing a compass does
// that a receiver cannot: it answers with no position fix at all. A device
// whose receiver has not locked still knows which way it is facing.
func TestPortalCompassApiWorksStandingStill(t *testing.T) {
	t.Parallel()

	portal := portalCompassFixture(t, GPSFix{}, portalReferenceHeading())
	got := decodeCompass(t, portalGet(t, portal, "/api/compass"))
	if !got.Valid {
		t.Fatal("valid = false without a fix, want the compass to answer anyway")
	}
	if got.Cardinal != "NE" {
		t.Errorf("cardinal = %q, want NE", got.Cardinal)
	}
	if got.Target != nil {
		t.Errorf("target = %+v with no fix, want none", got.Target)
	}
}

// TestPortalWhereAmIApiCarriesTheHeading asserts the dashboard's main endpoint
// carries the same heading object, so a phone makes one request to draw the
// whole page.
func TestPortalWhereAmIApiCarriesTheHeading(t *testing.T) {
	t.Parallel()

	portal := portalCompassFixture(t, sfFix(), portalReferenceHeading())
	rec := portalGet(t, portal, "/api/whereami")
	var got portalWhereAmI
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("whereami answer is not JSON: %v", err)
	}
	if got.Heading == nil {
		t.Fatal("the answer carries no heading")
	}
	if got.Heading.TrueDeg != 42 || got.Heading.MagneticDeg != 29 || got.Heading.Cardinal != "NE" {
		t.Errorf("heading = %+v, want 42 true / 29 magnetic / NE", got.Heading)
	}
	if !strings.Contains(strings.Join(got.Lines, "\n"), "Heading / Course  : 042° True") {
		t.Errorf("the card lines carry no heading:\n%v", strings.Join(got.Lines, "\n"))
	}
	if !strings.Contains(rec.Body.String(), `"heading"`) {
		t.Error("the heading is not on the wire")
	}
}

// TestPortalWhereAmIApiOmitsTheHeadingWithoutACompass asserts the JSON stays
// exactly what it was on a node with no compass: no empty object, no zeroes to
// mistake for a heading.
func TestPortalWhereAmIApiOmitsTheHeadingWithoutACompass(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	rec := portalGet(t, portal, "/api/whereami")
	if strings.Contains(rec.Body.String(), `"heading"`) {
		t.Errorf("answer = %v, want no heading field", rec.Body.String())
	}
}

// TestPortalDashboardDrawsTheCompassRose asserts the dashboard carries a real
// compass: a dial, a heading needle at the heading's own angle, a target needle
// at the site's bearing, and the numbers beside them.
func TestPortalDashboardDrawsTheCompassRose(t *testing.T) {
	t.Parallel()

	portal := portalCompassFixture(t, sfFix(), portalReferenceHeading())
	body := portalGet(t, portal, "/").Body.String()
	for _, want := range []string{
		`id="compass-rose"`,
		`id="compass"`,
		`id="heading-needle"`,
		`id="target-needle"`,
		`id="compass-heading"`,
		"042° NE",
		"True 042° (var +13.0° E)",
		"Mag 029°",
		"W6PW-2M",
		"Your heading",
		"Beacon or nearest site",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dashboard does not contain %q", want)
		}
	}
	// The dial is north-up, so the heading needle is drawn at the heading's own
	// true bearing and the target needle at the site's.
	if !strings.Contains(body, "rotate(42.0 100 100)") {
		t.Errorf("the dashboard does not rotate the heading needle to 42 degrees")
	}
	wantBearing := InitialBearing(sfFix().Position(), targetPoint(t, "W6PW-2M"))
	if !strings.Contains(body, fmt.Sprintf("rotate(%.1f 100 100)", wantBearing)) {
		t.Errorf("the dashboard does not rotate the target needle to %.1f degrees", wantBearing)
	}
}

// TestPortalDashboardCompassPanelWithoutACompass asserts the empty state is a
// sentence rather than a dial pointing at nothing.
func TestPortalDashboardCompassPanelWithoutACompass(t *testing.T) {
	t.Parallel()

	portal := portalFixture(t, sfFix())
	body := portalGet(t, portal, "/").Body.String()
	if !strings.Contains(body, "no compass configured") {
		t.Errorf("the dashboard does not explain the missing compass")
	}
	if !strings.Contains(body, `id="compass-rose"`) {
		t.Errorf("the dashboard dropped the compass panel entirely")
	}
}

// TestPortalCompassMethodHandling asserts the compass endpoint takes a GET and
// nothing else, like every other read-only route.
func TestPortalCompassMethodHandling(t *testing.T) {
	t.Parallel()

	portal := portalCompassFixture(t, sfFix(), portalReferenceHeading())
	for _, path := range []string{"/api/compass"} {
		if rec := portalPost(t, portal, path, "{}"); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %v = %v, want 405", path, rec.Code)
		}
	}
}

// TestPortalDashboardLeavesNoTokensBehind asserts every @@TOKEN@@ the template
// declares is substituted before the page is served. An unsubstituted token is
// a page a traveler reads as garbage on the worst day of their life, and it is
// invisible to a test that only looks for the parts that did render.
func TestPortalDashboardLeavesNoTokensBehind(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		heading CompassHeading
	}{
		{"with a compass", portalReferenceHeading()},
		{"without a compass", CompassHeading{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			portal := portalCompassFixture(t, sfFix(), tc.heading)
			body := portalGet(t, portal, "/").Body.String()
			if strings.Contains(body, "@@") {
				index := strings.Index(body, "@@")
				t.Errorf("the dashboard still carries a template token: %q", body[index:index+20])
			}
			for _, forbidden := range []string{"http://", "https://", "//cdn", "<link rel=\"stylesheet\""} {
				if strings.Contains(body, forbidden) {
					t.Errorf("the dashboard references %q; it must be entirely self-contained", forbidden)
				}
			}
		})
	}
}

// TestPortalCompassVectorsTowardAnActiveBeacon asserts the rose points at the
// person in trouble rather than at the nearest repeater: an active distress
// beacon outranks every routine site, because the operator looking at the phone
// is looking for somebody.
func TestPortalCompassVectorsTowardAnActiveBeacon(t *testing.T) {
	t.Parallel()

	portal := portalCompassFixture(t, sfFix(), portalReferenceHeading())
	// A beacon close to the device, east of it, so the expected bearing is easy
	// to reason about.
	beaconPoint := LatLng{Lat: refLat, Lng: refLng + 0.05}
	portal.run.(*registry).bot.sos = newSOSStore(tempDir(t))
	if _, err := portal.run.(*registry).sos().add(SOSRecord{
		Sender: "@hiker", LatLng: beaconPoint, Location: "test", Triage: "RED",
		Details: "twisted ankle",
	}); err != nil {
		t.Fatalf("recording a beacon: %v", err)
	}

	got := decodeCompass(t, portalGet(t, portal, "/api/compass"))
	if got.Target == nil {
		t.Fatal("the compass carries no target")
	}
	if got.Target.Kind != "beacon" {
		t.Errorf("target kind = %q, want a beacon to outrank the site", got.Target.Kind)
	}
	if got.Target.Triage != "RED" {
		t.Errorf("target triage = %q, want RED", got.Target.Triage)
	}
	want := normalizeDegrees(InitialBearing(sfFix().Position(), beaconPoint))
	if math.Abs(got.Target.BearingDeg-want) > 0.5 {
		t.Errorf("beacon bearing = %v, want %v", got.Target.BearingDeg, want)
	}
	if got.Target.Steering == "" {
		t.Error("a beacon with a true heading carries no steering instruction")
	}

	body := portalGet(t, portal, "/").Body.String()
	if !strings.Contains(body, "SOS RED beacon") {
		t.Error("the dashboard does not name the distress beacon it points at")
	}
}

// TestPortalCompassVectorsTowardASiteWithNoBeacon asserts a quiet device points
// at the nearest communications site and says that is what it is.
func TestPortalCompassVectorsTowardASiteWithNoBeacon(t *testing.T) {
	t.Parallel()

	portal := portalCompassFixture(t, sfFix(), portalReferenceHeading())
	got := decodeCompass(t, portalGet(t, portal, "/api/compass"))
	if got.Target == nil || got.Target.Kind != "site" {
		t.Fatalf("target = %+v, want the nearest site when nothing is in distress", got.Target)
	}
}
