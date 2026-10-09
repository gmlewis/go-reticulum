// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the sensor source layer: where a GNSS receiver's sentences and
// a compass's sentences come from, and what to do when they come from a socket
// rather than a device.
//
// It exists because a sensor does not have to be attached to the process that
// reads it. An Android appliance holding the platform's own GNSS receiver and
// magnetometer has to publish its readings to a bot and a network client that
// live in a *different application*, and two applications cannot hand each other
// a file or a device: the only channel they share is a socket. So a sensor source
// may be a device path, a loopback TCP endpoint, or a Unix-domain endpoint —
// exactly the three shapes Reticulum's own shared instance already accepts.
//
// The scheme is carried by the existing gps_port and compass_port settings rather
// than by new gps_url and compass_url keys. One source is configured in one
// place, and the precedence rule the bot already implements — a configured source
// beats a static fallback — stays literally intact instead of growing a third
// tier and a second precedence question.
//
// Two behaviours here are deliberate, and both are about a node staying useful:
//
// A device path behaves exactly as it always has. It is opened read-only, its
// errors are the errors an operator already knows, and a path with no scheme is
// never guessed at.
//
// A socket that cannot be reached does not stop the node. The sensor service
// lives in another application and may not have started yet, so a failed dial is
// a state to wait out rather than a reason to refuse to start. On the other hand
// a socket that *has* dropped is reconnected with a bounded backoff, because the
// reader behind it treats the end of its source as the end of its usefulness and
// never restarts on its own.
package bot

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sensor source schemes and reconnect bounds.
const (
	// sensorSchemeTCP is the scheme of a loopback or LAN TCP sensor endpoint.
	sensorSchemeTCP = "tcp"
	// sensorSchemeUnix is the scheme of a Unix-domain sensor endpoint, in either
	// the filesystem or the abstract namespace.
	sensorSchemeUnix = "unix"
	// sensorSchemeSeparator is what separates a scheme from its address.
	sensorSchemeSeparator = "://"
	// sensorAbstractPrefix marks a Unix address in the abstract namespace, which
	// the kernel does not back with a file.
	sensorAbstractPrefix = "@"
	// sensorInitialBackoff is how long the first reconnect attempt waits after a
	// sensor connection drops.
	sensorInitialBackoff = 100 * time.Millisecond
	// sensorMaxBackoff bounds the reconnect delay, so a sensor service that stays
	// down cannot turn the reader into a dial storm.
	sensorMaxBackoff = 5 * time.Second
	// sensorMaxPort and sensorMinPort bound an endpoint's port. Zero is refused
	// because it means "any port" to a listener and nothing at all to a dialer.
	sensorMinPort = 1
	sensorMaxPort = 65535
)

// sensorEndpoint is one parsed sensor source: either a device path or a socket.
type sensorEndpoint struct {
	// network is the socket network, empty for a device path.
	network string
	// address is the socket address, empty for a device path.
	address string
	// device is the device or file path, empty for a socket.
	device string
}

// isDevice reports whether the endpoint is an ordinary device or file path.
func (e sensorEndpoint) isDevice() bool { return e.device != "" }

// parseSensorEndpoint reads one gps_port or compass_port value.
//
// A value with no scheme is a device or file path and is returned as it stands,
// so every configuration written before this file existed keeps its meaning. A
// value with a scheme must name a transport this package can dial, with an
// address that transport can use: anything else is refused rather than ignored,
// because an endpoint that is silently dropped is a node that never gets a
// position and never says why.
func parseSensorEndpoint(text string) (sensorEndpoint, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return sensorEndpoint{}, errors.New("sensor endpoint is empty")
	}
	schemeText, rest, found := strings.Cut(trimmed, sensorSchemeSeparator)
	if !found {
		if scheme, ok := looksLikeScheme(trimmed); ok {
			return sensorEndpoint{}, fmt.Errorf(
				"sensor endpoint %q names the %q scheme without %q; write %v%vhost:port",
				trimmed, scheme, sensorSchemeSeparator, scheme, sensorSchemeSeparator)
		}
		return sensorEndpoint{device: trimmed}, nil
	}
	scheme := strings.ToLower(schemeText)
	switch scheme {
	case sensorSchemeTCP:
		if err := validateTCPEndpoint(rest); err != nil {
			return sensorEndpoint{}, err
		}
		return sensorEndpoint{network: sensorSchemeTCP, address: rest}, nil
	case sensorSchemeUnix:
		address, err := validateUnixEndpoint(rest)
		if err != nil {
			return sensorEndpoint{}, err
		}
		return sensorEndpoint{network: sensorSchemeUnix, address: address}, nil
	default:
		return sensorEndpoint{}, fmt.Errorf(
			"sensor endpoint %q names the unsupported scheme %q; use a device path, "+
				"tcp://host:port, or unix://path", trimmed, scheme)
	}
}

// looksLikeScheme reports whether a scheme-less value begins with something that
// reads as a transport name, which is how a mistyped "tcp:host:port" is caught
// instead of being tried as a file called "tcp:host:port".
func looksLikeScheme(text string) (string, bool) {
	colon := strings.IndexByte(text, ':')
	if colon <= 0 {
		return "", false
	}
	candidate := text[:colon]
	if strings.ContainsAny(candidate, "/\\.") {
		return "", false
	}
	for i := range len(candidate) {
		c := candidate[i]
		valid := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '+' || c == '-'
		if !valid {
			return "", false
		}
	}
	if !((candidate[0] >= 'a' && candidate[0] <= 'z') || (candidate[0] >= 'A' && candidate[0] <= 'Z')) {
		return "", false
	}
	return strings.ToLower(candidate), true
}

// validateTCPEndpoint checks that a TCP endpoint is a host and a numeric port the
// dialer can use. A hostname would have to be resolved, which is exactly the
// capability a stripped-down node cannot be assumed to have.
func validateTCPEndpoint(address string) error {
	if address == "" {
		return errors.New("tcp sensor endpoint needs a host and a port")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("tcp sensor endpoint %q is not host:port: %w", address, err)
	}
	if host == "" {
		return fmt.Errorf("tcp sensor endpoint %q names no host", address)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("tcp sensor endpoint %q needs a numeric port", address)
	}
	if port < sensorMinPort || port > sensorMaxPort {
		return fmt.Errorf("tcp sensor endpoint %q names the out-of-range port %v", address, port)
	}
	return nil
}

// validateUnixEndpoint checks that a Unix endpoint names either an absolute
// filesystem path or a name in the abstract namespace, and returns the address
// the dialer wants.
func validateUnixEndpoint(address string) (string, error) {
	switch {
	case strings.HasPrefix(address, sensorAbstractPrefix):
		if len(address) == len(sensorAbstractPrefix) {
			return "", errors.New("unix sensor endpoint names an empty abstract address")
		}
		return address, nil
	case strings.HasPrefix(address, "/"):
		if len(address) == 1 {
			return "", errors.New("unix sensor endpoint names an empty path")
		}
		return address, nil
	default:
		return "", fmt.Errorf(
			"unix sensor endpoint %q must be an absolute path or an %vabstract name",
			address, sensorAbstractPrefix)
	}
}

// open resolves the endpoint into a byte stream. A device path is opened
// read-only, exactly as it always was. A socket is dialled in the background and
// reconnected as it drops, so a sensor service that is not up yet is a state to
// wait out rather than a startup failure.
func (e sensorEndpoint) open() (io.ReadCloser, error) {
	if !e.isDevice() {
		return newSensorSocket(e), nil
	}
	file, err := os.OpenFile(e.device, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// OpenSensorSource opens one sensor byte stream from a configured endpoint. It is
// the same resolution the bot's own gps_port and compass_port use, exported so
// that a second consumer — the Nomad Network client on this machine, which needs
// the reader's own position for the distance and bearing it renders — reads the
// feed through one implementation rather than a second one of its own.
//
// The endpoint may be a device path, tcp://host:port, or unix://path. A device
// path that cannot be opened and a malformed endpoint are reported; a socket that
// cannot yet be reached is not, because the sensor service on the other side may
// simply not have started, and the returned stream reconnects itself.
//
// The caller owns the stream and must Close it.
func OpenSensorSource(endpoint string) (io.ReadCloser, error) {
	parsed, err := parseSensorEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return parsed.open()
}

// sensorSocket is an io.ReadCloser over a socket that reconnects itself. It
// exists because a socket is not a device: a device that is unplugged is gone,
// while a socket that drops is a service that restarted, and a reader that
// treated the two the same would go permanently blind the first time the sensor
// service respawned.
//
// One goroutine reads it. Close may be called from any goroutine and unblocks a
// pending Read.
type sensorSocket struct {
	// endpoint is where the stream comes from.
	endpoint sensorEndpoint
	// dial opens one connection. It is a field so a test can drive the
	// reconnect path without a real listener.
	dial func(network, address string) (net.Conn, error)
	// sleep waits out a reconnect delay and reports whether it reached the end
	// of it. It is a field so a test can reconnect without waiting on real time.
	sleep func(time.Duration) bool

	mu sync.Mutex
	// conn is the live connection, nil while disconnected.
	conn net.Conn
	// backoff is the delay the next failed attempt will wait.
	backoff time.Duration
	// closed is closed by Close, which is what unblocks a pending read and a
	// pending reconnect delay.
	closed chan struct{}
	// closeOnce guards the channel close, so Close is idempotent.
	closeOnce sync.Once
}

// newSensorSocket builds a reconnecting reader over an endpoint.
func newSensorSocket(endpoint sensorEndpoint) *sensorSocket {
	stream := &sensorSocket{
		endpoint: endpoint,
		dial:     net.Dial,
		backoff:  sensorInitialBackoff,
		closed:   make(chan struct{}),
	}
	stream.sleep = stream.wait
	return stream
}

// Read returns whatever the sensor stream has next. A connection that has ended,
// or that yielded nothing at all, is dropped and replaced after a bounded
// backoff; only Close ends the stream, and it reports an ordinary end of input so
// the scanner above it returns quietly.
func (s *sensorSocket) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if s.isClosed() {
			return 0, io.EOF
		}
		conn, err := s.acquire()
		if err == nil {
			n, readErr := conn.Read(p)
			if n > 0 {
				s.resetBackoff()
				return n, nil
			}
			// Either the peer closed the stream or it handed back no bytes; both
			// mean this connection is spent, and retrying it in a tight loop is
			// the failure this whole file exists to avoid.
			s.discard(conn)
			_ = readErr
		}
		if !s.retry() {
			return 0, io.EOF
		}
	}
}

// Close ends the stream and releases the connection. It is idempotent and safe to
// call from any goroutine.
func (s *sensorSocket) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.mu.Lock()
		conn := s.conn
		s.conn = nil
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	})
	return nil
}

// isClosed reports whether Close has run.
func (s *sensorSocket) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// errSensorClosed reports that a connection was refused because the stream had been closed
// while it was still being dialled.
var errSensorClosed = errors.New("sensor stream is closed")

// acquire returns the live connection, dialling one when there is none.
// acquire returns the live connection, dialling one when there is none.
//
// The dial happens WITHOUT the lock, and the connection it produces is refused once the socket
// has been closed. Both matter, and for the same reason: a reader must never end up blocked on
// a connection that nothing will ever close.
//
// Holding the lock across the dial parks Close on that mutex for the operating system's whole
// connect timeout whenever the peer is down, so the reader that owns the lock is the last thing
// Close can reach.
//
// And a dial that finishes after Close has run is the worse case, because it is silent. Close
// runs its body exactly once; a connection published after that body has finished is closed by
// nobody, the read on it blocks forever, and the wait for the scan goroutine never returns. The
// window is one instruction wide — retry reports true an instant before the close lands — which
// is why it showed up as a test that hung for the package timeout under load and passed alone.
func (s *sensorSocket) acquire() (net.Conn, error) {
	s.mu.Lock()
	if s.conn != nil {
		conn := s.conn
		s.mu.Unlock()
		return conn, nil
	}
	network, address := s.endpoint.network, s.endpoint.address
	s.mu.Unlock()

	conn, err := s.dial(network, address)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosedLocked() {
		_ = conn.Close()
		return nil, errSensorClosed
	}
	if s.conn != nil {
		// Another reader dialled while this one was connecting. Keep the connection
		// that was published first and drop this one, rather than leaking it.
		_ = conn.Close()
		return s.conn, nil
	}
	s.conn = conn
	return conn, nil
}

// isClosedLocked reports whether Close has run. It reads a closed channel, so it needs no lock,
// but it is named for its use while the caller holds one.
func (s *sensorSocket) isClosedLocked() bool {
	return s.isClosed()
}

// discard closes a spent connection and forgets it.
func (s *sensorSocket) discard(conn net.Conn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
	_ = conn.Close()
}

// resetBackoff returns the reconnect delay to its initial value, which is what
// makes the backoff a backoff rather than a fixed slow retry.
func (s *sensorSocket) resetBackoff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backoff = sensorInitialBackoff
}

// retry waits out the reconnect delay and reports whether another attempt should
// be made. The delay doubles up to a bound, so a sensor service that stays down is
// retried rarely rather than continuously.
func (s *sensorSocket) retry() bool {
	s.mu.Lock()
	delay := s.backoff
	if delay <= 0 {
		delay = sensorInitialBackoff
	}
	next := min(delay*2, sensorMaxBackoff)
	s.backoff = next
	s.mu.Unlock()

	if !s.sleep(delay) {
		return false
	}
	return !s.isClosed()
}

// wait sleeps for a reconnect delay, returning early when Close runs.
func (s *sensorSocket) wait(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.closed:
		return false
	}
}
