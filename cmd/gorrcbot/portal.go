// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the captive web portal: the survival dashboard a traveler's
// smartphone opens by itself the moment it joins the device's Wi-Fi.
//
// The design constraint is that nothing may be installed and nothing may be
// fetched. A phone in airplane mode, or a phone whose owner never expected to
// need an app for the worst day of their life, must be able to read the current
// position, raise a distress beacon, and ask the field assistant a question
// with the browser it already has. So the portal is one self-contained page
// with inline CSS and JavaScript, no external stylesheet, font, image, or
// script of any kind, and it answers the captive-network probes the operating
// systems use — Apple's hotspot-detect, Android's generate_204, and Windows'
// ncsi.txt — with the redirect that pops the native captive browser open.
//
// The command surface is the same registry the radio commands use, restricted
// to the commands marked local: the answers that are computed in-process. That
// is what keeps the portal honest about the Autonomous Local Intelligence Rule,
// and what makes "med hypothermia" answer in microseconds with no radio hop.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Portal bounds and wordings.
const (
	// portalMaxQueryBytes bounds one chat request. A field command is a short
	// line, and the bound is what keeps a phone on the same Wi-Fi from turning
	// the device into a memory sink.
	portalMaxQueryBytes = 8 << 10
	// portalNoFixMessage is what the dashboard shows while the receiver has no
	// lock. It is deliberately a sentence, not an empty panel.
	portalNoFixMessage = "acquiring a GNSS fix — no position yet"
	// portalReadHeaderTimeout bounds how long one connection may take to send
	// its headers, so a stalled phone cannot hold a socket forever.
	portalReadHeaderTimeout = 10 * time.Second
	// portalIdleTimeout bounds an idle keep-alive connection.
	portalIdleTimeout = 60 * time.Second
	// portalErrNoAddr reports an attempt to start a portal with no address.
	portalErrNoAddr = "portal: no portal_addr is configured"
)

// localRunner is the command surface the portal may use: the subset of the
// registry that computes its answer entirely in-process. The registry
// implements it, and a test can substitute its own.
type localRunner interface {
	// RunLocal runs one command line with no hub session behind it.
	RunLocal(line string) []string
}

// portalWhereAmI is the JSON the dashboard renders. It carries the same
// synthesis the operational card prints, plus the eleven-character code and the
// machine-readable numbers the page formats itself.
type portalWhereAmI struct {
	// Valid reports whether a live fix is behind the answer.
	Valid bool `json:"valid"`
	// Message explains an answer with no fix behind it.
	Message string `json:"message,omitempty"`
	// Lat and Lng are the WGS-84 decimal degrees.
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	// PlusCode is the ten-character Open Location Code.
	PlusCode string `json:"plus_code"`
	// PlusCode11 is the eleven-character code.
	PlusCode11 string `json:"plus_code_11"`
	// Area is the precision the ten-character code carries, as text.
	Area string `json:"area"`
	// Maidenhead is the six-character grid locator.
	Maidenhead string `json:"maidenhead"`
	// Coordinates is the card's own coordinate line.
	Coordinates string `json:"coordinates"`
	// AltitudeM and AltitudeFt are the antenna altitude in both units.
	AltitudeM  float64 `json:"altitude_m"`
	AltitudeFt float64 `json:"altitude_ft"`
	// HasAltitude reports that the receiver measured the height, so the
	// dashboard can show a dash rather than sea level for a fix without one.
	HasAltitude bool `json:"has_altitude"`
	// SpeedKnots and CourseDeg are the receiver's motion.
	SpeedKnots float64 `json:"speed_knots"`
	CourseDeg  float64 `json:"course_deg"`
	// Satellites, HDOP, and FixQuality describe how good the fix is.
	Satellites int     `json:"satellites"`
	HDOP       float64 `json:"hdop"`
	FixQuality int     `json:"fix_quality"`
	// FixStatus is the one-line receiver status the card prints.
	FixStatus string `json:"fix_status"`
	// TimeUTC is the receiver's own timestamp.
	TimeUTC string `json:"time_utc,omitempty"`
	// GCJ02 is the China datum coordinate, empty outside China.
	GCJ02 string `json:"gcj02,omitempty"`
	// LocalSolarTime, SolarNoon, Sunset, and SunsetCountdown are the solar
	// facts the dashboard clock shows.
	LocalSolarTime  string `json:"local_solar_time,omitempty"`
	SolarNoon       string `json:"solar_noon,omitempty"`
	Sunset          string `json:"sunset,omitempty"`
	SunsetCountdown string `json:"sunset_countdown,omitempty"`
	// Lines is the operational card exactly as the whereami command renders
	// it, so the dashboard and a radio reply never disagree.
	Lines []string `json:"lines,omitempty"`
}

// portalQueryRequest is one chat request from the dashboard.
type portalQueryRequest struct {
	// Command is the command line, with or without an addressed nick and with
	// or without a leading slash.
	Command string `json:"command"`
	// Text is accepted as a synonym so a plain HTML form post works too.
	Text string `json:"text"`
}

// portalQueryResponse is one chat answer.
type portalQueryResponse struct {
	// Command is the normalized command line that ran.
	Command string `json:"command"`
	// Lines is the reply, one line per element.
	Lines []string `json:"lines"`
	// Error is set when nothing ran.
	Error string `json:"error,omitempty"`
}

// PortalServer is the captive portal HTTP server. Its zero value is unusable;
// newPortalServer builds one.
type PortalServer struct {
	// addr is the listen address, empty when the operator has not enabled the
	// portal. Start refuses an empty one.
	addr string
	// gps is the GNSS source the dashboard reads. It is nil on a node with no
	// receiver, and the dashboard then says it is acquiring.
	gps *GPSReader
	// run is the local command surface.
	run localRunner
	// now is the clock, injected so a test can pin the solar clock.
	now func() time.Time
	// handler is the mux, built once.
	handler http.Handler

	mu  sync.Mutex
	srv *http.Server
	ln  net.Listener
}

// newPortalServer builds a portal. It binds nothing until Start runs, so a test
// can drive the handler directly.
func newPortalServer(addr string, gps *GPSReader, run localRunner) *PortalServer {
	p := &PortalServer{addr: strings.TrimSpace(addr), gps: gps, run: run, now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", p.handleDashboard)
	mux.HandleFunc("GET /hotspot-detect.html", p.handleCaptiveProbe)
	mux.HandleFunc("GET /generate_204", p.handleCaptiveProbe)
	mux.HandleFunc("GET /gen_204", p.handleCaptiveProbe)
	mux.HandleFunc("GET /ncsi.txt", p.handleCaptiveProbe)
	mux.HandleFunc("GET /connecttest.txt", p.handleCaptiveProbe)
	mux.HandleFunc("GET /api/whereami", p.handleWhereAmI)
	mux.HandleFunc("POST /api/query", p.handleQuery)
	p.handler = mux
	return p
}

// Handler returns the portal's HTTP handler, which is what a test drives
// directly and what Start serves.
func (p *PortalServer) Handler() http.Handler { return p.handler }

// Start binds the configured address and serves in the background. It returns
// once the listener exists, so a caller knows whether the port was available.
func (p *PortalServer) Start() error {
	if p.addr == "" {
		return errors.New(portalErrNoAddr)
	}
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return fmt.Errorf("portal: listen %v: %w", p.addr, err)
	}
	srv := &http.Server{
		Handler:           p.handler,
		ReadHeaderTimeout: portalReadHeaderTimeout,
		IdleTimeout:       portalIdleTimeout,
	}
	p.mu.Lock()
	p.ln = ln
	p.srv = srv
	p.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("portal: serve %v: %v", p.addr, err)
		}
	}()
	return nil
}

// Addr returns the address the portal is bound to, which is the useful form
// after Start resolved a port of zero. It is empty before Start.
func (p *PortalServer) Addr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln == nil {
		return ""
	}
	return p.ln.Addr().String()
}

// Close stops the server and waits for nothing further: the listener is closed
// and every in-flight request is allowed to finish. It is idempotent, so a
// shutdown path may call it without tracking whether Start ran.
func (p *PortalServer) Close() error {
	p.mu.Lock()
	srv := p.srv
	p.ln = nil
	p.srv = nil
	p.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Close()
}

// handleCaptiveProbe answers one operating system's captive-network check with
// the redirect that makes the native browser open the dashboard. A probe that
// is answered with the content the operating system expects is read as "the
// internet works", which is exactly the wrong answer here.
func (p *PortalServer) handleCaptiveProbe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusFound)
}

// handleDashboard serves the self-contained survival dashboard, with the live
// position already rendered into it so the page is readable before any script
// runs on a phone with a cold battery.
func (p *PortalServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write([]byte(p.renderDashboard())); err != nil {
		logf("portal: could not write the dashboard: %v", err)
	}
}

// handleWhereAmI serves the current fix and its geodetic synthesis as JSON.
func (p *PortalServer) handleWhereAmI(w http.ResponseWriter, r *http.Request) {
	writePortalJSON(w, http.StatusOK, p.currentWhereAmI())
}

// handleQuery runs one local field command and returns its reply lines. Only
// the commands marked local are reachable: a command that needs a live hub
// answers with a line saying so rather than failing.
func (p *PortalServer) handleQuery(w http.ResponseWriter, r *http.Request) {
	if p.run == nil {
		writePortalJSON(w, http.StatusServiceUnavailable, portalQueryResponse{
			Error: "the command registry is unavailable on this device",
		})
		return
	}
	body := http.MaxBytesReader(w, r.Body, portalMaxQueryBytes)
	var req portalQueryRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		writePortalJSON(w, http.StatusBadRequest, portalQueryResponse{
			Error: "send {\"command\": \"...\"} as JSON",
		})
		return
	}
	line := portalCommandLine(firstNonEmptyText(req.Command, req.Text))
	if line == "" {
		writePortalJSON(w, http.StatusBadRequest, portalQueryResponse{Error: "the command is empty"})
		return
	}
	writePortalJSON(w, http.StatusOK, portalQueryResponse{Command: line, Lines: p.run.RunLocal(line)})
}

// writePortalJSON encodes one JSON answer. An encoding failure after the
// headers are out can only be logged, which is what it is here.
func writePortalJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		logf("portal: could not write a JSON answer: %v", err)
	}
}

// portalCommandLine normalizes a chat line: it drops the addressed nick the way
// the reply policy does, so "@gobot med hypothermia" reaches the same command
// as "med hypothermia". A leading slash is dropped by the command splitter, so
// both notations the field guides use work.
func portalCommandLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "@") {
		if idx := strings.IndexAny(trimmed, " \t"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[idx+1:])
		} else {
			trimmed = ""
		}
	}
	return strings.TrimSpace(trimmed)
}

// firstNonEmptyText returns the first value with any content, so either JSON
// field name reaches the same command.
func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// currentWhereAmI synthesizes the dashboard's answer from the live fix.
func (p *PortalServer) currentWhereAmI() portalWhereAmI {
	if p.gps == nil {
		return portalWhereAmI{Valid: false, Message: portalNoFixMessage}
	}
	fix := p.gps.LastFix()
	if !fix.Valid {
		return portalWhereAmI{Valid: false, Message: portalNoFixMessage}
	}
	built, err := buildWhereAmI(fix, fix.Position(), whereamiSourceGNSS, p.now())
	if err != nil {
		logf("portal: %v", err)
		return portalWhereAmI{Valid: false, Message: "the geodetic engine could not place the fix: " + err.Error()}
	}
	view := portalWhereAmI{
		Valid:          true,
		Lat:            built.Point.Lat,
		Lng:            built.Point.Lng,
		PlusCode:       built.PlusCode,
		PlusCode11:     built.PlusCode11,
		Area:           fmt.Sprintf("~%vm x %vm", math.Round(built.AreaLatMeters), math.Round(built.AreaLngMeters)),
		Maidenhead:     built.Maidenhead,
		Coordinates:    formatCardCoordinates(built.Point),
		AltitudeM:      math.Round(built.Fix.AltitudeM),
		AltitudeFt:     math.Round(built.Fix.AltitudeM * feetPerMeter),
		HasAltitude:    built.Fix.HasAltitude,
		SpeedKnots:     built.Fix.SpeedKnots,
		CourseDeg:      built.Fix.CourseDeg,
		Satellites:     built.Fix.Satellites,
		HDOP:           built.Fix.HDOP,
		FixQuality:     built.Fix.FixQuality,
		FixStatus:      built.FixStatus,
		GCJ02:          built.GCJ02,
		LocalSolarTime: fmt.Sprintf("%v %v", built.Now.In(built.Zone).Format("15:04"), built.ZoneLabel),
		SolarNoon:      built.Almanac.Noon.In(built.Zone).Format("15:04"),
		Sunset:         whereamiSunsetText(built),
	}
	if !built.Fix.TimeUTC.IsZero() {
		view.TimeUTC = built.Fix.TimeUTC.UTC().Format(time.RFC3339)
	}
	if built.Almanac.Sunset.After(built.Now) {
		view.SunsetCountdown = formatDaylightRemaining(built.Almanac.Sunset.Sub(built.Now))
	}
	view.Lines = renderWhereAmICard(built)
	return view
}

// renderDashboard renders the self-contained page for the current fix.
func (p *PortalServer) renderDashboard() string {
	view := p.currentWhereAmI()
	plusCode := view.PlusCode
	fixStatus := view.FixStatus
	if !view.Valid {
		plusCode = "—"
		if view.Message == "" {
			view.Message = portalNoFixMessage
		}
		fixStatus = view.Message
	}
	data, err := json.Marshal(view)
	if err != nil {
		data = []byte(`{"valid":false}`)
	}
	return strings.NewReplacer(
		"@@PLUS_CODE@@", html.EscapeString(plusCode),
		"@@COORDINATES@@", html.EscapeString(orDash(view.Coordinates)),
		"@@MAIDENHEAD@@", html.EscapeString(orDash(view.Maidenhead)),
		"@@ELEVATION@@", html.EscapeString(orDash(portalElevation(view))),
		"@@FIX_STATUS@@", html.EscapeString(fixStatus),
		"@@SUNSET@@", html.EscapeString(orDash(view.Sunset)),
		"@@SOLAR_CLOCK@@", html.EscapeString(orDash(view.LocalSolarTime)),
		"@@INITIAL_JSON@@", string(data),
	).Replace(portalDashboardHTML)
}

// portalElevation renders the elevation cell, or a dash when the fix carries
// none.
func portalElevation(view portalWhereAmI) string {
	if !view.Valid || !view.HasAltitude {
		return ""
	}
	return fmt.Sprintf("%v m (%v ft) MSL", view.AltitudeM, view.AltitudeFt)
}

// orDash renders an empty value as a dash, so a panel never shows a blank cell.
func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
