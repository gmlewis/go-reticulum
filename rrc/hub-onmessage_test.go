// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package rrc

import (
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// newHookTestHub builds a hub with a distinct local identity hash so inbound
// envelopes are not mistaken for our own echo.
func newHookTestHub(t *testing.T) (*RRCManager, *RRCHub) {
	t.Helper()
	ownHash := make([]byte, 16)
	for i := range ownHash {
		ownHash[i] = byte(i + 1)
	}
	mgr := NewManager(tempDir(t), func() []byte { return ownHash })
	mgr.SetNickname("TestNick")
	hub := mgr.AddHub([]byte("hubhash000000000"), "rrc.hub", "TestHub")
	hub.AddRoom("general")
	return mgr, hub
}

// peerHash returns a deterministic 16-byte identity hash for test peers.
func peerHash(seed byte) []byte {
	h := make([]byte, 16)
	for i := range h {
		h[i] = seed + byte(i)
	}
	return h
}

// feedRoomMessage drives one inbound room MSG through the hub's decode path.
func feedRoomMessage(t *testing.T, hub *RRCHub, room string, src []byte, nick, text string) *RRCMessage {
	t.Helper()
	mid := make([]byte, 8)
	copy(mid, []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, src[0]})
	env := MakeClientEnvelope(TypeMsg, src, []byte(room), []byte(nick), text, mid, NowMs())
	data, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	hub.HandleData(data)
	return &RRCMessage{Kind: "msg", Room: room, Src: src, Nick: nick, Text: text}
}

// collectHook drains delivered messages until the predicate is satisfied or the
// timeout expires.
func waitForHook(t *testing.T, ch <-chan *RRCMessage, timeout time.Duration) *RRCMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(timeout):
		return nil
	}
}

// TestSetOnMessageDeliversInboundRoomMessage asserts the inbound hook receives
// a room MSG carrying room, nick, source hash, kind, and text.
func TestSetOnMessageDeliversInboundRoomMessage(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got := make(chan *RRCMessage, 4)
	hub.SetOnMessage(func(msg *RRCMessage) { got <- msg })

	src := peerHash(0x40)
	feedRoomMessage(t, hub, "general", src, "Alice", "hello there")

	msg := waitForHook(t, got, 2*time.Second)
	if msg == nil {
		t.Fatal("timed out waiting for the inbound message hook")
	}
	if msg.Kind != "msg" {
		t.Errorf("Kind = %q, want %q", msg.Kind, "msg")
	}
	if msg.Room != "general" {
		t.Errorf("Room = %q, want %q", msg.Room, "general")
	}
	if msg.Nick != "Alice" {
		t.Errorf("Nick = %q, want %q", msg.Nick, "Alice")
	}
	if hex.EncodeToString(msg.Src) != hex.EncodeToString(src) {
		t.Errorf("Src = %v, want %v", hex.EncodeToString(msg.Src), hex.EncodeToString(src))
	}
	if msg.Text != "hello there" {
		t.Errorf("Text = %q, want %q", msg.Text, "hello there")
	}
}

// TestSetOnMessageDeliversInboundNotice asserts notices reach the hook too, so a
// bot can observe hub errors and greetings.
func TestSetOnMessageDeliversInboundNotice(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got := make(chan *RRCMessage, 4)
	hub.SetOnMessage(func(msg *RRCMessage) { got <- msg })

	env := MakeClientEnvelope(TypeNotice, peerHash(0x10), []byte("general"), nil,
		"Greetings from the hub", make([]byte, 8), NowMs())
	data, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	hub.HandleData(data)

	msg := waitForHook(t, got, 2*time.Second)
	if msg == nil {
		t.Fatal("timed out waiting for the notice hook")
	}
	if msg.Kind != "notice" {
		t.Errorf("Kind = %q, want %q", msg.Kind, "notice")
	}
	if msg.Room != "general" {
		t.Errorf("Room = %q, want %q", msg.Room, "general")
	}
}

// TestSetOnMessageSkipsOwnMessages asserts the hook is an INBOUND hook: a
// message this client sent, and the hub's echo of it, are both withheld.
func TestSetOnMessageSkipsOwnMessages(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got := make(chan *RRCMessage, 4)
	hub.SetOnMessage(func(msg *RRCMessage) { got <- msg })

	mid := hub.SendMessage("general", "my own words")
	if mid == "" {
		t.Fatal("SendMessage returned an empty message id")
	}
	if msg := waitForHook(t, got, 200*time.Millisecond); msg != nil {
		t.Fatalf("hook delivered our own sent message: %+v", msg)
	}

	// The hub's fanout echo of that send: same source hash, known message id.
	rawMid, err := hex.DecodeString(mid)
	if err != nil {
		t.Fatalf("decoding mid %q: %v", mid, err)
	}
	own := hub.Manager.identityHash()
	env := MakeClientEnvelope(TypeMsg, own, []byte("general"), []byte("TestNick"), "my own words", rawMid, NowMs())
	data, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	hub.HandleData(data)
	if msg := waitForHook(t, got, 200*time.Millisecond); msg != nil {
		t.Fatalf("hook delivered the hub's echo of our own message: %+v", msg)
	}
}

// TestSetOnMessageCallbackMayCallLockingMethods asserts the callback runs with
// no RRCHub mutex held: a callback that inspects the hub must not deadlock.
func TestSetOnMessageCallbackMayCallLockingMethods(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	done := make(chan struct{})
	var members []string
	var capsLen int
	hub.SetOnMessage(func(*RRCMessage) {
		members = hub.GetMembers("general")
		capsLen = len(hub.HubCaps)
		_ = hub.GetStatusText()
		_ = hub.DisplayNameFor(peerHash(0x40))
		close(done)
	})

	feedRoomMessage(t, hub, "general", peerHash(0x40), "Alice", "hi")

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("callback that called locking hub methods deadlocked")
	}
	if members == nil {
		t.Error("GetMembers returned nil inside the callback")
	}
	if capsLen != 0 {
		t.Errorf("HubCaps length = %v, want 0", capsLen)
	}
}

// TestSetOnMessageSlowCallbackDoesNotBlockLink asserts the hook is decoupled
// from the link path: a blocked callback never stalls HandleData, and the
// bounded queue drops the overflow instead of growing without limit.
func TestSetOnMessageSlowCallbackDoesNotBlockLink(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	hub.SetOnMessage(func(*RRCMessage) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})

	// The first delivery parks the callback goroutine.
	feedRoomMessage(t, hub, "general", peerHash(0x40), "Alice", "first")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("callback was never entered")
	}

	overflow := messageHookQueueDepth + 25
	start := time.Now()
	for i := range overflow {
		// Distinct bodies and sources defeat the fanout/self-echo collapse.
		src := peerHash(byte(0x50 + i))
		feedRoomMessage(t, hub, "general", src, fmt.Sprintf("Peer%v", i), fmt.Sprintf("msg-%v", i))
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("HandleData stalled on a blocked callback: %v for %v envelopes", elapsed, overflow)
	}
	if dropped := hub.msgHookDropped.Load(); dropped == 0 {
		t.Errorf("msgHookDropped = 0, want at least one drop once the queue overflowed")
	}

	close(release)
}

// TestSetOnMessageStopsOnDisconnect asserts Disconnect stops the hook goroutine
// and withholds further delivery, leaving no goroutine behind.
func TestSetOnMessageStopsOnDisconnect(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got := make(chan *RRCMessage, 4)
	hub.SetOnMessage(func(msg *RRCMessage) { got <- msg })

	hub.lock.Lock()
	loopDone := hub.msgHookDone
	hub.lock.Unlock()
	if loopDone == nil {
		t.Fatal("SetOnMessage did not start a hook goroutine")
	}

	hub.Disconnect()

	select {
	case <-loopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("hook goroutine still running after Disconnect")
	}

	feedRoomMessage(t, hub, "general", peerHash(0x40), "Alice", "after disconnect")
	if msg := waitForHook(t, got, 300*time.Millisecond); msg != nil {
		t.Fatalf("hook delivered after Disconnect: %+v", msg)
	}
}

// TestSetOnMessageNilClearsHook asserts SetOnMessage(nil) unregisters the
// callback and stops its goroutine, so a bot can detach cleanly.
func TestSetOnMessageNilClearsHook(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got := make(chan *RRCMessage, 4)
	hub.SetOnMessage(func(msg *RRCMessage) { got <- msg })

	hub.lock.Lock()
	loopDone := hub.msgHookDone
	hub.lock.Unlock()
	if loopDone == nil {
		t.Fatal("SetOnMessage did not start a hook goroutine")
	}

	hub.SetOnMessage(nil)

	select {
	case <-loopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("hook goroutine still running after SetOnMessage(nil)")
	}

	feedRoomMessage(t, hub, "general", peerHash(0x40), "Alice", "after unregister")
	if msg := waitForHook(t, got, 300*time.Millisecond); msg != nil {
		t.Fatalf("hook delivered after SetOnMessage(nil): %+v", msg)
	}
}

// TestInboundMessageCarriesTheEnvelopeID asserts every message read off the link
// keeps the sender's message id, which is what lets a bot recognise a
// redelivered envelope instead of answering it twice.
func TestInboundMessageCarriesTheEnvelopeID(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	hub.onSend = func(map[any]any) {}
	hub.lock.Lock()
	hub.Rooms["general"] = true
	hub.lock.Unlock()

	src := peerHash(0x70)
	mid := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	env := MakeClientEnvelope(TypeMsg, src, []byte("general"), []byte("Alice"), "hello", mid, NowMs())
	data, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	hub.HandleData(data)

	messages := hub.GetMessages("general")
	if len(messages) != 1 {
		t.Fatalf("room buffer holds %v messages, want 1", len(messages))
	}
	if got := messages[0].ID; got != "0102030405060708" {
		t.Errorf("ID = %q, want %q", got, "0102030405060708")
	}

	// A notice carries its id too.
	noticeEnv := MakeClientEnvelope(TypeNotice, src, []byte("general"), []byte("Alice"), "notice", mid, NowMs())
	noticeData, err := EncodeEnvelope(noticeEnv)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	hub.HandleData(noticeData)
	messages = hub.GetMessages("general")
	if len(messages) != 2 {
		t.Fatalf("room buffer holds %v entries, want 2", len(messages))
	}
	if got := messages[1].ID; got != "0102030405060708" {
		t.Errorf("notice ID = %q, want %q", got, "0102030405060708")
	}
}

// TestPlainHistoryEntriesOmitTheMessageID asserts adding the id does not change
// the stored shape of an entry that has none.
func TestPlainHistoryEntriesOmitTheMessageID(t *testing.T) {
	t.Parallel()

	plain := (&RRCMessage{Kind: "msg", Room: "general", Text: "hi", Ts: 1}).HistoryEntry()
	if _, ok := plain[HID]; ok {
		t.Error("a message without an id wrote a history id key")
	}
	withID := (&RRCMessage{Kind: "msg", Room: "general", Text: "hi", Ts: 1, ID: "abc"}).HistoryEntry()
	if got := withID[HID]; got != "abc" {
		t.Errorf("history id = %v, want %q", got, "abc")
	}
	decoded := DecodeHistoryEntry(withID)
	if decoded.ID != "abc" {
		t.Errorf("decoded ID = %q, want %q", decoded.ID, "abc")
	}
}
