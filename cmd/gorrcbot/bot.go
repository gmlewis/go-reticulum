// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the gorrbot engine: it dials every configured hub, keeps the
// rooms joined, drains inbound messages on a single dispatcher goroutine, and
// shuts down in bounded time. The command and reply policy lives behind
// botHooks, so the engine has no knowledge of commands.

package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// Engine timing. The poll is a safety net: an inbound message wakes the
// supervisor immediately, so the poll only matters when a hub changes state
// without sending anything.
const (
	// defaultStatusPoll is how often each hub's connection status is checked.
	defaultStatusPoll = 250 * time.Millisecond
	// defaultShutdownGrace bounds how long Run waits for the supervisors and
	// the dispatcher to finish after the context is cancelled.
	defaultShutdownGrace = 2 * time.Second
	// inboundQueueDepth bounds the inbound backlog. A full queue drops the
	// newest message and counts it rather than blocking the hub's link
	// goroutine, which would stall the whole connection.
	inboundQueueDepth = 64
)

// botHooks are the policy callbacks the engine invokes. Keeping them explicit
// leaves the engine free of command-layer knowledge, and lets tests observe
// every step in isolation.
type botHooks struct {
	// Greeting returns the one-line self-introduction to post in a room right
	// after the hub confirms the JOIN, or "" to stay silent.
	Greeting func(s *hubSession, room string) string
	// Inbound handles one message read off a hub connection.
	Inbound func(s *hubSession, msg *rrc.RRCMessage)
}

// inbound is one queued message together with the hub it arrived on.
type inbound struct {
	session *hubSession
	msg     *rrc.RRCMessage
}

// bot is the running gorrbot.
type bot struct {
	cfg    *BotConfig
	paths  BotPaths
	logger *rns.Logger
	dialer hubDialer
	hooks  botHooks

	// ownHash is the bot's identity hash. Every message carrying it is the
	// bot's own composition, including the hub's fanout echo of it, and never
	// triggers a command.
	ownHash []byte

	// statusPoll and shutdownGrace are timing knobs; tests shorten both so no
	// unit test ever waits on a real delay.
	statusPoll    time.Duration
	shutdownGrace time.Duration

	sessions []*hubSession

	msgs      chan inbound
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	dropped   atomic.Int64
	startedAt time.Time

	// hubErrMu guards a dial failure recorded during start-up.
	hubErrMu sync.Mutex
	hubErr   error
}

// newBot builds the engine for one configuration. dialer and hooks are
// injected so the engine can be exercised without a Reticulum stack.
func newBot(cfg *BotConfig, paths BotPaths, logger *rns.Logger, ownHash []byte, dialer hubDialer, hooks botHooks) *bot {
	return &bot{
		cfg:           cfg,
		paths:         paths,
		logger:        logger,
		ownHash:       ownHash,
		dialer:        dialer,
		hooks:         hooks,
		statusPoll:    defaultStatusPoll,
		shutdownGrace: defaultShutdownGrace,
		msgs:          make(chan inbound, inboundQueueDepth),
		stopCh:        make(chan struct{}),
	}
}

// logf writes one operator-facing line. The Reticulum logger is async and
// carries the protocol chatter; the bot's own diagnostics go to stderr through
// the standard logger so they are never interleaved with link internals.
func (b *bot) logf(format string, args ...any) {
	log.Printf("gorrcbot: "+format, args...)
}

// logf is the package-level form used before a bot exists.
func logf(format string, args ...any) {
	log.Printf("gorrcbot: "+format, args...)
}

// StartedAt reports when Run started, which is what uptime is measured from.
func (b *bot) StartedAt() time.Time { return b.startedAt }

// uptime reports how long the engine has been running.
func (b *bot) uptime() time.Duration {
	if b.startedAt.IsZero() {
		return 0
	}
	return time.Since(b.startedAt)
}

// greeting returns the one-line self-introduction to post in a room.
func greeting(s *hubSession, room string) string {
	nick := s.bot.cfg.AdvertisedNick(s.cfg)
	return fmt.Sprintf("I am %v, an RRC bot. Address me by name: @%v help", nick, nick)
}

// DroppedInbound reports how many inbound messages were dropped because the
// dispatcher could not keep up.
func (b *bot) DroppedInbound() int64 { return b.dropped.Load() }

// Run dials every configured hub and serves until ctx is cancelled, then shuts
// down in bounded time and returns. Every callback is registered before the hub
// is asked to connect, so no message can be missed, and every side effect is
// torn down on the way out.
func (b *bot) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	b.startedAt = time.Now()

	for i := range b.cfg.Hubs {
		if err := ctx.Err(); err != nil {
			break
		}
		if err := b.addHub(&b.cfg.Hubs[i]); err != nil {
			b.setHubErr(err)
			break
		}
	}
	if err := b.hubError(); err != nil {
		b.shutdown()
		return err
	}

	// The dispatcher owns the command layer; the supervisors own the per-hub
	// connection state. Both stop when stopCh closes.
	b.wg.Go(b.dispatch)
	for _, s := range b.sessions {
		b.wg.Go(s.supervise)
	}
	// Announce the running configuration once, then connect.
	names := make([]string, 0, len(b.sessions))
	for _, s := range b.sessions {
		names = append(names, s.cfg.Name)
	}
	b.logf("listening on %v hub(s): %v (identity %v)", len(names), names, hex.EncodeToString(b.ownHash))

	for _, s := range b.sessions {
		s.conn.ConnectAsync()
	}

	<-ctx.Done()
	b.shutdown()
	return nil
}

// addHub creates one hub connection and registers every callback before the
// connection is allowed to reach the network, so no message and no link event
// can be missed.
func (b *bot) addHub(cfg *HubConfig) error {
	conn, err := b.dialer.Dial(cfg)
	if err != nil {
		return fmt.Errorf("hub %q: %w", cfg.Name, err)
	}
	s := newHubSession(b, cfg, conn)
	// Auto-reconnect keeps an always-on bot connected without a supervisor of
	// its own. The automatic /list and /who sweeps stay off: the bot only needs
	// them when a command asks for them.
	conn.SetAutoReconnect(true, false)
	conn.SetAutoList(false, false)
	conn.SetAutoWho(false, false)
	if cfg.Nick != "" {
		conn.SetNickOverride(cfg.Nick)
	}
	conn.SetOnLinkEstablished(s.onLinkEstablished)
	conn.SetOnMessage(func(msg *rrc.RRCMessage) { b.enqueue(s, msg) })
	b.sessions = append(b.sessions, s)
	return nil
}

// enqueue hands one inbound message to the dispatcher. It never blocks: it runs
// on the hub's link goroutine, where a slow command would stall the whole
// connection.
func (b *bot) enqueue(s *hubSession, msg *rrc.RRCMessage) {
	select {
	case <-b.stopCh:
		return
	default:
	}
	// A JOINED confirmation is what makes a room's greeting safe to send, so
	// the session records it before the command layer sees the message.
	s.observeJoin(msg)
	select {
	case b.msgs <- inbound{session: s, msg: msg}:
	default:
		if n := b.dropped.Add(1); n == 1 {
			b.logf("inbound queue full; dropping messages (depth %v)", inboundQueueDepth)
		}
	}
}

// dispatch is the single goroutine that runs the command layer. On shutdown it
// finishes the message it is holding and then drains everything already queued,
// so a reply for a message that arrived before the signal is still sent.
func (b *bot) dispatch() {
	for {
		select {
		case in := <-b.msgs:
			b.handle(in)
		case <-b.stopCh:
			for {
				select {
				case in := <-b.msgs:
					b.handle(in)
				default:
					return
				}
			}
		}
	}
}

// handle runs one inbound message through the command layer, recovering from a
// panic so one malformed message cannot take the bot down.
func (b *bot) handle(in inbound) {
	defer func() {
		if r := recover(); r != nil {
			b.logf("recovered while handling a message on hub %q: %v", in.session.cfg.Name, r)
		}
	}()
	if b.hooks.Inbound == nil {
		return
	}
	b.hooks.Inbound(in.session, in.msg)
}

// shutdown stops every goroutine and releases the connections. It is idempotent
// and always returns, even when a hub's link goroutine is stuck.
func (b *bot) shutdown() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
		// Detach every inbound hook first so nothing new is queued while the
		// dispatcher drains what is already in flight.
		for _, s := range b.sessions {
			s.conn.SetOnMessage(nil)
		}
		done := make(chan struct{})
		go func() {
			b.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(b.shutdownGrace):
			b.logf("shutdown did not finish within %v; disconnecting anyway", b.shutdownGrace)
		}
		b.dialer.Close()
		if n := b.dropped.Load(); n > 0 {
			b.logf("dropped %v inbound message(s) over this run", n)
		}
	})
}

// setHubErr records the first dial failure.
func (b *bot) setHubErr(err error) {
	b.hubErrMu.Lock()
	if b.hubErr == nil {
		b.hubErr = err
	}
	b.hubErrMu.Unlock()
}

// hubError returns the first dial failure, if any.
func (b *bot) hubError() error {
	b.hubErrMu.Lock()
	defer b.hubErrMu.Unlock()
	return b.hubErr
}
