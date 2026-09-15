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
// whoami, wx), because a client that already knows that bot should feel at
// home, and the wording of their replies matches its usage lines. A handful of
// commands go beyond the official set: they answer
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
	// weatherNotConfiguredLine is the answer when the operator has set no
	// weather_url.
	weatherNotConfiguredLine = "weather is not configured: set weather_url in config.toml to enable it"
	// weatherPlaceRejectedLine is the answer to a place outside the allowlist.
	// It never quotes the request back: the text is attacker-chosen, and
	// repeating it would publish it in a room under the bot's name.
	weatherPlaceRejectedLine = "weather: place may only contain letters, digits, spaces, commas, periods, hyphens or apostrophes (up to 64 bytes)"
	// weatherMisconfiguredLine is the answer when weather_url itself is
	// unusable. It never quotes the template, which may name a private host or
	// carry a key.
	weatherMisconfiguredLine = "weather is misconfigured: the operator must fix weather_url"
	// seenUsage is the usage line for the seen command.
	seenUsage = "seen <nick|hash>"
	// maxSeenTextBytes bounds the quoted message a seen reply includes.
	maxSeenTextBytes = 120
	// weatherTimeout bounds one weather lookup so a slow provider cannot hold
	// the command layer.
	weatherTimeout = 8 * time.Second
	// maxProviderBodyBytes bounds the provider response the bot will read. It is
	// deliberately generous: a weather answer is one short line, but a JSON
	// provider sends kilobytes, and a body cut short is invalid JSON that looks
	// like a provider failure. Each command applies its own, smaller limit on top
	// of this one through registry.fetchBounded.
	maxProviderBodyBytes = 256 << 10
)

// command is one registry entry.
type command struct {
	// name is the command word, lowercase and without a prefix.
	name string
	// summary is one short clause used by help.
	summary string
	// usage is the argument shape, empty when the command takes none.
	usage string
	// detail is the extra guidance "help <command>" prints after the summary
	// line. The literal {nick} is replaced with the nick this bot answers to,
	// so every example addresses the bot the way the asker must.
	detail []string
	// configHint is configuration guidance "help <command>" prints only while
	// configured reports the command's provider as unconfigured. Configuration
	// is the operator's concern: once the provider is set up, a user asking
	// about the command needs its usage, not how to install it. Empty for a
	// command that needs no configuration.
	configHint []string
	// configured reports whether this command's provider is set up. Nil for a
	// command that needs no configuration, in which case configHint is unused.
	configured func(*BotConfig) bool
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

// now is the clock this request runs on. A request without one is answered
// against the wall clock, which is what production always does.
func (c *commandContext) now() time.Time {
	if c.req.Now.IsZero() {
		return time.Now()
	}
	return c.req.Now
}

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
	// fetch performs one provider lookup. It is injected so the command layer
	// never reaches the network by itself.
	fetch func(url string) (string, error)
	// cache reuses a recent provider answer, so an active room cannot turn one
	// command into one request per asker.
	cache *providerCache
	// paths is the Reticulum transport the path command reads. It is nil when
	// the bot runs without one (unit tests), and the command says so.
	paths pathLookup
	// flightRoutes and flightLive cache parsed flight answers, keyed by the URL
	// that produced them.
	flightRoutes *flightCache[flightRoute]
	flightLive   *flightCache[flightLive]
	// watches is the subscription table the watch commands read. It is small
	// and in-memory: a watch does not survive a restart, and says so.
	watches *watchTable
	// announces is the announce cache the watch commands look names up in.
	announces *announceCache
	// lxmf is the LXMF router the msg command queues through. It is nil when
	// the operator has not enabled LXMF, and the command says exactly that
	// rather than reporting a failure.
	lxmf lxmfSender
	// lxmfBudget bounds how often the msg command writes into somebody else's
	// inbox, which no other command does.
	lxmfBudget *lxmfBudget
	// kjv loads and caches the Bible text the kjv command searches. It is
	// inert until the command runs, and its path is empty when the operator
	// has not configured one.
	kjv *kjvCache
}

// newRegistry builds the command table for one bot.
func newRegistry(b *bot) *registry {
	r := &registry{
		bot:     b,
		byName:  make(map[string]*command),
		aliases: map[string]string{"dn": "dnotice", "wx": "weather", "lxmf": "msg"},
		fetch:   httpFetch,
		cache:   newProviderCache(providerCacheTTL, providerCacheMaxEntries),
		// Flight answers are cached apart from the shared provider cache: a
		// route is static, while a live position is only worth reusing for as
		// long as it is still roughly where the aircraft is.
		flightRoutes: newFlightCache[flightRoute](flightRouteCacheTTL, providerCacheMaxEntries),
		flightLive:   newFlightCache[flightLive](flightLiveCacheTTL, providerCacheMaxEntries),
		// The announce cache belongs to the bot, which hears the announces; the
		// watch commands are just its user interface.
		watches:    b.announces.watches,
		announces:  b.announces,
		lxmfBudget: newLXMFBudget(),
		kjv:        newKJVCache(botKJVTxtFile(b)),
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
// config returns the operator's configuration, or nil when the registry was
// built without a bot (unit tests), in which case help prints no configuration
// guidance.
func (r *registry) config() *BotConfig {
	if r.bot == nil {
		return nil
	}
	return r.bot.cfg
}

func (r *registry) Run(req *commandRequest) []string {
	name, args := splitCommandLine(req.Command)
	if name == "" {
		// Addressed with no command: the only useful answer is the listing.
		return r.helpListing()
	}
	cmd, ok := r.byName[name]
	if !ok {
		return []string{unknownCommandLine(r.effectiveTriggerNick(req.Session, req.Room, req.Nick))}
	}
	return cmd.run(&commandContext{reg: r, req: req, Args: args})
}

// helpListing renders the one-line command listing, in the official bot's shape.
func (r *registry) helpListing() []string {
	return []string{"Commands: " + strings.Join(r.names(), ", ")}
}

// unknownCommandLine is the single short line an unrecognised command produces.
// The nick it names must be one the bot really answers to: an in-room request
// carries the addressed nick in its text, but a direct NOTICE carries only the
// command, so the caller passes the nick this session resolves for the room and
// the fallback is the operator's configured nick rather than the built-in
// default. Naming a nick the bot does not answer to would send the asker to a
// stranger's name.
func unknownCommandLine(nick string) string {
	nick = strings.TrimSpace(nick)
	if nick == "" {
		nick = DefaultTriggerNick
	}
	return fmt.Sprintf("unknown command — try @%v help", nick)
}

// effectiveTriggerNick is the nick this bot answers to in room: the nick the
// request itself named when it named one, and otherwise the configured nick the
// trigger policy resolves for that room (respond_to, then the hub's nick, then the
// global nick). A reply that tells somebody how to address the bot must name a
// nick the bot really answers to: a direct NOTICE carries no addressed nick at
// all, so the fallback matters, and the built-in default is only right when no
// configuration exists to ask.
func (r *registry) effectiveTriggerNick(session *hubSession, room, parsed string) string {
	if nick := strings.TrimSpace(parsed); nick != "" {
		return nick
	}
	if r == nil || r.bot == nil || r.bot.cfg == nil {
		return DefaultTriggerNick
	}
	var hub *HubConfig
	if session != nil {
		hub = session.cfg
	}
	if nick := r.bot.cfg.TriggerNick(hub, room); nick != "" {
		return nick
	}
	return DefaultTriggerNick
}

// effectiveTriggerNick is the registry method for this invocation's request.
func (c *commandContext) effectiveTriggerNick() string {
	return c.reg.effectiveTriggerNick(c.req.Session, c.req.Room, c.req.Nick)
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

// build is the command table itself. Every entry documents itself: the summary
// and usage are what "help" lists, and the detail is what "help <command>"
// explains, so no command is a mystery.
func (r *registry) build() []command {
	return []command{
		{
			name:    "help",
			summary: "list the commands, or explain one",
			usage:   "help [command]",
			detail: []string{
				"{nick} help lists every command.",
				"{nick} help <command> explains one, with examples.",
			},
			run: (*commandContext).runHelp,
		},
		{
			name:    "ping",
			summary: "check that the bot is awake",
			detail: []string{
				"Answers pong, so a client can tell the bot is up and the link is live.",
			},
			run: (*commandContext).runPing,
		},
		{
			name:    "uptime",
			summary: "report how long the bot and this connection have been up",
			detail: []string{
				"Reports the bot's runtime, this hub's hash, and this connection's age.",
			},
			run: (*commandContext).runUptime,
		},
		{
			name:    "whoami",
			summary: "report your nick and identity hash as this hub sees them",
			detail: []string{
				"Reports the nick this hub knows you by and your full identity hash.",
			},
			run: (*commandContext).runWhoami,
		},
		{
			name:    "botinfo",
			summary: "report the bot, this hub, and the bot's own identity",
			detail: []string{
				"Reports the bot's nick and identity, its hub's name and version, and",
				"how many rooms it has joined and commands it offers.",
			},
			run: (*commandContext).runBotinfo,
		},
		{
			name:    "dn",
			summary: "send a direct NOTICE to one client",
			usage:   dnoticeUsage,
			detail: []string{
				"<nick|hash|me> picks the recipient; a hash prefix needs at least 6",
				"hex characters, and me means you.",
				"The text travels as a direct NOTICE, so it must fit one envelope.",
			},
			run: (*commandContext).runDnotice,
		},
		{
			name:    "dnotice",
			summary: "send a direct NOTICE to one client",
			usage:   dnoticeUsage,
			detail: []string{
				"<nick|hash|me> picks the recipient; a hash prefix needs at least 6",
				"hex characters, and me means you.",
				"The text travels as a direct NOTICE, so it must fit one envelope.",
			},
			run: (*commandContext).runDnotice,
		},
		{
			name:    "dnoticecap",
			summary: "report the hub's direct-notice capability",
			usage:   "dnoticecap [target]",
			detail: []string{
				"With no target, reports whether this hub supports direct notices.",
				"With a target, also reports whether that client is connected here.",
			},
			run: (*commandContext).runDnoticeCap,
		},
		{
			name:    "dnoticeme",
			summary: "send yourself a direct NOTICE, to test the K_DST path",
			usage:   dnoticemeUsage,
			detail: []string{
				"Sends the text to yourself as a direct NOTICE: a live test of the",
				"private path, which no other command exercises.",
			},
			run: (*commandContext).runDnoticeme,
		},
		{
			name:    "weather",
			summary: "look up the weather for a place",
			usage:   weatherUsage,
			detail: []string{
				"A place is letters, digits, spaces, commas, periods, hyphens, or",
				"apostrophes — never a URL, which is what keeps the request on the",
				"operator's own provider.",
			},
			configHint: []string{
				"Needs weather_url in config.toml; the command says so when it is empty.",
			},
			configured: func(cfg *BotConfig) bool { return cfg.WeatherURL != "" },
			run:        (*commandContext).runWeather,
		},
		{
			name:    "wx",
			summary: "look up the weather for a place",
			usage:   weatherUsage,
			detail: []string{
				"Same command as weather.",
			},
			run: (*commandContext).runWeather,
		},
		{
			name:    "seen",
			summary: "report when a client last spoke in a joined room",
			usage:   seenUsage,
			detail: []string{
				"<nick|hash> is the client to look for.",
				"Reads the live room buffers and the saved history, so the answer",
				"survives a bot restart.",
			},
			run: (*commandContext).runSeen,
		},
		{
			name:    "catchup",
			summary: "summarize what was said in a room while you were away",
			usage:   catchupUsage,
			detail: []string{
				"<window> is a duration like 30m, 2h, or 1d; the default is 24h.",
				"<room> limits the digest to one room.",
			},
			run: (*commandContext).runCatchup,
		},
		{
			name:    "watch",
			summary: "tell me when a name or hash announces",
			usage:   watchUsage,
			detail: []string{
				"<filter> is a display name, an identity hash, or an announced name.",
				"<ttl> is how long to watch, like 30m or 2h; the default is 24h.",
				"At most 10 per client, 100 per bot, and never more than 7d.",
			},
			run: (*commandContext).runWatch,
		},
		{
			name:    "unwatch",
			summary: "stop watching for one or all of them",
			usage:   unwatchUsage,
			detail: []string{
				"<n> is the number from a watches row; all clears every watch you own.",
			},
			run: (*commandContext).runUnwatch,
		},
		{
			name:    "watches",
			summary: "list what I am watching for",
			usage:   watchesUsage,
			detail: []string{
				"Lists each watch with its filter and how long it has left.",
			},
			run: (*commandContext).runWatches,
		},
		{
			name:    "search",
			summary: "find where a term appeared in the rooms I have joined",
			usage:   searchUsage,
			detail: []string{
				"<term> is the text to find; a trailing #room or joined room name",
				"limits the search to that room.",
				"Reads the live room buffers and the saved history, newest first.",
			},
			run: (*commandContext).runSearch,
		},
		{
			name:    "path",
			summary: "report the Reticulum path to a peer's destinations",
			usage:   pathUsage,
			detail: []string{
				"<nick|hash> is a peer the bot knows.",
				"The answer names every destination the peer's identity publishes, with",
				"hops, next hop, interface, and path age.",
			},
			run: (*commandContext).runPath,
		},
		{
			name:    "launches",
			summary: "list upcoming or recent space launches",
			usage:   launchesUsage,
			detail: []string{
				"[upcoming|past] picks the window and [1-5] is how many to list.",
				"Answers are cached, because the provider allows only a few anonymous",
				"calls per hour.",
			},
			configHint: []string{
				"Needs launch_url in config.toml to enable it.",
			},
			configured: func(cfg *BotConfig) bool { return cfg.LaunchURL != "" },
			run:        (*commandContext).runLaunches,
		},
		{
			name:    "flight",
			summary: "where one flight is right now, by its flight number",
			usage:   flightUsage,
			detail: []string{
				"<number> is the number a passenger knows, like BA123; BA 123 and",
				"BA-123 work too.",
				"flight_route_url is optional and adds the airline, the airports, and",
				"the callsign.",
			},
			configHint: []string{
				"Needs flight_url (the live state) in config.toml to enable it.",
			},
			configured: func(cfg *BotConfig) bool { return cfg.FlightURL != "" },
			run:        (*commandContext).runFlight,
		},
		{
			name:    "msg",
			summary: "send an LXMF message to a peer, online or not",
			usage:   msgUsage,
			detail: []string{
				"<nick|hash> is the recipient and <text> is the message.",
				"The outcome arrives later as a direct NOTICE to you.",
			},
			configHint: []string{
				"Needs lxmf_enabled = true in config.toml; store-and-forward also needs",
				"lxmf_propagation_node.",
			},
			configured: func(cfg *BotConfig) bool { return cfg.LXMFEnabled },
			run:        (*commandContext).runMsg,
		},
		{
			name:    "lxmf",
			summary: "send an LXMF message to a peer, online or not",
			usage:   msgUsage,
			detail: []string{
				"Same command as msg.",
			},
			run: (*commandContext).runMsg,
		},
		{
			name:    "kjv",
			summary: "look up a Bible verse, or search the King James text",
			usage:   kjvUsage,
			detail: []string{
				"{nick} kjv jn3:16 — a reference; Psalm 23:1-6, ps23, and 1 jn 2 1 work too.",
				"{nick} kjv shepherd — every verse that contains the word.",
				"{nick} kjv love of god — verses with all those words;",
				`  {nick} kjv "the love of God" is a phrase, matched consecutively.`,
				"{nick} kjv love|charity, lov*, l?ve, l[ai]ve — OR and wildcards.",
				"{nick} kjv love -hate — exclude verses that contain hate.",
				"{nick} kjv love.*life — a regexp, matched across a whole verse.",
				"Answers label the whole verse: John 3:16: For God so loved the world, ...",
			},
			configHint: []string{
				"Needs kjv_txt_file in config.toml, pointing at a King James text file.",
			},
			configured: func(cfg *BotConfig) bool { return cfg.KJVTxtFile != "" },
			run:        (*commandContext).runKJV,
		},
		{
			name:    "members",
			summary: "list the clients in a room",
			usage:   "members [room]",
			detail: []string{
				"With no room, answers for the first joined room and names it, then",
				"mentions the others.",
			},
			run: (*commandContext).runMembers,
		},
		{
			name:    "rooms",
			summary: "list the rooms the bot has joined",
			detail: []string{
				"Lists the rooms the bot has joined on this hub.",
			},
			run: (*commandContext).runRooms,
		},
		{
			name:    "id",
			summary: "report the identity hash clients can use to address the bot",
			detail: []string{
				"Reports the bot's identity hash, its nick, and the @<hash-prefix>",
				"alias that works even where its nickname is taken.",
			},
			run: (*commandContext).runID,
		},
	}
}

// runHelp lists the commands, or explains the one that was named. An explanation
// is the summary line followed by the command's own detail, with {nick} replaced
// by the nick this bot really answers to, so the examples are usable as written.
func (c *commandContext) runHelp() []string {
	if c.Args == "" {
		return c.reg.helpListing()
	}
	name, _ := splitCommandLine(c.Args)
	cmd, ok := c.reg.byName[name]
	if !ok {
		return []string{unknownCommandLine(c.effectiveTriggerNick())}
	}
	line := cmd.name + " — " + cmd.summary
	if cmd.usage != "" {
		line += ". Usage: " + cmd.usage
	}
	lines := []string{line}
	nick := c.effectiveTriggerNick()
	for _, detail := range cmd.detail {
		lines = append(lines, strings.ReplaceAll(detail, "{nick}", nick))
	}
	// Configuration guidance is for the operator, not for the room: print it
	// only while the command's provider is actually unconfigured, so a user is
	// never told how to install a feature the operator already enabled.
	if len(cmd.configHint) > 0 && cmd.configured != nil {
		if cfg := c.reg.config(); cfg != nil && !cmd.configured(cfg) {
			for _, hint := range cmd.configHint {
				lines = append(lines, strings.ReplaceAll(hint, "{nick}", nick))
			}
		}
	}
	return lines
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
// bot reports hub-side scripts; gorrcbot loads none, so it reports the size of
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
	return []string{fmt.Sprintf("Direct NOTICE sent to %v", safeTarget(target))}
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
		pyBool(supported), safeTarget(target), pyBool(c.session().knowsPeer(target.HashHex)))}
}

// runWeather looks the place up with the configured provider. With no provider
// configured it says so instead of guessing, which is the honest answer.
func (c *commandContext) runWeather() []string {
	template := strings.TrimSpace(c.reg.bot.cfg.WeatherURL)
	if template == "" {
		return []string{weatherNotConfiguredLine}
	}
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + weatherUsage}
	}
	place, err := sanitizePlace(c.Args)
	if err != nil {
		return []string{weatherPlaceRejectedLine}
	}
	fetchURL, err := providerURL(template, place)
	if err != nil {
		// The operator's template is at fault. The detail goes to the log,
		// which only the operator reads, and never into the room.
		logf("weather: %v", err)
		return []string{weatherMisconfiguredLine}
	}
	nick := c.peerName(c.req.Msg.Src)
	line, err := c.reg.providerLine(fetchURL, c.req.Now)
	if err != nil {
		return []string{fmt.Sprintf(weatherFailedLine, nick)}
	}
	return []string{fmt.Sprintf("@%v %v", nick, line)}
}

// runSeen reports when a client last spoke, from the room buffers and the
// persisted history alike, so the answer survives a bot restart. This is the
// question a mesh client asks after being offline for hours.
//
// Private rows are skipped: a direct notice the client sent to the bot is filed
// in the room's buffer, and quoting one would publish a private command line —
// and any text it carried — to the whole room.
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
		for _, msg := range c.conversationRows(room) {
			if privateRow(msg) || hexString(msg.Src) != target.HashHex {
				continue
			}
			if best == nil || msg.Ts > best.Ts {
				best, bestRoom = msg, room
			}
		}
	}
	if best == nil {
		return []string{fmt.Sprintf("no messages from %v in the rooms I have joined", safeTarget(target))}
	}
	age := "an unknown time"
	if best.Ts > 0 {
		age = formatAge(c.req.Now.Sub(time.UnixMilli(best.Ts)))
	}
	// The quoted message and its nick are both peer-supplied: strip terminal
	// escapes and control characters before repeating them, or a room member
	// could paint every other member's terminal through the bot.
	text := safeEcho(best.Text, maxSeenTextBytes)
	if text == "" {
		text = "(a message with no readable text)"
	}
	return []string{fmt.Sprintf("%v last spoke %v ago in %v: %q",
		safeTarget(target), age, bestRoom, text)}
}

// runMembers lists the clients the hub reports in a room.
//
// The wording deliberately does NOT start with "members in ", which is the shape
// the hub's own /who reply uses (rrc/commands.go, handleWho). A client parses a
// NOTICE of that shape as protocol traffic rather than conversation: it replaces
// the client's member set for the room (rrc/hub.go, ParseWhoNotice and
// applyWhoReply) and, whenever the client has its periodic auto-/who outstanding,
// consumeWhoReply swallows it whole — so the answer was both invisible and able
// to corrupt the member list of every client in the room. Observed live: the bot
// logged "replied ... with 1 notice(s)" while neither of two clients displayed
// anything. TestMembersReplyIsNotProtocolTraffic guards the shape.
func (c *commandContext) runMembers() []string {
	room := normalizeRoom(c.Args)
	if room == "" {
		room = normalizeRoom(c.req.Room)
	}
	var also []string
	if room == "" {
		// A direct NOTICE carries no room, and the bot is usually in one, so
		// demanding a room name would be friction for no reason. Answer for the
		// first joined room — the answer names it, so there is nothing to guess —
		// and mention the others rather than pretending they do not exist.
		rooms := c.conn().JoinedRoomList()
		if len(rooms) == 0 {
			return []string{"I have not joined any room yet."}
		}
		sort.Strings(rooms)
		room = normalizeRoom(rooms[0])
		for _, other := range rooms[1:] {
			if name := normalizeRoom(other); name != "" {
				also = append(also, name)
			}
		}
	}
	members := c.conn().GetRoomMembers(room)
	if len(members) == 0 {
		return append([]string{fmt.Sprintf(
			"members of %v: (none reported; the hub only answers for rooms it tracks)", room)}, alsoLines(also)...)
	}
	parts := make([]string, 0, len(members))
	for _, member := range members {
		// A nick is chosen by its owner, so it is untrusted text that the bot
		// is about to publish under its own name: it gets the same hygiene as
		// any other echoed text.
		nick := safeEcho(member.Nick, maxEchoNickBytes)
		if nick == "" {
			nick = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%v (%v)", nick, shortHash(member.HashHex)))
	}
	sort.Strings(parts)
	return append([]string{fmt.Sprintf("members of %v: %v", room, strings.Join(parts, ", "))},
		alsoLines(also)...)
}

// alsoLines reports the other rooms the bot has joined, when there is more than
// one, so an answer that defaulted to a room never hides the rest.
func alsoLines(rooms []string) []string {
	if len(rooms) == 0 {
		return nil
	}
	return []string{"also joined: " + strings.Join(rooms, ", ")}
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

// normalizePeerToken trims a peer token and drops one leading "@" sigil, the
// sigil a room member types to address a peer. The rule lives in the rrc package
// beside the protocol it comes from, so the bot and the RRC client agree on what
// a token names.
func normalizePeerToken(token string) string {
	return rrc.NormalizePeerToken(token)
}

// resolveTarget resolves one peer token to a target, or returns the single reply
// line that explains why it could not.
func (c *commandContext) resolveTarget(token string) (rrc.PeerTarget, []string) {
	token = normalizePeerToken(token)
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
	token = normalizePeerToken(token)
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
			safeTarget(target))}
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
	case isError(err, rrc.ErrPeerNotFound):
		return "no such peer"
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

// botKJVTxtFile returns the configured King James text-file path, or "" when
// the operator has not set one. The kjv command reads it read-only.
func botKJVTxtFile(b *bot) string {
	if b == nil || b.cfg == nil {
		return ""
	}
	return b.cfg.KJVTxtFile
}

// historyStore returns the reader for the persisted room history of the session's
// hub. A bot with no configured storage directory gets a reader that finds
// nothing, so the commands that use it answer from the live buffers alone.
func (r *registry) historyStore(s *hubSession) *historyStore {
	if r.bot == nil || r.bot.cfg == nil || s == nil || s.conn == nil {
		return &historyStore{}
	}
	return newHistoryStore(r.bot.cfg.StorageDir, s.conn.HubAddressHex())
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

// httpFetch performs one provider lookup and returns the response body. The bot
// never reaches the network for any other command.
func httpFetch(url string) (string, error) {
	client := &http.Client{Timeout: weatherTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", &providerStatusError{code: resp.StatusCode, status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBodyBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Compile-time assertion that the registry satisfies the reply policy's seam:
// the responder calls exactly this method value.
var _ commandRunner = (*registry)(nil).Run
