// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bufio"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
)

// I2P SAM protocol constants (RNS/vendor/i2plib/sam.py:16-23). The SAM API
// speaks a line-oriented text protocol on 127.0.0.1:7656; RNS pins the
// negotiated version to 3.1.
const (
	SAMBufferSize        = 4096
	SAMDefaultHost       = "127.0.0.1"
	SAMDefaultPort       = 7656
	SAMDefaultMinVer     = "3.1"
	SAMDefaultMaxVer     = "3.1"
	SAMTransientDest     = "TRANSIENT"
	SAMI2PDefaultSigType = 7 // EdDSA_SHA512_Ed25519 (sam.Destination.default_sig_type)
)

// samDefaultAddress returns the default SAM API host:port string.
func samDefaultAddress() string {
	return net.JoinHostPort(SAMDefaultHost, strconv.Itoa(SAMDefaultPort))
}

// i2pB64Encoding is base64 with the I2P altchars "-~" replacing the standard
// "+/" (sam.py:6, i2p_b64encode).
var i2pB64Encoding = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~")

// i2pB64Encode encodes binary data to an I2P base64 destination string.
func i2pB64Encode(b []byte) string { return i2pB64Encoding.EncodeToString(b) }

// i2pB64Decode decodes an I2P base64 string to binary data.
func i2pB64Decode(s string) ([]byte, error) { return i2pB64Encoding.DecodeString(s) }

// i2pB32Hash returns the 52-char lowercase base32 SHA-256 destination hash for
// binary destination data (sam.Destination.base32). The hash is 32 bytes → 52
// base32 chars; Python lowercases the result.
var i2pB32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func i2pB32Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return strings.ToLower(i2pB32Encoding.EncodeToString(sum[:]))
}

// SAMMessage parses a SAM reply line into its command, action, and key=value
// options (sam.Message). A line is "CMD ACTION OPT1=VAL1 OPT2=VAL2 ..." where a
// bare token (no '=') maps to a true-valued option (sam.py:34-37).
type SAMMessage struct {
	Cmd    string
	Action string
	Opts   map[string]string
	raw    string
}

// ParseSAMMessage parses a single SAM reply line (without trailing newline).
func ParseSAMMessage(line string) *SAMMessage {
	m := &SAMMessage{Opts: map[string]string{}, raw: line}
	parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
	if len(parts) < 2 {
		return m
	}
	m.Cmd = parts[0]
	m.Action = parts[1]
	if len(parts) == 3 {
		for _, tok := range strings.Split(parts[2], " ") {
			if tok == "" {
				continue
			}
			kv := strings.SplitN(tok, "=", 2)
			if len(kv) == 2 {
				m.Opts[kv[0]] = kv[1]
			} else {
				m.Opts[kv[0]] = "true"
			}
		}
	}
	return m
}

// OK reports whether the reply carries RESULT=OK (sam.Message.ok).
func (m *SAMMessage) OK() bool {
	if m == nil {
		return false
	}
	return m.Opts["RESULT"] == "OK"
}

// String returns the raw reply line (sam.Message.__repr__).
func (m *SAMMessage) String() string { return m.raw }

// SAM request message builders (sam.py:48-76). Each returns the wire bytes
// including the trailing newline.

func samHelloBytes(minVer, maxVer string) []byte {
	return []byte(fmt.Sprintf("HELLO VERSION MIN=%s MAX=%s\n", minVer, maxVer))
}

func samSessionCreateBytes(style, sessionID, destination, options string) []byte {
	return []byte(fmt.Sprintf("SESSION CREATE STYLE=%s ID=%s DESTINATION=%s %s\n",
		style, sessionID, destination, options))
}

func samStreamConnectBytes(sessionID, destination, silent string) []byte {
	return []byte(fmt.Sprintf("STREAM CONNECT ID=%s DESTINATION=%s SILENT=%s\n",
		sessionID, destination, silent))
}

func samStreamAcceptBytes(sessionID, silent string) []byte {
	return []byte(fmt.Sprintf("STREAM ACCEPT ID=%s SILENT=%s\n", sessionID, silent))
}

func samStreamForwardBytes(sessionID string, port int, options string) []byte {
	return []byte(fmt.Sprintf("STREAM FORWARD ID=%s PORT=%d %s\n", sessionID, port, options))
}

func samNamingLookupBytes(name string) []byte {
	return []byte(fmt.Sprintf("NAMING LOOKUP NAME=%s\n", name))
}

func samDestGenerateBytes(signatureType int) []byte {
	return []byte(fmt.Sprintf("DEST GENERATE SIGNATURE_TYPE=%d\n", signatureType))
}

// samOptionsString renders an i2cp options map to the "k=v k=v" form Python
// joins with spaces (aiosam.create_session, sam.py:94).
func samOptionsString(opts map[string]string) string {
	pairs := make([]string, 0, len(opts))
	for k, v := range opts {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, " ")
}

// SAMResultError is the error returned when a SAM reply carries a non-OK
// RESULT. The Result string (e.g. "CANT_REACH_PEER") maps to the Python
// SAM_EXCEPTIONS table (exceptions.py:33-41); UnknownResult wraps any
// unrecognized result.
type SAMResultError struct {
	Result string
	Cmd    string
	Action string
}

func (e *SAMResultError) Error() string {
	return fmt.Sprintf("SAM %s %s failed: RESULT=%s", e.Cmd, e.Action, e.Result)
}

// samErrorForResult maps a SAM RESULT string to a sentinel error mirroring the
// Python SAM_EXCEPTIONS table (exceptions.py). Unknown results yield a generic
// I2PError-style error carrying the raw result.
var (
	ErrSAMCantReachPeer  = errors.New("SAM: the peer exists, but cannot be reached")
	ErrSAMDuplicatedDest = errors.New("SAM: the specified destination is already in use")
	ErrSAMDuplicatedID   = errors.New("SAM: the nickname is already associated with a session")
	ErrSAMI2PError       = errors.New("SAM: a generic I2P error")
	ErrSAMInvalidID      = errors.New("SAM: STREAM SESSION ID doesn't exist")
	ErrSAMInvalidKey     = errors.New("SAM: the specified key is not valid")
	ErrSAMKeyNotFound    = errors.New("SAM: the naming system can't resolve the given name")
	ErrSAMPeerNotFound   = errors.New("SAM: the peer cannot be found on the network")
	ErrSAMTimeout        = errors.New("SAM: timeout")
)

func samErrorForResult(result string) error {
	switch result {
	case "CANT_REACH_PEER":
		return ErrSAMCantReachPeer
	case "DUPLICATED_DEST":
		return ErrSAMDuplicatedDest
	case "DUPLICATED_ID":
		return ErrSAMDuplicatedID
	case "I2P_ERROR":
		return ErrSAMI2PError
	case "INVALID_ID":
		return ErrSAMInvalidID
	case "INVALID_KEY":
		return ErrSAMInvalidKey
	case "KEY_NOT_FOUND":
		return ErrSAMKeyNotFound
	case "PEER_NOT_FOUND":
		return ErrSAMPeerNotFound
	case "TIMEOUT":
		return ErrSAMTimeout
	default:
		return fmt.Errorf("%w: RESULT=%s", ErrSAMI2PError, result)
	}
}

// I2PDestination is an I2P destination (sam.Destination). It holds the binary
// destination data and the base64 form; Base32 is the 52-char lowercase
// SHA-256 destination hash.
type I2PDestination struct {
	Data   []byte
	Base64 string
}

// NewI2PDestinationFromB64 builds a destination from a base64 string
// (sam.Destination.__init__ with data).
func NewI2PDestinationFromB64(b64 string) (*I2PDestination, error) {
	data, err := i2pB64Decode(b64)
	if err != nil {
		return nil, err
	}
	return &I2PDestination{Data: data, Base64: b64}, nil
}

// NewI2PDestinationFromData builds a destination from binary data, computing
// the base64 form.
func NewI2PDestinationFromData(data []byte) *I2PDestination {
	return &I2PDestination{Data: data, Base64: i2pB64Encode(data)}
}

// Base32 returns the 52-char lowercase destination hash (sam.Destination.base32).
func (d *I2PDestination) Base32() string { return i2pB32Hash(d.Data) }

// SAMClient speaks the I2P SAMv3 protocol against a SAM API endpoint
// (RNS/vendor/i2plib/aiosam.py). It is synchronous: each method opens a fresh
// control connection (the SAM API multiplexes sessions by ID, so one control
// connection per operation matches Python's get_sam_socket per call). The
// Dialer indirection lets tests inject a mock SAM endpoint.
type SAMClient struct {
	// Address is the SAM API host:port (default 127.0.0.1:7656).
	Address string
	// MinVer/MaxVer pin the negotiated protocol version (default 3.1/3.1).
	MinVer string
	MaxVer string
	// Dialer returns a connection to the SAM API. Defaults to net.Dial; tests
	// substitute a mock SAM listener.
	Dialer func(network, addr string) (net.Conn, error)

	mu sync.Mutex
}

// newSAMClient returns a SAMClient with default settings.
func newSAMClient() *SAMClient {
	return &SAMClient{
		Address: samDefaultAddress(),
		MinVer:  SAMDefaultMinVer,
		MaxVer:  SAMDefaultMaxVer,
		Dialer:  net.Dial,
	}
}

// dial opens a connection to the SAM API via the configured Dialer.
func (c *SAMClient) dial(network string) (net.Conn, error) {
	addr := c.Address
	if addr == "" {
		addr = samDefaultAddress()
	}
	if c.Dialer != nil {
		return c.Dialer(network, addr)
	}
	return net.Dial(network, addr)
}

// readSAMReply reads one SAM reply line from r (aiosam.parse_reply +
// reader.readline).
func readSAMReply(r *bufio.Reader) (*SAMMessage, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, errors.New("SAM: empty response (SAM API went offline)")
	}
	return ParseSAMMessage(strings.TrimRight(line, "\r\n")), nil
}

// hello performs the HELLO VERSION handshake on conn and returns nil on OK
// (aiosam.get_sam_socket). The caller owns conn afterward.
func (c *SAMClient) hello(conn net.Conn) error {
	minVer, maxVer := c.MinVer, c.MaxVer
	if minVer == "" {
		minVer = SAMDefaultMinVer
	}
	if maxVer == "" {
		maxVer = SAMDefaultMaxVer
	}
	if _, err := conn.Write(samHelloBytes(minVer, maxVer)); err != nil {
		return err
	}
	br := bufio.NewReader(conn)
	reply, err := readSAMReply(br)
	if err != nil {
		return err
	}
	if !reply.OK() {
		return &SAMResultError{Result: reply.Opts["RESULT"], Cmd: reply.Cmd, Action: reply.Action}
	}
	return nil
}

// getSAMConn opens a new SAM connection and completes the HELLO handshake,
// returning the live conn (aiosam.get_sam_socket). Caller closes the conn.
func (c *SAMClient) getSAMConn() (net.Conn, error) {
	conn, err := c.dial("tcp")
	if err != nil {
		return nil, err
	}
	if err := c.hello(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// CreateSession creates a SAM STREAM session with the given ID. When
// destination is empty a TRANSIENT destination is used; the created
// destination is returned (aiosam.create_session). The returned conn is the
// session control connection; closing it tears down the session
// (tunnel.I2PTunnel.stop closes session_writer).
func (c *SAMClient) CreateSession(sessionID, destination string, opts map[string]string) (*I2PDestination, net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, err := c.getSAMConn()
	if err != nil {
		return nil, nil, err
	}
	dest := destination
	if dest == "" {
		dest = SAMTransientDest
	}
	if _, err := conn.Write(samSessionCreateBytes("STREAM", sessionID, dest, samOptionsString(opts))); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	reply, err := readSAMReply(br)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if !reply.OK() {
		_ = conn.Close()
		return nil, nil, &SAMResultError{Result: reply.Opts["RESULT"], Cmd: reply.Cmd, Action: reply.Action}
	}
	created, err := NewI2PDestinationFromB64(reply.Opts["DESTINATION"])
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("SAM: decode session DESTINATION: %w", err)
	}
	return created, conn, nil
}

// SAMStream is a duplex I2P stream carrying data after the SAM reply. It wraps
// the SAM conn for writes and a *bufio.Reader for reads so bytes the SAM API
// sent alongside the status reply (e.g. the ServerTunnel remote-destination
// line and any data arriving in the same chunk) are preserved rather than
// discarded by the reply bufio.Reader.
type SAMStream struct {
	br   *bufio.Reader
	conn net.Conn
}

// Read reads stream data via the buffered reader.
func (s *SAMStream) Read(p []byte) (int, error) { return s.br.Read(p) }

// Write writes stream data to the underlying SAM conn.
func (s *SAMStream) Write(p []byte) (int, error) { return s.conn.Write(p) }

// Close closes the underlying SAM conn.
func (s *SAMStream) Close() error { return s.conn.Close() }

// Conn returns the underlying net.Conn for callers that need it (e.g. to set
// deadlines). Reads must still go through Read so buffered bytes are not lost.
func (s *SAMStream) Conn() net.Conn { return s.conn }

// StreamConnect establishes a stream from sessionID to the remote base64
// destination and returns the duplex stream (aiosam.stream_connect).
func (c *SAMClient) StreamConnect(sessionID, destinationB64 string) (*SAMStream, error) {
	conn, err := c.getSAMConn()
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(samStreamConnectBytes(sessionID, destinationB64, "false")); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	reply, err := readSAMReply(br)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !reply.OK() {
		_ = conn.Close()
		return nil, &SAMResultError{Result: reply.Opts["RESULT"], Cmd: reply.Cmd, Action: reply.Action}
	}
	return &SAMStream{br: br, conn: conn}, nil
}

// StreamAccept waits for an inbound stream on sessionID and returns the duplex
// stream (aiosam.stream_accept). The first bytes read from the stream are the
// remote destination line followed by any initial data the peer sent
// (tunnel.ServerTunnel.handle_client splits on "\n").
func (c *SAMClient) StreamAccept(sessionID string) (*SAMStream, error) {
	pending, err := c.OpenStreamAccept(sessionID)
	if err != nil {
		return nil, err
	}
	return pending.Wait()
}

// SAMAcceptPending is an in-flight STREAM ACCEPT: the SAM conn is open and the
// STREAM ACCEPT command has been sent, but the status reply (which arrives
// only when a peer connects) has not yet been read. The caller owns the conn:
// Wait reads the reply and returns the stream; Cancel closes the conn to
// interrupt a blocked Wait (ServerTunnel.Close cancels its in-flight accept so
// the accept loop exits deterministically, mirroring asyncio task cancellation
// in tunnel.server_loop).
type SAMAcceptPending struct {
	br   *bufio.Reader
	conn net.Conn
}

// OpenStreamAccept opens a SAM conn, completes HELLO, sends STREAM ACCEPT, and
// returns the pending accept without reading the reply. The caller owns the
// conn via the returned pending and must call Wait or Cancel.
func (c *SAMClient) OpenStreamAccept(sessionID string) (*SAMAcceptPending, error) {
	conn, err := c.getSAMConn()
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(samStreamAcceptBytes(sessionID, "false")); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &SAMAcceptPending{br: bufio.NewReader(conn), conn: conn}, nil
}

// Wait reads the STREAM STATUS reply and returns the stream on OK. On any error
// or non-OK reply the conn is closed.
func (p *SAMAcceptPending) Wait() (*SAMStream, error) {
	reply, err := readSAMReply(p.br)
	if err != nil {
		_ = p.conn.Close()
		return nil, err
	}
	if !reply.OK() {
		_ = p.conn.Close()
		return nil, &SAMResultError{Result: reply.Opts["RESULT"], Cmd: reply.Cmd, Action: reply.Action}
	}
	return &SAMStream{br: p.br, conn: p.conn}, nil
}

// Cancel closes the underlying conn, interrupting a blocked Wait.
func (p *SAMAcceptPending) Cancel() { _ = p.conn.Close() }

// DestLookup resolves a .i2p domain or .b32.i2p address to a full destination
// (aiosam.dest_lookup).
func (c *SAMClient) DestLookup(name string) (*I2PDestination, error) {
	conn, err := c.getSAMConn()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(samNamingLookupBytes(name)); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	reply, err := readSAMReply(br)
	if err != nil {
		return nil, err
	}
	if !reply.OK() {
		return nil, &SAMResultError{Result: reply.Opts["RESULT"], Cmd: reply.Cmd, Action: reply.Action}
	}
	return NewI2PDestinationFromB64(reply.Opts["VALUE"])
}

// NewDestination asks the SAM API to generate a fresh destination with a
// private key of the given signature type (aiosam.new_destination). The
// returned destination carries the base64 PRIV form.
func (c *SAMClient) NewDestination(sigType int) (*I2PDestination, error) {
	if sigType == 0 {
		sigType = SAMI2PDefaultSigType
	}
	conn, err := c.getSAMConn()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(samDestGenerateBytes(sigType)); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	reply, err := readSAMReply(br)
	if err != nil {
		return nil, err
	}
	if !reply.OK() {
		return nil, &SAMResultError{Result: reply.Opts["RESULT"], Cmd: reply.Cmd, Action: reply.Action}
	}
	return NewI2PDestinationFromB64(reply.Opts["PRIV"])
}

// generateSessionID returns a session nickname, mirroring
// utils.generate_session_id (a short hex string). Callers that need uniqueness
// pass an explicit ID; this is the fallback for tunnels without one.
func generateSessionID() string {
	return "rnsgo"
}
