// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the geodesy engine: everything that turns a position a human
// typed into a coordinate, and a coordinate back into the forms a human or a
// radio operator can use. It implements the five notations a field operator is
// likely to be handed — a Plus Code, decimal degrees, degrees/minutes/seconds,
// degrees and decimal minutes, and a Maidenhead grid locator — plus the
// great-circle distance, bearing, and dead-reckoning projection the navigation
// commands are built on.
//
// Everything here is closed-form and pure: no map data, no projection library,
// no network. A sphere of radius 6,371 km is the model, which is what the
// original field references use and is accurate to a few tenths of a percent
// against the ellipsoid — far below the precision of any position a person can
// read off a map or a handheld GPS under a canopy.

package bot

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Earth and angle constants.
const (
	// earthRadiusMeters is the mean radius of the sphere the geodesy functions
	// work on.
	earthRadiusMeters = 6371000.0
	// compassPoints is the sixteen-point compass rose, clockwise from north.
	compassPointCount = 16
	// degreesPerCompassPoint is the width of one compass sector.
	degreesPerCompassPoint = 360.0 / compassPointCount
	// maidenheadFieldLng and maidenheadFieldLat are the field sizes: 20 degrees
	// of longitude by 10 degrees of latitude.
	maidenheadFieldLng = 20.0
	maidenheadFieldLat = 10.0
	// maidenheadSquareLng and maidenheadSquareLat are the square sizes within a
	// field: 2 degrees of longitude by 1 degree of latitude.
	maidenheadSquareLng = 2.0
	maidenheadSquareLat = 1.0
	// maidenheadSubdivisions splits a square into 24 subsquares in each
	// direction, so a subsquare is 5 minutes of longitude by 2.5 of latitude.
	maidenheadSubdivisions = 24.0
)

// ErrLocationUnrecognized reports text that is not a location in any notation
// this engine accepts.
var ErrLocationUnrecognized = errors.New("unrecognized location")

// CompassPoints is the sixteen-point compass rose, in clockwise order from
// north. The names are the standard three-letter abbreviations.
var CompassPoints = []string{
	"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE",
	"S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW",
}

// ParseLocation turns a location a human typed into a coordinate. It accepts a
// full Plus Code, decimal degrees with or without hemisphere letters, degrees
// and minutes and seconds, degrees and decimal minutes, and a Maidenhead grid
// locator, in any mix of upper and lower case. A leading '@' (such as copied
// out of a Google Maps URL) is stripped. A form that cannot be placed
// without more information — a short Plus Code above all — is refused rather
// than guessed, because a wrong position is worse than no position.
//
// A "gcj:" (or "gcj02:") prefix marks the value as a Mars coordinate copied out
// of a Chinese map app; the result is the GPS position behind it. Every other
// notation is WGS-84 and is returned as given.
func ParseLocation(input string) (LatLng, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return LatLng{}, fmt.Errorf("%w: empty", ErrLocationUnrecognized)
	}
	text = strings.TrimPrefix(text, "@")
	text = strings.TrimSpace(text)
	if text == "" {
		return LatLng{}, fmt.Errorf("%w: empty", ErrLocationUnrecognized)
	}
	if inner, ok := trimGCJPrefix(text); ok {
		gcj, err := ParseLocation(inner)
		if err != nil {
			return LatLng{}, err
		}
		lat, lng := GCJ02ToWGS84(gcj.Lat, gcj.Lng)
		return LatLng{Lat: lat, Lng: lng}, nil
	}
	if IsFullOLC(text) {
		area, err := DecodeOLC(text)
		if err != nil {
			return LatLng{}, err
		}
		return area.Center(), nil
	}
	if looksLikeMaidenhead(text) {
		return MaidenheadToLatLng(text)
	}
	return parseCoordinateText(text)
}

// HaversineDistance returns the great-circle distance between two coordinates
// in meters.
func HaversineDistance(p1, p2 LatLng) float64 {
	phi1 := radians(p1.Lat)
	phi2 := radians(p2.Lat)
	deltaPhi := radians(p2.Lat - p1.Lat)
	deltaLambda := radians(p2.Lng - p1.Lng)
	a := math.Sin(deltaPhi/2)*math.Sin(deltaPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(deltaLambda/2)*math.Sin(deltaLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}

// InitialBearing returns the initial great-circle bearing from p1 to p2 in
// degrees, in the range 0 to 360 exclusive.
func InitialBearing(p1, p2 LatLng) float64 {
	phi1 := radians(p1.Lat)
	phi2 := radians(p2.Lat)
	deltaLambda := radians(p2.Lng - p1.Lng)
	y := math.Sin(deltaLambda) * math.Cos(phi2)
	x := math.Cos(phi1)*math.Sin(phi2) - math.Sin(phi1)*math.Cos(phi2)*math.Cos(deltaLambda)
	return normalizeDegrees(degrees(math.Atan2(y, x)))
}

// CompassPoint names the sixteen-point compass sector a bearing falls in.
func CompassPoint(bearing float64) string {
	index := int(math.Round(normalizeDegrees(bearing)/degreesPerCompassPoint)) % compassPointCount
	if index < 0 {
		index += compassPointCount
	}
	return CompassPoints[index]
}

// ProjectWaypoint returns the coordinate reached by travelling distMeters from
// origin along the great circle that leaves origin on bearingDeg. It is dead
// reckoning: a stated course and distance become a position, which is what
// turns "walked 3 km on 048" into something a searcher can plot.
func ProjectWaypoint(origin LatLng, bearingDeg, distMeters float64) LatLng {
	angular := distMeters / earthRadiusMeters
	theta := radians(bearingDeg)
	phi1 := radians(origin.Lat)
	lambda1 := radians(origin.Lng)

	sinPhi2 := math.Sin(phi1)*math.Cos(angular) + math.Cos(phi1)*math.Sin(angular)*math.Cos(theta)
	phi2 := math.Asin(math.Max(-1, math.Min(1, sinPhi2)))
	lambda2 := lambda1 + math.Atan2(
		math.Sin(theta)*math.Sin(angular)*math.Cos(phi1),
		math.Cos(angular)-math.Sin(phi1)*sinPhi2)
	return LatLng{Lat: degrees(phi2), Lng: normalizeLongitude(degrees(lambda2))}
}

// LatLngToMaidenhead renders a coordinate as a six-character Maidenhead grid
// locator, the form amateur radio operators exchange on the air. Four
// characters identify a 2-by-1 degree square; the last two refine it to a
// 5-by-2.5 minute subsquare.
func LatLngToMaidenhead(lat, lng float64) string {
	lat = math.Min(89.999999, math.Max(-89.999999, lat))
	lng = normalizeLongitude(lng)
	latAdj := lat + olcLatMax
	lngAdj := lng + olcLngMax

	lngField := int(lngAdj / maidenheadFieldLng)
	latField := int(latAdj / maidenheadFieldLat)
	lngSquare := int((lngAdj - float64(lngField)*maidenheadFieldLng) / maidenheadSquareLng)
	latSquare := int((latAdj - float64(latField)*maidenheadFieldLat) / maidenheadSquareLat)
	lngSub := int((lngAdj - float64(lngField)*maidenheadFieldLng - float64(lngSquare)*maidenheadSquareLng) /
		(maidenheadSquareLng / maidenheadSubdivisions))
	latSub := int((latAdj - float64(latField)*maidenheadFieldLat - float64(latSquare)*maidenheadSquareLat) /
		(maidenheadSquareLat / maidenheadSubdivisions))

	return fmt.Sprintf("%c%c%v%v%c%c",
		'A'+rune(lngField), 'A'+rune(latField),
		lngSquare, latSquare,
		'a'+rune(lngSub), 'a'+rune(latSub))
}

// MaidenheadToLatLng decodes a Maidenhead grid locator into the coordinate at
// the center of the square it names. Locators of four, six, or eight characters
// are accepted; the two extra digits of an eight-character locator refine the
// center within the subsquare.
func MaidenheadToLatLng(grid string) (LatLng, error) {
	text := strings.TrimSpace(grid)
	if len(text) != 4 && len(text) != 6 && len(text) != 8 {
		return LatLng{}, fmt.Errorf("%w: %q is not a 4, 6, or 8 character Maidenhead locator",
			ErrLocationUnrecognized, grid)
	}
	upper := strings.ToUpper(text)
	lngField := int(upper[0] - 'A')
	latField := int(upper[1] - 'A')
	if lngField < 0 || lngField > 17 || latField < 0 || latField > 17 {
		return LatLng{}, fmt.Errorf("%w: %q has a field letter outside A-R", ErrLocationUnrecognized, grid)
	}
	lngSquare, err := maidenheadDigit(upper[2])
	if err != nil {
		return LatLng{}, err
	}
	latSquare, err := maidenheadDigit(upper[3])
	if err != nil {
		return LatLng{}, err
	}

	lng := float64(lngField)*maidenheadFieldLng + float64(lngSquare)*maidenheadSquareLng
	lat := float64(latField)*maidenheadFieldLat + float64(latSquare)*maidenheadSquareLat
	lngSize := maidenheadSquareLng
	latSize := maidenheadSquareLat

	if len(upper) >= 6 {
		lngSub := int(upper[4] - 'A')
		latSub := int(upper[5] - 'A')
		if lngSub < 0 || lngSub >= int(maidenheadSubdivisions) || latSub < 0 || latSub >= int(maidenheadSubdivisions) {
			return LatLng{}, fmt.Errorf("%w: %q has a subsquare letter outside A-X", ErrLocationUnrecognized, grid)
		}
		lngSize = maidenheadSquareLng / maidenheadSubdivisions
		latSize = maidenheadSquareLat / maidenheadSubdivisions
		lng += float64(lngSub) * lngSize
		lat += float64(latSub) * latSize
	}
	if len(upper) == 8 {
		lngExt, err := maidenheadDigit(upper[6])
		if err != nil {
			return LatLng{}, err
		}
		latExt, err := maidenheadDigit(upper[7])
		if err != nil {
			return LatLng{}, err
		}
		lngSize /= 10
		latSize /= 10
		lng += float64(lngExt) * lngSize
		lat += float64(latExt) * latSize
	}
	return LatLng{
		Lat: lat - olcLatMax + latSize/2,
		Lng: lng - olcLngMax + lngSize/2,
	}, nil
}

// maidenheadDigit decodes one decimal digit of a Maidenhead locator.
func maidenheadDigit(c byte) (int, error) {
	if c < '0' || c > '9' {
		return 0, fmt.Errorf("%w: %q is not a decimal digit", ErrLocationUnrecognized, string(c))
	}
	return int(c - '0'), nil
}

// looksLikeMaidenhead reports whether text has the shape of a Maidenhead
// locator and nothing else: the two field letters, the two square digits, and
// optionally the two subsquare letters, with no separators or degree symbols.
// The shape check runs before the coordinate parser so a locator is never
// mistaken for a pair of numbers.
func looksLikeMaidenhead(text string) bool {
	runes := []rune(text)
	switch len(runes) {
	case 4, 6, 8:
	default:
		return false
	}
	for i, r := range runes {
		var ok bool
		switch {
		case i < 2:
			ok = inLetterRange(r, 'A', 'R')
		case i < 4:
			ok = r >= '0' && r <= '9'
		case i < 6:
			ok = inLetterRange(r, 'A', 'X')
		default:
			ok = r >= '0' && r <= '9'
		}
		if !ok {
			return false
		}
	}
	return true
}

// inLetterRange reports whether r is a letter between low and high, ignoring
// case.
func inLetterRange(r rune, low, high rune) bool {
	upper := unicode.ToUpper(r)
	return upper >= low && upper <= high
}

// parseCoordinateText parses the numeric coordinate notations. It separates the
// latitude from the longitude and then hands each half to the parser for the
// shape it has.
func parseCoordinateText(text string) (LatLng, error) {
	tokens, err := coordinateTokens(text)
	if err != nil {
		return LatLng{}, err
	}
	if slices.ContainsFunc(tokens, isDirectionLetter) {
		return parseDirectedCoordinates(tokens)
	}
	return parseNumericCoordinates(tokens)
}

// coordinateTokens splits coordinate text into a stream of direction letters
// and numbers, discarding the degree, minute, and second symbols and any
// commas. A letter attached to a number becomes its own token, so "N37.5" and
// "37.5N" both yield a direction and a number.
func coordinateTokens(text string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsDigit(r), r == '.', r == '-', r == '+':
			// A sign only belongs to a number at the start of one; a '+' or '-'
			// in the middle is malformed, and parseFloat refuses it.
			current.WriteRune(r)
		case r == ',', r == '@', r == '°', r == '\'', r == '"', r == '′', r == '″', unicode.IsSpace(r):
			flush()
		case unicode.IsLetter(r):
			flush()
			tokens = append(tokens, string(unicode.ToUpper(r)))
		default:
			return nil, fmt.Errorf("%w: unexpected character %q", ErrLocationUnrecognized, string(r))
		}
	}
	flush()
	if len(tokens) == 0 {
		return nil, fmt.Errorf("%w: no coordinate found", ErrLocationUnrecognized)
	}
	return tokens, nil
}

// isDirectionLetter reports whether a token is one of the four hemisphere
// letters.
func isDirectionLetter(token string) bool {
	switch token {
	case "N", "S", "E", "W":
		return true
	default:
		return false
	}
}

// parseNumericCoordinates parses coordinate text that carries no hemisphere
// letters: a signed decimal pair, or a six-number degrees/minutes/seconds pair.
// The latitude always comes first, and a bare number is a decimal degree.
func parseNumericCoordinates(tokens []string) (LatLng, error) {
	switch len(tokens) {
	case 2:
		lat, err := parseFloatToken(tokens[0])
		if err != nil {
			return LatLng{}, err
		}
		lng, err := parseFloatToken(tokens[1])
		if err != nil {
			return LatLng{}, err
		}
		return validateLatLng(LatLng{Lat: lat, Lng: lng})
	case 6:
		lat, err := parseDMS(tokens[0:3])
		if err != nil {
			return LatLng{}, err
		}
		lng, err := parseDMS(tokens[3:6])
		if err != nil {
			return LatLng{}, err
		}
		return validateLatLng(LatLng{Lat: lat, Lng: lng})
	default:
		return LatLng{}, fmt.Errorf("%w: expected a latitude and longitude, found %v numbers",
			ErrLocationUnrecognized, len(tokens))
	}
}

// parseDirectedCoordinates parses coordinate text whose halves carry hemisphere
// letters. A letter labels the numbers it is adjacent to: with numbers in front
// of it ("37 25 19N") it closes that group, and with none in front ("N37 25 19")
// it labels the numbers that follow. The latitude always comes first, so a group
// without a letter fills latitude and then longitude.
func parseDirectedCoordinates(tokens []string) (LatLng, error) {
	type group struct {
		dir     string
		numbers []string
	}
	var groups []group
	var pending []string
	var leadingDir string
	closePending := func(dir string) {
		if len(pending) == 0 {
			return
		}
		groups = append(groups, group{dir: dir, numbers: pending})
		pending = nil
	}
	for _, tok := range tokens {
		if isDirectionLetter(tok) {
			switch {
			case leadingDir != "":
				// The numbers in hand belong to the letter that led them, and
				// this letter starts the next group.
				closePending(leadingDir)
				leadingDir = tok
			case len(pending) > 0:
				// The letter trails the numbers it labels.
				closePending(tok)
			default:
				leadingDir = tok
			}
			continue
		}
		if _, err := parseFloatToken(tok); err != nil {
			return LatLng{}, err
		}
		pending = append(pending, tok)
	}
	switch {
	case leadingDir != "":
		closePending(leadingDir)
	case len(pending) > 0:
		closePending("")
	}
	if len(groups) != 2 {
		return LatLng{}, fmt.Errorf("%w: expected two hemisphere-labelled coordinates, found %v",
			ErrLocationUnrecognized, len(groups))
	}

	var result LatLng
	var haveLat, haveLng bool
	for _, g := range groups {
		if len(g.numbers) == 0 || len(g.numbers) > 3 {
			return LatLng{}, fmt.Errorf("%w: a coordinate takes one to three numbers, found %v",
				ErrLocationUnrecognized, len(g.numbers))
		}
		value, err := parseDMS(g.numbers)
		if err != nil {
			return LatLng{}, err
		}
		switch g.dir {
		case "N", "S":
			if haveLat {
				return LatLng{}, fmt.Errorf("%w: two latitudes", ErrLocationUnrecognized)
			}
			if g.dir == "S" {
				value = -value
			}
			result.Lat, haveLat = value, true
		case "E", "W":
			if haveLng {
				return LatLng{}, fmt.Errorf("%w: two longitudes", ErrLocationUnrecognized)
			}
			if g.dir == "W" {
				value = -value
			}
			result.Lng, haveLng = value, true
		default:
			// An unlabelled group keeps the latitude-first order of every other
			// notation this engine accepts.
			if !haveLat {
				result.Lat, haveLat = value, true
				continue
			}
			if !haveLng {
				result.Lng, haveLng = value, true
				continue
			}
			return LatLng{}, fmt.Errorf("%w: too many coordinates", ErrLocationUnrecognized)
		}
	}
	return validateLatLng(result)
}

// parseDMS converts one to three numbers — degrees, optionally minutes and
// seconds — into unsigned decimal degrees. The hemisphere letter carries the
// sign, so a negative degree is refused rather than silently combined with a
// positive hemisphere.
func parseDMS(numbers []string) (float64, error) {
	values := make([]float64, len(numbers))
	for i, token := range numbers {
		value, err := parseFloatToken(token)
		if err != nil {
			return 0, err
		}
		values[i] = value
	}
	degrees := values[0]
	if degrees < 0 {
		return 0, fmt.Errorf("%w: a hemisphere letter and a negative degree contradict each other",
			ErrLocationUnrecognized)
	}
	value := degrees
	if len(values) > 1 {
		if values[1] < 0 || values[1] >= 60 {
			return 0, fmt.Errorf("%w: minutes must be between 0 and 60, got %v", ErrLocationUnrecognized, values[1])
		}
		value += values[1] / 60
	}
	if len(values) > 2 {
		if values[2] < 0 || values[2] >= 60 {
			return 0, fmt.Errorf("%w: seconds must be between 0 and 60, got %v", ErrLocationUnrecognized, values[2])
		}
		value += values[2] / 3600
	}
	return value, nil
}

// parseFloatToken parses one numeric token.
func parseFloatToken(token string) (float64, error) {
	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a number", ErrLocationUnrecognized, token)
	}
	return value, nil
}

// validateLatLng rejects a coordinate outside the legal range, so a mistyped
// latitude cannot silently become a position in the wrong hemisphere.
func validateLatLng(p LatLng) (LatLng, error) {
	if p.Lat < -90 || p.Lat > 90 {
		return LatLng{}, fmt.Errorf("%w: latitude %v is outside -90..90", ErrLocationUnrecognized, p.Lat)
	}
	if p.Lng < -180 || p.Lng > 180 {
		return LatLng{}, fmt.Errorf("%w: longitude %v is outside -180..180", ErrLocationUnrecognized, p.Lng)
	}
	return p, nil
}

// ParseDistance parses a distance a human typed, such as "3.5km", "2 mi",
// "1500m", "1.2nm", "500ft", or "300yd". A bare number is meters, which is the
// unit every other value in the bot's answers is computed in.
func ParseDistance(text string) (float64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return 0, fmt.Errorf("distance: empty")
	}
	split := 0
	for split < len(trimmed) && (trimmed[split] == '.' || trimmed[split] == '-' || trimmed[split] == '+' ||
		(trimmed[split] >= '0' && trimmed[split] <= '9')) {
		split++
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(trimmed[:split]), 64)
	if err != nil {
		return 0, fmt.Errorf("distance %q: %w", text, err)
	}
	unit := strings.TrimSpace(trimmed[split:])
	if unit == "" {
		// A bare number is meters, the unit every other value in the bot's
		// answers is computed in.
		unit = "m"
	}
	factor, ok := distanceUnits[unit]
	if !ok {
		return 0, fmt.Errorf("distance %q: unknown unit %q", text, unit)
	}
	if value < 0 {
		return 0, fmt.Errorf("distance %q: must not be negative", text)
	}
	return value * factor, nil
}

// distanceUnits maps a distance unit onto its length in meters.
var distanceUnits = map[string]float64{
	"m":      1,
	"meter":  1,
	"meters": 1,
	"km":     1000,
	"mi":     1609.344,
	"mile":   1609.344,
	"miles":  1609.344,
	"nm":     1852,
	"nmi":    1852,
	"ft":     0.3048,
	"feet":   0.3048,
	"yd":     0.9144,
	"yards":  0.9144,
}

// ParseBearing parses a bearing as either degrees or a compass point name. A
// bare number is degrees; "NE" is the center of that sector.
func ParseBearing(text string) (float64, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, fmt.Errorf("bearing: empty")
	}
	if value, err := strconv.ParseFloat(strings.TrimSuffix(trimmed, "°"), 64); err == nil {
		if value < 0 || value >= 360 {
			return 0, fmt.Errorf("bearing %q: must be between 0 and 360", text)
		}
		return value, nil
	}
	upper := strings.ToUpper(trimmed)
	for i, name := range CompassPoints {
		if name == upper {
			return float64(i) * degreesPerCompassPoint, nil
		}
	}
	return 0, fmt.Errorf("bearing %q: not degrees or a compass point", text)
}

// FormatDistance renders a distance in meters as kilometers, statute miles, and
// nautical miles, which are the three units a field operator is likely to need
// at once. Short distances are reported in meters instead, where a kilometer
// reading would round away the information.
func FormatDistance(meters float64) string {
	if meters < 1000 {
		return fmt.Sprintf("%.0f m (%.0f ft, %.0f yd)", meters, meters/0.3048, meters/0.9144)
	}
	return fmt.Sprintf("%.2f km (%.2f mi, %.2f nm)", meters/1000, meters/1609.344, meters/1852)
}

// FormatBearing renders a bearing as a three-digit course and its compass
// point, the shape a navigator reads off a compass.
func FormatBearing(bearing float64) string {
	normalized := normalizeDegrees(bearing)
	return fmt.Sprintf("%03.0f° %v", normalized, CompassPoint(normalized))
}

// formatDD renders decimal degrees with hemisphere letters, the notation a GPS
// receiver displays.
func formatDD(p LatLng) string {
	return fmt.Sprintf("%.4f°%v, %.4f°%v", math.Abs(p.Lat), hemisphereLetter(p.Lat, "N", "S"),
		math.Abs(p.Lng), hemisphereLetter(p.Lng, "E", "W"))
}

// formatDDM renders degrees and decimal minutes, the notation aviation and
// marine charts use.
func formatDDM(p LatLng) string {
	latDeg, latMin := degreesMinutes(p.Lat)
	lngDeg, lngMin := degreesMinutes(p.Lng)
	return fmt.Sprintf("%v°%05.2f'%v %v°%05.2f'%v",
		latDeg, latMin, hemisphereLetter(p.Lat, "N", "S"),
		lngDeg, lngMin, hemisphereLetter(p.Lng, "E", "W"))
}

// formatDMS renders degrees, minutes, and seconds, the notation a paper map and
// a protractor use.
func formatDMS(p LatLng) string {
	latDeg, latMin, latSec := degreesMinutesSeconds(p.Lat)
	lngDeg, lngMin, lngSec := degreesMinutesSeconds(p.Lng)
	return fmt.Sprintf(`%v°%02d'%02.0f"%v %v°%02d'%02.0f"%v`,
		latDeg, latMin, latSec, hemisphereLetter(p.Lat, "N", "S"),
		lngDeg, lngMin, lngSec, hemisphereLetter(p.Lng, "E", "W"))
}

// degreesMinutes splits a signed degree value into unsigned whole degrees and
// the decimal minutes that remain.
func degreesMinutes(value float64) (int, float64) {
	absolute := math.Abs(value)
	whole := math.Floor(absolute)
	return int(whole), (absolute - whole) * 60
}

// degreesMinutesSeconds splits a signed degree value into unsigned whole
// degrees, whole minutes, and seconds.
func degreesMinutesSeconds(value float64) (int, int, float64) {
	absolute := math.Abs(value)
	whole := math.Floor(absolute)
	minutes := (absolute - whole) * 60
	wholeMinutes := math.Floor(minutes)
	return int(whole), int(wholeMinutes), (minutes - wholeMinutes) * 60
}

// hemisphereLetter picks the hemisphere letter for a signed coordinate.
func hemisphereLetter(value float64, positive, negative string) string {
	if value < 0 {
		return negative
	}
	return positive
}

// FormatLatLng renders a coordinate pair as signed decimal degrees, the
// machine-readable form the bot uses when a coordinate must round-trip exactly.
func FormatLatLng(p LatLng) string {
	return fmt.Sprintf("%.6f, %.6f", p.Lat, p.Lng)
}

// radians converts degrees to radians.
func radians(degrees float64) float64 { return degrees * math.Pi / 180 }

// degrees converts radians to degrees.
func degrees(radians float64) float64 { return radians * 180 / math.Pi }

// normalizeDegrees wraps a degree value into 0..360.
func normalizeDegrees(value float64) float64 {
	value = math.Mod(value, 360)
	if value < 0 {
		value += 360
	}
	return value
}
