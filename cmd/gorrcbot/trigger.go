// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the addressing contract: the single rule that decides whether
// a message was addressed to gorrbot. Everything else in the bot is silent by
// default, so this is the only place that may turn a message into a reply.
//
// A message addresses the bot when the FIRST token of its text is one of:
//
//	@<nick>            the room's trigger nick, case-insensitive
//	@<hash-prefix>     at least MinHashPrefix hexadecimal characters of the
//	                   bot's identity hash
//
// optionally followed by ':' or ','. The rest of the text is the command line.
// A direct NOTICE (RRC K_DST) addressed to the bot needs no prefix at all: the
// envelope itself is the address, so its whole body is the command line.
//
// Anything else is silence, including a mention in the middle of a sentence, a
// prose mention of the nickname, a message addressed to somebody else, and a
// nickname that merely starts with the trigger nick.

package main

import (
	"strings"

	"github.com/gmlewis/go-reticulum/rrc"
)

// MinHashPrefix is the shortest hexadecimal identity-hash prefix the bot accepts
// as an address. It matches the client's peer-resolution threshold, so the
// prefix that addresses the bot is also the prefix that resolves it as a peer.
const MinHashPrefix = rrc.MinPeerHashPrefix

// trigger describes how a message addressed the bot.
type trigger struct {
	// Addressed reports that the message addresses the bot.
	Addressed bool
	// Command is the command line: the text after the address, trimmed.
	Command string
	// Direct reports that the message arrived as a direct NOTICE addressed to
	// the bot, so no @ prefix was required.
	Direct bool
	// Nick is the trigger nick in effect for the room the message arrived in,
	// which is what a usage message should show. It is empty only when the
	// configuration names no nick at all.
	Nick string
}

// parseTrigger decides whether msg addresses the bot. nick is the trigger nick
// for the room the message arrived in, and ownHashHex is the bot's identity hash
// in lowercase hexadecimal.
func parseTrigger(msg *rrc.RRCMessage, nick, ownHashHex string) trigger {
	if msg == nil {
		return trigger{}
	}
	if msg.Direct {
		return trigger{Addressed: true, Command: strings.TrimSpace(msg.Text), Direct: true}
	}
	// Only conversation can address the bot. Notices from the hub, system rows
	// and error rows are protocol traffic: they may quote the bot's nickname
	// without meaning to talk to it.
	switch msg.Kind {
	case "msg", "action":
	default:
		return trigger{}
	}

	text := strings.TrimLeft(msg.Text, " \t")
	if !strings.HasPrefix(text, "@") {
		return trigger{}
	}
	// The address is the first token, ended by whitespace or by the optional
	// ':' or ',' separator a human naturally types after a name.
	body := text[1:]
	token, rest := body, ""
	if idx := strings.IndexAny(body, " \t:,"); idx >= 0 {
		token, rest = body[:idx], strings.TrimSpace(strings.TrimLeft(body[idx+1:], " \t:,"))
	}
	if token == "" {
		return trigger{}
	}

	trimmedNick := strings.TrimSpace(nick)
	if trimmedNick != "" && strings.EqualFold(token, trimmedNick) {
		return trigger{Addressed: true, Command: rest, Nick: trimmedNick}
	}
	if len(token) >= MinHashPrefix && isHexPrefix(token, ownHashHex) {
		// Nick reports the trigger nick in effect for the room, which is what
		// a usage message should show; the hash alias is not a name.
		return trigger{Addressed: true, Command: rest, Nick: trimmedNick}
	}
	return trigger{}
}
