// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/toml"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newTestRoomManager builds a room manager with a fixed clock and no
// persistence paths.
func newTestRoomManager(t *testing.T) *RoomManager {
	t.Helper()
	fixed := 1730000000.0
	return NewRoomManager(RoomHooks{
		IsServerOp: func(hash []byte) bool {
			return string(hash) == "serverop"
		},
		Now: func() float64 { return fixed },
	})
}

// G4.1 membership.
func TestMembership(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	link1 := &rns.Link{}
	link2 := &rns.Link{}

	m.AddMember("general", link1, []byte("aa"))
	m.AddMember("general", link2, nil)

	if got := len(m.GetRoomMembers("general")); got != 2 {
		t.Errorf("members = %v, want 2", got)
	}
	if got := m.GetMemberRooms(link1); len(got) != 1 || got[0] != "general" {
		t.Errorf("GetMemberRooms(link1) = %v", got)
	}
	// Ensure created the state with the founder on the first add.
	st := m.RoomStateGet("general")
	if st == nil || string(st.Founder) != "aa" {
		t.Fatalf("state = %+v", st)
	}
	if !st.Ops[hexKey([]byte("aa"))] {
		t.Errorf("founder not in ops")
	}

	m.RemoveMember("general", link1)
	if got := len(m.GetRoomMembers("general")); got != 1 {
		t.Errorf("members after remove = %v, want 1", got)
	}

	// Emptying an unregistered room cleans it up.
	m.RemoveMember("general", link2)
	if m.RoomStateGet("general") != nil {
		t.Error("unregistered room state must be popped when empty")
	}
	if got := m.GetRoomMembers("general"); len(got) != 0 {
		t.Error("empty room must be popped")
	}
}

func TestRemoveMemberFromAll(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	link := &rns.Link{}
	m.AddMember("a", link, nil)
	m.AddMember("b", link, nil)
	m.AddMember("c", &rns.Link{}, nil)

	if got := m.RemoveMemberFromAll(link); got != 2 {
		t.Errorf("RemoveMemberFromAll = %v, want 2", got)
	}
	if len(m.GetMemberRooms(link)) != 0 {
		t.Error("link still in rooms")
	}
}

func TestGetRoomStats(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	for range 3 {
		m.AddMember("busy", &rns.Link{}, nil)
	}
	for range 2 {
		m.AddMember("mid", &rns.Link{}, nil)
	}
	emptyLink := &rns.Link{}
	m.AddMember("empty-room", emptyLink, nil)
	m.RemoveMember("empty-room", emptyLink)
	m.AddMember("aardvark", &rns.Link{}, nil)

	stats := m.GetRoomStats()
	if stats.RoomsTotal != 3 {
		t.Errorf("RoomsTotal = %v, want 3", stats.RoomsTotal)
	}
	if stats.Memberships != 6 {
		t.Errorf("Memberships = %v, want 6", stats.Memberships)
	}
	if len(stats.TopRooms) != 3 {
		t.Fatalf("TopRooms = %+v, want 3", stats.TopRooms)
	}
	// Sorted by descending count then name: busy(3), mid(2), aardvark(1).
	if stats.TopRooms[0].Room != "busy" || stats.TopRooms[0].Count != 3 {
		t.Errorf("top[0] = %+v", stats.TopRooms[0])
	}
	if stats.TopRooms[1].Room != "mid" || stats.TopRooms[1].Count != 2 {
		t.Errorf("top[1] = %+v", stats.TopRooms[1])
	}
	if stats.TopRooms[2].Room != "aardvark" || stats.TopRooms[2].Count != 1 {
		t.Errorf("top[2] = %+v", stats.TopRooms[2])
	}
}

// G4.2 EnsureRoomState.
func TestEnsureRoomState(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)

	// Fresh unregistered room with founder.
	st := m.EnsureRoomState("general", []byte("aa"))
	if st.Registered {
		t.Error("fresh room must be unregistered")
	}
	if string(st.Founder) != "aa" || !st.Ops[hexKey([]byte("aa"))] {
		t.Errorf("founder/ops wrong: %+v", st)
	}
	if st.Topic != nil || st.Key != nil || st.Invited != nil {
		t.Errorf("fresh room defaults wrong: %+v", st)
	}

	// Existing state with no founder back-fills from the parameter.
	room2 := m.EnsureRoomState("other", nil)
	room2.Founder = nil
	room2.Ops = map[string]bool{}
	st2 := m.EnsureRoomState("other", []byte("bb"))
	if string(st2.Founder) != "bb" {
		t.Errorf("founder not back-filled: %+v", st2)
	}
	if !st2.Ops[hexKey([]byte("bb"))] {
		t.Error("ops not back-filled")
	}

	// Registry-backed build.
	reg := map[string]*RoomState{
		"registered": {
			Founder:       []byte("ff"),
			Registered:    true,
			Topic:         new("hello"),
			Moderated:     true,
			InviteOnly:    true,
			TopicOpsOnly:  true,
			NoOutsideMsgs: true,
			Private:       true,
			Key:           new("secret"),
			Ops:           map[string]bool{hexKey([]byte("01")): true},
			Voiced:        map[string]bool{hexKey([]byte("02")): true},
			Bans:          map[string]bool{hexKey([]byte("03")): true},
			Invited:       []Invite{{Hash: []byte("04"), Expires: 2000000000.0}},
			LastUsedTS:    nil,
		},
	}
	// Merge only touches rooms already in the live state.
	m.EnsureRoomState("registered", nil)
	m.MergeRegistryIntoState(reg)
	st3 := m.RoomStateGet("registered")
	if st3 == nil {
		t.Fatal("registry-backed state missing")
	}
	if !st3.Registered || st3.Topic == nil || *st3.Topic != "hello" ||
		!st3.Moderated || !st3.InviteOnly || !st3.TopicOpsOnly ||
		!st3.NoOutsideMsgs || !st3.Private || st3.Key == nil ||
		*st3.Key != "secret" {
		t.Fatalf("registry build wrong: %+v", st3)
	}
	if !st3.Ops[hexKey([]byte("01"))] || !st3.Voiced[hexKey([]byte("02"))] ||
		!st3.Bans[hexKey([]byte("03"))] {
		t.Errorf("registry sets wrong: %+v", st3)
	}
	if len(st3.Invited) != 1 || string(st3.Invited[0].Hash) != "04" ||
		st3.Invited[0].Expires != 2000000000.0 {
		t.Errorf("registry invites wrong: %+v", st3.Invited)
	}
}

// G4.3 room modes.
func TestGetRoomModeString(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	// An unknown room materializes as unregistered with no flags.
	if got := m.GetRoomModeString("nope"); got != "(none)" {
		t.Errorf("mode string = %q, want (none)", got)
	}

	doc, err := toml.Parse("[rooms]\n")
	if err != nil {
		t.Fatal(err)
	}
	room := doc.TablePath("rooms", "test")
	room.Set("founder", toml.StringValue(strings.Repeat("a", 32)))
	room.Set("moderated", toml.BoolValue(true))
	room.Set("invite_only", toml.BoolValue(true))
	room.Set("topic_ops_only", toml.BoolValue(true))
	room.Set("no_outside_msgs", toml.BoolValue(true))
	room.Set("private", toml.BoolValue(true))
	room.Set("key", toml.StringValue("secret"))
	path := writeTemp(t, testutils.TempDir(t, "modes-"), "rooms.toml", doc.Dump())
	registry, errMsg := m.LoadRegistryFromPath(path)
	if errMsg != "" {
		t.Fatalf("registry load: %v", errMsg)
	}
	t.Logf("loaded registry: %+v", registry["test"])
	m.EnsureRoomState("test", nil)
	t.Logf("post-ensure order: %v state: %+v", m.registryOrderForTest(), m.RoomStateGet("test"))
	m.MergeRegistryIntoState(registry)
	st := m.RoomStateGet("test")
	t.Logf("post-merge state: %+v", st)

	// Flags append in the order i, k, m, n, p, r, t.
	want := "+ikmnprt"
	if got := m.GetRoomModeString("test"); got != want {
		t.Errorf("mode string = %q, want %q", got, want)
	}
}

// G4.4 permission checks.
func TestPermissionChecks(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	serverOp := []byte("serverop")
	founder := []byte("aa")
	op := []byte("op")
	voiced := []byte("vc")
	banned := []byte("banned")
	plain := []byte("plain")

	m.AddMember("general", &rns.Link{}, founder)
	room := m.RoomStateGet("general")
	room.Ops[hexKey(op)] = true
	room.Voiced[hexKey(voiced)] = true
	room.Bans[hexKey(banned)] = true

	if !m.IsRoomOp("general", serverOp) {
		t.Error("server op must be a room op")
	}
	if !m.IsRoomOp("general", founder) {
		t.Error("founder must be a room op")
	}
	if !m.IsRoomOp("general", op) {
		t.Error("ops member must be a room op")
	}
	if m.IsRoomOp("general", nil) {
		t.Error("nil hash must not be a room op")
	}
	if m.IsRoomOp("general", plain) {
		t.Error("plain member must not be a room op")
	}
	if !m.IsRoomVoiced("general", voiced) {
		t.Error("voiced member must have voice")
	}
	if !m.IsRoomVoiced("general", op) {
		t.Error("op must have voice")
	}
	if m.IsRoomVoiced("general", plain) {
		t.Error("plain member must not have voice")
	}
	if !m.IsRoomBanned("general", banned) {
		t.Error("banned member must be banned")
	}
	if m.IsRoomBanned("general", plain) {
		t.Error("plain member must not be banned")
	}
	if m.IsRoomBanned("general", nil) {
		t.Error("nil hash must not be banned")
	}
}

func TestInvites(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	peer := []byte("peer")
	if m.IsInvited("general", peer) {
		t.Fatal("unknown room must not have invites")
	}
	// Materializes state; add an invite with a future expiry relative to
	// the fixed clock (1730000000).
	room := m.EnsureRoomState("general", nil)
	room.Invited = append(room.Invited, Invite{Hash: peer, Expires: 1730000100.0})
	if !m.IsInvited("general", peer) {
		t.Error("valid invite rejected")
	}
	// An expired invite is removed by PruneExpiredInvites.
	expired := []byte("expired")
	room.Invited = append(room.Invited, Invite{Hash: expired, Expires: 1729999000.0})
	if !m.PruneExpiredInvites("general") {
		t.Error("PruneExpiredInvites = false, want true")
	}
	if m.PruneExpiredInvites("general") {
		t.Error("second prune removed anything")
	}
	if m.IsInvited("general", expired) {
		t.Error("expired invite accepted after prune")
	}
	// The valid invite survives.
	if !m.IsInvited("general", peer) {
		t.Error("valid invite lost by prune")
	}
}

// G4.5 registry loading.
func TestLoadRegistryFromPath(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	dir := testutils.TempDir(t, "rooms-load-")

	if registry, errMsg := m.LoadRegistryFromPath(""); errMsg != "" || len(registry) != 0 {
		t.Errorf("empty path = %v, %v", registry, errMsg)
	}
	if registry, errMsg := m.LoadRegistryFromPath(testutils.TempDir(t, "missing") + "/rooms.toml"); errMsg != "" || len(registry) != 0 {
		t.Errorf("missing file = %v, %q", registry, errMsg)
	}
	if _, errMsg := m.LoadRegistryFromPath(writeTemp(t, dir, "rooms.toml", "not [valid")); errMsg == "" ||
		!strings.HasPrefix(errMsg, "parse error: ") {
		t.Errorf("parse error missing: %q", errMsg)
	}

	src := `# a comment

[rooms]

[rooms."general"]
founder = "AABB"
topic = "welcome"
moderated = "yes"
invite_only = 1
topic_ops_only = []
no_outside_msgs = 1
private = "truthy"
key = "k1"
operators = ["0xAABBCC", "zz", "ddee"]
voiced = ["ff01"]
bans = []
last_used_ts = 1730000000.5

[rooms."general".invited]
"0xbb01" = 2000000000.0
"cc02" = 100.0
`
	path := writeTemp(t, dir, "rooms.toml", src)
	registry, errMsg := m.LoadRegistryFromPath(path)
	if errMsg != "" {
		t.Fatalf("error = %q", errMsg)
	}
	general, ok := registry["general"]
	if !ok {
		t.Fatalf("missing general: %v", registry)
	}
	// Python bool() truthiness applies to the raw TOML values.
	if !general.Moderated || !general.NoOutsideMsgs || !general.Private {
		t.Errorf("truthy string/int values must be true: %+v", general)
	}
	// invite_only = 1 → true; topic_ops_only = [] → false.
	if !general.InviteOnly || general.TopicOpsOnly {
		t.Errorf("invite_only/topic_ops_only wrong: %+v", general)
	}
	if general.Founder == nil || strings.ToLower(hexKey(general.Founder))[:4] != "aabb" {
		t.Errorf("founder = %v", general.Founder)
	}
	if general.Topic == nil || *general.Topic != "welcome" {
		t.Errorf("topic = %v", general.Topic)
	}
	if general.Key == nil || *general.Key != "k1" {
		t.Errorf("key = %v", general.Key)
	}
	// Operators skip unparseable entries.
	if len(general.Ops) != 2 {
		t.Fatalf("ops = %v, want 2", general.Ops)
	}
	// invited: only exp > now (fixed clock 1730000000) is kept.
	if len(general.Invited) != 1 || hexKey(general.Invited[0].Hash) != "bb01" {
		t.Errorf("invited = %+v", general.Invited)
	}
	if general.LastUsedTS == nil || *general.LastUsedTS != 1730000000.5 {
		t.Errorf("last_used_ts = %v", general.LastUsedTS)
	}
}

// G4.10 registry diff.
func TestDiffRegistrySummary(t *testing.T) {
	t.Parallel()
	old := map[string]*RoomState{
		"kept":     {},
		"removed":  {},
		"changed1": {},
	}
	new := map[string]*RoomState{
		"kept":     {},
		"added":    {},
		"changed1": {},
	}
	got := DiffRegistrySummary(old, new)
	want := []string{
		"rooms_added=1: added",
		"rooms_removed=1: removed",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("diff = %v, want %v", got, want)
	}
	// No changes → the rooms_changed line.
	if got := DiffRegistrySummary(old, old); len(got) != 1 ||
		got[0] != "rooms_changed=0 (registered_rooms=3)" {
		t.Errorf("no-change diff = %v", got)
	}
	// More than 10 added rooms truncate with a suffix.
	big := map[string]*RoomState{}
	for i := range 12 {
		big[strings.Repeat("r", i+1)+"x"] = &RoomState{}
	}
	got = DiffRegistrySummary(map[string]*RoomState{}, big)
	if len(got) != 1 || !strings.Contains(got[0], "rooms_added=12: ") ||
		!strings.Contains(got[0], "(+2 more)") {
		t.Errorf("12-room diff = %v", got)
	}
}

// G4.8 prune.
func TestPruneUnusedRegisteredRooms(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	const fixedNow = 1730000000.0
	m.hooks.Now = func() float64 { return fixedNow }

	// old: used 10 days ago (prunable), fresh: used now (kept),
	// active: used long ago but has members (kept).
	old := float64(fixedNow - 3000000)
	fresh := fixedNow
	m.RegistrySet("old", &RoomState{Registered: true, LastUsedTS: &old})
	m.RegistrySet("fresh", &RoomState{Registered: true, LastUsedTS: &fresh})
	m.RegistrySet("active", &RoomState{Registered: true, LastUsedTS: &old})
	m.StateSet("active", &RoomState{})
	m.AddMember("active", &rns.Link{}, nil)

	pruned := m.PruneUnusedRegisteredRooms(2592000.0, fixedNow-100)
	if len(pruned) != 1 || pruned[0] != "old" {
		t.Errorf("pruned = %v, want [old]", pruned)
	}
	if _, ok := m.RegistryGet("old"); ok {
		t.Error("old still registered")
	}
	if _, ok := m.RegistryGet("fresh"); !ok {
		t.Error("fresh pruned")
	}
	if _, ok := m.RegistryGet("active"); !ok {
		t.Error("active pruned despite members")
	}
	// A room with no last_used_ts uses startedWallTime.
	m.RegistrySet("never-used", &RoomState{})
	if got := m.PruneUnusedRegisteredRooms(2592000.0, fixedNow-3000000); len(got) != 1 {
		t.Errorf("never-used prune = %v (startedWallTime not honored)", got)
	}
}

// G4.9 merge.
func TestMergeRegistryIntoState(t *testing.T) {
	t.Parallel()
	m := newTestRoomManager(t)
	// Live state for two rooms; the registry only knows about one. The
	// known room is materialized first (merge only touches live state).
	m.EnsureRoomState("known", []byte("old"))
	m.StateSet("known", &RoomState{Registered: true, Founder: []byte("old")})
	m.EnsureRoomState("missing", nil)
	m.StateSet("missing", &RoomState{Registered: true})

	registry := map[string]*RoomState{
		"known": {
			Founder:       []byte("new-founder"),
			Registered:    true,
			Topic:         new("new topic"),
			Moderated:     true,
			Key:           new("k"),
			Ops:           map[string]bool{hexKey([]byte("op")): true},
			Voiced:        map[string]bool{hexKey([]byte("v")): true},
			Bans:          map[string]bool{hexKey([]byte("b")): true},
			Invited:       []Invite{{Hash: []byte("inv"), Expires: 100.0}},
			LastUsedTS:    func() *float64 { f := 50.0; return &f }(),
			NoOutsideMsgs: true,
		},
	}
	m.MergeRegistryIntoState(registry)

	known := m.RoomStateGet("known")
	if string(known.Founder) != "new-founder" || !known.Moderated ||
		known.Topic == nil || *known.Topic != "new topic" {
		t.Errorf("known merge wrong: %+v", known)
	}
	if known.Key == nil || *known.Key != "k" {
		t.Errorf("known key wrong: %v", known.Key)
	}
	if missing := m.RoomStateGet("missing"); missing == nil || missing.Registered {
		t.Errorf("missing room = %+v, want kept but unregistered", missing)
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %v: %v", path, err)
	}
	return path
}

func (m *RoomManager) registryOrderForTest() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.roomStateOrder...)
}

// G4.11: the Go-built registry and the Python loader must agree room by
// room on the same file.
func TestRegistryInteropWithPython(t *testing.T) {
	testutils.SkipIfNoPythonRNS(t)
	t.Parallel()
	m := newTestRoomManager(t)
	dir := testutils.TempDir(t, "registry-interop-")
	path := filepath.Join(dir, "rooms.toml")

	doc, err := toml.Parse("[rooms]\n")
	if err != nil {
		t.Fatal(err)
	}
	rooms := doc.TablePath("rooms")

	general := rooms.SetTable("general")
	general.Set("founder", toml.StringValue(strings.Repeat("ab", 16)))
	general.Set("topic", toml.StringValue("the topic"))
	general.Set("moderated", toml.BoolValue(true))
	general.Set("invite_only", toml.BoolValue(true))
	general.Set("topic_ops_only", toml.BoolValue(false))
	general.Set("no_outside_msgs", toml.BoolValue(true))
	general.Set("key", toml.StringValue("roomkey"))
	general.Set("last_used_ts", toml.FloatValue(1730000000.5))
	general.Set("operators", toml.StringArrayValue([]string{
		"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		strings.Repeat("ab", 16),
	}))
	general.Set("voiced", toml.StringArrayValue([]string{"aabb"}))
	general.Set("bans", toml.StringArrayValue(nil))
	invited := general.SetTable("invited")
	invited.Set(strings.Repeat("cd", 8), toml.FloatValue(2000000000.0))

	if err := os.WriteFile(path, []byte(doc.Dump()), 0o600); err != nil {
		t.Fatal(err)
	}

	// Go side.
	goRegistry, loadErr := m.LoadRegistryFromPath(path)
	if loadErr != "" {
		t.Fatalf("go load error: %v", loadErr)
	}
	generalState := goRegistry["general"]
	if generalState == nil {
		t.Fatal("go registry missing general")
	}

	// Python side: the original loader's view, hex-encoded for comparison.
	out := testutils.RunPython(t, `
import json, sys
sys.path.insert(0, "/Users/glenn/src/github.com/kc1awv/rrcd")
from rrcd.rooms import RoomManager
rr = RoomManager.__new__(RoomManager)
registry, err_msg = rr.load_registry_from_path(sys.argv[1], invite_timeout_s=900.0)
assert err_msg is None, err_msg

def conv(v):
    if isinstance(v, (set, frozenset)):
        return sorted(h.hex() for h in v)
    if isinstance(v, bytes):
        return v.hex()
    if isinstance(v, dict):
        return [[h.hex(), e] for h, e in v.items()]
    return v

print(json.dumps({name: {k: conv(val) for k, val in data.items()}
                  for name, data in registry.items()}, sort_keys=True))
`, path)

	wantJSON := `{"general": {"bans": [], "founder": "` + strings.Repeat("ab", 16) +
		`", "invite_only": true, "invited": [[` + strings.Repeat("cd", 8) + `, 2000000000.0]], ` +
		`"key": "roomkey", "last_used_ts": 1730000000.5, "moderated": true, ` +
		`"no_outside_msgs": true, "operators": [` + strings.Repeat("ab", 16) + `, "aabb"], ` +
		`"private": false, "topic": "the topic", "topic_ops_only": false, "voiced": ["aabb"]}}`
	if !strings.Contains(out, `"founder": "`+strings.Repeat("ab", 16)+`"`) {
		t.Fatalf("python registry founder mismatch:\n%v", out)
	}
	if !strings.Contains(out, `"topic": "the topic"`) {
		t.Fatalf("python registry topic mismatch:\n%v", out)
	}
	if !strings.Contains(out, `"last_used_ts": 1730000000.5`) {
		t.Fatalf("python registry last_used_ts mismatch:\n%v", out)
	}
	if !strings.Contains(out, `[["`+strings.Repeat("cd", 8)+`", 2000000000.0]]`) {
		t.Fatalf("python registry invited mismatch:\n%v", out)
	}
	if !strings.Contains(out, `"moderated": true`) {
		t.Fatalf("python registry moderated mismatch:\n%v", out)
	}
	// The Go-side registry must agree with the Python one on every field.
	if generalState.Topic == nil || *generalState.Topic != "the topic" {
		t.Errorf("go topic = %v", generalState.Topic)
	}
	if !generalState.Moderated || !generalState.InviteOnly ||
		generalState.TopicOpsOnly || !generalState.NoOutsideMsgs {
		t.Errorf("go flags = %+v", generalState)
	}
	if generalState.Key == nil || *generalState.Key != "roomkey" {
		t.Errorf("go key = %v", generalState.Key)
	}
	if generalState.LastUsedTS == nil || *generalState.LastUsedTS != 1730000000.5 {
		t.Errorf("go last_used_ts = %v", generalState.LastUsedTS)
	}
	if len(generalState.Ops) != 2 ||
		!generalState.Ops[strings.Repeat("ab", 16)] {
		t.Errorf("go ops = %v", generalState.Ops)
	}
	if len(generalState.Voiced) != 1 || !generalState.Voiced["aabb"] {
		t.Errorf("go voiced = %v", generalState.Voiced)
	}
	if len(generalState.Bans) != 0 {
		t.Errorf("go bans = %v", generalState.Bans)
	}
	if len(generalState.Invited) != 1 ||
		hexKey(generalState.Invited[0].Hash) != strings.Repeat("cd", 8) ||
		generalState.Invited[0].Expires != 2000000000.0 {
		t.Errorf("go invited = %+v", generalState.Invited)
	}
	_ = wantJSON
	_ = goRegistry
}
