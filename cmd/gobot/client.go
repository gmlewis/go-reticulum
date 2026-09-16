// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the RRC exchange: connect to the hub, join a room so the bot
// can see this identity, send one private command, and gather the reply lines
// the bot sends back. The hub connection is behind hubConn, so the whole
// exchange is unit-testable without a Reticulum stack.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

// pollInterval is how often the connection and membership state is re-read
// while waiting. It is short enough that a live hub is never visibly waited on
// and long enough that polling costs nothing.
const pollInterval = 50 * time.Millisecond

// ErrNoReply reports that the bot never answered. It is a sentinel so main can
// map "the bot stayed silent" onto a distinct exit without matching on text.
var ErrNoReply = errors.New("the bot did not reply")

// hubConn is the RRC client surface this tool drives. *rrc.RRCHub implements it;
// tests substitute a fake so the orchestration is exercised without a network.
type hubConn interface {
	SetOnMessage(fn func(*rrc.RRCMessage))
	SetAutoReconnect(enabled, save bool)
	SetAutoList(enabled, save bool)
	SetAutoWho(enabled, save bool)
	SetNickOverride(nick string)
	ConnectAsync()
	Disconnect()
	GetHubStatus() int
	GetStatusText() string
	JoinRoom(room string, silent bool)
	GetRoomMembers(room string) []rrc.RoomMemberInfo
	ResolvePeerToken(token string) (rrc.PeerTarget, error)
	SendPrivateCommand(text string) error
	SendDirectNotice(peerHash []byte, text string) error
}

// runRequest drives one private request to the bot and returns the reply lines.
// It registers the collector before asking the connection to connect, so no
// reply can arrive before the tool is listening for it.
func runRequest(ctx context.Context, conn hubConn, opts *options, ownHash []byte) ([]string, error) {
	collector := newReplyCollector(opts.target, hexString(ownHash))
	conn.SetOnMessage(collector.add)
	// A one-shot tool must not reconnect, list rooms, or sweep membership on
	// its own timers: the run is over before any of that could help.
	conn.SetAutoReconnect(false, false)
	conn.SetAutoList(false, false)
	conn.SetAutoWho(false, false)
	if strings.TrimSpace(opts.nick) != "" {
		conn.SetNickOverride(opts.nick)
	}
	conn.ConnectAsync()
	defer conn.Disconnect()

	if err := waitForWelcome(ctx, conn); err != nil {
		return nil, err
	}
	// The bot only delivers a direct reply to a peer it has seen in a room it
	// joined, so the room membership is what makes the answer possible at all.
	conn.JoinRoom(opts.room, true)
	if err := waitForJoin(ctx, conn, opts.room, ownHash); err != nil {
		return nil, err
	}
	// Knowing the bot's hash up front lets a stray private message from
	// somebody else be ignored. It is best-effort: the hub's member list may
	// not have arrived yet, and the nick carried on the reply is the fallback.
	if target, err := conn.ResolvePeerToken(opts.target); err == nil && target.Resolvable() {
		collector.setTargetHash(target.HashHex)
	}
	if err := sendRequest(conn, opts); err != nil {
		return nil, err
	}
	return collector.collect(ctx, opts.quiet)
}

// waitForWelcome blocks until the hub has welcomed the session, or fails.
func waitForWelcome(ctx context.Context, conn hubConn) error {
	for {
		switch conn.GetHubStatus() {
		case rrc.StatusConnected:
			return nil
		case rrc.StatusFailed:
			return fmt.Errorf("could not connect to the hub: %v", statusText(conn))
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return fmt.Errorf("timed out waiting for the hub to connect: %w", err)
		}
	}
}

// waitForJoin blocks until the hub's JOINED fanout has put this client's own
// identity hash in the room's member set. The member set is the only
// confirmation the client has: JoinRoom itself returns immediately, so a check
// straight after it would ask the hub for a room the hub has not answered for
// yet.
func waitForJoin(ctx context.Context, conn hubConn, room string, ownHash []byte) error {
	want := hexString(ownHash)
	for {
		for _, member := range conn.GetRoomMembers(room) {
			if strings.EqualFold(member.HashHex, want) {
				return nil
			}
		}
		if conn.GetHubStatus() == rrc.StatusFailed {
			return fmt.Errorf("the hub connection failed while joining %q: %v",
				room, statusText(conn))
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return fmt.Errorf("timed out joining room %q: %w", room, err)
		}
	}
}

// sendRequest asks the hub to deliver one private command to the bot. The
// command is the exact "/msg <nick> <text>" line a human would type, so the
// hub's own target resolution decides where it goes.
func sendRequest(conn hubConn, opts *options) error {
	line := "/dnotice " + opts.target + " " + opts.message
	err := conn.SendPrivateCommand(line)
	if err == nil {
		return nil
	}
	// A hub without the private-command extension cannot read a command
	// addressed to itself, but it may still forward a direct notice; fall back
	// to the bot's identity hash when it can be resolved.
	if errors.Is(err, rrc.ErrPrivateCommandsUnsupported) {
		if target, resolveErr := conn.ResolvePeerToken(opts.target); resolveErr == nil && target.Resolvable() {
			return conn.SendDirectNotice(target.Hash, opts.message)
		}
	}
	return fmt.Errorf("could not send the request to %q: %w", opts.target, err)
}

// replyCollector gathers the direct notices the target bot sends back. It is
// the client's message hook, so every field it reads is copied or used
// immediately and no live client state is retained.
type replyCollector struct {
	mu         sync.Mutex
	ownHash    string
	targetNick string
	targetHash string
	lines      []string
	ready      chan struct{}
}

// newReplyCollector builds a collector for one target, keyed by the caller's own
// identity hash so the caller's own echoed traffic is never mistaken for a
// reply.
func newReplyCollector(targetNick, ownHash string) *replyCollector {
	return &replyCollector{
		ownHash:    strings.ToLower(ownHash),
		targetNick: strings.TrimSpace(targetNick),
		ready:      make(chan struct{}, 1),
	}
}

// setTargetHash teaches the collector the bot's identity hash, which makes the
// filter exact instead of nick-based.
func (c *replyCollector) setTargetHash(hashHex string) {
	c.mu.Lock()
	c.targetHash = strings.ToLower(strings.TrimSpace(hashHex))
	c.mu.Unlock()
}

// add is the client's message hook. It keeps only a direct notice from the
// target, and never blocks the link goroutine that calls it.
func (c *replyCollector) add(msg *rrc.RRCMessage) {
	if !c.accepts(msg) {
		return
	}
	c.mu.Lock()
	c.lines = append(c.lines, msg.Text)
	c.mu.Unlock()
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

// accepts reports whether one inbound message is a reply from the target.
func (c *replyCollector) accepts(msg *rrc.RRCMessage) bool {
	if msg == nil || !msg.Direct {
		return false
	}
	src := strings.ToLower(hexString(msg.Src))
	if src == "" || src == c.ownHash {
		return false
	}
	c.mu.Lock()
	targetHash := c.targetHash
	c.mu.Unlock()
	if targetHash != "" {
		return src == targetHash
	}
	// Without a resolved hash the advisory nick the hub attaches to the
	// forwarded notice is the next best filter; when the hub sent none, any
	// direct notice that is not this client's own is the only evidence there is.
	if c.targetNick != "" && msg.Nick != "" {
		return strings.EqualFold(msg.Nick, c.targetNick)
	}
	return true
}

// collect waits for the first reply line and then keeps reading until the stream
// has been silent for quiet. A run that receives nothing before the deadline
// fails with ErrNoReply; a stream cut short by the deadline returns what
// arrived.
func (c *replyCollector) collect(ctx context.Context, quiet time.Duration) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %v did not answer before the deadline (is it connected to the hub?)",
			ErrNoReply, c.targetName())
	case <-c.ready:
	}
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return c.snapshot(), nil
		case <-c.ready:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quiet)
		case <-timer.C:
			return c.snapshot(), nil
		}
	}
}

// snapshot returns a copy of the reply lines, so the caller owns them.
func (c *replyCollector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

// targetName renders the target for a diagnostic.
func (c *replyCollector) targetName() string {
	if c.targetNick != "" {
		return "@" + c.targetNick
	}
	return "the bot"
}

// statusText returns the client's own status text, or a placeholder when the
// connection offers none.
func statusText(conn hubConn) string {
	if text := strings.TrimSpace(conn.GetStatusText()); text != "" {
		return text
	}
	return "unknown error"
}

// sleepCtx waits for d, returning the context error when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// hexString renders bytes as lowercase hexadecimal.
func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	if len(b) == 0 {
		return ""
	}
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0x0f])
	}
	return string(out)
}

// compile-time assertion: the production client is what the exchange expects.
var _ hubConn = (*rrc.RRCHub)(nil)
