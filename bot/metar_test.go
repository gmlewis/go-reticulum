// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// metarConfig is a configuration with a METAR provider set.
func metarConfig() *BotConfig {
	cfg := defaultTestConfig()
	cfg.MetarURL = "https://aviationweather.gov/api/data/metar?ids={place}&format=raw"
	return cfg
}

// TestParseMETARDecodesTheStandardReport asserts every group of a real report
// decodes into the fields the answer prints.
func TestParseMETARDecodesTheStandardReport(t *testing.T) {
	t.Parallel()

	raw := "KDEN 152053Z 34012KT 10SM FEW250 18/02 A3012 RMK AO2 SLP123 T01780017"
	got, err := ParseMETAR(raw)
	if err != nil {
		t.Fatalf("ParseMETAR: %v", err)
	}
	if got.Station != "KDEN" {
		t.Errorf("Station = %q, want KDEN", got.Station)
	}
	if got.Time != "152053Z" {
		t.Errorf("Time = %q, want 152053Z", got.Time)
	}
	if got.Wind != "340° 12kt" {
		t.Errorf("Wind = %q, want 340° 12kt", got.Wind)
	}
	if got.Visibility != "10SM" {
		t.Errorf("Visibility = %q, want 10SM", got.Visibility)
	}
	if !got.HasTemperature || got.TemperatureC != 18 || got.DewpointC != 2 {
		t.Errorf("temperature = %v/%v (present %v), want 18/2", got.TemperatureC, got.DewpointC, got.HasTemperature)
	}
	if !got.HasAltimeter || !closeWithin(got.AltimeterInHg, 30.12, 1e-9) || got.AltimeterHPa != 1020 {
		t.Errorf("altimeter = %v inHg / %v hPa, want 30.12 / 1020", got.AltimeterInHg, got.AltimeterHPa)
	}
	if got.Sky != "FEW250" {
		t.Errorf("Sky = %q, want FEW250", got.Sky)
	}
}

// TestMETARLineMatchesTheDocumentedShape asserts the rendered line is exactly
// the documented shape for the documented report.
func TestMETARLineMatchesTheDocumentedShape(t *testing.T) {
	t.Parallel()

	report, err := ParseMETAR("KDEN 152053Z 34012KT 10SM FEW250 18/02 A3012 RMK AO2")
	if err != nil {
		t.Fatalf("ParseMETAR: %v", err)
	}
	want := "KDEN 152053Z: Wind 340° 12kt | Vis 10SM | Temp 18°C (64°F) / DP 2°C | " +
		"Altimeter 30.12 inHg (1020 hPa) | Sky FEW250"
	if got := report.Line(); got != want {
		t.Errorf("Line =\n  %v\nwant\n  %v", got, want)
	}
}

// TestParseMETARHandlesFieldVariants asserts the groups that differ from the
// simplest case — sub-zero temperatures, gusts, variable wind, calm, statute
// and metric visibility, and a Q altimeter — all decode.
func TestParseMETARHandlesFieldVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		raw    string
		wind   string
		vis    string
		tempC  float64
		dewC   float64
		altHg  float64
		altHPa float64
	}{
		{"gusts", "KJFK 152051Z 27018G28KT 10SM SCT050 21/12 A2992", "270° 18kt gusting 28kt", "10SM", 21, 12, 29.92, 1013},
		{"variable wind", "EGLL 152050Z VRB03KT 9999 FEW030 12/09 Q1018", "variable 3kt", "9999", 12, 9, 30.06, 1018},
		{"calm", "KLAX 152053Z 00000KT 6SM BR OVC008 16/14 A3005", "calm", "6SM", 16, 14, 30.05, 1018},
		{"sub zero", "CYYZ 152100Z 32008KT 15SM SKC M05/M10 A3021", "320° 8kt", "15SM", -5, -10, 30.21, 1023},
		{"metric wind", "EGKK 152050Z 20010MPS 9999 BKN020 11/08 Q1015", "200° 10mps", "9999", 11, 8, 29.97, 1015},
		{"fractional visibility", "KSEA 152053Z 18005KT 1/2SM FG VV002 09/09 A3020", "180° 5kt", "1/2SM", 9, 9, 30.20, 1023},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseMETAR(tc.raw)
			if err != nil {
				t.Fatalf("ParseMETAR: %v", err)
			}
			if got.Wind != tc.wind {
				t.Errorf("Wind = %q, want %q", got.Wind, tc.wind)
			}
			if got.Visibility != tc.vis {
				t.Errorf("Visibility = %q, want %q", got.Visibility, tc.vis)
			}
			if got.TemperatureC != tc.tempC || got.DewpointC != tc.dewC {
				t.Errorf("temperature = %v/%v, want %v/%v", got.TemperatureC, got.DewpointC, tc.tempC, tc.dewC)
			}
			if !closeWithin(got.AltimeterInHg, tc.altHg, 0.01) || got.AltimeterHPa != tc.altHPa {
				t.Errorf("altimeter = %v inHg / %v hPa, want %v / %v",
					got.AltimeterInHg, got.AltimeterHPa, tc.altHg, tc.altHPa)
			}
		})
	}
}

// TestParseMETARRejectsUnusableReports asserts a body that carries no decodable
// group is refused, so the command falls back to the raw text instead of
// printing an empty line.
func TestParseMETARRejectsUnusableReports(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "   ", "not a report at all", "<html>error</html>"} {
		if got, err := ParseMETAR(raw); err == nil {
			t.Errorf("ParseMETAR(%q) = %+v, want an error", raw, got)
		}
	}
}

// TestMetarStationAcceptsIcaoCodes asserts a station code is normalized and
// that anything else is refused before it reaches a URL.
func TestMetarStationAcceptsIcaoCodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"kden", "KDEN"},
		{" EGLL ", "EGLL"},
		{"KJFK", "KJFK"},
		{"K1A2", "K1A2"},
	} {
		got, err := metarStationID(tc.in)
		if err != nil {
			t.Errorf("metarStationID(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("metarStationID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"", "K", "KDENX", "KD EN", "KDEN/../etc", "ÜDEN"} {
		if got, err := metarStationID(in); err == nil {
			t.Errorf("metarStationID(%q) = %q, want an error", in, got)
		}
	}
}

// TestMetarCommandAnswersTheDecodedReport asserts the command fetches through
// the injected provider and answers with the decoded line, and that the answer
// is reused while the cache is warm.
func TestMetarCommandAnswersTheDecodedReport(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, metarConfig())
	fetches := 0
	reg.fetch = func(url string) (string, error) {
		fetches++
		if !strings.Contains(url, "kden") {
			t.Errorf("the provider was asked for %q, want the lowercased station", url)
		}
		return "KDEN 152053Z 34012KT 10SM FEW250 18/02 A3012", nil
	}

	lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock)
	if len(lines) != 1 {
		t.Fatalf("metar = %v, want one line", lines)
	}
	if !strings.HasPrefix(lines[0], "KDEN 152053Z: Wind 340° 12kt") {
		t.Errorf("metar = %q, want the decoded report", lines[0])
	}
	runLinesAt(t, reg, session, "metar KDEN", spacewxClock.Add(time.Minute))
	if fetches != 1 {
		t.Errorf("fetches = %v, want the answer reused from the cache", fetches)
	}
}

// TestMetarCommandFallsBackToTheRawReport asserts an undecodable answer is
// still reported, because a raw METAR is better than nothing.
func TestMetarCommandFallsBackToTheRawReport(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, metarConfig())
	reg.fetch = func(url string) (string, error) {
		return "KDEN RMK AO2 SOMETHING UNUSUAL", nil
	}
	lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock)
	if len(lines) != 1 || !strings.Contains(lines[0], "(raw):") {
		t.Fatalf("metar with an undecodable report = %v, want the raw fallback", lines)
	}
}

// TestMetarCommandReportsItsConfiguration asserts the command explains its
// configuration state rather than guessing.
func TestMetarCommandReportsItsConfiguration(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	if lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock); len(lines) != 1 ||
		lines[0] != metarNotConfiguredLine {
		t.Errorf("metar with no provider = %v, want %q", lines, metarNotConfiguredLine)
	}

	reg, session, _ = commandFixture(t, metarConfig())
	if lines := runLinesAt(t, reg, session, "metar", spacewxClock); len(lines) != 2 ||
		!strings.Contains(lines[0], "Usage: "+metarUsage) || lines[1] != metarUsageHint {
		t.Errorf("metar with no station = %v, want the usage line and the discovery hint", lines)
	}
	if lines := runLinesAt(t, reg, session, "metar KDENX", spacewxClock); len(lines) != 2 ||
		lines[0] != metarPlaceRejectedLine || lines[1] != metarUsageHint {
		t.Errorf("metar with a bad station = %v, want the station line and the hint", lines)
	}

	reg.fetch = func(url string) (string, error) { return "", errors.New("no route to host") }
	if lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock); len(lines) != 1 ||
		lines[0] != metarFailedLine {
		t.Errorf("metar with a failing provider = %v, want %q", lines, metarFailedLine)
	}
}

// TestMetarSearchFindsAnAirfieldByCityNameAndCode asserts the three things an
// asker can type without knowing an ICAO code: the city, the field's name, and
// the passenger code they read off a ticket.
func TestMetarSearchFindsAnAirfieldByCityNameAndCode(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)

	byCity := runLines(t, reg, session, "metar search denver")
	if len(byCity) < 3 || !strings.Contains(byCity[0], `Airports matching "denver" (Page 1 of `) {
		t.Fatalf("metar search denver = %v, want a paged answer", byCity)
	}
	if !strings.Contains(byCity[1], "  KDEN: Denver International (CO)") {
		t.Errorf("first match = %q, want Denver International first", byCity[1])
	}
	if !strings.Contains(byCity[2], "KBJC") {
		t.Errorf("second match = %q, want the Denver satellite fields next", byCity[2])
	}

	byCode := runLines(t, reg, session, "metar search EGLL")
	if len(byCode) < 2 || !strings.Contains(byCode[1], "  EGLL: London Heathrow (GB)") {
		t.Errorf("metar search EGLL = %v, want Heathrow first", byCode)
	}

	byIATA := runLines(t, reg, session, "metar search LHR")
	if !strings.Contains(strings.Join(byIATA, "\n"), "EGLL") {
		t.Errorf("metar search LHR = %v, want the IATA code to reach its field", byIATA)
	}

	byName := runLines(t, reg, session, "metar search heathrow")
	if !strings.Contains(strings.Join(byName, "\n"), "EGLL") {
		t.Errorf("metar search heathrow = %v, want the field by name", byName)
	}

	if got := runLines(t, reg, session, "metar search zzyzx"); len(got) != 2 ||
		!strings.Contains(got[0], `No Airports match "zzyzx"`) {
		t.Errorf("metar search zzyzx = %v, want a no-match line and a hint", got)
	}
	if got := runLines(t, reg, session, "metar search"); len(got) == 0 ||
		!strings.Contains(got[0], "Usage: metar search") {
		t.Errorf("metar search with no words = %v, want the usage line", got)
	}
}

// TestMetarListFiltersByStateAndCountry asserts the list form takes a state code
// or name inside the states and a country code elsewhere, and that the
// unfiltered form lists everything.
func TestMetarListFiltersByStateAndCountry(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	byState := runLines(t, reg, session, "metar list CO")
	if len(byState) == 0 || !strings.Contains(byState[0], "Airports in CO (Page 1 of ") {
		t.Fatalf("metar list CO = %v, want the Coloradan airports paged", byState)
	}
	for _, line := range byState[1 : len(byState)-1] {
		if !strings.Contains(line, "(CO)") {
			t.Errorf("metar list CO listed an airport outside Colorado: %q", line)
		}
	}
	if !strings.Contains(strings.Join(byState, "\n"), "KDEN") {
		t.Errorf("metar list CO = %v, want Denver International on the first page", byState)
	}

	byName := runLines(t, reg, session, "metar list colorado")
	if len(byName) == 0 || !strings.Contains(byName[0], "Airports in COLORADO (Page 1 of ") || byName[1] != byState[1] {
		t.Errorf("metar list colorado = %v, want the same first page as metar list CO", byName)
	}

	byCountry := runLines(t, reg, session, "metar list GB")
	if len(byCountry) == 0 || !strings.Contains(byCountry[0], "Airports in GB (Page 1 of ") ||
		!strings.Contains(strings.Join(byCountry, "\n"), "EGLL") {
		t.Errorf("metar list GB = %v, want Heathrow among the British fields", byCountry)
	}

	// A country code that is also a state code must not leak its country into
	// the state's list, and the country must still be reachable by name.
	if joined := strings.Join(byState, "\n"); strings.Contains(joined, "El Dorado") || strings.Contains(joined, "(Colombia)") {
		t.Errorf("metar list CO listed Colombia's fields:\n%v", joined)
	}
	byNation := runLines(t, reg, session, "metar list colombia")
	if len(byNation) == 0 || !strings.Contains(byNation[0], "Airports in COLOMBIA (Page 1 of ") ||
		!strings.Contains(strings.Join(byNation, "\n"), "SKBO") {
		t.Errorf("metar list colombia = %v, want Bogota", byNation)
	}

	if got := runLines(t, reg, session, "metar list ZZ"); len(got) != 2 ||
		!strings.Contains(got[0], "No Airports in ZZ") {
		t.Errorf("metar list ZZ = %v, want a no-region line and a hint", got)
	}
}

// TestMetarNearNamesTheClosestAirfields asserts a position resolves to the
// fields around it, and that an ICAO code resolves to the field's own runway.
func TestMetarNearNamesTheClosestAirfields(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "metar near 39.86,-104.67")
	if len(lines) != 5 {
		t.Fatalf("metar near 39.86,-104.67 = %v, want a header, three fields, and a footer", lines)
	}
	if !strings.Contains(lines[0], "Airports near 39.86,-104.67 (Page 1 of 1):") {
		t.Errorf("header = %q, want the position that was asked for", lines[0])
	}
	if !strings.Contains(lines[1], "KDEN (") || !strings.Contains(lines[1], " nmi ") ||
		!strings.Contains(lines[1], "): Denver International (CO)") {
		t.Errorf("nearest field = %q, want a distance, a bearing, and the name", lines[1])
	}

	byCode := runLines(t, reg, session, "metar near KDEN")
	if len(byCode) < 2 || !strings.Contains(byCode[1], "KDEN (0.0 nmi ") {
		t.Errorf("metar near KDEN = %v, want the field itself at zero distance", byCode)
	}

	if got := runLines(t, reg, session, "metar near nowhere at all"); len(got) != 2 ||
		!strings.Contains(got[0], "not a place, a coordinate, or a plus code") {
		t.Errorf("metar near nowhere = %v, want an explanation and a hint", got)
	}
}

// TestMetarDiscoveryNeedsNoProvider asserts finding an airfield is an offline
// question: it answers on a bot with no metar_url at all.
func TestMetarDiscoveryNeedsNoProvider(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	reg.fetch = func(url string) (string, error) {
		t.Errorf("discovery reached the network: %v", url)
		return "", nil
	}
	for _, line := range []string{"metar search denver", "metar list CO", "metar near KDEN"} {
		if lines := runLinesAt(t, reg, session, line, spacewxClock); len(lines) < 2 {
			t.Errorf("%v = %v, want an offline answer", line, lines)
		}
	}
	if lines := runLinesAt(t, reg, session, "metar KDEN", spacewxClock); len(lines) != 1 ||
		lines[0] != metarNotConfiguredLine {
		t.Errorf("metar KDEN = %v, want %q", lines, metarNotConfiguredLine)
	}
}

// TestMetarStationTableIsCoherent asserts the reference catalog has no duplicate
// codes, no empty names, real positions, and the coverage it claims.
func TestMetarStationTableIsCoherent(t *testing.T) {
	t.Parallel()

	if len(metarStations) < 200 {
		t.Fatalf("the reference table holds %v airfields, want at least 200", len(metarStations))
	}
	seen := map[string]bool{}
	regions := map[string]bool{}
	for _, station := range metarStations {
		if seen[station.ICAO] {
			t.Errorf("airfield %v appears twice", station.ICAO)
		}
		seen[station.ICAO] = true
		if len(station.ICAO) != 4 {
			t.Errorf("airfield code %q is not four characters", station.ICAO)
		}
		if station.Name == "" || station.City == "" || station.Country == "" {
			t.Errorf("airfield %v is missing a name, a city, or a country: %+v", station.ICAO, station)
		}
		if station.State != "" && usStateNames[station.State] == "" {
			t.Errorf("airfield %v has state %q, which is not a state or territory", station.ICAO, station.State)
		}
		if math.Abs(station.Lat) > 90 || math.Abs(station.Lng) > 180 {
			t.Errorf("airfield %v is at an impossible position: %v, %v", station.ICAO, station.Lat, station.Lng)
		}
		regions[station.region()] = true
	}
	for _, want := range []string{"CO", "CA", "TX", "NY", "GB", "FR", "Germany", "JP", "AU", "Colombia"} {
		if !regions[want] {
			t.Errorf("the reference table has no airfield in %v", want)
		}
	}
}
