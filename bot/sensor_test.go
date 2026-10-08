// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitForFix polls a reader until it holds a valid fix, and fails the test if
// none arrives. The poll is bounded and returns as soon as the data lands, so it
// costs nothing in the ordinary case.
func waitForFix(t *testing.T, reader *GPSReader) GPSFix {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fix := reader.LastFix(); fix.Valid {
			return fix
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no fix arrived")
	return GPSFix{}
}

// waitForHeading polls a reader until it holds a heading.
func waitForHeading(t *testing.T, reader *CompassReader) CompassHeading {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if heading := reader.LastHeading(); heading.Valid {
			return heading
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no heading arrived")
	return CompassHeading{}
}

// serveSessions accepts one connection per session and writes that session's
// bytes to it. A session is closed by the server when the session ends, which is
// what makes a dropped link reproducible.
func serveSessions(t *testing.T, ln net.Listener, sessions [][]string, closeEach bool) {
	t.Helper()

	go func() {
		for _, session := range sessions {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			for _, sentence := range session {
				_, _ = io.WriteString(conn, sentence)
			}
			if closeEach {
				_ = conn.Close()
			}
		}
	}()
}

// tcpSensorListener starts a loopback listener and returns it with its address.
func tcpSensorListener(t *testing.T) (net.Listener, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, ln.Addr().String()
}

// TestSensorEndpointSchemeValidation asserts every supported sensor source is
// recognised, and that a malformed one is refused with a clear error rather than
// silently ignored — a silently ignored endpoint is a node that never gets a
// position and never says why.
func TestSensorEndpointSchemeValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		text     string
		device   bool
		network  string
		address  string
		failWant bool
	}{
		{name: "absolute device", text: "/dev/ttyUSB0", device: true},
		{name: "relative device", text: "run/gps.nmea", device: true},
		{name: "surrounding whitespace", text: "  /dev/ttyUSB0  ", device: true},
		{name: "tcp ipv4", text: "tcp://127.0.0.1:37429", network: "tcp", address: "127.0.0.1:37429"},
		{name: "tcp ipv6", text: "tcp://[::1]:37429", network: "tcp", address: "[::1]:37429"},
		{name: "tcp uppercase scheme", text: "TCP://127.0.0.1:1", network: "tcp", address: "127.0.0.1:1"},
		{name: "unix abstract", text: "unix://@rns/sensor", network: "unix", address: "@rns/sensor"},
		{name: "unix filesystem", text: "unix:///tmp/sensor.sock", network: "unix", address: "/tmp/sensor.sock"},
		{name: "empty", text: "", failWant: true},
		{name: "whitespace only", text: "   ", failWant: true},
		{name: "unknown scheme", text: "udp://127.0.0.1:37429", failWant: true},
		{name: "http scheme", text: "http://127.0.0.1:37429", failWant: true},
		{name: "tcp without a port", text: "tcp://127.0.0.1", failWant: true},
		{name: "tcp without a host", text: "tcp://:37429", failWant: true},
		{name: "tcp with a named port", text: "tcp://127.0.0.1:rns", failWant: true},
		{name: "tcp with a port out of range", text: "tcp://127.0.0.1:70000", failWant: true},
		{name: "tcp with a zero port", text: "tcp://127.0.0.1:0", failWant: true},
		{name: "tcp with nothing after the scheme", text: "tcp://", failWant: true},
		{name: "unix with nothing after the scheme", text: "unix://", failWant: true},
		{name: "unix with a bare at sign", text: "unix://@", failWant: true},
		{name: "unix with a relative path", text: "unix://sensor.sock", failWant: true},
		{name: "unix with a bare slash", text: "unix:///", failWant: true},
		{name: "scheme without slashes", text: "tcp:127.0.0.1:37429", failWant: true},
		{name: "unix scheme without slashes", text: "unix:/tmp/x", failWant: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSensorEndpoint(tc.text)
			if tc.failWant {
				if err == nil {
					t.Fatalf("parseSensorEndpoint(%q) = %+v, want an error", tc.text, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSensorEndpoint(%q): %v", tc.text, err)
			}
			if got.isDevice() != tc.device {
				t.Fatalf("parseSensorEndpoint(%q).isDevice() = %v, want %v", tc.text, got.isDevice(), tc.device)
			}
			if got.network != tc.network || got.address != tc.address {
				t.Fatalf("parseSensorEndpoint(%q) = (%q,%q), want (%q,%q)",
					tc.text, got.network, got.address, tc.network, tc.address)
			}
		})
	}
}

// TestOpenGPSTCPEndpoint asserts a loopback TCP sensor source works end to end
// through openGPS, which is the cross-application path an Android sensor service
// publishes on.
func TestOpenGPSTCPEndpoint(t *testing.T) {
	t.Parallel()

	ln, address := tcpSensorListener(t)
	serveSessions(t, ln, [][]string{{
		testRMCSentence + "\r\n",
		testGGASentence + "\r\n",
	}}, false)

	reader, err := openGPS(&BotConfig{GPSPort: "tcp://" + address})
	if err != nil {
		t.Fatalf("openGPS: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	fix := waitForFix(t, reader)
	if !closeWithin(fix.Lat, 37.755321, 1e-4) || !closeWithin(fix.Lng, -122.452719, 1e-4) {
		t.Fatalf("fix = (%v,%v), want the sentences the listener sent", fix.Lat, fix.Lng)
	}
	if fix.FixQuality != 1 {
		t.Fatalf("fix quality = %v, want 1 from the fix-quality sentence", fix.FixQuality)
	}
}

// TestOpenCompassTCPEndpoint is the same property for the heading, which is the
// half of the feed a stationary device cannot get from its receiver.
func TestOpenCompassTCPEndpoint(t *testing.T) {
	t.Parallel()

	ln, address := tcpSensorListener(t)
	serveSessions(t, ln, [][]string{{hdmSentence + "\r\n"}}, false)

	reader, err := openCompass(&BotConfig{CompassPort: "tcp://" + address})
	if err != nil {
		t.Fatalf("openCompass: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	heading := waitForHeading(t, reader)
	if !heading.HasMagnetic || !closeWithin(heading.MagneticDeg, 101.1, 1e-6) {
		t.Fatalf("heading = %+v, want magnetic 101.1", heading)
	}
}

// TestOpenGPSUnixEndpoint asserts a Unix-socket sensor source works through
// openGPS. Reticulum's own shared instance can be either kind of socket, so the
// sensor feed accepts the same two shapes.
func TestOpenGPSUnixEndpoint(t *testing.T) {
	t.Parallel()

	path := filepath.Join(tempDir(t), "sensor.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	serveSessions(t, ln, [][]string{{testRMCSentence + "\r\n"}}, false)

	reader, err := openGPS(&BotConfig{GPSPort: "unix://" + path})
	if err != nil {
		t.Fatalf("openGPS: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	if fix := waitForFix(t, reader); !fix.Valid {
		t.Fatalf("fix = %+v, want a valid fix", fix)
	}
}

// TestOpenGPSUnixAbstractEndpoint asserts the abstract-socket form is accepted
// and is dialled as an abstract Unix socket, which is the transport Reticulum
// itself uses by default on Linux.
//
// Darwin has no abstract socket namespace, so the round trip runs only where the
// kernel provides one; on the other platforms the test still proves the endpoint
// is accepted and that a socket that cannot be reached does not stop the node
// from starting.
func TestOpenGPSUnixAbstractEndpoint(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		reader, err := openGPS(&BotConfig{GPSPort: "unix://@gonomadnet-test-sensor"})
		if err != nil {
			t.Fatalf("openGPS on an abstract endpoint: %v", err)
		}
		t.Cleanup(func() { _ = reader.Close() })
		if fix := reader.LastFix(); fix.Valid {
			t.Fatalf("fix = %+v, want nothing before any sentence arrived", fix)
		}
		return
	}

	ln, err := net.Listen("unix", "@gonomadnet-test-sensor")
	if err != nil {
		t.Fatalf("Listen abstract unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	serveSessions(t, ln, [][]string{{testRMCSentence + "\r\n"}}, false)

	reader, err := openGPS(&BotConfig{GPSPort: "unix://@gonomadnet-test-sensor"})
	if err != nil {
		t.Fatalf("openGPS: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if fix := waitForFix(t, reader); !fix.Valid {
		t.Fatalf("fix = %+v, want a valid fix", fix)
	}
}

// TestOpenGPSAbstractEndpointIsDialledAsUnix asserts the abstract form reaches
// the dialer as the abstract-namespace address, so the endpoint the operator
// writes is the socket the kernel is asked for.
func TestOpenGPSAbstractEndpointIsDialledAsUnix(t *testing.T) {
	t.Parallel()

	endpoint, err := parseSensorEndpoint("unix://@rns/sensor")
	if err != nil {
		t.Fatalf("parseSensorEndpoint: %v", err)
	}

	var mu sync.Mutex
	var dialled []string
	stream := newSensorSocket(endpoint)
	stream.dial = func(network, address string) (net.Conn, error) {
		mu.Lock()
		dialled = append(dialled, network+" "+address)
		mu.Unlock()
		return nil, errors.New("no sensor is listening")
	}
	stream.sleep = func(time.Duration) bool { return false }
	t.Cleanup(func() { _ = stream.Close() })

	buf := make([]byte, 8)
	if _, err := stream.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("Read on an unreachable abstract sensor = %v, want io.EOF", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(dialled) == 0 || dialled[0] != "unix @rns/sensor" {
		t.Fatalf("dialled %v, want the abstract Unix address first", dialled)
	}
}

// TestSensorReconnectsAfterEOF asserts a dropped sensor connection is retried
// rather than ending the stream, because the receiver's own reader treats the end
// of its source as the end of its usefulness and never restarts.
func TestSensorReconnectsAfterEOF(t *testing.T) {
	t.Parallel()

	ln, address := tcpSensorListener(t)
	first := testRMCSentence + "\r\n"
	second := sentence(t, "GNRMC,204534.00,A,1045.31926,N,12227.16314,W,0.02,142.3,160926,,,A") + "\r\n"
	serveSessions(t, ln, [][]string{{first}, {second}}, true)

	reader, err := openGPS(&BotConfig{GPSPort: "tcp://" + address})
	if err != nil {
		t.Fatalf("openGPS: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	fix := waitForFix(t, reader)
	if !closeWithin(fix.Lat, 37.755321, 1e-4) {
		t.Fatalf("first session fix = %+v, want latitude 37.755321", fix)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := reader.LastFix(); closeWithin(got.Lat, 10.755321, 1e-4) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("after the first connection dropped, fix = %+v, want the second session's latitude", reader.LastFix())
}

// TestSensorReconnectBacksOff asserts the reconnect delay grows and stays
// bounded, so a sensor service that is down cannot turn the reader into a dial
// storm.
func TestSensorReconnectBacksOff(t *testing.T) {
	t.Parallel()

	stream := newSensorSocket(sensorEndpoint{network: "tcp", address: "127.0.0.1:1"})
	defer func() { _ = stream.Close() }()

	var slept []time.Duration
	stream.sleep = func(d time.Duration) bool {
		slept = append(slept, d)
		return len(slept) < 8
	}
	stream.dial = func(string, string) (net.Conn, error) {
		return nil, errors.New("no sensor is listening")
	}

	buf := make([]byte, 64)
	if _, err := stream.Read(buf); err != io.EOF {
		t.Fatalf("Read on an unreachable sensor = %v, want io.EOF", err)
	}
	if len(slept) < 4 {
		t.Fatalf("retried %v times, want several attempts before giving up", len(slept))
	}
	if slept[0] > slept[len(slept)-1] {
		t.Fatalf("delays %v did not grow", slept)
	}
	for _, d := range slept {
		if d > sensorMaxBackoff {
			t.Fatalf("delay %v exceeds the %v cap", d, sensorMaxBackoff)
		}
	}
}

// TestDevicePathBehaviourUnchanged asserts a plain path is still an ordinary
// read-only file, with the same error text an operator already knows.
func TestDevicePathBehaviourUnchanged(t *testing.T) {
	t.Parallel()

	path := filepath.Join(tempDir(t), "gps.nmea")
	if err := os.WriteFile(path, []byte(testRMCSentence+"\n"+testGGASentence+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reader, err := openGPS(&BotConfig{GPSPort: path})
	if err != nil {
		t.Fatalf("openGPS(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if fix := waitForFix(t, reader); !fix.Valid {
		t.Fatalf("fix = %+v, want a valid fix from the file", fix)
	}

	missing := filepath.Join(tempDir(t), "no-such-device")
	_, err = openGPS(&BotConfig{GPSPort: missing})
	if err == nil {
		t.Fatalf("openGPS(%q) accepted a missing device", missing)
	}
	if want := "gps: could not open " + missing + ":"; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("openGPS error = %q, want it to begin %q", err, want)
	}
}

// TestSensorSourceFailureDoesNotStopStartup asserts a sensor endpoint that
// cannot be reached at all still yields a reader and no error, because a node
// whose sensor service has not started yet must come up and work normally rather
// than refuse to start.
func TestSensorSourceFailureDoesNotStopStartup(t *testing.T) {
	t.Parallel()

	// Bind and immediately release a port so the address is real but nothing is
	// listening on it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	address := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := openGPS(&BotConfig{GPSPort: "tcp://" + address})
	if err != nil {
		t.Fatalf("openGPS on an unreachable endpoint: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if fix := reader.LastFix(); fix.Valid {
		t.Fatalf("fix = %+v, want nothing from an unreachable endpoint", fix)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestLiveSensorSourceBeatsStaticFix asserts the two-tier precedence: a
// configured live source is the only source, so a static fallback that happens to
// be configured beside it never leaks into a reading.
func TestLiveSensorSourceBeatsStaticFix(t *testing.T) {
	t.Parallel()

	ln, address := tcpSensorListener(t)
	serveSessions(t, ln, [][]string{{
		testRMCSentence + "\r\n",
		testGGASentence + "\r\n",
	}}, false)

	reader, err := openGPS(&BotConfig{
		GPSPort: "tcp://" + address,
		GPSFix:  "45.0,-93.0",
	})
	if err != nil {
		t.Fatalf("openGPS: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	fix := waitForFix(t, reader)
	if closeWithin(fix.Lat, 45.0, 1e-6) && closeWithin(fix.Lng, -93.0, 1e-6) {
		t.Fatalf("fix = %+v, want the live source rather than the static fallback", fix)
	}
}

// TestStaticFixIsUsedOnlyWithoutALiveSource asserts a node with no live source
// keeps the static fix, which is what a sensorless desktop install relies on.
func TestStaticFixIsUsedOnlyWithoutALiveSource(t *testing.T) {
	t.Parallel()

	reader, err := openGPS(&BotConfig{GPSFix: "45.0,-93.0"})
	if err != nil {
		t.Fatalf("openGPS: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	fix := reader.LastFix()
	if !fix.Valid || !closeWithin(fix.Lat, 45.0, 1e-6) || !closeWithin(fix.Lng, -93.0, 1e-6) {
		t.Fatalf("fix = %+v, want the static fix", fix)
	}
}

// TestLiveCompassSourceBeatsStaticHeading is the same precedence rule for the
// heading.
func TestLiveCompassSourceBeatsStaticHeading(t *testing.T) {
	t.Parallel()

	ln, address := tcpSensorListener(t)
	serveSessions(t, ln, [][]string{{hdmSentence + "\r\n"}}, false)

	reader, err := openCompass(&BotConfig{
		CompassPort:    "tcp://" + address,
		CompassHeading: "042",
	})
	if err != nil {
		t.Fatalf("openCompass: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	heading := waitForHeading(t, reader)
	if closeWithin(heading.MagneticDeg, 42, 1e-6) {
		t.Fatalf("heading = %+v, want the live source rather than the static heading", heading)
	}
}

// TestOpenSensorSourceIsTheSameReaderTheBotUses asserts the exported entry point
// a second consumer resolves an endpoint through behaves exactly as the bot's own
// openers do: a socket streams NMEA, a device path that is missing is reported,
// and a malformed endpoint is refused rather than ignored.
func TestOpenSensorSourceIsTheSameReaderTheBotUses(t *testing.T) {
	t.Parallel()

	ln, address := tcpSensorListener(t)
	serveSessions(t, ln, [][]string{{testRMCSentence + "\r\n"}}, false)

	stream, err := OpenSensorSource("tcp://" + address)
	if err != nil {
		t.Fatalf("OpenSensorSource: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	line, err := bufio.NewReader(stream).ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "$GNRMC") {
		t.Fatalf("first sentence = %q, want the recommended-minimum sentence", line)
	}

	missing := filepath.Join(tempDir(t), "no-such-sensor")
	if _, err := OpenSensorSource(missing); err == nil {
		t.Fatalf("OpenSensorSource(%q) accepted a missing device", missing)
	}
	if _, err := OpenSensorSource("udp://127.0.0.1:1"); err == nil {
		t.Fatalf("OpenSensorSource accepted an unsupported scheme")
	}
}
