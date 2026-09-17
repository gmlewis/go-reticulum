// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// The reference position the appliance tests use. It is the same point the
// shared engine's own tests pin, so a dashboard that stopped agreeing with the
// radio answer fails here rather than in the field.
const (
	appRefFix      = "37.7553,-122.4527"
	appRefPlusCode = "849VQG4W+4W"
	appRefHeading  = "042"
)

// writeRNSConfig writes the minimal Reticulum configuration a test appliance
// runs on: no interfaces at all, so nothing touches the real network, a shared
// instance named after this test's own directory so two suites cannot adopt each
// other's (on Linux the shared-instance socket is an abstract name shared across
// the whole machine), and reserved ports so two parallel packages cannot
// collide.
func writeRNSConfig(t *testing.T, dir, instanceName string) {
	t.Helper()
	content := fmt.Sprintf(`[reticulum]
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
instance_name = %v

[logging]
loglevel = 0

[interfaces]
`, testutils.ReserveTCPPort(t), testutils.ReserveTCPPort(t), instanceName)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing the Reticulum config: %v", err)
	}
}

// applianceConfig is the configuration an appliance test starts from: an
// isolated Reticulum directory, state under the test's own directory, a static
// position and heading instead of hardware, and the dashboard on a port the
// kernel picks so parallel tests never collide.
func applianceConfig(t *testing.T) Config {
	t.Helper()
	dir := tempDir(t)
	rnsDir := filepath.Join(dir, "rns")
	if err := os.MkdirAll(rnsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// The temp directory's own base name is unique to this test, which is what
	// keeps two appliance tests, or two packages, from sharing a Reticulum
	// shared-instance socket.
	writeRNSConfig(t, rnsDir, filepath.Base(dir))

	cfg := DefaultConfig()
	cfg.Device.Callsign = "GRL-TEST"
	cfg.Device.StorageDir = filepath.Join(dir, "storage")
	cfg.RNS.ConfigPath = rnsDir
	cfg.Portal.PortalAddr = "127.0.0.1:0"
	cfg.GNSS.StaticFix = appRefFix
	cfg.Compass.StaticHeading = appRefHeading
	return cfg
}

// startApp builds and starts an appliance and registers its shutdown.
func startApp(t *testing.T, cfg Config) *App {
	t.Helper()
	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return app
}

// portalGet issues one GET against the running appliance's dashboard.
func portalGet(t *testing.T, app *App, path string) *httptest.ResponseRecorder {
	t.Helper()
	portal := app.Portal()
	if portal == nil {
		t.Fatal("the appliance has no dashboard")
	}
	rec := httptest.NewRecorder()
	portal.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestAppStartServesTheDashboard asserts the whole appliance comes up and that
// the dashboard answers with the live position and heading: the promise that a
// phone joining the device's network sees the same picture the radio would
// carry, with zero radio hops.
func TestAppStartServesTheDashboard(t *testing.T) {
	app := startApp(t, applianceConfig(t))

	if app.PortalAddr() == "" {
		t.Fatal("the dashboard bound no address")
	}
	if app.Engine() == nil {
		t.Fatal("the appliance has no command engine")
	}
	if app.GPS() == nil {
		t.Error("a configured static fix did not produce a GNSS source")
	}
	if app.Compass() == nil {
		t.Error("a configured static heading did not produce a compass source")
	}

	t.Run("position", func(t *testing.T) {
		rec := portalGet(t, app, "/api/whereami")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/whereami = %v, want %v", rec.Code, http.StatusOK)
		}
		var answer struct {
			Valid       bool     `json:"valid"`
			PlusCode    string   `json:"plus_code"`
			Coordinates string   `json:"coordinates"`
			Lines       []string `json:"lines"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("decoding the position answer: %v\n%v", err, rec.Body.String())
		}
		if !answer.Valid {
			t.Errorf("the position answer is not valid: %v", rec.Body.String())
		}
		if answer.PlusCode != appRefPlusCode {
			t.Errorf("plus_code = %q, want %q", answer.PlusCode, appRefPlusCode)
		}
		if !strings.Contains(answer.Coordinates, "37.75530") {
			t.Errorf("coordinates = %q, want the configured static fix", answer.Coordinates)
		}
		if len(answer.Lines) == 0 || !strings.Contains(answer.Lines[0], "whereami") {
			t.Errorf("the answer carries no operational card: %v", answer.Lines)
		}
	})

	t.Run("heading", func(t *testing.T) {
		rec := portalGet(t, app, "/api/compass")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/compass = %v, want %v", rec.Code, http.StatusOK)
		}
		var answer struct {
			Valid       bool    `json:"valid"`
			MagneticDeg float64 `json:"mag_deg"`
			TrueDeg     float64 `json:"true_deg"`
			HasTrue     bool    `json:"has_true"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("decoding the compass answer: %v\n%v", err, rec.Body.String())
		}
		if !answer.Valid {
			t.Errorf("the compass answer is not valid: %v", rec.Body.String())
		}
		// The static heading is magnetic, so the World Magnetic Model corrects
		// it to true north at the configured position before anyone reads it.
		if answer.MagneticDeg != 42 {
			t.Errorf("mag_deg = %v, want the configured 42", answer.MagneticDeg)
		}
		if !answer.HasTrue {
			t.Errorf("has_true is false: %v", rec.Body.String())
		}
		if answer.TrueDeg <= answer.MagneticDeg {
			t.Errorf("true_deg = %v, want the east variation applied to %v",
				answer.TrueDeg, answer.MagneticDeg)
		}
	})

	t.Run("dashboard page", func(t *testing.T) {
		rec := portalGet(t, app, "/")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %v, want %v", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<html") {
			t.Errorf("the dashboard is not HTML:\n%v", body)
		}
		if !strings.Contains(body, appRefPlusCode) {
			t.Errorf("the dashboard does not carry the live Plus Code:\n%v", body)
		}
	})

	t.Run("captive probes redirect", func(t *testing.T) {
		for _, path := range []string{
			"/generate_204", "/gen_204", "/hotspot-detect.html",
			"/ncsi.txt", "/connecttest.txt",
		} {
			rec := portalGet(t, app, path)
			if rec.Code != http.StatusFound {
				t.Errorf("GET %v = %v, want %v", path, rec.Code, http.StatusFound)
			}
			if got := rec.Header().Get("Location"); got != "/" {
				t.Errorf("GET %v redirected to %q, want %q", path, got, "/")
			}
		}
	})

	t.Run("offline command", func(t *testing.T) {
		portal := app.Portal()
		rec := httptest.NewRecorder()
		body := strings.NewReader(`{"command":"whereami"}`)
		portal.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/query", body))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/query = %v, want %v", rec.Code, http.StatusOK)
		}
		var answer struct {
			Command string   `json:"command"`
			Lines   []string `json:"lines"`
			Error   string   `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("decoding the query answer: %v\n%v", err, rec.Body.String())
		}
		if answer.Error != "" {
			t.Fatalf("the query failed: %v", answer.Error)
		}
		if joined := strings.Join(answer.Lines, "\n"); !strings.Contains(joined, appRefPlusCode) {
			t.Errorf("the whereami reply does not carry %q:\n%v", appRefPlusCode, joined)
		}
	})
}

// TestAppEngineAnswersWithNoDashboard asserts the field assistant does not
// depend on the dashboard: with portal_addr empty the appliance binds no HTTP
// listener at all, yet the zero-hop command engine is still complete.
func TestAppEngineAnswersWithNoDashboard(t *testing.T) {
	cfg := applianceConfig(t)
	cfg.Portal.PortalAddr = ""
	app := startApp(t, cfg)

	if app.Portal() != nil || app.PortalAddr() != "" {
		t.Error("the appliance bound a dashboard although portal_addr was empty")
	}
	engine := app.Engine()
	if engine == nil {
		t.Fatal("the appliance has no command engine")
	}
	got, err := engine.Eval(context.Background(), "whereami")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !strings.Contains(got, appRefPlusCode) {
		t.Errorf("the offline card does not carry %q:\n%v", appRefPlusCode, got)
	}
	if offline := engine.OfflineCommands(); len(offline) == 0 {
		t.Error("the engine offers no offline commands")
	}
}

// TestAppRunsWithoutAnySensor asserts the appliance is fully usable with no
// hardware at all: it reports that no fix has arrived rather than inventing a
// position, which is what makes the same binary run on a desk.
func TestAppRunsWithoutAnySensor(t *testing.T) {
	cfg := applianceConfig(t)
	cfg.GNSS.StaticFix = ""
	cfg.Compass.StaticHeading = ""
	app := startApp(t, cfg)

	if app.GPS() != nil {
		t.Error("an appliance with no receiver and no static fix produced a GNSS source")
	}
	if app.Compass() != nil {
		t.Error("an appliance with no compass and no static heading produced a compass source")
	}
	rec := portalGet(t, app, "/api/whereami")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/whereami = %v, want %v", rec.Code, http.StatusOK)
	}
	var answer struct {
		Valid   bool     `json:"valid"`
		Message string   `json:"message"`
		Lines   []string `json:"lines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decoding the position answer: %v\n%v", err, rec.Body.String())
	}
	if answer.Valid {
		t.Errorf("the appliance claimed a fix with no hardware: %v", rec.Body.String())
	}
	// The dashboard reports the missing fix in its message, and the offline
	// command engine says the same thing in its own words; both must be honest
	// rather than inventing a position.
	if !strings.Contains(answer.Message, "no position yet") {
		t.Errorf("the dashboard did not report the missing fix: %q", answer.Message)
	}
	if got, err := app.Engine().Eval(context.Background(), "whereami"); err != nil {
		t.Fatalf("Eval: %v", err)
	} else if !strings.Contains(got, "no GNSS fix") {
		t.Errorf("the offline engine did not report the missing fix:\n%v", got)
	}
}

// TestAppStorageSurvivesARestart asserts the appliance keeps its own state under
// the configured storage directory, so distress beacons and situation reports
// outlive the process.
func TestAppStorageSurvivesARestart(t *testing.T) {
	cfg := applianceConfig(t)
	app := startApp(t, cfg)

	if _, err := app.Engine().Eval(context.Background(), "sos", "raise", "test beacon"); err != nil {
		t.Fatalf("Eval(sos raise): %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(cfg.Device.StorageDir)
	if err != nil {
		t.Fatalf("the appliance wrote no state under %v: %v", cfg.Device.StorageDir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("the appliance wrote no state under %v", cfg.Device.StorageDir)
	}
}

// TestAppLifecycleIsGuarded asserts the appliance refuses a second start, a
// start after a close, and a close after a close: a caller that gets the
// lifecycle wrong is told so rather than silently sharing resources.
func TestAppLifecycleIsGuarded(t *testing.T) {
	cfg := applianceConfig(t)
	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Start(ctx); err == nil {
		t.Error("a second Start was accepted")
	}
	if err := app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Errorf("a second Close failed: %v", err)
	}
	if err := app.Start(ctx); err == nil {
		t.Error("a Start after Close was accepted")
	}
	if app.Engine() != nil || app.Portal() != nil {
		t.Error("the appliance kept a resource after Close")
	}
}

// TestAppStartWithACancelledContext asserts an appliance whose caller has
// already given up starts nothing at all.
func TestAppStartWithACancelledContext(t *testing.T) {
	app, err := NewApp(applianceConfig(t))
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Start(ctx); err == nil {
		t.Fatal("Start ran with a cancelled context")
	}
	if app.Portal() != nil || app.GPS() != nil {
		t.Error("a cancelled Start left a resource behind")
	}
	if err := app.Close(); err != nil {
		t.Errorf("Close after a cancelled Start: %v", err)
	}
}

// TestNewAppRejectsAnUnusableConfiguration asserts a typo is refused before any
// socket, device, or goroutine exists.
func TestNewAppRejectsAnUnusableConfiguration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		tune func(*Config)
	}{
		{"no callsign", func(c *Config) { c.Device.Callsign = "" }},
		{"unusable portal address", func(c *Config) { c.Portal.PortalAddr = "127.0.0.1" }},
		{"unusable static fix", func(c *Config) { c.GNSS.StaticFix = "somewhere north" }},
		{"unusable static heading", func(c *Config) { c.Compass.StaticHeading = "sideways" }},
		{"no line speed", func(c *Config) { c.GNSS.Baud = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultConfig()
			tc.tune(&cfg)
			if _, err := NewApp(cfg); err == nil {
				t.Fatal("NewApp accepted an unusable configuration")
			}
		})
	}
}

// TestAppRefusesAnUnusablePortalAddress asserts a dashboard that cannot bind is
// reported instead of leaving an appliance that looks healthy but serves
// nothing.
func TestAppRefusesAnUnusablePortalAddress(t *testing.T) {
	cfg := applianceConfig(t)
	// A port already held by this test is the one address the dashboard cannot
	// have, and it is a stand-in for every "something else is on 9111" case in
	// the field.
	listener := httptest.NewServer(http.NotFoundHandler())
	defer listener.Close()
	cfg.Portal.PortalAddr = strings.TrimPrefix(listener.URL, "http://")

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("Start bound an address that was already taken")
	}
	// A failed start must not leave the Reticulum stack running behind it.
	if err := app.Close(); err != nil {
		t.Errorf("Close after a failed Start: %v", err)
	}
}
