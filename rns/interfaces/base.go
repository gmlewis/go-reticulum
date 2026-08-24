// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	rnscrypto "github.com/gmlewis/go-reticulum/rns/crypto"
)

// IFACConfig describes the cryptographic parameters required to secure a physical
// or virtual interface. It contains an enable flag and entropy material used to
// authenticate payload frames at the hardware boundary.
type IFACConfig struct {
	Enabled bool
	NetName string
	NetKey  string
	Size    int
}

// IFACMaxSize is the largest IFAC field an interface may emit or accept. The
// IFAC is a truncation of an Ed25519 signature, which is always 64 bytes, so an
// IFAC size larger than 64 can never be satisfied and would underflow the
// signature slice (sig[len(sig)-ifacSize:]) and panic. Clamping here and in
// SetIFACConfig prevents a misconfigured ifac_size from crashing the first
// inbound/outbound frame.
const IFACMaxSize = 64

// DiscoveryConfig captures the per-interface metadata and policy used by
// Reticulum's on-network interface discovery mechanism.
type DiscoveryConfig struct {
	SupportsDiscovery bool
	Discoverable      bool
	AnnounceInterval  time.Duration
	StampValue        int
	Name              string
	Encrypt           bool
	ReachableOn       string
	PublishIFAC       bool
	Latitude          *float64
	Longitude         *float64
	Height            *float64
	// LocationCmd, when non-empty, is a path to an executable run at announce
	// time whose stdout is parsed as "lat,lon,hgt" to override the static
	// Latitude/Longitude/Height fields (RNS/Reticulum.py:887,
	// RNS/Discovery.py:103-123). Only evaluated on non-Windows platforms.
	LocationCmd     string
	Frequency       *int
	Bandwidth       *int
	SpreadingFactor *int
	CodingRate      *int
	Channel         *int
	Modulation      string
}

// InboundHandler rigorously defines the callback signature invoked universally across all interface types whenever a valid payload frame is successfully reassembled.
// It acts as the critical bridge, injecting raw network bytes back into the core routing engine for cryptographic validation and dispatch.
type InboundHandler func(data []byte, iface Interface)

// ConnectHandler defines the callback signature invoked when a new connection is established on a server interface.
type ConnectHandler func(iface Interface)

// BaseInterface implements the foundational structure used by all concrete
// interface types. It encapsulates state such as byte metrics, IFAC keying
// material, and lifecycle flags to guarantee consistent behavior across
// interface implementations.
type BaseInterface struct {
	name     string
	mode     int
	bitrate  int
	created  time.Time
	detached int32

	rxBytes uint64
	txBytes uint64
	ifacMu  sync.RWMutex

	// currentRxSpeedBits/currentTxSpeedBits hold the per-interface
	// bit-per-second speeds computed by the transport's count_traffic_loop
	// (Python interface.current_rx_speed / current_tx_speed), stored as
	// math.Float64bits so they can be read/written atomically. They are read
	// by the interface_stats RPC.
	currentRxSpeedBits uint64
	currentTxSpeedBits uint64

	errorPolicyMu sync.RWMutex
	discoveryMu   sync.RWMutex
	autoMu        sync.RWMutex

	// ingressMu guards the ingress/egress-control configuration and the
	// announce/PR burst state machine, frequency deques, and held-announce
	// map. These fields are read by spawned-peer accept loops
	// (copyIngressEgressFrom) and by the transport's announce/PR handling
	// (ShouldIngressLimit, HoldAnnounce, ReceivedAnnounce, …) which run
	// outside the transport mutex (see transport.go runInterfaceJobs /
	// shouldHoldAnnounce comments), and written by RPC/test setters — so a
	// per-interface mutex is required. It is a leaf lock: no code holds it
	// while acquiring another mutex, so there is no lock-ordering hazard
	// with the transport mutex or the AutoInterface/BackboneInterface locks.
	ingressMu sync.RWMutex

	panicOnError       atomic.Bool
	ifacConfig         IFACConfig
	ifacKey            []byte
	ifacSigner         *rnscrypto.Ed25519PrivateKey
	interfacePanicHook func(string)
	discoveryConfig    DiscoveryConfig
	bootstrapOnly      bool
	autoconnectHash    []byte
	autoconnectSrc     string

	// Contract fields (RNS v1.3.7/1.4.1). Defaults mirror
	// Interface.__init__: gravity=0, recursive_prs=false,
	// announces_from_internal=true, announces_to_internal=nil.
	// gravity is accessed atomically because it is set at runtime (e.g. via
	// RPC or SetGravity) and concurrently read by spawned-peer accept loops
	// (TCPInterface.handleConnection, AutoInterface.addPeer).
	gravity               atomic.Int32
	recursivePrs          bool
	announcesFromInternal bool
	announcesToInternal   *bool

	// Ingress/egress-control configuration (RNS/Interfaces/Interface.py
	// :118-136, v1.1.5). Defaults mirror Interface.__init__ via the
	// Reticulum _default_* getters, which resolve to the Interface class
	// constants defined above. Spawned peers inherit these from their
	// parent server interface at accept/spawn time.
	ingressControl        bool
	icMaxHeldAnnounces    int
	icBurstHold           float64
	icBurstFreqNew        float64
	icBurstFreq           float64
	icPrBurstFreqNew      float64
	icPrBurstFreq         float64
	icNewTime             float64
	icBurstPenalty        float64
	icHeldReleaseInterval float64
	ecPrFreq              float64
	egressControl         bool

	// Announce-rate-control config (Interface.py:90-92, 118-120;
	// Reticulum.py:819-857,938-940). A nil pointer mirrors Python's None,
	// meaning "no rate limit configured". When transport is enabled the
	// Reticulum config layer fills nil values from the instance-wide
	// default_ar_target/penalty/grace (which themselves resolve to the
	// DEFAULT_AR_* class constants). Spawned peers inherit these from their
	// parent server interface at spawn time (AutoInterface.py:579-581,
	// BackboneInterface.py:481-483).
	announceRateTarget  *int
	announceRateGrace   *int
	announceRatePenalty *int

	// parentInterface is the server interface that spawned this peer
	// (Python spawned_interface.parent_interface = self). It is set on
	// spawned peers so frequency/byte-counter events propagate up to the
	// aggregating parent (Interface.py:257-275). nil on root interfaces.
	parentInterface *BaseInterface

	// iaFreqDeque / oaFreqDeque are the incoming/outgoing announce
	// frequency sample deques (Python ia_freq_deque / oa_freq_deque,
	// Interface.py:139-140, maxlen IA_FREQ_SAMPLES/OA_FREQ_SAMPLES).
	// Populated by ReceivedAnnounce / SentAnnounce and read by the
	// incoming/outgoing announce-frequency formulas.
	iaFreqDeque []time.Time
	oaFreqDeque []time.Time

	// ipFreqDeque / opFreqDeque are the incoming/outgoing path-request
	// frequency sample deques (Python ip_freq_deque / op_freq_deque,
	// Interface.py:139-140, maxlen IP_FREQ_SAMPLES/OP_FREQ_SAMPLES).
	// Populated by ReceivedPathRequest / SentPathRequest and read by the
	// incoming/outgoing PR-frequency formulas and the PR burst state machine.
	ipFreqDeque []time.Time
	opFreqDeque []time.Time

	// Ingress-limit burst + held-announce state (Interface.py:121-137, 224-255).
	// icBurstActive is the announce-burst flag set by ShouldIngressLimit;
	// icBurstActivated is when it activated; icHeldRelease is the earliest
	// time the next held announce may be released (now + icBurstPenalty on
	// activation, then now + icHeldReleaseInterval after each release).
	// heldAnnounces maps destination hash -> held announce awaiting release.
	icBurstActive    bool
	icBurstActivated time.Time
	icHeldRelease    time.Time
	heldAnnounces    map[string]heldAnnounce

	// PR-burst state (Interface.py:121-122, 174-190). icPrBurstActive is the
	// path-request ingress-burst flag set by ShouldIngressLimitPr;
	// icPrBurstActivated is when it activated. Unlike the announce burst, the
	// PR burst has no held-release penalty — it only gates recursive path
	// request forwarding.
	icPrBurstActive    bool
	icPrBurstActivated time.Time

	// defaultIFACSize is this interface type's DEFAULT_IFAC_SIZE class
	// attribute (RNS/Interfaces/*.py). Set by each concrete constructor, it
	// drives SetIFACConfig's size default and the discovery autoconnect path
	// so the IFAC size matches the interface type instead of a hardcoded 16.
	defaultIFACSize int

	// hash is the memoized identity hash (Python Interface.get_hash,
	// RNS/Interfaces/Interface.py:144-146): the SHA-256 of the interface's
	// Python __str__ (Interface.__str__ and its overrides). For most types
	// that is "{Type}[{Name}]", but TCP/UDP/Local/Backbone append "/ip:port";
	// see interfaceHashString in transport.go. hashOnce guarantees the
	// computation runs at most once per instance. The computation itself is
	// supplied by the caller (see MemoizedHash) so the concrete HashString
	// (or Type/Name default) virtual dispatch is used, matching Python's
	// full_hash(str(self)) where str dispatches to the subclass __str__.
	hash     []byte
	hashOnce sync.Once
}

// NewBaseInterface allocates and initializes a BaseInterface with the given
// name, mode, and bitrate. It records a creation timestamp and provides the
// baseline state required by specialized interfaces.
func NewBaseInterface(name string, mode int, bitrate int) *BaseInterface {
	return &BaseInterface{
		name:                  name,
		mode:                  mode,
		bitrate:               bitrate,
		created:               time.Now(),
		announcesFromInternal: true,

		// Ingress/egress-control defaults (Interface.py:118-136).
		ingressControl:        true,
		icMaxHeldAnnounces:    MaxHeldAnnounces,
		icBurstHold:           ICBurstHold,
		icBurstFreqNew:        ICBurstFreqNew,
		icBurstFreq:           ICBurstFreq,
		icPrBurstFreqNew:      ICPrBurstFreqNew,
		icPrBurstFreq:         ICPrBurstFreq,
		icNewTime:             ICNewTime,
		icBurstPenalty:        ICBurstPenalty,
		icHeldReleaseInterval: ICHeldReleaseInterval,
		ecPrFreq:              ECPrFreq,
		egressControl:         EgressControlDefault,
	}
}

// Name returns the immutably configured identifier assigned to this interface.
// It allows higher-level orchestrators to distinguish between different routing
// components.
func (bi *BaseInterface) Name() string { return bi.name }

// Mode returns the operational simplex/duplex mode flag for this interface.
// It indicates whether the interface can participate in bidirectional or
// unidirectional routing topologies.
func (bi *BaseInterface) Mode() int { return bi.mode }

// SetMode updates the interface's operational routing mode.
func (bi *BaseInterface) SetMode(mode int) { bi.mode = mode }

// Bitrate returns the estimated transmission capacity of the interface in bits
// per second. The routing engine uses this metric to calculate transit costs and
// shape traffic queues.
func (bi *BaseInterface) Bitrate() int { return bi.bitrate }

// SetBitrate atomically updates the interface's operational bitrate to reflect
// changing hardware constraints. Updating this value influences routing cost
// calculations downstream.
func (bi *BaseInterface) SetBitrate(bitrate int) { bi.bitrate = bitrate }

// Gravity returns the interface gravity used for weighted path selection
// (RNS v1.4.1). Higher gravity interfaces win same-timebase path ties. The
// default is 0 (Interface.DEFAULT_GRAVITY).
func (bi *BaseInterface) Gravity() int { return int(bi.gravity.Load()) }

// SetGravity sets the interface gravity used for weighted path selection.
func (bi *BaseInterface) SetGravity(g int) { bi.gravity.Store(int32(g)) }

// RecursivePrs reports whether recursive path requests egress on this
// interface regardless of its mode (RNS v1.3.7). Defaults to false.
func (bi *BaseInterface) RecursivePrs() bool { return bi.recursivePrs }

// SetRecursivePrs sets the recursive-path-request policy.
func (bi *BaseInterface) SetRecursivePrs(v bool) { bi.recursivePrs = v }

// Ingress/egress-control accessors (RNS/Interfaces/Interface.py:118-136).
// Each defaults to the Interface class constant of the same name; spawned
// peers inherit the parent's configured values via copyIngressEgressFrom.

func (bi *BaseInterface) IngressControl() bool {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.ingressControl
}
func (bi *BaseInterface) SetIngressControl(v bool) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.ingressControl = v
}
func (bi *BaseInterface) ICMaxHeldAnnounces() int {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icMaxHeldAnnounces
}
func (bi *BaseInterface) SetICMaxHeldAnnounces(v int) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icMaxHeldAnnounces = v
}
func (bi *BaseInterface) ICBurstHold() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icBurstHold
}
func (bi *BaseInterface) SetICBurstHold(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icBurstHold = v
}
func (bi *BaseInterface) ICBurstFreqNew() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icBurstFreqNew
}
func (bi *BaseInterface) SetICBurstFreqNew(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icBurstFreqNew = v
}
func (bi *BaseInterface) ICBurstFreq() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icBurstFreq
}
func (bi *BaseInterface) SetICBurstFreq(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icBurstFreq = v
}
func (bi *BaseInterface) ICPrBurstFreqNew() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icPrBurstFreqNew
}
func (bi *BaseInterface) SetICPrBurstFreqNew(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icPrBurstFreqNew = v
}
func (bi *BaseInterface) ICPrBurstFreq() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icPrBurstFreq
}
func (bi *BaseInterface) SetICPrBurstFreq(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icPrBurstFreq = v
}
func (bi *BaseInterface) ICNewTime() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icNewTime
}
func (bi *BaseInterface) SetICNewTime(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icNewTime = v
}
func (bi *BaseInterface) ICBurstPenalty() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icBurstPenalty
}
func (bi *BaseInterface) SetICBurstPenalty(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icBurstPenalty = v
}
func (bi *BaseInterface) ICHeldReleaseInterval() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icHeldReleaseInterval
}
func (bi *BaseInterface) SetICHeldReleaseInterval(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.icHeldReleaseInterval = v
}
func (bi *BaseInterface) ECPrFreq() float64 {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.ecPrFreq
}
func (bi *BaseInterface) SetECPrFreq(v float64) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.ecPrFreq = v
}
func (bi *BaseInterface) EgressControl() bool {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.egressControl
}
func (bi *BaseInterface) SetEgressControl(v bool) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.egressControl = v
}

// Ingress-control burst-state accessors (Interface.py:121-124). These expose
// the per-interface announce/PR burst flags and activation timestamps used by
// ifstats (Reticulum.py:1461-1464) and the Backbone aggregate getters. The
// activated timestamps are zero until the corresponding burst activates.
func (bi *BaseInterface) ICBurstActive() bool {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icBurstActive
}
func (bi *BaseInterface) ICBurstActivated() time.Time {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icBurstActivated
}
func (bi *BaseInterface) ICPrBurstActive() bool {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icPrBurstActive
}
func (bi *BaseInterface) ICPrBurstActivated() time.Time {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.icPrBurstActivated
}

// Announce-rate-control accessors (Interface.py:90-92,118-120;
// Reticulum.py:819-857,938-940). A nil pointer mirrors Python's None (no rate
// limit). Defaults are applied by the Reticulum config layer, not here.
func (bi *BaseInterface) AnnounceRateTarget() *int {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.announceRateTarget
}
func (bi *BaseInterface) SetAnnounceRateTarget(v *int) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.announceRateTarget = v
}
func (bi *BaseInterface) AnnounceRateGrace() *int {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.announceRateGrace
}
func (bi *BaseInterface) SetAnnounceRateGrace(v *int) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.announceRateGrace = v
}
func (bi *BaseInterface) AnnounceRatePenalty() *int {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return bi.announceRatePenalty
}
func (bi *BaseInterface) SetAnnounceRatePenalty(v *int) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.announceRatePenalty = v
}

// copyIngressEgressFrom copies the full ingress/egress-control
// configuration from parent into bi, mirroring the per-field spawn blocks
// in TCPInterface.py:595-608, AutoInterface.py:542-554,
// I2PInterface.py:828-840, and BackboneInterface.py:446-458 (v1.1.5).
// It is called from each server interface's accept/spawn path so spawned
// peers inherit their parent's ingress/egress policy.
func (bi *BaseInterface) copyIngressEgressFrom(parent *BaseInterface) {
	if bi == nil || parent == nil {
		return
	}
	// Snapshot the parent's config under its lock, then write the copy into
	// bi under bi's lock. The two critical sections are separate (no nested
	// locking) so there is no lock-ordering hazard, and each side is
	// synchronized with concurrent setters on the respective interface.
	parent.ingressMu.RLock()
	ingressControl := parent.ingressControl
	icMaxHeldAnnounces := parent.icMaxHeldAnnounces
	icBurstHold := parent.icBurstHold
	icBurstFreqNew := parent.icBurstFreqNew
	icBurstFreq := parent.icBurstFreq
	icNewTime := parent.icNewTime
	icBurstPenalty := parent.icBurstPenalty
	icHeldReleaseInterval := parent.icHeldReleaseInterval
	egressControl := parent.egressControl
	ecPrFreq := parent.ecPrFreq
	icPrBurstFreqNew := parent.icPrBurstFreqNew
	icPrBurstFreq := parent.icPrBurstFreq
	announceRateTarget := parent.announceRateTarget
	announceRateGrace := parent.announceRateGrace
	announceRatePenalty := parent.announceRatePenalty
	parent.ingressMu.RUnlock()

	bi.ingressMu.Lock()
	bi.ingressControl = ingressControl
	bi.icMaxHeldAnnounces = icMaxHeldAnnounces
	bi.icBurstHold = icBurstHold
	bi.icBurstFreqNew = icBurstFreqNew
	bi.icBurstFreq = icBurstFreq
	bi.icNewTime = icNewTime
	bi.icBurstPenalty = icBurstPenalty
	bi.icHeldReleaseInterval = icHeldReleaseInterval
	bi.egressControl = egressControl
	bi.ecPrFreq = ecPrFreq
	bi.icPrBurstFreqNew = icPrBurstFreqNew
	bi.icPrBurstFreq = icPrBurstFreq

	// Announce-rate-control inheritance (AutoInterface.py:579-581,
	// BackboneInterface.py:481-483, TCPInterface.py spawn block).
	bi.announceRateTarget = announceRateTarget
	bi.announceRateGrace = announceRateGrace
	bi.announceRatePenalty = announceRatePenalty
	bi.ingressMu.Unlock()
}

// appendFreqSample appends now to a maxlen-capped frequency deque, dropping
// the oldest entry when it exceeds max (Python collections.deque(maxlen=N)).
func appendFreqSample(deque []time.Time, now time.Time, max int) []time.Time {
	deque = append(deque, now)
	if len(deque) > max {
		deque = deque[len(deque)-max:]
	}
	return deque
}

// ReceivedAnnounce records an incoming announce on this interface at the
// current instant and propagates it to the parent server interface
// (Interface.py:257-260).
func (bi *BaseInterface) ReceivedAnnounce() {
	bi.receivedAnnounceAt(time.Now(), false)
}

// receivedAnnounceAt is the lock-free core of ReceivedAnnounce taking an
// explicit now for deterministic frequency tests. When fromSpawned is false
// the sample also propagates to the parent interface (Python passes
// from_spawned=True to the parent so it does not re-propagate).
func (bi *BaseInterface) receivedAnnounceAt(now time.Time, fromSpawned bool) {
	bi.ingressMu.Lock()
	bi.iaFreqDeque = appendFreqSample(bi.iaFreqDeque, now, IAFreqSamples)
	bi.ingressMu.Unlock()
	if !fromSpawned && bi.parentInterface != nil {
		bi.parentInterface.receivedAnnounceAt(now, true)
	}
}

// SentAnnounce records an outgoing announce on this interface at the current
// instant and propagates it to the parent server interface
// (Interface.py:262-265).
func (bi *BaseInterface) SentAnnounce() {
	bi.sentAnnounceAt(time.Now(), false)
}

// sentAnnounceAt is the lock-free core of SentAnnounce taking an explicit
// now for deterministic frequency tests.
func (bi *BaseInterface) sentAnnounceAt(now time.Time, fromSpawned bool) {
	bi.ingressMu.Lock()
	bi.oaFreqDeque = appendFreqSample(bi.oaFreqDeque, now, OAFreqSamples)
	bi.ingressMu.Unlock()
	if !fromSpawned && bi.parentInterface != nil {
		bi.parentInterface.sentAnnounceAt(now, true)
	}
}

// IncomingAnnounceFrequency returns the current incoming-announce rate in Hz
// (Interface.py:277-286). It is the public time.Now()-driven entry point.
func (bi *BaseInterface) IncomingAnnounceFrequency() float64 {
	return bi.incomingAnnounceFrequencyAt(time.Now())
}

// incomingAnnounceFrequencyAt is the deterministic core of
// IncomingAnnounceFrequency. It mirrors Interface.py:277-286 exactly:
//
//	n = len(deque); if not n > IC_DEQUE_MIN_SAMPLE(2): return 0
//	oldest = deque[0]; span = now - oldest
//	if span > AR_FREQ_DECAY(10): popleft
//	if span <= 0: return 0
//	hz = n / span
//
// The returned hz uses the pre-pop n and the pre-pop span; the popleft is a
// side effect that ages the deque for the next call.
func (bi *BaseInterface) incomingAnnounceFrequencyAt(now time.Time) float64 {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	return bi.incomingAnnounceFrequencyAtLocked(now)
}

// incomingAnnounceFrequencyAtLocked is the ingressMu-held core of
// incomingAnnounceFrequencyAt. The caller must hold bi.ingressMu.
func (bi *BaseInterface) incomingAnnounceFrequencyAtLocked(now time.Time) float64 {
	n := len(bi.iaFreqDeque)
	if !(n > ICDequeMinSample) {
		return 0
	}
	oldest := bi.iaFreqDeque[0]
	span := now.Sub(oldest).Seconds()
	if span > ARFreqDecay {
		bi.iaFreqDeque = bi.iaFreqDeque[1:]
	}
	if span <= 0 {
		return 0
	}
	return float64(n) / span
}

// OutgoingAnnounceFrequency returns the current outgoing-announce rate in Hz
// (Interface.py:288-297). It is the public time.Now()-driven entry point.
func (bi *BaseInterface) OutgoingAnnounceFrequency() float64 {
	return bi.outgoingAnnounceFrequencyAt(time.Now())
}

// outgoingAnnounceFrequencyAt is the deterministic core of
// OutgoingAnnounceFrequency. It mirrors Interface.py:288-297: the only
// difference from the incoming formula is the minimum gate, which is
// `len > 1` (needs 2+ samples) rather than IC_DEQUE_MIN_SAMPLE (3+).
func (bi *BaseInterface) outgoingAnnounceFrequencyAt(now time.Time) float64 {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	return bi.outgoingAnnounceFrequencyAtLocked(now)
}

// outgoingAnnounceFrequencyAtLocked is the ingressMu-held core of
// outgoingAnnounceFrequencyAt. The caller must hold bi.ingressMu.
func (bi *BaseInterface) outgoingAnnounceFrequencyAtLocked(now time.Time) float64 {
	n := len(bi.oaFreqDeque)
	if !(n > 1) {
		return 0
	}
	oldest := bi.oaFreqDeque[0]
	span := now.Sub(oldest).Seconds()
	if span > ARFreqDecay {
		bi.oaFreqDeque = bi.oaFreqDeque[1:]
	}
	if span <= 0 {
		return 0
	}
	return float64(n) / span
}

// ageAt returns the interface's age in seconds at the given instant, mirroring
// Python Interface.age() = time.time() - self.created (Interface.py:221-222).
func (bi *BaseInterface) ageAt(now time.Time) float64 {
	return now.Sub(bi.created).Seconds()
}

// ReceivedPathRequest records an incoming path request on this interface at the
// current instant and propagates it to the parent server interface
// (Interface.py:267-270).
func (bi *BaseInterface) ReceivedPathRequest() {
	bi.receivedPathRequestAt(time.Now(), false)
}

// receivedPathRequestAt is the lock-free core of ReceivedPathRequest taking an
// explicit now for deterministic frequency tests. When fromSpawned is false the
// sample also propagates to the parent interface (Python passes from_spawned=True
// to the parent so it does not re-propagate).
func (bi *BaseInterface) receivedPathRequestAt(now time.Time, fromSpawned bool) {
	bi.ingressMu.Lock()
	bi.ipFreqDeque = appendFreqSample(bi.ipFreqDeque, now, IPFreqSamples)
	bi.ingressMu.Unlock()
	if !fromSpawned && bi.parentInterface != nil {
		bi.parentInterface.receivedPathRequestAt(now, true)
	}
}

// SentPathRequest records an outgoing path request on this interface at the
// current instant and propagates it to the parent server interface
// (Interface.py:272-275).
func (bi *BaseInterface) SentPathRequest() {
	bi.sentPathRequestAt(time.Now(), false)
}

// sentPathRequestAt is the lock-free core of SentPathRequest taking an explicit
// now for deterministic frequency tests.
func (bi *BaseInterface) sentPathRequestAt(now time.Time, fromSpawned bool) {
	bi.ingressMu.Lock()
	bi.opFreqDeque = appendFreqSample(bi.opFreqDeque, now, OPFreqSamples)
	bi.ingressMu.Unlock()
	if !fromSpawned && bi.parentInterface != nil {
		bi.parentInterface.sentPathRequestAt(now, true)
	}
}

// IncomingPrFrequency returns the current incoming path-request rate in Hz
// (Interface.py:299-308). It is the public time.Now()-driven entry point.
func (bi *BaseInterface) IncomingPrFrequency() float64 {
	return bi.incomingPrFrequencyAt(time.Now())
}

// incomingPrFrequencyAt is the deterministic core of IncomingPrFrequency. It
// mirrors Interface.py:299-308 exactly: same shape as the incoming-announce
// formula but with the PR_FREQ_DECAY (1/PR_MINFREQ_HZ = 10s) decay window and
// the ip_freq_deque.
func (bi *BaseInterface) incomingPrFrequencyAt(now time.Time) float64 {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	return bi.incomingPrFrequencyAtLocked(now)
}

// incomingPrFrequencyAtLocked is the ingressMu-held core of
// incomingPrFrequencyAt. The caller must hold bi.ingressMu.
func (bi *BaseInterface) incomingPrFrequencyAtLocked(now time.Time) float64 {
	n := len(bi.ipFreqDeque)
	if !(n > ICDequeMinSample) {
		return 0
	}
	oldest := bi.ipFreqDeque[0]
	span := now.Sub(oldest).Seconds()
	if span > PRFreqDecay {
		bi.ipFreqDeque = bi.ipFreqDeque[1:]
	}
	if span <= 0 {
		return 0
	}
	return float64(n) / span
}

// OutgoingPrFrequency returns the current outgoing path-request rate in Hz
// (Interface.py:310-319). It is the public time.Now()-driven entry point.
func (bi *BaseInterface) OutgoingPrFrequency() float64 {
	return bi.outgoingPrFrequencyAt(time.Now())
}

// outgoingPrFrequencyAt is the deterministic core of OutgoingPrFrequency. It
// mirrors Interface.py:310-319: same shape as the outgoing-announce formula
// (len > 1 gate, PR_FREQ_DECAY decay window) but with the op_freq_deque.
func (bi *BaseInterface) outgoingPrFrequencyAt(now time.Time) float64 {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	return bi.outgoingPrFrequencyAtLocked(now)
}

// outgoingPrFrequencyAtLocked is the ingressMu-held core of
// outgoingPrFrequencyAt. The caller must hold bi.ingressMu.
func (bi *BaseInterface) outgoingPrFrequencyAtLocked(now time.Time) float64 {
	n := len(bi.opFreqDeque)
	if !(n > 1) {
		return 0
	}
	oldest := bi.opFreqDeque[0]
	span := now.Sub(oldest).Seconds()
	if span > PRFreqDecay {
		bi.opFreqDeque = bi.opFreqDeque[1:]
	}
	if span <= 0 {
		return 0
	}
	return float64(n) / span
}

// ShouldIngressLimit reports whether an inbound announce on this interface
// should be held rather than processed, activating and deactivating the
// announce-burst state machine as a side effect. It is the public
// time.Now()-driven entry point for Interface.should_ingress_limit
// (Interface.py:152-172).
func (bi *BaseInterface) ShouldIngressLimit() bool {
	return bi.shouldIngressLimitAt(time.Now())
}

// shouldIngressLimitAt is the deterministic core of ShouldIngressLimit. It
// mirrors Interface.py:152-172 exactly:
//
//	if not ingress_control: return False
//	freq_threshold = ic_burst_freq_new if age < ic_new_time else ic_burst_freq
//	ia_freq = incoming_announce_frequency()
//	if ic_burst_active:
//	    if ia_freq < freq_threshold and now > activated+ic_burst_hold:
//	        if len(ia_freq_deque) >= IC_DEQUE_MIN_SAMPLE(2): ic_burst_active = False
//	    return True
//	else:
//	    if ia_freq > freq_threshold:
//	        ic_burst_active = True; ic_burst_activated = now
//	        ic_held_release = now + ic_burst_penalty
//	        return True
//	    else: return False
func (bi *BaseInterface) shouldIngressLimitAt(now time.Time) bool {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	if !bi.ingressControl {
		return false
	}
	freqThreshold := bi.icBurstFreqNew
	if bi.ageAt(now) >= bi.icNewTime {
		freqThreshold = bi.icBurstFreq
	}
	iaFreq := bi.incomingAnnounceFrequencyAtLocked(now)
	if bi.icBurstActive {
		if iaFreq < freqThreshold && now.After(bi.icBurstActivated.Add(time.Duration(bi.icBurstHold)*time.Second)) {
			if len(bi.iaFreqDeque) >= ICDequeMinSample {
				bi.icBurstActive = false
			}
		}
		return true
	}
	if iaFreq > freqThreshold {
		bi.icBurstActive = true
		bi.icBurstActivated = now
		bi.icHeldRelease = now.Add(time.Duration(bi.icBurstPenalty) * time.Second)
		return true
	}
	return false
}

// ShouldIngressLimitPr reports whether inbound path requests on this interface
// should be gated (recursive path-request forwarding suppressed), activating and
// deactivating the PR-burst state machine as a side effect. It is the public
// time.Now()-driven entry point for Interface.should_ingress_limit_pr
// (Interface.py:174-190).
func (bi *BaseInterface) ShouldIngressLimitPr() bool {
	return bi.shouldIngressLimitPrAt(time.Now())
}

// shouldIngressLimitPrAt is the deterministic core of ShouldIngressLimitPr. It
// mirrors Interface.py:174-190 exactly:
//
//	if not ingress_control: return False
//	freq_threshold = ic_pr_burst_freq_new if age < ic_new_time else ic_pr_burst_freq
//	ip_freq = incoming_pr_frequency()
//	if ic_pr_burst_active:
//	    if ip_freq < freq_threshold and now > activated+ic_burst_hold:
//	        ic_pr_burst_active = False
//	    return True
//	else:
//	    if ip_freq > freq_threshold:
//	        ic_pr_burst_active = True; ic_pr_burst_activated = now
//	        return True
//	    else: return False
//
// Unlike the announce burst, activation sets no held-release penalty — the PR
// burst only suppresses recursive path-request forwarding while active.
func (bi *BaseInterface) shouldIngressLimitPrAt(now time.Time) bool {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	if !bi.ingressControl {
		return false
	}
	freqThreshold := bi.icPrBurstFreqNew
	if bi.ageAt(now) >= bi.icNewTime {
		freqThreshold = bi.icPrBurstFreq
	}
	ipFreq := bi.incomingPrFrequencyAtLocked(now)
	if bi.icPrBurstActive {
		if ipFreq < freqThreshold && now.After(bi.icPrBurstActivated.Add(time.Duration(bi.icBurstHold)*time.Second)) {
			bi.icPrBurstActive = false
		}
		return true
	}
	if ipFreq > freqThreshold {
		bi.icPrBurstActive = true
		bi.icPrBurstActivated = now
		return true
	}
	return false
}

// ShouldEgressLimitPr reports whether outbound path requests on this interface
// should be gated due to egress limiting. It is the public time.Now()-driven
// entry point for Interface.should_egress_limit_pr (Interface.py:192-200).
func (bi *BaseInterface) ShouldEgressLimitPr() bool {
	return bi.shouldEgressLimitPrAt(time.Now())
}

// shouldEgressLimitPrAt is the deterministic core of ShouldEgressLimitPr. It
// mirrors Interface.py:192-200 exactly:
//
//	if not egress_control: return False
//	freq_threshold = ec_pr_freq
//	op_freq = outgoing_pr_frequency()
//	if op_freq > freq_threshold:
//	    if len(op_freq_deque) >= IC_BURST_MIN_SAMPLES(6): return True
//	return False
func (bi *BaseInterface) shouldEgressLimitPrAt(now time.Time) bool {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	if !bi.egressControl {
		return false
	}
	opFreq := bi.outgoingPrFrequencyAtLocked(now)
	if opFreq > bi.ecPrFreq {
		if len(bi.opFreqDeque) >= ICBurstMinSamples {
			return true
		}
	}
	return false
}

// HoldAnnounce stashes an inbound announce so it can be released later once
// the ingress burst subsides (Interface.py:224-230). An announce at or beyond
// PATHFINDER_M-1 hops is dropped outright (v1.4.0): it has already traveled too
// far to be worth re-broadcasting once released. An announce for an already-held
// destination replaces the held copy; a new destination is held only while under
// ic_max_held_announces.
func (bi *BaseInterface) HoldAnnounce(raw []byte, recv Interface, hops int, destHash []byte) {
	if hops >= PathfinderM-1 {
		return
	}
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	if bi.heldAnnounces == nil {
		bi.heldAnnounces = make(map[string]heldAnnounce)
	}
	key := string(destHash)
	entry := heldAnnounce{raw: raw, recv: recv, hops: hops, destHash: append([]byte(nil), destHash...)}
	if _, exists := bi.heldAnnounces[key]; exists {
		bi.heldAnnounces[key] = entry
	} else if len(bi.heldAnnounces) < bi.icMaxHeldAnnounces {
		bi.heldAnnounces[key] = entry
	}
}

// HeldAnnounces returns the number of announces currently held by ingress
// limiting on this interface (Python len(self.held_announces)).
func (bi *BaseInterface) HeldAnnounces() int {
	bi.ingressMu.RLock()
	defer bi.ingressMu.RUnlock()
	return len(bi.heldAnnounces)
}

// ReleaseHeldAnnounce removes and returns the held announce for the given
// destination hash, bypassing the ingress-limit frequency gate. This is used
// when an explicit path request is made for the destination (e.g. on-send),
// so the held announce is processed immediately instead of waiting for the
// announce frequency to drop below the burst threshold.
func (bi *BaseInterface) ReleaseHeldAnnounce(destHash []byte) (raw []byte, recv Interface, ok bool) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	if bi.heldAnnounces == nil {
		return nil, nil, false
	}
	key := string(destHash)
	entry, exists := bi.heldAnnounces[key]
	if !exists {
		return nil, nil, false
	}
	delete(bi.heldAnnounces, key)
	return entry.raw, entry.recv, true
}

// ClearHeldAnnounces empties the interface's held-announce deque. It is the
// per-interface analog of clearing Python's global Transport.held_announces
// dict, used by TransportSystem.voidQueuesLocked on transport stop
// (Transport.py:3517-3521).
func (bi *BaseInterface) ClearHeldAnnounces() {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	bi.heldAnnounces = make(map[string]heldAnnounce)
}

// ProcessHeldAnnounces releases a single held announce — the fewest-hops one
// — once the burst has subsided, returning it for the caller to re-inject
// into Transport.Inbound. It is the public time.Now()-driven entry point for
// Interface.process_held_announces (Interface.py:232-255). ok is false when
// nothing was released.
func (bi *BaseInterface) ProcessHeldAnnounces() (raw []byte, recv Interface, ok bool) {
	return bi.processHeldAnnouncesAt(time.Now())
}

// processHeldAnnouncesAt is the deterministic core of ProcessHeldAnnounces.
// It mirrors Interface.py:232-255: release the min-hops held announce when
// there are held announces, now is past ic_held_release, and the incoming
// announce frequency has fallen below the burst threshold. On release it
// advances ic_held_release by ic_held_release_interval.
func (bi *BaseInterface) processHeldAnnouncesAt(now time.Time) (raw []byte, recv Interface, ok bool) {
	bi.ingressMu.Lock()
	defer bi.ingressMu.Unlock()
	if len(bi.heldAnnounces) == 0 || !now.After(bi.icHeldRelease) {
		return nil, nil, false
	}
	freqThreshold := bi.icBurstFreqNew
	if bi.ageAt(now) >= bi.icNewTime {
		freqThreshold = bi.icBurstFreq
	}
	if bi.incomingAnnounceFrequencyAtLocked(now) >= freqThreshold {
		return nil, nil, false
	}

	minHops := PathfinderM
	var selectedKey string
	var selected heldAnnounce
	found := false
	for key, ha := range bi.heldAnnounces {
		if !found || ha.hops < minHops {
			minHops = ha.hops
			selectedKey = key
			selected = ha
			found = true
		}
	}
	if !found {
		return nil, nil, false
	}
	bi.icHeldRelease = now.Add(time.Duration(bi.icHeldReleaseInterval) * time.Second)
	delete(bi.heldAnnounces, selectedKey)
	return selected.raw, selected.recv, true
}

// AnnouncesFromInternal reports whether announces whose next-hop interface is
// MODE_INTERNAL are accepted (RNS v1.3.7). Defaults to true, matching
// Interface.__init__.
func (bi *BaseInterface) AnnouncesFromInternal() bool { return bi.announcesFromInternal }

// SetAnnouncesFromInternal sets the internal-announce acceptance policy.
func (bi *BaseInterface) SetAnnouncesFromInternal(v bool) { bi.announcesFromInternal = v }

// AnnouncesToInternal reports the boundary→internal announce allowance
// (RNS v1.4.1). A nil pointer means the interface does not override the
// default block (Python default None).
func (bi *BaseInterface) AnnouncesToInternal() *bool { return bi.announcesToInternal }

// SetAnnouncesToInternal sets the boundary→internal allowance; pass nil to
// restore the default (no override).
func (bi *BaseInterface) SetAnnouncesToInternal(v *bool) { bi.announcesToInternal = v }

// DefaultIFACSize returns the interface type's DEFAULT_IFAC_SIZE (RNS
// v1.1.6 class attribute). Concrete constructors set it so IFAC size
// defaulting matches the interface type rather than a hardcoded value. Returns
// the historical default 16 when a constructor did not set it.
func (bi *BaseInterface) DefaultIFACSize() int {
	if bi == nil {
		return 16
	}
	if bi.defaultIFACSize > 0 {
		return bi.defaultIFACSize
	}
	return 16
}

// setDefaultIFACSize records the concrete interface type's DEFAULT_IFAC_SIZE.
// It is called from each concrete constructor to mirror the Python class
// attribute.
func (bi *BaseInterface) setDefaultIFACSize(n int) { bi.defaultIFACSize = n }

// MemoizedHash returns the interface identity hash, computing it via compute on
// the first call and returning the cached value thereafter. It is the Go port of
// Python Interface.get_hash (RNS/Interfaces/Interface.py:144-146), where the
// SHA-256 of str(self) is computed once and stored on the instance. The compute
// closure is supplied by the caller (e.g. the transport's interfaceHash helper)
// so the concrete Type()/Name() virtual dispatch supplies the string identity,
// matching Python's full_hash(str(self)); the sync.Once guarantees the closure
// runs at most once across concurrent callers.
func (bi *BaseInterface) MemoizedHash(compute func() []byte) []byte {
	bi.hashOnce.Do(func() {
		if bi.hash == nil && compute != nil {
			bi.hash = compute()
		}
	})
	return bi.hash
}

// Age returns the duration since the interface was created and added to the
// network. It is used to identify and prune stale or malfunctioning
// interfaces.
func (bi *BaseInterface) Age() time.Duration { return time.Since(bi.created) }

// IsDetached returns true if the interface has been logically severed from the
// active stack. Readers and writers use this flag to terminate work and release
// resources.
func (bi *BaseInterface) IsDetached() bool { return atomic.LoadInt32(&bi.detached) == 1 }

// SetDetached atomically updates the interface lifecycle flag. Setting this to
// true signals workers to stop and release resources.
func (bi *BaseInterface) SetDetached(detached bool) {
	if detached {
		atomic.StoreInt32(&bi.detached, 1)
		return
	}
	atomic.StoreInt32(&bi.detached, 0)
}

// BytesReceived returns the atomically managed counter of payload bytes
// ingested by this interface. It is used for telemetry and throughput modeling.
// The counter is written atomically from interface receive goroutines, so the
// read here is atomic too (the traffic counter loop reads it concurrently with
// ingress).
func (bi *BaseInterface) BytesReceived() uint64 { return atomic.LoadUint64(&bi.rxBytes) }

// BytesSent returns the atomic metric recording bytes dispatched by this
// interface. It provides observability into the interface's workload.
func (bi *BaseInterface) BytesSent() uint64 { return atomic.LoadUint64(&bi.txBytes) }

// CurrentRxSpeed returns the most recent receive speed (bits/sec) computed by
// the transport traffic loop (Python interface.current_rx_speed). It is 0
// until the loop has completed at least one interval.
func (bi *BaseInterface) CurrentRxSpeed() float64 {
	return math.Float64frombits(atomic.LoadUint64(&bi.currentRxSpeedBits))
}

// CurrentTxSpeed returns the most recent transmit speed (bits/sec) computed by
// the transport traffic loop (Python interface.current_tx_speed).
func (bi *BaseInterface) CurrentTxSpeed() float64 {
	return math.Float64frombits(atomic.LoadUint64(&bi.currentTxSpeedBits))
}

// SetTrafficSpeeds stores the per-interface receive/transmit speeds (bits/sec)
// computed by the transport traffic loop. Mirrors Python's
// `interface.current_rx_speed = crxs; interface.current_tx_speed = ctxs`
// (Transport.py:435-436).
func (bi *BaseInterface) SetTrafficSpeeds(rxSpeed, txSpeed float64) {
	atomic.StoreUint64(&bi.currentRxSpeedBits, math.Float64bits(rxSpeed))
	atomic.StoreUint64(&bi.currentTxSpeedBits, math.Float64bits(txSpeed))
}

// SetIFACConfig reinitializes the interface's cryptographic authentication
// layer using the provided parameters. It regenerates keying material and
// updates signing state while holding a lock for thread safety.
func (bi *BaseInterface) SetIFACConfig(cfg IFACConfig) {
	bi.ifacMu.Lock()
	defer bi.ifacMu.Unlock()

	bi.ifacConfig = cfg
	bi.ifacKey = nil
	bi.ifacSigner = nil

	if !cfg.Enabled {
		return
	}

	if bi.ifacConfig.Size < 1 {
		bi.ifacConfig.Size = bi.DefaultIFACSize()
	}
	if bi.ifacConfig.Size > IFACMaxSize {
		bi.ifacConfig.Size = IFACMaxSize
	}

	origin := make([]byte, 0, 64)
	if cfg.NetName != "" {
		origin = append(origin, rnscrypto.SHA256([]byte(cfg.NetName))...)
	}
	if cfg.NetKey != "" {
		origin = append(origin, rnscrypto.SHA256([]byte(cfg.NetKey))...)
	}
	if len(origin) == 0 {
		bi.ifacConfig.Enabled = false
		return
	}

	originHash := rnscrypto.SHA256(origin)
	ifacKey, err := rnscrypto.HKDF(64, originHash, ifacSalt, nil)
	if err != nil || len(ifacKey) != 64 {
		bi.ifacConfig.Enabled = false
		return
	}

	signer, err := rnscrypto.NewEd25519PrivateKeyFromBytes(ifacKey[32:])
	if err != nil {
		bi.ifacConfig.Enabled = false
		return
	}

	bi.ifacKey = ifacKey
	bi.ifacSigner = signer
}

// IFACConfig returns a thread-consistent snapshot of the interface's active
// authentication configuration. It enables inspection without violating memory
// safety.
func (bi *BaseInterface) IFACConfig() IFACConfig {
	bi.ifacMu.RLock()
	defer bi.ifacMu.RUnlock()
	return bi.ifacConfig
}

func cloneOptionalFloat64(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneOptionalInt(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneDiscoveryConfig(cfg DiscoveryConfig) DiscoveryConfig {
	cfg.Latitude = cloneOptionalFloat64(cfg.Latitude)
	cfg.Longitude = cloneOptionalFloat64(cfg.Longitude)
	cfg.Height = cloneOptionalFloat64(cfg.Height)
	cfg.Frequency = cloneOptionalInt(cfg.Frequency)
	cfg.Bandwidth = cloneOptionalInt(cfg.Bandwidth)
	cfg.SpreadingFactor = cloneOptionalInt(cfg.SpreadingFactor)
	cfg.CodingRate = cloneOptionalInt(cfg.CodingRate)
	cfg.Channel = cloneOptionalInt(cfg.Channel)
	return cfg
}

// SetDiscoveryConfig stores the interface discovery metadata used by the
// discovery announcer and listener subsystems.
func (bi *BaseInterface) SetDiscoveryConfig(cfg DiscoveryConfig) {
	bi.discoveryMu.Lock()
	defer bi.discoveryMu.Unlock()
	bi.discoveryConfig = cloneDiscoveryConfig(cfg)
}

// DiscoveryConfig returns a copy of the configured interface discovery
// metadata.
func (bi *BaseInterface) DiscoveryConfig() DiscoveryConfig {
	bi.discoveryMu.RLock()
	defer bi.discoveryMu.RUnlock()
	return cloneDiscoveryConfig(bi.discoveryConfig)
}

// SetAutoconnect records discovery-driven metadata for interfaces that were
// synthesized from on-network interface discovery.
func (bi *BaseInterface) SetAutoconnect(hash []byte, source string) {
	bi.autoMu.Lock()
	defer bi.autoMu.Unlock()
	bi.autoconnectHash = append(bi.autoconnectHash[:0], hash...)
	bi.autoconnectSrc = source
}

// SetBootstrapOnly marks the interface as a bootstrap-only helper that may be
// torn down once enough auto-discovered interfaces are online.
func (bi *BaseInterface) SetBootstrapOnly(bootstrapOnly bool) {
	bi.autoMu.Lock()
	defer bi.autoMu.Unlock()
	bi.bootstrapOnly = bootstrapOnly
}

// BootstrapOnly reports whether the interface was configured for bootstrap-only
// use.
func (bi *BaseInterface) BootstrapOnly() bool {
	bi.autoMu.RLock()
	defer bi.autoMu.RUnlock()
	return bi.bootstrapOnly
}

// AutoconnectHash returns the stable endpoint hash associated with a
// discovery-autoconnected interface.
func (bi *BaseInterface) AutoconnectHash() []byte {
	bi.autoMu.RLock()
	defer bi.autoMu.RUnlock()
	return append([]byte(nil), bi.autoconnectHash...)
}

// AutoconnectSource returns the discovery source network identity hash that
// produced this auto-connected interface.
func (bi *BaseInterface) AutoconnectSource() string {
	bi.autoMu.RLock()
	defer bi.autoMu.RUnlock()
	return bi.autoconnectSrc
}

// ApplyIFACInbound processes incoming raw bytes and validates cryptographic
// authentication tags. Malformed or unauthentic payloads are rejected at the
// lowest possible layer.
func (bi *BaseInterface) ApplyIFACInbound(data []byte) ([]byte, bool) {
	if len(data) <= 2 {
		return nil, false
	}

	bi.ifacMu.RLock()
	ifacConfig := bi.ifacConfig
	ifacSigner := bi.ifacSigner
	ifacKey := make([]byte, len(bi.ifacKey))
	copy(ifacKey, bi.ifacKey)
	bi.ifacMu.RUnlock()

	ifacEnabled := ifacConfig.Enabled && ifacSigner != nil && len(ifacKey) == 64
	hasIFACFlag := (data[0] & 0x80) == 0x80

	if !ifacEnabled {
		if hasIFACFlag {
			return nil, false
		}
		out := make([]byte, len(data))
		copy(out, data)
		return out, true
	}

	if !hasIFACFlag {
		return nil, false
	}

	ifacSize := ifacConfig.Size
	if len(data) <= 2+ifacSize {
		return nil, false
	}

	ifac := make([]byte, ifacSize)
	copy(ifac, data[2:2+ifacSize])

	mask, err := rnscrypto.HKDF(len(data), ifac, ifacKey, nil)
	if err != nil {
		return nil, false
	}

	unmasked := make([]byte, len(data))
	for i := range len(data) {
		if i <= 1 || i > ifacSize+1 {
			unmasked[i] = data[i] ^ mask[i]
		} else {
			unmasked[i] = data[i]
		}
	}

	newRaw := make([]byte, 0, len(data)-ifacSize)
	newRaw = append(newRaw, unmasked[0]&0x7f, unmasked[1])
	newRaw = append(newRaw, unmasked[2+ifacSize:]...)

	sig := ifacSigner.Sign(newRaw)
	// Defense-in-depth: SetIFACConfig clamps Size to IFACMaxSize, but a
	// misconfigured or directly-constructed config could still set ifacSize
	// larger than the signature. Guard before slicing to avoid underflow.
	if ifacSize > len(sig) {
		return nil, false
	}
	expectedIFAC := sig[len(sig)-ifacSize:]
	if !bytes.Equal(ifac, expectedIFAC) {
		return nil, false
	}

	return newRaw, true
}

// ApplyIFACOutbound embeds cryptographic signatures into outgoing payloads
// before physical transmission. It ensures outgoing data adheres to the IFAC
// security envelope.
func (bi *BaseInterface) ApplyIFACOutbound(data []byte) ([]byte, error) {
	bi.ifacMu.RLock()
	ifacConfig := bi.ifacConfig
	ifacSigner := bi.ifacSigner
	ifacKey := make([]byte, len(bi.ifacKey))
	copy(ifacKey, bi.ifacKey)
	bi.ifacMu.RUnlock()

	if len(data) <= 2 || !ifacConfig.Enabled || ifacSigner == nil || len(ifacKey) != 64 {
		out := make([]byte, len(data))
		copy(out, data)
		return out, nil
	}

	ifacSize := ifacConfig.Size
	sig := ifacSigner.Sign(data)
	// Defense-in-depth: SetIFACConfig clamps Size to IFACMaxSize, but a
	// misconfigured or directly-constructed config could still set ifacSize
	// larger than the 64-byte Ed25519 signature and underflow the slice.
	if ifacSize > len(sig) {
		return nil, fmt.Errorf("ifac size %d exceeds signature length %d", ifacSize, len(sig))
	}
	ifac := make([]byte, ifacSize)
	copy(ifac, sig[len(sig)-ifacSize:])

	mask, err := rnscrypto.HKDF(len(data)+ifacSize, ifac, ifacKey, nil)
	if err != nil {
		return nil, err
	}

	newRaw := make([]byte, 0, len(data)+ifacSize)
	newRaw = append(newRaw, data[0]|0x80, data[1])
	newRaw = append(newRaw, ifac...)
	newRaw = append(newRaw, data[2:]...)

	masked := make([]byte, len(newRaw))
	for i := range len(newRaw) {
		if i == 0 {
			masked[i] = (newRaw[i] ^ mask[i]) | 0x80
		} else if i == 1 || i > ifacSize+1 {
			masked[i] = newRaw[i] ^ mask[i]
		} else {
			masked[i] = newRaw[i]
		}
	}

	return masked, nil
}

var ifacSalt = []byte{
	0xad, 0xf5, 0x4d, 0x88, 0x2c, 0x9a, 0x9b, 0x80,
	0x77, 0x1e, 0xb4, 0x99, 0x5d, 0x70, 0x2d, 0x4a,
	0x3e, 0x73, 0x33, 0x91, 0xb2, 0xa0, 0xf5, 0x3f,
	0x41, 0x6d, 0x9f, 0x90, 0x7e, 0x55, 0xcf, 0xf8,
}

// Ingress/egress-control constants matching Python's Interface class
// (RNS/Interfaces/Interface.py:60-92, v1.1.5→1.4.0).
const (
	// Announce/PR frequency sample deque lengths.
	IAFreqSamples = 48
	OAFreqSamples = 48
	IPFreqSamples = 48
	OPFreqSamples = 48

	// Minimum frequency (Hz) before a frequency deque decays: once the span
	// exceeds 1/MINFREQ the oldest sample is popped. AR_FREQ_DECAY and
	// PR_FREQ_DECAY both equal 10 (Interface.py:65-68).
	ARMinfreqHz = 0.1
	PRMinfreqHz = 0.1
	ARFreqDecay = 1 / ARMinfreqHz
	PRFreqDecay = 1 / PRMinfreqHz

	// MaxHeldAnnounces matches Python's MAX_HELD_ANNOUNCES.
	MaxHeldAnnounces = 256

	// Control parameters (Interface.py:76-87).
	ICNewTime             = 2 * 60 * 60
	ICBurstFreqNew        = 3.0
	ICBurstFreq           = 10.0
	ICPrBurstFreqNew      = 3.0
	ICPrBurstFreq         = 8.0
	ICBurstHold           = 15.0
	ICBurstPenalty        = 15.0
	ICHeldReleaseInterval = 5.0
	ICDequeMinSample      = 2
	ICBurstMinSamples     = 6
	ECPrFreq              = 5.0
	EgressControlDefault  = false

	// Default announce rate targets (Interface.py:90-92). These are the
	// class-constant fallbacks for the Reticulum [reticulum]
	// default_ar_target/penalty/grace keys when those keys are unset or zero
	// (Reticulum.py:1145-1152 resolves None → these constants).
	DefaultARTarget  = 3600
	DefaultARPenalty = 0
	DefaultARGrace   = 5

	// PathfinderM mirrors rns.PathfinderM / Transport.PATHFINDER_M (128):
	// the maximum number of hops in path finding. It is used as the initial
	// minimum when selecting the fewest-hops held announce to release.
	PathfinderM = 128
)

// heldAnnounce captures an inbound announce temporarily held by ingress
// limiting, together with the receiving interface and hop count needed to
// re-inject it into Transport.Inbound once the burst subsides. Mirrors the
// Python held_announces entries (full announce packets, Interface.py:137/224).
type heldAnnounce struct {
	raw      []byte
	recv     Interface
	hops     int
	destHash []byte
}
