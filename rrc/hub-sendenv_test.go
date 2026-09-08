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
// as a dead composer while messages never left the client.
func TestSendEnvLogsNilLinkDrop(t *testing.T) {
	buf := captureStdLog(t)

	hash := []byte{0x01, 0x02, 0x03, 0x04}
	hub := NewHub(nil, hash, "rrc.hub", "Test Hub")
	env := MakeClientEnvelope(TypeMsg, hash, []byte("general"), []byte("nick"), "hello", MsgID(), NowMs())

	hub.sendEnv(env)

	if got := buf.String(); !strings.Contains(got, "hub link is down") {
		t.Errorf("sendEnv with nil link logged %q, want a 'hub link is down' drop notice", got)
	}
}

// Note: the encode-failure branch of sendEnv is not exercised here — the
// custom CBOR encoder in rrc/cbor panics on unsupported value types
// (encode.go appendValue) rather than returning an error, so EncodeEnvelope
// effectively cannot fail through that path.
