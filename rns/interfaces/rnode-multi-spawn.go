// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// RNodeMulti dynamic sub-interface spawn/despawn over the RNode serial command
// protocol (RNS/Interfaces/RNodeMultiInterface.py: configure_device spawn block
// 331-385, readLoop CMD_INTERFACES parsing 833-838, teardown_subinterfaces
// 909-914, class RNodeSubInterface 939-1048). The spawn is driven by the
// device's CMD_INTERFACES response, which reports one radio-chip type per
// virtual port; an enabled subinterface config is spawned only if its vport
// exists among the reported types, and is despawned via the transport remove.
//
// This is the pure protocol + bookkeeping layer: the KISS command constants,
// the interface-type mapping, the detect command, the byte-stream parser that
// populates the interface-type list, and the spawn/despawn driver. It has no
// build tag so it is unit-testable cross-platform with a mock serial command
// stream; the linux-only RNodeMultiInterface wires it into its real serial
// read loop. This mirrors the Phase 20 Weave split (weave-wdcl.go / weave-peer.go).

package interfaces

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// KISS command bytes specific to the multi-interface serial protocol
// (RNodeMultiInterface.py:45-77). The framing constants (KISSFend, KISSCmdDetect,
// KISSCmdSelInt, etc.) live in kiss.go; these are the multi-interface extras.
const (
	KISSCmdFwVersion  = 0x50
	KISSCmdPlatform   = 0x48
	KISSCmdMcu        = 0x49
	KISSCmdInterfaces = 0x71
	KISSDetectReq     = 0x73
	KISSDetectResp    = 0x46

	// RNodeMaxSubinterfaces is the fixed number of virtual-port slots
	// (RNodeMultiInterface.py:146, MAX_SUBINTERFACES). A slot is nil when no
	// subinterface is spawned at that vport.
	RNodeMaxSubinterfaces = 11

	// RNode radio-chip type codes (RNodeMultiInterface.py:103-114). These are
	// the second byte of each CMD_INTERFACES descriptor; the first byte is
	// unused for the type lookup.
	rNodeChipSX127X = 0x00
	rNodeChipSX1276 = 0x01
	rNodeChipSX1278 = 0x02
	rNodeChipSX126X = 0x10
	rNodeChipSX1262 = 0x11
	rNodeChipSX128X = 0x20
	rNodeChipSX1280 = 0x21
)

// RNodeInterfaceTypeStr maps a radio-chip type code (the second byte of a
// CMD_INTERFACES descriptor) to its chip-family name, exactly as Python's
// KISS.interface_type_to_str does (RNodeMultiInterface.py:118-128). Unknown
// codes fall back to "SX127X" (the Python else branch).
func RNodeInterfaceTypeStr(chipType byte) string {
	switch chipType {
	case rNodeChipSX126X, rNodeChipSX1262:
		return "SX126X"
	case rNodeChipSX127X, rNodeChipSX1276, rNodeChipSX1278:
		return "SX127X"
	case rNodeChipSX128X, rNodeChipSX1280:
		return "SX128X"
	default:
		return "SX127X"
	}
}

// RNodeMultiDetectCommand builds the detect command an RNodeMultiInterface sends
// at startup (RNodeMultiInterface.py:387): one KISS frame sequence probing
// detect, firmware version, platform, MCU, and the interface list. The device's
// response is parsed by rnodeMultiSerialParser.
func RNodeMultiDetectCommand() []byte {
	return []byte{
		KISSFend, KISSCmdDetect, KISSDetectReq, KISSFend,
		KISSCmdFwVersion, 0x00, KISSFend,
		KISSCmdPlatform, 0x00, KISSFend,
		KISSCmdMcu, 0x00, KISSFend,
		KISSCmdInterfaces, 0x00, KISSFend,
	}
}

// rnodeMultiSerialParser is the byte-oriented KISS state machine for the
// multi-interface serial protocol, restricted to the commands relevant to the
// dynamic spawn: CMD_DETECT (sets the detected flag) and CMD_INTERFACES
// (accumulates 2-byte descriptors into the interface-type list). It mirrors the
// framing of RNodeMultiInterface.readLoop (lines 558-838) for those branches:
// FEND starts a new frame, the next byte is the command, and CMD_INTERFACES
// accumulates raw descriptor bytes (no FESC un-escape, matching Python, which
// does not un-escape the CMD_INTERFACES branch) two at a time.
type rnodeMultiSerialParser struct {
	inFrame        bool
	command        byte
	commandBuffer  []byte
	detected       bool
	interfaceTypes []string
	selectedIndex  int
}

// feedByte processes one byte from the serial stream, updating the parser state.
func (p *rnodeMultiSerialParser) feedByte(b byte) {
	if b == KISSFend {
		// FEND starts a new frame (resets command + buffers). Python only ends
		// a frame on FEND for CMD_DATA; for the detect-phase commands every
		// FEND begins a fresh frame, which is the spawn-relevant behavior.
		p.inFrame = true
		p.command = KISSCmdUnknown
		p.commandBuffer = p.commandBuffer[:0]
		return
	}
	if !p.inFrame {
		return
	}
	if p.command == KISSCmdUnknown {
		p.command = b
		return
	}
	switch p.command {
	case KISSCmdInterfaces:
		// Each 2-byte descriptor's second byte is the chip type
		// (RNodeMultiInterface.py:833-838). Raw accumulation, no un-escape.
		p.commandBuffer = append(p.commandBuffer, b)
		if len(p.commandBuffer) == 2 {
			p.interfaceTypes = append(p.interfaceTypes, RNodeInterfaceTypeStr(p.commandBuffer[1]))
			p.commandBuffer = p.commandBuffer[:0]
		}
	case KISSCmdDetect:
		// DETECT_RESP sets detected=true; any other byte clears it
		// (RNodeMultiInterface.py:828-831).
		p.detected = b == KISSDetectResp
	case KISSCmdSelInt:
		p.selectedIndex = int(b)
	}
}

// InterfaceTypes returns the interface types reported by the device so far,
// one per virtual port in order (vport 0 first).
func (p *rnodeMultiSerialParser) InterfaceTypes() []string {
	out := make([]string, len(p.interfaceTypes))
	copy(out, p.interfaceTypes)
	return out
}

// Detected reports whether the device answered the detect probe.
func (p *rnodeMultiSerialParser) Detected() bool { return p.detected }

// KISSCmdUnknown is the sentinel command value before the first in-frame byte
// (RNodeMultiInterface.py:46, KISS.CMD_UNKNOWN = 0xFE).
const KISSCmdUnknown = 0xFE

// RNodeMultiSubSpawn is the logical spawn record for one RNode sub-interface
// (RNodeMultiInterface.py:939, class RNodeSubInterface). It holds the vport
// index, the chip family the device reported for that port, the configured RF
// parameters, and the OUT flag copied from the config (Python
// interface.OUT = subint[10], line 374). The linux transport wraps a real RNode
// radio; the spawn/despawn bookkeeping — the parity-relevant behavior — is
// testable here via this platform-independent record.
type RNodeMultiSubSpawn struct {
	*BaseInterface
	Index           int
	InterfaceType   string
	Frequency       int
	Bandwidth       int
	TXPower         int
	SpreadingFactor int
	CodingRate      int
	FlowControl     bool
	out             bool
	online          atomic.Bool
}

// Type identifies this interface as an RNodeMultiSubSpawn (the Go analog of
// Python's RNodeSubInterface, used for the dynamic spawn surface).
func (s *RNodeMultiSubSpawn) Type() string { return "RNodeSubInterface" }

// Status reports whether the sub-interface is online and its parent
// RNodeMultiInterface is still up (RNodeSubInterface.online is set true after
// the parent configures it).
func (s *RNodeMultiSubSpawn) Status() bool { return s.online.Load() }

// IsOut reports the OUT flag copied from the config at spawn time
// (RNodeMultiInterface.py:374).
func (s *RNodeMultiSubSpawn) IsOut() bool { return s.out }

// Send is a no-op for the logical spawn record: the real radio TX path belongs
// to the linux transport (it routes via the parent serial CMD_SEL_INT). An
// offline subinterface returns an error.
func (s *RNodeMultiSubSpawn) Send(data []byte) error {
	if !s.online.Load() {
		return fmt.Errorf("rnode subinterface %d is offline", s.Index)
	}
	return nil
}

// Detach marks the subinterface offline and detached.
func (s *RNodeMultiSubSpawn) Detach() error {
	s.online.Store(false)
	s.SetDetached(true)
	return nil
}

// RNodeMultiSpawnState holds the dynamic spawn bookkeeping of an
// RNodeMultiInterface (RNodeMultiInterface.py:264 subinterfaces array, 348
// clients counter). subinterfaces is indexed by vport; a nil slot means no
// subinterface is spawned there.
//
// prAggregator is the parent RNodeMultiInterface's aggregated PR-frequency
// tracker (Python RNodeMultiInterface.ip_freq_deque / op_freq_deque,
// RNodeMultiInterface.py:552-555). Each spawned subinterface's parentInterface
// points at it so the subinterface's received_path_request / sent_path_request
// events (Interface.py:267-275) propagate up to the parent's aggregated PR
// deque — Phase 21 task 3. The linux RNodeMultiInterface reads its aggregated
// incoming/outgoing PR frequency from this aggregator.
type RNodeMultiSpawnState struct {
	subinterfaces []*RNodeMultiSubSpawn
	clients       int
	prAggregator  *BaseInterface
}

// NewRNodeMultiSpawnState returns a spawn state with RNodeMaxSubinterfaces slots
// and a fresh PR aggregator (the parent's ip_freq_deque / op_freq_deque,
// empty until spawned subinterfaces propagate events up).
func NewRNodeMultiSpawnState() *RNodeMultiSpawnState {
	return &RNodeMultiSpawnState{
		subinterfaces: make([]*RNodeMultiSubSpawn, RNodeMaxSubinterfaces),
		prAggregator:  NewBaseInterface("RNodeMultiInterface", ModeFull, 0),
	}
}

// PrAggregator returns the parent's aggregated PR-frequency tracker, the
// BaseInterface whose ip_freq_deque / op_freq_deque collect the
// received_path_request / sent_path_request events propagated up by spawned
// subinterfaces. The linux RNodeMultiInterface reads its incoming/outgoing PR
// frequency from this aggregator.
func (s *RNodeMultiSpawnState) PrAggregator() *BaseInterface { return s.prAggregator }

// IncomingPrCount returns the number of incoming path-request samples the
// parent aggregator has recorded from spawned subinterfaces
// (len(ip_freq_deque)). It is the test surface for "the parent's PR counter
// advances" — Phase 21 task 3.
func (s *RNodeMultiSpawnState) IncomingPrCount() int {
	s.prAggregator.ingressMu.Lock()
	defer s.prAggregator.ingressMu.Unlock()
	return len(s.prAggregator.ipFreqDeque)
}

// OutgoingPrCount returns the number of outgoing path-request samples the
// parent aggregator has recorded from spawned subinterfaces
// (len(op_freq_deque)).
func (s *RNodeMultiSpawnState) OutgoingPrCount() int {
	s.prAggregator.ingressMu.Lock()
	defer s.prAggregator.ingressMu.Unlock()
	return len(s.prAggregator.opFreqDeque)
}

// IncomingPrFrequencyAt returns the parent's aggregated incoming path-request
// rate in Hz at now (Python incoming_pr_frequency over the aggregated
// ip_freq_deque, Interface.py:299-308). It is the deterministic core the linux
// RNodeMultiInterface.IncomingPrFrequency delegates to.
func (s *RNodeMultiSpawnState) IncomingPrFrequencyAt(now time.Time) float64 {
	return s.prAggregator.incomingPrFrequencyAt(now)
}

// OutgoingPrFrequencyAt returns the parent's aggregated outgoing path-request
// rate in Hz at now (Python outgoing_pr_frequency over the aggregated
// op_freq_deque, Interface.py:310-319).
func (s *RNodeMultiSpawnState) OutgoingPrFrequencyAt(now time.Time) float64 {
	return s.prAggregator.outgoingPrFrequencyAt(now)
}

// Clients returns the number of spawned subinterfaces
// (RNodeMultiInterface.py:349, self.clients).
func (s *RNodeMultiSpawnState) Clients() int { return s.clients }

// SubinterfaceAt returns the spawned subinterface at vport, or nil if none.
func (s *RNodeMultiSpawnState) SubinterfaceAt(vport int) *RNodeMultiSubSpawn {
	if vport < 0 || vport >= len(s.subinterfaces) {
		return nil
	}
	return s.subinterfaces[vport]
}

// SpawnRNodeSubinterfaces spawns one subinterface per enabled config whose vport
// exists among the device's reported interface types, registering each with the
// transport via addInterface (RNodeMultiInterface.py:351-382). A config whose
// vport is not in interfaceTypes raises a ValueError in Python ("Virtual port
// ... does not exist"); here it is returned as an error and no partial spawns
// from that config are committed. Already-spawned vports are skipped (dedup is
// the caller's / transport's job — see Phase 21 task 2).
//
// parent is the owning RNodeMultiInterface (kept as Interface so this file has
// no build constraint); the subinterface reports to it for Status. cfgs is the
// full configured subinterface list; only Enabled entries are spawned.
func SpawnRNodeSubinterfaces(state *RNodeMultiSpawnState, parent Interface, cfgs []RNodeMultiSubinterfaceConfig, interfaceTypes []string, addInterface func(Interface)) error {
	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}
		vport := cfg.Vport
		if vport < 0 || vport >= len(interfaceTypes) {
			return fmt.Errorf("Virtual port \"%d\" for subinterface %s does not exist", vport, cfg.Name)
		}
		if vport >= len(state.subinterfaces) {
			return fmt.Errorf("Virtual port \"%d\" for subinterface %s does not exist", vport, cfg.Name)
		}
		if state.subinterfaces[vport] != nil {
			// Already spawned at this vport; skip (dedup is the transport's
			// canonical register — Phase 21 task 2).
			continue
		}
		name := cfg.Name
		if name == "" {
			name = fmt.Sprintf("sub%d", vport)
		}
		bi := NewBaseInterface(name, ModeFull, 0)
		// Wire the subinterface's parentInterface to the parent's PR aggregator
		// so its received_path_request / sent_path_request events propagate up
		// to the parent's aggregated PR deque (Python spawned
		// interface.parent_interface = self + Interface.py:267-275; the
		// RNodeMulti parent only records from_spawned=True samples,
		// RNodeMultiInterface.py:552-555). Phase 21 task 3.
		bi.parentInterface = state.prAggregator
		sub := &RNodeMultiSubSpawn{
			BaseInterface:   bi,
			Index:           vport,
			InterfaceType:   interfaceTypes[vport],
			Frequency:       cfg.Frequency,
			Bandwidth:       cfg.Bandwidth,
			TXPower:         cfg.TXPower,
			SpreadingFactor: cfg.SpreadingFactor,
			CodingRate:      cfg.CodingRate,
			FlowControl:     cfg.FlowControl,
			out:             true,
		}
		sub.online.Store(true)
		state.subinterfaces[vport] = sub
		state.clients++
		if addInterface != nil {
			addInterface(sub)
		}
	}
	return nil
}

// DespawnRNodeSubinterfaces removes every spawned subinterface via removeInterface
// and resets its slot (RNodeMultiInterface.py:909-914, teardown_subinterfaces).
// The clients counter is reset to zero.
func DespawnRNodeSubinterfaces(state *RNodeMultiSpawnState, removeInterface func(Interface)) {
	for i, sub := range state.subinterfaces {
		if sub != nil {
			if removeInterface != nil {
				removeInterface(sub)
			}
			state.subinterfaces[i] = nil
		}
	}
	state.clients = 0
}

// RNodeMultiRegistry is the canonical interface registry an RNodeMultiInterface
// routes its dynamic spawn/despawn through. It is the Go analog of Python's
// Transport.interfaces list with the Transport.add_interface /
// Transport.remove_interface dedup (Transport.py:438-451): add_interface appends
// only when the interface is not already present, and remove_interface removes
// only when it is. The registry is platform-independent (no build tag) so the
// canonical register/remove + dedup is unit-testable cross-platform; the
// linux-only RNodeMultiInterface supplies a registry whose Add/Remove it can
// delegate to its TransportSystem's RegisterInterface/RemoveInterface, keeping
// the parity-critical dedup behavior testable here without a live transport.
type RNodeMultiRegistry struct {
	mu         sync.Mutex
	interfaces []Interface
}

// NewRNodeMultiRegistry returns an empty canonical interface registry.
func NewRNodeMultiRegistry() *RNodeMultiRegistry {
	return &RNodeMultiRegistry{interfaces: make([]Interface, 0)}
}

// Add registers iface if it is not already present (Python Transport.add_interface,
// Transport.py:439-441: "if not interface in Transport.interfaces:
// Transport.interfaces.append(interface)"). A re-add of the same interface is a
// no-op, so the registry keeps each interface at most once — the dedup that
// makes a re-spawn of the same vport a no-op at the transport level.
func (r *RNodeMultiRegistry) Add(iface Interface) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.Contains(r.interfaces, iface) {
		return
	}
	r.interfaces = append(r.interfaces, iface)
}

// Remove unregisters iface if it is present (Python Transport.remove_interface,
// Transport.py:445-448: "if interface in Transport.interfaces:
// Transport.interfaces.remove(interface)"). Removing an absent interface is a
// no-op.
func (r *RNodeMultiRegistry) Remove(iface Interface) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := slices.Index(r.interfaces, iface); i >= 0 {
		r.interfaces = append(r.interfaces[:i], r.interfaces[i+1:]...)
	}
}

// Interfaces returns a snapshot of the registered interfaces in insertion
// order. The returned slice is safe to retain and iterate without holding the
// registry lock.
func (r *RNodeMultiRegistry) Interfaces() []Interface {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Interface, len(r.interfaces))
	copy(out, r.interfaces)
	return out
}

// Count returns the number of registered interfaces.
func (r *RNodeMultiRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.interfaces)
}

// SpawnRNodeSubinterfacesRegistered spawns subinterfaces and routes each
// addInterface through the canonical registry's Add (dedup), so the dynamic
// spawn goes through the transport's canonical register with dedup — Phase 21
// task 2. It is the wiring the linux RNodeMultiInterface uses in place of a
// bare addInterface callback; a re-spawn of an already-spawned vport is a no-op
// at both the spawner's slot level (same object retained) and the registry's
// identity-dedup level.
func SpawnRNodeSubinterfacesRegistered(state *RNodeMultiSpawnState, parent Interface, cfgs []RNodeMultiSubinterfaceConfig, interfaceTypes []string, registry *RNodeMultiRegistry) error {
	return SpawnRNodeSubinterfaces(state, parent, cfgs, interfaceTypes, registry.Add)
}

// DespawnRNodeSubinterfacesRegistered despawns all subinterfaces and routes each
// removeInterface through the canonical registry's Remove, clearing the
// registry of every spawned subinterface (Phase 21 task 2).
func DespawnRNodeSubinterfacesRegistered(state *RNodeMultiSpawnState, registry *RNodeMultiRegistry) {
	DespawnRNodeSubinterfaces(state, registry.Remove)
}
