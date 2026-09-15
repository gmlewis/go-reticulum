// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// i2pTunnelBufferSize mirrors tunnel.BUFFER_SIZE (65536), the copy chunk size
// for the bidirectional proxy.
const i2pTunnelBufferSize = 65536

// i2pTunnelLogger is the default logger for I2P tunnel setup diagnostics.
// A tunnel with a nil Logger writes through this.
var i2pTunnelLogger = log.New(os.Stderr, "", log.LstdFlags)

// i2pDialTimeout is the 5s timeout Python waits when dialing the local service
// in ServerTunnel.handle_client (tunnel.py:155).
const i2pDialTimeout = 5 * time.Second

// i2pTunnelDrainTimeout bounds how long Close waits for the tracked proxy
// goroutines. Close closes every registered conn before draining, so the drain
// completes at once in normal operation. The bound mirrors Python's shutdown,
// which cancels the tunnel's tasks and returns instead of waiting for them
// (I2PController.stop), and keeps a future registration leak from consuming a
// whole test-suite timeout as a hang.
const i2pTunnelDrainTimeout = 5 * time.Second

// I2PTunnel is the base I2P tunnel (tunnel.I2PTunnel). It holds a SAM session
// and proxies data between a local TCP endpoint and the I2P network.
// Subclasses (ClientTunnel, ServerTunnel) set Style and implement Run.
//
// Goroutine retention: background proxy goroutines are
// tracked in bgWG + the activeConns set so they are never fire-and-forget.
// Close drains them via bgWG before returning, and activeConns retains the
// live proxy conns so they are not reaped while the tunnel is live
// (tunnel.py:96-102 status["background_tasks"]).
type I2PTunnel struct {
	// LocalAddress is the local host:port the tunnel binds/contacts.
	LocalAddress string
	// Destination is the I2P destination used for the session; nil = TRANSIENT
	// (a new destination is generated via the SAM API on Run).
	Destination *I2PDestination
	// SessionName is the SAM session nickname; empty = generated.
	SessionName string
	// Options are i2cp session options.
	Options map[string]string
	// SAM is the SAM client; defaults to newSAMClient().
	SAM *SAMClient
	// Style is the SAM session style ("STREAM" for both tunnel types).
	Style string
	// Logger receives tunnel setup diagnostics. nil uses the
	// package default (stderr); tests inject a buffered logger to assert the
	// catch-all "Unspecified I2P daemon error" log.
	Logger *log.Logger

	mu          sync.Mutex
	sessionConn net.Conn
	listener    net.Listener
	stopped     bool
	// pendingAccept is the in-flight STREAM ACCEPT the ServerTunnel is blocked
	// on; Close cancels it so the accept loop exits deterministically.
	pendingAccept *SAMAcceptPending
	// onAcceptOpened, when non-nil, is invoked after a ServerTunnel has sent
	// STREAM ACCEPT and before the pending is tracked. It lets deterministic
	// tests park the accept loop in the Close-vs-track window using channel
	// synchronization (no time.Sleep); production leaves it nil.
	onAcceptOpened func()
	// onConnReady, when non-nil, is invoked by both accept loops once a stream
	// is ready to be proxied and before its conn is registered in the active
	// set. It lets deterministic tests park the loop in the Close-vs-register
	// window using channel synchronization (no time.Sleep); production leaves
	// it nil.
	onConnReady func()
	// bgWG tracks all background proxy goroutines so Close drains them.
	bgWG sync.WaitGroup
	// activeConns retains the live proxy conns so they are not reaped while
	// the tunnel is running.
	activeConns map[net.Conn]struct{}
}

// sessionID returns the tunnel's session nickname, generating one when unset
// (utils.generate_session_id).
func (t *I2PTunnel) sessionID() string {
	if t.SessionName != "" {
		return t.SessionName
	}
	return generateSessionID()
}

// logger returns the tunnel's diagnostic logger, falling back to the package
// default when no Logger is set.
func (t *I2PTunnel) logger() *log.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return i2pTunnelLogger
}

// logSetupError mirrors the I2PInterface.py setup-error classification chain
// (client: lines 196-205, server: 288-294). A known SAM RESULT logs its specific
// message; an unknown RESULT (which Python raises as a KeyError — not a known
// i2plib exception) and any non-SAM-protocol error fall to the else catch-all
// "Unspecified I2P daemon error".
func (t *I2PTunnel) logSetupError(err error) {
	lg := t.logger()
	if se, ok := err.(*SAMResultError); ok {
		switch se.Result {
		case "CANT_REACH_PEER":
			lg.Printf("The I2P daemon can't reach peer")
		case "DUPLICATED_DEST":
			lg.Printf("The I2P daemon reported that the destination is already in use")
		case "DUPLICATED_ID":
			lg.Printf("The I2P daemon reported that the ID is already in use")
		case "INVALID_ID":
			lg.Printf("The I2P daemon reported that the stream session ID doesn't exist")
		case "INVALID_KEY":
			lg.Printf("The I2P daemon reported that the key is invalid")
		case "KEY_NOT_FOUND":
			lg.Printf("The I2P daemon could not find the key")
		case "PEER_NOT_FOUND":
			lg.Printf("The I2P daemon could not find the peer")
		case "I2P_ERROR":
			lg.Printf("The I2P daemon experienced an unspecified error")
		case "TIMEOUT":
			lg.Printf("I2P daemon timed out while setting up tunnel")
		default:
			lg.Printf("Unspecified I2P daemon error: RESULT=%v", se.Result)
		}
		return
	}
	lg.Printf("Unspecified I2P daemon error: %v", err)
}

// preRun creates the destination (if none) and the SAM session, mirroring
// tunnel.I2PTunnel._pre_run (tunnel.py:53-59). It stores the session control
// conn; closing it tears the session down.
func (t *I2PTunnel) preRun() error {
	if t.SAM == nil {
		t.SAM = newSAMClient()
	}
	if t.Destination == nil {
		dest, err := t.SAM.NewDestination(SAMI2PDefaultSigType)
		if err != nil {
			t.logSetupError(err)
			return err
		}
		t.Destination = dest
	}
	dest := ""
	if t.Destination != nil {
		dest = t.Destination.Base64
	}
	_, conn, err := t.SAM.CreateSession(t.sessionID(), dest, t.Options)
	if err != nil {
		t.logSetupError(err)
		return err
	}
	t.mu.Lock()
	t.sessionConn = conn
	t.mu.Unlock()
	return nil
}

// trackConn registers a proxy conn in the active set without checking whether
// the tunnel is still running. Registration that can race with Close must use
// trackConnIfLive instead: a conn registered after Close has snapshotted the
// active set is never closed by Close.
func (t *I2PTunnel) trackConn(c net.Conn) {
	t.mu.Lock()
	if t.activeConns == nil {
		t.activeConns = map[net.Conn]struct{}{}
	}
	t.activeConns[c] = struct{}{}
	t.mu.Unlock()
}

// trackConnIfLive registers c in the active set, but only while the tunnel is
// still running. It returns false when the tunnel has been closed, in which case
// the caller owns c and must close it.
//
// Registration must be atomic with Close's decision to stop, because Close
// snapshots the active set and then waits for the proxy goroutines: a conn
// registered after that snapshot is unreachable by Close, so nothing ever closes
// it and its copy stays blocked on a peer forever. Checking stopped and
// registering under the one lock Close uses to set stopped closes that window
// (the same shape as trackPendingIfLive for the in-flight STREAM ACCEPT).
func (t *I2PTunnel) trackConnIfLive(c net.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	if t.activeConns == nil {
		t.activeConns = map[net.Conn]struct{}{}
	}
	t.activeConns[c] = struct{}{}
	return true
}

// untrackConn removes a proxy conn from the active set.
func (t *I2PTunnel) untrackConn(c net.Conn) {
	t.mu.Lock()
	delete(t.activeConns, c)
	t.mu.Unlock()
}

// stopSession tears down the SAM session by closing its control conn
// (tunnel.I2PTunnel.stop closes session_writer).
func (t *I2PTunnel) stopSession() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionConn != nil {
		_ = t.sessionConn.Close()
		t.sessionConn = nil
	}
}

// isStopped reports whether the tunnel has been closed.
func (t *I2PTunnel) isStopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

// closeActiveConns closes every retained proxy conn. Called during Close so
// blocked proxy goroutines unblock and bgWG drains.
func (t *I2PTunnel) closeActiveConns() {
	t.mu.Lock()
	conns := make([]net.Conn, 0, len(t.activeConns))
	for c := range t.activeConns {
		conns = append(conns, c)
	}
	t.activeConns = nil
	t.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// drainBackground waits for the tracked background goroutines to exit, bounded
// by i2pTunnelDrainTimeout. Every registered conn has already been closed when
// it is called, so the copies unblock immediately; the bound reports a leak
// instead of hanging the caller forever.
func (t *I2PTunnel) drainBackground() error {
	done := make(chan struct{})
	go func() {
		t.bgWG.Wait()
		close(done)
	}()
	timer := time.NewTimer(i2pTunnelDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("I2P tunnel %v: background proxy goroutines still running %v after close",
			t.sessionID(), i2pTunnelDrainTimeout)
	}
}

// Close tears the tunnel down: stops accepting, tears down the SAM session,
// cancels any in-flight accept, drains the background proxy goroutines, and
// closes the retained proxy conns. It blocks until all proxies have exited
// (goroutines are tracked, never fire-and-forget), bounded by
// i2pTunnelDrainTimeout so that a leaked conn cannot hang the caller forever.
func (t *I2PTunnel) Close() error {
	t.mu.Lock()
	t.stopped = true
	listener := t.listener
	t.listener = nil
	pending := t.pendingAccept
	t.pendingAccept = nil
	t.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	if pending != nil {
		pending.Cancel()
	}
	t.stopSession()
	// Close the retained proxy conns first so the proxy goroutines unblock,
	// then drain them. Waiting before closing would deadlock.
	t.closeActiveConns()
	return t.drainBackground()
}

// trackPendingIfLive records the in-flight STREAM ACCEPT for cancellation by
// Close, but only if the tunnel is still running. It returns true when the
// pending was tracked (tunnel live); false when the tunnel was already stopped,
// in which case the pending is NOT tracked and the caller must Cancel it and
// exit. Performing the track-and-check atomically under one lock closes the
// window where Close ran between OpenStreamAccept returning and the pending
// being tracked: Close saw pendingAccept=nil and could not cancel it, so the
// accept loop blocked in pending.Wait forever and Close's bgWG.Wait hung
// (mirroring asyncio task cancellation in tunnel.server_loop).
func (t *I2PTunnel) trackPendingIfLive(p *SAMAcceptPending) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.pendingAccept = p
	return true
}
func (t *I2PTunnel) untrackPending() {
	t.mu.Lock()
	t.pendingAccept = nil
	t.mu.Unlock()
}

// ClientTunnel is an I2P client tunnel (tunnel.ClientTunnel): all connections
// to LocalAddress are proxied to RemoteDestination over the I2P SAM session.
type ClientTunnel struct {
	I2PTunnel
	// RemoteDestination is the base64 I2P destination (or .i2p/.b32 address,
	// resolved separately) the tunnel connects to.
	RemoteDestination string
}

// NewClientTunnel returns a ClientTunnel bound to localAddress proxying to
// remoteDestination (tunnel.ClientTunnel.__init__).
func NewClientTunnel(remoteDestination, localAddress string) *ClientTunnel {
	return &ClientTunnel{
		I2PTunnel:         I2PTunnel{LocalAddress: localAddress, Style: "STREAM"},
		RemoteDestination: remoteDestination,
	}
}

// Run starts the client tunnel (tunnel.ClientTunnel.run): it creates the SAM
// session, listens on LocalAddress, and for each accepted local client opens a
// SAM stream to RemoteDestination and proxies bidirectionally. It blocks
// until Close closes the listener; the accept loop runs in a background
// goroutine so Run returns after setup.
func (t *ClientTunnel) Run() error {
	if err := t.preRun(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", t.LocalAddress)
	if err != nil {
		t.stopSession()
		return err
	}
	t.mu.Lock()
	t.listener = ln
	t.mu.Unlock()
	t.bgWG.Add(1)
	go t.acceptLoop(ln)
	return nil
}

func (t *ClientTunnel) acceptLoop(ln net.Listener) {
	defer t.bgWG.Done()
	for {
		client, err := ln.Accept()
		if err != nil {
			return
		}
		if t.isStopped() {
			_ = client.Close()
			return
		}
		stream, err := t.SAM.StreamConnect(t.sessionID(), t.RemoteDestination)
		if err != nil {
			if t.isStopped() {
				_ = client.Close()
				return
			}
			log.Printf("I2P ClientTunnel %v: stream connect failed: %v", t.sessionID(), err)
			_ = client.Close()
			continue
		}
		if t.onConnReady != nil {
			t.onConnReady()
		}
		// proxyPair registers both conns only while the tunnel is live and closes
		// them when it cannot: registering after Close has snapshotted the active
		// set would leak them, and both copies would then block forever.
		if !t.proxyPair(stream, client) {
			return
		}
	}
}

// ServerTunnel is an I2P server tunnel (tunnel.ServerTunnel): it exposes the
// local service at LocalAddress to the I2P network. Inbound I2P streams are
// proxied to LocalAddress.
type ServerTunnel struct {
	I2PTunnel
}

// NewServerTunnel returns a ServerTunnel exposing localAddress to the I2P
// network (tunnel.ServerTunnel.__init__).
func NewServerTunnel(localAddress string) *ServerTunnel {
	return &ServerTunnel{I2PTunnel{LocalAddress: localAddress, Style: "STREAM"}}
}

// Run starts the server tunnel (tunnel.ServerTunnel.run): it creates the SAM
// session and loops accepting inbound streams. For each stream it reads the
// remote destination line, dials LocalAddress, and proxies bidirectionally.
func (t *ServerTunnel) Run() error {
	if err := t.preRun(); err != nil {
		return err
	}
	t.bgWG.Add(1)
	go t.acceptLoop()
	return nil
}

func (t *ServerTunnel) acceptLoop() {
	defer t.bgWG.Done()
	for {
		if t.isStopped() {
			return
		}
		pending, err := t.SAM.OpenStreamAccept(t.sessionID())
		if err != nil {
			if t.isStopped() {
				return
			}
			log.Printf("I2P ServerTunnel %v: open accept failed: %v", t.sessionID(), err)
			continue
		}
		if t.onAcceptOpened != nil {
			t.onAcceptOpened()
		}
		// Track the in-flight accept for cancellation by Close. If Close ran
		// while OpenStreamAccept was in flight (after it sent STREAM ACCEPT but
		// before tracking), the tunnel is already stopped: cancel the
		// freshly-opened pending ourselves and exit so Close's bgWG.Wait returns.
		if !t.trackPendingIfLive(pending) {
			pending.Cancel()
			return
		}
		stream, err := pending.Wait()
		t.untrackPending()
		if err != nil {
			if t.isStopped() {
				return
			}
			continue
		}
		if t.onConnReady != nil {
			t.onConnReady()
		}
		// Register the stream conn only while the tunnel is live. The accept loop
		// owns the stream until the handler takes it over, so it must close the
		// stream itself when Close has already snapshotted the active conns.
		if !t.trackConnIfLive(stream.Conn()) {
			_ = stream.Close()
			return
		}
		// Serve the inbound stream in a tracked background goroutine so the
		// accept loop continues to the next accept (tunnel.py:178-181
		// asyncio.ensure_future(handle_client, ...)).
		t.bgWG.Go(func() { t.handleServerClient(stream) })
	}
}

// handleServerClient reads the remote-destination line from the inbound stream,
// dials LocalAddress, and proxies bidirectionally (tunnel.ServerTunnel.
// handle_client). A local dial failure closes the stream and logs.
func (t *ServerTunnel) handleServerClient(stream *SAMStream) {
	// The accept loop registered the stream conn before starting this handler.
	// Release that registration on every path that does not hand the stream to a
	// proxy pair: proxyPair takes over both conns and unregisters them when the
	// copies finish. Unregistering the stream before the pair starts would put it
	// out of Close's reach while its copy is still running.
	proxied := false
	defer func() {
		if !proxied {
			t.untrackConn(stream.Conn())
		}
	}()
	// Read the remote destination line; data may follow in the same chunk
	// (tunnel.py:144-147).
	line, err := stream.br.ReadString('\n')
	if err != nil && line == "" {
		log.Printf("I2P ServerTunnel %v: read remote destination failed: %v", t.sessionID(), err)
		_ = stream.Close()
		return
	}
	remoteDest := strings.TrimRight(line, "\r\n")
	if remoteDest == "" {
		log.Printf("I2P ServerTunnel %v: empty remote destination", t.sessionID())
		_ = stream.Close()
		return
	}
	local, err := net.DialTimeout("tcp", t.LocalAddress, i2pDialTimeout)
	if err != nil {
		log.Printf("I2P ServerTunnel %v: dial local %v failed: %v", t.sessionID(), t.LocalAddress, err)
		_ = stream.Close()
		return
	}
	// If the dest line and data arrived together, the bufio.Reader has the
	// leftover data buffered; write it to the local service before proxying.
	if leftover := stream.br.Buffered(); leftover > 0 {
		buf := make([]byte, leftover)
		if _, err := io.ReadFull(stream.br, buf); err == nil {
			if _, err := local.Write(buf); err != nil {
				// proxyPair re-drives the flow, but this initial payload is
				// lost — say so rather than dropping it silently.
				log.Printf("I2P ServerTunnel %v: writing %v leftover bytes to local service failed: %v",
					t.sessionID(), leftover, err)
			}
		}
	}
	proxied = t.proxyPair(stream, local)
}

// proxyPair runs two tracked proxy goroutines copying in each direction between
// the SAM stream a and the net.Conn b (tunnel.handle_client's two proxy_data
// tasks). Both conns are registered in the active set atomically with respect to
// Close; when the tunnel has already closed it returns false after closing both
// conns and starting no goroutine. Each goroutine untracks its read-side conn on
// completion and drains the tunnel-wide bgWG so Close waits for the proxies.
func (t *I2PTunnel) proxyPair(a *SAMStream, b net.Conn) bool {
	if !t.trackConnIfLive(b) {
		_ = b.Close()
		_ = a.Close()
		return false
	}
	if !t.trackConnIfLive(a.Conn()) {
		t.untrackConn(b)
		_ = b.Close()
		_ = a.Close()
		return false
	}
	t.bgWG.Add(2)
	go func() {
		defer t.bgWG.Done()
		_, _ = io.CopyBuffer(b, a, make([]byte, i2pTunnelBufferSize))
		_ = b.Close()
		t.untrackConn(b)
	}()
	go func() {
		defer t.bgWG.Done()
		_, _ = io.CopyBuffer(a, b, make([]byte, i2pTunnelBufferSize))
		_ = a.Close()
		t.untrackConn(a.Conn())
	}()
	return true
}
