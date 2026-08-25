// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux || darwin

package interfaces

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	rnodeHWMTU            = 508
	rnodeSerialSpeed      = 115200
	rnodeReadIdleSleep    = 80 * time.Millisecond
	rnodeReadTimeout      = 100 * time.Millisecond
	rnodeDefaultIFAC      = 8
	rnodeTCPDetectTimeout = 5 * time.Second
	rnodeTCPDialTimeout   = 5 * time.Second

	RNodeDefaultSpeed    = 115200
	RNodeDefaultDataBits = 8
	RNodeDefaultStopBits = 1
	RNodeDefaultParity   = "N"

	rNodeFreqMin        = 137000000
	rNodeFreqMax        = 3000000000
	rNodeBandwidthMin   = 7800
	rNodeBandwidthMax   = 1625000
	rNodeTXPowerMin     = 0
	rNodeTXPowerMax     = 37
	rNodeSFMin          = 5
	rNodeSFMax          = 12
	rNodeCRMin          = 5
	rNodeCRMax          = 8
	rNodeCallsignMaxLen = 32
)

// rnodeReconnectWait is the delay between reconnect attempts. It is a var (not
// a const) so tests can shorten it to keep self-heal tests fast.
var rnodeReconnectWait = 5 * time.Second

// rnodeConn is the serial link an RNode controller reads from and writes KISS
// frames to. *os.File satisfies it for a real USB RNode; tests inject a mock.
type rnodeConn interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

// RNodeInterface wraps a serial connection to an RNode LoRa radio modem. It
// is a faithful Go port of RNS/Interfaces/RNodeInterface.py: it detects the
// device, configures the radio (frequency/bandwidth/TX power/spreading
// factor/coding rate/airtime locks), validates the reported radio state,
// streams inbound KISS telemetry into radio state, applies flow control and a
// packet queue, emits periodic ID beacons, reconnects on failure, and detaches
// cleanly. It embeds *BaseInterface for the shared Interface contract.
type RNodeInterface struct {
	*BaseInterface

	radio   rnodeRadioState
	decoder *rnodeDecoder
	// radioMu guards r.radio (written by the readLoop, read by accessors and
	// configureDevice) and firstTx, so the -race detector sees no concurrent
	// read/write of the radio-state fields.
	radioMu sync.RWMutex

	port     string
	speed    int
	databits int
	stopbits int
	parity   string

	// useTCP selects the TCP transport when the port is a tcp:// URI
	// (RNodeInterface.py: port = "tcp://host[:port]"); otherwise the port is a
	// serial device path. BLE (ble://) is not supported by the Go port (it
	// requires a native BLE stack not available in the standard library).
	useTCP  bool
	tcpHost string

	flowControl bool
	idInterval  int
	idCallsign  []byte

	inboundHandler InboundHandler

	connMu sync.Mutex
	conn   rnodeConn

	online         int32 // atomic: 1 when configured and powered up
	interfaceReady int32 // atomic: 1 when the radio can accept a TX
	running        int32 // atomic: 1 while the readLoop is active
	reconnecting   int32

	queueMu     sync.Mutex
	packetQueue [][]byte

	firstTx time.Time // zero = no TX yet (drives the ID beacon schedule)

	hwmtu   int
	timeout time.Duration

	// Lifecycle delays mirror RNodeInterface.py's fixed sleeps; overridable
	// by tests to keep them fast.
	openSettleDelay     time.Duration
	detectWaitDelay     time.Duration
	validateSettleDelay time.Duration
	postOnlineDelay     time.Duration
}

// NewRNodeInterface validates hardware bounds and brings up a physical RNode
// radio over a serial link: it opens the port, detects the device, configures
// the radio, and validates the reported state before reporting online.
func NewRNodeInterface(name, port string, speed, databits, stopbits int, parity string, frequency, bandwidth, txpower, spreadingFactor, codingRate int, flowControl bool, idInterval int, idCallsign string, handler InboundHandler) (Interface, error) {
	if strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("no port specified for RNode interface")
	}

	// Transport selection by port URI scheme (RNodeInterface.py:148-170).
	// tcp://host[:port] selects the TCP transport; a bare device path selects
	// serial. ble:// is rejected: the Go port has no native BLE stack.
	useTCP := false
	tcpHost := ""
	switch {
	case strings.HasPrefix(strings.ToLower(port), "tcp://"):
		useTCP = true
		tcpHost = port[len("tcp://"):]
		if strings.TrimSpace(tcpHost) == "" {
			return nil, fmt.Errorf("no TCP host specified for RNode interface")
		}
	case strings.HasPrefix(strings.ToLower(port), "ble://"):
		return nil, fmt.Errorf("BLE transport (ble://) is not supported by the Go RNode port; use a serial device path or tcp:// host")
	}

	if frequency < rNodeFreqMin || frequency > rNodeFreqMax {
		return nil, fmt.Errorf("invalid frequency configured for RNode interface")
	}
	if bandwidth < rNodeBandwidthMin || bandwidth > rNodeBandwidthMax {
		return nil, fmt.Errorf("invalid bandwidth configured for RNode interface")
	}
	if txpower < rNodeTXPowerMin || txpower > rNodeTXPowerMax {
		return nil, fmt.Errorf("invalid txpower configured for RNode interface")
	}
	if spreadingFactor < rNodeSFMin || spreadingFactor > rNodeSFMax {
		return nil, fmt.Errorf("invalid spreading factor configured for RNode interface")
	}
	if codingRate < rNodeCRMin || codingRate > rNodeCRMax {
		return nil, fmt.Errorf("invalid coding rate configured for RNode interface")
	}

	if idInterval > 0 || strings.TrimSpace(idCallsign) != "" {
		if idInterval <= 0 || strings.TrimSpace(idCallsign) == "" {
			return nil, fmt.Errorf("id_interval and id_callsign must both be set for RNode interface")
		}
		if len([]byte(idCallsign)) > rNodeCallsignMaxLen {
			return nil, fmt.Errorf("id_callsign exceeds max length for RNode interface")
		}
	}

	if speed <= 0 {
		speed = RNodeDefaultSpeed
	}
	if databits <= 0 {
		databits = RNodeDefaultDataBits
	}
	if stopbits <= 0 {
		stopbits = RNodeDefaultStopBits
	}
	if strings.TrimSpace(parity) == "" {
		parity = RNodeDefaultParity
	}

	r := &RNodeInterface{
		BaseInterface:       NewBaseInterface(name, ModeFull, speed),
		port:                port,
		speed:               speed,
		databits:            databits,
		stopbits:            stopbits,
		parity:              parity,
		useTCP:              useTCP,
		tcpHost:             tcpHost,
		flowControl:         flowControl,
		idInterval:          idInterval,
		idCallsign:          []byte(idCallsign),
		inboundHandler:      handler,
		hwmtu:               rnodeHWMTU,
		timeout:             rnodeReadTimeout,
		openSettleDelay:     2 * time.Second,
		detectWaitDelay:     200 * time.Millisecond,
		validateSettleDelay: 250 * time.Millisecond,
		postOnlineDelay:     300 * time.Millisecond,
	}
	r.setDefaultIFACSize(rnodeDefaultIFAC)
	r.radio = rnodeRadioState{
		frequency: frequency,
		bandwidth: bandwidth,
		txpower:   txpower,
		sf:        spreadingFactor,
		cr:        codingRate,
		state:     RadioStateOff,
	}
	r.decoder = newRNodeDecoder(&r.radio, r.hwmtu)

	// Bring up the radio. Python's RNodeInterface.__init__ does NOT raise on an
	// open/configure failure: it logs and spawns a background reconnect thread so
	// the interface self-heals once the port/radio becomes available (e.g. a
	// USB re-plug, or another process releasing the port at startup). Mirror
	// that: return the interface (offline) and reconnect in the background
	// rather than failing construction. A configuration error (invalid bounds,
	// ble://) is still returned immediately.
	if err := r.openPort(); err != nil {
		r.logf("could not open port: %v; will retry in background", err)
		go r.reconnectPort()
		return r, nil
	}
	if err := r.configureDevice(); err != nil {
		r.logf("could not configure device: %v; will retry in background", err)
		r.stopReadLoopAndConn()
		go r.reconnectPort()
		return r, nil
	}
	return r, nil
}

// stopReadLoopAndConn stops the readLoop configureDevice may have started and
// closes the serial connection, leaving the interface in a clean offline state
// for a reconnect attempt.
func (r *RNodeInterface) stopReadLoopAndConn() {
	atomic.StoreInt32(&r.running, 0)
	atomic.StoreInt32(&r.online, 0)
	_ = r.closeConn()
}

// openPort opens and termios-configures the configured serial device.
// openPort opens the configured transport: a TCP connection for a tcp:// port
// URI (RNodeInterface.open_port TCP path), or a termios-configured serial
// device otherwise.
func (r *RNodeInterface) openPort() error {
	if r.useTCP {
		conn, err := net.DialTimeout("tcp", r.tcpHost, rnodeTCPDialTimeout)
		if err != nil {
			return err
		}
		r.connMu.Lock()
		r.conn = conn
		r.connMu.Unlock()
		return nil
	}
	file, err := r.openSerialFile(r.port)
	if err != nil {
		return err
	}
	if err := configureTermios(file.Fd(), r.speed, r.databits, r.parity, r.stopbits); err != nil {
		_ = file.Close()
		return err
	}
	r.connMu.Lock()
	r.conn = file
	r.connMu.Unlock()
	return nil
}

// openSerialFile opens the configured serial device via the shared
// openSerialPort helper, which on darwin retries /dev/cu.* when /dev/tty.*
// returns EBUSY (see openSerialPort). It logs the remap and remembers the
// working port for reconnect.
func (r *RNodeInterface) openSerialFile(port string) (*os.File, error) {
	file, effPort, err := openSerialPort(port)
	if err != nil {
		return nil, err
	}
	if effPort != port {
		r.logf("configured port %v was busy; using callout %v", port, effPort)
		r.port = effPort
	}
	return file, nil
}

func (r *RNodeInterface) closeConn() error {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	if r.conn == nil {
		return nil
	}
	err := r.conn.Close()
	r.conn = nil
	return err
}

// configureDevice mirrors RNodeInterface.configure_device: reset state, settle,
// start the readLoop, detect the device, then initRadio + validateRadioState.
func (r *RNodeInterface) configureDevice() error {
	r.resetRadioState()
	time.Sleep(r.openSettleDelay)

	atomic.StoreInt32(&r.running, 1)
	go r.readLoop()

	if err := r.detect(); err != nil {
		return err
	}
	// Wait for the device to answer the detect handshake. Python sleeps 0.2s
	// for serial and polls up to 5s for TCP/BLE; poll so detection completes as
	// soon as the readLoop sees DETECT_RESP (and never longer than the timeout).
	detectTimeout := r.detectWaitDelay
	if r.useTCP {
		detectTimeout = rnodeTCPDetectTimeout
	}
	deadline := time.Now().Add(detectTimeout)
	for time.Now().Before(deadline) {
		r.radioMu.RLock()
		det := r.radio.detected
		r.radioMu.RUnlock()
		if det {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	r.radioMu.RLock()
	detected := r.radio.detected
	platform := r.radio.platform
	r.radioMu.RUnlock()
	if !detected {
		return fmt.Errorf("could not detect device for %v", r.Name())
	}

	if platform != nil && (*platform == 0x80 || *platform == 0x70) {
		r.radioMu.Lock()
		r.radio.display = true
		r.radioMu.Unlock()
	}
	r.logf("Configuring RNode interface...")
	if err := r.initRadio(); err != nil {
		return err
	}

	// Validate firmware (Python validate_firmware panics on failure; the Go
	// port records the result and proceeds so a mis-versioned radio is still
	// observable in diagnostics rather than killing the process).
	r.radioMu.Lock()
	r.radio.validateFirmware()
	r.radioMu.Unlock()

	time.Sleep(r.validateSettleDelay)
	r.radioMu.RLock()
	valid := r.radio.validateRadioState()
	r.radioMu.RUnlock()
	if !valid {
		return fmt.Errorf("reported radio parameters did not match configuration for %v", r.Name())
	}

	atomic.StoreInt32(&r.interfaceReady, 1)
	r.logf("configured and powered up")
	time.Sleep(r.postOnlineDelay)
	atomic.StoreInt32(&r.online, 1)
	return nil
}

func (r *RNodeInterface) resetRadioState() {
	r.radioMu.Lock()
	defer r.radioMu.Unlock()
	r.radio.rFrequency = nil
	r.radio.rBandwidth = nil
	r.radio.rTXPower = nil
	r.radio.rSF = nil
	r.radio.rCR = nil
	r.radio.rState = nil
	r.radio.rLock = nil
	r.radio.detected = false
}

// detect writes the multi-command detect frame (RNodeInterface.detect).
func (r *RNodeInterface) detect() error {
	return r.writeRaw(RNodeDetect())
}

// initRadio sends the full radio configuration and powers the radio on
// (RNodeInterface.initRadio).
func (r *RNodeInterface) initRadio() error {
	for _, frame := range [][]byte{
		RNodeSetFrequency(uint32(r.radio.frequency)),
		RNodeSetBandwidth(uint32(r.radio.bandwidth)),
		RNodeSetTXPower(byte(r.radio.txpower)),
		RNodeSetSpreadingFactor(byte(r.radio.sf)),
		RNodeSetCodingRate(byte(r.radio.cr)),
		RNodeSetSTALock(r.radio.stAlock),
		RNodeSetLTALock(r.radio.ltAlock),
	} {
		if frame == nil {
			continue
		}
		if err := r.writeRaw(frame); err != nil {
			return err
		}
	}
	// setRadioState updates the configured state BEFORE writing so
	// validateRadioState compares the just-set state against the reported one
	// (RNodeInterface.setRadioState).
	r.radio.state = RadioStateOn
	return r.writeRaw(RNodeSetRadioState(RadioStateOn))
}

// readLoop mirrors RNodeInterface.readLoop: feed inbound bytes through the
// KISS decoder, deliver CMD_DATA payloads, act on hardware errors, and run
// the ID beacon / packet-queue idle work. On an unrecoverable error it marks
// the interface offline and triggers a reconnect.
func (r *RNodeInterface) readLoop() {
	for !r.IsDetached() {
		if err := r.readLoopOnce(); err != nil {
			atomic.StoreInt32(&r.online, 0)
			atomic.StoreInt32(&r.running, 0)
			if r.IsDetached() {
				return
			}
			r.logf("serial port error on %v: %v", r.Name(), err)
			r.panicOnInterfaceErrorf("rnode interface %v read failed: %v", r.Name(), err)
			_ = r.closeConn()
			if !r.IsDetached() {
				r.reconnectPort()
			}
			return
		}
	}
}

// readLoopOnce reads one chunk from the serial link and processes every byte.
// The KISS decoder writes r.radio, so the feed loop runs under radioMu; the
// inbound handler and queue/beacon work run outside the lock to avoid
// re-entrancy (the handler may call back into the interface).
func (r *RNodeInterface) readLoopOnce() error {
	r.connMu.Lock()
	conn := r.conn
	r.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("serial port closed")
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EIO) {
			time.Sleep(20 * time.Millisecond)
			return nil
		}
		if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
			return nil
		}
		return err
	}
	var delivered [][]byte
	var fatalErr error
	ready := false
	r.radioMu.Lock()
	for i := range n {
		data, ferr := r.decoder.feed(buf[i])
		if data != nil {
			delivered = append(delivered, data)
		}
		if ferr != nil && fatalErr == nil {
			fatalErr = ferr
		}
	}
	if r.radio.readySignal {
		r.radio.readySignal = false
		ready = true
	}
	r.radioMu.Unlock()

	for _, data := range delivered {
		atomic.AddUint64(&r.rxBytes, uint64(len(data)))
		if r.inboundHandler != nil {
			r.inboundHandler(data, r)
		}
	}
	if ready {
		r.processQueue()
	}
	r.maybeIDBeacon()
	if n == 0 {
		time.Sleep(rnodeReadIdleSleep)
	}
	return fatalErr
}

// maybeIDBeacon transmits the configured callsign once per id_interval after
// the first outbound transmission (RNodeInterface.readLoop idle branch).
func (r *RNodeInterface) maybeIDBeacon() {
	if r.idInterval <= 0 || len(r.idCallsign) == 0 {
		return
	}
	r.radioMu.RLock()
	firstTx := r.firstTx
	r.radioMu.RUnlock()
	if firstTx.IsZero() {
		return
	}
	if time.Since(firstTx) < time.Duration(r.idInterval)*time.Second {
		return
	}
	r.logf("interface %v is transmitting beacon data: %v", r.Name(), string(r.idCallsign))
	r.radioMu.Lock()
	r.firstTx = time.Time{}
	r.radioMu.Unlock()
	_ = r.processOutgoing(r.idCallsign)
}

// Send implements Interface.Send: process_outgoing with flow control.
func (r *RNodeInterface) Send(data []byte) error {
	return r.processOutgoing(data)
}

// processOutgoing mirrors RNodeInterface.process_outgoing. When the radio is
// online and ready it KISS-frames and writes the payload (toggling
// interface_ready when flow control is enabled); otherwise it queues the
// payload for later transmission.
func (r *RNodeInterface) processOutgoing(data []byte) error {
	if atomic.LoadInt32(&r.online) != 1 {
		return fmt.Errorf("interface %v is not online", r.Name())
	}
	if atomic.LoadInt32(&r.interfaceReady) != 1 {
		r.queue(data)
		return nil
	}
	if r.flowControl {
		atomic.StoreInt32(&r.interfaceReady, 0)
	}
	r.radioMu.Lock()
	if bytesEqual(data, r.idCallsign) {
		r.firstTx = time.Time{}
	} else if r.firstTx.IsZero() {
		r.firstTx = time.Now()
	}
	r.radioMu.Unlock()
	frame := rnodeDataFrame(data)
	if err := r.writeRaw(frame); err != nil {
		return err
	}
	atomic.AddUint64(&r.txBytes, uint64(len(data)))
	return nil
}

func (r *RNodeInterface) queue(data []byte) {
	r.queueMu.Lock()
	r.packetQueue = append(r.packetQueue, data)
	r.queueMu.Unlock()
}

// processQueue mirrors RNodeInterface.process_queue: flush the pending queue
// when the radio signals CMD_READY.
func (r *RNodeInterface) processQueue() {
	r.queueMu.Lock()
	if len(r.packetQueue) == 0 {
		r.queueMu.Unlock()
		atomic.StoreInt32(&r.interfaceReady, 1)
		return
	}
	data := r.packetQueue[0]
	r.packetQueue = r.packetQueue[1:]
	r.queueMu.Unlock()
	atomic.StoreInt32(&r.interfaceReady, 1)
	_ = r.processOutgoing(data)
}

// reconnectPort mirrors RNodeInterface.reconnect_port: periodically reopen and
// reconfigure the device until it is online or detached.
func (r *RNodeInterface) reconnectPort() {
	if !atomic.CompareAndSwapInt32(&r.reconnecting, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&r.reconnecting, 0)
	for atomic.LoadInt32(&r.online) != 1 && !r.IsDetached() {
		time.Sleep(rnodeReconnectWait)
		if r.IsDetached() {
			return
		}
		if err := r.openPort(); err != nil {
			r.logf("error while reconnecting %v: %v", r.Name(), err)
			continue
		}
		if err := r.configureDevice(); err != nil {
			r.logf("error while reconfiguring %v: %v", r.Name(), err)
			// configureDevice started a readLoop before failing; stop it and
			// release the port so the next attempt opens cleanly.
			r.stopReadLoopAndConn()
		}
	}
	if atomic.LoadInt32(&r.online) == 1 {
		r.logf("reconnected port for %v", r.Name())
	}
}

// Detach implements Interface.Detach: cleanly power down the radio, tell it the
// host is leaving, disable the external framebuffer, and close the port
// (RNodeInterface.detach).
func (r *RNodeInterface) Detach() error {
	r.SetDetached(true)
	atomic.StoreInt32(&r.online, 0)
	atomic.StoreInt32(&r.running, 0)
	r.radioMu.RLock()
	display := r.radio.display
	r.radioMu.RUnlock()
	if display {
		_ = r.writeRaw(RNodeDisableExternalFramebuffer())
	}
	r.radioMu.Lock()
	r.radio.state = RadioStateOff
	r.radioMu.Unlock()
	_ = r.writeRaw(RNodeSetRadioState(RadioStateOff))
	_ = r.writeRaw(RNodeLeave())
	return r.closeConn()
}

// writeRaw writes a complete KISS frame to the serial link, failing if a
// short write leaves the device with a partial frame.
func (r *RNodeInterface) writeRaw(frame []byte) error {
	r.connMu.Lock()
	conn := r.conn
	r.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("rnode interface %v has no open port", r.Name())
	}
	n, err := conn.Write(frame)
	if err != nil {
		return err
	}
	if n != len(frame) {
		return fmt.Errorf("rnode interface %v only wrote %v of %v bytes", r.Name(), n, len(frame))
	}
	return nil
}

// rnodeDataFrame builds a CMD_DATA KISS frame for an outbound payload.
func rnodeDataFrame(data []byte) []byte {
	frame := append([]byte{KISSFend, KISSCmdData}, KISSEscape(data)...)
	frame = append(frame, KISSFend)
	return frame
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *RNodeInterface) logf(format string, args ...any) {
	log.Printf("Go RNodeInterface %v: "+format, append([]any{r.Name()}, args...)...)
}

// Type identifies this interface as an RNode radio interface.
func (r *RNodeInterface) Type() string { return "RNodeInterface" }

// IsOut reports whether the RNode can originate outbound traffic.
func (r *RNodeInterface) IsOut() bool { return true }

// Status reports whether the RNode is configured, powered up, and online.
func (r *RNodeInterface) Status() bool { return atomic.LoadInt32(&r.online) == 1 }

// Bitrate returns the on-air bitrate computed from the reported radio
// parameters (RNodeInterface.updateBitrate); 0 until the radio reports back.
func (r *RNodeInterface) Bitrate() int {
	r.radioMu.RLock()
	defer r.radioMu.RUnlock()
	return r.radio.bitrate
}

// radioSnapshot returns a shallow copy of the radio state under radioMu. The
// readLoop only ever replaces the pointer-typed reported fields (it never
// mutates the value a pointer already points at), so a snapshot's pointers
// stay safe to dereference after the lock is released.
func (r *RNodeInterface) radioSnapshot() rnodeRadioState {
	r.radioMu.RLock()
	defer r.radioMu.RUnlock()
	return r.radio
}

// RNode telemetry accessors expose the radio state populated by the inbound
// parser, for diagnostics (gornstatus ifstats parity with Python rnstatus).

// RNodeDetected reports whether the device answered the detect handshake.
func (r *RNodeInterface) RNodeDetected() bool { return r.radioSnapshot().detected }

// RNodeFirmwareVersion returns the reported firmware version as major, minor.
func (r *RNodeInterface) RNodeFirmwareVersion() (int, int) {
	s := r.radioSnapshot()
	return s.majVersion, s.minVersion
}

// RNodeSymbolRate returns the reported symbol rate in baud, or 0 if unreported.
func (r *RNodeInterface) RNodeSymbolRate() int {
	s := r.radioSnapshot()
	if s.rSymbolRate != nil {
		return *s.rSymbolRate
	}
	return 0
}

// RNodeSymbolTimeMs returns the reported symbol time in milliseconds.
func (r *RNodeInterface) RNodeSymbolTimeMs() float64 {
	s := r.radioSnapshot()
	if s.rSymbolTimeMs != nil {
		return *s.rSymbolTimeMs
	}
	return 0
}

// RNodePreamble returns the reported preamble symbol count and time in ms.
func (r *RNodeInterface) RNodePreamble() (int, int) {
	s := r.radioSnapshot()
	sym, ms := 0, 0
	if s.rPreambleSymbols != nil {
		sym = *s.rPreambleSymbols
	}
	if s.rPreambleTimeMs != nil {
		ms = *s.rPreambleTimeMs
	}
	return sym, ms
}

// RNodeCSMA returns the reported CSMA slot time and DIFS in ms.
func (r *RNodeInterface) RNodeCSMA() (int, int) {
	s := r.radioSnapshot()
	slot, difs := 0, 0
	if s.rCSMASlotTimeMs != nil {
		slot = *s.rCSMASlotTimeMs
	}
	if s.rCSMADifsMs != nil {
		difs = *s.rCSMADifsMs
	}
	return slot, difs
}

// RNodeRSSI returns the last received-frame RSSI in dBm, or 0 if unreported.
func (r *RNodeInterface) RNodeRSSI() int {
	s := r.radioSnapshot()
	if s.rStatRSSI != nil {
		return *s.rStatRSSI
	}
	return 0
}

// RNodeSNR returns the last received-frame SNR in dB, or 0 if unreported.
func (r *RNodeInterface) RNodeSNR() float64 {
	s := r.radioSnapshot()
	if s.rStatSNR != nil {
		return *s.rStatSNR
	}
	return 0
}

// RNodeSignalQuality returns the SNR-derived link quality (0–100), or 0.
func (r *RNodeInterface) RNodeSignalQuality() float64 {
	s := r.radioSnapshot()
	if s.rStatQ != nil {
		return *s.rStatQ
	}
	return 0
}

// RNodeAirtime returns the short- and long-term airtime percentages.
func (r *RNodeInterface) RNodeAirtime() (float64, float64) {
	s := r.radioSnapshot()
	return s.rAirtimeShort, s.rAirtimeLong
}

// RNodeChannelLoad returns the short- and long-term channel load percentages.
func (r *RNodeInterface) RNodeChannelLoad() (float64, float64) {
	s := r.radioSnapshot()
	return s.rChanLoadShort, s.rChanLoadLong
}

// RNodeBattery returns the battery state byte and charge percent (0–100).
func (r *RNodeInterface) RNodeBattery() (byte, int) {
	s := r.radioSnapshot()
	if s.rBatteryState != nil && s.rBatteryPercent != nil {
		return *s.rBatteryState, *s.rBatteryPercent
	}
	return 0, 0
}

// RNodeTemperature returns the reported CPU temperature in °C, or 0.
func (r *RNodeInterface) RNodeTemperature() int {
	s := r.radioSnapshot()
	if s.rTemperature != nil {
		return *s.rTemperature
	}
	return 0
}

// RNodeCurrentRSSI returns the current-channel RSSI in dBm, or 0.
func (r *RNodeInterface) RNodeCurrentRSSI() int {
	s := r.radioSnapshot()
	if s.rCurrentRSSI != nil {
		return *s.rCurrentRSSI
	}
	return 0
}

// RNodeNoiseFloor returns the noise floor in dBm, or 0.
func (r *RNodeInterface) RNodeNoiseFloor() int {
	s := r.radioSnapshot()
	if s.rNoiseFloor != nil {
		return *s.rNoiseFloor
	}
	return 0
}

// RNodeNoiseFloorDBm returns the noise floor in dBm, or nil when the radio has
// not reported it (Python None → ifstats "Unknown").
func (r *RNodeInterface) RNodeNoiseFloorDBm() *float64 {
	s := r.radioSnapshot()
	if s.rNoiseFloor != nil {
		f := float64(*s.rNoiseFloor)
		return &f
	}
	return nil
}

// RNodeInterferenceDBm returns the interference level in dBm, or nil.
func (r *RNodeInterface) RNodeInterferenceDBm() *float64 {
	s := r.radioSnapshot()
	if s.rInterference != nil {
		f := float64(*s.rInterference)
		return &f
	}
	return nil
}

// RNodeCPUTempC returns the reported CPU temperature in °C, or nil (Python
// cpu_temp, which RNode sets from r_temperature).
func (r *RNodeInterface) RNodeCPUTempC() *float64 {
	s := r.radioSnapshot()
	if s.rTemperature != nil {
		f := float64(*s.rTemperature)
		return &f
	}
	return nil
}

// RNodeAirtimeShort/Long return the short-/long-term airtime percentages
// (default 0.0, always emitted in ifstats — Python r_airtime_short/long).
func (r *RNodeInterface) RNodeAirtimeShort() float64 { return r.radioSnapshot().rAirtimeShort }
func (r *RNodeInterface) RNodeAirtimeLong() float64  { return r.radioSnapshot().rAirtimeLong }

// RNodeChannelLoadShort/Long return the short-/long-term channel load
// percentages (default 0.0).
func (r *RNodeInterface) RNodeChannelLoadShort() float64 { return r.radioSnapshot().rChanLoadShort }
func (r *RNodeInterface) RNodeChannelLoadLong() float64  { return r.radioSnapshot().rChanLoadLong }

// RNodeBatteryPercent returns the reported battery charge percent (0–100).
func (r *RNodeInterface) RNodeBatteryPercent() int {
	s := r.radioSnapshot()
	if s.rBatteryPercent != nil {
		return *s.rBatteryPercent
	}
	return 0
}

// RNodeBatteryStateStr returns the battery-state string ("charged"/"charging"/
// "discharging") or "" when unreported (Python only emits when state != 0).
func (r *RNodeInterface) RNodeBatteryStateStr() string {
	s := r.radioSnapshot()
	if s.rBatteryState == nil || *s.rBatteryState == 0x00 {
		return ""
	}
	return rnodeBatteryStateString(*s.rBatteryState)
}

// rnodeBatteryStateString mirrors RNodeInterface.get_battery_state_string.
func rnodeBatteryStateString(state byte) string {
	switch state {
	case 0x03:
		return "charged"
	case 0x02:
		return "charging"
	case 0x01:
		return "discharging"
	default:
		return "unknown"
	}
}
