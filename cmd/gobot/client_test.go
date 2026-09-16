// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

// tempDir returns a short-lived directory under /tmp. t.TempDir() is unusable
// here: on macOS its path is too long for the Unix domain sockets Reticulum
// binds.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gobot-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// directSend records one SendDirectNotice call.
type directSend struct {
	hash []byte
	text string
}

// fakeConn is a scripted hubConn: it reports whatever connection state the test
// asks for and records every call, so the orchestration can be exercised
// without a Reticulum stack or a live hub.
type fakeConn struct {
	mu sync.Mutex

	status     int
	statusText string
	onMessage  func(*rrc.RRCMessage)

	// roomMembers is what GetRoomMembers reports once the room has been joined.
	roomMembers []rrc.RoomMemberInfo
	joined      bool

	// target and resolveErr script ResolvePeerToken.
	target     rrc.PeerTarget
	resolveErr error

	// privateErr, when set, is returned by SendPrivateCommand instead of
	// delivering replies.
	privateErr error
	// replies are delivered through onMessage when a request is sent.
	replies []*rrc.RRCMessage

	sent         []string
	directs      []directSend
	connectCalls int
}

// newFakeConn builds a connected fake whose room already lists the given
// members.
func newFakeConn(members ...rrc.RoomMemberInfo) *fakeConn {
	return &fakeConn{status: rrc.StatusConnected, roomMembers: members}
}

func (f *fakeConn) SetOnMessage(fn func(*rrc.RRCMessage)) {
	f.mu.Lock()
	f.onMessage = fn
	f.mu.Unlock()
}

func (f *fakeConn) SetAutoReconnect(bool, bool) {}
func (f *fakeConn) SetAutoList(bool, bool)      {}
func (f *fakeConn) SetAutoWho(bool, bool)       {}
func (f *fakeConn) SetNickOverride(string)      {}

func (f *fakeConn) ConnectAsync() {
	f.mu.Lock()
	f.connectCalls++
	f.mu.Unlock()
}

func (f *fakeConn) Disconnect() {}

func (f *fakeConn) GetHubStatus() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeConn) GetStatusText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusText
}

func (f *fakeConn) JoinRoom(string, bool) {
	f.mu.Lock()
	f.joined = true
	f.mu.Unlock()
}

func (f *fakeConn) GetRoomMembers(string) []rrc.RoomMemberInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.joined {
		return nil
	}
	return append([]rrc.RoomMemberInfo(nil), f.roomMembers...)
}

func (f *fakeConn) ResolvePeerToken(string) (rrc.PeerTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resolveErr != nil {
		return rrc.PeerTarget{}, f.resolveErr
	}
	return f.target, nil
}

// deliver pushes every scripted reply through the message hook, copying the
// leaf fields so the test owns them.
func (f *fakeConn) deliver() {
	f.mu.Lock()
	onMessage := f.onMessage
	replies := make([]*rrc.RRCMessage, 0, len(f.replies))
	for _, reply := range f.replies {
		copied := *reply
		copied.Src = append([]byte(nil), reply.Src...)
		replies = append(replies, &copied)
	}
	f.mu.Unlock()
	for _, reply := range replies {
		if onMessage != nil {
			onMessage(reply)
		}
	}
}

func (f *fakeConn) SendPrivateCommand(text string) error {
	f.mu.Lock()
	f.sent = append(f.sent, text)
	err := f.privateErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	f.deliver()
	return nil
}

func (f *fakeConn) SendDirectNotice(hash []byte, text string) error {
	f.mu.Lock()
	f.directs = append(f.directs, directSend{hash: append([]byte(nil), hash...), text: text})
	f.mu.Unlock()
	f.deliver()
	return nil
}

func (f *fakeConn) sentText() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *fakeConn) directSends() []directSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]directSend(nil), f.directs...)
}

// testHash returns n deterministic identity-hash bytes.
func testHash(n byte) []byte {
	out := make([]byte, rrc.IdentityHashLen)
	for i := range out {
		out[i] = n
	}
	return out
}

// botReply builds one direct notice from the bot.
func botReply(src []byte, nick, text string) *rrc.RRCMessage {
	return &rrc.RRCMessage{Src: src, Nick: nick, Text: text, Direct: true}
}

// requestOptions returns options suitable for a fast unit test.
func requestOptions() *options {
	return &options{
		target: "gobot",
		room:   "general",
		nick:   "gobot-cli",
		quiet:  40 * time.Millisecond,
	}
}

// TestRunRequestSendsPrivateCommandAndReturnsReply is the central unit test:
// the tool connects, joins the room, asks the hub to deliver "/dnotice gobot
// <command>", and returns every reply line the bot sent.
func TestRunRequestSendsPrivateCommandAndReturnsReply(t *testing.T) {
	t.Parallel()

	own := testHash(0x01)
	bot := testHash(0x02)
	conn := newFakeConn(rrc.RoomMemberInfo{HashHex: hexString(own), Nick: "gobot-cli"})
	conn.target = rrc.PeerTarget{HashHex: hexString(bot), Hash: bot, Nick: "gobot"}
	conn.replies = []*rrc.RRCMessage{
		botReply(bot, "gobot", "Buoys: enter a station ID, e.g. 41010"),
		botReply(bot, "gobot", "Example: gobot buoy 41010"),
	}

	opts := requestOptions()
	opts.message = "help buoy"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lines, err := runRequest(ctx, conn, opts, own)
	if err != nil {
		t.Fatalf("runRequest: %v", err)
	}
	want := []string{
		"Buoys: enter a station ID, e.g. 41010",
		"Example: gobot buoy 41010",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	if got := conn.sentText(); !reflect.DeepEqual(got, []string{"/dnotice gobot help buoy"}) {
		t.Errorf("sent = %q, want the /dnotice line", got)
	}
	if conn.connectCalls != 1 {
		t.Errorf("ConnectAsync calls = %v, want 1", conn.connectCalls)
	}
	if !conn.joined {
		t.Error("the room was never joined; the bot could not answer a private request")
	}
}

// TestRunRequestFiltersOtherTraffic asserts a non-direct message, the caller's
// own traffic, and a private message from somebody else are not printed as the
// bot's reply.
func TestRunRequestFiltersOtherTraffic(t *testing.T) {
	t.Parallel()

	own := testHash(0x01)
	bot := testHash(0x02)
	other := testHash(0x03)
	conn := newFakeConn(rrc.RoomMemberInfo{HashHex: hexString(own)})
	conn.target = rrc.PeerTarget{HashHex: hexString(bot), Hash: bot, Nick: "gobot"}
	room := botReply(bot, "gobot", "room chatter")
	room.Direct = false
	conn.replies = []*rrc.RRCMessage{
		room,
		botReply(own, "gobot-cli", "our own echo"),
		botReply(other, "someone-else", "a private message from a stranger"),
		botReply(bot, "gobot", "the real answer"),
	}

	opts := requestOptions()
	opts.message = "ping"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lines, err := runRequest(ctx, conn, opts, own)
	if err != nil {
		t.Fatalf("runRequest: %v", err)
	}
	if want := []string{"the real answer"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}

// TestRunRequestMatchesNickWhenHashUnknown asserts the advisory nick on the
// forwarded notice is enough to recognize the reply when the bot's identity
// hash could not be resolved up front.
func TestRunRequestMatchesNickWhenHashUnknown(t *testing.T) {
	t.Parallel()

	own := testHash(0x01)
	bot := testHash(0x02)
	other := testHash(0x03)
	conn := newFakeConn(rrc.RoomMemberInfo{HashHex: hexString(own)})
	conn.resolveErr = rrc.ErrPeerNotFound
	conn.replies = []*rrc.RRCMessage{
		botReply(other, "someone-else", "not the bot"),
		botReply(bot, "gobot", "the answer"),
	}

	opts := requestOptions()
	opts.message = "ping"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lines, err := runRequest(ctx, conn, opts, own)
	if err != nil {
		t.Fatalf("runRequest: %v", err)
	}
	if want := []string{"the answer"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}

// TestRunRequestFallsBackToDirectNotice asserts a hub without the
// private-command extension still reaches the bot when its hash is known.
func TestRunRequestFallsBackToDirectNotice(t *testing.T) {
	t.Parallel()

	own := testHash(0x01)
	bot := testHash(0x02)
	conn := newFakeConn(rrc.RoomMemberInfo{HashHex: hexString(own)})
	conn.target = rrc.PeerTarget{HashHex: hexString(bot), Hash: bot, Nick: "gobot"}
	conn.privateErr = rrc.ErrPrivateCommandsUnsupported
	conn.replies = []*rrc.RRCMessage{botReply(bot, "gobot", "the answer")}

	opts := requestOptions()
	opts.message = "ping"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lines, err := runRequest(ctx, conn, opts, own)
	if err != nil {
		t.Fatalf("runRequest: %v", err)
	}
	if want := []string{"the answer"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	sends := conn.directSends()
	if len(sends) != 1 {
		t.Fatalf("SendDirectNotice calls = %v, want 1", len(sends))
	}
	if sends[0].text != "ping" {
		t.Errorf("direct notice text = %q, want %q", sends[0].text, "ping")
	}
	if !reflect.DeepEqual(sends[0].hash, bot) {
		t.Errorf("direct notice hash = %v, want the bot's hash", hexString(sends[0].hash))
	}
}

// TestRunRequestFailsWhenHubCannotConnect asserts a failed connection is
// reported with the client's own status text rather than timing out.
func TestRunRequestFailsWhenHubCannotConnect(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()
	conn.status = rrc.StatusFailed
	conn.statusText = "Hub identity unknown"

	opts := requestOptions()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runRequest(ctx, conn, opts, testHash(0x01))
	if err == nil {
		t.Fatal("runRequest with a failed hub = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), "Hub identity unknown") {
		t.Errorf("error = %q, want the client's status text", err)
	}
	if len(conn.sentText()) != 0 {
		t.Errorf("sent = %q, want nothing sent on a failed connection", conn.sentText())
	}
}

// TestRunRequestReportsSilence asserts that a delivered request whose answer
// never comes fails with ErrNoReply, so the caller can tell silence from a
// transport failure.
func TestRunRequestReportsSilence(t *testing.T) {
	t.Parallel()

	own := testHash(0x01)
	conn := newFakeConn(rrc.RoomMemberInfo{HashHex: hexString(own)})
	conn.target = rrc.PeerTarget{HashHex: hexString(testHash(0x02)), Hash: testHash(0x02), Nick: "gobot"}

	opts := requestOptions()
	opts.quiet = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := runRequest(ctx, conn, opts, own)
	if !errors.Is(err, ErrNoReply) {
		t.Fatalf("runRequest with no reply = %v, want ErrNoReply", err)
	}
	if got := conn.sentText(); len(got) != 1 {
		t.Errorf("sent = %q, want the request to have gone out", got)
	}
}

// TestRunRequestTimeoutJoining asserts a hub that never confirms the JOIN is
// reported as a join failure, not a reply failure.
func TestRunRequestTimeoutJoining(t *testing.T) {
	t.Parallel()

	// The fake reports no members at all, so the join confirmation never
	// arrives.
	conn := newFakeConn()
	opts := requestOptions()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := runRequest(ctx, conn, opts, testHash(0x01))
	if err == nil {
		t.Fatal("runRequest without a join confirmation = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), "joining") {
		t.Errorf("error = %q, want it to name the join step", err)
	}
}

// TestReplyCollectorSnapshotIsACopy asserts the returned lines are owned by the
// caller and cannot be mutated through the collector.
func TestReplyCollectorSnapshotIsACopy(t *testing.T) {
	t.Parallel()

	collector := newReplyCollector("gobot", hexString(testHash(0x01)))
	collector.add(botReply(testHash(0x02), "gobot", "one"))
	lines := collector.snapshot()
	lines[0] = "changed"
	if got := collector.snapshot(); got[0] != "one" {
		t.Errorf("snapshot shares its backing array: got %q, want %q", got[0], "one")
	}
}

// TestHexString asserts the local renderer matches encoding/hex for the values
// this tool handles.
func TestHexString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "empty", in: []byte{}, want: ""},
		{name: "one byte", in: []byte{0x00}, want: "00"},
		{name: "high nibble", in: []byte{0xf0}, want: "f0"},
		{name: "identity hash", in: testHash(0xab), want: strings.Repeat("ab", rrc.IdentityHashLen)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hexString(tt.in); got != tt.want {
				t.Errorf("hexString(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
