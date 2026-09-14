// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package rrc

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

// Errors returned by SendNotice.
var (
	// ErrNoticeRoomRequired means SendNotice was called without a room. A
	// roomless notice is the hub's greeting convention, not a client reply.
	ErrNoticeRoomRequired = errors.New("notice requires a room name")
	// ErrNoticeBodyEmpty means the notice body had no visible content.
	ErrNoticeBodyEmpty = errors.New("notice body is empty")
)

// SendNotice sends one T_NOTICE to a room. This is the reply shape the RRC bot
// contract requires: one envelope per line, addressed to the room, carrying no
// K_DST. A caller that has more to say than one envelope holds must split the
// text itself and send each line separately.
//
// Unlike SendMessage there is no user-command interception: a NOTICE is never a
// slash command. The notice is recorded locally as this client's own notice and
// its message id is remembered so the hub's per-member fanout copy is collapsed
// instead of rendering a second time.
//
// The returned string is the message id in hexadecimal.
func (h *RRCHub) SendNotice(room, text string) (string, error) {
	room = strings.ToLower(strings.TrimSpace(room))
	if room == "" {
		return "", ErrNoticeRoomRequired
	}
	if strings.TrimSpace(text) == "" {
		return "", ErrNoticeBodyEmpty
	}

	mid := MsgID()
	ts := NowMs()

	nick := h.effectiveNick()
	var srcHash []byte
	if h.Manager != nil {
		srcHash = h.Manager.identityHash()
	}
	env := MakeClientEnvelope(TypeNotice, srcHash, []byte(room), []byte(nick), text, mid, ts)

	// The link layer silently drops an envelope larger than the MDU, so an
	// oversized notice would vanish with no error anywhere. Report it here and
	// send nothing.
	if size := cborEncodedLen(env); size > rns.MDU {
		return "", fmt.Errorf("notice is %v bytes encoded, larger than one envelope (MDU %v)", size, rns.MDU)
	}

	h.rememberSentID(hexString(mid))
	// The body is remembered BEFORE the send so a fanout echo that races back
	// ahead of the local record is still collapsed (see collapseSelfEcho).
	h.rememberSentBody("notice", room, text, ts)
	h.sendEnv(env)

	h.recordNotice(&RRCMessage{
		Kind: "notice",
		Room: room,
		Src:  srcHash,
		Nick: nick,
		Text: text,
		Ts:   ts,
	})

	return hexString(mid), nil
}
