// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// WeaveInterfacePeer spawning (RNS/Interfaces/WeaveInterface.py:920-1063,
// WeaveInterface.add_peer + class WeaveInterfacePeer). The peer is a logical
// RNS interface representing one remote endpoint multiplexed over a parent
// WeaveInterface's serial connection. The spawn copies a specific subset of
// the parent's routing-policy fields onto the peer; the data path
// (process_incoming/process_outgoing routed through the owner's Weave device)
// belongs to the linux-only connection-layer port and is not wired here. The
// spawn + field-inheritance — the parity-relevant behavior — is pure and
// platform-independent, so it lives here (no build tag) and is unit-tested
// cross-platform.

package interfaces

import (
	"fmt"
	"sync/atomic"
)

// WeaveDefaultIFACSize is Weave's own DEFAULT_IFAC_SIZE (16), overriding the
// wrapped serial interface's 8 (RNS/Interfaces/WeaveInterface.py:820,881-884).
// It is shared by the linux-only WeaveInterface and the platform-independent
// WeaveInterfacePeer spawn, so it lives here (no build tag).
const WeaveDefaultIFACSize = 16

// WeavePeerSpawnConfig is the set of parent-WeaveInterface fields that add_peer
// copies onto each spawned WeaveInterfacePeer (WeaveInterface.py:924-963). The
// linux-only WeaveInterface serial transport fills it from its own state and
// hands it to SpawnWeavePeer; tests build it directly. This is the exact Python
// add_peer field list: OUT/IN, gravity, bitrate, mode, HW_MTU, the IFAC config,
// the ingress-control + ic_* fields, and the announce-rate config. Note the
// absent fields — egress_control, ec_pr_freq, ic_pr_burst_freq,
// ic_pr_burst_freq_new — are deliberately NOT propagated to Weave peers
// (Python's add_peer never sets them), unlike the Auto/TCP/Backbone/I2P spawn
// which uses copyIngressEgressFrom and does propagate them.
type WeavePeerSpawnConfig struct {
	OUT                   bool
	IN                    bool
	Gravity               int
	Bitrate               int
	Mode                  int
	HWMTU                 int
	IFAC                  IFACConfig
	IngressControl        bool
	ICMaxHeldAnnounces    int
	ICBurstHold           float64
	ICBurstFreq           float64
	ICBurstFreqNew        float64
	ICNewTime             float64
	ICBurstPenalty        float64
	ICHeldReleaseInterval float64
	AnnounceRateTarget    *int
	AnnounceRateGrace     *int
	AnnounceRatePenalty   *int
}

// WeaveInterfacePeer is a logical RNS interface for one remote Weave endpoint
// multiplexed over a parent WeaveInterface (WeaveInterface.py:1009-1018,
// class WeaveInterfacePeer). It embeds a BaseInterface for the routing-policy
// surface. The endpoint address identifies the remote peer on the shared
// connection; viaSwitchID/peerAddr are filled later by the Weave control
// protocol (WeaveInterface.py:1019-1021, set by endpoint_via). OUT/IN/HWMTU are
// peer-specific copies of the parent's values (they are not on BaseInterface).
type WeaveInterfacePeer struct {
	*BaseInterface
	owner        Interface
	endpointAddr []byte
	viaSwitchID  []byte
	peerAddr     []byte
	out          bool
	in           bool
	hwmtu        int
	online       atomic.Bool
}

// SpawnWeavePeer creates a WeaveInterfacePeer for endpointAddr, applying the
// Weave add_peer spawn field set from cfg (WeaveInterface.py:920-963). The peer
// is returned online and detached=false, ready for the caller to register with
// Transport (mirroring RNS.Transport.add_interface(spawned_interface), which is
// the caller's responsibility since it needs a live Transport instance).
//
// Field copy (faithful to the Python add_peer block, NOT the broader
// copyIngressEgressFrom set): gravity, mode, bitrate, HW_MTU, OUT/IN, the IFAC
// config (re-derived by SetIFACConfig, matching Python's ifac key derivation),
// ingress_control + the eight ic_* tuning fields, and the announce-rate
// config. egress_control, ec_pr_freq, and the ic_pr_* PR-burst fields are
// intentionally left at their BaseInterface defaults — Python's add_peer does
// not propagate them to Weave peers.
//
// parent is the owning WeaveInterface (kept as Interface so this file has no
// build constraint); the peer reports to it for Status.
func SpawnWeavePeer(parent Interface, cfg WeavePeerSpawnConfig, endpointAddr []byte) *WeaveInterfacePeer {
	name := weaveHexrep(endpointAddr, false)
	bi := NewBaseInterface(name, cfg.Mode, cfg.Bitrate)
	bi.SetGravity(cfg.Gravity)
	bi.setDefaultIFACSize(WeaveDefaultIFACSize)
	bi.SetIFACConfig(cfg.IFAC)
	bi.SetIngressControl(cfg.IngressControl)
	bi.SetICMaxHeldAnnounces(cfg.ICMaxHeldAnnounces)
	bi.SetICBurstHold(cfg.ICBurstHold)
	bi.SetICBurstFreq(cfg.ICBurstFreq)
	bi.SetICBurstFreqNew(cfg.ICBurstFreqNew)
	bi.SetICNewTime(cfg.ICNewTime)
	bi.SetICBurstPenalty(cfg.ICBurstPenalty)
	bi.SetICHeldReleaseInterval(cfg.ICHeldReleaseInterval)
	bi.SetAnnounceRateTarget(cfg.AnnounceRateTarget)
	bi.SetAnnounceRateGrace(cfg.AnnounceRateGrace)
	bi.SetAnnounceRatePenalty(cfg.AnnounceRatePenalty)

	p := &WeaveInterfacePeer{
		BaseInterface: bi,
		owner:         parent,
		endpointAddr:  append([]byte(nil), endpointAddr...),
		out:           cfg.OUT,
		in:            cfg.IN,
		hwmtu:         cfg.HWMTU,
	}
	p.online.Store(true)
	return p
}

// Type identifies this interface as a WeaveInterfacePeer
// (WeaveInterface.py:1009, class name).
func (p *WeaveInterfacePeer) Type() string { return "WeaveInterfacePeer" }

// Status reports whether the peer is online and its owner WeaveInterface is
// still up (WeaveInterface.py:1027-1029, the online property).
func (p *WeaveInterfacePeer) Status() bool {
	if !p.online.Load() {
		return false
	}
	if p.owner != nil {
		return p.owner.Status()
	}
	return true
}

// IsOut reports the peer's OUT flag copied from the parent at spawn time
// (WeaveInterface.py:925, spawned_interface.OUT = self.OUT).
func (p *WeaveInterfacePeer) IsOut() bool { return p.out }

// Send transmits a payload to this peer via the owner's Weave device
// (WeaveInterface.py:1051-1060). The device data path is part of the
// linux-only connection-layer port and is not wired here; an offline peer
// returns an error, an online peer returns nil (no-op until the connection
// layer is ported).
func (p *WeaveInterfacePeer) Send(data []byte) error {
	if !p.Status() {
		return fmt.Errorf("weave peer %s is offline", weaveHexrep(p.endpointAddr, false))
	}
	return nil
}

// Detach marks the peer offline and detached
// (WeaveInterface.py:1062-1065, WeaveInterfacePeer.detach).
func (p *WeaveInterfacePeer) Detach() error {
	p.online.Store(false)
	p.SetDetached(true)
	return nil
}
