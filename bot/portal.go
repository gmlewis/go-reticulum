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

package bot

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
	// portalNoCompassMessage is what the dashboard's compass panel shows on a
	// node with no compass. It is a sentence for the same reason: an empty dial
	// looks broken, and a broken dial is worse than an honest one.
	portalNoCompassMessage = "no compass configured — heading unavailable"
	// portalReadHeaderTimeout bounds how long one connection may take to send
	// its headers, so a stalled phone cannot hold a socket forever.
	portalReadHeaderTimeout = 10 * time.Second
	// portalIdleTimeout bounds an idle keep-alive connection.
	portalIdleTimeout = 60 * time.Second
	// portalErrNoAddr reports an attempt to start a portal with no address.
	portalErrNoAddr = "portal: no portal_addr is configured"
)

// LocalRunner is the command surface the portal may use: the subset of the
// registry that computes its answer entirely in-process. The registry
// implements it, Engine implements it, and a test can substitute its own.
type LocalRunner interface {
	// RunLocal runs one command line with no hub session behind it.
	RunLocal(line string) []string
}

// headingSource is the compass half of the command surface: the live heading
// the dashboard draws. The registry implements it, and a portal whose command
// surface does not simply has no heading to show.
type headingSource interface {
	currentHeading() (CompassHeading, bool)
}

// beaconSource is the distress-beacon half of the command surface: the active
// beacons the compass rose vectors toward ahead of any routine site. The
// registry implements it, and a portal whose command surface does not simply
// has no beacon to point at.
type beaconSource interface {
	activeBeacons() []SOSRecord
}

// The two kinds of thing the compass rose can vector toward. A distress beacon
// outranks every communications site: an operator looking for somebody in
// trouble needs the person, not the repeater.
const (
	portalTargetBeacon = "beacon"
	portalTargetSite   = "site"
)

// portalCompass is the JSON shape of one live compass reading: the two frames
// the instrument can report, the variation between them, and the nearest
// communications site the compass rose points its second needle at.
type portalCompass struct {
	// Valid reports whether a live heading is behind the answer.
	Valid bool `json:"valid"`
	// Message explains an answer with no compass behind it.
	Message string `json:"message,omitempty"`
	// MagneticDeg and TrueDeg are the two headings, 0 to 360.
	MagneticDeg float64 `json:"mag_deg"`
	TrueDeg     float64 `json:"true_deg"`
	// DeclinationDeg is the local magnetic variation, positive east.
	DeclinationDeg float64 `json:"declination_deg"`
	// HasDeclination, HasMagnetic, and HasTrue report which of those numbers
	// are actually known, so a dashboard never labels a magnetic heading as
	// true north.
	HasDeclination bool `json:"has_declination"`
	HasMagnetic    bool `json:"has_magnetic"`
	HasTrue        bool `json:"has_true"`
	// Cardinal is the sixteen-point sector of the heading.
	Cardinal string `json:"cardinal"`
	// PitchDeg and RollDeg are the tilt a level-corrected sensor reports.
	PitchDeg float64 `json:"pitch_deg"`
	RollDeg  float64 `json:"roll_deg"`
	// TimeUTC is when the reading was taken.
	TimeUTC string `json:"time_utc,omitempty"`
	// Heading is the display-ready heading text the operational card prints.
	Heading string `json:"heading,omitempty"`
	// Target is the nearest communications site, which is what the rose's
	// second needle points at. It is absent when the device has no position.
	Target *portalCompassTarget `json:"target,omitempty"`
}

// portalCompassTarget is the nearest site the compass rose vectors toward: how
// far away it is, what true bearing it is on, and the turn that aims an antenna
// at it.
type portalCompassTarget struct {
	// Kind is "beacon" for an active distress beacon and "site" for a
	// communications site.
	Kind string `json:"kind,omitempty"`
	// ID and Name identify the target.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Triage is the distress level of a beacon target, empty for a site.
	Triage string `json:"triage,omitempty"`
	// BearingDeg is the true bearing from the device to the target.
	BearingDeg float64 `json:"bearing_deg"`
	// DistanceKm is the great-circle distance to the target.
	DistanceKm float64 `json:"distance_km"`
	// Frequency is the site's channel, in the compact band-plan form. An
	// emergency beacon has no frequency, because it is found by its position.
	Frequency string `json:"frequency,omitempty"`
	// Steering is the relative turn toward the target, present only when a
	// true heading is known to steer against.
	Steering string `json:"steering,omitempty"`
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
	// Heading is the live compass reading, which the dashboard draws as a
	// compass rose. It is absent on a node with no compass, and it is present
	// even when there is no position fix, because a compass works standing
	// still and a receiver that has not locked does not.
	Heading *portalCompass `json:"heading,omitempty"`
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
	run LocalRunner
	// readHeading reads the live compass heading, nil when the command surface
	// has no compass behind it.
	readHeading func() (CompassHeading, bool)
	// readBeacons reads the active distress beacons, nil when the command
	// surface has no beacon registry behind it.
	readBeacons func() []SOSRecord
	// now is the clock, injected so a test can pin the solar clock.
	now func() time.Time
	// handler is the mux, built once.
	handler http.Handler

	mu  sync.Mutex
	srv *http.Server
	ln  net.Listener
}

// NewPortalServer builds a portal for addr whose command surface is run. It
// binds nothing until Start runs, so a caller can drive Handler directly in a
// test and so a portal told to listen on port zero reports the port it actually
// got through Addr.
//
// run is normally an *Engine, which supplies the offline command answers, the
// live compass heading, and the active distress beacons; a run that implements
// less than that simply leaves the corresponding dashboard panel empty.
func NewPortalServer(addr string, gps *GPSReader, run LocalRunner) *PortalServer {
	return newPortalServer(addr, gps, run)
}

// newPortalServer builds a portal. It binds nothing until Start runs, so a test
// can drive the handler directly.
func newPortalServer(addr string, gps *GPSReader, run LocalRunner) *PortalServer {
	p := &PortalServer{addr: strings.TrimSpace(addr), gps: gps, run: run, now: time.Now}
	if source, ok := run.(headingSource); ok {
		p.readHeading = source.currentHeading
	}
	if source, ok := run.(beaconSource); ok {
		p.readBeacons = source.activeBeacons
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", p.handleDashboard)
	mux.HandleFunc("GET /hotspot-detect.html", p.handleCaptiveProbe)
	mux.HandleFunc("GET /generate_204", p.handleCaptiveProbe)
	mux.HandleFunc("GET /gen_204", p.handleCaptiveProbe)
	mux.HandleFunc("GET /ncsi.txt", p.handleCaptiveProbe)
	mux.HandleFunc("GET /connecttest.txt", p.handleCaptiveProbe)
	mux.HandleFunc("GET /api/whereami", p.handleWhereAmI)
	mux.HandleFunc("GET /api/compass", p.handleCompass)
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

// handleCompass serves the live compass heading as JSON, with the nearest
// communications site the rose vectors toward. It is the endpoint a widget or a
// second screen polls: the dashboard itself reads the same object out of
// /api/whereami, so there is only ever one heading on the device.
func (p *PortalServer) handleCompass(w http.ResponseWriter, r *http.Request) {
	writePortalJSON(w, http.StatusOK, p.currentCompass())
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

// currentWhereAmI synthesizes the dashboard's answer from the live fix and the
// live compass. The heading is attached before the fix is considered, because a
// compass answers while the device is standing still: a receiver that has not
// locked yet must not hide the one instrument that already knows which way the
// operator is facing.
func (p *PortalServer) currentWhereAmI() portalWhereAmI {
	view := portalWhereAmI{}
	heading, hasHeading := p.currentHeading()
	if hasHeading {
		view.Heading = p.compassView(heading)
	} else {
		heading = CompassHeading{}
	}
	if p.gps == nil {
		view.Message = portalNoFixMessage
		return view
	}
	fix := p.gps.LastFix()
	if !fix.Valid {
		view.Message = portalNoFixMessage
		return view
	}
	built, err := buildWhereAmIHeading(fix, fix.Position(), whereamiSourceGNSS, p.now(), heading)
	if err != nil {
		logf("portal: %v", err)
		return portalWhereAmI{Valid: false, Message: "the geodetic engine could not place the fix: " + err.Error()}
	}
	view.Valid = true
	view.Lat = built.Point.Lat
	view.Lng = built.Point.Lng
	view.PlusCode = built.PlusCode
	view.PlusCode11 = built.PlusCode11
	view.Area = fmt.Sprintf("~%vm x %vm", math.Round(built.AreaLatMeters), math.Round(built.AreaLngMeters))
	view.Maidenhead = built.Maidenhead
	view.Coordinates = formatCardCoordinates(built.Point)
	view.AltitudeM = math.Round(built.Fix.AltitudeM)
	view.AltitudeFt = math.Round(built.Fix.AltitudeM * feetPerMeter)
	view.HasAltitude = built.Fix.HasAltitude
	view.SpeedKnots = built.Fix.SpeedKnots
	view.CourseDeg = built.Fix.CourseDeg
	view.Satellites = built.Fix.Satellites
	view.HDOP = built.Fix.HDOP
	view.FixQuality = built.Fix.FixQuality
	view.FixStatus = built.FixStatus
	view.GCJ02 = built.GCJ02
	view.LocalSolarTime = fmt.Sprintf("%v %v", built.Now.In(built.Zone).Format("15:04"), built.ZoneLabel)
	view.SolarNoon = built.Almanac.Noon.In(built.Zone).Format("15:04")
	view.Sunset = whereamiSunsetText(built)
	if !built.Fix.TimeUTC.IsZero() {
		view.TimeUTC = built.Fix.TimeUTC.UTC().Format(time.RFC3339)
	}
	if built.Almanac.Sunset.After(built.Now) {
		view.SunsetCountdown = formatDaylightRemaining(built.Almanac.Sunset.Sub(built.Now))
	}
	view.Lines = renderWhereAmICard(built)
	return view
}

// currentHeading returns the live compass reading and whether there is one.
func (p *PortalServer) currentHeading() (CompassHeading, bool) {
	if p == nil || p.readHeading == nil {
		return CompassHeading{}, false
	}
	return p.readHeading()
}

// currentCompass is the standalone compass answer: the live heading together
// with the target the rose points at, which is an active distress beacon when
// there is one and the nearest communications site otherwise.
func (p *PortalServer) currentCompass() portalCompass {
	heading, ok := p.currentHeading()
	if !ok {
		return portalCompass{Valid: false, Message: portalNoCompassMessage}
	}
	return *p.compassView(heading)
}

// compassView renders one heading as the dashboard's JSON object, resolving the
// target the rose vectors toward when the device knows where it is.
func (p *PortalServer) compassView(heading CompassHeading) *portalCompass {
	view := &portalCompass{
		Valid:          true,
		MagneticDeg:    heading.MagneticDeg,
		TrueDeg:        heading.TrueDeg,
		DeclinationDeg: heading.DeclinationDeg,
		HasDeclination: heading.HasDeclination,
		HasMagnetic:    heading.HasMagnetic,
		HasTrue:        heading.HasTrue,
		Cardinal:       heading.Cardinal,
		PitchDeg:       heading.PitchDeg,
		RollDeg:        heading.RollDeg,
		Heading:        whereamiHeadingText(heading),
	}
	if !heading.TimeUTC.IsZero() {
		view.TimeUTC = heading.TimeUTC.UTC().Format(time.RFC3339)
	}
	view.Target = p.nearestSite(heading)
	return view
}

// headingOrZero turns a rendered compass view back into the heading the card
// renders, so the JSON and the card's own lines can never disagree.
func headingOrZero(view *portalCompass) CompassHeading {
	if view == nil {
		return CompassHeading{}
	}
	return CompassHeading{
		Valid:          view.Valid,
		MagneticDeg:    view.MagneticDeg,
		TrueDeg:        view.TrueDeg,
		DeclinationDeg: view.DeclinationDeg,
		HasDeclination: view.HasDeclination,
		HasMagnetic:    view.HasMagnetic,
		HasTrue:        view.HasTrue,
		Cardinal:       view.Cardinal,
		PitchDeg:       view.PitchDeg,
		RollDeg:        view.RollDeg,
	}
}

// nearestSite returns the target the compass rose vectors toward: an active
// distress beacon whenever one is registered, which outranks every routine
// site, and otherwise the closest communications site to the device. It is nil
// when the device has no position to measure from.
func (p *PortalServer) nearestSite(heading CompassHeading) *portalCompassTarget {
	if p.gps == nil {
		return nil
	}
	fix := p.gps.LastFix()
	if !fix.Valid {
		return nil
	}
	if beacon := p.nearestBeacon(fix.Position(), heading); beacon != nil {
		return beacon
	}
	entries := catalogFrom(towerRecords, TowerRecord.catalogEntry)
	near := nearestCatalog(entries, fix.Position(), 1)
	if len(near) == 0 {
		return nil
	}
	record, ok := towerRecordByID(towerRecords, near[0].Entry.ID)
	if !ok {
		return nil
	}
	target := &portalCompassTarget{
		Kind:       portalTargetSite,
		ID:         record.ID,
		Name:       record.Name,
		BearingDeg: normalizeDegrees(near[0].Bearing),
		DistanceKm: near[0].Meters / 1000,
		Frequency:  towerFrequencySegment(record),
	}
	if heading.HasTrue {
		target.Steering = FormatSteeringInstruction(near[0].Bearing, heading.TrueDeg)
	}
	return target
}

// nearestBeacon returns the closest active distress beacon to a position, or
// nil when no beacon is registered. A beacon with no position — one that was
// raised from free text with no fix — cannot be steered toward and is skipped.
func (p *PortalServer) nearestBeacon(from LatLng, heading CompassHeading) *portalCompassTarget {
	if p.readBeacons == nil {
		return nil
	}
	var best *portalCompassTarget
	var bestMeters float64
	for _, beacon := range p.readBeacons() {
		if beacon.LatLng.Lat == 0 && beacon.LatLng.Lng == 0 {
			continue
		}
		meters := HaversineDistance(from, beacon.LatLng)
		if best != nil && meters >= bestMeters {
			continue
		}
		bearing := InitialBearing(from, beacon.LatLng)
		target := &portalCompassTarget{
			Kind:       portalTargetBeacon,
			ID:         fmt.Sprintf("SOS #%v", beacon.ID),
			Name:       "SOS " + beacon.Triage + " beacon",
			Triage:     beacon.Triage,
			BearingDeg: normalizeDegrees(bearing),
			DistanceKm: meters / 1000,
		}
		if heading.HasTrue {
			target.Steering = FormatSteeringInstruction(bearing, heading.TrueDeg)
		}
		best, bestMeters = target, meters
	}
	return best
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
	compass := view.Heading
	return strings.NewReplacer(
		"@@PLUS_CODE@@", html.EscapeString(plusCode),
		"@@COORDINATES@@", html.EscapeString(orDash(view.Coordinates)),
		"@@MAIDENHEAD@@", html.EscapeString(orDash(view.Maidenhead)),
		"@@ELEVATION@@", html.EscapeString(orDash(portalElevation(view))),
		"@@FIX_STATUS@@", html.EscapeString(fixStatus),
		"@@SUNSET@@", html.EscapeString(orDash(view.Sunset)),
		"@@SOLAR_CLOCK@@", html.EscapeString(orDash(view.LocalSolarTime)),
		"@@COMPASS_HEADING@@", html.EscapeString(orDash(portalCompassHeading(compass))),
		"@@COMPASS_TRUE@@", html.EscapeString(portalCompassFrame(compass, true)),
		"@@COMPASS_MAG@@", html.EscapeString(portalCompassFrame(compass, false)),
		"@@COMPASS_TARGET@@", html.EscapeString(portalCompassTargetText(compass)),
		"@@HEADING_ROTATION@@", portalCompassRotation(headingOrZero(compass)),
		"@@TARGET_ROTATION@@", portalTargetRotation(compass),
		"@@INITIAL_JSON@@", string(data),
	).Replace(portalDashboardHTML)
}

// portalCompassHeading renders the big compass readout: the heading and the
// sector it names, or a dash when there is no compass to read.
func portalCompassHeading(compass *portalCompass) string {
	if compass == nil || !compass.Valid {
		return ""
	}
	return fmt.Sprintf("%03.0f° %v", compassHeadingAngle(compass), compass.Cardinal)
}

// portalCompassFrame renders one of the two digital readouts under the dial.
// The true frame is shown only when it is known, so a magnetic heading is never
// labelled true north.
func portalCompassFrame(compass *portalCompass, wantTrue bool) string {
	if compass == nil || !compass.Valid {
		return ""
	}
	if wantTrue {
		if !compass.HasTrue {
			return ""
		}
		if compass.HasDeclination {
			return fmt.Sprintf("True %03.0f° (var %+.1f° %v)",
				compass.TrueDeg, compass.DeclinationDeg, variationHemisphere(compass.DeclinationDeg))
		}
		return fmt.Sprintf("True %03.0f°", compass.TrueDeg)
	}
	if !compass.HasMagnetic {
		return ""
	}
	return fmt.Sprintf("Mag %03.0f°", compass.MagneticDeg)
}

// portalCompassTargetText renders the vector line under the dial: the nearest
// distress beacon or communications site, how far away it is, and the turn that
// aims at it. A node with no compass says so here rather than leaving the panel
// blank, because an empty dial looks like a fault.
func portalCompassTargetText(compass *portalCompass) string {
	if compass == nil || !compass.Valid {
		return portalNoCompassMessage
	}
	if compass.Target == nil {
		return "no beacon or site within range of the catalog"
	}
	target := compass.Target
	text := fmt.Sprintf("%v · %.1f km at %03.0f°",
		target.Name, target.DistanceKm, target.BearingDeg)
	if target.Frequency != "" {
		text += " · " + target.Frequency
	}
	if target.Steering != "" {
		text += " " + target.Steering
	}
	return text
}

// portalCompassRotation renders the dial rotation of the heading needle in
// degrees. A north-up rose draws the device's heading as a needle pointing at
// that true bearing, so the number on the dial and the bearing on the target
// needle are read in the same frame.
func portalCompassRotation(heading CompassHeading) string {
	if !heading.Valid {
		return "0"
	}
	return fmt.Sprintf("%.1f", normalizeDegrees(headingReference(heading)))
}

// portalTargetRotation renders the dial rotation of the target needle.
func portalTargetRotation(compass *portalCompass) string {
	if compass == nil || compass.Target == nil {
		return "0"
	}
	return fmt.Sprintf("%.1f", normalizeDegrees(compass.Target.BearingDeg))
}

// compassHeadingAngle returns the angle the big readout shows: the true heading
// when one is known, and the magnetic heading otherwise.
func compassHeadingAngle(compass *portalCompass) float64 {
	return headingReference(headingOrZero(compass))
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
