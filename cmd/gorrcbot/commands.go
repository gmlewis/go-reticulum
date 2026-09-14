// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the command registry: one entry per command, with a summary
// and a handler, so the help listing and the per-command help are generated from
// the same table and can never drift apart.
//
// The command NAMES mirror the official RRC hub bot minus its '!' prefix
// (botinfo, dn, dnotice, dnoticecap, dnoticeme, help, ping, uptime, weather,
// whoami, wx — captured from the RNS Community Hub, see
// testdata/oracle-rns-community.txt), because a client that already knows that
// bot should feel at home. The wording of their replies follows the captured
// transcripts. A handful of commands go beyond the official set: they answer
// questions that only matter on a mesh, where a peer may have been offline for
// hours (seen, members, rooms, id).

package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// HubDestName is the destination name every RRC hub answers on.
const HubDestName = rrc.HubDestName

// Reply limits and wordings that come from the captured oracle transcript.
const (
	// weatherFailedLine is the official bot's weather failure wording; the
	// requester's nick is substituted in.
	weatherFailedLine = "@%v Weather lookup failed (network error). Try again later."
	// dnoticeUsage is the official usage line, widened to accept a nick so a
	// human does not have to know a 32-character hash.
	dnoticeUsage = "dnotice <nick|hash|me> <text>"
	// dnoticemeUsage is the official usage line.
	dnoticemeUsage = "dnoticeme <text>"
	// weatherUsage is the usage line for the weather commands.
	weatherUsage = "weather <place>"
	// seenUsage is the usage line for the seen command.
	seenUsage = "seen <nick|hash>"
	// maxSeenTextBytes bounds the quoted message a seen reply includes.
	maxSeenTextBytes = 120
	// weatherTimeout bounds one weather lookup so a slow provider cannot hold
	// the command layer.
	weatherTimeout = 8 * time.Second
	// maxWeatherBodyBytes bounds the provider response the bot will read.
	maxWeatherBodyBytes = 8 << 10
)

// command is one registry entry.
type command struct {
	// name is the command word, lowercase and without a prefix.
	name string
	// summary is one short clause used by help.
	summary string
	// usage is the argument shape, empty when the command takes none.
	usage string
	// run produces the reply lines for one invocation.
	run func(*commandContext) []string
}

// commandContext is one command invocation.
type commandContext struct {
	reg *registry
	req *commandRequest
	// Args is the text after the command name, trimmed.
	Args string
}

// session is the hub session the command runs against.
func (c *commandContext) session() *hubSession { return c.req.Session }

// conn is the connection the command runs against.
func (c *commandContext) conn() hubConn { return c.req.Session.conn }

// registry maps command names to their handlers.
type registry struct {
	bot *bot
	// commands is sorted by name, which is the order help lists them in.
	commands []command
	// byName indexes every command name.
	byName map[string]*command
	// aliases groups the names that share a handler, for introspection only.
	aliases map[string]string
	// fetch performs one weather lookup. It is injected so the command layer
	// never reaches the network by itself.
	fetch func(url string) (string, error)
}

// newRegistry builds the command table for one bot.
func newRegistry(b *bot) *registry {
	r := &registry{
		bot:     b,
		byName:  make(map[string]*command),
		aliases: map[string]string{"dn": "dnotice", "wx": "weather"},
		fetch:   httpFetch,
	}
	r.commands = r.build()
	sort.Slice(r.commands, func(i, j int) bool { return r.commands[i].name < r.commands[j].name })
	for i := range r.commands {
		r.byName[r.commands[i].name] = &r.commands[i]
	}
	return r
}

// names returns the command names in registry order.
func (r *registry) names() []string {
	out := make([]string, 0, len(r.commands))
	for _, cmd := range r.commands {
		out = append(out, cmd.name)
	}
	return out
}

// Run executes one addressed command line. It is the commandRunner the reply
// policy calls, so its result is exactly the reply lines.
func (r *registry) Run(req *commandRequest) []string {
	name, args := splitCommandLine(req.Command)
	if name == "" {
		// Addressed with no command: the only useful answer is the listing.
		return r.helpListing()
	}
	cmd, ok := r.byName[name]
	if !ok {
		return []string{unknownCommandLine(req.Nick)}
	}
	return cmd.run(&commandContext{reg: r, req: req, Args: args})
}

// helpListing renders the one-line command listing, in the official bot's shape.
func (r *registry) helpListing() []string {
	return []string{"Commands: " + strings.Join(r.names(), ", ")}
}

// unknownCommandLine is the single short line an unrecognised command produces.
func unknownCommandLine(nick string) string {
	if strings.TrimSpace(nick) == "" {
		nick = DefaultTriggerNick
	}
	return fmt.Sprintf("unknown command — try @%v help", nick)
}

// splitCommandLine splits a command line into its lowercase name and arguments.
func splitCommandLine(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.IndexAny(trimmed, " \t")
	if idx < 0 {
		return strings.ToLower(trimmed), ""
	}
	return strings.ToLower(trimmed[:idx]), strings.TrimSpace(trimmed[idx+1:])
}

// build is the command table itself.
func (r *registry) build() []command {
	return []command{
		{
			name:    "help",
			summary: "list the commands, or explain one",
			usage:   "help [command]",
			run:     (*commandContext).runHelp,
		},
		{
			name:    "ping",
			summary: "check that the bot is awake",
			run:     (*commandContext).runPing,
		},
		{
			name:    "uptime",
			summary: "report how long the bot and this connection have been up",
			run:     (*commandContext).runUptime,
		},
		{
			name:    "whoami",
			summary: "report your nick and identity hash as this hub sees them",
			run:     (*commandContext).runWhoami,
		},
		{
			name:    "botinfo",
			summary: "report the bot, this hub, and the bot's own identity",
			run:     (*commandContext).runBotinfo,
		},
		{
			name:    "dn",
			summary: "send a direct NOTICE to one client",
			usage:   dnoticeUsage,
			run:     (*commandContext).runDnotice,
		},
		{
			name:    "dnotice",
			summary: "send a direct NOTICE to one client",
			usage:   dnoticeUsage,
			run:     (*commandContext).runDnotice,
		},
		{
			name:    "dnoticecap",
			summary: "report the hub's direct-notice capability",
			usage:   "dnoticecap [target]",
			run:     (*commandContext).runDnoticeCap,
		},
		{
			name:    "dnoticeme",
			summary: "send yourself a direct NOTICE, to test the K_DST path",
			usage:   dnoticemeUsage,
			run:     (*commandContext).runDnoticeme,
		},
		{
			name:    "weather",
			summary: "look up the weather for a place",
			usage:   weatherUsage,
			run:     (*commandContext).runWeather,
		},
		{
			name:    "wx",
			summary: "look up the weather for a place",
			usage:   weatherUsage,
			run:     (*commandContext).runWeather,
		},
		{
			name:    "seen",
			summary: "report when a client last spoke in a joined room",
			usage:   seenUsage,
			run:     (*commandContext).runSeen,
		},
		{
			name:    "members",
			summary: "list the clients in a room",
			usage:   "members [room]",
			run:     (*commandContext).runMembers,
		},
		{
			name:    "rooms",
			summary: "list the rooms the bot has joined",
			run:     (*commandContext).runRooms,
		},
		{
			name:    "id",
			summary: "report the identity hash clients can use to address the bot",
			run:     (*commandContext).runID,
		},
	}
}

// runHelp lists the commands, or explains the one that was named.
func (c *commandContext) runHelp() []string {
	if c.Args == "" {
		return c.reg.helpListing()
	}
	name, _ := splitCommandLine(c.Args)
	cmd, ok := c.reg.byName[name]
	if !ok {
		return []string{unknownCommandLine(c.req.Nick)}
	}
	line := cmd.name + " — " + cmd.summary
	if cmd.usage != "" {
		line += ". Usage: " + cmd.usage
	}
	return []string{line}
}

// runPing answers pong, the official bot's wording.
func (c *commandContext) runPing() []string {
	return []string{"pong"}
}

// runUptime reports the bot's runtime and the current connection age in the
// official bot's shape.
func (c *commandContext) runUptime() []string {
	now := c.req.Now
	if now.IsZero() {
		now = time.Now()
	}
	var runtime, connection time.Duration
	if c.reg.bot != nil && !c.reg.bot.StartedAt().IsZero() {
		runtime = now.Sub(c.reg.bot.StartedAt())
	}
	connection = c.session().connectionAge(now)
	return []string{fmt.Sprintf("Uptime: runtime=%v; hub=%v; hub-connection=%v.",
		formatDuration(runtime), c.conn().HubAddressHex(), formatDuration(connection))}
}

// runWhoami reports the caller's nick and full identity hash, in the official
// bot's shape.
func (c *commandContext) runWhoami() []string {
	src := c.req.Msg.Src
	if len(src) == 0 {
		return []string{"I can see your message but not your identity hash."}
	}
	nick := c.peerName(src)
	return []string{fmt.Sprintf("You are %v (%v), role=user.", nick, hexString(src))}
}

// runBotinfo reports the bot, the hub, and the bot's own identity. The official
// bot reports hub-side scripts; gorrbot loads none, so it reports the size of
// its own command set instead of a meaningless zero.
func (c *commandContext) runBotinfo() []string {
	session := c.session()
	conn := c.conn()
	nick := c.reg.advertisedNick(session)
	rooms := len(conn.JoinedRoomList())
	return []string{fmt.Sprintf(
		"BotInfo: nickname=%v; dest=%v; hub=%v; hubname=%v; hubversion=%v; identity=%v; rooms=%v; commands=%v.",
		nick, HubDestName, conn.HubAddressHex(), hubNameOrUnknown(conn.GetServerName()),
		hubNameOrUnknown(conn.GetHubVersion()), c.reg.identityHex(), rooms, len(c.reg.commands))}
}

// runDnotice sends one direct NOTICE to a resolved target.
func (c *commandContext) runDnotice() []string {
	target, text, lines := c.resolveTargetWithText(c.Args)
	if lines != nil {
		return lines
	}
	fits, err := directNoticeFits(c.reg.bot.ownHash, target.Hash, c.reg.advertisedNick(c.session()), text)
	if err != nil {
		return []string{"could not build the direct notice: " + err.Error()}
	}
	if !fits {
		// The link layer would drop an oversized envelope without a word, so
		// the sender is told instead.
		return []string{"that text is too long for one direct notice; send it in shorter pieces"}
	}
	if err := c.conn().SendDirectNotice(target.Hash, text); err != nil {
		return []string{directNoticeErrorLine(err)}
	}
	return []string{fmt.Sprintf("Direct NOTICE sent to %v", target.String())}
}

// directNoticeFits reports whether text fits one direct-notice envelope. Like a
// room notice, an oversized one is dropped silently by the link layer.
func directNoticeFits(ownHash, dst []byte, nick, text string) (bool, error) {
	env := rrc.MakeDirectNoticeEnvelope(ownHash, dst, []byte(nick), text, make([]byte, 8), rrc.NowMs())
	data, err := rrc.EncodeEnvelope(env)
	if err != nil {
		return false, err
	}
	return len(data) <= rns.MDU, nil
}

// runDnoticeme sends the caller a direct NOTICE, which is a live self-test of
// the K_DST path.
func (c *commandContext) runDnoticeme() []string {
	text := strings.TrimSpace(c.Args)
	if text == "" {
		return []string{"Usage: " + dnoticemeUsage}
	}
	src := c.req.Msg.Src
	if len(src) != rrc.IdentityHashLen {
		return []string{"I cannot see your identity hash, so I cannot send you a notice."}
	}
	if err := c.conn().SendDirectNotice(src, text); err != nil {
		return []string{directNoticeErrorLine(err)}
	}
	return []string{"Direct NOTICE sent to self"}
}

// runDnoticeCap reports the hub's direct-notice capability, and for a named
// target whether that target is reachable.
func (c *commandContext) runDnoticeCap() []string {
	supported := c.conn().HasCapability(rrc.CapDirectNotice)
	if strings.TrimSpace(c.Args) == "" {
		return []string{fmt.Sprintf("Direct NOTICE supported: %v", pyBool(supported))}
	}
	target, lines := c.resolveTarget(strings.TrimSpace(c.Args))
	if lines != nil {
		return lines
	}
	return []string{fmt.Sprintf("Direct NOTICE supported: %v; %v reachable: %v",
		pyBool(supported), target.String(), pyBool(c.session().knowsPeer(target.HashHex)))}
}

// runWeather looks the place up with the configured provider. With no provider
// configured it says so instead of guessing, which is the honest answer.
func (c *commandContext) runWeather() []string {
	url := strings.TrimSpace(c.reg.bot.cfg.WeatherURL)
	if url == "" {
		return []string{"weather is not configured: set weather_url in config.toml to enable it"}
	}
	place := strings.TrimSpace(c.Args)
	if place == "" {
		return []string{"Usage: " + weatherUsage}
	}
	nick := c.peerName(c.req.Msg.Src)
	body, err := c.reg.fetch(strings.ReplaceAll(url, "{place}", place))
	if err != nil {
		return []string{fmt.Sprintf(weatherFailedLine, nick)}
	}
	line := firstNonEmptyLine(body)
	if line == "" {
		return []string{fmt.Sprintf(weatherFailedLine, nick)}
	}
	return []string{fmt.Sprintf("@%v %v", nick, line)}
}

// runSeen reports when a client last spoke, from the bot's own room buffers.
// This is the question a mesh client asks after being offline for hours.
func (c *commandContext) runSeen() []string {
	token := strings.TrimSpace(c.Args)
	if token == "" {
		return []string{"Usage: " + seenUsage}
	}
	target, lines := c.resolveTarget(token)
	if lines != nil {
		return lines
	}

	var (
		best     *rrc.RRCMessage
		bestRoom string
	)
	for _, room := range c.conn().JoinedRoomList() {
		for _, msg := range c.conn().GetMessages(room) {
			if hexString(msg.Src) != target.HashHex {
				continue
			}
			if best == nil || msg.Ts > best.Ts {
				best, bestRoom = msg, room
			}
		}
	}
	if best == nil {
		return []string{fmt.Sprintf("no messages from %v in the rooms I have joined", target.String())}
	}
	age := "an unknown time"
	if best.Ts > 0 {
		age = formatAge(c.req.Now.Sub(time.UnixMilli(best.Ts)))
	}
	return []string{fmt.Sprintf("%v last spoke %v ago in %v: %q",
		target.String(), age, bestRoom, truncateUTF8Bytes(best.Text, maxSeenTextBytes))}
}

// runMembers lists the clients the hub reports in a room, in the official /who
// notice shape.
func (c *commandContext) runMembers() []string {
	room := normalizeRoom(c.Args)
	if room == "" {
		room = normalizeRoom(c.req.Room)
	}
	if room == "" {
		return []string{"Usage: members [room]"}
	}
	members := c.conn().GetRoomMembers(room)
	if len(members) == 0 {
		return []string{fmt.Sprintf("members in %v: (none reported; the hub only answers for rooms it tracks)", room)}
	}
	parts := make([]string, 0, len(members))
	for _, member := range members {
		nick := member.Nick
		if nick == "" {
			nick = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%v (%v)", nick, shortHash(member.HashHex)))
	}
	sort.Strings(parts)
	return []string{fmt.Sprintf("members in %v: %v", room, strings.Join(parts, ", "))}
}

// runRooms lists the rooms the bot has joined.
func (c *commandContext) runRooms() []string {
	rooms := c.conn().JoinedRoomList()
	if len(rooms) == 0 {
		return []string{"I have not joined any room yet."}
	}
	sort.Strings(rooms)
	return []string{fmt.Sprintf("joined rooms on %v: %v", c.conn().HubAddressHex(), strings.Join(rooms, ", "))}
}

// runID reports the identity hash and the nicks clients can address the bot by.
func (c *commandContext) runID() []string {
	session := c.session()
	hash := c.reg.identityHex()
	nick := c.reg.advertisedNick(session)
	return []string{fmt.Sprintf("identity=%v; nick=%v; dest=%v; also answers to @%v and to a room's trigger nick.",
		hash, nick, HubDestName, shortHash(hash))}
}

// resolveTarget resolves one peer token to a target, or returns the single reply
// line that explains why it could not.
func (c *commandContext) resolveTarget(token string) (rrc.PeerTarget, []string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return rrc.PeerTarget{}, []string{"no target given"}
	}
	target, err := c.lookupTarget(token)
	if err != nil {
		return rrc.PeerTarget{}, []string{targetLookupErrorLine(err)}
	}
	return target, nil
}

// resolveTargetWithText resolves the target and the remaining arguments, or
// returns the single reply line that explains why it could not.
func (c *commandContext) resolveTargetWithText(args string) (rrc.PeerTarget, string, []string) {
	token, rest := splitCommandLine(args)
	if token == "" || rest == "" {
		return rrc.PeerTarget{}, "", []string{"Usage: " + dnoticeUsage}
	}
	target, err := c.lookupTarget(token)
	if err != nil {
		return rrc.PeerTarget{}, "", []string{targetLookupErrorLine(err)}
	}
	if len(target.Hash) != rrc.IdentityHashLen {
		return rrc.PeerTarget{}, "", []string{fmt.Sprintf(
			"I only know %v by a short hash prefix; use the full 32-character hash to reach them",
			target.String())}
	}
	return target, rest, nil
}

// lookupTarget resolves a nick, hash, or the literal "me".
func (c *commandContext) lookupTarget(token string) (rrc.PeerTarget, error) {
	if strings.EqualFold(token, "me") {
		src := c.req.Msg.Src
		if len(src) != rrc.IdentityHashLen {
			return rrc.PeerTarget{}, rrc.ErrPeerNotFound
		}
		return rrc.PeerTarget{
			HashHex: hexString(src),
			Hash:    src,
			Nick:    c.peerName(src),
		}, nil
	}
	return c.conn().ResolvePeerToken(token)
}

// targetLookupErrorLine turns a resolution failure into one short line.
func targetLookupErrorLine(err error) string {
	switch {
	case err == nil:
		return ""
	case isError(err, rrc.ErrPeerTokenEmpty):
		return "no target given"
	case isError(err, rrc.ErrPeerTokenTooShort):
		return fmt.Sprintf("a hash prefix needs at least %v characters; use the full hash or a nick",
			rrc.MinPeerHashPrefix)
	case isError(err, rrc.ErrPeerAmbiguous):
		return "that target is ambiguous: " + err.Error()
	default:
		return "no such peer: " + err.Error()
	}
}

// directNoticeErrorLine turns a direct-notice failure into one short line.
func directNoticeErrorLine(err error) string {
	switch {
	case isError(err, rrc.ErrDirectNoticesUnsupported):
		return "direct notices are not supported by this hub (it advertised no CAP_DIRECT_NOTICE)"
	case isError(err, rrc.ErrDestinationNotConnected):
		return "that client is not connected to this hub, so a direct notice cannot reach them"
	default:
		return "could not send the direct notice: " + err.Error()
	}
}

// isError reports whether err is or wraps target.
func isError(err, target error) bool {
	return target != nil && errors.Is(err, target)
}

// peerName reports the display nick of a peer, falling back to its short hash.
func (c *commandContext) peerName(peer []byte) string {
	if len(peer) == 0 {
		return "unknown"
	}
	if nick := strings.TrimSpace(c.conn().DisplayNameFor(peer)); nick != "" {
		return nick
	}
	return shortHash(hexString(peer))
}

// advertisedNick reports the nick the bot advertises on this hub.
func (r *registry) advertisedNick(s *hubSession) string {
	if r.bot == nil || r.bot.cfg == nil {
		return DefaultNick
	}
	return r.bot.cfg.AdvertisedNick(s.cfg)
}

// identityHex reports the bot's identity hash, or "" when it is unknown.
func (r *registry) identityHex() string {
	if r.bot == nil {
		return ""
	}
	return hexString(r.bot.ownHash)
}

// pyBool renders a boolean the way the official bot's Python does, because the
// captured oracle wording is "True" and "False".
func pyBool(v bool) string {
	if v {
		return "True"
	}
	return "False"
}

// shortHash renders the first 12 characters of a hash, which is the shape the
// hub's own /who notice uses.
func shortHash(hashHex string) string {
	if len(hashHex) <= 12 {
		return hashHex
	}
	return hashHex[:12]
}

// hubNameOrUnknown renders a hub-reported string, or "unknown" when the hub has
// not said.
func hubNameOrUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return strings.TrimSpace(s)
}

// formatDuration renders a duration as HH:MM:SS, matching the official bot's
// uptime output. Hours are not wrapped, so a long-running bot reads honestly.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, total/60%60, total%60)
}

// formatAge renders how long ago something happened, compactly.
func formatAge(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%vs", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%vm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%vh", int(d.Hours()))
	default:
		return fmt.Sprintf("%vd", int(d.Hours()/24))
	}
}

// truncateUTF8Bytes shortens s to at most n bytes without breaking a rune, and
// marks the shortening.
func truncateUTF8Bytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s, cut) {
		cut--
	}
	return s[:cut] + "…"
}

// isRuneStart reports whether the byte at index i begins a rune.
func isRuneStart(s string, i int) bool {
	return s[i]&0xC0 != 0x80
}

// firstNonEmptyLine returns the first line of body with visible content,
// trimmed. It is what a plain-text weather provider's answer is reduced to.
func firstNonEmptyLine(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// httpFetch performs one weather lookup and returns the response body. The bot
// never reaches the network for any other command.
func httpFetch(url string) (string, error) {
	client := &http.Client{Timeout: weatherTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("weather provider answered %v", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWeatherBodyBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Compile-time assertion that the registry satisfies the reply policy's seam:
// the responder calls exactly this method value.
var _ commandRunner = (*registry)(nil).Run
