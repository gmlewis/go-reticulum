// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// captureStdLog redirects the std log output to a buffer for the duration of
// the test (sendEnv reports dropped sends through the std logger).
func captureStdLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return &buf
}

// TestSendEnvLogsNilLinkDrop verifies sendEnv reports a dropped send when the
// hub link is down, instead of failing silently — the silence previously read
// as a dead composer while messages never left the client. The failure is both
// logged and returned so a caller can surface it.
func TestSendEnvLogsNilLinkDrop(t *testing.T) {
	buf := captureStdLog(t)

	hash := []byte{0x01, 0x02, 0x03, 0x04}
	hub := NewHub(nil, hash, "rrc.hub", "Test Hub")
	env := MakeClientEnvelope(TypeMsg, hash, []byte("general"), []byte("nick"), "hello", MsgID(), NowMs())

	err := hub.sendEnv(env)

	if got := buf.String(); !strings.Contains(got, "hub link is down") {
		t.Errorf("sendEnv with nil link logged %q, want a 'hub link is down' drop notice", got)
	}
	if err == nil {
		t.Error("sendEnv with nil link returned no error; the drop is not observable to the caller")
	} else if !strings.Contains(err.Error(), "link is down") {
		t.Errorf("sendEnv error = %v, want it to name the down link", err)
	}
}

// TestSendMessageLinkDownRecordsNotice verifies an operator-typed message the
// hub link drops is observable in the room, not merely logged: SendMessage
// records a local error notice so the operator sees why their text never left,
// instead of watching a locally-echoed row the hub never received.
//
// The hub reports Connected while its link has gone — the race the composer's
// connected-gate cannot see (a hub already known to be down is gated in the
// UI, keeping the draft). Before the fix the drop was silent: the room held
// only the local echo.
func TestSendMessageLinkDownRecordsNotice(t *testing.T) {
	t.Parallel()

	hash := []byte{0x01, 0x02, 0x03, 0x04}
	hub := NewHub(nil, hash, "rrc.hub", "Test Hub")
	hub.SetStatus(StatusConnected, "Connected")
	// No link is ever established, so both sends below are dropped.

	hub.SendMessage("general", "hello")
	hub.SendAction("random", "waves")

	assertNotice := func(room string) {
		t.Helper()
		for _, m := range hub.GetMessages(room) {
			if m.Kind == "error" && strings.Contains(m.Text, "Message not sent") &&
				strings.Contains(m.Text, "link is down") {
				return
			}
		}
		t.Errorf("no send-failure notice in %q after a dropped send; buffer = %v",
			room, describeMessages(hub.GetMessages(room)))
	}
	assertNotice("general")
	assertNotice("random")
}

// describeMessages renders a message buffer compactly for failure output.
func describeMessages(msgs []*RRCMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString("\n  ")
		b.WriteString(m.Kind)
		b.WriteString(": ")
		b.WriteString(m.Text)
	}
	return b.String()
}

// Note: the encode-failure branch of sendEnv is not exercised here — the
// custom CBOR encoder in rrc/cbor panics on unsupported value types
// (encode.go appendValue) rather than returning an error, so EncodeEnvelope
// effectively cannot fail through that path.
