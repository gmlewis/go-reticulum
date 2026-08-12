// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"testing"
	"time"
)

// TestRNodeInterfaceTypeStrGolden covers Phase 21 task 1: the radio-chip type
// code -> chip-family mapping matches Python's KISS.interface_type_to_str
// (RNodeMultiInterface.py:118-128) exactly, including the "SX127X" fallback for
// unknown codes. Golden captured from RNS 1.4.2.
func TestRNodeInterfaceTypeStrGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code byte
		want string
	}{
		{0x00, "SX127X"}, // SX127X
		{0x01, "SX127X"}, // SX1276
		{0x02, "SX127X"}, // SX1278
		{0x10, "SX126X"}, // SX126X
		{0x11, "SX126X"}, // SX1262
		{0x20, "SX128X"}, // SX128X
		{0x21, "SX128X"}, // SX1280
		{0xFF, "SX127X"}, // unknown -> fallback
	}
	for _, tc := range cases {
		if got := RNodeInterfaceTypeStr(tc.code); got != tc.want {
			t.Errorf("RNodeInterfaceTypeStr(0x%02X) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

// TestRNodeMultiDetectCommandGolden covers Phase 21 task 1: the detect command
// RNodeMultiInterface sends at startup is the exact 16-byte KISS frame sequence
// probing detect + firmware + platform + MCU + interfaces. Golden hex captured
// from RNS 1.4.2: c00873c05000c04800c04900c07100c0.
func TestRNodeMultiDetectCommandGolden(t *testing.T) {
	t.Parallel()
	cmd := RNodeMultiDetectCommand()
	wantHex := "c00873c05000c04800c04900c07100c0"
	if len(cmd) != 16 {
		t.Fatalf("detect command len = %d, want 16", len(cmd))
	}
	if got := hexBytes(cmd); got != wantHex {
		t.Errorf("detect command = %s, want %s", got, wantHex)
	}
}

// hexBytes returns the lowercase hex of a byte slice (test-only helper).
func hexBytes(b []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[2*i] = hexd[v>>4]
		out[2*i+1] = hexd[v&0x0f]
	}
	return string(out)
}

// TestRNodeMultiInterfacesParserGolden covers Phase 21 task 1: feeding a mock
// serial detect response through rnodeMultiSerialParser populates the
// interface-type list with one entry per virtual port and sets the detected
// flag. The mock response carries a CMD_DETECT frame (DETECT_RESP) plus a
// CMD_INTERFACES frame with two 2-byte descriptors (vport0=SX1278 0x02,
// vport1=SX1262 0x11). Golden: detected=true, interfaceTypes=["SX127X","SX126X"].
func TestRNodeMultiInterfacesParserGolden(t *testing.T) {
	t.Parallel()
	var p rnodeMultiSerialParser
	// [FEND CMD_DETECT DETECT_RESP FEND] then
	// [FEND CMD_INTERFACES desc0(2 bytes) desc1(2 bytes) FEND].
	resp := []byte{
		KISSFend, KISSCmdDetect, KISSDetectResp, KISSFend,
		KISSFend, KISSCmdInterfaces, 0x00, 0x02, 0x01, 0x11, KISSFend,
	}
	for _, b := range resp {
		p.feedByte(b)
	}
	if !p.Detected() {
		t.Error("Detected = false, want true")
	}
	got := p.InterfaceTypes()
	want := []string{"SX127X", "SX126X"}
	if len(got) != len(want) {
		t.Fatalf("InterfaceTypes = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("InterfaceTypes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRNodeMultiParserDetectClear covers Phase 21 task 1: a CMD_DETECT frame
// whose data byte is not DETECT_RESP clears the detected flag
// (RNodeMultiInterface.py:828-831, else branch sets detected=False).
func TestRNodeMultiParserDetectClear(t *testing.T) {
	t.Parallel()
	var p rnodeMultiSerialParser
	for _, b := range []byte{KISSFend, KISSCmdDetect, KISSDetectResp, KISSFend} {
		p.feedByte(b)
	}
	if !p.Detected() {
		t.Fatal("Detected should be true after DETECT_RESP")
	}
	for _, b := range []byte{KISSFend, KISSCmdDetect, 0x00, KISSFend} {
		p.feedByte(b)
	}
	if p.Detected() {
		t.Error("Detected = true after a non-DETECT_RESP byte, want false")
	}
}

// TestRNodeMultiSpawnAndDespawn covers Phase 21 task 1: given the device's
// reported interface types and a set of enabled subinterface configs, the
// spawner creates one subinterface per config at its vport (registered via
// addInterface, clients incremented), a config whose vport is not reported
// returns the Python-faithful "Virtual port ... does not exist" error, and
// Despawn removes every spawned subinterface via removeInterface and resets
// the slots + clients counter. Golden behavior captured from RNS 1.4.2's
// configure_device + teardown_subinterfaces.
func TestRNodeMultiSpawnAndDespawn(t *testing.T) {
	t.Parallel()
	state := NewRNodeMultiSpawnState()
	interfaceTypes := []string{"SX127X", "SX126X"}

	var added, removed []Interface
	addInterface := func(iface Interface) { added = append(added, iface) }
	removeInterface := func(iface Interface) { removed = append(removed, iface) }

	cfgs := []RNodeMultiSubinterfaceConfig{
		{Name: "sub0", Enabled: true, Vport: 0, Frequency: 433050000, Bandwidth: 125000, TXPower: 10, SpreadingFactor: 7, CodingRate: 5},
		{Name: "sub1", Enabled: true, Vport: 1, Frequency: 433150000, Bandwidth: 125000, TXPower: 10, SpreadingFactor: 7, CodingRate: 5},
		{Name: "sub_disabled", Enabled: false, Vport: 0},
	}
	if err := SpawnRNodeSubinterfaces(state, nil, cfgs, interfaceTypes, addInterface); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := state.Clients(); got != 2 {
		t.Errorf("Clients = %d, want 2", got)
	}
	if len(added) != 2 {
		t.Errorf("addInterface called %d times, want 2", len(added))
	}
	sub0 := state.SubinterfaceAt(0)
	sub1 := state.SubinterfaceAt(1)
	if sub0 == nil || sub1 == nil {
		t.Fatalf("sub0/sub1 not spawned: %v %v", sub0, sub1)
	}
	if sub0.Index != 0 || sub0.InterfaceType != "SX127X" {
		t.Errorf("sub0 = {Index:%d Type:%s}, want {0, SX127X}", sub0.Index, sub0.InterfaceType)
	}
	if sub1.Index != 1 || sub1.InterfaceType != "SX126X" {
		t.Errorf("sub1 = {Index:%d Type:%s}, want {1, SX126X}", sub1.Index, sub1.InterfaceType)
	}
	if sub0.Name() != "sub0" || sub1.Name() != "sub1" {
		t.Errorf("names = %q %q, want sub0 sub1", sub0.Name(), sub1.Name())
	}
	if sub0.Type() != "RNodeSubInterface" {
		t.Errorf("sub0.Type = %q, want RNodeSubInterface", sub0.Type())
	}
	if !sub0.Status() || !sub0.IsOut() {
		t.Errorf("sub0 online/out = %v %v, want true true", sub0.Status(), sub0.IsOut())
	}

	// Despawn removes both, resets slots + clients.
	DespawnRNodeSubinterfaces(state, removeInterface)
	if got := state.Clients(); got != 0 {
		t.Errorf("Clients after despawn = %d, want 0", got)
	}
	if len(removed) != 2 {
		t.Errorf("removeInterface called %d times, want 2", len(removed))
	}
	if state.SubinterfaceAt(0) != nil || state.SubinterfaceAt(1) != nil {
		t.Error("slots not reset to nil after despawn")
	}
}

// TestRNodeMultiSpawnMissingVport covers Phase 21 task 1: a config whose vport
// is beyond the device's reported interface-type list returns the Python-
// faithful "Virtual port ... does not exist" error
// (RNodeMultiInterface.py:380-382). Golden message captured from RNS 1.4.2.
func TestRNodeMultiSpawnMissingVport(t *testing.T) {
	t.Parallel()
	state := NewRNodeMultiSpawnState()
	interfaceTypes := []string{"SX127X"} // only vport 0 exists
	cfgs := []RNodeMultiSubinterfaceConfig{
		{Name: "subX", Enabled: true, Vport: 5},
	}
	err := SpawnRNodeSubinterfaces(state, nil, cfgs, interfaceTypes, nil)
	if err == nil {
		t.Fatal("Spawn succeeded for a missing vport, want error")
	}
	want := `Virtual port "5" for subinterface subX does not exist`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if state.Clients() != 0 {
		t.Errorf("Clients = %d after failed spawn, want 0", state.Clients())
	}
}

// TestRNodeMultiSpawnDedupSkip covers Phase 21 task 1 (and previews task 2): a
// second spawn for a vport that already holds a subinterface is a no-op for
// that slot (Python's add_peer-style dedup is the transport's job; the spawner
// itself never overwrites a live slot). The clients counter is not incremented
// for the skipped slot.
func TestRNodeMultiSpawnDedupSkip(t *testing.T) {
	t.Parallel()
	state := NewRNodeMultiSpawnState()
	interfaceTypes := []string{"SX127X", "SX126X"}
	var added []Interface
	add := func(i Interface) { added = append(added, i) }
	cfgs := []RNodeMultiSubinterfaceConfig{{Name: "sub0", Enabled: true, Vport: 0}}
	if err := SpawnRNodeSubinterfaces(state, nil, cfgs, interfaceTypes, add); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	first := state.SubinterfaceAt(0)
	if first == nil {
		t.Fatal("first spawn did not populate vport 0")
	}
	// Re-spawn the same vport: the spawner skips (no new client, slot unchanged).
	if err := SpawnRNodeSubinterfaces(state, nil, cfgs, interfaceTypes, add); err != nil {
		t.Fatalf("second spawn: %v", err)
	}
	if state.Clients() != 1 {
		t.Errorf("Clients = %d after re-spawn of same vport, want 1", state.Clients())
	}
	if state.SubinterfaceAt(0) != first {
		t.Error("slot was overwritten by re-spawn; want the original subinterface retained")
	}
	if len(added) != 1 {
		t.Errorf("addInterface called %d times, want 1 (re-spawn skipped)", len(added))
	}
}

// TestRNodeMultiDetectCommandRoundTrip covers Phase 21 task 1: the detect
// command is itself a valid KISS byte sequence that the parser can consume
// (the device echoes/answers it); feeding it back through the parser sets the
// in-frame state correctly and the CMD_INTERFACES probe segment parses to zero
// interfaces (no descriptors). This pins the command<->parser symmetry.
func TestRNodeMultiDetectCommandRoundTrip(t *testing.T) {
	t.Parallel()
	cmd := RNodeMultiDetectCommand()
	if !bytes.HasPrefix(cmd, []byte{KISSFend}) || !bytes.HasSuffix(cmd, []byte{KISSFend}) {
		t.Fatal("detect command must begin and end with FEND")
	}
	var p rnodeMultiSerialParser
	for _, b := range cmd {
		p.feedByte(b)
	}
	// The detect command itself does not contain DETECT_RESP, so the probe
	// does not self-mark detected; interface types remain empty.
	if p.Detected() {
		t.Error("probe command should not self-mark Detected")
	}
	if len(p.InterfaceTypes()) != 0 {
		t.Errorf("InterfaceTypes = %v, want empty (probe has no descriptors)", p.InterfaceTypes())
	}
}

// TestRNodeMultiRegistryDedupGolden covers Phase 21 task 2: the canonical
// registry routes the dynamic spawn/despawn through the transport's
// register/remove with the exact dedup semantics of Python's
// Transport.add_interface / Transport.remove_interface (Transport.py:438-451):
// a repeated Add of the same interface is a no-op (the interface appears at
// most once), and a Remove of an absent interface is a no-op. Golden behavior
// captured from RNS 1.4.2. Real spawned *RNodeMultiSubSpawn values (which
// implement Interface) are used so the registry is exercised with the same
// interface type the spawner registers in production.
func TestRNodeMultiRegistryDedupGolden(t *testing.T) {
	t.Parallel()
	reg := NewRNodeMultiRegistry()

	// Produce real Interface values via the spawner (a, b, c, d); d is never
	// registered and stands in for an absent interface.
	state := NewRNodeMultiSpawnState()
	interfaceTypes := []string{"SX127X", "SX126X", "SX128X", "SX127X"}
	cfgs := []RNodeMultiSubinterfaceConfig{
		{Name: "a", Enabled: true, Vport: 0},
		{Name: "b", Enabled: true, Vport: 1},
		{Name: "c", Enabled: true, Vport: 2},
		{Name: "d", Enabled: true, Vport: 3},
	}
	if err := SpawnRNodeSubinterfaces(state, nil, cfgs, interfaceTypes, nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	a, b, c, absent := Interface(state.SubinterfaceAt(0)), Interface(state.SubinterfaceAt(1)), Interface(state.SubinterfaceAt(2)), Interface(state.SubinterfaceAt(3))

	// Add A once -> count 1.
	reg.Add(a)
	if got := reg.Count(); got != 1 {
		t.Fatalf("Count after Add(a) = %d, want 1", got)
	}
	// Re-add A: dedup keeps it at 1 (Python "if not interface in ...: append").
	reg.Add(a)
	if got := reg.Count(); got != 1 {
		t.Errorf("Count after re-Add(a) = %d, want 1 (dedup)", got)
	}
	// Add B and c -> count 3, snapshot holds all three in insertion order.
	reg.Add(b)
	reg.Add(c)
	if got := reg.Count(); got != 3 {
		t.Errorf("Count after Add(b)+Add(c) = %d, want 3", got)
	}
	if snaps := reg.Interfaces(); len(snaps) != 3 || snaps[0] != a || snaps[1] != b || snaps[2] != c {
		t.Errorf("Interfaces() = %v, want [a b c] in insertion order", snaps)
	}
	// Remove an absent interface (d was never registered): no-op, count stays 3.
	reg.Remove(absent)
	if got := reg.Count(); got != 3 {
		t.Errorf("Count after Remove(absent) = %d, want 3 (no-op)", got)
	}
	// Remove A: count 2, A gone, B and c retained.
	reg.Remove(a)
	if got := reg.Count(); got != 2 {
		t.Errorf("Count after Remove(a) = %d, want 2", got)
	}
	for _, existing := range reg.Interfaces() {
		if existing == a {
			t.Error("a still present after Remove(a)")
		}
	}
	// Remove A again: no-op (already gone), count stays 2.
	reg.Remove(a)
	if got := reg.Count(); got != 2 {
		t.Errorf("Count after re-Remove(a) = %d, want 2 (no-op)", got)
	}
	// Remove B and c -> empty.
	reg.Remove(b)
	reg.Remove(c)
	if got := reg.Count(); got != 0 {
		t.Errorf("Count after removing all = %d, want 0", got)
	}
}

// TestRNodeMultiSpawnViaRegistryDedupOnRespawn covers Phase 21 task 2: when the
// dynamic spawn/despawn is routed through the canonical registry, a re-spawn
// of the same vport set does not duplicate entries in the registry. The
// spawner's slot-level skip keeps the SAME subinterface object across
// re-spawns (subinterfaces[vport] != nil -> skip), so the registry's identity
// dedup sees the already-registered interface and leaves it at one entry. This
// is the Phase 21 task 2 parity test: "dedup on re-spawn".
func TestRNodeMultiSpawnViaRegistryDedupOnRespawn(t *testing.T) {
	t.Parallel()
	state := NewRNodeMultiSpawnState()
	interfaceTypes := []string{"SX127X", "SX126X"}
	reg := NewRNodeMultiRegistry()
	cfgs := []RNodeMultiSubinterfaceConfig{
		{Name: "sub0", Enabled: true, Vport: 0},
		{Name: "sub1", Enabled: true, Vport: 1},
	}

	// First spawn: routes each addInterface through the canonical registry.
	if err := SpawnRNodeSubinterfacesRegistered(state, nil, cfgs, interfaceTypes, reg); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if got, want := state.Clients(), 2; got != want {
		t.Fatalf("Clients after first spawn = %d, want %d", got, want)
	}
	if got, want := reg.Count(), 2; got != want {
		t.Fatalf("registry Count after first spawn = %d, want %d", got, want)
	}
	first := reg.Interfaces()

	// Re-spawn the same configs: slot-level skip keeps the live subinterfaces,
	// so no new addInterface calls fire and the registry is unchanged.
	if err := SpawnRNodeSubinterfacesRegistered(state, nil, cfgs, interfaceTypes, reg); err != nil {
		t.Fatalf("re-spawn: %v", err)
	}
	if got, want := state.Clients(), 2; got != want {
		t.Errorf("Clients after re-spawn = %d, want %d (no new spawns)", got, want)
	}
	if got, want := reg.Count(), 2; got != want {
		t.Errorf("registry Count after re-spawn = %d, want %d (dedup)", got, want)
	}
	// The registry holds the SAME interface objects as before the re-spawn.
	second := reg.Interfaces()
	if len(first) != len(second) {
		t.Fatalf("registry snapshot len changed: first=%d second=%d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("registry entry %d changed identity after re-spawn (dedup should retain same object)", i)
		}
	}

	// Despawn routes each removeInterface through the canonical registry,
	// clearing it back to zero.
	DespawnRNodeSubinterfacesRegistered(state, reg)
	if got, want := state.Clients(), 0; got != want {
		t.Errorf("Clients after despawn = %d, want %d", got, want)
	}
	if got, want := reg.Count(), 0; got != want {
		t.Errorf("registry Count after despawn = %d, want %d", got, want)
	}
	if state.SubinterfaceAt(0) != nil || state.SubinterfaceAt(1) != nil {
		t.Error("slots not reset to nil after despawn")
	}
}

// TestRNodeMultiPrAggregation covers Phase 21 task 3: a spawned subinterface's
// received_path_request / sent_path_request events (Interface.py:267-275)
// propagate up to the parent RNodeMultiInterface's aggregated PR frequency
// tracking (RNodeMultiInterface.py:552-555, ip_freq_deque / op_freq_deque
// appended only on from_spawned=True). The Go wiring sets each spawned
// subinterface's parentInterface to the state's PR aggregator, so invoking the
// spawned-peer PR hook advances the parent's PR counter. Golden behavior
// captured from RNS 1.1.5 / 1.4.2.
func TestRNodeMultiPrAggregation(t *testing.T) {
	t.Parallel()
	state := NewRNodeMultiSpawnState()
	interfaceTypes := []string{"SX127X", "SX126X"}
	cfgs := []RNodeMultiSubinterfaceConfig{
		{Name: "sub0", Enabled: true, Vport: 0},
		{Name: "sub1", Enabled: true, Vport: 1},
	}
	if err := SpawnRNodeSubinterfaces(state, nil, cfgs, interfaceTypes, nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sub0 := state.SubinterfaceAt(0)
	sub1 := state.SubinterfaceAt(1)
	if sub0 == nil || sub1 == nil {
		t.Fatalf("subs not spawned: %v %v", sub0, sub1)
	}

	// Parent PR counters start at zero.
	if got := state.IncomingPrCount(); got != 0 {
		t.Fatalf("IncomingPrCount before any PR = %d, want 0", got)
	}
	if got := state.OutgoingPrCount(); got != 0 {
		t.Fatalf("OutgoingPrCount before any PR = %d, want 0", got)
	}

	// Invoke the spawned-peer incoming PR hook (deterministic now): the event
	// propagates to the parent's aggregated incoming PR counter.
	now := time.Unix(1_000_000, 0)
	sub0.receivedPathRequestAt(now, false)
	if got := state.IncomingPrCount(); got != 1 {
		t.Errorf("IncomingPrCount after 1 sub0 PR = %d, want 1", got)
	}
	// A PR from a different spawned subinterface also aggregates into the
	// same parent counter (the aggregation is across all spawned peers).
	sub1.receivedPathRequestAt(now, false)
	if got := state.IncomingPrCount(); got != 2 {
		t.Errorf("IncomingPrCount after sub0+sub1 PR = %d, want 2 (aggregated)", got)
	}
	// A second PR from sub0 advances the parent counter again.
	sub0.receivedPathRequestAt(now, false)
	if got := state.IncomingPrCount(); got != 3 {
		t.Errorf("IncomingPrCount after 3 PRs = %d, want 3", got)
	}

	// The outgoing PR hook aggregates into the parent's outgoing counter.
	sub0.sentPathRequestAt(now, false)
	sub1.sentPathRequestAt(now, false)
	if got := state.OutgoingPrCount(); got != 2 {
		t.Errorf("OutgoingPrCount after 2 sent PRs = %d, want 2", got)
	}
	// Incoming counter is untouched by outgoing events.
	if got := state.IncomingPrCount(); got != 3 {
		t.Errorf("IncomingPrCount changed after sent PRs = %d, want 3", got)
	}
}

// TestRNodeMultiPrAggregatorFrequency covers Phase 21 task 3: the parent's
// aggregated PR frequency (Python incoming_pr_frequency / outgoing_pr_frequency
// over the aggregated deque, Interface.py:299-319) is computed from the
// spawned-peer samples the aggregator collected. With enough samples within
// the PR decay window the frequency is the sample count over the span. This
// pins that the aggregator is a real frequency tracker, not just a counter.
func TestRNodeMultiPrAggregatorFrequency(t *testing.T) {
	t.Parallel()
	state := NewRNodeMultiSpawnState()
	interfaceTypes := []string{"SX127X"}
	if err := SpawnRNodeSubinterfaces(state, nil, []RNodeMultiSubinterfaceConfig{{Name: "sub0", Enabled: true, Vport: 0}}, interfaceTypes, nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sub := state.SubinterfaceAt(0)

	// Seed the parent aggregator with enough samples across a 0.5s span to
	// clear the IC_DEQUE_MIN_SAMPLE gate and produce a nonzero frequency.
	// Samples land at t, t+0.1, ... t+0.5 (6 samples).
	base := time.Unix(2_000_000, 0)
	for i := range 6 {
		sub.receivedPathRequestAt(base.Add(time.Duration(i)*100*time.Millisecond), false)
	}
	if got := state.IncomingPrCount(); got != 6 {
		t.Fatalf("IncomingPrCount = %d, want 6", got)
	}
	// At base+0.5s the span is 0.5s, n=6 -> 12 Hz.
	freq := state.IncomingPrFrequencyAt(base.Add(500 * time.Millisecond))
	if freq == 0 {
		t.Fatal("IncomingPrFrequencyAt = 0, want nonzero (aggregator should compute frequency from spawned-peer samples)")
	}
	want := 12.0
	if freq < want-0.01 || freq > want+0.01 {
		t.Errorf("IncomingPrFrequencyAt = %v, want ~%v", freq, want)
	}
}
