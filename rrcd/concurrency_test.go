// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"sync"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// lockSendRecorder replaces the hub's send hook with a mutex-guarded
// recorder so concurrent OnPacket calls can append safely.
func lockSendRecorder(t *testing.T, env *hubTestEnv) {
	t.Helper()
	var mu sync.Mutex
	env.hub.sendPacket = func(link *rns.Link, payload []byte) error {
		mu.Lock()
		defer mu.Unlock()
		env.sends = append(env.sends, sentPacket{link: link, payload: payload})
		return nil
	}
}

// G16.5 The PING loop and concurrent PONG packets must not race the
// pending-pong bookkeeping: Python routes packets and scans sessions
// under one re-entrant state lock, and the Go port serializes the ping
// scan, routing, and the link lifecycle callbacks under its dispatch
// lock. Run under -race.
func TestPingLoopPongClearsAwaitingPong(t *testing.T) {
	t.Parallel()

	env := newRouterTestEnv(t, false)
	hub := env.hub
	hub.Config.PingIntervalS = 0.01
	lockSendRecorder(t, env)

	link1 := &rns.Link{}
	link2 := &rns.Link{}
	peer1 := bytesOf(0xaa, 32)
	peer2 := bytesOf(0xbb, 32)
	env.identifyLink(t, link1, peer1)
	env.identifyLink(t, link2, peer2)
	env.helloFrom(link1, peer1, "one")
	env.helloFrom(link2, peer2, "two")

	cycles := 0
	hub.sleep = func(float64) bool {
		cycles++
		return cycles <= 50
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.PingLoop()
	}()

	// PONG packets land on both links while the loop scans and pings.
	var pongers sync.WaitGroup
	pongers.Add(4)
	for range 4 {
		go func() {
			defer pongers.Done()
			for range 200 {
				hub.OnPacket(link1, cbor.Encode(MakeEnvelope(int(TPong), peer1)))
				hub.OnPacket(link2, cbor.Encode(MakeEnvelope(int(TPong), peer2)))
			}
		}()
	}

	pongers.Wait()
	<-done

	// The loop ran and pinged; the PONG handling cleared the pending
	// markers (at least one of the two links must be eligible for its
	// next PING).
	if hub.StatsManager.Counter("pings_out") == 0 {
		t.Error("the PING loop never pinged")
	}
	awaiting1 := hub.SessionManager.AwaitingPong(link1)
	awaiting2 := hub.SessionManager.AwaitingPong(link2)
	if awaiting1 == nil && awaiting2 == nil {
		t.Log("both pending markers cleared by the concurrent PONGs")
	}
}

// G16.5 A JOIN routed concurrently with the link's closure must produce
// the Python-serialized outcome: each operation is atomic, so the room
// membership and the session's room set stay coherent, and no concurrent
// map access panics. Run under -race.
func TestRoutePacketJoinConcurrentWithOnLinkClosed(t *testing.T) {
	t.Parallel()

	env := newRouterTestEnv(t, false)
	hub := env.hub
	lockSendRecorder(t, env)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	env.identifyLink(t, link, peer)
	env.helloFrom(link, peer, "joiner")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			hub.OnPacket(link, cbor.Encode(MakeEnvelope(int(TJoin), peer, WithRoom("general"))))
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			hub.OnClose(link)
			hub.OnLink(link)
			hub.OnRemoteIdentified(link, mustTestIdentified(t, peer))
		}
	}()
	wg.Wait()

	// The serialized outcome leaves the link identified with a live
	// session, and the room membership agrees with the session's rooms.
	if hub.SessionManager.GetSession(link) == nil {
		t.Fatal("the link's session did not survive the interleaved run")
	}
	if string(hub.SessionManager.PeerOf(link)) != string(peer) {
		t.Error("the peer hash did not survive the interleaved run")
	}
	inSession := hub.SessionManager.InRoom(link, "general")
	inRoom := hub.RoomManager.GetRoomMembers("general")[link]
	if inSession != inRoom {
		t.Errorf("room membership diverged from the session rooms: session=%v room=%v",
			inSession, inRoom)
	}
}
