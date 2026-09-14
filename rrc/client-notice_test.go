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
	"errors"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestSendNoticeEnvelopeShape asserts a room notice is one T_NOTICE carrying
// the room, the sender's nick, and no K_DST: the reply shape the RRC bot
// contract requires.
func TestSendNoticeEnvelopeShape(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	var got []map[any]any
	hub.onSend = func(env map[any]any) { got = append(got, env) }
	hub.lock.Lock()
	hub.Rooms["general"] = true
	hub.lock.Unlock()

	mid, err := hub.SendNotice("General", "hello room")
	if err != nil {
		t.Fatalf("SendNotice: %v", err)
	}
	if mid == "" {
		t.Error("SendNotice returned an empty message id")
	}
	if len(got) != 1 {
		t.Fatalf("sent %v envelopes, want exactly 1", len(got))
	}

	env := got[0]
	if v := intVal(env, KeyType); v != TypeNotice {
		t.Errorf("K_T = %v, want TNotice (%v)", v, TypeNotice)
	}
	if v := intVal(env, KeyVersion); v != RRCVersion {
		t.Errorf("K_V = %v, want %v", v, RRCVersion)
	}
	if v := envVal(env, KeyRoom); v != "general" {
		t.Errorf("K_ROOM = %v, want %q (lowercased)", v, "general")
	}
	if _, ok := env[KeyDst]; ok {
		t.Error("K_DST is set on a room notice, want it absent")
	}
	if v := envVal(env, KeyBody); v != "hello room" {
		t.Errorf("K_BODY = %v, want %q", v, "hello room")
	}
	if v := envVal(env, KeyNick); v != hub.GetEffectiveNick() {
		t.Errorf("K_NICK = %v, want the effective nick %q", v, hub.GetEffectiveNick())
	}
	src := byteVal(env, KeySource)
	if len(src) != IdentityHashLen {
		t.Errorf("K_SRC = %v bytes, want %v", len(src), IdentityHashLen)
	}
	id := byteVal(env, KeyMessageID)
	if hex.EncodeToString(id) != mid {
		t.Errorf("K_ID = %v, want the returned id %v", hex.EncodeToString(id), mid)
	}
}

// TestSendNoticeRecordsLocally asserts the sender sees its own notice in the
// room buffer and in the notice bucket, exactly like SendMessage.
func TestSendNoticeRecordsLocally(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	hub.onSend = func(map[any]any) {}
	hub.lock.Lock()
	hub.Rooms["general"] = true
	hub.lock.Unlock()

	if _, err := hub.SendNotice("general", "local echo"); err != nil {
		t.Fatalf("SendNotice: %v", err)
	}

	var found int
	for _, msg := range hub.GetMessages("general") {
		if msg.Text == "local echo" && msg.Kind == "notice" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("room buffer holds %v copies of the notice, want 1", found)
	}
	hub.lock.Lock()
	noticeCount := len(hub.Notices)
	hub.lock.Unlock()
	if noticeCount != 1 {
		t.Errorf("notice bucket holds %v entries, want 1", noticeCount)
	}
}

// TestSendNoticeCollapsesItsOwnFanoutEcho asserts the hub's per-member fanout
// copy is recognised and dropped, so the sender never renders its own notice
// twice.
func TestSendNoticeCollapsesItsOwnFanoutEcho(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	var echo map[any]any
	hub.onSend = func(env map[any]any) { echo = env }
	hub.lock.Lock()
	hub.Rooms["general"] = true
	hub.lock.Unlock()

	if _, err := hub.SendNotice("general", "echo me"); err != nil {
		t.Fatalf("SendNotice: %v", err)
	}
	if echo == nil {
		t.Fatal("no envelope was sent")
	}

	// Feed the envelope straight back, as the hub's fanout does.
	data, err := EncodeEnvelope(echo)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	hub.HandleData(data)

	var found int
	for _, msg := range hub.GetMessages("general") {
		if msg.Text == "echo me" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("room buffer holds %v copies after the fanout echo, want 1", found)
	}
}

// TestSendNoticeRejectsBadInput asserts the notice never silently disappears:
// an empty room, an empty body, and a body that cannot fit one envelope are all
// reported to the caller.
func TestSendNoticeRejectsBadInput(t *testing.T) {
	t.Parallel()

	t.Run("empty room", func(t *testing.T) {
		t.Parallel()
		_, hub := newHookTestHub(t)
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		_, err := hub.SendNotice("", "hi")
		if !errors.Is(err, ErrNoticeRoomRequired) {
			t.Fatalf("error = %v, want ErrNoticeRoomRequired", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})

	t.Run("blank body", func(t *testing.T) {
		t.Parallel()
		_, hub := newHookTestHub(t)
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		_, err := hub.SendNotice("general", "   ")
		if !errors.Is(err, ErrNoticeBodyEmpty) {
			t.Fatalf("error = %v, want ErrNoticeBodyEmpty", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})

	t.Run("body larger than one envelope", func(t *testing.T) {
		t.Parallel()
		_, hub := newHookTestHub(t)
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		_, err := hub.SendNotice("general", strings.Repeat("x", rns.MDU*2))
		if err == nil {
			t.Fatal("oversized SendNotice = nil error, want an error")
		}
		if !strings.Contains(err.Error(), "MDU") {
			t.Errorf("error = %q, want it to mention the MDU", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})

	t.Run("body just under one envelope", func(t *testing.T) {
		t.Parallel()
		_, hub := newHookTestHub(t)
		hub.onSend = func(map[any]any) {}
		hub.lock.Lock()
		hub.Rooms["general"] = true
		hub.lock.Unlock()

		// 300 bytes leaves ample room for the envelope keys around it.
		if _, err := hub.SendNotice("general", strings.Repeat("y", 300)); err != nil {
			t.Fatalf("SendNotice(300 bytes) = %v, want success", err)
		}
	})
}
