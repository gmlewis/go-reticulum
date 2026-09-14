// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the bot's seam onto the RRC client stack: the hubConn
// interface the engine drives, the per-hub session state, and the production
// dialer built on rrc.RRCManager. The engine never talks to a live hub
// directly, so its dial/join/observe/shutdown behaviour is unit-testable
// without a Reticulum stack.

package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// hubConn is the rrc.RRCHub surface the bot uses. *rrc.RRCHub implements it.
type hubConn interface {
	SetOnMessage(fn func(*rrc.RRCMessage))
	SetOnLinkEstablished(fn func())
	SetAutoReconnect(enabled, save bool)
	SetAutoList(enabled, save bool)
	SetAutoWho(enabled, save bool)
	SetNickOverride(nick string)
	ConnectAsync()
	Disconnect()

	GetHubStatus() int
	GetServerName() string
	GetHubVersion() string
	HubAddressHex() string
	GetEffectiveNick() string
	GetMOTD() string
	HasCapability(capability int) bool

	JoinRoom(room string, silent bool)
	JoinRoomWithKey(room string, silent bool, key string)
	HasRoom(room string) bool
	JoinedRoomList() []string
	GetMembers(room string) []string
	GetRoomMembers(room string) []rrc.RoomMemberInfo
	GetMessages(room string) []*rrc.RRCMessage
	DisplayNameFor(peer []byte) string

	SendNotice(room, text string) (string, error)
	SendDirectNotice(peerHash []byte, text string) error
	ResolvePeerToken(token string) (rrc.PeerTarget, error)
}

// hubDialer creates and tears down the connections the engine drives. In
// production it owns the RRCManager and the Reticulum instance beneath it.
type hubDialer interface {
	// Dial creates the connection for cfg. It must NOT connect: the engine
	// registers every inbound callback first, then calls ConnectAsync, so no
	// message can arrive before the bot is listening for it.
	Dial(cfg *HubConfig) (hubConn, error)
	// Close tears down every connection and everything beneath them.
	Close()
}

// hubSession is the bot's runtime state for one configured hub: the live
// connection, the rooms it has confirmed joined this session, and the rooms it
// has already greeted.
type hubSession struct {
	bot  *bot
	cfg  *HubConfig
	conn hubConn

	// wake nudges the supervisor so a state change is acted on immediately
	// instead of waiting for the next poll.
	wake chan struct{}

	mu sync.Mutex
	// welcomed records that the current link has already been brought up:
	// the rooms joined and the greetings enabled. It is cleared when the link
	// goes away so the next link joins and greets again.
	welcomed bool
	// joinedOK records the rooms whose JOINED confirmation the hub sent. The
	// hub rejects room traffic for a room the session has not joined yet, so a
	// greeting waits for this confirmation rather than for our own JOIN.
	joinedOK map[string]bool
	// greeted records the rooms that already received this session's
	// self-introduction NOTICE.
	greeted map[string]bool
	// connectedAt is when the current link came up, which is what the uptime
	// command reports as the connection age.
	connectedAt time.Time
}

// newHubSession builds the session state for one hub.
func newHubSession(b *bot, cfg *HubConfig, conn hubConn) *hubSession {
	return &hubSession{
		bot:      b,
		cfg:      cfg,
		conn:     conn,
		wake:     make(chan struct{}, 1),
		joinedOK: make(map[string]bool),
		greeted:  make(map[string]bool),
	}
}

// trigger wakes the supervisor without ever blocking the caller.
func (s *hubSession) trigger() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// isJoined reports whether the bot is in the room for this session: the hub has
// confirmed the JOIN and still reports the membership. A reply is only allowed
// in a room the bot is actually in, because the hub rejects room traffic for any
// other.
// connectionAge reports how long the current link has been up, or 0 when no
// link has been established.
func (s *hubSession) connectionAge(now time.Time) time.Duration {
	s.mu.Lock()
	started := s.connectedAt
	s.mu.Unlock()
	if started.IsZero() || now.Before(started) {
		return 0
	}
	return now.Sub(started)
}

// isJoined reports whether the bot is in the room for this session: the hub has
// confirmed the JOIN and still reports the membership. A reply is only allowed
// in a room the bot is actually in, because the hub rejects room traffic for any
// other.
func (s *hubSession) isJoined(room string) bool {
	if room == "" {
		return false
	}
	s.mu.Lock()
	confirmed := s.joinedOK[room]
	s.mu.Unlock()
	return confirmed && s.conn.HasRoom(room)
}

// knowsPeer reports whether hashHex is a member of any room the bot is in. A
// direct NOTICE can only reach a client the hub still has a live session for,
// and the member list is the only view of that this side has.
func (s *hubSession) knowsPeer(hashHex string) bool {
	if hashHex == "" {
		return false
	}
	for _, room := range s.conn.JoinedRoomList() {
		for _, member := range s.conn.GetRoomMembers(room) {
			if member.HashHex == hashHex {
				return true
			}
		}
	}
	return false
}

// observeJoin records the hub's JOINED confirmation for a room. The engine's
// inbound enqueue calls this before handing the message to the command layer.
func (s *hubSession) observeJoin(msg *rrc.RRCMessage) {
	if msg.Kind != "system" || msg.Room == "" {
		return
	}
	s.mu.Lock()
	// A confirmation for the previous link must not arm a greeting on the new
	// one: the hub has to confirm the JOIN it just received.
	if !s.welcomed {
		s.mu.Unlock()
		return
	}
	s.joinedOK[normalizeRoomName(msg.Room)] = true
	s.mu.Unlock()
	s.trigger()
}

// onLinkEstablished brings a freshly established link up to the configured
// state: the hub has forgotten any previous session, so every room is joined
// again and every greeting is re-armed. It runs on the client's link goroutine,
// so it only joins and wakes the supervisor.
func (s *hubSession) onLinkEstablished() {
	s.mu.Lock()
	s.welcomed = true
	s.connectedAt = time.Now()
	s.joinedOK = make(map[string]bool)
	s.greeted = make(map[string]bool)
	s.mu.Unlock()
	s.joinRooms()
	s.trigger()
}

// linkLost marks the current link as gone so the next established link rejoins
// and greets again.
func (s *hubSession) linkLost() {
	s.mu.Lock()
	again := s.welcomed
	s.welcomed = false
	s.joinedOK = make(map[string]bool)
	s.greeted = make(map[string]bool)
	s.mu.Unlock()
	if again {
		s.bot.logf("hub %q: link lost; rejoining and announcing again when it returns",
			s.cfg.Name)
	}
}

// supervise keeps one hub joined and greeted for as long as the bot runs. The
// link-established callback is the primary signal; the poll is a safety net for
// a hub whose status changes without the callback firing, so an always-on bot
// never sits idle on a live connection.
func (s *hubSession) supervise() {
	ticker := time.NewTicker(s.bot.statusPoll)
	defer ticker.Stop()
	for {
		select {
		case <-s.bot.stopCh:
			return
		case <-ticker.C:
		case <-s.wake:
		}
		s.sync()
	}
}

// sync reconciles the session with the connection: a connected hub whose link
// callback was missed is brought up anyway, and a room whose JOIN the hub has
// confirmed receives its greeting exactly once.
func (s *hubSession) sync() {
	connected := s.conn.GetHubStatus() == rrc.StatusConnected
	s.mu.Lock()
	welcomed := s.welcomed
	s.mu.Unlock()

	if !connected {
		if welcomed {
			s.linkLost()
		}
		return
	}
	if !welcomed {
		s.onLinkEstablished()
		return
	}
	s.greetJoinedRooms()
}

// joinRooms asks the hub to join every configured room. It runs once per
// connection: after a reconnect the hub has forgotten the session, so the JOIN
// has to be sent again.
func (s *hubSession) joinRooms() {
	for _, room := range s.cfg.Rooms {
		if room.Key != "" {
			s.conn.JoinRoomWithKey(room.Name, false, room.Key)
			continue
		}
		s.conn.JoinRoom(room.Name, false)
	}
	if len(s.cfg.Rooms) > 0 {
		names := make([]string, 0, len(s.cfg.Rooms))
		for _, room := range s.cfg.Rooms {
			names = append(names, room.Name)
		}
		// An always-on bot has to say what it is doing: without this line an
		// operator cannot tell a joined room from a failed join.
		s.bot.logf("hub %q: joined %v", s.cfg.Name, strings.Join(names, ", "))
	}
}

// greetJoinedRooms posts the one-per-session self-introduction NOTICE in every
// room the hub has confirmed us joined.
func (s *hubSession) greetJoinedRooms() {
	if !s.bot.cfg.AnnounceOnJoin {
		return
	}
	for _, room := range s.cfg.Rooms {
		s.mu.Lock()
		pending := s.joinedOK[room.Name] && !s.greeted[room.Name]
		s.mu.Unlock()
		if !pending {
			continue
		}
		text := s.bot.hooks.Greeting(s, room.Name)
		// Mark the room greeted before sending: a greeting is announced once
		// per session, so a failed send is logged rather than retried in a
		// loop.
		s.mu.Lock()
		s.greeted[room.Name] = true
		s.mu.Unlock()
		if text == "" {
			continue
		}
		if _, err := s.conn.SendNotice(room.Name, text); err != nil {
			s.bot.logf("hub %q room %q: could not announce: %v", s.cfg.Name, room.Name, err)
			continue
		}
		s.bot.logf("hub %q room %q: announced: %v", s.cfg.Name, room.Name, text)
	}
}

// managerDialer is the production dialer: one RRCManager and one Reticulum
// instance shared by every hub, with the bot's own identity installed on the
// manager so every hub sees the same identity hash.
type managerDialer struct {
	mgr       *rrc.RRCManager
	reticulum *rns.Reticulum
	closeOnce sync.Once
}

// newManagerDialer installs the identity and nick on a fresh manager.
func newManagerDialer(identity *rns.Identity, nick string, storagePath string, reticulum *rns.Reticulum) *managerDialer {
	mgr := rrc.NewManager(storagePath, func() []byte { return identity.Hash })
	mgr.SetIdentity(identity)
	mgr.SetNickname(nick)
	if reticulum != nil {
		mgr.SetTransport(reticulum.Transport())
	}
	return &managerDialer{mgr: mgr, reticulum: reticulum}
}

// Dial creates the hub connection. The manager's persistence is never loaded:
// the hubs, their rooms, and their nicks come from the configuration file, so
// editing the configuration always takes effect on the next start. The engine
// configures the connection and asks it to connect.
func (d *managerDialer) Dial(cfg *HubConfig) (hubConn, error) {
	if d.mgr == nil {
		return nil, fmt.Errorf("dialer is not initialized")
	}
	hub := d.mgr.AddHub(cfg.DestHash, rrc.HubDestName, cfg.Name)
	if hub == nil {
		return nil, fmt.Errorf("could not create a hub connection for %q", cfg.Name)
	}
	return hub, nil
}

// Close disconnects every hub and releases the Reticulum instance.
func (d *managerDialer) Close() {
	d.closeOnce.Do(func() {
		if d.mgr != nil {
			d.mgr.Shutdown()
		}
		if d.reticulum != nil {
			if err := d.reticulum.Close(); err != nil {
				logf("closing Reticulum: %v", err)
			}
		}
	})
}
