// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornode-diagnostics sniffs a machine's serial ports for LoRa RNode radios
// (completely ignoring ~/.reticulum/config) and runs a coordinated fleet
// radio test: every participating node transmits uniquely-identified test
// packets over LoRa and acknowledges the packets it hears from the other
// nodes, collecting per-radio firmware statistics for a final summary report
// of how well each radio transmits and receives across the fleet.
//
// Serial lines cannot be shared: if every RNode found is currently held by
// another process (e.g. gornsd), the program stops with an error instead of
// corrupting the other holder's traffic.
package main

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	mrand "math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const usageText = `gornode-diagnostics — LoRa RNode fleet radio diagnostics

Sniffs every serial device on this machine for an RNode LoRa radio (ignoring
~/.reticulum/config entirely), then — if an RNode is found and free — runs a
timed over-the-air fleet test and prints a per-radio summary report.

Fleet usage: start this program on every node inside the grace window (all
nodes must use the same -frequency/-bandwidth/-sf/-cr/-txpower settings).
Each node waits the grace period, transmits uniquely-identified test packets
for the test duration, acknowledges every 5th peer packet it hears by default
(-ack-every N to tune: 1 = every packet, higher = lighter channel load), and
prints a report. To find a radio that cannot transmit: compare the reports —
a nodeID that appears in its own "sent" counts but in NO peer's "packets
heard" table belongs to a radio whose transmitter is not putting out RF.

Usage:
  gornode-diagnostics [flags]`

const (
	diagMagic   = "GRND" // fixed prefix known only to this testing program
	diagVersion = 0x01

	diagTypeTest = 0x01
	diagTypeAck  = 0x02

	// RNode protocol commands (local copies of RNS/Interfaces/kiss.go,
	// rnode-multi-spawn.go and rnode-state.go constants so this tool stays
	// independent of the interfaces package internals).
	kissFend  = 0xC0
	kissFesc  = 0xDB
	kissTfend = 0xDC
	kissTfesc = 0xDD

	kissCmdData       = 0x00
	kissCmdFrequency  = 0x01
	kissCmdBandwidth  = 0x02
	kissCmdTXPower    = 0x03
	kissCmdSF         = 0x04
	kissCmdCR         = 0x05
	kissCmdRadioState = 0x06
	kissCmdDetect     = 0x08
	kissCmdLeave      = 0x0A
	kissCmdSTALock    = 0x0B
	kissCmdLTALock    = 0x0C
	kissCmdReady      = 0x0F
	kissCmdBoard      = 0x47
	kissCmdPlatform   = 0x48
	kissCmdMcu        = 0x49
	kissCmdFwVersion  = 0x50
	kissCmdError      = 0x90
	kissCmdDetectReq  = 0x73
	kissCmdDetectResp = 0x46

	// RNode firmware statistics query/response commands
	// (RNodeInterface.py stat burst, rnode-state.go handlers).
	statCmdRX   = 0x21 // CMD_STAT_RX
	statCmdTX   = 0x22 // CMD_STAT_TX
	statCmdRSSI = 0x23 // CMD_STAT_RSSI
	statCmdSNR  = 0x24 // CMD_STAT_SNR
	statCmdCHTM = 0x25 // CMD_STAT_CHTM
	statCmdPHY  = 0x26 // CMD_STAT_PHYPRM
	statCmdBAT  = 0x27 // CMD_STAT_BAT
	statCmdCSMA = 0x28 // CMD_STAT_CSMA
	statCmdTEMP = 0x29 // CMD_STAT_TEMP

	rnodeRSSIOffset = 157

	// Firmware error codes (RNodeInterface.py ERROR_*).
	errorInitRadio    = 0x01
	errorTXFailed     = 0x02
	errorMemoryLow    = 0x05
	errorModemTimeout = 0x06

	radioStateOn  = 0x01
	radioStateOff = 0x00

	probeSettleDelay = 2 * time.Second // RNodeInterface open_settle_delay
	probeReadWindow  = 1500 * time.Millisecond
	statPollInterval = 15 * time.Second
	progressInterval = 30 * time.Second
	maxRadioMsgs     = 50 // bounded log of radio message strings / unknown frames
)

// Fleet test settings (flag values; package-level so the radio methods and
// reports can read them).
var (
	portArg   = flag.String("port", "", "comma-separated serial ports to probe (default: sniff every serial device)")
	grace     = flag.Int("grace", 60, "grace period in seconds to wait before starting the test (lets all fleet nodes launch)")
	duration  = flag.Int("duration", 300, "test duration in seconds")
	interval  = flag.Float64("interval", 3, "seconds between transmitted test packets (jittered ±0.4s)")
	frequency = flag.Int("frequency", 915000000, "LoRa frequency in Hz")
	bandwidth = flag.Int("bandwidth", 125000, "LoRa bandwidth in Hz")
	txpower   = flag.Int("txpower", 17, "LoRa TX power in dBm")
	sf        = flag.Int("sf", 9, "LoRa spreading factor (5-12)")
	cr        = flag.Int("cr", 5, "LoRa coding rate (5-8)")
	speed     = flag.Int("speed", 115200, "serial baud rate")
	ackEvery  = flag.Int("ack-every", 5, "acknowledge every Nth test packet heard from each peer (1 = every packet, heaviest channel load; the default 5 keeps a 1757 bps LoRa channel from saturating on large fleets)")
	sniffOnly = flag.Bool("sniff-only", false, "only sniff for RNodes, report findings, and exit")
)

func main() {
	log.SetFlags(log.Ltime)

	flag.Usage = func() {
		fmt.Println(usageText)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *ackEvery < 1 {
		log.Printf("WARNING: -ack-every must be >= 1; using 1")
		*ackEvery = 1
	}

	log.Printf("gornode-diagnostics: sniffing serial devices for RNode radios (ignoring ~/.reticulum/config)")

	// Phase 1: sniff all serial ports for RNodes.
	radios := sniffSerialPorts(*portArg, *speed)
	reportSniff(radios)

	busyCount := 0
	for _, r := range radios {
		if r.busy {
			busyCount++
		}
	}
	if len(radios) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: no serial device that could hold an RNode was found on this machine; nothing to test.\n")
		os.Exit(1)
	}
	freeCount := 0
	for _, r := range radios {
		if r.detected() && !r.busy {
			freeCount++
		}
	}
	if freeCount == 0 && busyCount == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: no RNode detected on any serial device on this machine; nothing to test.\n")
		os.Exit(1)
	}

	if *sniffOnly {
		return
	}

	free := testCandidates(radios)
	if len(free) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: %v currently unavailable (in use by another process — serial lines cannot be shared); cannot continue.\n", unavailableNoun(busyCount))
		os.Exit(2)
	}
	if len(free) < busyCount+freeCount {
		log.Printf("skipping unavailable RNode(s); testing the remaining %v", len(free))
	}

	// Phase 2: configure the free radios for the test channel.
	for _, r := range free {
		if err := r.configure(); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: could not configure RNode on %v: %v\n", r.port, err)
			r.markFailed()
		}
	}
	testable := configuredRadios(free)
	if len(testable) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: no RNode could be configured for the test; cannot continue.\n")
		os.Exit(2)
	}

	// Phase 3: announce and wait out the grace period so every fleet node
	// starts the test in the same window.
	for _, r := range testable {
		log.Printf("about to test RNode on %v (nodeID %x) — grace %vs, test %vs, then summary report", r.port, r.nodeID, *grace, *duration)
	}
	// The idle grace window is a good moment to fill in any identity details
	// (firmware version, platform) the radios have not reported yet.
	for _, r := range testable {
		if !r.detailsKnown() {
			r.requestRadioDetails()
		}
	}
	waitGrace(*grace)
	for _, r := range testable {
		if !r.detailsKnown() {
			r.requestRadioDetails()
		}
	}

	// Phase 4: run the fleet test. All radios on this machine run concurrently
	// in the same window so they hear the same fleet traffic (one radio per
	// machine is the normal case).
	log.Printf("=== TEST WINDOW OPEN (%vs): transmitting and acknowledging fleet packets on %v radio(s) ===", *duration, len(testable))
	runTest(testable, *duration, *interval)

	// Phase 5: wind down and report.
	for _, r := range testable {
		_ = r.shutdown()
	}
	log.Printf("=== TEST COMPLETE — summary report ===")
	for _, r := range testable {
		fmt.Println()
		r.report(*duration)
	}
	fmt.Println()
	fmt.Println("Fleet comparison hint: collect the report from every node. A nodeID that")
	fmt.Println("transmitted packets but appears in NO peer's 'packets heard' table belongs")
	fmt.Println("to a radio whose transmitter is not putting out RF (receive-only).")
}

func unavailableNoun(busyCount int) string {
	if busyCount == 1 {
		return "the one RNode is"
	}
	return "all RNodes are"
}

// ---------------------------------------------------------------------------
// Serial port sniffing

type radio struct {
	port string

	busy     bool
	busyHint string
	openErr  error

	file    *os.File
	writeMu sync.Mutex

	nodeID [8]byte

	stateMu sync.RWMutex
	state   radioState

	// windowOpen gates fleet-packet handling: packets delivered before the
	// test window opens are stale (left buffered by a previous run or another
	// program) or belong to a foreign process, and must not be counted or
	// acknowledged. Guarded by stateMu (handleFleetPacket runs with it held).
	windowOpen bool

	txCounterStart *uint32 // firmware TX counter snapshot at test start

	sentPackets  int
	heardPeers   map[[8]byte]bool
	heardPackets map[[8]byte]int // peer nodeID -> TEST packet count
	ackedPackets map[string]bool // "peer:seq" -> already acknowledged
	ackReceipts  map[[8]byte]int // peer nodeID -> ACK count for our packets
	rtts         map[[8]byte][]time.Duration
	txTimes      map[uint32]time.Time

	logMu sync.Mutex
}

// detected reports whether an RNode answered the detect handshake. The parser
// may be feeding r.state concurrently, so read under the state lock.
func (r *radio) detected() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state.detected
}

func (r *radio) markFailed() {
	r.writeMu.Lock()
	file := r.file
	r.file = nil
	r.writeMu.Unlock()
	if file != nil {
		_ = file.Close()
	}
}

func (r *radio) shutdown() error {
	r.writeMu.Lock()
	file := r.file
	r.file = nil
	r.writeMu.Unlock()
	if file == nil {
		return nil
	}
	// Power the radio down and tell it the host is leaving
	// (RNodeInterface.detach).
	_, _ = file.Write(kissFrame(kissCmdRadioState, []byte{radioStateOff}))
	_, _ = file.Write(kissFrame(kissCmdLeave, nil))
	return file.Close()
}

func newRadio(port string) *radio {
	return &radio{
		port:         port,
		heardPeers:   map[[8]byte]bool{},
		heardPackets: map[[8]byte]int{},
		ackedPackets: map[string]bool{},
		ackReceipts:  map[[8]byte]int{},
		rtts:         map[[8]byte][]time.Duration{},
		txTimes:      map[uint32]time.Time{},
	}
}

// sniffSerialPorts probes every candidate serial device (or the explicit
// -port list) and returns one radio record per device, detected RNode or not,
// so the report can also flag ports that look like an RNode but are busy.
func sniffSerialPorts(portArg string, speed int) []*radio {
	var candidates []string
	if strings.TrimSpace(portArg) != "" {
		for _, p := range strings.Split(portArg, ",") {
			if p = strings.TrimSpace(p); p != "" {
				candidates = append(candidates, p)
			}
		}
	} else {
		candidates = enumerateSerialPorts()
	}

	var radios []*radio
	for _, port := range candidates {
		r := newRadio(port)
		radios = append(radios, r)

		// Busy check FIRST: a port held by another process (e.g. gornsd) must
		// not be probed — serial lines cannot be shared for TX/RX.
		if busy, hint := serialBusyCheck(port); busy {
			r.busy = true
			r.busyHint = hint
			continue
		}

		file, err := os.OpenFile(port, os.O_RDWR|nocttyFlag(), 0)
		if err != nil {
			r.openErr = err
			if errors.Is(err, errBusy()) {
				r.busy = true
				r.busyHint = "open() returned busy — another process is using this serial line"
			}
			continue
		}
		if err := postOpenSerialPort(file); err != nil {
			r.openErr = fmt.Errorf("post-open setup failed: %w", err)
			_ = file.Close()
			continue
		}
		r.writeMu.Lock()
		r.file = file
		r.writeMu.Unlock()
		if err := configureSerialPort(file.Fd(), speed); err != nil {
			r.openErr = fmt.Errorf("termios setup failed: %w", err)
			r.markFailed()
			continue
		}
		// Discard anything buffered on the serial line from before this open —
		// packets a previous run (or another program) left unread can sit in
		// the host/tty or firmware queue for a long time and would otherwise
		// be misread as fleet traffic (fleet packets are also gated on the
		// test window opening, so this belt-and-braces flush is not load-
		// bearing, but it keeps the probe honest).
		_ = flushSerialInput(file.Fd())
		if !probeRNode(r) {
			r.markFailed()
		}
	}
	return radios
}

func reportSniff(rs []*radio) {
	fmt.Println()
	fmt.Println("--- Serial port sniff results (ignoring ~/.reticulum/config) ---")
	for _, r := range rs {
		switch {
		case r.busy:
			fmt.Printf("  %v: IN USE — %v (suspected RNode; unavailable for testing)\n", r.port, r.busyHint)
		case r.openErr != nil:
			fmt.Printf("  %v: could not open — %v\n", r.port, r.openErr)
		case r.detected():
			fmt.Printf("  %v: RNode DETECTED — %v\n", r.port, r.describeState())
		default:
			fmt.Printf("  %v: no RNode detected (not a LoRa RNode)\n", r.port)
		}
	}
	fmt.Println()
}

// enumerateSerialPorts returns every device path that MIGHT be a serial RNode
// on this machine: USB CDC-ACM/adapters on Linux (including /dev/serial/by-id
// aliases) and macOS callout devices matching the known USB-serial families.
func enumerateSerialPorts() []string {
	var patterns []string
	switch runtime.GOOS {
	case "darwin":
		patterns = []string{
			"/dev/cu.usbmodem*",
			"/dev/cu.usbserial*",
			"/dev/cu.*jtag*",
			"/dev/cu.*qtag*",
			"/dev/cu.usb*",
		}
	case "linux":
		patterns = []string{
			"/dev/ttyACM*",
			"/dev/ttyUSB*",
			"/dev/serial/by-id/*",
		}
	default:
		patterns = []string{"/dev/ttyACM*", "/dev/ttyUSB*", "/dev/tty.usbmodem*", "/dev/tty.usbserial*", "/dev/cu.usb*"}
	}

	seen := map[string]bool{}
	var out []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)

	// Several paths can name the same physical device (e.g. Linux exposes one
	// radio as both /dev/serial/by-id/…-if00 and /dev/ttyACM0). Probing both
	// wastes a sniff slot and reports the radio twice, so keep the first path
	// per underlying device (sorted order puts the descriptive by-id path
	// first).
	return dedupeSameDevices(out)
}

// dedupeSameDevices drops later paths that name a device already represented
// by an earlier path in the list (same character-device ID). Paths that are
// not device nodes (or that cannot be statted) are kept as-is.
func dedupeSameDevices(paths []string) []string {
	var out []string
	seenDevices := map[uint64]bool{}
	for _, p := range paths {
		id, ok := deviceID(p)
		if !ok {
			out = append(out, p)
			continue
		}
		if seenDevices[id] {
			continue
		}
		seenDevices[id] = true
		out = append(out, p)
	}
	return out
}

func testCandidates(rs []*radio) []*radio {
	var out []*radio
	for _, r := range rs {
		if !r.busy && r.file != nil && r.openErr == nil {
			out = append(out, r)
		}
	}
	return out
}

func configuredRadios(rs []*radio) []*radio {
	var out []*radio
	for _, r := range rs {
		r.stateMu.RLock()
		configured := r.state.configured
		r.stateMu.RUnlock()
		if configured {
			out = append(out, r)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// RNode detection probe

// probeRNode sends the RNode detect handshake burst over an open, termios-
// configured port and waits for the detect response. It is independent of
// ~/.reticulum/config: detection is purely protocol-based.
func probeRNode(r *radio) bool {
	// RNodeInterface.detect: detect request + fw version, platform, MCU and
	// board queries.
	burst := [][]byte{
		kissFrame(kissCmdDetect, []byte{kissCmdDetectReq}),
		kissFrame(kissCmdFwVersion, nil),
		kissFrame(kissCmdPlatform, nil),
		kissFrame(kissCmdMcu, nil),
		kissFrame(kissCmdBoard, nil),
	}
	time.Sleep(probeSettleDelay)
	r.writeMu.Lock()
	for _, frame := range burst {
		if _, err := r.file.Write(frame); err != nil {
			r.writeMu.Unlock()
			r.stateMu.Lock()
			r.state.fatal = err
			r.stateMu.Unlock()
			return false
		}
	}
	r.writeMu.Unlock()

	deadline := time.Now().Add(probeReadWindow)
	buf := make([]byte, 512)
	parser := r.newParser()
	detected := false
	for time.Now().Before(deadline) {
		n, err := r.read(buf)
		if n > 0 {
			parser.feedBytes(buf[:n])
		}
		if err != nil && !isReadTimeout(err) {
			r.stateMu.Lock()
			r.state.fatal = err
			r.stateMu.Unlock()
			break
		}
		if r.detected() {
			detected = true
			break
		}
	}
	if !detected {
		return false
	}
	// Live units sometimes answer the DETECT request but leave the firmware
	// version, platform, MCU and board queries unanswered within the first
	// read window; re-ask for those so the report can show full identity
	// details.
	for attempt := 0; attempt < 2 && !r.detailsKnown(); attempt++ {
		r.requestRadioDetails()
	}
	return true
}

// detailsKnown reports whether the radio's platform and firmware-version
// replies have both been received.
func (r *radio) detailsKnown() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state.platform != nil && (r.state.fwMaj != 0 || r.state.fwMin != 0)
}

// requestRadioDetails re-sends the firmware-version, platform, MCU and board
// queries and waits one probe window for the responses.
func (r *radio) requestRadioDetails() {
	queries := [][]byte{
		kissFrame(kissCmdFwVersion, nil),
		kissFrame(kissCmdPlatform, nil),
		kissFrame(kissCmdMcu, nil),
		kissFrame(kissCmdBoard, nil),
	}
	r.writeMu.Lock()
	if r.file == nil {
		r.writeMu.Unlock()
		return
	}
	for _, frame := range queries {
		if _, err := r.file.Write(frame); err != nil {
			break
		}
	}
	r.writeMu.Unlock()

	parser := r.newParser()
	deadline := time.Now().Add(probeReadWindow)
	buf := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, err := r.read(buf)
		if n > 0 {
			parser.feedBytes(buf[:n])
		}
		if err != nil && !isReadTimeout(err) {
			return
		}
	}
}

// read reads from the open port under the port pointer lock.
func (r *radio) read(buf []byte) (int, error) {
	r.writeMu.Lock()
	file := r.file
	r.writeMu.Unlock()
	if file == nil {
		return 0, errors.New("port closed")
	}
	return file.Read(buf)
}

// isReadTimeout reports whether a read error is just the termios VTIME
// inter-byte timeout / a pollable deadline elapsing (no data available).
func isReadTimeout(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, errTimeout())
}

// ---------------------------------------------------------------------------
// Radio configuration and the fleet test

func (r *radio) configure() error {
	log.Printf("configuring RNode on %v: %v Hz, BW %v, SF%v, CR%v, TX %v dBm", r.port, *frequency, *bandwidth, *sf, *cr, *txpower)

	// initRadio equivalent (RNodeInterface.initRadio): full config + power on.
	cfg := [][]byte{
		kissFrame(kissCmdFrequency, be32(*frequency)),
		kissFrame(kissCmdBandwidth, be32(*bandwidth)),
		kissFrame(kissCmdTXPower, []byte{byte(*txpower)}),
		kissFrame(kissCmdSF, []byte{byte(*sf)}),
		kissFrame(kissCmdCR, []byte{byte(*cr)}),
		kissFrame(kissCmdRadioState, []byte{radioStateOn}),
	}
	r.writeMu.Lock()
	for _, frame := range cfg {
		if _, err := r.file.Write(frame); err != nil {
			r.writeMu.Unlock()
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.writeMu.Unlock()

	parser := r.newParser()
	deadline := time.Now().Add(2 * time.Second)
	buf := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, err := r.read(buf)
		if n > 0 {
			parser.feedBytes(buf[:n])
		}
		if err != nil && !isReadTimeout(err) {
			return err
		}
		r.stateMu.RLock()
		done := r.state.allConfigReported()
		fatal := r.state.fatal
		r.stateMu.RUnlock()
		if fatal != nil {
			return fmt.Errorf("radio reported fatal error: %v", fatal)
		}
		if done {
			break
		}
	}

	if mismatches := r.validateConfig(); len(mismatches) > 0 {
		log.Printf("WARNING: RNode on %v reported %v", r.port, strings.Join(mismatches, "; "))
	}

	if _, err := rand.Read(r.nodeID[:]); err != nil {
		return err
	}
	r.stateMu.Lock()
	r.state.configured = true
	r.stateMu.Unlock()
	return nil
}

// validateConfig compares reported radio parameters against the flags.
func (r *radio) validateConfig() []string {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state.validate(*frequency, *bandwidth, *txpower, *sf, *cr)
}

func (r *radio) describeState() string {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state.describe()
}

func be32(v int) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

// runTest transmits uniquely-identified test packets and acknowledges every
// peer packet until the duration elapses. All radios on this machine run
// concurrently so they share the fleet's test window.
func runTest(radios []*radio, durationSec int, intervalSec float64) {
	deadline := time.Now().Add(time.Duration(durationSec) * time.Second)

	// Snapshot the firmware TX counter at window open so the report can show
	// how many packets the firmware actually transmitted during the test.
	for _, r := range radios {
		r.pollStats()
		r.stateMu.Lock()
		r.windowOpen = true
		r.stateMu.Unlock()
		r.stateMu.RLock()
		if r.state.rStatTX != nil {
			v := *r.state.rStatTX
			r.txCounterStart = &v
		}
		r.stateMu.RUnlock()
	}

	var wg sync.WaitGroup
	for _, r := range radios {
		wg.Add(2)
		go r.txLoop(deadline, time.Duration(intervalSec*float64(time.Second)), &wg)
		go r.rxLoop(deadline, &wg)
	}

	statTicker := time.NewTicker(statPollInterval)
	progressTicker := time.NewTicker(progressInterval)
	defer statTicker.Stop()
	defer progressTicker.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-statTicker.C:
			for _, r := range radios {
				r.pollStats()
			}
		case <-progressTicker.C:
			for _, r := range radios {
				r.logProgress()
			}
		default:
			time.Sleep(250 * time.Millisecond)
		}
	}
	for _, r := range radios {
		r.pollStats() // final counter snapshot
		r.logProgress()
	}
	wg.Wait()
}

// packet layout (all integers big-endian):
//
//	TEST: magic[4] ver[1] type[1] nodeID[8] seq[4] txMs[8]              = 26 bytes
//	ACK:  magic[4] ver[1] type[1] nodeID[8] seq[4] txMs[8]
//	      originNodeID[8] originSeq[4]                                  = 38 bytes
func buildPacket(typ byte, nodeID [8]byte, seq uint32, txMs uint64, origin [8]byte, originSeq uint32) []byte {
	p := make([]byte, 0, 38)
	p = append(p, diagMagic...)
	p = append(p, diagVersion, typ)
	p = append(p, nodeID[:]...)
	p = append(p, byte(seq>>24), byte(seq>>16), byte(seq>>8), byte(seq))
	p = append(p, byte(txMs>>56), byte(txMs>>48), byte(txMs>>40), byte(txMs>>32), byte(txMs>>24), byte(txMs>>16), byte(txMs>>8), byte(txMs))
	if typ == diagTypeAck {
		p = append(p, origin[:]...)
		p = append(p, byte(originSeq>>24), byte(originSeq>>16), byte(originSeq>>8), byte(originSeq))
	}
	return p
}

func parsePacket(p []byte) (typ byte, nodeID [8]byte, seq uint32, origin [8]byte, originSeq uint32, ok bool) {
	if len(p) < 26 || string(p[:4]) != diagMagic || p[4] != diagVersion {
		return 0, nodeID, 0, origin, 0, false
	}
	copy(nodeID[:], p[6:14])
	seq = binary.BigEndian.Uint32(p[14:18])
	typ = p[5]
	if typ == diagTypeAck && len(p) >= 38 {
		copy(origin[:], p[26:34])
		originSeq = binary.BigEndian.Uint32(p[34:38])
	}
	return typ, nodeID, seq, origin, originSeq, true
}

// txLoop transmits one uniquely-identified TEST packet per interval with a
// little jitter so fleet nodes do not synchronise their transmissions.
func (r *radio) txLoop(deadline time.Time, interval time.Duration, wg *sync.WaitGroup) {
	defer wg.Done()
	rng := mrand.New(mrand.NewSource(time.Now().UnixNano() + int64(r.nodeID[0])))
	var seq uint32
	first := true
	for time.Now().Before(deadline) {
		if !first {
			jitter := time.Duration(rng.Int63n(int64(800*time.Millisecond))) - 400*time.Millisecond
			sleep := interval + jitter
			if sleep < 500*time.Millisecond {
				sleep = 500 * time.Millisecond
			}
			if time.Now().Add(sleep).After(deadline) {
				break
			}
			time.Sleep(sleep)
		}
		first = false
		seq++
		packet := buildPacket(diagTypeTest, r.nodeID, seq, uint64(time.Now().UnixMilli()), [8]byte{}, 0)
		if err := r.transmit(packet); err != nil {
			r.logf("TX failed: %v", err)
			continue
		}
		r.logMu.Lock()
		r.sentPackets++
		r.txTimes[seq] = time.Now()
		r.logMu.Unlock()
	}
}

// transmit writes one payload as a KISS CMD_DATA frame.
func (r *radio) transmit(payload []byte) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.file == nil {
		return fmt.Errorf("port not open")
	}
	frame := append([]byte{kissFend, kissCmdData}, kissEscape(payload)...)
	frame = append(frame, kissFend)
	_, err := r.file.Write(frame)
	return err
}

// rxLoop reads the serial stream, parses KISS frames, and dispatches fleet
// packets: TEST packets from other nodes are recorded and acknowledged; ACK
// packets for this node's transmissions are recorded as receipts with RTT.
func (r *radio) rxLoop(deadline time.Time, wg *sync.WaitGroup) {
	defer wg.Done()
	parser := r.newParser()
	buf := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, err := r.read(buf)
		if n > 0 {
			parser.feedBytes(buf[:n])
		}
		if err != nil {
			if !isReadTimeout(err) {
				r.logf("read error: %v", err)
				return
			}
			if n == 0 {
				// Brief pause so a silent radio does not spin the loop.
				time.Sleep(20 * time.Millisecond)
			}
		}
		r.stateMu.RLock()
		fatal := r.state.fatal
		r.stateMu.RUnlock()
		if fatal != nil {
			r.logf("fatal radio error during test: %v", fatal)
			return
		}
	}
}

// shouldAck reports whether this node acknowledges a peer TEST packet with
// the given sequence number, given the -ack-every setting (1 = every packet;
// N = only sequences divisible by N, deterministic and fleet-consistent).
func shouldAck(seq uint32) bool {
	return *ackEvery <= 1 || seq%uint32(*ackEvery) == 0
}

// handleFleetPacket processes one delivered CMD_DATA payload. Called with
// stateMu held (the parser holds it across feed). Packets delivered before
// the test window opens are dropped: they are stale traffic left buffered by
// a previous run or another program (in one real fleet run, a previous run's
// final packets — sent 25 minutes earlier — surfaced this way, were counted
// as phantom peers, and got pointlessly acknowledged).
func (r *radio) handleFleetPacket(payload []byte) {
	if !r.windowOpen {
		return
	}
	typ, nodeID, seq, origin, originSeq, ok := parsePacket(payload)
	if !ok {
		return
	}
	switch typ {
	case diagTypeTest:
		if nodeID == r.nodeID {
			return // our own packet echoed back by the radio; ignore
		}
		key := fmt.Sprintf("%x:%v", nodeID, seq)
		alreadyAcked := r.ackedPackets[key]
		r.heardPeers[nodeID] = true
		r.heardPackets[nodeID]++
		// ACK thinning: acknowledge every Nth packet (per-sequence) so large
		// fleets do not saturate the channel with acknowledgements. Only
		// packets actually acknowledged are marked, keeping the "ACKs sent"
		// accounting accurate.
		if !alreadyAcked && shouldAck(seq) {
			r.ackedPackets[key] = true
			ack := buildPacket(diagTypeAck, r.nodeID, 0, 0, nodeID, seq)
			if err := r.transmit(ack); err != nil {
				r.logf("ACK TX failed: %v", err)
			} else {
				r.logf("heard TEST seq %v from %x — acknowledged", seq, nodeID[:2])
			}
		}
	case diagTypeAck:
		if origin != r.nodeID || nodeID == r.nodeID {
			return // an ACK for someone else's packet
		}
		txTime, known := r.txTimes[originSeq]
		r.ackReceipts[nodeID]++
		if known {
			r.rtts[nodeID] = append(r.rtts[nodeID], time.Since(txTime))
		}
		r.logf("our TEST seq %v was RECEIVED by %x", originSeq, nodeID[:2])
	}
}

// pollStats sends the firmware statistics burst (RNodeInterface.py stat burst)
// and lets the parser snapshot the responses.
func (r *radio) pollStats() {
	queries := []byte{
		statCmdRX, statCmdTX, statCmdRSSI, statCmdSNR,
		statCmdCHTM, statCmdPHY, statCmdBAT, statCmdCSMA, statCmdTEMP,
	}
	r.writeMu.Lock()
	if r.file == nil {
		r.writeMu.Unlock()
		return
	}
	for _, q := range queries {
		if _, err := r.file.Write(kissFrame(q, nil)); err != nil {
			r.writeMu.Unlock()
			r.logf("stat query 0x%02x failed: %v", q, err)
			return
		}
	}
	r.writeMu.Unlock()

	parser := r.newParser()
	deadline := time.Now().Add(500 * time.Millisecond)
	buf := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, err := r.read(buf)
		if n > 0 {
			parser.feedBytes(buf[:n])
		}
		if err != nil && !isReadTimeout(err) {
			return
		}
	}
}

func (r *radio) logProgress() {
	r.logMu.Lock()
	sent, heardPackets, peers, ackTotal := r.sentPackets, sumValues(r.heardPackets), len(r.heardPeers), 0
	for _, v := range r.ackReceipts {
		ackTotal += v
	}
	r.logMu.Unlock()
	r.stateMu.RLock()
	txC, rxC := r.state.statTXTotal(), r.state.statRXTotal()
	r.stateMu.RUnlock()
	log.Printf("%v nodeID %x: sent %v, heard %v packet(s) from %v peer(s), ACK receipts %v, firmware TX counter %v, RX counter %v",
		r.port, r.nodeID[:2], sent, heardPackets, peers, ackTotal, txC, rxC)
}

func sumValues(m map[[8]byte]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (r *radio) logf(format string, args ...any) {
	log.Printf("%v: %v", r.port, fmt.Sprintf(format, args...))
}

// ---------------------------------------------------------------------------
// Final report

func (r *radio) report(durationSec int) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	s := &r.state

	fmt.Printf("=================================================================\n")
	fmt.Printf(" RNode Diagnostics Report — %v (nodeID %x)\n", r.port, r.nodeID)
	fmt.Printf("=================================================================\n")
	fmt.Printf("Radio:          %v\n", s.describe())
	fmt.Printf("Configured:     %v Hz, BW %v Hz, SF%v, CR%v, TX %v dBm\n", *frequency, *bandwidth, *sf, *cr, *txpower)
	if mismatches := s.validate(*frequency, *bandwidth, *txpower, *sf, *cr); len(mismatches) > 0 {
		fmt.Printf("State check:    MISMATCH — %v\n", strings.Join(mismatches, "; "))
	} else {
		fmt.Printf("State check:    reported values match configuration\n")
	}
	fmt.Printf("Bitrate:        %v bps (on-air, derived from reported radio params)\n", s.bitrate)

	fmt.Printf("\nFirmware counters (hardware-level):\n")
	fmt.Printf("  Packets RX:   %v\n", s.statRXTotal())
	fmt.Printf("  Packets TX:   %v\n", s.statTXTotal())
	if r.txCounterStart != nil {
		fmt.Printf("  TX during test: %v packet(s) accepted by the radio\n", int(s.statTXTotal())-int(*r.txCounterStart))
	}

	r.logMu.Lock()
	defer r.logMu.Unlock()
	fmt.Printf("\nFleet test results (%vs window", durationSec)
	if *ackEvery > 1 {
		fmt.Printf(", ack-every %v", *ackEvery)
	}
	fmt.Printf("):\n")
	fmt.Printf("  TEST packets sent:     %v\n", r.sentPackets)
	fmt.Printf("  TEST packets heard:    %v (from %v peer(s))\n", sumValues(r.heardPackets), len(r.heardPeers))
	fmt.Printf("  ACKs sent:             %v\n", len(r.ackedPackets))
	ackTotal := 0
	for _, v := range r.ackReceipts {
		ackTotal += v
	}
	fmt.Printf("  ACK receipts received: %v (of %v sent packets)\n", ackTotal, r.sentPackets)

	if len(r.heardPeers) > 0 {
		fmt.Printf("\n  Packets heard per peer:\n")
		var peers [][8]byte
		for id := range r.heardPackets {
			peers = append(peers, id)
		}
		sort.Slice(peers, func(i, j int) bool { return string(peers[i][:]) < string(peers[j][:]) })
		for _, id := range peers {
			line := fmt.Sprintf("    %x: %v TEST packet(s)", id, r.heardPackets[id])
			if acks := r.ackReceipts[id]; acks > 0 {
				rtts := r.rtts[id]
				avg, min, max := rtts[0], rtts[0], rtts[0]
				for _, d := range rtts {
					if d < min {
						min = d
					}
					if d > max {
						max = d
					}
					avg += d
				}
				line += fmt.Sprintf(", %v ACK(s) of ours, RTT avg %v (min %v / max %v)", acks, avg/time.Duration(len(rtts)), min, max)
			}
			fmt.Println(line)
		}
	}

	if len(s.statSnapshots) > 0 {
		fmt.Printf("\nRadio statistics snapshots (15s polls):\n")
		for i, line := range s.statSnapshots {
			fmt.Printf("  [%v] %v\n", (i+1)*15, line)
		}
	}

	if len(s.radioMsgs) > 0 {
		fmt.Printf("\nRadio message strings / unrecognized frames:\n")
		for _, m := range s.radioMsgs {
			fmt.Printf("  %v\n", m)
		}
	}

	if len(s.hwErrors) > 0 {
		fmt.Printf("\nHardware errors reported by the radio:\n")
		for _, e := range s.hwErrors {
			fmt.Printf("  %v\n", e)
		}
	}
	if s.fatal != nil {
		fmt.Printf("\nFATAL radio error: %v\n", s.fatal)
	}

	fmt.Printf("\nVerdict: %v\n", r.verdict(ackTotal))
}

// verdict interprets this radio's observed behavior.
func (r *radio) verdict(ackTotal int) string {
	switch {
	case r.sentPackets == 0:
		return "no test packets were transmitted (radio failed early or was never configured)"
	case ackTotal > 0:
		return fmt.Sprintf("TRANSMIT + RECEIVE OK — %v of %v sent packets were confirmed received by peers; this radio is putting out RF", ackTotal, r.sentPackets)
	case len(r.heardPeers) > 0:
		return "RECEIVE ONLY? — this radio heard peer packets but NO peer ever acknowledged any of its transmissions; if no peer's report lists this nodeID, its transmitter is not radiating (dead PA / antenna)"
	case sumValues(r.heardPackets) == 0:
		return "heard NOTHING over the air — receive path problem, wrong frequency/settings, or the fleet was not transmitting during this window"
	default:
		return "insufficient data"
	}
}

func waitGrace(seconds int) {
	if seconds <= 0 {
		return
	}
	log.Printf("waiting %v seconds before the test begins (start gornode-diagnostics on the other fleet nodes now)...", seconds)
	for i := seconds; i > 0; {
		step := 15
		if i < step {
			step = i
		}
		time.Sleep(time.Duration(step) * time.Second)
		i -= step
		if i > 0 {
			log.Printf("grace period: %v seconds remain...", i)
		}
	}
	log.Printf("grace period over — starting test")
}

// ---------------------------------------------------------------------------
// KISS frame parser (local copy of the RNode inbound parser semantics)

// radioState accumulates everything the radio reports (rnode-state.go
// semantics, local copy so the tool has no dependency on interfaces internals).
// All field access is guarded by the owning radio's stateMu.
type radioState struct {
	detected bool
	platform *byte
	mcu      *byte
	board    *byte
	fwMaj    int
	fwMin    int

	rFreq, rBW, rTXP, rSF, rCR *int
	rState                     *byte
	rStatRX, rStatTX           *uint32
	rRSSI                      *int
	rSNR                       *float64
	rCurrentRSSI, rNoiseFloor  *int
	rInterference              *int
	rAirtimeS, rAirtimeL       float64
	rChanS, rChanL             float64
	symbolRate                 int
	rTemp                      *int
	rBatState                  *byte
	rBatPct                    *int

	hwErrors   []string
	fatal      error
	configured bool
	bitrate    int

	statSnapshots []string
	radioMsgs     []string // firmware message strings / unrecognized frames
}

func (s *radioState) describe() string {
	if !s.detected {
		return "not detected"
	}
	parts := []string{"RNode"}
	if s.platform != nil {
		parts = append(parts, "platform "+platformName(*s.platform))
	}
	if s.mcu != nil {
		parts = append(parts, fmt.Sprintf("mcu 0x%02x", *s.mcu))
	}
	if s.board != nil {
		parts = append(parts, fmt.Sprintf("board 0x%02x", *s.board))
	}
	if s.fwMaj > 0 || s.fwMin > 0 {
		parts = append(parts, fmt.Sprintf("firmware %v.%v", s.fwMaj, s.fwMin))
	}
	return strings.Join(parts, ", ")
}

func platformName(p byte) string {
	switch p {
	case 0x80:
		return "ESP32 (0x80)"
	case 0x70:
		return "NRF52 (0x70)"
	case 0x90:
		return "AVR (0x90)"
	default:
		return fmt.Sprintf("0x%02x", p)
	}
}

func (s *radioState) allConfigReported() bool {
	return s.rFreq != nil && s.rBW != nil && s.rTXP != nil && s.rSF != nil && s.rCR != nil && s.rState != nil
}

// validate compares reported radio parameters against the configuration
// (rnodeRadioState.validateRadioState semantics: frequency ±100 Hz, the rest
// exact; unreported values are skipped).
func (s *radioState) validate(freq, bw, txp, sf, cr int) []string {
	var out []string
	if s.rFreq != nil && abs(*s.rFreq-freq) > 100 {
		out = append(out, fmt.Sprintf("frequency %v Hz (want %v)", *s.rFreq, freq))
	}
	if s.rBW != nil && *s.rBW != bw {
		out = append(out, fmt.Sprintf("bandwidth %v (want %v)", *s.rBW, bw))
	}
	if s.rTXP != nil && *s.rTXP != txp {
		out = append(out, fmt.Sprintf("txpower %v (want %v)", *s.rTXP, txp))
	}
	if s.rSF != nil && *s.rSF != sf {
		out = append(out, fmt.Sprintf("spreading factor %v (want %v)", *s.rSF, sf))
	}
	if s.rCR != nil && *s.rCR != cr {
		out = append(out, fmt.Sprintf("coding rate %v (want %v)", *s.rCR, cr))
	}
	if s.rState != nil && *s.rState != radioStateOn {
		out = append(out, fmt.Sprintf("radio state 0x%02x (want ON)", *s.rState))
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (s *radioState) statRXTotal() uint32 {
	if s.rStatRX != nil {
		return *s.rStatRX
	}
	return 0
}

func (s *radioState) statTXTotal() uint32 {
	if s.rStatTX != nil {
		return *s.rStatTX
	}
	return 0
}

// statLine renders one statistics snapshot for the report.
func (s *radioState) statLine() string {
	line := fmt.Sprintf("fw RX=%v TX=%v", s.statRXTotal(), s.statTXTotal())
	if s.rRSSI != nil {
		line += fmt.Sprintf(", last-packet RSSI %v dBm", *s.rRSSI)
	}
	if s.rSNR != nil {
		line += fmt.Sprintf(", SNR %.2f dB", *s.rSNR)
	}
	if s.rNoiseFloor != nil {
		line += fmt.Sprintf(", noise floor %v dBm", *s.rNoiseFloor)
	}
	if s.rCurrentRSSI != nil {
		line += fmt.Sprintf(", current RSSI %v dBm", *s.rCurrentRSSI)
	}
	if s.rInterference != nil {
		line += fmt.Sprintf(", interference %v dBm", *s.rInterference)
	}
	if s.rAirtimeS != 0 || s.rAirtimeL != 0 {
		line += fmt.Sprintf(", airtime %.1f%%/%.1f%% (short/long)", s.rAirtimeS, s.rAirtimeL)
	}
	if s.rChanS != 0 || s.rChanL != 0 {
		line += fmt.Sprintf(", channel load %.1f%%/%.1f%%", s.rChanS, s.rChanL)
	}
	if s.symbolRate != 0 {
		line += fmt.Sprintf(", symbol rate %v baud", s.symbolRate)
	}
	if s.rTemp != nil {
		line += fmt.Sprintf(", temp %v C", *s.rTemp)
	}
	if s.rBatPct != nil {
		st := ""
		if s.rBatState != nil {
			st = batteryStateName(*s.rBatState)
		}
		line += fmt.Sprintf(", battery %v%v%%", st, *s.rBatPct)
	}
	return line
}

func batteryStateName(b byte) string {
	switch b {
	case 0x01:
		return "discharging/"
	case 0x02:
		return "charging/"
	case 0x03:
		return "charged/"
	default:
		return ""
	}
}

// kissParser is a byte-by-byte KISS frame parser for the RNode inbound serial
// stream (rnodeDecoder semantics). Delivered CMD_DATA payloads go to the
// owning radio's fleet-packet handler; every other command updates radioState.
// All methods MUST be called with the owning radio's stateMu held (feedBytes
// takes and holds it).
type kissParser struct {
	r      *radio
	cmd    byte
	buf    []byte
	escape bool
}

func (r *radio) newParser() *kissParser {
	return &kissParser{r: r}
}

// feedBytes feeds one inbound byte chunk through the parser.
func (p *kissParser) feedBytes(data []byte) {
	p.r.stateMu.Lock()
	defer p.r.stateMu.Unlock()
	for i := range data {
		p.feed(data[i])
	}
}

// feed processes one inbound byte.
func (p *kissParser) feed(b byte) {
	r := p.r
	s := &r.state
	if p.cmd != 0xFF && b == kissFend && p.cmd == kissCmdData {
		payload := p.buf
		p.buf = nil
		p.cmd = 0xFF
		if len(payload) > 0 {
			r.handleFleetPacket(payload)
		}
		return
	}
	if b == kissFend {
		p.cmd = 0xFF
		p.buf = nil
		p.escape = false
		return
	}
	if p.cmd == 0xFF {
		p.cmd = b
		p.buf = nil
		p.escape = false
		return
	}

	switch p.cmd {
	case kissCmdData:
		if b == kissFesc {
			p.escape = true
			return
		}
		if p.escape {
			p.escape = false
			switch b {
			case kissTfend:
				b = kissFend
			case kissTfesc:
				b = kissFesc
			}
		}
		p.buf = append(p.buf, b)
	case kissCmdDetect:
		s.detected = b == kissCmdDetectResp
	case kissCmdFwVersion:
		p.buf = append(p.buf, b)
		if len(p.buf) == 2 {
			s.fwMaj = int(p.buf[0])
			s.fwMin = int(p.buf[1])
			p.buf = nil
		}
	case kissCmdPlatform:
		bb := b
		s.platform = &bb
	case kissCmdMcu:
		bb := b
		s.mcu = &bb
	case kissCmdBoard:
		bb := b
		s.board = &bb
	case kissCmdFrequency, kissCmdBandwidth:
		p.buf = append(p.buf, b)
		if len(p.buf) == 4 {
			v := int(binary.BigEndian.Uint32(p.buf))
			if p.cmd == kissCmdFrequency {
				s.rFreq = &v
			} else {
				s.rBW = &v
			}
			s.updateBitrate()
			p.buf = nil
		}
	case kissCmdTXPower:
		v := int(b)
		s.rTXP = &v
	case kissCmdSF:
		v := int(b)
		s.rSF = &v
		s.updateBitrate()
	case kissCmdCR:
		v := int(b)
		s.rCR = &v
		s.updateBitrate()
	case kissCmdRadioState:
		bb := b
		s.rState = &bb
	case statCmdRX:
		p.consumeCounter(b, false)
	case statCmdTX:
		p.consumeCounter(b, true)
	case statCmdRSSI:
		v := int(b) - rnodeRSSIOffset
		s.rRSSI = &v
	case statCmdSNR:
		snr := float64(int8(b)) * 0.25
		s.rSNR = &snr
	case statCmdCHTM: // CMD_STAT_CHTM, 11 bytes
		p.buf = append(p.buf, b)
		if len(p.buf) == 11 {
			cb := p.buf
			ats := int(binary.BigEndian.Uint16(cb[0:2]))
			atl := int(binary.BigEndian.Uint16(cb[2:4]))
			cus := int(binary.BigEndian.Uint16(cb[4:6]))
			cul := int(binary.BigEndian.Uint16(cb[6:8]))
			crs := int(cb[8]) - rnodeRSSIOffset
			nfl := int(cb[9]) - rnodeRSSIOffset
			s.rAirtimeS = float64(ats) / 100.0
			s.rAirtimeL = float64(atl) / 100.0
			s.rChanS = float64(cus) / 100.0
			s.rChanL = float64(cul) / 100.0
			s.rCurrentRSSI = &crs
			s.rNoiseFloor = &nfl
			if cb[10] != 0xFF {
				v := int(cb[10]) - rnodeRSSIOffset
				s.rInterference = &v
			}
			p.buf = nil
		}
	case statCmdPHY: // CMD_STAT_PHYPRM, 12 bytes
		p.buf = append(p.buf, b)
		if len(p.buf) == 12 {
			cb := p.buf
			s.symbolRate = int(binary.BigEndian.Uint16(cb[2:4]))
			p.buf = nil
		}
	case statCmdBAT: // CMD_STAT_BAT, 2 bytes
		p.buf = append(p.buf, b)
		if len(p.buf) == 2 {
			st := p.buf[0]
			pct := int(p.buf[1])
			if pct > 100 {
				pct = 100
			}
			s.rBatState = &st
			s.rBatPct = &pct
			p.buf = nil
		}
	case statCmdCSMA: // CMD_STAT_CSMA, 3 bytes — recorded as a radio message
		p.buf = append(p.buf, b)
		if len(p.buf) == 3 {
			s.noteRadioMsg(fmt.Sprintf("CSMA params: cw band 0x%02x, min 0x%02x, max 0x%02x", p.buf[0], p.buf[1], p.buf[2]))
			p.buf = nil
		}
	case statCmdTEMP:
		temp := int(b) - 120
		if temp >= -30 && temp <= 90 {
			s.rTemp = &temp
		}
	case kissCmdError:
		p.handleErrorCode(b)
	case kissCmdReady:
		// CMD_READY: the radio drained its TX queue — nothing to record.
	case kissCmdSTALock, kissCmdLTALock: // 2-byte airtime locks
		p.buf = append(p.buf, b)
		if len(p.buf) == 2 {
			at := float64(binary.BigEndian.Uint16(p.buf)) / 100.0
			s.noteRadioMsg(fmt.Sprintf("radio lock 0x%02x: %.2f%%", p.cmd, at))
			p.buf = nil
		}
	default:
		// Firmware message strings and any unrecognized command frame are
		// recorded (bounded) for the final report.
		p.buf = append(p.buf, b)
		s.noteRadioMsg(fmt.Sprintf("cmd 0x%02x: %q (0x % x)", p.cmd, printable(p.buf), p.buf))
		p.buf = nil
	}
}

// consumeCounter accumulates a 4-byte firmware counter (CMD_STAT_RX /
// CMD_STAT_TX).
func (p *kissParser) consumeCounter(b byte, isTX bool) {
	p.buf = append(p.buf, b)
	if len(p.buf) != 4 {
		return
	}
	v := binary.BigEndian.Uint32(p.buf)
	p.buf = nil
	if isTX {
		p.r.state.rStatTX = &v
		return
	}
	p.r.state.rStatRX = &v
}

func printable(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c < 0x7F {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

func (p *kissParser) handleErrorCode(b byte) {
	s := &p.r.state
	switch b {
	case errorInitRadio:
		s.fatal = fmt.Errorf("radio init failed (ERROR_INITRADIO)")
	case errorTXFailed:
		s.fatal = fmt.Errorf("radio TX FAILED (ERROR_TXFAILED) — the firmware could not transmit a packet")
	default:
		desc := "unknown error"
		switch b {
		case errorMemoryLow:
			desc = "memory exhausted on connected device"
		case errorModemTimeout:
			desc = "modem communication timed out"
		}
		s.noteRadioMsg(fmt.Sprintf("hardware error 0x%02x: %v", b, desc))
	}
}

func (s *radioState) noteRadioMsg(msg string) {
	if len(s.radioMsgs) < maxRadioMsgs {
		s.radioMsgs = append(s.radioMsgs, msg)
	}
}

// updateBitrate computes the on-air bitrate from the reported radio params
// (rnodeRadioState.updateBitrate): sf * ((4/cr) / (2^sf / (bw/1000))) * 1000.
func (s *radioState) updateBitrate() {
	if s.rSF == nil || s.rCR == nil || s.rBW == nil || *s.rSF <= 0 || *s.rCR <= 0 || *s.rBW <= 0 {
		return
	}
	denom := float64(uint64(1) << uint(*s.rSF))
	if denom == 0 {
		return
	}
	s.bitrate = int(float64(*s.rSF) * (4.0 / float64(*s.rCR)) / (denom / (float64(*s.rBW) / 1000.0)) * 1000.0)
}

// ---------------------------------------------------------------------------
// KISS escaping

func kissEscape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case kissFesc:
			out = append(out, kissFesc, kissTfesc)
		case kissFend:
			out = append(out, kissFesc, kissTfend)
		default:
			out = append(out, b)
		}
	}
	return out
}

func kissFrame(cmd byte, data []byte) []byte {
	return append([]byte{kissFend, cmd}, kissEscape(data)...)
}
