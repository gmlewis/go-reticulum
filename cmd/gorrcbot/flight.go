// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the flight command: where one flight is, right now. It is the
// question somebody asks about a person they love who is in the air, so the two
// rules that shape the whole file are "never guess" and "never report a stale
// position as a live one".
//
// Two keyless providers answer it, each through the same operator-supplied URL
// template the weather and launches commands use:
//
//   - the route provider (flight_route_url, adsbdb by default) turns the number
//     a passenger knows into the callsign the radio carries — "BA123" and
//     "BAW123" both resolve to British Airways LHR → DOH — and names the airline
//     and the two airports;
//   - the live provider (flight_url, adsb.lol by default) reports the aircraft
//     transmitting that callsign right now: altitude, climb or descent, ground
//     speed, track, position, squawk, and how long ago that position was heard.
//
// Neither provider needs a key, both answer plain JSON, and both are somebody
// else's data, so every field is stripped and shortened before it is posted, and
// a provider's wording is never repeated into a room.
//
// The two answers are cached separately on purpose. A route is static, so it is
// cached through the shared provider cache. A live state is only useful while it
// is current, so it is cached as the parsed state plus the moment it was fetched,
// and the age the answer reports is the provider's own age plus how long the
// cache held it. A cached answer that went on claiming "heard 0s ago" would be
// exactly the lie this command exists to avoid.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// flightToken is the literal a flight template uses for the requested number.
const flightToken = "{flight}"

// The flight command's limits and wording.
const (
	// flightUsage is the usage line for the flight command.
	flightUsage = "flight <number>"
	// flightNotConfiguredLine answers a build with no flight_url.
	flightNotConfiguredLine = "flight is not configured: set flight_url in config.toml to enable it"
	// flightRejectedLine answers an argument that cannot be a flight number. It
	// never repeats the argument: the asker's own text is not the bot's to
	// publish under its own name.
	flightRejectedLine = "that is not a flight number: use an airline code and a number, as in BA123"
	// flightFailedLine answers two providers that both failed to answer at all.
	flightFailedLine = "flight: providers unreachable, try again later"
	// flightMisconfiguredLine answers a flight_url the bot refuses to use.
	flightMisconfiguredLine = "flight is misconfigured: the operator must fix flight_url"
	// flightUnknownLine answers a number no provider publishes.
	flightUnknownLine = "no flight number %v is published: check the number and try again"
	// flightNoAircraftLine answers a valid number with nothing in the air: the
	// flight may be between legs, or simply beyond any receiver's range, and the
	// answer says which possibilities it cannot tell apart.
	flightNoAircraftLine = "no aircraft is transmitting %v right now: it may be between flights, or beyond receiver range"
	// maxFlightBodyBytes bounds one flight answer. Both providers answer a few
	// kilobytes; this leaves generous room and still bounds what the bot reads.
	maxFlightBodyBytes = 64 << 10
	// maxFlightFieldBytes bounds one provider-supplied field of the answer.
	maxFlightFieldBytes = 48
	// maxFlightNumberBytes bounds a requested flight number. Airline numbers are
	// far shorter, and the cap bounds the URL and every log line built from it.
	maxFlightNumberBytes = 8
	// flightLiveCacheTTL is how long one live state is reused. It is far shorter
	// than the shared provider TTL because a position is only interesting while
	// it is current, and the answer reports how old it really is either way.
	flightLiveCacheTTL = 90 * time.Second
	// flightRouteCacheTTL is how long one route is reused. A published route
	// changes about as often as an airline's timetable does.
	flightRouteCacheTTL = 10 * time.Minute
	// flightStaleAfter is the age at which a position stops being reported as
	// where the aircraft is and starts being reported as where it was.
	flightStaleAfter = 60 * time.Second
	// flightGroundSpeedFloor is the ground speed at or below which a reported
	// speed is not worth showing: a parked aircraft's last known speed is noise.
	flightGroundSpeedFloor = 0.5
)

// errFlightNumberRejected reports a requested flight number outside the
// allowlist. It is a sentinel so the command layer answers one fixed line and
// never repeats the text that caused it.
var errFlightNumberRejected = errors.New("flight number rejected")

// Errors returned while reading a provider's answer. They keep "the provider says
// this number is not published" apart from "the provider could not be read",
// because the first is an answer about the flight and the second is not.
var (
	// errFlightRouteUnknown reports a route provider that answered, and whose
	// answer says it does not publish this number.
	errFlightRouteUnknown = errors.New("flight: the route provider does not publish this number")
	// errFlightRouteUnreadable reports a route answer that is not the JSON the
	// command can read.
	errFlightRouteUnreadable = errors.New("flight: the route provider's answer is unreadable")
	// errFlightLiveUnreadable reports a live answer that is not the JSON the
	// command can read.
	errFlightLiveUnreadable = errors.New("flight: the live provider's answer is unreadable")
)

// flightEmergencies maps the emergency words a live provider may report onto the
// wording the answer uses. A word that is not in this table is not repeated: the
// provider's own text is untrusted, and a flag is exactly the kind of field an
// attacker would use to post arbitrary text under the bot's name.
var flightEmergencies = map[string]string{
	"general":   "general emergency",
	"lifeguard": "lifeguard",
	"minfuel":   "minimum fuel",
	"nordo":     "radio failure",
	"unlawful":  "unlawful interference",
	"downed":    "downed aircraft",
	"reserved":  "reserved",
}

// flightSquawkCodes maps the three transponder codes that mean something to
// everybody onto their meaning, so the answer says what the code means instead of
// leaving the reader to look it up.
var flightSquawkCodes = map[string]string{
	"7500": "unlawful interference",
	"7600": "radio failure",
	"7700": "general emergency",
}

// sanitizeFlightNumber validates and normalizes one requested flight number.
// Airline numbers are written many ways — "BA123", "ba123", "BA 123", "BA-123" —
// so the spaces and hyphens a passenger may type are dropped, the letters are
// upper-cased, and the result must be two to eight letters and digits carrying at
// least one of each, which every airline number has and which cannot restructure
// a URL. Everything else is rejected.
func sanitizeFlightNumber(raw string) (string, error) {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == ' ' || r == '-' || r == '\t':
			continue
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			return "", errFlightNumberRejected
		}
		if b.Len() > maxFlightNumberBytes {
			return "", errFlightNumberRejected
		}
	}
	number := b.String()
	if len(number) < 2 ||
		!strings.ContainsFunc(number, isASCIIDigit) ||
		!strings.ContainsFunc(number, isASCIILetter) {
		return "", errFlightNumberRejected
	}
	return number, nil
}

// isASCIIDigit reports whether r is an ASCII digit.
func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// isASCIILetter reports whether r is an ASCII letter.
func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// flightAirport is one endpoint of a route, reduced to the parts worth showing:
// the code a passenger reads on a boarding pass, and the place it names.
type flightAirport struct {
	// code is the IATA code when the provider gave one, and the ICAO code
	// otherwise, so an airport is never shown without a code.
	code string
	// place is the municipality when the provider named one, and the airport's
	// own name otherwise.
	place string
}

// flightRoute is the static half of the answer: who is flying, under which
// callsign, and between which two airports.
type flightRoute struct {
	// airline is the operator's name, e.g. "British Airways".
	airline string
	// iata is the number a passenger knows, e.g. "BA123".
	iata string
	// icao is the callsign the radio carries, e.g. "BAW123". The live query uses
	// this, which is the whole reason the route is looked up first.
	icao string
	// origin and destination are the two ends of the published route.
	origin      flightAirport
	destination flightAirport
}

// parseFlightRoute reads the route provider's answer. It tolerates every field
// being absent, because a provider may add or rename fields at any time, and it
// reports errFlightRouteUnknown only when the answer is readable JSON that carries
// no route for this number.
func parseFlightRoute(body []byte) (flightRoute, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return flightRoute{}, fmt.Errorf("%w: %w", errFlightRouteUnreadable, err)
	}
	// An unknown number is answered with a plain string where the route object
	// would be ("response": "invalid callsign: …"), so a non-empty string there
	// is the provider saying "not one of mine".
	if note := jsonField(root, "response"); note != "" {
		return flightRoute{}, errFlightRouteUnknown
	}
	icao := safeEcho(jsonField(root, "response.flightroute.callsign_icao"), maxFlightFieldBytes)
	iata := safeEcho(jsonField(root, "response.flightroute.callsign_iata"), maxFlightFieldBytes)
	if icao == "" && iata == "" {
		// Readable JSON with no route in it: the provider answered, and it has
		// nothing for this number.
		return flightRoute{}, errFlightRouteUnknown
	}
	return flightRoute{
		airline:     safeEcho(jsonField(root, "response.flightroute.airline.name"), maxFlightFieldBytes),
		iata:        iata,
		icao:        icao,
		origin:      flightAirportFrom(root, "response.flightroute.origin"),
		destination: flightAirportFrom(root, "response.flightroute.destination"),
	}, nil
}

// flightAirportFrom reads one endpoint of a route, preferring the code and the
// place a passenger would recognize.
func flightAirportFrom(root any, path string) flightAirport {
	code := jsonField(root, path+".iata_code")
	if code == "" {
		code = jsonField(root, path+".icao_code")
	}
	place := jsonField(root, path+".municipality")
	if place == "" {
		place = jsonField(root, path+".name")
	}
	return flightAirport{
		code:  safeEcho(code, maxFlightFieldBytes),
		place: safeEcho(place, maxFlightFieldBytes),
	}
}

// flightLive is one aircraft's live state, already reduced to the fields the
// answer uses. fetchedAt is kept so a cached state's age stays honest: what the
// answer reports is seenPos plus however long the cache has held it.
type flightLive struct {
	// fetchedAt is when the provider answered, or zero for a state that was
	// never fetched.
	fetchedAt time.Time
	// callsign is the identification the aircraft transmits, trimmed.
	callsign string
	// kind is the aircraft type, e.g. "A333", empty when the provider gave none.
	kind string
	// altitudeFeet is the barometric altitude, and altitudeKnown reports whether
	// the provider gave one: the same field reads "ground" when it did not.
	altitudeFeet  int
	altitudeKnown bool
	// onGround reports the provider's own on-ground flag.
	onGround bool
	// verticalFPM is the rate of climb in feet per minute, negative when
	// descending, and verticalKnown reports whether it was reported at all.
	verticalFPM   int
	verticalKnown bool
	// speedKt is the ground speed in knots, and speedKnown reports whether the
	// provider gave one worth showing.
	speedKt    float64
	speedKnown bool
	// trackDeg is the ground track in degrees true, and trackKnown reports
	// whether it was given.
	trackDeg   float64
	trackKnown bool
	// lat and lon are the last known position, and positionKnown reports whether
	// the provider gave one.
	lat           float64
	lon           float64
	positionKnown bool
	// seenPos is how long before the fetch the provider last heard a position.
	seenPos time.Duration
	// squawk is the transponder code, empty when the provider gave none.
	squawk string
	// emergency is the wording for the provider's emergency word, empty when the
	// provider reported none or reported a word this command does not know.
	emergency string
	// alert reports the provider's alert flag, which is set for the three
	// emergency squawks and for a general emergency.
	alert bool
	// others is how many other aircraft transmitted the same callsign in the
	// same answer, which happens and is worth saying rather than hiding.
	others int
}

// parseFlightLive reads the live provider's answer and returns the freshest
// aircraft carrying the callsign. found reports whether any aircraft was in the
// answer at all: an answer with an empty list is the provider saying that nothing
// is transmitting this callsign right now, which is a real answer and not a
// failure. now is when the answer was fetched.
func parseFlightLive(body []byte, now time.Time) (state flightLive, found bool, err error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return flightLive{}, false, fmt.Errorf("%w: %w", errFlightLiveUnreadable, err)
	}
	aircraft, ok := jsonArray(root, "ac")
	if !ok {
		return flightLive{}, false, fmt.Errorf("%w: the answer carries no aircraft list", errFlightLiveUnreadable)
	}
	if len(aircraft) == 0 {
		return flightLive{}, false, nil
	}
	best := -1
	bestAge := time.Duration(0)
	for i := range aircraft {
		item, ok := aircraft[i].(map[string]any)
		if !ok {
			continue
		}
		age := parsedSeconds(jsonField(item, "seen_pos"))
		if best < 0 || age < bestAge {
			best, bestAge = i, age
		}
	}
	if best < 0 {
		return flightLive{}, false, nil
	}
	item, _ := aircraft[best].(map[string]any)
	state = flightLiveFrom(item, now)
	state.others = len(aircraft) - 1
	return state, true, nil
}

// flightLiveFrom reduces one aircraft object to the fields the answer uses. Every
// number is read as text and parsed here, so a provider that renames or drops a
// field degrades that one field instead of the whole answer.
func flightLiveFrom(item map[string]any, now time.Time) flightLive {
	state := flightLive{
		fetchedAt: now,
		callsign:  safeEcho(jsonField(item, "flight"), maxFlightFieldBytes),
		kind:      safeEcho(jsonField(item, "t"), maxFlightFieldBytes),
		squawk:    safeEcho(jsonField(item, "squawk"), maxFlightFieldBytes),
		onGround:  jsonField(item, "on_ground") == "true",
		seenPos:   parsedSeconds(jsonField(item, "seen_pos")),
	}
	if word := strings.ToLower(strings.TrimSpace(jsonField(item, "emergency"))); word != "" {
		state.emergency = flightEmergencies[word]
	}
	if alert := strings.TrimSpace(jsonField(item, "alert")); alert != "" && alert != "0" && alert != "false" {
		state.alert = true
	}
	// The altitude field reads "ground" while an aircraft is on the ground, so a
	// non-numeric value means "no altitude", never zero feet.
	if feet, ok := parsedInt(jsonField(item, "alt_baro")); ok {
		state.altitudeFeet, state.altitudeKnown = feet, true
	}
	if fpm, ok := parsedInt(jsonField(item, "baro_rate")); ok {
		state.verticalFPM, state.verticalKnown = fpm, true
	}
	if knots, ok := parsedFloat(jsonField(item, "gs")); ok && knots > flightGroundSpeedFloor {
		state.speedKt, state.speedKnown = knots, true
	}
	if track, ok := parsedFloat(jsonField(item, "track")); ok {
		state.trackDeg, state.trackKnown = track, true
	}
	lat, latOK := parsedFloat(jsonField(item, "lat"))
	lon, lonOK := parsedFloat(jsonField(item, "lon"))
	if latOK && lonOK {
		state.lat, state.lon, state.positionKnown = lat, lon, true
	}
	return state
}

// parsedFloat reads a number the provider sent as text or as a JSON number.
func parsedFloat(text string) (float64, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, false
	}
	var value float64
	if _, err := fmt.Sscanf(text, "%g", &value); err != nil {
		return 0, false
	}
	return value, true
}

// parsedInt reads a whole number the provider sent as text or as a JSON number,
// which is how the shared JSON reader renders every numeric field.
func parsedInt(text string) (int, bool) {
	value, ok := parsedFloat(text)
	if !ok {
		return 0, false
	}
	return int(value), true
}

// parsedSeconds reads a duration the provider reports in seconds, clamping a
// negative value to zero rather than reporting a position from the future.
func parsedSeconds(text string) time.Duration {
	seconds, ok := parsedFloat(text)
	if !ok || seconds < 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// flightStateAge reports how old the last known position really is: what the
// provider said when it answered, plus however long the answer has been held.
// A clock that went backwards reports the provider's own age rather than a
// negative one.
func flightStateAge(live flightLive, now time.Time) time.Duration {
	held := now.Sub(live.fetchedAt)
	if held < 0 || live.fetchedAt.IsZero() {
		held = 0
	}
	return live.seenPos + held
}

// flightRouteLine renders the route: the airline and the number a passenger
// knows, the callsign the radio carries, and the two airports. Every part is
// optional, so a provider that answers with only some of it still produces a
// readable line.
func flightRouteLine(route flightRoute) string {
	number := route.iata
	if number == "" {
		number = route.icao
	}
	head := strings.TrimSpace(strings.TrimSpace(route.airline + " " + number))
	if route.icao != "" && route.icao != number {
		head += " (" + route.icao + ")"
	}
	ends := flightEndpointText(route.origin) + " → " + flightEndpointText(route.destination)
	switch {
	case head == "":
		return ends
	case ends == " → ":
		return head
	default:
		return head + ": " + ends
	}
}

// flightEndpointText renders one end of a route: "London (LHR)", or just the code
// or the place when only one of them is known.
func flightEndpointText(end flightAirport) string {
	switch {
	case end.place != "" && end.code != "":
		return end.place + " (" + end.code + ")"
	case end.code != "":
		return end.code
	default:
		return end.place
	}
}

// flightStateLine renders where the aircraft is: what it is doing, how high, how
// fast, on which track, and where the last position put it.
func flightStateLine(live flightLive) string {
	parts := make([]string, 0, 7)
	switch {
	case live.onGround:
		parts = append(parts, "on the ground")
	case live.altitudeKnown:
		parts = append(parts, fmt.Sprintf("%v ft", live.altitudeFeet))
	default:
		parts = append(parts, "airborne, altitude not reported")
	}
	if live.verticalKnown {
		switch {
		case live.verticalFPM > 0:
			parts = append(parts, fmt.Sprintf("climbing %v fpm", live.verticalFPM))
		case live.verticalFPM < 0:
			parts = append(parts, fmt.Sprintf("descending %v fpm", -live.verticalFPM))
		default:
			parts = append(parts, "level")
		}
	}
	if live.speedKnown {
		parts = append(parts, fmt.Sprintf("%v kt", int(live.speedKt+0.5)))
	}
	if live.trackKnown {
		parts = append(parts, fmt.Sprintf("track %03v°", int(live.trackDeg+0.5)%360))
	}
	if live.positionKnown {
		parts = append(parts, flightPositionText(live.lat, live.lon))
	}
	if live.kind != "" {
		parts = append(parts, live.kind)
	}
	line := strings.Join(parts, " | ")
	if live.callsign == "" {
		return line
	}
	return live.callsign + ": " + line
}

// flightPositionText renders a position the way a person reads it off a map:
// degrees to three decimals, with the hemisphere spelled out.
func flightPositionText(lat, lon float64) string {
	return fmt.Sprintf("%v%v %v%v", fixed1(lat), hemisphere(lat, "N", "S"), fixed1(lon), hemisphere(lon, "E", "W"))
}

// hemisphere returns the letter for a coordinate's sign.
func hemisphere(value float64, positive, negative string) string {
	if value < 0 {
		return negative
	}
	return positive
}

// fixed1 renders the magnitude of a coordinate to three decimals. Degrees to
// three decimals are about 100 metres, which is as fine as a chat line needs.
func fixed1(value float64) string {
	if value < 0 {
		value = -value
	}
	return fmt.Sprintf("%.3f", value)
}

// flightTransponderLine renders the transponder code, its meaning when it has one
// everybody knows, any emergency the provider reported, and how old the position
// is. The age belongs here because a position without its age is a claim the bot
// cannot support.
func flightTransponderLine(live flightLive, now time.Time) string {
	parts := make([]string, 0, 4)
	if live.squawk != "" {
		code := live.squawk
		if meaning := flightSquawkCodes[code]; meaning != "" {
			code += " (" + meaning + ")"
		}
		parts = append(parts, "squawk "+code)
	}
	if live.emergency != "" {
		parts = append(parts, "EMERGENCY "+live.emergency)
	}
	if live.alert {
		parts = append(parts, "alert")
	}
	parts = append(parts, "position heard "+countdownMagnitude(flightStateAge(live, now))+" ago")
	return strings.Join(parts, " | ")
}

// flightStaleNote answers a position that has stopped being current, so the
// answer never reads as "it is there now" when it is not.
func flightStaleNote(live flightLive, now time.Time) string {
	age := flightStateAge(live, now)
	if age < flightStaleAfter {
		return ""
	}
	return fmt.Sprintf("note: that position is %v old, so the aircraft has moved since",
		countdownMagnitude(age))
}

// flightCache remembers one parsed flight answer for a short time. It is
// deliberately generic over the answer, because a route and a live state are
// cached with different lifetimes, and it never invents a value it did not
// receive: found is stored and returned with the value.
type flightCache[V any] struct {
	mu      sync.Mutex
	entries map[string]flightCacheEntry[V]
	order   []string
	ttl     time.Duration
	max     int
}

// flightCacheEntry is one cached answer and when it stops being reused.
type flightCacheEntry[V any] struct {
	value     V
	found     bool
	expiresAt time.Time
}

// newFlightCache builds a cache for at most max answers, each reused for ttl.
func newFlightCache[V any](ttl time.Duration, max int) *flightCache[V] {
	return &flightCache[V]{
		entries: make(map[string]flightCacheEntry[V]),
		ttl:     ttl,
		max:     max,
	}
}

// get returns the cached answer for key while it is still fresh. The middle
// result is the stored found flag, and the last one reports whether anything was
// cached at all: a cached "nothing is transmitting this callsign" is an answer,
// not a miss.
func (c *flightCache[V]) get(key string, now time.Time) (V, bool, bool) {
	var zero V
	if c == nil {
		return zero, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expiresAt) {
		return zero, false, false
	}
	return entry.value, entry.found, true
}

// put stores one answer for key, evicting the oldest entry when the cache is
// full.
func (c *flightCache[V]) put(key string, value V, found bool, now time.Time) {
	if c == nil || c.max <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := flightCacheEntry[V]{value: value, found: found, expiresAt: now.Add(c.ttl)}
	if _, ok := c.entries[key]; ok {
		c.entries[key] = entry
		return
	}
	for len(c.entries) >= c.max && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.entries[key] = entry
	c.order = append(c.order, key)
}

// runFlight answers "where is this flight". The route is looked up first, because
// its callsign is what the live feed knows the flight by; the live state is then
// looked up with that callsign. Neither lookup can veto the other: a route that
// cannot be read still leaves a live position worth reporting, and a live feed
// that cannot be reached still leaves the route, so the answer always says what
// it knows and names what it could not find out.
func (c *commandContext) runFlight() []string {
	template := strings.TrimSpace(c.reg.bot.cfg.FlightURL)
	if template == "" {
		return []string{flightNotConfiguredLine}
	}
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + flightUsage}
	}
	number, err := sanitizeFlightNumber(c.Args)
	if err != nil {
		return []string{flightRejectedLine}
	}
	now := c.req.Now
	if now.IsZero() {
		now = time.Now()
	}

	var (
		route    *flightRoute
		routeOut flightRouteOutcome
		notes    []string
	)
	if routeTemplate := strings.TrimSpace(c.reg.bot.cfg.FlightRouteURL); routeTemplate != "" {
		route, routeOut = c.lookupFlightRoute(routeTemplate, number, now)
		switch routeOut {
		case flightRouteMisconfigured:
			notes = append(notes, "note: the route provider is misconfigured; the operator must fix flight_route_url")
		case flightRouteFailed:
			notes = append(notes, "note: the route provider did not answer")
		case flightRouteUnreadable:
			notes = append(notes, "note: the route provider's answer could not be read")
		case flightRouteUnknown:
			notes = append(notes, "note: no route is published for this number")
		case flightRouteOK:
		}
	}

	// The live feed knows a flight by its radio callsign, so the route's ICAO
	// callsign is used when there is one and the number as typed otherwise.
	callsign := number
	if route != nil && route.icao != "" {
		callsign = route.icao
	}
	fetchURL, err := providerURLValues(template, map[string]string{flightToken: callsign})
	if err != nil {
		// The operator's template is at fault: the detail goes to the log, which
		// only the operator reads, and never into the room.
		logf("flight: %v", err)
		return []string{flightMisconfiguredLine}
	}
	live, found, liveOut := c.lookupFlightLive(fetchURL, now)
	switch liveOut {
	case flightLiveFailed:
		notes = append(notes, "note: the live provider did not answer")
	case flightLiveUnreadable:
		notes = append(notes, "note: the live provider's answer could not be read")
	case flightLiveOK:
	}

	lines := make([]string, 0, 5)
	if route != nil {
		lines = append(lines, flightRouteLine(*route))
	}
	switch {
	case found:
		// The state line carries the callsign the aircraft is actually
		// transmitting, so the answer identifies itself even when no route was
		// found for the number that was asked about.
		lines = append(lines, flightStateLine(live))
		lines = append(lines, flightTransponderLine(live, now))
		if note := flightStaleNote(live, now); note != "" {
			notes = append([]string{note}, notes...)
		}
		switch {
		case live.others == 1:
			notes = append(notes, "note: 1 other aircraft is transmitting this callsign")
		case live.others > 1:
			notes = append(notes, fmt.Sprintf("note: %v other aircraft are transmitting this callsign", live.others))
		}
	case routeOut == flightRouteUnknown && liveOut == flightLiveOK:
		// Nothing is in the air under a number no route provider publishes, so
		// there is nothing to report but the number itself.
		return []string{fmt.Sprintf(flightUnknownLine, number)}
	case liveOut == flightLiveOK:
		lines = append(lines, fmt.Sprintf(flightNoAircraftLine, callsign))
	}
	if len(lines) == 0 {
		// Nothing came back from either provider, so there is nothing to report
		// but the failure itself: two notes saying "unreachable" twice would only
		// bury the one useful sentence.
		return []string{flightFailedLine}
	}
	lines = append(lines, notes...)
	return flightBudget(lines, c.replyBudget())
}

// flightRouteOutcome is what one route lookup produced.
type flightRouteOutcome int

// The outcomes of a route lookup. The zero value is reserved for the case
// where no route provider is configured, which is not a lookup outcome.
const (
	_ flightRouteOutcome = iota
	// flightRouteOK means a route was read.
	flightRouteOK
	// flightRouteUnknown means the provider answered that it does not publish
	// this number.
	flightRouteUnknown
	// flightRouteFailed means the provider did not answer usably: it was
	// unreachable, or it answered a status this command cannot read.
	flightRouteFailed
	// flightRouteUnreadable means the provider answered something this command
	// cannot read, including an answer too large to hold.
	flightRouteUnreadable
	// flightRouteMisconfigured means the operator's template is unusable.
	flightRouteMisconfigured
)

// flightLiveOutcome is what one live lookup produced.
type flightLiveOutcome int

// The outcomes of a live lookup.
const (
	// flightLiveOK means the provider answered in a shape the bot can read.
	flightLiveOK flightLiveOutcome = iota
	// flightLiveFailed means the provider did not answer usably, as above.
	flightLiveFailed
	// flightLiveUnreadable means the provider answered something unreadable.
	flightLiveUnreadable
)

// lookupFlightRoute resolves one flight number through the route provider,
// caching a successful route for its own lifetime. A provider failure and an
// answer that says "not one of mine" are reported apart, because only the second
// is a statement about the flight.
func (c *commandContext) lookupFlightRoute(template, number string, now time.Time) (*flightRoute, flightRouteOutcome) {
	fetchURL, err := providerURLValues(template, map[string]string{flightToken: number})
	if err != nil {
		logf("flight: route template: %v", err)
		return nil, flightRouteMisconfigured
	}
	if cached, ok, hit := c.reg.flightRoutes.get(fetchURL, now); hit {
		if !ok {
			return nil, flightRouteUnknown
		}
		return &cached, flightRouteOK
	}
	body, err := c.reg.fetchBounded(fetchURL, maxFlightBodyBytes)
	if err != nil {
		if statusCode(err) == http.StatusNotFound {
			// Verified live: a number that is shaped like a flight number but is
			// not published is answered with 404 and {"response":"unknown
			// callsign"}. That is an answer about the number rather than a
			// failure, and it is cached like any other answer, because asking
			// again inside the lifetime would only get the same reply.
			c.reg.flightRoutes.put(fetchURL, flightRoute{}, false, now)
			return nil, flightRouteUnknown
		}
		logf("flight: route lookup: %v", err)
		return nil, flightOutcomeFrom(err, flightRouteFailed, flightRouteUnreadable)
	}
	route, err := parseFlightRoute([]byte(body))
	if errors.Is(err, errFlightRouteUnknown) {
		c.reg.flightRoutes.put(fetchURL, flightRoute{}, false, now)
		return nil, flightRouteUnknown
	}
	if err != nil {
		logf("flight: route lookup: %v", err)
		return nil, flightRouteUnreadable
	}
	c.reg.flightRoutes.put(fetchURL, route, true, now)
	return &route, flightRouteOK
}

// lookupFlightLive resolves one callsign through the live provider, caching the
// parsed state for its own short lifetime so the reported age can keep growing
// while the position stays the one the provider gave.
func (c *commandContext) lookupFlightLive(fetchURL string, now time.Time) (flightLive, bool, flightLiveOutcome) {
	if cached, found, hit := c.reg.flightLive.get(fetchURL, now); hit {
		return cached, found, flightLiveOK
	}
	body, err := c.reg.fetchBounded(fetchURL, maxFlightBodyBytes)
	if err != nil {
		logf("flight: live lookup: %v", err)
		return flightLive{}, false, flightOutcomeFrom(err, flightLiveFailed, flightLiveUnreadable)
	}
	state, found, err := parseFlightLive([]byte(body), now)
	if err != nil {
		logf("flight: live lookup: %v", err)
		return flightLive{}, false, flightLiveUnreadable
	}
	c.reg.flightLive.put(fetchURL, state, found, now)
	return state, found, flightLiveOK
}

// flightOutcomeFrom classifies one fetch failure for a command outcome: an answer
// that arrived but could not be read is a different thing from a provider that
// did not answer usably, and the answer says which happened.
func flightOutcomeFrom[T comparable](err error, unreachable, unreadable T) T {
	if errors.Is(err, errProviderBodyTooLarge) {
		return unreadable
	}
	return unreachable
}

// statusCode reports the HTTP status a provider answered with, or 0 when the
// failure was not a status at all.
func statusCode(err error) int {
	if status, ok := errors.AsType[*providerStatusError](err); ok {
		return status.code
	}
	return 0
}

// flightBudget caps the answer at the reply budget, saying how many lines it
// dropped rather than dropping them silently.
func flightBudget(lines []string, budget int) []string {
	if budget <= 0 || len(lines) <= budget {
		return lines
	}
	kept := append([]string(nil), lines[:budget-1]...)
	return append(kept, fmt.Sprintf("… %v not shown", len(lines)-(budget-1)))
}
