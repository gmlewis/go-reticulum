// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"
	"time"

	rnscrypto "github.com/gmlewis/go-reticulum/rns/crypto"
)

func TestBaseInterfaceIFACRoundTrip(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)

	cfg := IFACConfig{Enabled: true, NetName: "mesh", NetKey: "secret", Size: 16}
	bi.SetIFACConfig(cfg)

	stored := bi.IFACConfig()
	if stored.NetName != "mesh" || stored.NetKey != "secret" || stored.Size != 16 || !stored.Enabled {
		t.Fatalf("unexpected IFAC config stored: %+v", stored)
	}

	raw := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	outProcessed, err := bi.ApplyIFACOutbound(raw)
	if err != nil {
		t.Fatalf("unexpected outbound IFAC error: %v", err)
	}
	if len(outProcessed) != len(raw)+stored.Size {
		t.Fatalf("outbound IFAC length mismatch: got %v want %v", len(outProcessed), len(raw)+stored.Size)
	}
	if outProcessed[0]&0x80 == 0 {
		t.Fatalf("expected IFAC flag to be set in outbound frame")
	}

	inProcessed, ok := bi.ApplyIFACInbound(outProcessed)
	if !ok {
		t.Fatalf("expected inbound IFAC verification to accept packet")
	}
	if string(inProcessed) != string(raw) {
		t.Fatalf("inbound IFAC round-trip mismatch")
	}
}

func TestBaseInterfaceIFACKeyDerivation(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)
	cfg := IFACConfig{Enabled: true, NetName: "mesh", NetKey: "secret", Size: 16}
	bi.SetIFACConfig(cfg)

	// Expected value from Python script:
	// fb627f692fc06e22193bc67b5f38875b7e238e0b01dba3cc78da71f432012ce7702fd7d32af340d46c0c1bce096430133063d6362b3a54de341355424bfdbeb9
	expectedHex := "fb627f692fc06e22193bc67b5f38875b7e238e0b01dba3cc78da71f432012ce7702fd7d32af340d46c0c1bce096430133063d6362b3a54de341355424bfdbeb9"
	gotKey := fmt.Sprintf("%x", bi.ifacKey)
	if gotKey != expectedHex {
		t.Fatalf("IFAC key derivation mismatch:\ngot:  %v\nwant: %v", gotKey, expectedHex)
	}
}

func TestBaseInterfaceIFACDropsWhenEnabledButFlagMissing(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)
	bi.SetIFACConfig(IFACConfig{Enabled: true, NetName: "mesh", NetKey: "secret", Size: 16})

	if _, ok := bi.ApplyIFACInbound([]byte{0x01, 0x02, 0x03, 0x04}); ok {
		t.Fatalf("expected inbound drop when IFAC is enabled but packet lacks IFAC flag")
	}
}

func TestBaseInterfaceIFACDropsFlaggedWhenDisabled(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)
	bi.SetIFACConfig(IFACConfig{Enabled: false})

	flagged := []byte{0x80, 0x01, 0x02, 0x03}
	if _, ok := bi.ApplyIFACInbound(flagged); ok {
		t.Fatalf("expected inbound drop when IFAC flag is set on non-IFAC interface")
	}
}

func TestBaseInterfaceDiscoveryRoundTrip(t *testing.T) {
	bi := NewBaseInterface("discovery-test", ModeFull, 1000)

	lat := 12.34
	lon := 56.78
	height := 90.12
	freq := 123456789
	bw := 250000
	sf := 7
	cr := 5
	channel := 11
	wantLat := lat
	wantFreq := freq
	cfg := DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        14,
		Name:              "Discovery Node",
		Encrypt:           true,
		ReachableOn:       "example.net",
		PublishIFAC:       true,
		Latitude:          &lat,
		Longitude:         &lon,
		Height:            &height,
		Frequency:         &freq,
		Bandwidth:         &bw,
		SpreadingFactor:   &sf,
		CodingRate:        &cr,
		Channel:           &channel,
		Modulation:        "lora",
	}

	bi.SetDiscoveryConfig(cfg)
	stored := bi.DiscoveryConfig()
	if !stored.SupportsDiscovery || !stored.Discoverable {
		t.Fatalf("unexpected discovery flags: %+v", stored)
	}
	if stored.AnnounceInterval != 6*time.Hour || stored.StampValue != 14 || stored.Name != "Discovery Node" {
		t.Fatalf("unexpected discovery config stored: %+v", stored)
	}
	if stored.ReachableOn != "example.net" || !stored.PublishIFAC || !stored.Encrypt || stored.Modulation != "lora" {
		t.Fatalf("unexpected discovery metadata stored: %+v", stored)
	}
	if stored.Latitude == nil || *stored.Latitude != lat || stored.Longitude == nil || *stored.Longitude != lon {
		t.Fatalf("unexpected discovery coordinates: %+v", stored)
	}
	if stored.Height == nil || *stored.Height != height || stored.Frequency == nil || *stored.Frequency != freq || stored.Bandwidth == nil || *stored.Bandwidth != bw {
		t.Fatalf("unexpected discovery radio values: %+v", stored)
	}
	if stored.SpreadingFactor == nil || *stored.SpreadingFactor != sf || stored.CodingRate == nil || *stored.CodingRate != cr || stored.Channel == nil || *stored.Channel != channel {
		t.Fatalf("unexpected discovery radio values: %+v", stored)
	}

	*cfg.Latitude = 0
	*cfg.Frequency = 0
	if *stored.Latitude != wantLat || *stored.Frequency != wantFreq {
		t.Fatalf("stored discovery config should not alias caller pointers: %+v", stored)
	}
}

func TestBaseInterfaceAutoconnectRoundTrip(t *testing.T) {
	bi := NewBaseInterface("auto-test", ModeFull, 1000)
	hash := []byte{0xaa, 0xbb, 0xcc}
	bi.SetAutoconnect(hash, "deadbeef")

	hash[0] = 0
	gotHash := bi.AutoconnectHash()
	if len(gotHash) != 3 || gotHash[0] != 0xaa || gotHash[1] != 0xbb || gotHash[2] != 0xcc {
		t.Fatalf("AutoconnectHash()=%x want aabbcc", gotHash)
	}
	if gotSrc := bi.AutoconnectSource(); gotSrc != "deadbeef" {
		t.Fatalf("AutoconnectSource()=%q want %q", gotSrc, "deadbeef")
	}

	gotHash[1] = 0
	if again := bi.AutoconnectHash(); again[1] != 0xbb {
		t.Fatalf("AutoconnectHash() returned aliasing slice: %x", again)
	}
}

// TestInterfaceContractDefaults asserts the Phase 1 interface-contract
// accessors return the RNS 1.4.2 defaults (Interface.__init__: gravity=0,
// recursive_prs=False, announces_from_internal=True, announces_to_internal=None)
// and that overridden values round-trip through the setters.
func TestInterfaceContractDefaults(t *testing.T) {
	t.Parallel()

	bi := NewBaseInterface("contract", ModeFull, 1000)

	if got := bi.Gravity(); got != 0 {
		t.Fatalf("default Gravity() = %v, want 0", got)
	}
	if got := bi.RecursivePrs(); got {
		t.Fatalf("default RecursivePrs() = %v, want false", got)
	}
	if got := bi.AnnouncesFromInternal(); !got {
		t.Fatalf("default AnnouncesFromInternal() = %v, want true", got)
	}
	if got := bi.AnnouncesToInternal(); got != nil {
		t.Fatalf("default AnnouncesToInternal() = %v, want nil", got)
	}
}

func TestInterfaceContractOverride(t *testing.T) {
	t.Parallel()

	bi := NewBaseInterface("contract-override", ModeBoundary, 1000)
	bi.SetGravity(7)
	bi.SetRecursivePrs(true)
	bi.SetAnnouncesFromInternal(false)
	allow := true
	bi.SetAnnouncesToInternal(&allow)

	if got := bi.Gravity(); got != 7 {
		t.Fatalf("Gravity() = %v, want 7", got)
	}
	if got := bi.RecursivePrs(); !got {
		t.Fatalf("RecursivePrs() = %v, want true", got)
	}
	if got := bi.AnnouncesFromInternal(); got {
		t.Fatalf("AnnouncesFromInternal() = %v, want false", got)
	}
	got := bi.AnnouncesToInternal()
	if got == nil || *got != true {
		t.Fatalf("AnnouncesToInternal() = %v, want &true", got)
	}

	// Setting back to nil must clear the pointer.
	bi.SetAnnouncesToInternal(nil)
	if got := bi.AnnouncesToInternal(); got != nil {
		t.Fatalf("AnnouncesToInternal() after nil set = %v, want nil", got)
	}
}

// TestDefaultIFACSizePerType asserts concrete interface types expose their
// RNS 1.4.2 class-level DEFAULT_IFAC_SIZE (Backbone/TCP/UDP/Auto/I2P/Weave=16,
// KISS/AX25KISS/RNode/RNodeMulti/Serial/Pipe=8) via DefaultIFACSize(), instead
// of a single hardcoded 16. Golden values captured from
// RNS/Interfaces/*.py DEFAULT_IFAC_SIZE class attributes.
func TestDefaultIFACSizePerType(t *testing.T) {
	t.Parallel()

	backbone := NewDormantBackboneClientInterface("backbone-default", nil)
	t.Cleanup(func() { _ = backbone.Detach() })
	bbDefault, ok := backbone.(interface{ DefaultIFACSize() int })
	if !ok {
		t.Fatalf("BackboneClientInterface does not expose DefaultIFACSize()")
	}
	if got := bbDefault.DefaultIFACSize(); got != 16 {
		t.Fatalf("BackboneClientInterface DefaultIFACSize() = %v, want 16", got)
	}

	pipe := NewPipeInterface("pipe-default", nil)
	t.Cleanup(func() { _ = pipe.Detach() })
	if got := pipe.DefaultIFACSize(); got != 8 {
		t.Fatalf("PipeInterface DefaultIFACSize() = %v, want 8", got)
	}
}

// TestSetIFACConfigUsesTypeDefault verifies SetIFACConfig defaults an unset
// IFAC size (Size < 1) to the interface type's DefaultIFACSize() rather than a
// hardcoded 16. A Pipe interface (DEFAULT_IFAC_SIZE=8) must get size 8.
func TestSetIFACSizeUsesTypeDefault(t *testing.T) {
	t.Parallel()

	pipe := NewPipeInterface("pipe-ifac-default", nil)
	t.Cleanup(func() { _ = pipe.Detach() })
	pipe.SetIFACConfig(IFACConfig{Enabled: true, NetName: "mesh"})
	if got := pipe.IFACConfig(); got.Size != 8 {
		t.Fatalf("Pipe IFACConfig.Size = %v, want 8 (type default)", got)
	}

	backbone := NewDormantBackboneClientInterface("backbone-ifac-default", nil)
	t.Cleanup(func() { _ = backbone.Detach() })
	if setter, ok := backbone.(interface{ SetIFACConfig(IFACConfig) }); ok {
		setter.SetIFACConfig(IFACConfig{Enabled: true, NetName: "mesh"})
	}
	bbCfg, ok := backbone.(interface{ IFACConfig() IFACConfig })
	if !ok {
		t.Fatalf("BackboneClientInterface does not expose IFACConfig()")
	}
	if got := bbCfg.IFACConfig(); got.Size != 16 {
		t.Fatalf("Backbone IFACConfig.Size = %v, want 16 (type default)", got)
	}
}

// TestInterfaceGetHashMemoized verifies MemoizedHash memoizes the interface
// identity hash (Python Interface.get_hash, RNS/Interfaces/Interface.py:144-146):
// the SHA-256 of "{Type}[{Name}]" is computed once and cached, not recomputed on
// every call. The golden value is the SHA-256 of "PipeInterface[<name>]"
// matching Python's RNS.Identity.full_hash(str(self)).
func TestInterfaceGetHashMemoized(t *testing.T) {
	t.Parallel()

	pipe := NewPipeInterface("pipe-hash-test", nil)
	t.Cleanup(func() { _ = pipe.Detach() })

	want := sha256.Sum256(fmt.Appendf(nil, "%v[%v]", pipe.Type(), pipe.Name()))

	// compute counts how many times the underlying hash computation runs. It
	// must run exactly once across both calls; the second call must hit the cache.
	var computes int
	compute := func() []byte {
		computes++
		return rnscrypto.SHA256(fmt.Appendf(nil, "%v[%v]", pipe.Type(), pipe.Name()))
	}

	h1 := pipe.MemoizedHash(compute)
	if len(h1) != len(want) {
		t.Fatalf("MemoizedHash len = %v, want %v", len(h1), len(want))
	}
	if !bytes.Equal(h1, want[:]) {
		t.Fatalf("MemoizedHash = %x, want %x", h1, want)
	}

	h2 := pipe.MemoizedHash(compute)
	if computes != 1 {
		t.Fatalf("hash computation ran %v times, want 1 (memoized)", computes)
	}
	if !bytes.Equal(h2, want[:]) {
		t.Fatalf("second MemoizedHash = %x, want %x", h2, want)
	}
	if reflect.ValueOf(h1).Pointer() != reflect.ValueOf(h2).Pointer() {
		t.Fatalf("memoized hash returned a different backing array on second call")
	}
}
