// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// mentionCases is the input battery. Each case goes to BOTH the Python reference
// (which decides with its own _mention_re) and the port (matchMention); the test
// fails when the two disagree, so the expected answers are never transcribed.
var mentionCases = []struct {
	nick string
	body string
}{
	// The ordinary forms.
	{"alice", "hey @alice how are you"},
	{"alice", "@alice"},
	{"alice", "@alice,"},
	{"alice", "(@alice)"},
	{"alice", "hi @alice!"},
	{"alice", "line one\n@alice line two"},
	// Case folding: Python compiles with re.IGNORECASE.
	{"alice", "@ALICE"},
	{"Alice", "@alice"},
	{"aLiCe", "@AlIcE hi"},
	// Word-character boundaries: a nick embedded in a longer word is not a mention.
	{"alice", "user@alice"},
	{"alice", "@alicemail"},
	{"alice", "@alice_mail"},
	{"alice", "x@alice"},
	{"alice", "@alicex"},
	{"alice", "3@alice"},
	{"alice", "_@alice"},
	// Non-word boundaries are fine.
	{"alice", "-@alice-"},
	{"alice", ".@alice."},
	{"alice", "/@alice/"},
	{"alice", "\t@alice\t"},
	// No "@" at all.
	{"alice", "alice"},
	{"alice", "hello everyone"},
	{"alice", ""},
	// A different nick is not a mention.
	{"alice", "@bob"},
	{"alice", "hey @bob and @carol"},
	// Two mentions of the same nick: the second follows a word character, so only
	// the first satisfies the boundary rule — in the reference too.
	{"alice", "@alice@alice"},
	// A nick containing regex metacharacters must be quoted, not interpreted.
	{"a.c", "hey @a.c"},
	{"a.c", "hey @abc"},
	{"a+b", "cc @a+b dd"},
	{"a+b", "cc @aab dd"},
	// An empty nick never matches (Python's _mention_re returns None).
	{"", "@"},
	{"", "anything"},
	// A long nick and a multi-byte body.
	{"alice", "π @alice π"},
	{"näke", "@näke"},
}

// TestMentionRegexAgainstPython pins the mention rule to the reference: Python's
// _mention_re compiles (?<![A-Za-z0-9_])@<re.escape(nick)>(?![A-Za-z0-9_]) with
// re.IGNORECASE (nomadnet RRC.py:27-36), and the port has to agree on every body
// in the battery. Go's regexp is RE2, which has no lookbehind, so the port encodes
// the boundaries differently (MentionRegex) — this test is what keeps that
// encoding honest.
func TestMentionRegexAgainstPython(t *testing.T) {
	t.Parallel()
	skipIfNoPythonNomadnet(t)

	req := make([]map[string]string, 0, len(mentionCases))
	for _, c := range mentionCases {
		req = append(req, map[string]string{"nick": c.nick, "body": c.body})
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	dir := testutils.TempDir(t, "rrc-mention-py-")
	inPath := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(inPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(getPythonNomadnetExe(), "-c", mentionParityScript, inPath)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python mention probe failed: %v\n--- stderr ---\n%v\n--- stdout ---\n%v", err, stderr.String(), stdout.String())
	}

	var wants []bool
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &wants); err != nil {
		t.Fatalf("decoding python mention answers: %v\nraw: %v", err, stdout.String())
	}
	if len(wants) != len(mentionCases) {
		t.Fatalf("python returned %v answers for %v cases", len(wants), len(mentionCases))
	}

	for i, c := range mentionCases {
		if got := matchMention(c.nick, c.body); got != wants[i] {
			t.Errorf("matchMention(%q, %q) = %v, but the Python reference says %v",
				c.nick, c.body, got, wants[i])
		}
	}
}

// mentionParityScript answers each case with the reference's own rule: it calls
// nomadnet.RRC._mention_re (the very function the hub uses, RRC.py:27-36) and
// reports whether its pattern searches the body. It reads cases from argv[1] and
// prints a JSON array of booleans.
const mentionParityScript = `import json, re, sys
from nomadnet.RRC import _mention_re

with open(sys.argv[1]) as f:
    cases = json.load(f)

answers = []
for c in cases:
    pat = _mention_re(c["nick"])
    answers.append(bool(pat is not None and pat.search(c["body"])))
print(json.dumps(answers))
`

// TestMentionCacheIsBoundedAndCorrect pins the compiled-pattern cache: a repeated
// nick reuses one pattern, and the cache is cleared once it grows past 32 entries,
// mirroring Python's _MENTION_RE_CACHE (RRC.py:30-36).
func TestMentionCacheIsBoundedAndCorrect(t *testing.T) {
	mentionCacheMu.Lock()
	mentionCache = map[string]*regexp.Regexp{}
	mentionCacheMu.Unlock()

	first := mentionPattern("alice")
	if first == nil {
		t.Fatal("mentionPattern(alice) = nil")
	}
	if second := mentionPattern("alice"); second != first {
		t.Error("mentionPattern(alice) compiled a second pattern; the cache did not hit")
	}

	for i := range 40 {
		mentionPattern("nick" + strings.Repeat("x", i+1))
	}
	mentionCacheMu.Lock()
	size := len(mentionCache)
	mentionCacheMu.Unlock()
	if size > 33 {
		t.Errorf("mention cache holds %v entries, want it trimmed at 32", size)
	}

	// The rule still works after the trim.
	if !matchMention("alice", "hey @alice") {
		t.Error("matchMention stopped matching after the cache was trimmed")
	}
	if matchMention("alice", "user@alice") {
		t.Error("matchMention started matching a word-embedded nick after the cache was trimmed")
	}
}

// TestFlagMention pins the hub-level rule: an inbound message whose body names the
// local nick is flagged, and the flag is what populates the hub's mention rooms.
// Python does this at the tail of its T_MSG and T_ACTION branches (RRC.py:1086-1091,
// 1119-1124); without the flag MentionRooms stays empty, the "!" marker never
// appears on the hub list, and stored history never carries H_MENTION.
func TestFlagMention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		src       string
		body      string
		wantFlag  bool
		wantRooms bool
	}{
		{"names the local nick", "peerhash", "hello @OwnNick", true, true},
		{"names it case-insensitively", "peerhash", "hello @ownnick", true, true},
		{"a different nick is not a mention", "peerhash", "hello @Someone", false, false},
		{"a word-embedded nick is not a mention", "peerhash", "mail@OwnNick", false, false},
		{"own source is never a mention", "ownhash", "hello @OwnNick", false, false},
		{"no mention at all", "peerhash", "hello everyone", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The fixture manager reports no active room for this hub, so the
			// room counts as unread and a mention is recorded (hub.go:3376-3396).
			_, hub := fanoutFixture(t)

			hub.HandleData(fanoutCopy(tc.src, "Peer", tc.body, "mid-"+tc.name, time.Now().UnixMilli()))

			msgs := hub.GetMessages("test")
			if len(msgs) == 0 {
				t.Fatal("the message was not recorded")
			}
			if got := msgs[len(msgs)-1].Mention; got != tc.wantFlag {
				t.Errorf("message Mention = %v, want %v", got, tc.wantFlag)
			}
			if got := hub.MentionRoomList(); (len(got) > 0) != tc.wantRooms {
				t.Errorf("MentionRoomList = %v, want a mention room = %v", got, tc.wantRooms)
			}
		})
	}
}

// TestFlagMentionOnAction pins the same rule for the action branch: Python sets
// msg.mention in T_ACTION as well as T_MSG (RRC.py:1119-1124).
func TestFlagMentionOnAction(t *testing.T) {
	t.Parallel()

	_, hub := fanoutFixture(t)

	env := MakeClientEnvelope(TypeAction, []byte("peerhash"), []byte("test"), []byte("Peer"), "waves at @OwnNick", []byte("act-1"), time.Now().UnixMilli())
	data, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	hub.HandleData(data)

	msgs := hub.GetMessages("test")
	if len(msgs) == 0 {
		t.Fatal("the action was not recorded")
	}
	if !msgs[len(msgs)-1].Mention {
		t.Error("action Mention = false, want true for a body naming the local nick")
	}
}
