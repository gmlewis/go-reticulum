// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"context"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// The card's reference position and the codes it really produces. The Phase 0
// brief pairs these coordinates with Google's own 849VCWC8+R9, but that code
// belongs to the Googleplex at 37.422,-122.0841; the code these coordinates
// encode to is 849VQG4W+4W, which is what the in-tree encoder returns (and it
// is pinned to the Googleplex vector in olc_test.go, so the encoder itself is
// not in question). The Maidenhead square and the coordinate formatting do
// match the brief.
const (
	refLat      = 37.755321
	refLng      = -122.452719
	refPlus10   = "849VQG4W+4W"
	refPlus11   = "849VQG4W+4WC"
	refGrid     = "CM87ss"
	refAltitude = 142.4
)

// whereamiTestNow is the fixed clock every card test is answered against: the
// instant the reference fix carries, which is 12:45 local solar at the reference
// position and therefore hours inside the daylight the countdown describes. A
// card asked against the wall clock instead asserts "daylight remaining" by day
// and fails every night once the sun is down — which is exactly how a build that
// ran at 18:22 local solar failed a card whose answer was correct, and how it
// would have failed again every evening after that.
var whereamiTestNow = time.Date(2026, time.September, 16, 20, 45, 33, 0, time.UTC)

// sfFix is the reference GNSS fix the whereami tests build on.
func sfFix() GPSFix {
	return GPSFix{
		Valid:       true,
		Lat:         refLat,
		Lng:         refLng,
		AltitudeM:   refAltitude,
		HasAltitude: true,
		Satellites:  9,
		HDOP:        0.8,
		FixQuality:  1,
		TimeUTC:     whereamiTestNow,
	}
}

// whereamiFixture builds a registry whose GNSS source holds fix. A zero fix
// leaves the registry with no receiver at all, which is the headless case.
func whereamiFixture(t *testing.T, fix GPSFix) (*registry, *hubSession) {
	t.Helper()
	reg, session, _ := commandFixture(t, nil)
	if fix.Valid {
		reader := NewGPSReader(nil)
		reader.SetFix(fix)
		t.Cleanup(func() { _ = reader.Close() })
		reg.gps = reader
	}
	return reg, session
}

// whereami runs the command against the fixture session and returns its lines.
// It answers on the reference clock rather than the wall clock, so the card it
// renders is the same card whatever time of day the build runs at.
func whereami(t *testing.T, reg *registry, session *hubSession, args string) []string {
	t.Helper()
	line := "whereami"
	if strings.TrimSpace(args) != "" {
		line += " " + args
	}
	return runLinesAt(t, reg, session, line, whereamiTestNow)
}

// TestWhereAmICard asserts the operational card's content at the reference
// position: the Plus Code, the coordinates, the grid square, the elevation, the
// fix status, the solar clock, and the sunset countdown.
func TestWhereAmICard(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, sfFix())
	lines := whereami(t, reg, session, "")
	card := strings.Join(lines, "\n")
	for _, want := range []string{
		"/whereami",
		refPlus10,
		refGrid,
		"37.75532° N",
		"122.45272° W",
		"142 m (467 ft) MSL",
		"3D Fix (9 satellites, HDOP 0.8)",
		"UTC-8",
		"Solar noon:",
		"Sunset at",
		"daylight remaining",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card does not contain %q:\n%v", want, card)
		}
	}
	if !strings.HasPrefix(lines[0], "/whereami") {
		t.Errorf("the card's first line = %q, want the command header", lines[0])
	}
	if !strings.Contains(lines[1], "---") {
		t.Errorf("the card's second line = %q, want a rule", lines[1])
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "---") {
		t.Errorf("the card's last line = %q, want a rule", last)
	}
}

// TestWhereAmIFormats pins the exact rendered lines of the card at a fixed
// clock, so a wording change is a visible failure rather than a quiet drift in
// what an operator reads before deciding to walk out of a canyon.
func TestWhereAmIFormats(t *testing.T) {
	t.Parallel()

	point := LatLng{Lat: refLat, Lng: refLng}
	now := whereamiTestNow
	built, err := buildWhereAmI(sfFix(), point, whereamiSourceGNSS, now)
	if err != nil {
		t.Fatalf("buildWhereAmI: %v", err)
	}
	lines := renderWhereAmICard(built)
	if len(lines) != 10 {
		t.Fatalf("card has %v lines, want 10:\n%v", len(lines), strings.Join(lines, "\n"))
	}
	want := []string{
		"/whereami",
		"------------------------------------------------------------",
		"Plus Code (OLC)   : " + refPlus10 + " (Area: ~14m x 14m)",
		"Coordinates       : 37.75532° N, 122.45272° W",
		"Maidenhead Grid   : " + refGrid + " (Amateur Radio QTH)",
		"Elevation         : 142 m (467 ft) MSL",
		"GNSS Fix Status   : 3D Fix (9 satellites, HDOP 0.8)",
		"Local Solar Time  : 12:45 UTC-8 (Solar noon: " + built.Almanac.Noon.In(built.Zone).Format("15:04") + ")",
		"Sunset Countdown  : Sunset at " + built.Almanac.Sunset.In(built.Zone).Format("15:04") +
			" (" + formatDaylightRemaining(built.Almanac.Sunset.Sub(now)) + " daylight remaining)",
		"------------------------------------------------------------",
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("card line %v = %q,\nwant %q", i, lines[i], want[i])
		}
	}
	// The elevation is the conversion of 142.4 m, which rounds to 467 ft; the
	// brief's example 466 ft is the conversion of exactly 142.0 m.
	if got := int(math.Round(refAltitude * feetPerMeter)); got != 467 {
		t.Errorf("elevation conversion = %v ft, want 467", got)
	}
}

// TestWhereAmIHighPrecisionCode asserts the eleven-character code is computed
// alongside the ten-character one the card prints.
func TestWhereAmIHighPrecisionCode(t *testing.T) {
	t.Parallel()

	built, err := buildWhereAmI(sfFix(), LatLng{Lat: refLat, Lng: refLng}, whereamiSourceGNSS, time.Now())
	if err != nil {
		t.Fatalf("buildWhereAmI: %v", err)
	}
	if built.PlusCode != refPlus10 {
		t.Errorf("PlusCode = %v, want %v", built.PlusCode, refPlus10)
	}
	if built.PlusCode11 != refPlus11 {
		t.Errorf("PlusCode11 = %v, want %v", built.PlusCode11, refPlus11)
	}
	if len(built.PlusCode11) != 12 {
		t.Errorf("the eleven-character code is %v characters long, want 12 with the separator",
			len(built.PlusCode11))
	}
	if built.AreaLatMeters < 13 || built.AreaLatMeters > 15 {
		t.Errorf("the ten-character area is %v m across in latitude, want about 14",
			built.AreaLatMeters)
	}
}

// TestWhereAmIChinaAddsGCJ02 asserts a position inside China carries the datum
// a domestic map app accepts, and a position outside it does not.
func TestWhereAmIChinaAddsGCJ02(t *testing.T) {
	t.Parallel()

	beijing := GPSFix{Valid: true, Lat: 39.9042, Lng: 116.4074, AltitudeM: 44, HasAltitude: true,
		Satellites: 12, HDOP: 0.9, FixQuality: 1,
		TimeUTC: time.Date(2026, time.September, 16, 20, 45, 33, 0, time.UTC)}
	reg, session := whereamiFixture(t, beijing)
	card := strings.Join(whereami(t, reg, session, ""), "\n")
	for _, want := range []string{"GCJ-02", "8PFRWC34+MX", "OM89ev"} {
		if !strings.Contains(card, want) {
			t.Errorf("a Beijing fix must carry %q:\n%v", want, card)
		}
	}

	reg, session = whereamiFixture(t, sfFix())
	if card := strings.Join(whereami(t, reg, session, ""), "\n"); strings.Contains(card, "GCJ-02") {
		t.Errorf("a San Francisco fix must not carry GCJ-02:\n%v", card)
	}
}

// TestWhereAmIManualLocation asserts an explicit argument is placed exactly as
// the navigation commands place it, and says so rather than pretending a
// receiver supplied the altitude. A grid locator names a square, not a point,
// so its card is the square's own center and is checked against the parser
// rather than against the reference fix's code.
func TestWhereAmIManualLocation(t *testing.T) {
	t.Parallel()

	cases := []struct{ args, plus string }{
		{"37.7553,-122.4527", refPlus10},
		{refPlus10, refPlus10},
		{refGrid, "849VQGCR+8M"},
	}
	for _, tc := range cases {
		t.Run(tc.args, func(t *testing.T) {
			t.Parallel()
			reg, session := whereamiFixture(t, sfFix())
			card := strings.Join(whereami(t, reg, session, tc.args), "\n")
			if !strings.Contains(card, tc.plus) {
				t.Errorf("whereami %v did not resolve to %v:\n%v", tc.args, tc.plus, card)
			}
			if !strings.Contains(card, whereamiSourceManual) {
				t.Errorf("whereami %v must report a manual position:\n%v", tc.args, card)
			}
			if strings.Contains(card, "3D Fix") {
				t.Errorf("whereami %v must not claim a GNSS fix:\n%v", tc.args, card)
			}
			if !strings.Contains(card, "unknown (no GNSS altitude)") {
				t.Errorf("whereami %v must not invent an altitude:\n%v", tc.args, card)
			}
		})
	}
}

// TestWhereAmIManualLocationRejectsGarbage asserts a mistyped argument answers
// with the notation help instead of a position.
func TestWhereAmIManualLocationRejectsGarbage(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, sfFix())
	lines := whereami(t, reg, session, "not-a-place-at-all")
	if !strings.Contains(strings.Join(lines, "\n"), locationNotationHelp) {
		t.Errorf("whereami with a bad argument = %v, want the notation help", lines)
	}
}

// TestWhereAmIWithoutAFix asserts a node with no receiver says it is acquiring
// one instead of inventing a position.
func TestWhereAmIWithoutAFix(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, GPSFix{})
	lines := whereami(t, reg, session, "")
	if len(lines) != 1 || lines[0] != whereamiNoFixLine {
		t.Fatalf("whereami without a fix = %v, want [%v]", lines, whereamiNoFixLine)
	}
}

// TestWhereAmINoFixStillAnswersManualArgs asserts an argument works even when
// no receiver is configured, because a remote position is a question the
// geodetic engine can answer offline.
func TestWhereAmINoFixStillAnswersManualArgs(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, GPSFix{})
	card := strings.Join(whereami(t, reg, session, "39.7392,-104.9903"), "\n")
	for _, want := range []string{"85FQP2Q5+MV", "DM79mr", whereamiSourceManual} {
		if !strings.Contains(card, want) {
			t.Errorf("manual whereami does not contain %q:\n%v", want, card)
		}
	}
}

// TestWhereAmISlashForm asserts the slash notation the field brief uses reaches
// the same command, so "/whereami" is registered in every way that matters.
func TestWhereAmISlashForm(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, sfFix())
	lines := runLinesAt(t, reg, session, "/whereami", whereamiTestNow)
	if !strings.Contains(strings.Join(lines, "\n"), refPlus10) {
		t.Errorf("/whereami = %v, want the same card as whereami", lines)
	}
}

// TestWhereAmICommandIsLocalAndRegistered asserts the command exists under the
// name the field brief uses and is marked runnable with no hub session, which
// is what the captive portal needs.
func TestWhereAmICommandIsLocalAndRegistered(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	cmd, ok := reg.byName["whereami"]
	if !ok {
		t.Fatal("whereami is not registered")
	}
	if !cmd.local {
		t.Error("whereami is not marked local; the portal could not run it")
	}
	if cmd.usage == "" {
		t.Error("whereami has no usage line")
	}
}

// TestWhereAmIGCJ02LineIsTheAmapDatum asserts the China line carries the
// converted coordinate, not the WGS-84 one, because that is the only form a
// domestic map accepts.
func TestWhereAmIGCJ02LineIsTheAmapDatum(t *testing.T) {
	t.Parallel()

	built, err := buildWhereAmI(GPSFix{Valid: true, Lat: 39.9042, Lng: 116.4074},
		LatLng{Lat: 39.9042, Lng: 116.4074}, whereamiSourceGNSS, time.Now())
	if err != nil {
		t.Fatalf("buildWhereAmI: %v", err)
	}
	gcjLat, gcjLng := WGS84ToGCJ02(39.9042, 116.4074)
	if math.Abs(built.GCJLat-gcjLat) > 1e-9 || math.Abs(built.GCJLng-gcjLng) > 1e-9 {
		t.Errorf("GCJ-02 = %v,%v, want %v,%v", built.GCJLat, built.GCJLng, gcjLat, gcjLng)
	}
	if !strings.Contains(built.GCJ02, "GCJ-02") {
		t.Errorf("GCJ02 rendering = %q, want it to name the datum", built.GCJ02)
	}
}

// TestWhereAmIPolarNightAndMidnightSun asserts the almanac's two edge cases are
// reported rather than rendered as a countdown to a sunset that never comes.
func TestWhereAmIPolarNightAndMidnightSun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		lat  float64
		lng  float64
		when time.Time
		want string
	}{
		{"midnight sun", 78.2232, 15.6469, time.Date(2026, time.June, 21, 12, 0, 0, 0, time.UTC), "midnight sun"},
		{"polar night", 78.2232, 15.6469, time.Date(2026, time.December, 21, 12, 0, 0, 0, time.UTC), "polar night"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fix := GPSFix{Valid: true, Lat: tc.lat, Lng: tc.lng, AltitudeM: 10, HasAltitude: true,
				Satellites: 8, HDOP: 1.0, FixQuality: 1}
			built, err := buildWhereAmI(fix, LatLng{Lat: tc.lat, Lng: tc.lng}, whereamiSourceGNSS, tc.when)
			if err != nil {
				t.Fatalf("buildWhereAmI: %v", err)
			}
			card := strings.Join(renderWhereAmICard(built), "\n")
			if !strings.Contains(card, tc.want) {
				t.Errorf("card does not report %q:\n%v", tc.want, card)
			}
		})
	}
}

// TestWhereAmISolarZoneFollowsLongitude asserts the zone the card prints is the
// one the position's longitude defines, which is the only zone a node with no
// timezone database can honestly claim.
func TestWhereAmISolarZoneFollowsLongitude(t *testing.T) {
	t.Parallel()

	cases := []struct {
		lng  float64
		want string
	}{
		{-122.452719, "UTC-8"},
		{0, "UTC+0"},
		{116.4074, "UTC+8"},
		{-0.1278, "UTC+0"},
		{151.2093, "UTC+10"},
		{179.9, "UTC+12"},
		{-179.9, "UTC-12"},
	}
	for _, tc := range cases {
		_, label := solarZone(tc.lng)
		if label != tc.want {
			t.Errorf("solarZone(%v) label = %v, want %v", tc.lng, label, tc.want)
		}
	}
}

// TestWhereAmIDarkAfterSunset asserts the countdown turns into a plain
// statement once the sun is down, instead of a negative duration.
func TestWhereAmIDarkAfterSunset(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 17, 6, 0, 0, 0, time.UTC) // 22:00 the previous day, local
	built, err := buildWhereAmI(sfFix(), LatLng{Lat: refLat, Lng: refLng}, whereamiSourceGNSS, now)
	if err != nil {
		t.Fatalf("buildWhereAmI: %v", err)
	}
	card := strings.Join(renderWhereAmICard(built), "\n")
	if !strings.Contains(card, "dark") {
		t.Errorf("the card after sunset does not say it is dark:\n%v", card)
	}
	if strings.Contains(card, "daylight remaining") {
		t.Errorf("the card after sunset still counts daylight:\n%v", card)
	}
}

// TestWhereAmIFixQualityNames asserts the fix-quality indicator is reported as
// the word a receiver manual uses, so an RTK survey fix is distinguishable from
// a standalone one. Quality 1 is the plain case the card already states with
// "3D Fix", so it carries no extra word.
func TestWhereAmIFixQualityNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		quality int
		want    string
	}{
		{1, ""},
		{2, "DGPS"},
		{4, "RTK fixed"},
		{5, "RTK float"},
	}
	for _, tc := range cases {
		fix := sfFix()
		fix.FixQuality = tc.quality
		built, err := buildWhereAmI(fix, LatLng{Lat: refLat, Lng: refLng}, whereamiSourceGNSS, time.Now())
		if err != nil {
			t.Fatalf("buildWhereAmI: %v", err)
		}
		if tc.want == "" {
			if want := "3D Fix (9 satellites, HDOP 0.8)"; built.FixStatus != want {
				t.Errorf("FixStatus for quality 1 = %q, want %q", built.FixStatus, want)
			}
			continue
		}
		if !strings.Contains(built.FixStatus, tc.want) {
			t.Errorf("FixStatus for quality %v = %q, want it to name %v", tc.quality, built.FixStatus, tc.want)
		}
	}
}

// TestWhereAmIWithoutAMeasuredAltitude asserts a live fix that never carried an
// altitude is not reported as sea level: a height nobody measured is unknown,
// and saying otherwise would send a rescue party downhill.
func TestWhereAmIWithoutAMeasuredAltitude(t *testing.T) {
	t.Parallel()

	fix := sfFix()
	fix.HasAltitude = false
	fix.AltitudeM = 0
	built, err := buildWhereAmI(fix, LatLng{Lat: refLat, Lng: refLng}, whereamiSourceGNSS, time.Now())
	if err != nil {
		t.Fatalf("buildWhereAmI: %v", err)
	}
	card := strings.Join(renderWhereAmICard(built), "\n")
	if !strings.Contains(card, "unknown (no GNSS altitude)") {
		t.Errorf("card = %v, want the altitude reported as unknown", card)
	}
	if strings.Contains(card, "0 m (0 ft)") {
		t.Errorf("card = %v, want no invented sea-level altitude", card)
	}
}

// The compass sentences the card tests build a heading from. The first is the
// worked example the Phase 0.6 brief prints: 029° magnetic with 13.0° of east
// variation, which is 042° true, and 042° is in the north-east sector.
const (
	cardHDGSentence = "$HCHDG,29.0,,,13.0,E*20"
	cardHDTSentence = "$HCHDT,42.0,T*1F"
)

// whereamiCompassFixture builds a registry whose GNSS source holds fix and
// whose compass holds the heading the sentence produced. It mirrors the way the
// running bot wires the two devices to one registry.
func whereamiCompassFixture(t *testing.T, sentence string) (*registry, *hubSession) {
	t.Helper()
	reg, session := whereamiFixture(t, sfFix())
	reader := NewCompassReader(strings.NewReader(sentence))
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("reading the compass sentence: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	reg.compass = reader
	return reg, session
}

// TestWhereAmIHeadingLineFormats pins the exact heading line the Phase 0.6 brief
// specifies, and the two frames it degrades to when only one is known.
func TestWhereAmIHeadingLineFormats(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		heading CompassHeading
		want    string
	}{
		{
			"the brief's worked example",
			CompassHeading{
				Valid: true, MagneticDeg: 29, TrueDeg: 42, DeclinationDeg: 13,
				HasDeclination: true, HasMagnetic: true, HasTrue: true, Cardinal: "NE",
			},
			"042° True (029° Mag, Var: +13.0° E) · NE",
		},
		{
			"a west variation",
			CompassHeading{
				Valid: true, MagneticDeg: 29, TrueDeg: 16, DeclinationDeg: -13,
				HasDeclination: true, HasMagnetic: true, HasTrue: true, Cardinal: "NNE",
			},
			"016° True (029° Mag, Var: -13.0° W) · NNE",
		},
		{
			"true only",
			CompassHeading{Valid: true, HasTrue: true, TrueDeg: 42, Cardinal: "NE"},
			"042° True · NE",
		},
		{
			"magnetic only",
			CompassHeading{Valid: true, HasMagnetic: true, MagneticDeg: 29, Cardinal: "NNE"},
			"029° Mag · NNE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := whereamiHeadingText(tc.heading); got != tc.want {
				t.Errorf("whereamiHeadingText(%+v) = %q, want %q", tc.heading, got, tc.want)
			}
		})
	}
}

// TestWhereAmICardCarriesTheHeading asserts the operational card prints the
// heading between the elevation and the fix status, exactly where the brief
// puts it, and that the line is the one the briefing specifies.
func TestWhereAmICardCarriesTheHeading(t *testing.T) {
	t.Parallel()

	reg, session := whereamiCompassFixture(t, cardHDGSentence)
	lines := whereami(t, reg, session, "")
	index := slices.IndexFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, "Heading / Course")
	})
	if index < 0 {
		t.Fatalf("the card carries no heading line:\n%v", strings.Join(lines, "\n"))
	}
	want := "Heading / Course  : 042° True (029° Mag, Var: +13.0° E) · NE"
	if lines[index] != want {
		t.Errorf("the heading line = %q, want %q", lines[index], want)
	}
	if !strings.HasPrefix(lines[index-1], "Elevation") || !strings.HasPrefix(lines[index+1], "GNSS Fix Status") {
		t.Errorf("the heading line sits between %q and %q, want the elevation and the fix status",
			lines[index-1], lines[index+1])
	}
}

// TestWhereAmICardOmitsTheHeadingWithoutACompass asserts a node with no compass
// prints exactly the card it always printed: no empty label, no invented
// heading, and the same line count.
func TestWhereAmICardOmitsTheHeadingWithoutACompass(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, sfFix())
	lines := whereami(t, reg, session, "")
	for _, line := range lines {
		if strings.HasPrefix(line, "Heading") {
			t.Errorf("a card with no compass printed %q", line)
		}
	}
	if len(lines) != 10 {
		t.Errorf("the card has %v lines, want the 10 it had before the compass existed:\n%v",
			len(lines), strings.Join(lines, "\n"))
	}
}

// TestWhereAmICardKeepsAMagneticOnlyHeadingHonest asserts a heading that has
// not been corrected is printed as magnetic rather than passed off as true
// north: the two differ by up to twenty degrees, which at fourteen kilometers
// is kilometers of error.
func TestWhereAmICardKeepsAMagneticOnlyHeadingHonest(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, sfFix())
	reader := NewCompassReader(strings.NewReader("$HCHDM,29.0,M*12"))
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("reading the compass sentence: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	// The reader is deliberately not wired to the fix, which is what a node
	// with no position source for the compass looks like.
	reg.compass = reader
	card := strings.Join(whereami(t, reg, session, ""), "\n")
	if !strings.Contains(card, "029° Mag · NNE") {
		t.Errorf("the card = %v, want the uncorrected heading reported as magnetic", card)
	}
	if strings.Contains(card, "° True") {
		t.Errorf("the card = %v, want no invented true heading", card)
	}
}

// TestWhereAmICardLeavesTheHeadingOffAManualPosition asserts a card about
// somewhere the operator is not standing carries no heading, because the
// heading describes the device and the position does not.
func TestWhereAmICardLeavesTheHeadingOffAManualPosition(t *testing.T) {
	t.Parallel()

	reg, session := whereamiCompassFixture(t, cardHDGSentence)
	card := strings.Join(whereami(t, reg, session, "39.7392,-104.9903"), "\n")
	if strings.Contains(card, "Heading / Course") {
		t.Errorf("a manual-position card = %v, want no device heading", card)
	}
}

// TestWhereAmICardCorrectsAMagneticHeadingFromTheFix asserts the integration
// the whole subsystem rests on: a magnetic-only sentence plus the live fix
// yields a true heading, and the card prints it with the variation the World
// Magnetic Model supplies for that position.
func TestWhereAmICardCorrectsAMagneticHeadingFromTheFix(t *testing.T) {
	t.Parallel()

	reg, session := whereamiFixture(t, sfFix())
	reader := NewCompassReader(strings.NewReader("$HCHDM,29.0,M*12"))
	reader.SetLocationSource(reg.currentFix)
	if err := reader.Run(context.Background()); err != nil {
		t.Fatalf("reading the compass sentence: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	reg.compass = reader
	card := strings.Join(whereami(t, reg, session, ""), "\n")
	if !strings.Contains(card, "° True") || !strings.Contains(card, "Var: +") {
		t.Errorf("the card = %v, want a corrected true heading and the variation", card)
	}
	if !strings.Contains(card, "029° Mag") {
		t.Errorf("the card = %v, want the magnetic heading beside the true one", card)
	}
}

// TestWhereAmIHeadingIsLocalAndRegisteredWithTheCard asserts the heading text
// survives the slash form the field brief uses, so "/whereami" prints the same
// card as "whereami".
func TestWhereAmIHeadingIsLocalAndRegisteredWithTheCard(t *testing.T) {
	t.Parallel()

	reg, session := whereamiCompassFixture(t, cardHDTSentence)
	card := strings.Join(runLinesAt(t, reg, session, "/whereami", whereamiTestNow), "\n")
	if !strings.Contains(card, "042° True · NE") {
		t.Errorf("/whereami = %v, want the true heading alone", card)
	}
}
