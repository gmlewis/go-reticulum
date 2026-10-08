// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gmlewis/go-reticulum/qr"
	"github.com/gmlewis/go-reticulum/rns"
)

// qrTestIdentity is the identity hash of a live Reticulum identity, and
// qrTestLXMF is the lxmf.delivery address `gornid -H lxmf.delivery` printed for
// it. The pair is a fixed point of the derivation the qr command relies on, so a
// change to the destination name — or to which identity material a destination
// hash covers — shows up here instead of only on the wire.
const (
	qrTestIdentity = "0a8b370a62de4c5464b7ef7f56ff33c8"
	qrTestLXMF     = "2a6105f57145860441a62fe3b2a1352c"
	qrTestPeerNick = "Bob"
)

// qrCarriesWhite reports whether a glyph is one the picture draws for a module
// the camera must see as light. Every line of the picture begins and ends with
// one, which is what keeps a body trimmer from shifting the rows.
func qrCarriesWhite(r rune) bool {
	switch r {
	case '█', '▀', '▄':
		return true
	}
	return false
}

// TestQRPictureShape asserts the geometry the help text promises: a 32-character
// address at level L becomes 27 cells across and 14 lines down, every line the
// same width, and no line begins or ends in whitespace.
func TestQRPictureShape(t *testing.T) {
	t.Parallel()

	lines, err := qrPicture(qrTestLXMF)
	if err != nil {
		t.Fatalf("qrPicture: %v", err)
	}
	if len(lines) != 14 {
		t.Fatalf("qrPicture produced %v lines, want 14", len(lines))
	}
	for i, line := range lines {
		if got := utf8.RuneCountInString(line); got != 27 {
			t.Errorf("line %v is %v cells wide, want 27", i, got)
		}
		if strings.TrimSpace(line) != line {
			t.Errorf("line %v has leading or trailing whitespace, which a trimmer would remove", i)
		}
		runes := []rune(line)
		if !qrCarriesWhite(runes[0]) || !qrCarriesWhite(runes[len(runes)-1]) {
			t.Errorf("line %v begins or ends with a background cell: %q", i, line)
		}
	}
}

// TestQRPictureEncodesThePayload reads the picture back, cell by cell, and
// compares the module grid it draws with the encoder's own output. It is the same
// check that was run against a real client's rendering of a real room, and it is
// what makes the picture trustworthy: nothing between the encoder and the screen
// may reorder, drop, or shift a module.
func TestQRPictureEncodesThePayload(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{qrTestLXMF, qrTestIdentity} {
		lines, err := qrPicture(payload)
		if err != nil {
			t.Fatalf("qrPicture(%v): %v", payload, err)
		}
		code, err := qr.Encode(payload, qrErrorCorrection)
		if err != nil {
			t.Fatalf("qr.Encode(%v): %v", payload, err)
		}
		if len(lines) != (code.Size+2*qrQuietZone+1)/2 {
			t.Fatalf("payload %v: %v lines for a %v-module code", payload, len(lines), code.Size)
		}

		// whiteAt reads one half of one cell back out of the picture: the glyph
		// carries the code's white modules, so a black module is a background cell.
		whiteAt := func(row, cell int, bottom bool) bool {
			runes := []rune(lines[row])
			r := runes[cell]
			if bottom {
				return r == '█' || r == '▄'
			}
			return r == '█' || r == '▀'
		}
		for row := range lines {
			for cell := 0; cell < code.Size+2*qrQuietZone; cell++ {
				for _, half := range []struct {
					bottom bool
					offset int
				}{{false, 0}, {true, 1}} {
					moduleX := cell - qrQuietZone
					moduleY := 2*row + half.offset - qrQuietZone
					inGrid := moduleX >= 0 && moduleX < code.Size && moduleY >= 0 && moduleY < code.Size
					// Outside the module grid is the quiet zone, which is white.
					want := true
					if inGrid {
						want = !code.Black(moduleX, moduleY)
					}
					if got := whiteAt(row, cell, half.bottom); got != want {
						t.Fatalf("payload %v: cell (%v,%v bottom=%v) is white=%v, want %v",
							payload, cell, row, half.bottom, got, want)
					}
				}
			}
		}
	}
}

// TestQRAddressForDerivesKnownAddresses asserts both address families, using the
// live identity and destination hash pair as the reference point. It also pins the
// specifier format, which is the one `gornid -H` prints.
func TestQRAddressForDerivesKnownAddresses(t *testing.T) {
	t.Parallel()

	subject := pathSubject{hash: mustHex(qrTestIdentity), label: "glenn"}

	lxmf := qrAddressFor(subject, qrModeLXMF)
	if lxmf.hex != qrTestLXMF {
		t.Errorf("lxmf address = %v, want %v", lxmf.hex, qrTestLXMF)
	}
	if want := "lxmf.delivery." + qrTestIdentity + ":" + qrTestLXMF; lxmf.specifier != want {
		t.Errorf("specifier = %v, want %v", lxmf.specifier, want)
	}
	if lxmf.kind() != "lxmf.delivery" {
		t.Errorf("kind = %v, want lxmf.delivery", lxmf.kind())
	}

	identity := qrAddressFor(subject, qrModeIdentity)
	if identity.hex != qrTestIdentity {
		t.Errorf("identity address = %v, want %v", identity.hex, qrTestIdentity)
	}
	if identity.specifier != "" {
		t.Errorf("an identity hash publishes no destination, so its specifier must be empty, got %q",
			identity.specifier)
	}
}

// TestQRParseArgs covers the argument shapes the usage line admits.
func TestQRParseArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        string
		wantSubject string
		wantMode    string
		wantOK      bool
	}{
		{"no arguments means the asker and the default address", "", "", "", true},
		{"a bare subject", "glenn", "glenn", "", true},
		{"a bare mode means the asker", "lxmf", "", "lxmf", true},
		{"a bare identity mode means the asker", "identity", "", "identity", true},
		{"a subject and a mode", "glenn lxmf", "glenn", "lxmf", true},
		{"a subject and the identity mode", "glenn identity", "glenn", "identity", true},
		{"mode words are matched case-insensitively", "glenn IDENTITY", "glenn", "IDENTITY", true},
		{"a second word that is not a mode is a typo", "glenn nonsense", "", "", false},
		{"more than two words is a misuse", "a b c", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			subject, mode, ok := qrParseArgs(tc.args)
			if ok != tc.wantOK {
				t.Fatalf("qrParseArgs(%q) ok = %v, want %v", tc.args, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if subject != tc.wantSubject || mode != tc.wantMode {
				t.Errorf("qrParseArgs(%q) = (%q, %q), want (%q, %q)",
					tc.args, subject, mode, tc.wantSubject, tc.wantMode)
			}
		})
	}
}

// TestQRCommandDrawsTheAskersLXMFAddress asserts the whole answer an asker sees:
// the caption, the picture, the hex line, and the specifier, in that order.
func TestQRCommandDrawsTheAskersLXMFAddress(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "qr me")

	// The asker is the fixed requester the command fixture addresses from.
	want := hexString(rns.CalculateHash(&rns.Identity{Hash: peerHashFor(0x11)}, "lxmf", "delivery"))
	picture, err := qrPicture(want)
	if err != nil {
		t.Fatalf("qrPicture: %v", err)
	}
	if wantLines := len(picture) + 3; len(lines) != wantLines {
		t.Fatalf("the reply is %v lines, want %v: %q", len(lines), wantLines, lines)
	}
	if !strings.Contains(lines[0], "lxmf.delivery address for") {
		t.Errorf("caption = %q, want it to name the address family", lines[0])
	}
	for i, line := range picture {
		if lines[1+i] != line {
			t.Fatalf("picture line %v = %q, want %q", i, lines[1+i], line)
		}
	}
	hexLine := lines[len(picture)+1]
	if wantLine := "< lxmf@" + want + " >"; hexLine != wantLine {
		t.Errorf("address line = %q, want %q", hexLine, wantLine)
	}
	if got, wantSpec := lines[len(picture)+2], "full destination specifier: lxmf.delivery."+
		hexString(peerHashFor(0x11))+":"+want; got != wantSpec {
		t.Errorf("specifier line = %q, want %q", got, wantSpec)
	}
	if len(lines) > qrReplyBudget {
		t.Errorf("the answer is %v lines, over the command's own budget of %v", len(lines), qrReplyBudget)
	}
}

// TestQRCommandIdentityModeNamesNoDestination asserts the identity family: the
// asker's own identity hash, drawn, with no specifier line because an identity
// publishes no destination of its own.
func TestQRCommandIdentityModeNamesNoDestination(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "qr me identity")

	identityHex := hexString(peerHashFor(0x11))
	picture, err := qrPicture(identityHex)
	if err != nil {
		t.Fatalf("qrPicture: %v", err)
	}
	if wantLines := len(picture) + 2; len(lines) != wantLines {
		t.Fatalf("the reply is %v lines, want %v: %q", len(lines), wantLines, lines)
	}
	if !strings.Contains(lines[0], "RNS identity address for") {
		t.Errorf("caption = %q, want it to name the identity family", lines[0])
	}
	if got := lines[len(picture)+1]; got != "< @"+identityHex+" >" {
		t.Errorf("address line = %q, want %q", got, "< @"+identityHex+" >")
	}
	for _, line := range lines {
		if strings.Contains(line, "specifier") {
			t.Errorf("an identity hash has no destination specifier, got %q", line)
		}
	}
}

// TestQRCommandDrawsAnotherPeer asserts a named peer is drawn from their own
// identity hash, which is the hash the hub knows them by — not the asker's.
func TestQRCommandDrawsAnotherPeer(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	peer := peerHashFor(0x31)
	fake.setKnownPeer(hexString(peer), qrTestPeerNick)

	lines := runLines(t, reg, session, "qr "+qrTestPeerNick)
	want := hexString(rns.CalculateHash(&rns.Identity{Hash: peer}, "lxmf", "delivery"))
	if got, wantLine := lines[len(lines)-2], "< lxmf@"+want+" >"; got != wantLine {
		t.Errorf("address line = %q, want the address derived from %v: %q",
			got, qrTestPeerNick, wantLine)
	}
	if !strings.Contains(lines[0], qrTestPeerNick) {
		t.Errorf("caption = %q, want it to name %v", lines[0], qrTestPeerNick)
	}
}

// TestQRCommandRejectsAStrayWord asserts a mistyped mode is answered with the
// usage line rather than a guess at what was meant.
func TestQRCommandRejectsAStrayWord(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "qr me lxmff")
	if len(lines) != 1 || lines[0] != "Usage: "+qrUsage {
		t.Errorf("reply = %q, want the usage line %q", lines, "Usage: "+qrUsage)
	}
}

// TestQRReplySurvivesTheReplyBudget asserts the picture reaches the room whole
// even though max_reply_lines is smaller than the picture: a picture cut at line
// twelve is not a shorter answer, it is a wrong one. The control case is an
// ordinary long reply, which the same bound must still truncate.
func TestQRReplySurvivesTheReplyBudget(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	if cfg.MaxReplyLines >= qrReplyBudget {
		t.Fatalf("this test needs max_reply_lines (%v) below the picture's budget (%v)",
			cfg.MaxReplyLines, qrReplyBudget)
	}
	reg, session, fake := commandFixture(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), reg.Run)
	r.lineBudget = reg.lineBudget
	r.handle(session, addressedMessage("general", "@gorrcbot qr me"))

	notices := fake.noticeList()
	picture, err := qrPicture(hexString(rns.CalculateHash(
		&rns.Identity{Hash: peerHashFor(0x21)}, "lxmf", "delivery")))
	if err != nil {
		t.Fatalf("qrPicture: %v", err)
	}
	if want := len(picture) + 3; len(notices) != want {
		t.Fatalf("the bot sent %v notices, want %v: %v", len(notices), want, noticeTexts(fake))
	}
	for i, notice := range notices {
		if strings.Contains(notice.Text, truncatedMarker) {
			t.Fatalf("notice %v was truncated, so the picture is not scannable: %q", i, notice.Text)
		}
	}
	for i, line := range picture {
		if got := notices[1+i].Text; got != line {
			t.Errorf("picture row %v reached the room as %q, want %q", i, got, line)
		}
	}

	// The control: an ordinary reply longer than the configured bound is still
	// truncated, so the exception above belongs to the picture and not to every
	// command.
	plainSession, plainFake := newReplySession(t, cfg)
	plain := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"one", "two", "three", "four", "five", "six", "seven", "eight",
			"nine", "ten", "eleven", "twelve", "thirteen", "fourteen"}
	})
	plain.lineBudget = reg.lineBudget
	plain.handle(plainSession, addressedMessage("general", "@gorrcbot ping"))
	plainNotices := plainFake.noticeList()
	if len(plainNotices) != cfg.MaxReplyLines {
		t.Fatalf("an ordinary reply produced %v notices, want the bound %v",
			len(plainNotices), cfg.MaxReplyLines)
	}
	if last := plainNotices[len(plainNotices)-1].Text; !strings.Contains(last, truncatedMarker) {
		t.Errorf("the last ordinary notice = %q, want the truncation marker", last)
	}
}
