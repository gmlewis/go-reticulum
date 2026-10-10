// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// mustTestNewRemoteOutDestination creates an outbound SINGLE destination whose
// hash is NOT in the transport's destinations-map, so the packet addressed to
// it has no local copy to be looped back to and no path-table entry to be sent
// along. What is left is the transport's broadcast fallback, which is the
// branch this test is about.
func mustTestNewRemoteOutDestination(t *testing.T, ts *TransportSystem, identity *Identity, appName string, aspects ...string) *Destination {
	t.Helper()
	dest, err := NewDestination(ts, identity, DestinationOut, DestinationSingle, appName, aspects...)
	mustTest(t, err)
	delete(ts.destinationsMap, string(dest.Hash))
	return dest
}

// readFramesUntilQuiet collects every HDLC frame that arrives on conn within
// window, and returns them unescaped. Reads are bounded, so a frame that never
// arrives fails the test instead of hanging it.
func readFramesUntilQuiet(t *testing.T, conn net.Conn, window time.Duration) [][]byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(window)); err != nil {
		t.Fatalf("setting a read deadline on the client socket: %v", err)
	}
	var collected []byte
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		collected = append(collected, buf[:n]...)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			t.Fatalf("reading from the client socket: %v", err)
		}
	}

	var frames [][]byte
	for part := range bytes.SplitSeq(collected, []byte{interfaces.HDLCFlag}) {
		if len(part) == 0 {
			continue
		}
		frames = append(frames, interfaces.HDLCUnescape(part))
	}
	return frames
}

// TestTCPClientReceivesOneCopyOfEachFrame is the defect behind the intermittent
// link timeouts: a TCP server's client received every frame the transport sent
// TWICE, so all inbound traffic at every client of a TCP hub was duplicated.
//
// Reticulum has exactly one delivery path to a server-accepted client, and
// Python states which one it is. In Python the spawned client is registered
// with the transport (TCPInterface.py:648, RNS.Transport.add_interface) and is
// the interface that transmits, while the listening TCPServerInterface's own
// process_outgoing is `pass` (TCPInterface.py:647-648) — so a packet reaches a
// client once. Go did both: the server fanned out to its spawned clients in
// Send AND each spawned client was registered with the transport, whose
// broadcast fallback writes to every registered interface. The client
// therefore read two identical frames per send.
//
// Measured on the fleet before this test existed: 43.9% of the operator's
// client's inbound packets were exact immediate duplicates, 23.2% for a fresh
// Go client with its own transport, and 50% at a raw TCP socket — 20 frames
// read, 10 distinct.
func TestTCPClientReceivesOneCopyOfEachFrame(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: mustHexDecode(t, "11223344556677889900112233445566")}

	server, spawned, rawConn := startSingleClientServer(t)
	defer func() {
		_ = rawConn.Close()
		_ = server.Detach()
	}()

	// Production registers each spawned client as its connectHandler fires
	// (rns.go wraps NewTCPServerInterface's onConnect with
	// Transport.RegisterInterface). Mirror that here; both the listener and
	// the accepted client end up in the transport's interface list.
	ts.RegisterInterface(server)
	ts.RegisterInterface(interfaces.Interface(spawned))
	waitForCondition(t, 3*time.Second, func() bool {
		return slices.Contains(ts.GetInterfaces(), interfaces.Interface(spawned))
	}, "spawned client never registered with transport")

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewRemoteOutDestination(t, ts, remoteID, "dup", "test")
	packet := NewPacketWithTransport(ts, dest, []byte("hello"))
	if err := packet.Pack(); err != nil {
		t.Fatalf("packing the outbound packet: %v", err)
	}
	if err := ts.Outbound(packet); err != nil {
		t.Fatalf("outbound failed: %v", err)
	}

	frames := readFramesUntilQuiet(t, rawConn, 750*time.Millisecond)
	if len(frames) != 1 {
		t.Fatalf("one packet sent once reached the client as %v frames, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], packet.Raw) {
		t.Errorf("the client received %x, want %x", frames[0], packet.Raw)
	}
}
