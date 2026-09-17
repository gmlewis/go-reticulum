// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	var dir string
	var err error
	if runtime.GOOS == "darwin" {
		dir, err = os.MkdirTemp("/tmp", "test-update-offline-data-*")
	} else {
		dir, err = os.MkdirTemp("", "test-update-offline-data-*")
	}
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func TestRunHelpAndVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr, http.DefaultClient, "/tmp")
	if code != exitOK {
		t.Fatalf("run(--help) = %v, want %v", code, exitOK)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"--version"}, &stdout, &stderr, http.DefaultClient, "/tmp")
	if code != exitOK {
		t.Fatalf("run(--version) = %v, want %v", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "update-offline-data") {
		t.Fatalf("run(--version) output missing version prefix: %v", stdout.String())
	}
}

func TestRunMockDatasets(t *testing.T) {
	t.Parallel()

	mockXML := `<stations>
		<station id="41002" lat="31.743" lon="-74.955" name="South Hatteras" met="y"/>
		<station id="41004" lat="32.502" lon="-79.099" name="Edisto, SC" met="y"/>
		<station id="99999" lat="0.0" lon="0.0" name="Non-met" met="n"/>
	</stations>`

	mockJSON := `{"stations": [
		{"id": "9410135", "name": "South San Diego Bay", "state": "CA", "lat": 32.629101, "lng": -117.107803},
		{"id": "9410660", "name": "Los Angeles", "state": "CA", "lat": 33.72, "lng": -118.272}
	]}`

	mockCSV := `ident,type,name,latitude_deg,longitude_deg,iso_country,iso_region,municipality,scheduled_service,iata_code
BIKF,large_airport,Keflavik International Airport,63.985001,-22.6056,IS,IS-1,Reykjavik,yes,KEF
CYVR,large_airport,Vancouver International Airport,49.193901,-123.183998,CA,CA-BC,Vancouver,yes,YVR
`

	mockWMM := `<html><body><p>WMM2025 is valid until December 31, 2029</p></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "activestations.xml"):
			_, _ = w.Write([]byte(mockXML))
		case strings.HasSuffix(r.URL.Path, "stations.json"):
			_, _ = w.Write([]byte(mockJSON))
		case strings.HasSuffix(r.URL.Path, "airports.csv"):
			_, _ = w.Write([]byte(mockCSV))
		case strings.HasSuffix(r.URL.Path, "WMM.COF"):
			_, _ = w.Write([]byte(mockWMM))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	// Override default URLs for the test
	origNDBC := defaultNDBCURL
	origCOOPS := defaultCOOPSURL
	origOurAirports := defaultOurAirportsURL
	origWMM := defaultWMMURL

	defaultNDBCURL = ts.URL + "/activestations.xml"
	defaultCOOPSURL = ts.URL + "/stations.json"
	defaultOurAirportsURL = ts.URL + "/airports.csv"
	defaultWMMURL = ts.URL + "/WMM.COF"

	defer func() {
		defaultNDBCURL = origNDBC
		defaultCOOPSURL = origCOOPS
		defaultOurAirportsURL = origOurAirports
		defaultWMMURL = origWMM
	}()

	repoDir := tempDir(t)
	botDir := filepath.Join(repoDir, "bot")
	if err := os.MkdirAll(botDir, 0o755); err != nil {
		t.Fatalf("failed to create bot dir: %v", err)
	}

	// 1. Dry run on empty directory: should report [UPDATE AVAILABLE]
	var stdout, stderr bytes.Buffer
	code := run([]string{"-n"}, &stdout, &stderr, ts.Client(), repoDir)
	if code != exitOK {
		t.Fatalf("run(-n) = %v, want %v (stderr: %v)", code, exitOK, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "[UPDATE AVAILABLE] bot/buoy-stations.go") {
		t.Errorf("dry-run missing buoy update notice: %v", out)
	}
	if !strings.Contains(out, "[UPDATE AVAILABLE] bot/tide-stations.go") {
		t.Errorf("dry-run missing tide update notice: %v", out)
	}
	if !strings.Contains(out, "[UPDATE AVAILABLE] bot/metar-stations.go") {
		t.Errorf("dry-run missing metar update notice: %v", out)
	}
	if !strings.Contains(out, "[UP TO DATE] bot/declination.go: WMM2025") {
		t.Errorf("dry-run missing wmm status: %v", out)
	}

	// Verify no files written during dry run
	if _, err := os.Stat(filepath.Join(botDir, "buoy-stations.go")); err == nil {
		t.Fatalf("buoy-stations.go was written during dry-run!")
	}

	// 2. Real run: should generate files
	stdout.Reset()
	stderr.Reset()
	code = run([]string{}, &stdout, &stderr, ts.Client(), repoDir)
	if code != exitOK {
		t.Fatalf("run() = %v, want %v (stderr: %v)", code, exitOK, stderr.String())
	}
	out = stdout.String()
	if !strings.Contains(out, "[UPDATED] bot/buoy-stations.go (2 buoy stations)") {
		t.Errorf("run missing buoy updated notice: %v", out)
	}
	if !strings.Contains(out, "[UPDATED] bot/tide-stations.go (2 tide stations)") {
		t.Errorf("run missing tide updated notice: %v", out)
	}
	if !strings.Contains(out, "[UPDATED] bot/metar-stations.go (2 METAR airfields)") {
		t.Errorf("run missing metar updated notice: %v", out)
	}

	// 3. Second run: content is identical, should report [UP TO DATE]
	stdout.Reset()
	stderr.Reset()
	code = run([]string{}, &stdout, &stderr, ts.Client(), repoDir)
	if code != exitOK {
		t.Fatalf("second run = %v, want %v (stderr: %v)", code, exitOK, stderr.String())
	}
	out = stdout.String()
	if !strings.Contains(out, "[UP TO DATE] bot/buoy-stations.go") {
		t.Errorf("second run missing up to date buoy notice: %v", out)
	}
}
