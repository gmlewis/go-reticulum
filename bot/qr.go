// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the qr command: a peer's address drawn as a black-and-white
// square a phone camera can read straight off the screen.
//
// The picture is text. Each character cell is one module wide and half a module
// tall, so the usual 1:2 terminal cell draws a square module and two QR rows ride
// in one line. Two decisions in here are not obvious, and both come from watching
// a real client paint a real room:
//
//   - Polarity. An RRC client paints a message body in the theme's text color on
//     the theme's background, and that palette is light-on-dark. Drawing the
//     code's BLACK modules as block glyphs would therefore put light modules on a
//     dark field: a photographic negative, which a camera may refuse. Drawing the
//     WHITE modules instead lets the terminal's own dark background be the code's
//     black modules, so the camera sees the ordinary dark-on-light square.
//   - Quiet zone. One module is the minimum that survives the whole pipeline
//     intact: drawn in light glyphs it leaves no line of the picture beginning or
//     ending in whitespace, so a client or a hub that trims a message body cannot
//     pull the rows out of alignment. The zone is not what a reader has to manage;
//     the pane width is, because a pane too narrow for the row makes the client
//     wrap it at one of its spaces, which destroys the code rather than degrading
//     it. The help text therefore names the width.
//
// Which address goes in the picture is the other half of the command. A
// destination hash covers the destination's own name and the publishing
// identity's hash, and nothing else: the public key is not part of it, so the
// lxmf.delivery address of any peer the bot can name is derivable on the spot,
// whether or not that peer has ever announced.
package bot

import (
	"fmt"
	"strings"

	"github.com/gmlewis/go-reticulum/qr"
	"github.com/gmlewis/go-reticulum/rns"
)

const (
	// qrErrorCorrection is the error-correction level the picture is drawn at.
	// Level L is the smallest code for a 32-character address, which keeps the
	// picture at 27 cells and 14 lines; the heavier levels cost two more lines
	// each without buying anything a screen never damages.
	qrErrorCorrection = qr.L
	// qrQuietZone is the quiet zone in modules on every side.
	qrQuietZone = 1
	// qrReplyBudget bounds the NOTICE lines one picture may produce, including
	// its caption and its hex line. It is larger than max_reply_lines on purpose:
	// a picture is one indivisible answer, so a truncated one is not a shorter
	// answer but a wrong one, and this bound is what keeps that exception honest.
	qrReplyBudget = 24
	// qrUsage is the usage line for the qr command.
	qrUsage = "qr [<nick|hash|me>] [identity|lxmf]"
)

// qrMode names which of a peer's addresses to draw. An RNS identity owns many
// destinations, and they are different addresses: the identity hash is what a
// hub addresses a client by, and lxmf.delivery is where messages go.
type qrMode int

const (
	// qrModeLXMF draws the peer's lxmf.delivery destination, the address to send
	// messages to.
	qrModeLXMF qrMode = iota
	// qrModeIdentity draws the peer's RNS identity hash, which is the address this
	// hub knows a client by and needs no destination name at all.
	qrModeIdentity
)

// qrAddress is one address resolved to something drawable, plus what the caption
// and the trailing lines say about it.
type qrAddress struct {
	// mode is the address family that was resolved.
	mode qrMode
	// hex is the address the picture encodes.
	hex string
	// specifier is the full destination specifier, empty for an identity hash,
	// which publishes no destination name of its own.
	specifier string
}

// kind names the address family in the caption, using the RNS name a reader can
// look up rather than a private shorthand.
func (a qrAddress) kind() string {
	if a.mode == qrModeIdentity {
		return "RNS identity"
	}
	return "lxmf.delivery"
}

// linkForm is the address written the way a client acts on it: an LXMF address
// carries the lxmf@ sigil the client turns into a link that opens a conversation,
// and an identity hash carries the @ a hub mention uses. The picture above encodes
// the bare address either way, because that is what a camera reads — but a bare hash
// in a chat body is a link to a NomadNet NODE, so printing one for a message address
// would invite a reader to click it and be told the node does not exist.
func (a qrAddress) linkForm() string {
	if a.mode == qrModeIdentity {
		return "@" + a.hex
	}
	return "lxmf@" + a.hex
}

// qrPicture renders text as the lines of the room's picture: one text line per
// two module rows, each cell one module wide.
func qrPicture(text string) ([]string, error) {
	code, err := qr.Encode(text, qrErrorCorrection)
	if err != nil {
		return nil, err
	}
	// The picture is the code plus its quiet zone, so a cell outside the module
	// grid is white.
	size := code.Size + 2*qrQuietZone
	black := func(x, y int) bool {
		x -= qrQuietZone
		y -= qrQuietZone
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false
		}
		return code.Black(x, y)
	}
	lines := make([]string, 0, (size+1)/2)
	for y := 0; y < size; y += 2 {
		var b strings.Builder
		for x := range size {
			top := black(x, y)
			bottom := y+1 < size && black(x, y+1)
			switch {
			case top && bottom:
				// Both halves black: the terminal background shows through.
				b.WriteRune(' ')
			case top && !bottom:
				// Black above, white below: the light half sits at the bottom.
				b.WriteRune('▄')
			case !top && bottom:
				// White above, black below: the light half sits at the top.
				b.WriteRune('▀')
			default:
				// Both halves white: a full light cell.
				b.WriteRune('█')
			}
		}
		lines = append(lines, b.String())
	}
	return lines, nil
}

// qrParseArgs splits a qr argument list into a subject token and a mode word. The
// subject defaults to the asker, which is what most readers mean, and an empty
// mode leaves the address family at its default. A second token that is not a mode
// word is a typo the caller reports rather than guesses at.
func qrParseArgs(args string) (subject string, mode string, ok bool) {
	fields := strings.Fields(args)
	if len(fields) > 2 {
		return "", "", false
	}
	for i, field := range fields {
		if !isQRModeWord(field) {
			continue
		}
		// The mode word may stand alone ("qr lxmf" asks for the asker's own lxmf
		// address) or follow a subject.
		if i == 0 {
			return "", field, true
		}
		return fields[0], field, true
	}
	if len(fields) == 2 {
		return "", "", false
	}
	if len(fields) == 1 {
		return fields[0], "", true
	}
	return "", "", true
}

// isQRModeWord reports whether a token names an address family.
func isQRModeWord(token string) bool {
	switch strings.ToLower(token) {
	case "lxmf", "delivery":
		return true
	case "identity", "rns", "id":
		return true
	}
	return false
}

// qrModeFor reads the mode word qrParseArgs accepted.
func qrModeFor(word string) qrMode {
	switch strings.ToLower(word) {
	case "identity", "rns", "id":
		return qrModeIdentity
	}
	return qrModeLXMF
}

// qrAddressFor resolves one subject and mode to the address to draw. The subject
// is the identity hash the bot knows the peer by; the mode picks which of that
// identity's addresses comes out.
func qrAddressFor(subject pathSubject, mode qrMode) qrAddress {
	identityHex := hexString(subject.hash)
	if mode == qrModeIdentity {
		return qrAddress{mode: qrModeIdentity, hex: identityHex}
	}
	// A destination hash covers the expanded destination name and the identity's
	// hash, so only the hash is needed here: the peer's public key, and therefore
	// any announce of theirs, is not part of it.
	identity := &rns.Identity{Hash: subject.hash}
	destHash := rns.CalculateHash(identity, "lxmf", "delivery")
	destHex := hexString(destHash)
	return qrAddress{
		mode:      qrModeLXMF,
		hex:       destHex,
		specifier: fmt.Sprintf("lxmf.delivery.%v:%v", identityHex, destHex),
	}
}

// runQR draws a peer's address as a picture the asker can scan, followed by the
// same address in hex so it can also be copied.
func (c *commandContext) runQR() []string {
	subjectToken, modeWord, ok := qrParseArgs(c.Args)
	if !ok {
		return []string{"Usage: " + qrUsage}
	}
	if subjectToken == "" {
		subjectToken = "me"
	}
	subject, rejected := c.pathSubject(subjectToken)
	if rejected != nil {
		return rejected
	}
	address := qrAddressFor(subject, qrModeFor(modeWord))
	picture, err := qrPicture(address.hex)
	if err != nil {
		return []string{fmt.Sprintf("I could not draw %v: %v", address.hex, err)}
	}
	label := safeEcho(subject.label, maxEchoNickBytes)
	lines := make([]string, 0, len(picture)+3)
	lines = append(lines, fmt.Sprintf("%v address for %v (scan the picture below, or copy the address):",
		address.kind(), label))
	lines = append(lines, picture...)
	lines = append(lines, fmt.Sprintf("< %v >", address.linkForm()))
	if address.specifier != "" {
		lines = append(lines, "full destination specifier: "+address.specifier)
	}
	return lines
}
