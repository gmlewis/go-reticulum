// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux || darwin

package interfaces

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// mockRNode is a virtual RNode that responds to the host's KISS commands the
// way a real radio does: detect yields DETECT_RESP + firmware/platform/mcu +
// PHYPRM telemetry, and each config command is echoed back as the matching
// reported-radio-state frame so validateRadioState succeeds. It blocks Read
// until response bytes are available, mirroring a blocking serial read.
type mockRNode struct {
	mu        sync.Mutex
	cond      *sync.Cond
	buf       []byte
	written   []byte // every byte the host wrote (for detach byte-parity)
	closed    bool
	fwMaj     byte
	fwMin     byte
	platform  byte
	driftFreq int // if non-zero, reported frequency is shifted by this (mismatch)
}

func newMockRNode() *mockRNode {
	m := &mockRNode{fwMaj: 1, fwMin: 85, platform: 0x80}
	m.cond = sync.NewCond(&m.mu)
	return m
}

func (m *mockRNode) queue(frame []byte) {
	m.mu.Lock()
	m.buf = append(m.buf, frame...)
	m.cond.Broadcast()
	m.mu.Unlock()
}

func (m *mockRNode) Read(p []byte) (int, error) {
	m.mu.Lock()
	for len(m.buf) == 0 && !m.closed {
		m.cond.Wait()
	}
	if m.closed && len(m.buf) == 0 {
		m.mu.Unlock()
		return 0, io.EOF
	}
	n := copy(p, m.buf)
	m.buf = m.buf[n:]
	m.mu.Unlock()
	return n, nil
}

func (m *mockRNode) Write(p []byte) (int, error) {
	m.mu.Lock()
	m.written = append(m.written, p...)
	frames := splitFEND(p)
	m.mu.Unlock()
	// Respond outside the lock: queue() re-acquires it.
	for _, fr := range frames {
		if len(fr) == 0 {
			continue
		}
		m.respond(fr[0], fr[1:])
	}
	return len(p), nil
}

func (m *mockRNode) Close() error {
	m.mu.Lock()
	m.closed = true
	m.cond.Broadcast()
	m.mu.Unlock()
	return nil
}

// respond emits the radio's reply to a host command, matching what a real
// RNode reports back so the controller's validateRadioState succeeds.
func (m *mockRNode) respond(cmd byte, payload []byte) {
	// payload from the host is KISS-escaped; unescape to read the value.
	raw := KISSUnescape(payload)
	switch cmd {
	case KISSCmdDetect: // CMD_DETECT
		m.queue(KISSFrame(KISSCmdDetect, []byte{KISSDetectResp}))
		m.queue(KISSFrame(KISSCmdFwVersion, []byte{m.fwMaj, m.fwMin}))
		m.queue(KISSFrame(KISSCmdPlatform, []byte{m.platform}))
		m.queue(KISSFrame(KISSCmdMcu, []byte{0x01}))
		// PHYPRM telemetry a real radio autonomously streams (live values).
		phy := append(be16(2050), be16(488)...)
		phy = append(phy, be16(18)...)
		phy = append(phy, be16(37)...)
		phy = append(phy, be16(24)...)
		phy = append(phy, be16(48)...)
		m.queue(KISSFrame(0x26, phy))
	case KISSCmdFrequency:
		if m.driftFreq != 0 && len(raw) == 4 {
			v := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
			v += uint32(m.driftFreq)
			drifted := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
			m.queue(KISSFrame(KISSCmdFrequency, drifted))
		} else {
			m.queue(KISSFrame(KISSCmdFrequency, raw))
		}
	case KISSCmdBandwidth:
		m.queue(KISSFrame(KISSCmdBandwidth, raw))
	case KISSCmdTXPower:
		m.queue(KISSFrame(KISSCmdTXPower, raw))
	case KISSCmdSF:
		m.queue(KISSFrame(KISSCmdSF, raw))
	case KISSCmdCR:
		m.queue(KISSFrame(KISSCmdCR, raw))
	case KISSCmdRadioState:
		m.queue(KISSFrame(KISSCmdRadioState, raw))
	}
}

// splitFEND splits a byte stream on FEND into the inner frame bodies.
func splitFEND(p []byte) [][]byte {
	var out [][]byte
	start := -1
	for i, b := range p {
		if b == KISSFend {
			if start >= 0 {
				out = append(out, p[start:i])
				start = -1
			} else {
				start = i + 1
			}
		}
	}
	if start >= 0 && start < len(p) {
		out = append(out, p[start:])
	}
	return out
}

// newRNodeForTest builds an RNodeInterface backed by a mock conn with tiny
// lifecycle delays and runs configureDevice synchronously.
func newRNodeForTest(t *testing.T, conn rnodeConn, freq, bw, tx, sf, cr int, handler InboundHandler) *RNodeInterface {
	t.Helper()
	r := &RNodeInterface{
		BaseInterface:       NewBaseInterface("test-rnode", ModeFull, 115200),
		port:                "mock",
		speed:               115200,
		databits:            8,
		stopbits:            1,
		parity:              "N",
		hwmtu:               rnodeHWMTU,
		timeout:             rnodeReadTimeout,
		inboundHandler:      handler,
		openSettleDelay:     2 * time.Millisecond,
		detectWaitDelay:     15 * time.Millisecond,
		validateSettleDelay: 50 * time.Millisecond,
		postOnlineDelay:     1 * time.Millisecond,
	}
	r.setDefaultIFACSize(rnodeDefaultIFAC)
	r.radio = rnodeRadioState{frequency: freq, bandwidth: bw, txpower: tx, sf: sf, cr: cr, state: RadioStateOff}
	r.decoder = newRNodeDecoder(&r.radio, r.hwmtu)
	r.connMu.Lock()
	r.conn = conn
	r.connMu.Unlock()
	if err := r.configureDevice(); err != nil {
		t.Fatalf("configureDevice: %v", err)
	}
	return r
}

// TestRNodeLifecycleDetectConfigureValidateOnline is the Phase D state-machine
// test: a virtual RNode responds to detect, echoes the radio configuration, and
// the controller reaches online with the reported on-air bitrate and symbol
// rate matching the live radio (3.12 kbps, 488 baud).
func TestRNodeLifecycleDetectConfigureValidateOnline(t *testing.T) {
	t.Parallel()
	mock := newMockRNode()
	r := newRNodeForTest(t, mock, 915000000, 125000, 17, 8, 5, nil)

	if !r.Status() {
		t.Fatal("interface should be online after configure")
	}
	if !r.RNodeDetected() {
		t.Fatal("device should be detected")
	}
	maj, min := r.RNodeFirmwareVersion()
	if maj != 1 || min != 85 {
		t.Fatalf("firmware = %d.%d, want 1.85", maj, min)
	}
	if r.Bitrate() != 3125 {
		t.Fatalf("on-air bitrate = %d, want 3125 (3.12 kbps)", r.Bitrate())
	}
	if r.RNodeSymbolRate() != 488 {
		t.Fatalf("symbol rate = %d, want 488", r.RNodeSymbolRate())
	}
	if r.RNodeSymbolTimeMs() != 2.05 {
		t.Fatalf("symbol time = %v, want 2.05ms", r.RNodeSymbolTimeMs())
	}
	slot, difs := r.RNodeCSMA()
	if slot != 24 || difs != 48 {
		t.Fatalf("csma = %d/%d, want 24/48", slot, difs)
	}
	if err := r.Detach(); err != nil {
		t.Fatalf("detach: %v", err)
	}
}

// TestRNodeLifecycleValidationFailsOnMismatch asserts the controller stays
// offline when the radio reports parameters that do not match the config.
func TestRNodeLifecycleValidationFailsOnMismatch(t *testing.T) {
	t.Parallel()
	mock := newMockRNode()
	mock.driftFreq = 5000 // reported frequency off by 5 kHz (>100 Hz tolerance)
	r := &RNodeInterface{
		BaseInterface: NewBaseInterface("test-rnode-bad", ModeFull, 115200),
		port:          "mock", speed: 115200, databits: 8, stopbits: 1, parity: "N",
		hwmtu: rnodeHWMTU, timeout: rnodeReadTimeout,
		openSettleDelay: 2 * time.Millisecond, detectWaitDelay: 15 * time.Millisecond,
		validateSettleDelay: 50 * time.Millisecond, postOnlineDelay: 1 * time.Millisecond,
	}
	r.setDefaultIFACSize(rnodeDefaultIFAC)
	r.radio = rnodeRadioState{frequency: 915000000, bandwidth: 125000, txpower: 17, sf: 8, cr: 5, state: RadioStateOff}
	r.decoder = newRNodeDecoder(&r.radio, r.hwmtu)
	r.connMu.Lock()
	r.conn = mock
	r.connMu.Unlock()
	if err := r.configureDevice(); err == nil {
		t.Fatal("configureDevice should fail when reported frequency mismatches")
	}
	if r.Status() {
		t.Fatal("interface should remain offline on validation failure")
	}
	_ = r.Detach()
}

// TestRNodeDetachByteParity asserts Detach emits the same command sequence as
// Python RNodeInterface.detach: disable external framebuffer, radio state off,
// leave (for a display-capable ESP32 device).
func TestRNodeDetachByteParity(t *testing.T) {
	t.Parallel()
	mock := newMockRNode()
	r := newRNodeForTest(t, mock, 915000000, 125000, 17, 8, 5, nil)
	// Clear written log captured during bring-up, then detach.
	mock.mu.Lock()
	mock.written = nil
	mock.mu.Unlock()
	if err := r.Detach(); err != nil {
		t.Fatalf("detach: %v", err)
	}
	mock.mu.Lock()
	w := append([]byte(nil), mock.written...)
	mock.mu.Unlock()
	// Expect: disable_FB, radio_off, leave — in order.
	want := append([]byte{}, RNodeDisableExternalFramebuffer()...)
	want = append(want, RNodeSetRadioState(RadioStateOff)...)
	want = append(want, RNodeLeave()...)
	if !bytes.Equal(w, want) {
		t.Fatalf("detach byte sequence mismatch:\n got %x\n want %x", w, want)
	}
}

// serveRNodeTCPConn runs the virtual-RNode protocol over a real TCP connection:
// it reads the host's KISS frames from conn and writes back the responses a
// real RNode would emit (detect echo + config echo + PHYPRM telemetry).
func serveRNodeTCPConn(t *testing.T, conn net.Conn) {
	t.Helper()
	defer func() { _ = conn.Close() }()
	readBuf := make([]byte, 1024)
	acc := []byte{}
	for {
		n, err := conn.Read(readBuf)
		if err != nil {
			return
		}
		acc = append(acc, readBuf[:n]...)
		for {
			idx := bytes.IndexByte(acc, KISSFend)
			if idx < 0 {
				acc = []byte{}
				break
			}
			// frame body is acc[idx+1:nextFEND]
			rest := acc[idx+1:]
			next := bytes.IndexByte(rest, KISSFend)
			if next < 0 {
				acc = append([]byte{KISSFend}, rest...) // keep partial
				break
			}
			body := rest[:next]
			acc = append([]byte{}, rest[next:]...)
			if len(body) == 0 {
				continue
			}
			for _, resp := range rnodeTCPResponse(body[0], body[1:]) {
				if _, werr := conn.Write(resp); werr != nil {
					return
				}
			}
		}
	}
}

// rnodeTCPResponse returns the radio's reply frames for one host command.
func rnodeTCPResponse(cmd byte, payload []byte) [][]byte {
	raw := KISSUnescape(payload)
	switch cmd {
	case KISSCmdDetect:
		phy := append(be16(2050), be16(488)...)
		phy = append(phy, be16(18)...)
		phy = append(phy, be16(37)...)
		phy = append(phy, be16(24)...)
		phy = append(phy, be16(48)...)
		return [][]byte{
			KISSFrame(KISSCmdDetect, []byte{KISSDetectResp}),
			KISSFrame(KISSCmdFwVersion, []byte{1, 85}),
			KISSFrame(KISSCmdPlatform, []byte{0x80}),
			KISSFrame(KISSCmdMcu, []byte{0x01}),
			KISSFrame(0x26, phy),
		}
	case KISSCmdFrequency:
		return [][]byte{KISSFrame(KISSCmdFrequency, raw)}
	case KISSCmdBandwidth:
		return [][]byte{KISSFrame(KISSCmdBandwidth, raw)}
	case KISSCmdTXPower:
		return [][]byte{KISSFrame(KISSCmdTXPower, raw)}
	case KISSCmdSF:
		return [][]byte{KISSFrame(KISSCmdSF, raw)}
	case KISSCmdCR:
		return [][]byte{KISSFrame(KISSCmdCR, raw)}
	case KISSCmdRadioState:
		return [][]byte{KISSFrame(KISSCmdRadioState, raw)}
	}
	return nil
}

// TestRNodeTCPTransport brings up an RNode over a tcp:// loopback connection
// (Phase H): a Go TCP server speaks the RNode KISS protocol and the controller
// reaches online with the on-air bitrate matching the live serial radio.
func TestRNodeTCPTransport(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		serveRNodeTCPConn(t, conn)
	}()

	iface, err := NewRNodeInterface("rnode-tcp", fmt.Sprintf("tcp://127.0.0.1:%d", port),
		115200, 8, 1, "N", 915000000, 125000, 17, 8, 5, false, 0, "", nil)
	if err != nil {
		t.Fatalf("NewRNodeInterface tcp://: %v", err)
	}
	r, ok := iface.(*RNodeInterface)
	if !ok {
		t.Fatalf("expected *RNodeInterface, got %T", iface)
	}
	if !r.Status() {
		t.Fatal("tcp RNode should be online after configure")
	}
	if r.Bitrate() != 3125 {
		t.Fatalf("on-air bitrate = %d, want 3125", r.Bitrate())
	}
	if r.RNodeSymbolRate() != 488 {
		t.Fatalf("symbol rate = %d, want 488", r.RNodeSymbolRate())
	}
	_ = r.Detach()
}

// TestRNodeBLETransportRejected asserts ble:// is rejected with a clear error
// (the Go port has no native BLE stack; serial and tcp:// remain supported).
func TestRNodeBLETransportRejected(t *testing.T) {
	t.Parallel()
	_, err := NewRNodeInterface("rnode-ble", "ble://AA:BB:CC:DD:EE:FF",
		115200, 8, 1, "N", 915000000, 125000, 17, 8, 5, false, 0, "", nil)
	if err == nil {
		t.Fatal("ble:// should be rejected")
	}
}
