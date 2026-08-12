// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"testing"
)

// weaveSpawnTestConfig returns a WeavePeerSpawnConfig with every field set to a
// non-default value, matching the golden captured from RNS 1.4.2's
// WeaveInterface.add_peer (WeaveInterface.py:920-963). The values are chosen to
// differ from the BaseInterface defaults so a copy that misses any field is
// caught.
func weaveSpawnTestConfig() WeavePeerSpawnConfig {
	return WeavePeerSpawnConfig{
		OUT:                   true,
		IN:                    true,
		Gravity:               88,
		Bitrate:               500000,
		Mode:                  1,
		HWMTU:                 4096,
		IFAC:                  IFACConfig{Enabled: true, NetName: "weave-net", Size: 16},
		IngressControl:        false,
		ICMaxHeldAnnounces:    99,
		ICBurstHold:           99.5,
		ICBurstFreq:           99.6,
		ICBurstFreqNew:        99.7,
		ICNewTime:             99.8,
		ICBurstPenalty:        99.9,
		ICHeldReleaseInterval: 99.1,
	}
}

// TestWeavePeerSpawnCopiesFields covers Phase 20 task 4: SpawnWeavePeer copies
// the full Weave add_peer field set — gravity, mode, bitrate, HW_MTU, OUT/IN,
// IFAC config, ingress_control, all eight ic_* tuning fields, and the
// announce-rate config — onto the spawned peer (WeaveInterface.py:924-963).
// Each field is asserted against the golden config value.
func TestWeavePeerSpawnCopiesFields(t *testing.T) {
	t.Parallel()
	cfg := weaveSpawnTestConfig()
	target, grace, penalty := 12, 34, 56
	cfg.AnnounceRateTarget = &target
	cfg.AnnounceRateGrace = &grace
	cfg.AnnounceRatePenalty = &penalty

	endpoint := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	p := SpawnWeavePeer(nil, cfg, endpoint)

	if got := p.Gravity(); got != cfg.Gravity {
		t.Errorf("Gravity = %d, want %d", got, cfg.Gravity)
	}
	if got := p.Mode(); got != cfg.Mode {
		t.Errorf("Mode = %d, want %d", got, cfg.Mode)
	}
	if got := p.Bitrate(); got != cfg.Bitrate {
		t.Errorf("Bitrate = %d, want %d", got, cfg.Bitrate)
	}
	if got := p.hwmtu; got != cfg.HWMTU {
		t.Errorf("hwmtu = %d, want %d", got, cfg.HWMTU)
	}
	if got := p.IsOut(); got != cfg.OUT {
		t.Errorf("IsOut = %v, want %v", got, cfg.OUT)
	}
	if got := p.in; got != cfg.IN {
		t.Errorf("in = %v, want %v", got, cfg.IN)
	}
	if got := p.IngressControl(); got != cfg.IngressControl {
		t.Errorf("IngressControl = %v, want %v", got, cfg.IngressControl)
	}
	if got := p.ICMaxHeldAnnounces(); got != cfg.ICMaxHeldAnnounces {
		t.Errorf("ICMaxHeldAnnounces = %d, want %d", got, cfg.ICMaxHeldAnnounces)
	}
	if got := p.ICBurstHold(); got != cfg.ICBurstHold {
		t.Errorf("ICBurstHold = %v, want %v", got, cfg.ICBurstHold)
	}
	if got := p.ICBurstFreq(); got != cfg.ICBurstFreq {
		t.Errorf("ICBurstFreq = %v, want %v", got, cfg.ICBurstFreq)
	}
	if got := p.ICBurstFreqNew(); got != cfg.ICBurstFreqNew {
		t.Errorf("ICBurstFreqNew = %v, want %v", got, cfg.ICBurstFreqNew)
	}
	if got := p.ICNewTime(); got != cfg.ICNewTime {
		t.Errorf("ICNewTime = %v, want %v", got, cfg.ICNewTime)
	}
	if got := p.ICBurstPenalty(); got != cfg.ICBurstPenalty {
		t.Errorf("ICBurstPenalty = %v, want %v", got, cfg.ICBurstPenalty)
	}
	if got := p.ICHeldReleaseInterval(); got != cfg.ICHeldReleaseInterval {
		t.Errorf("ICHeldReleaseInterval = %v, want %v", got, cfg.ICHeldReleaseInterval)
	}
	// Announce-rate pointers are shared with the config (Python copies the
	// reference; the existing copyIngressEgressFrom does the same).
	if p.AnnounceRateTarget() != cfg.AnnounceRateTarget {
		t.Errorf("AnnounceRateTarget = %p, want %p", p.AnnounceRateTarget(), cfg.AnnounceRateTarget)
	}
	if p.AnnounceRateGrace() != cfg.AnnounceRateGrace {
		t.Errorf("AnnounceRateGrace = %p, want %p", p.AnnounceRateGrace(), cfg.AnnounceRateGrace)
	}
	if p.AnnounceRatePenalty() != cfg.AnnounceRatePenalty {
		t.Errorf("AnnounceRatePenalty = %p, want %p", p.AnnounceRatePenalty(), cfg.AnnounceRatePenalty)
	}
	// endpointAddr is copied (defensive copy, not aliased).
	if string(p.endpointAddr) != string(endpoint) {
		t.Errorf("endpointAddr = %v, want %v", p.endpointAddr, endpoint)
	}
}

// TestWeavePeerSpawnIFACCopied covers Phase 20 task 4: the IFAC config
// (ifac_size/ifac_netname/ifac_netkey) is copied onto the spawned peer and the
// key material is re-derived by SetIFACConfig, matching Python's add_peer IFAC
// block (WeaveInterface.py:940-958). Two peers spawned with the same IFAC
// config produce the same IFACConfig snapshot.
func TestWeavePeerSpawnIFACCopied(t *testing.T) {
	t.Parallel()
	cfg := WeavePeerSpawnConfig{IFAC: IFACConfig{Enabled: true, NetName: "weave-net", Size: 16}}
	p := SpawnWeavePeer(nil, cfg, []byte{0x01})
	got := p.IFACConfig()
	if !got.Enabled {
		t.Errorf("IFAC.Enabled = false, want true (key derived from NetName)")
	}
	if got.NetName != "weave-net" {
		t.Errorf("IFAC.NetName = %q, want %q", got.NetName, "weave-net")
	}
	if got.Size != 16 {
		t.Errorf("IFAC.Size = %d, want 16", got.Size)
	}
	// Same IFAC inputs -> same derived config snapshot.
	p2 := SpawnWeavePeer(nil, cfg, []byte{0x02})
	got2 := p2.IFACConfig()
	if got != got2 {
		t.Errorf("IFAC snapshots differ for identical inputs: %v vs %v", got, got2)
	}
}

// TestWeavePeerSpawnOmitsEgressAndPR covers Phase 20 task 4: the Weave add_peer
// spawn deliberately does NOT propagate egress_control, ec_pr_freq, or the
// ic_pr_* PR-burst fields (WeaveInterface.py:924-963 omits them, unlike
// copyIngressEgressFrom used by Auto/TCP/Backbone/I2P). The spawned peer keeps
// the BaseInterface defaults for these fields. Golden: egress_control=false,
// ec_pr_freq=5.0, ic_pr_burst_freq=8.0, ic_pr_burst_freq_new=3.0.
func TestWeavePeerSpawnOmitsEgressAndPR(t *testing.T) {
	t.Parallel()
	cfg := weaveSpawnTestConfig()
	p := SpawnWeavePeer(nil, cfg, []byte{0x01})

	if got := p.EgressControl(); got != EgressControlDefault {
		t.Errorf("EgressControl = %v, want default %v (Weave spawn must not propagate egress)", got, EgressControlDefault)
	}
	if got := p.ECPrFreq(); got != ECPrFreq {
		t.Errorf("ECPrFreq = %v, want default %v (Weave spawn must not propagate ec_pr_freq)", got, ECPrFreq)
	}
	if got := p.ICPrBurstFreq(); got != ICPrBurstFreq {
		t.Errorf("ICPrBurstFreq = %v, want default %v (Weave spawn must not propagate ic_pr_burst_freq)", got, ICPrBurstFreq)
	}
	if got := p.ICPrBurstFreqNew(); got != ICPrBurstFreqNew {
		t.Errorf("ICPrBurstFreqNew = %v, want default %v (Weave spawn must not propagate ic_pr_burst_freq_new)", got, ICPrBurstFreqNew)
	}
}

// TestWeavePeerTypeStatusDetach covers Phase 20 task 4: the spawned peer
// reports Type "WeaveInterfacePeer", is online right after spawn (Status true
// with a nil owner), IsOut reflects the copied OUT flag, and Detach takes it
// offline + detached (WeaveInterface.py:1009,1027-1029,1062-1065).
func TestWeavePeerTypeStatusDetach(t *testing.T) {
	t.Parallel()
	cfg := weaveSpawnTestConfig()
	p := SpawnWeavePeer(nil, cfg, []byte{0x01})

	if got := p.Type(); got != "WeaveInterfacePeer" {
		t.Errorf("Type = %q, want %q", got, "WeaveInterfacePeer")
	}
	if !p.Status() {
		t.Error("Status = false, want true (freshly spawned peer is online)")
	}
	if !p.IsOut() {
		t.Error("IsOut = false, want true (cfg.OUT = true)")
	}
	if p.IsDetached() {
		t.Error("IsDetached = true, want false before Detach")
	}
	if err := p.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if p.Status() {
		t.Error("Status = true after Detach, want false")
	}
	if !p.IsDetached() {
		t.Error("IsDetached = false after Detach, want true")
	}
}
