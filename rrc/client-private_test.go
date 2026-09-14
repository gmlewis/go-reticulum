// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// privateCapHub returns a hub that advertises CAPPrivateCommand and has
// processed a WELCOME from hubIdentity, with every outbound envelope captured.
func privateCapHub(t *testing.T, hubIdentity []byte) (*RRCHub, *[]map[any]any) {
	t.Helper()
	_, hub := newHookTestHub(t)
	sent := &[]map[any]any{}
	hub.onSend = func(env map[any]any) { *sent = append(*sent, env) }

	feedWelcome(t, hub, hubIdentity, true)
	// A WELCOME makes the client answer on the link, so start from empty.
	*sent = (*sent)[:0]
	return hub, sent
}

// feedWelcome drives one inbound WELCOME through the hub's decode path.
func feedWelcome(t *testing.T, hub *RRCHub, src []byte, privateCommands bool) {
	t.Helper()
	caps := map[any]any{
		CAPAction:       true,
		CAPDirectNotice: true,
	}
	if privateCommands {
		caps[CAPPrivateCommand] = true
	}
	body := map[any]any{BWelcomeHub: "TestHub", BWelcomeCaps: caps}
	env := MakeClientEnvelope(TypeWelcome, src, nil, nil, body, make([]byte, 8), NowMs())
	hub.HandleData(cbor.Encode(env))
}

// TestWelcomeTeachesTheHubIdentity asserts the client captures the hub's
// identity hash — the value K_DST needs, which is not the configured hub hash —
// and ignores a source that is not an identity hash.
func TestWelcomeTeachesTheHubIdentity(t *testing.T) {
	t.Parallel()

	hubIdentity := peerHash(0x70)
	hub, _ := privateCapHub(t, hubIdentity)
	if got := hub.HubIdentityHash(); hex.EncodeToString(got) != hex.EncodeToString(hubIdentity) {
		t.Errorf("HubIdentityHash() = %v, want %v", hex.EncodeToString(got), hex.EncodeToString(hubIdentity))
	}

	_, other := newHookTestHub(t)
	feedWelcome(t, other, []byte{1, 2, 3, 4, 5}, true)
	if got := other.HubIdentityHash(); got != nil {
		t.Errorf("HubIdentityHash() = %v after a 5-byte source, want nil", hex.EncodeToString(got))
	}
}

// TestSendPrivateCommandRequiresTheCapability asserts an extension-free hub is
// never sent one, and that nothing reaches the wire.
func TestSendPrivateCommandRequiresTheCapability(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	sent := &[]map[any]any{}
	hub.onSend = func(env map[any]any) { *sent = append(*sent, env) }
	feedWelcome(t, hub, peerHash(0x70), false)
	*sent = (*sent)[:0]

	err := hub.SendPrivateCommand("/dnotice minipc hello")
	if !errors.Is(err, ErrPrivateCommandsUnsupported) {
		t.Errorf("SendPrivateCommand error = %v, want ErrPrivateCommandsUnsupported", err)
	}
	if len(*sent) != 0 {
		t.Errorf("sent %v envelopes to a hub without the capability, want 0", len(*sent))
	}
}

// TestSendPrivateCommandRequiresTheHubIdentity asserts the capability alone is
// not enough: without a WELCOME there is no address to use.
func TestSendPrivateCommandRequiresTheHubIdentity(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	sent := &[]map[any]any{}
	hub.onSend = func(env map[any]any) { *sent = append(*sent, env) }
	hub.lock.Lock()
	hub.HubCaps = map[any]any{int64(CapPrivateCommand): true}
	hub.lock.Unlock()

	err := hub.SendPrivateCommand("/dnotice minipc hello")
	if !errors.Is(err, ErrHubIdentityUnknown) {
		t.Errorf("SendPrivateCommand error = %v, want ErrHubIdentityUnknown", err)
	}
	if len(*sent) != 0 {
		t.Errorf("sent %v envelopes without a hub identity, want 0", len(*sent))
	}
}

// TestSendPrivateCommandSendsAnAddressedNotice pins the envelope: a NOTICE
// carrying this client's identity as the source, the hub's identity as the
// destination, the command line as the body, and no room.
func TestSendPrivateCommandSendsAnAddressedNotice(t *testing.T) {
	t.Parallel()

	hubIdentity := peerHash(0x70)
	hub, sent := privateCapHub(t, hubIdentity)
	const line = "/dnotice minipc hello there"

	if err := hub.SendPrivateCommand(line); err != nil {
		t.Fatalf("SendPrivateCommand: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("sent %v envelopes, want 1", len(*sent))
	}
	env := (*sent)[0]
	if got := intVal(env, KeyType); got != TypeNotice {
		t.Errorf("K_T = %v, want TNotice (%v)", got, TypeNotice)
	}
	if got := byteVal(env, KeyDst); hex.EncodeToString(got) != hex.EncodeToString(hubIdentity) {
		t.Errorf("K_DST = %v, want the hub identity %v", hex.EncodeToString(got), hex.EncodeToString(hubIdentity))
	}
	if got := byteVal(env, KeySource); hex.EncodeToString(got) == hex.EncodeToString(hubIdentity) {
		t.Error("K_SRC is the hub identity; it must be this client's identity")
	}
	if got := envVal(env, KeyBody); got != line {
		t.Errorf("K_B = %v, want %q", got, line)
	}
	if _, ok := env[KeyRoom]; ok {
		t.Error("a private command carries a room; it must carry none")
	}
	if intVal(env, KeyVersion) != RRCVersion {
		t.Errorf("K_V = %v, want %v", intVal(env, KeyVersion), RRCVersion)
	}
}

// TestAddLocalSelfMessageKeepsTheTypedEcho asserts the local copy of a line the
// user typed lands in the room buffer credited to the local user: the room view
// is rebuilt from that buffer on every hub refresh, so an echo held only by the
// widget would vanish as soon as the private reply arrived.
func TestAddLocalSelfMessageKeepsTheTypedEcho(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	sent := &[]map[any]any{}
	hub.onSend = func(env map[any]any) { *sent = append(*sent, env) }

	const line = "/msg gorrcbot help"
	hub.AddLocalSelfMessage("general", "glenn", line)

	msgs := hub.GetMessages("general")
	if len(msgs) != 1 {
		t.Fatalf("room buffer has %v rows, want the echo", len(msgs))
	}
	got := msgs[0]
	if got.Kind != "msg" || got.Text != line || got.Nick != "glenn" || got.Room != "general" {
		t.Errorf("echo row = %+v, want a msg row with the typed line and the local nick", got)
	}
	if len(got.Src) == 0 {
		t.Error("echo row carries no source hash; the client cannot recognise it as its own")
	}
	// A client-only row must never reach the wire.
	if len(*sent) != 0 {
		t.Errorf("a local echo sent %v envelopes, want none", len(*sent))
	}
}
