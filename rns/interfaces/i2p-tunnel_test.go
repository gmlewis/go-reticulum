// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockSAMBridge is a SAM API endpoint that drives real stream data for tunnel
// tests. On STREAM CONNECT it replies OK then runs connectHandler on the conn
// (the "remote I2P peer" for a ClientTunnel). On STREAM ACCEPT it replies OK,
// writes the remote-destination line, then runs acceptHandler on the conn (the
// "incoming I2P peer" for a ServerTunnel). Handlers own the conn for the
// exchange; the bridge closes it when the handler returns.
type mockSAMBridge struct {
	t              *testing.T
	listener       net.Listener
	wg             sync.WaitGroup
	destB64        string
	connectHandler func(net.Conn)
	acceptHandler  func(net.Conn)
	// holdAccept, when set, makes the bridge hold a STREAM ACCEPT connection
	// open without replying, so the tunnel's pending.Wait blocks. acceptReceived
	// is closed when the STREAM ACCEPT command arrives.
	holdAccept      bool
	acceptReceived  chan struct{}
	acceptCloseOnce sync.Once
}

func newMockSAMBridge(t *testing.T) *mockSAMBridge {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bridge listen: %v", err)
	}
	b := &mockSAMBridge{
		t:        t,
		listener: l,
		destB64:  i2pB64Encode([]byte("bridge-destination-payload")),
	}
	b.wg.Add(1)
	go b.serve()
	return b
}

func (b *mockSAMBridge) addr() string { return b.listener.Addr().String() }
func (b *mockSAMBridge) close()       { _ = b.listener.Close(); b.wg.Wait() }

func (b *mockSAMBridge) serve() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		b.wg.Add(1)
		go b.handle(conn)
	}
}

func (b *mockSAMBridge) handle(conn net.Conn) {
	defer b.wg.Done()
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "HELLO"):
			_, _ = io.WriteString(conn, "HELLO REPLY RESULT=OK\n")
		case strings.HasPrefix(line, "SESSION CREATE"):
			_, _ = io.WriteString(conn, "SESSION STATUS RESULT=OK DESTINATION="+b.destB64+"\n")
			// The session control conn stays open for the tunnel's lifetime;
			// return without closing so the tunnel keeps it. The deferred
			// close will close it when the tunnel closes the session.
			return
		case strings.HasPrefix(line, "STREAM CONNECT"):
			_, _ = io.WriteString(conn, "STREAM STATUS RESULT=OK\n")
			if b.connectHandler != nil {
				b.connectHandler(conn)
			}
			return
		case strings.HasPrefix(line, "STREAM ACCEPT"):
			if b.holdAccept {
				// Hold the conn open without replying so the tunnel's
				// pending.Wait blocks reading the status reply; signal that
				// the ACCEPT command arrived so a test can then Close the
				// tunnel and assert Cancel unblocks it. The Once makes the
				// close idempotent (no field write after setup → no race
				// with the test reading acceptReceived).
				b.acceptCloseOnce.Do(func() { close(b.acceptReceived) })
				_, _ = io.Copy(io.Discard, conn)
				return
			}
			_, _ = io.WriteString(conn, "STREAM STATUS RESULT=OK\n")
			// Write the remote-destination line the ServerTunnel reads before
			// any data (tunnel.py:144-147).
			_, _ = io.WriteString(conn, b.destB64+"\n")
			if b.acceptHandler != nil {
				b.acceptHandler(conn)
			}
			return
		default:
			return
		}
	}
}

// reservePort returns a free TCP port for a local tunnel listener.
func reservePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	return addr.Port
}

// TestClientTunnelProxiesData covers Phase 19 task 1: a ClientTunnel listens
// on a local address, and a local client connecting there is proxied over a
// SAM stream to the remote destination (tunnel.ClientTunnel.run/handle_client).
// The bridge acts as the remote peer, echoing data back through the tunnel.
func TestClientTunnelProxiesData(t *testing.T) {
	t.Parallel()
	bridge := newMockSAMBridge(t)
	bridge.connectHandler = func(c net.Conn) {
		// Remote peer echoes.
		_, _ = io.Copy(c, c)
	}
	defer bridge.close()

	localPort := reservePort(t)
	ct := NewClientTunnel("REMOTEDESTB64", net.JoinHostPort("127.0.0.1", itoa(localPort)))
	ct.SAM = newSAMClient()
	ct.SAM.Address = bridge.addr()
	ct.Destination = NewI2PDestinationFromData([]byte("client-tunnel-dest"))
	if err := ct.Run(); err != nil {
		t.Fatalf("ClientTunnel.Run: %v", err)
	}
	defer func() { _ = ct.Close() }()

	conn, err := net.Dial("tcp", ct.LocalAddress)
	if err != nil {
		t.Fatalf("dial local tunnel: %v", err)
	}
	defer func() { _ = conn.Close() }()

	want := "hello-i2p-client-tunnel"
	if _, err := conn.Write([]byte(want)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf[:n]) != want {
		t.Fatalf("echo = %q want %q", buf[:n], want)
	}
}

// TestServerTunnelProxiesData covers Phase 19 task 1: a ServerTunnel accepts an
// inbound I2P stream, reads the remote-destination line, dials the local
// service, and proxies bidirectionally (tunnel.ServerTunnel.run/handle_client).
// The bridge acts as the incoming peer; a local echo server is the exposed
// service.
func TestServerTunnelProxiesData(t *testing.T) {
	t.Parallel()
	// Local service S: echo.
	svcLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svcLn.Close() }()
	go func() {
		for {
			c, err := svcLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { _, _ = io.Copy(c, c); _ = c.Close() }(c)
		}
	}()

	bridge := newMockSAMBridge(t)
	got := make(chan string, 1)
	var did atomic.Int32
	bridge.acceptHandler = func(c net.Conn) {
		// Incoming peer: on the first accept, send data and read the local
		// service's echo back. On subsequent accepts the accept loop reaches
		// here only if the tunnel is still running; just hold the conn open
		// until Close closes it (idempotent — avoids a second exchange racing
		// with the test's single `got` send).
		if did.Add(1) == 1 {
			want := "hello-i2p-server-tunnel"
			if _, err := c.Write([]byte(want)); err != nil {
				got <- "write err"
				return
			}
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			buf := make([]byte, 64)
			n, err := c.Read(buf)
			if err != nil {
				got <- "read err: " + err.Error()
				return
			}
			got <- string(buf[:n])
		}
		// Hold the conn open until Close closes it so the proxy goroutine
		// exits cleanly via Close's closeActiveConns.
		_, _ = io.Copy(io.Discard, c)
	}
	defer bridge.close()

	st := NewServerTunnel(svcLn.Addr().String())
	st.SAM = newSAMClient()
	st.SAM.Address = bridge.addr()
	st.Destination = NewI2PDestinationFromData([]byte("server-tunnel-dest"))
	if err := st.Run(); err != nil {
		t.Fatalf("ServerTunnel.Run: %v", err)
	}
	defer func() { _ = st.Close() }()

	select {
	case echoed := <-got:
		if echoed != "hello-i2p-server-tunnel" {
			t.Fatalf("server tunnel echo = %q want %q", echoed, "hello-i2p-server-tunnel")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for server tunnel proxy exchange")
	}
}

// TestClientTunnelCloseDrainsProxies covers Phase 19 task 2: an active proxy
// goroutine is tracked (activeConns non-empty while the tunnel is live) and
// Close drains it so no proxy is fire-and-forget (tunnel.py:96-102
// background_tasks set). peerDone proves Close actually closes the retained
// proxy conns — a fire-and-forget Close that returned instantly without
// draining would leave the peer's io.Copy blocked forever.
func TestClientTunnelCloseDrainsProxies(t *testing.T) {
	t.Parallel()
	bridge := newMockSAMBridge(t)
	peerStarted := make(chan struct{})
	peerDone := make(chan struct{})
	bridge.connectHandler = func(c net.Conn) {
		close(peerStarted)
		// Hold the peer open so the proxy stays active until Close closes it.
		_, _ = io.Copy(io.Discard, c)
		close(peerDone)
	}
	defer bridge.close()

	localPort := reservePort(t)
	ct := NewClientTunnel("REMOTEDESTB64", net.JoinHostPort("127.0.0.1", itoa(localPort)))
	ct.SAM = newSAMClient()
	ct.SAM.Address = bridge.addr()
	ct.Destination = NewI2PDestinationFromData([]byte("drain-dest"))
	if err := ct.Run(); err != nil {
		t.Fatalf("ClientTunnel.Run: %v", err)
	}

	conn, err := net.Dial("tcp", ct.LocalAddress)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	<-peerStarted

	// While the proxy is live the tunnel retains the proxy conns (the
	// background_tasks set in Python). trackConn races with the peer's
	// connect handler, so poll briefly for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ct.mu.Lock()
		active := len(ct.activeConns)
		ct.mu.Unlock()
		if active > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	ct.mu.Lock()
	active := len(ct.activeConns)
	ct.mu.Unlock()
	if active == 0 {
		t.Fatal("activeConns empty during live proxy; want retained refs (task 2)")
	}

	// Close must drain the proxies and return.
	done := make(chan struct{})
	go func() {
		_ = ct.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not drain proxies within timeout")
	}
	// The retained proxy conns were closed by Close, so the peer's io.Copy
	// unblocked and the proxy goroutine actually exited (not fire-and-forget).
	select {
	case <-peerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy goroutine did not exit after Close; conns not retained/closed (task 2)")
	}
}

// TestServerTunnelCloseCancelsAccept covers Phase 19 task 2: Close cancels the
// in-flight STREAM ACCEPT so the accept loop exits deterministically. The bridge
// holds the ACCEPT conn open without replying (holdAccept) and signals when the
// ACCEPT command arrives, so the tunnel is guaranteed to be blocked in
// pending.Wait when Close runs; Close's Cancel closes that conn, pending.Wait
// errors, the loop sees isStopped, and Close returns.
func TestServerTunnelCloseCancelsAccept(t *testing.T) {
	t.Parallel()
	bridge := newMockSAMBridge(t)
	bridge.holdAccept = true
	bridge.acceptReceived = make(chan struct{})
	defer bridge.close()

	st := NewServerTunnel("127.0.0.1:1") // local service need not exist
	st.SAM = newSAMClient()
	st.SAM.Address = bridge.addr()
	st.Destination = NewI2PDestinationFromData([]byte("cancel-dest"))
	if err := st.Run(); err != nil {
		t.Fatalf("ServerTunnel.Run: %v", err)
	}

	// Wait until the accept loop has sent STREAM ACCEPT and is blocked in
	// pending.Wait (trackPending done, conn held open by the bridge).
	select {
	case <-bridge.acceptReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for STREAM ACCEPT to arrive at the bridge")
	}

	done := make(chan struct{})
	go func() {
		_ = st.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ServerTunnel.Close did not return (accept not cancelled)")
	}
}

// TestServerTunnelCloseTrackPendingRace deterministically exercises the
// Close-vs-trackPendingIfLive race that intermittently hung the suite under
// load: Close runs while the accept loop has already sent STREAM ACCEPT and
// returned from OpenStreamAccept, but has not yet tracked the pending. In that
// window pendingAccept is nil, so Close cannot cancel it. The fixed accept loop
// re-checks stopped after tracking and self-cancels; the old code blocked in
// pending.Wait forever and Close's bgWG.Wait hung.
//
// The race is forced with channel/goroutine synchronization (no time.Sleep): a
// test-only hook parks the accept loop in the window, then spins on isStopped
// (yielding via runtime.Gosched, never time.Sleep) until Close has set stopped
// — guaranteeing Close already saw pendingAccept == nil — before proceeding to
// trackPendingIfLive, which must observe stopped and self-cancel.
func TestServerTunnelCloseTrackPendingRace(t *testing.T) {
	t.Parallel()
	bridge := newMockSAMBridge(t)
	bridge.holdAccept = true
	bridge.acceptReceived = make(chan struct{})
	defer bridge.close()

	st := NewServerTunnel("127.0.0.1:1") // local service need not exist
	st.SAM = newSAMClient()
	st.SAM.Address = bridge.addr()
	st.Destination = NewI2PDestinationFromData([]byte("race-dest"))

	// Park the accept loop after OpenStreamAccept returns and before it tracks
	// the pending — the exact window where Close previously saw pendingAccept
	// nil and could not cancel. The hook proceeds only once Close has set
	// stopped, so the subsequent trackPendingIfLive observes stopped.
	reachedHook := make(chan struct{})
	st.onAcceptOpened = func() {
		close(reachedHook)
		for !st.isStopped() {
			runtime.Gosched()
		}
	}
	if err := st.Run(); err != nil {
		t.Fatalf("ServerTunnel.Run: %v", err)
	}

	// Wait until the accept loop is parked in the window (STREAM ACCEPT sent,
	// pending not yet tracked).
	<-bridge.acceptReceived
	<-reachedHook

	// Close runs with pendingAccept still nil (the loop has not tracked it).
	// It must still return: the accept loop, once the hook observes stopped,
	// self-cancels via trackPendingIfLive and exits so bgWG.Wait completes.
	// Pure channel sync — if Close leaks, <-closeDone blocks until the test
	// deadline fails it.
	closeDone := make(chan struct{})
	go func() {
		_ = st.Close()
		close(closeDone)
	}()
	<-closeDone
}

// TestTunnelSetupLogsUnrecognizedSAMResult covers Phase 19 task 3: when the SAM
// API returns a RESULT not in the known SAM_EXCEPTIONS table, Python's aiosam
// raises a KeyError (dict lookup miss) which is not a known i2plib exception, so
// both ClientTunnel and ServerTunnel setup log the catch-all
// "Unspecified I2P daemon error" (I2PInterface.py:205/294 else branch).
func TestTunnelSetupLogsUnrecognizedSAMResult(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, run func() error, buf *bytes.Buffer) {
		t.Helper()
		if err := run(); err == nil {
			t.Fatal("tunnel setup succeeded, want error")
		}
		out := buf.String()
		if !strings.Contains(out, "Unspecified I2P daemon error") {
			t.Fatalf("missing catch-all log; got:\n%s", out)
		}
		if !strings.Contains(out, "BOGUS_RESULT") {
			t.Fatalf("log missing BOGUS_RESULT; got:\n%s", out)
		}
	}

	// ClientTunnel.
	m := newMockSAM(t)
	m.sessionErr = "BOGUS_RESULT"
	defer m.close()
	var cbuf bytes.Buffer
	cl := log.New(&cbuf, "", 0)
	ct := NewClientTunnel("REMOTEDEST", "127.0.0.1:1")
	ct.SAM = newMockSAMClient(m)
	ct.Destination = NewI2PDestinationFromData([]byte("d"))
	ct.Logger = cl
	check(t, ct.Run, &cbuf)

	// ServerTunnel.
	m2 := newMockSAM(t)
	m2.sessionErr = "BOGUS_RESULT"
	defer m2.close()
	var sbuf bytes.Buffer
	sl := log.New(&sbuf, "", 0)
	st := NewServerTunnel("127.0.0.1:1")
	st.SAM = newMockSAMClient(m2)
	st.Destination = NewI2PDestinationFromData([]byte("d"))
	st.Logger = sl
	check(t, st.Run, &sbuf)
}

// TestTunnelSetupLogsUnrecognizedSAMError covers Phase 19 task 3: a non-SAM-
// protocol error during setup (e.g. the SAM API is unreachable) is not a known
// i2plib exception, so the else catch-all logs "Unspecified I2P daemon error"
// with the underlying error text.
func TestTunnelSetupLogsUnrecognizedSAMError(t *testing.T) {
	t.Parallel()
	// Point the SAM client at a dead port so dialing the API fails with a
	// non-*SAMResultError (a network error).
	dead := newDeadListener(t)
	defer func() { _ = dead.Close() }()

	var buf bytes.Buffer
	cl := log.New(&buf, "", 0)
	ct := NewClientTunnel("REMOTEDEST", "127.0.0.1:1")
	ct.SAM = newSAMClient()
	ct.SAM.Address = dead.addr()
	ct.Destination = NewI2PDestinationFromData([]byte("d"))
	ct.Logger = cl
	if err := ct.Run(); err == nil {
		t.Fatal("tunnel setup succeeded, want error")
	}
	out := buf.String()
	if !strings.Contains(out, "Unspecified I2P daemon error") {
		t.Fatalf("missing catch-all log; got:\n%s", out)
	}
}

// deadListener accepts connections and immediately closes them, so a SAM
// client dialing it gets a write/read EOF rather than a SAM protocol reply — a
// non-*SAMResultError path (the else catch-all in tunnel setup).
type deadListener struct {
	ln net.Listener
	wg sync.WaitGroup
}

func newDeadListener(t *testing.T) *deadListener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &deadListener{ln: l}
	d.wg.Go(func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	})
	return d
}

func (d *deadListener) addr() string { return d.ln.Addr().String() }
func (d *deadListener) Close() error {
	err := d.ln.Close()
	d.wg.Wait()
	return err
}

// itoa is a thin strconv.Itoa wrapper to avoid importing strconv in this test
// file (keeps the helper list tight).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
