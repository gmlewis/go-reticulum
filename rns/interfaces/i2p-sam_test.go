// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestSAMRequestBuildersGolden covers Phase 19 task 1: the SAM request message
// builders produce the exact wire bytes Python emits (sam.py:48-76), so a Go
// SAM client is wire-compatible with a Python-trained SAM API.
func TestSAMRequestBuildersGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"hello", samHelloBytes("3.1", "3.1"), "HELLO VERSION MIN=3.1 MAX=3.1\n"},
		{"session_create", samSessionCreateBytes("STREAM", "sess1", "TRANSIENT", ""), "SESSION CREATE STYLE=STREAM ID=sess1 DESTINATION=TRANSIENT \n"},
		{"session_create_opts", samSessionCreateBytes("STREAM", "s2", "TRANSIENT", "inbound.length=2 outbound.length=2"), "SESSION CREATE STYLE=STREAM ID=s2 DESTINATION=TRANSIENT inbound.length=2 outbound.length=2\n"},
		{"stream_connect", samStreamConnectBytes("sess1", "DESTB64", "false"), "STREAM CONNECT ID=sess1 DESTINATION=DESTB64 SILENT=false\n"},
		{"stream_accept", samStreamAcceptBytes("sess1", "false"), "STREAM ACCEPT ID=sess1 SILENT=false\n"},
		{"stream_forward", samStreamForwardBytes("sess1", 6668, ""), "STREAM FORWARD ID=sess1 PORT=6668 \n"},
		{"naming_lookup", samNamingLookupBytes("irc.echelon.i2p"), "NAMING LOOKUP NAME=irc.echelon.i2p\n"},
		{"dest_generate", samDestGenerateBytes(7), "DEST GENERATE SIGNATURE_TYPE=7\n"},
	}
	for _, tc := range cases {
		if string(tc.got) != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestParseSAMMessage covers Phase 19 task 1: ParseSAMMessage splits a SAM reply
// into cmd/action/opts and OK() detects RESULT=OK (sam.py:25-45).
func TestParseSAMMessage(t *testing.T) {
	t.Parallel()
	m := ParseSAMMessage("HELLO REPLY RESULT=OK")
	if m.Cmd != "HELLO" || m.Action != "REPLY" || !m.OK() {
		t.Fatalf("HELLO REPLY parse: cmd=%q action=%q ok=%v", m.Cmd, m.Action, m.OK())
	}
	m = ParseSAMMessage("SESSION STATUS RESULT=OK DESTINATION=ABCDE")
	if m.Cmd != "SESSION" || m.Action != "STATUS" || !m.OK() || m.Opts["DESTINATION"] != "ABCDE" {
		t.Fatalf("SESSION STATUS parse: %#v", m)
	}
	m = ParseSAMMessage("STREAM STATUS RESULT=CANT_REACH_PEER")
	if m.OK() || m.Opts["RESULT"] != "CANT_REACH_PEER" {
		t.Fatalf("STREAM STATUS error parse: %#v ok=%v", m, m.OK())
	}
	// A bare token maps to a true-valued option (sam.py:36).
	m = ParseSAMMessage("NAMING REPLY RESULT=OK VALUE=Z BARE")
	if m.Opts["BARE"] != "true" {
		t.Fatalf("bare token BARE=%q want true", m.Opts["BARE"])
	}
}

// TestSAMErrorForResult covers Phase 19 task 1: RESULT strings map to the
// Python SAM_EXCEPTIONS sentinel errors (exceptions.py:33-41).
func TestSAMErrorForResult(t *testing.T) {
	t.Parallel()
	cases := map[string]error{
		"CANT_REACH_PEER": ErrSAMCantReachPeer,
		"DUPLICATED_DEST": ErrSAMDuplicatedDest,
		"DUPLICATED_ID":   ErrSAMDuplicatedID,
		"I2P_ERROR":       ErrSAMI2PError,
		"INVALID_ID":      ErrSAMInvalidID,
		"INVALID_KEY":     ErrSAMInvalidKey,
		"KEY_NOT_FOUND":   ErrSAMKeyNotFound,
		"PEER_NOT_FOUND":  ErrSAMPeerNotFound,
		"TIMEOUT":         ErrSAMTimeout,
	}
	for result, want := range cases {
		got := samErrorForResult(result)
		if got != want {
			t.Errorf("result=%q: got %v want %v", result, got, want)
		}
	}
	// Unknown result wraps in an I2PError-style message.
	if err := samErrorForResult("BOGUS_RESULT"); err == nil || !strings.Contains(err.Error(), "BOGUS_RESULT") {
		t.Fatalf("unknown result: got %v want error mentioning BOGUS_RESULT", err)
	}
}

// TestI2PDestinationBase32 covers Phase 19 task 1: the I2P destination base32
// hash is the lowercase 52-char base32 SHA-256 of the binary destination
// (sam.Destination.base32).
func TestI2PDestinationBase32(t *testing.T) {
	t.Parallel()
	data := make([]byte, 32) // arbitrary 32-byte destination
	for i := range data {
		data[i] = byte(i)
	}
	d := NewI2PDestinationFromData(data)
	b32 := d.Base32()
	if len(b32) != 52 {
		t.Fatalf("base32 len=%d want 52", len(b32))
	}
	if b32 != strings.ToLower(b32) {
		t.Fatalf("base32 %q not lowercase", b32)
	}
	if d.Base64 != i2pB64Encode(data) {
		t.Fatalf("base64 mismatch")
	}
	// Round-trip base64.
	d2, err := NewI2PDestinationFromB64(d.Base64)
	if err != nil {
		t.Fatal(err)
	}
	if string(d2.Data) != string(data) {
		t.Fatal("base64 round-trip data mismatch")
	}
}

// mockSAM is a scriptable SAM API endpoint for unit tests. Each accepted
// connection runs the handler, which reads SAM command lines and responds
// according to the reply table. The handler closes after the configured
// exchange count to release the client.
type mockSAM struct {
	t          *testing.T
	listener   net.Listener
	wg         sync.WaitGroup
	mu         sync.Mutex
	hellos     int
	sessions   int
	connects   int
	accepts    int
	lookups    int
	destgens   int
	destB64    string
	connectErr string // RESULT to return for STREAM CONNECT (empty = OK)
	sessionErr string // RESULT to return for SESSION CREATE (empty = OK)
}

func newMockSAM(t *testing.T) *mockSAM {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock SAM listen: %v", err)
	}
	m := &mockSAM{t: t, listener: l, destB64: i2pB64Encode([]byte("mock-destination-payload"))}
	m.wg.Add(1)
	go m.serve()
	return m
}

func (m *mockSAM) addr() string { return m.listener.Addr().String() }

func (m *mockSAM) close() {
	_ = m.listener.Close()
	m.wg.Wait()
}

func (m *mockSAM) serve() {
	defer m.wg.Done()
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			return
		}
		m.wg.Add(1)
		go m.handle(conn)
	}
}

func (m *mockSAM) handle(conn net.Conn) {
	defer m.wg.Done()
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "HELLO"):
			m.mu.Lock()
			m.hellos++
			m.mu.Unlock()
			io.WriteString(conn, "HELLO REPLY RESULT=OK VERSION=3.1\n")
		case strings.HasPrefix(line, "SESSION CREATE"):
			m.mu.Lock()
			m.sessions++
			res := m.sessionErr
			m.mu.Unlock()
			if res != "" {
				io.WriteString(conn, "SESSION STATUS RESULT="+res+"\n")
				return
			}
			io.WriteString(conn, "SESSION STATUS RESULT=OK DESTINATION="+m.destB64+"\n")
		case strings.HasPrefix(line, "STREAM CONNECT"):
			m.mu.Lock()
			m.connects++
			res := m.connectErr
			m.mu.Unlock()
			if res != "" {
				io.WriteString(conn, "STREAM STATUS RESULT="+res+"\n")
				return
			}
			io.WriteString(conn, "STREAM STATUS RESULT=OK\n")
		case strings.HasPrefix(line, "STREAM ACCEPT"):
			m.mu.Lock()
			m.accepts++
			m.mu.Unlock()
			io.WriteString(conn, "STREAM STATUS RESULT=OK\n")
		case strings.HasPrefix(line, "NAMING LOOKUP"):
			m.mu.Lock()
			m.lookups++
			m.mu.Unlock()
			io.WriteString(conn, "NAMING REPLY RESULT=OK VALUE="+m.destB64+"\n")
		case strings.HasPrefix(line, "DEST GENERATE"):
			m.mu.Lock()
			m.destgens++
			m.mu.Unlock()
			io.WriteString(conn, "DEST REPLY RESULT=OK PRIV="+m.destB64+"\n")
		default:
			io.WriteString(conn, "HELLO REPLY RESULT=I2P_ERROR\n")
			return
		}
	}
}

// newMockSAMClient wires a SAMClient at the mock SAM endpoint.
func newMockSAMClient(m *mockSAM) *SAMClient {
	c := newSAMClient()
	c.Address = m.addr()
	return c
}

// TestSAMClientCreateSession covers Phase 19 task 1: CreateSession performs the
// HELLO handshake then SESSION CREATE, returns the created destination, and
// leaves the session control conn open (aiosam.create_session).
func TestSAMClientCreateSession(t *testing.T) {
	t.Parallel()
	m := newMockSAM(t)
	defer m.close()
	c := newMockSAMClient(m)

	dest, conn, err := c.CreateSession("sess1", "", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if dest == nil || dest.Base64 != m.destB64 {
		t.Fatalf("dest = %v, want base64 %q", dest, m.destB64)
	}
	m.mu.Lock()
	hellos := m.hellos
	sessions := m.sessions
	m.mu.Unlock()
	if hellos != 1 {
		t.Fatalf("hellos=%d want 1", hellos)
	}
	if sessions != 1 {
		t.Fatalf("sessions=%d want 1", sessions)
	}
}

// TestSAMClientCreateSessionError covers Phase 19 task 1: a non-OK SESSION
// STATUS reply surfaces a *SAMResultError carrying the RESULT.
func TestSAMClientCreateSessionError(t *testing.T) {
	t.Parallel()
	m := newMockSAM(t)
	m.sessionErr = "DUPLICATED_ID"
	defer m.close()
	c := newMockSAMClient(m)

	_, conn, err := c.CreateSession("sess1", "", nil)
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("CreateSession succeeded, want error")
	}
	se, ok := err.(*SAMResultError)
	if !ok || se.Result != "DUPLICATED_ID" {
		t.Fatalf("err = %v, want *SAMResultError DUPLICATED_ID", err)
	}
}

// TestSAMClientStreamConnect covers Phase 19 task 1: StreamConnect performs
// HELLO + STREAM CONNECT and returns the stream conn on OK.
func TestSAMClientStreamConnect(t *testing.T) {
	t.Parallel()
	m := newMockSAM(t)
	defer m.close()
	c := newMockSAMClient(m)

	conn, err := c.StreamConnect("sess1", "DESTB64")
	if err != nil {
		t.Fatalf("StreamConnect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	m.mu.Lock()
	connects := m.connects
	m.mu.Unlock()
	if connects != 1 {
		t.Fatalf("connects=%d want 1", connects)
	}
}

// TestSAMClientStreamConnectError covers Phase 19 task 1: a non-OK STREAM
// STATUS reply surfaces a *SAMResultError.
func TestSAMClientStreamConnectError(t *testing.T) {
	t.Parallel()
	m := newMockSAM(t)
	m.connectErr = "CANT_REACH_PEER"
	defer m.close()
	c := newMockSAMClient(m)

	conn, err := c.StreamConnect("sess1", "DESTB64")
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("StreamConnect succeeded, want error")
	}
	se, ok := err.(*SAMResultError)
	if !ok || se.Result != "CANT_REACH_PEER" {
		t.Fatalf("err = %v, want *SAMResultError CANT_REACH_PEER", err)
	}
}

// TestSAMClientStreamAccept covers Phase 19 task 1: StreamAccept performs HELLO
// + STREAM ACCEPT and returns the stream conn on OK.
func TestSAMClientStreamAccept(t *testing.T) {
	t.Parallel()
	m := newMockSAM(t)
	defer m.close()
	c := newMockSAMClient(m)

	conn, err := c.StreamAccept("sess1")
	if err != nil {
		t.Fatalf("StreamAccept: %v", err)
	}
	defer func() { _ = conn.Close() }()
	m.mu.Lock()
	accepts := m.accepts
	m.mu.Unlock()
	if accepts != 1 {
		t.Fatalf("accepts=%d want 1", accepts)
	}
}

// TestSAMClientDestLookup covers Phase 19 task 1: DestLookup resolves a name
// via NAMING LOOKUP and returns the destination (aiosam.dest_lookup).
func TestSAMClientDestLookup(t *testing.T) {
	t.Parallel()
	m := newMockSAM(t)
	defer m.close()
	c := newMockSAMClient(m)

	dest, err := c.DestLookup("irc.echelon.i2p")
	if err != nil {
		t.Fatalf("DestLookup: %v", err)
	}
	if dest == nil || dest.Base64 != m.destB64 {
		t.Fatalf("dest = %v, want base64 %q", dest, m.destB64)
	}
}

// TestSAMClientNewDestination covers Phase 19 task 1: NewDestination asks the
// SAM API to generate a fresh destination (aiosam.new_destination).
func TestSAMClientNewDestination(t *testing.T) {
	t.Parallel()
	m := newMockSAM(t)
	defer m.close()
	c := newMockSAMClient(m)

	dest, err := c.NewDestination(SAMI2PDefaultSigType)
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	if dest == nil || dest.Base64 != m.destB64 {
		t.Fatalf("dest = %v, want base64 %q", dest, m.destB64)
	}
}

// TestSAMClientHelloFailure covers Phase 19 task 1: a non-OK HELLO reply fails
// the handshake so getSAMConn returns an error.
func TestSAMClientHelloFailure(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		br := bufio.NewReader(conn)
		br.ReadString('\n') // consume HELLO
		io.WriteString(conn, "HELLO REPLY RESULT=I2P_ERROR\n")
	}()
	c := newSAMClient()
	c.Address = l.Addr().String()
	if _, err := c.getSAMConn(); err == nil {
		t.Fatal("getSAMConn succeeded on failing HELLO, want error")
	}
}

// TestSAMDefaultAddress covers Phase 19 task 1: the default SAM address is
// 127.0.0.1:7656 (sam.DEFAULT_ADDRESS).
func TestSAMDefaultAddress(t *testing.T) {
	t.Parallel()
	if got, want := samDefaultAddress(), net.JoinHostPort(SAMDefaultHost, strconv.Itoa(SAMDefaultPort)); got != want {
		t.Fatalf("samDefaultAddress=%q want %q", got, want)
	}
}
