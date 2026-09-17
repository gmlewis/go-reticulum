// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the msg command, and with it the bot's only way to reach a
// peer who is not connected to the hub they share. RRC is connection-oriented,
// so a mention of somebody who is offline is simply lost; LXMF is
// store-and-forward, so the same message can wait for them and be handed over
// when they come back.
//
// Four rules shape it. First, LXMF is opt-in: with lxmf_enabled = false no
// router is created at all — no job loop runs, no state is written under the
// storage directory — and the command answers with the line that says how to
// turn it on. Second, this command writes into somebody else's inbox, which no
// other command does, so it carries its own budget on top of the reply cooldown.
// Third, RRC is not LXMF: the target is an RRC peer, so a peer that has never
// announced cannot be reached at all, because there is no public key to encrypt
// to, and the command says exactly that instead of pretending. Fourth, the
// outcome is asynchronous, and "a propagation node accepted it" is not "the peer
// has it" — the asker is told which of the two happened, in those words.

package bot

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

const (
	// msgUsage is the usage line of the msg command and its lxmf alias.
	msgUsage = "msg <nick|hash> <text>"
	// lxmfNotConfiguredLine is the answer while the operator has not enabled
	// LXMF. Nothing is created and nothing is written until they do.
	lxmfNotConfiguredLine = "LXMF is not configured: set lxmf_enabled = true in config.toml"
	// lxmfUnavailableLine is the answer when LXMF is enabled but this bot has
	// no sender to queue through, which is a wiring fault rather than a
	// configuration one and is reported as such.
	lxmfUnavailableLine = "LXMF is enabled but unavailable: this bot has no LXMF router"
	// lxmfNoAnnounceLine is the answer for a peer whose identity cannot be
	// recalled. LXMF encrypts to a public key learned from an announce, so a
	// peer that has never announced anywhere is unreachable, and guessing would
	// only produce a message that encrypts to nothing.
	lxmfNoAnnounceLine = "that peer has never announced, so I cannot reach them over LXMF"
	// lxmfQueueFailedLine is the answer when the router refuses the message. It
	// never quotes the router's own error, which may name a peer or a path.
	lxmfQueueFailedLine = "lxmf: could not queue the message, try again later"
	// lxmfEmptyTextLine is the answer to a text with nothing readable left once
	// the terminal escapes and control characters are stripped, which would
	// otherwise write an empty message into somebody's inbox.
	lxmfEmptyTextLine = "msg: the text has nothing readable in it"
	// lxmfTextTooLongLine is the answer to a text over the cap. The number is
	// substituted from the cap itself, so the two can never drift apart.
	lxmfTextTooLongLine = "msg: the text is longer than %v bytes"
	// lxmfShortHashLine is the answer for a peer the hub knows only by a
	// partial hash: a shortened hash is not enough to derive the peer's
	// lxmf.delivery destination.
	lxmfShortHashLine = "I only know %v by a short hash prefix; use the full 32-character hash to reach them over LXMF"
	// lxmfDeliveryAspect is the LXMF aspect a delivery destination lives under.
	lxmfDeliveryAspect = "delivery"
	// lxmfTitle is the title every message this bot sends carries. An LXMF
	// message has a title and a content field, and a chat message belongs in
	// the content one.
	lxmfTitle = ""
)

// LXMF input limits and budgets.
const (
	// maxLXMFTextBytes bounds one message body. LXMF itself carries far more,
	// but a chat message is not a file transfer, and every byte here is written
	// into somebody else's inbox.
	maxLXMFTextBytes = 400
	// maxLXMFAskerPerMinute is how many messages one identity may queue in one
	// window.
	maxLXMFAskerPerMinute = 5
	// maxLXMFBotPerMinute bounds the whole bot whatever the number of askers,
	// which is the bound that matters when a room is busy.
	maxLXMFBotPerMinute = 20
	// lxmfBudgetWindow is the window both budgets are measured over.
	lxmfBudgetWindow = time.Minute
	// lxmfBudgetMaxAskers bounds the per-asker table, so a busy hub cannot grow
	// the bot's memory through this command.
	lxmfBudgetMaxAskers = 256
)

// lxmfDestinationHashLen is the length of an LXMF destination hash in bytes. A
// propagation node is addressed by its lxmf.propagation destination hash, so it
// is the same truncated hash every other Reticulum destination is.
const lxmfDestinationHashLen = rns.TruncatedHashLength / 8

// lxmfDestinationHexLen is lxmfDestinationHashLen in hexadecimal characters.
const lxmfDestinationHexLen = lxmfDestinationHashLen * 2

// Delivery errors and budget refusals. They are sentinels or pre-rendered lines
// so every failure class has exactly one wording and no attacker-chosen text can
// reach an answer.
var (
	// errLXMFPeerNeverAnnounced reports a peer whose identity the transport
	// cannot recall, which makes the lxmf.delivery destination underivable.
	errLXMFPeerNeverAnnounced = errors.New("the peer has never announced")
	// errLXMFEmptyText reports a text with nothing readable left.
	errLXMFEmptyText = errors.New("the text has no readable characters")
	// errLXMFAskerBudget is the per-asker rate limit.
	errLXMFAskerBudget = fmt.Errorf("you have sent %v LXMF messages in the last minute; try again shortly",
		maxLXMFAskerPerMinute)
	// errLXMFBotBudget is the bot-wide rate limit.
	errLXMFBotBudget = errors.New("this bot is writing too much LXMF right now; try again shortly")
	// errLXMFNoTransport reports lxmf_enabled with no Reticulum transport to
	// run a router on.
	errLXMFNoTransport = errors.New("lxmf_enabled is true but this bot has no Reticulum transport for an LXMF router")
	// errLXMFNoStorage reports lxmf_enabled with no storage directory. The
	// router refuses an empty path, so the bot says which key is missing
	// instead of starting without one.
	errLXMFNoStorage = errors.New("lxmf_enabled is true but storage_dir is empty, and the LXMF router needs a storage directory")
)

// lxmfMethod is the delivery method one message is queued with, in LXMF's own
// numbering: the bot and the router can then never disagree about what
// opportunistic, direct or propagated means.
type lxmfMethod int

const (
	// lxmfNone is "no method", which is what a send the router never accepted
	// reports.
	lxmfNone = lxmfMethod(0)
	// lxmfOpportunistic is one encrypted packet sent straight at the peer's
	// ratchet: no link and no handshake, the fastest and least certain route.
	lxmfOpportunistic = lxmfMethod(lxmf.MethodOpportunistic)
	// lxmfDirect is delivery over a link, which retransmits and returns a
	// proof of delivery.
	lxmfDirect = lxmfMethod(lxmf.MethodDirect)
	// lxmfPropagated hands the message to a propagation node, which holds it
	// until the peer collects it.
	lxmfPropagated = lxmfMethod(lxmf.MethodPropagated)
)

// label renders the method the way the confirmation line names it.
func (m lxmfMethod) label() string {
	switch m {
	case lxmfOpportunistic:
		return "opportunistic"
	case lxmfDirect:
		return "direct"
	case lxmfPropagated:
		return "propagated"
	default:
		return "unknown"
	}
}

// lxmfMethodFor chooses the delivery method for one message from what the
// transport knows now. A path and a ratchet allow the single opportunistic
// packet; a path alone needs a link; a propagation node is the only route to a
// peer with no path at all. With none of the three the bot still tries direct
// delivery, which fails visibly instead of silently doing nothing.
func lxmfMethodFor(hasPath, hasRatchet, hasPropagationNode bool) lxmfMethod {
	switch {
	case hasPath && hasRatchet:
		return lxmfOpportunistic
	case hasPath:
		return lxmfDirect
	case hasPropagationNode:
		return lxmfPropagated
	default:
		return lxmfDirect
	}
}

// lxmfAsk is who asked for one message and where the asynchronous outcome has to
// go: the asker's identity hash, the nick the hub knows them by, and the hub the
// ask arrived on, which is the only hub that can reach them again.
type lxmfAsk struct {
	// AskerHash is the asker's identity hash, the address of the direct NOTICE
	// that carries the outcome.
	AskerHash []byte
	// AskerNick is the asker's nick, for the bot's log only.
	AskerNick string
	// HubHex is the destination of the hub the ask arrived on.
	HubHex string
	// PeerName is the name the peer is reported by in both the confirmation and
	// the outcome: the nick the hub knows, else the hash prefix. It is never
	// empty, so no line can be left naming nobody.
	PeerName string
}

// lxmfOutcome is what became of one queued message, in LXMF's own terms.
type lxmfOutcome struct {
	// State is the LXMF state the message ended in: StateDelivered for a
	// proven delivery, StateSent for a message a propagation node accepted, and
	// StateFailed for one that ran out of attempts.
	State int
	// Method is the method the message ended up using, which is not always the
	// one it was queued with: the router falls back to propagated delivery when
	// a node is configured and direct delivery keeps failing.
	Method int
	// Attempts is how many delivery attempts were made.
	Attempts int
	// PropagationNodeHex is the node that accepted the message, hex, set when
	// the message was propagated.
	PropagationNodeHex string
}

// lxmfSend is one queued LXMF delivery.
type lxmfSend struct {
	// Ask is who asked and where the answer goes.
	Ask lxmfAsk
	// PeerHash is the recipient's 16-byte identity hash, from which their
	// lxmf.delivery destination is derived.
	PeerHash []byte
	// Text is the payload, already sanitized: one line, no escapes.
	Text string
	// OnResult receives the outcome once, on whichever goroutine the sender
	// learns it on. Implementations must never call it before Send returns.
	OnResult func(lxmfOutcome)
}

// lxmfSender queues one LXMF message for delivery and reports the method it will
// use. It is the seam the command layer queues through, so no unit test needs a
// real router or a network. Send must not block: the command layer runs on the
// engine's single dispatcher goroutine.
type lxmfSender interface {
	// Send queues one message and returns the method it was queued with.
	Send(req lxmfSend) (lxmfMethod, error)
	// Close releases the router behind the sender. It must be safe to call more
	// than once.
	Close() error
}

// lxmfBudget is the rate limit on this command, which writes into somebody
// else's inbox rather than into a room. Two sliding windows are kept: one per
// asker, so a single client cannot flood a peer, and one for the bot as a whole,
// so a room full of askers cannot either. The table of askers is bounded like
// every other table the bot keeps.
type lxmfBudget struct {
	mu     sync.Mutex
	asks   map[string][]time.Time
	order  []string
	global []time.Time
}

// newLXMFBudget builds an empty budget.
func newLXMFBudget() *lxmfBudget {
	return &lxmfBudget{asks: map[string][]time.Time{}}
}

// admit records one ask and reports whether it is within both budgets. The error
// it returns is user-facing: it says which budget was hit.
func (b *lxmfBudget) admit(askerHex string, now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := now.Add(-lxmfBudgetWindow)
	b.global = pruneTimes(b.global, cutoff)
	mine := pruneTimes(b.asks[askerHex], cutoff)
	switch {
	case len(mine) >= maxLXMFAskerPerMinute:
		b.asks[askerHex] = mine
		return errLXMFAskerBudget
	case len(b.global) >= maxLXMFBotPerMinute:
		b.asks[askerHex] = mine
		return errLXMFBotBudget
	}
	b.asks[askerHex] = append(mine, now)
	b.global = append(b.global, now)
	b.touchLocked(askerHex)
	return nil
}

// touchLocked records one asker in eviction order and drops the oldest asker's
// window when the table is full. The caller holds the lock.
func (b *lxmfBudget) touchLocked(askerHex string) {
	for i, key := range b.order {
		if key == askerHex {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
	b.order = append(b.order, askerHex)
	for len(b.order) > lxmfBudgetMaxAskers {
		delete(b.asks, b.order[0])
		b.order = b.order[1:]
	}
}

// pruneTimes drops the timestamps that are no longer inside the window ending
// at cutoff, keeping the rest in order. A timestamp exactly on the cutoff has
// aged out: the window is the whole minute before it, not the minute before it
// plus one instant.
func pruneTimes(times []time.Time, cutoff time.Time) []time.Time {
	kept := times[:0]
	for _, at := range times {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

// sanitizeLXMFText reduces one message body to what this bot is willing to write
// into somebody else's inbox: terminal escapes are removed whole, control and
// format characters are dropped, runs of whitespace collapse to a single space
// so the payload is one line however it was typed, and the ends are trimmed. It
// reports errLXMFEmptyText when nothing readable is left, and it never shortens
// the text: an over-long message is refused by the caller rather than silently
// cut.
func sanitizeLXMFText(text string) (string, error) {
	var (
		out     strings.Builder
		pending bool
	)
	for _, r := range stripEscapes(text) {
		if unicode.IsSpace(r) {
			pending = out.Len() > 0
			continue
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			continue
		}
		if pending {
			out.WriteByte(' ')
			pending = false
		}
		out.WriteRune(r)
	}
	cleaned := out.String()
	if cleaned == "" {
		return "", errLXMFEmptyText
	}
	return cleaned, nil
}

// runMsg queues one LXMF message for a peer. It answers immediately with the
// method the message was queued with; the outcome arrives later as a direct
// NOTICE to the asker, because no command may wait inside the dispatcher.
func (c *commandContext) runMsg() []string {
	if !c.reg.lxmfEnabled() {
		return []string{lxmfNotConfiguredLine}
	}
	if c.reg.lxmf == nil {
		return []string{lxmfUnavailableLine}
	}
	token, text := splitCommandLine(c.Args)
	if token == "" || strings.TrimSpace(text) == "" {
		return []string{"Usage: " + msgUsage}
	}
	target, rejected := c.resolveTarget(token)
	if rejected != nil {
		return rejected
	}
	if len(target.Hash) != rrc.IdentityHashLen {
		return []string{fmt.Sprintf(lxmfShortHashLine, safeTarget(target))}
	}
	payload, err := sanitizeLXMFText(text)
	if err != nil {
		return []string{lxmfEmptyTextLine}
	}
	if len(payload) > maxLXMFTextBytes {
		return []string{fmt.Sprintf(lxmfTextTooLongLine, maxLXMFTextBytes)}
	}
	if err := c.reg.lxmfBudget.admit(hexString(c.req.Msg.Src), c.now()); err != nil {
		return []string{err.Error()}
	}
	ask := lxmfAsk{
		AskerHash: c.req.Msg.Src,
		AskerNick: safeEcho(c.peerName(c.req.Msg.Src), maxEchoNickBytes),
		HubHex:    c.conn().HubAddressHex(),
		PeerName:  lxmfPeerName(target),
	}
	method, err := c.reg.lxmf.Send(lxmfSend{
		Ask:      ask,
		PeerHash: target.Hash,
		Text:     payload,
		OnResult: func(o lxmfOutcome) { c.reg.noticeLXMFOutcome(ask, o) },
	})
	if err != nil {
		if isError(err, errLXMFPeerNeverAnnounced) {
			return []string{lxmfNoAnnounceLine}
		}
		// The detail goes to the log, which only the operator reads.
		logf("lxmf: could not queue a message to %v: %v", ask.PeerName, err)
		return []string{lxmfQueueFailedLine}
	}
	return []string{lxmfQueuedLine(ask.PeerName, target.HashHex, method)}
}

// lxmfPeerName reports the name a message's peer is reported by: the nick the
// hub knows, sanitized so a chosen nick cannot repaint the terminal reading the
// answer, else the hash prefix. It is never empty, because every line this
// command produces has to name somebody.
func lxmfPeerName(target rrc.PeerTarget) string {
	if nick := safeEcho(target.Nick, maxEchoNickBytes); nick != "" {
		return nick
	}
	return shortHash(target.HashHex)
}

// lxmfQueuedLine renders the one line that confirms a queued message: who it is
// for, which client that is, and how it is going. A peer the hub knows no nick
// for is named by the hash prefix alone rather than twice over.
func lxmfQueuedLine(name, hashHex string, method lxmfMethod) string {
	prefix := shortHash(hashHex) + "…"
	if name == "" || name == shortHash(hashHex) {
		return fmt.Sprintf("queued for LXMF delivery to %v via %v delivery", prefix, method.label())
	}
	return fmt.Sprintf("queued for LXMF delivery to %v (%v) via %v delivery", name, prefix, method.label())
}

// lxmfOutcomeLine renders the asynchronous answer the asker receives. A
// propagated message is reported as accepted by the node and never as delivered:
// StateSent proves only that a node took it, and a bot that blurred the two
// would be telling its asker something it cannot know.
func lxmfOutcomeLine(ask lxmfAsk, o lxmfOutcome) string {
	switch o.State {
	case lxmf.StateDelivered:
		return "lxmf: delivered to " + ask.PeerName
	case lxmf.StateSent:
		if o.Method == int(lxmfPropagated) {
			return fmt.Sprintf("lxmf: accepted by propagation node %v… (store-and-forward)",
				shortHash(o.PropagationNodeHex))
		}
		return "lxmf: sent to " + ask.PeerName + "; no delivery proof yet"
	case lxmf.StateFailed:
		return fmt.Sprintf("lxmf: failed after %v", pluralCount(max(o.Attempts, 1), "attempt", "attempts"))
	default:
		return fmt.Sprintf("lxmf: the delivery outcome is unknown (state %v)", o.State)
	}
}

// noticeLXMFOutcome hands one asynchronous outcome to the bot, which owns the
// sessions the notice has to travel through. A registry with no bot — a bare
// unit-test wiring — logs it rather than failing.
func (r *registry) noticeLXMFOutcome(ask lxmfAsk, o lxmfOutcome) {
	if r.bot == nil {
		logf("lxmf: %v", lxmfOutcomeLine(ask, o))
		return
	}
	r.bot.noticeLXMFOutcome(ask, o)
}

// lxmfEnabled reports whether the operator has switched LXMF on. It is the one
// place "does this bot do LXMF at all" is answered.
func (r *registry) lxmfEnabled() bool {
	return r.bot != nil && r.bot.cfg != nil && r.bot.cfg.LXMFEnabled
}

// noticeLXMFOutcome sends the outcome of one queued message back to the asker as
// a direct NOTICE, through the hub their ask arrived on: that is the only hub
// that can reach them. An outcome that cannot be delivered is logged rather than
// reported anywhere else, because there is nowhere else for it to go.
func (b *bot) noticeLXMFOutcome(ask lxmfAsk, o lxmfOutcome) {
	text := lxmfOutcomeLine(ask, o)
	if len(ask.AskerHash) != rrc.IdentityHashLen {
		b.logf("lxmf: %v (the asker has no identity hash to tell)", text)
		return
	}
	for _, s := range b.sessions {
		if s.conn.HubAddressHex() != ask.HubHex {
			continue
		}
		if err := s.conn.SendDirectNotice(ask.AskerHash, text); err != nil {
			b.logf("lxmf: cannot report %q to %v: %v", text, ask.AskerNick, err)
		}
		return
	}
	b.logf("lxmf: no session for hub %v to report %q on", ask.HubHex, text)
}

// lxmfDeliveryOpener opens one LXMF delivery sender over a live transport. It is
// a seam so the startup rule — no router at all when lxmf_enabled is false — is
// testable without a Reticulum stack.
type lxmfDeliveryOpener func(ts rns.Transport, identity *rns.Identity, cfg *BotConfig) (lxmfSender, error)

// openLXMFDelivery returns the LXMF sender a configuration asks for: a live one
// when lxmf_enabled is true, and nothing at all when it is false. With LXMF off
// the opener is never called, so no router, no job loop and no state under the
// storage directory can come into existence.
func openLXMFDelivery(cfg *BotConfig, ts rns.Transport, identity *rns.Identity, open lxmfDeliveryOpener) (lxmfSender, error) {
	if cfg == nil || !cfg.LXMFEnabled {
		return nil, nil
	}
	if ts == nil || identity == nil {
		return nil, errLXMFNoTransport
	}
	if strings.TrimSpace(cfg.StorageDir) == "" {
		return nil, errLXMFNoStorage
	}
	if open == nil {
		open = openLiveLXMFDelivery
	}
	return open(ts, identity, cfg)
}

// liveLXMFDelivery is the real sender: one LXMF router over the bot's own
// Reticulum transport, with its own storage and its own announce schedule.
type liveLXMFDelivery struct {
	ts     rns.Transport
	router *lxmf.Router
	// source is the delivery destination messages are sent from, which is also
	// what makes the bot reachable for a reply.
	source   *rns.Destination
	destHash []byte
	// pnHash is the configured propagation node, nil when there is none. The
	// router never discovers one by itself, so without it there is no
	// store-and-forward at all.
	pnHash []byte
	// announceEvery is how often the delivery destination is re-announced.
	announceEvery time.Duration

	stop      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
	logf      func(format string, args ...any)
}

// openLiveLXMFDelivery builds the real sender: the router, the delivery
// destination outbound messages come from, the optional propagation node, and
// the announce loop that keeps the bot reachable. The router's own storage is
// <storage_dir>/lxmf, which NewRouter names itself.
func openLiveLXMFDelivery(ts rns.Transport, identity *rns.Identity, cfg *BotConfig) (lxmfSender, error) {
	router, err := lxmf.NewRouter(ts, identity, cfg.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("starting the LXMF router: %w", err)
	}
	source, err := router.RegisterDeliveryIdentity(identity, cfg.Nick, nil)
	if err != nil {
		_ = router.Close()
		return nil, fmt.Errorf("registering the bot's LXMF delivery identity: %w", err)
	}
	d := &liveLXMFDelivery{
		ts:            ts,
		router:        router,
		source:        source,
		destHash:      source.Hash,
		announceEvery: time.Duration(cfg.LXMFAnnounceMinutes) * time.Minute,
		stop:          make(chan struct{}),
		logf:          logf,
	}
	if cfg.LXMFPropagationNode != "" {
		d.pnHash = cfg.LXMFPropagationNodeHash
		if err := router.SetOutboundPropagationNode(d.pnHash); err != nil {
			_ = router.Close()
			return nil, fmt.Errorf("configuring propagation node %v: %w", cfg.LXMFPropagationNode, err)
		}
	}
	// An inbound message is logged and nothing else: relaying it into a room
	// would publish a private message to everybody present, and this bot has no
	// conversation store to keep it in.
	router.RegisterDeliveryCallback(func(m *lxmf.Message) {
		d.logf("lxmf: received a message from %v", shortHash(hexString(m.SourceHash)))
	})
	d.startAnnounceLoop()
	return d, nil
}

// startAnnounceLoop announces the bot's delivery destination once and then on
// the configured interval, so a peer can learn where to send a reply. Without an
// announce the bot can still send, but nobody can answer it.
func (d *liveLXMFDelivery) startAnnounceLoop() {
	if d.announceEvery <= 0 {
		return
	}
	d.wg.Go(func() {
		ticker := time.NewTicker(d.announceEvery)
		defer ticker.Stop()
		d.announce()
		for {
			select {
			case <-d.stop:
				return
			case <-ticker.C:
				d.announce()
			}
		}
	})
}

// announce broadcasts the bot's delivery destination.
func (d *liveLXMFDelivery) announce() {
	if err := d.router.Announce(d.destHash); err != nil {
		d.logf("lxmf: could not announce the delivery destination: %v", err)
	}
}

// Send queues one message. It resolves the peer's identity, derives their
// lxmf.delivery destination, chooses the method from what the transport knows
// now, and hands the queueing itself to a goroutine of its own: the router packs
// the message and may open a link, and the command layer runs on the engine's
// single dispatcher, which must never wait.
func (d *liveLXMFDelivery) Send(req lxmfSend) (lxmfMethod, error) {
	if len(req.PeerHash) != rrc.IdentityHashLen {
		return lxmfNone, errLXMFPeerNeverAnnounced
	}
	identity := rns.RecallIdentity(d.ts, req.PeerHash)
	if identity == nil {
		return lxmfNone, errLXMFPeerNeverAnnounced
	}
	dest, err := rns.NewDestination(d.ts, identity, rns.DestinationOut, rns.DestinationSingle,
		lxmf.AppName, lxmfDeliveryAspect)
	if err != nil {
		return lxmfNone, fmt.Errorf("deriving the peer's lxmf.delivery destination: %w", err)
	}
	method := lxmfMethodFor(d.ts.HasPath(dest.Hash), len(d.ts.GetRatchet(dest.Hash)) > 0, len(d.pnHash) > 0)
	d.wg.Go(func() { d.queue(req, dest, method) })
	return method, nil
}

// queue constructs one message and hands it to the router. Its failure is logged
// and reported to the asker, because the confirmation line has already been sent
// by the time this runs.
func (d *liveLXMFDelivery) queue(req lxmfSend, dest *rns.Destination, method lxmfMethod) {
	var once sync.Once
	report := func(o lxmfOutcome) {
		if req.OnResult == nil {
			return
		}
		once.Do(func() { req.OnResult(o) })
	}
	message, err := lxmf.NewMessage(dest, d.source, req.Text, lxmfTitle, nil)
	if err != nil {
		d.logf("lxmf: could not compose a message to %v: %v", req.Ask.PeerName, err)
		report(lxmfOutcome{State: lxmf.StateFailed, Method: int(method)})
		return
	}
	message.DesiredMethod = int(method)
	// TryPropagationOnFail is set only when a node is configured: without one
	// the router fails the message outright instead of retrying it there.
	message.TryPropagationOnFail = len(d.pnHash) > 0
	message.DeliveryCallback = func(m *lxmf.Message) { report(d.outcome(m)) }
	message.FailedCallback = func(m *lxmf.Message) { report(d.outcome(m)) }
	if err := d.router.HandleOutbound(message); err != nil {
		d.logf("lxmf: the router refused a message to %v: %v", req.Ask.PeerName, err)
		report(d.outcome(message))
	}
}

// outcome reads what became of one message in LXMF's own terms.
func (d *liveLXMFDelivery) outcome(m *lxmf.Message) lxmfOutcome {
	o := lxmfOutcome{State: m.State(), Method: m.Method(), Attempts: m.DeliveryAttempts}
	if o.Method == int(lxmfPropagated) {
		o.PropagationNodeHex = hexString(d.pnHash)
	}
	return o
}

// Close stops the announce loop and releases the router. It is idempotent, so
// both the shutdown path and a second caller can use it.
func (d *liveLXMFDelivery) Close() error {
	d.closeOnce.Do(func() {
		close(d.stop)
		d.wg.Wait()
		d.closeErr = d.router.Close()
	})
	if d.closeErr != nil {
		return fmt.Errorf("closing the LXMF router: %w", d.closeErr)
	}
	return nil
}

// Compile-time assertion that the production sender is what the command layer
// queues through.
var _ lxmfSender = (*liveLXMFDelivery)(nil)
