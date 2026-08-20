// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// reactionHash is a 32-byte stand-in for a full LXMessage hash used in the
// golden payloads below (msgpack bin8 of length 0x20).
var reactionHash = bytes.Repeat([]byte{0x11}, 32)

// TestRelationFieldConstants verifies that the reply/reaction/comment/
// continuation field constants and their sub-key constants match
// Python LXMF (lxmf/LXMF.py:23-27, 109-126, v1.0.0).
func TestRelationFieldConstants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"FieldReplyTo", FieldReplyTo, 0x30},
		{"FieldReplyQuote", FieldReplyQuote, 0x31},
		{"FieldReaction", FieldReaction, 0x40},
		{"FieldComment", FieldComment, 0x41},
		{"FieldContinuation", FieldContinuation, 0x42},
		{"ReactionTo", ReactionTo, 0x00},
		{"ReactionContent", ReactionContent, 0x01},
		{"CommentFor", CommentFor, 0x00},
		{"ContinuationOf", ContinuationOf, 0x00},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

// TestRelationFieldsPackGolden verifies that each new relation field, packed
// as a lone-entry fields map with msgpack.PackSorted, matches the golden
// bytes captured from CPython+umsgpack (lxmf/LXMF.py:23-27, v1.0.0). PackSorted
// orders keys by their canonical msgpack encoding, which for these small
// fixint keys is ascending numeric order — the same order umsgpack emits when
// the dict literal is written in ascending key order.
func TestRelationFieldsPackGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		field any
		value any
		hex   string
	}{
		{
			"reply_to", FieldReplyTo, cloneBytes(reactionHash),
			"8130c4201111111111111111111111111111111111111111111111111111111111111111",
		},
		{
			"reply_quote", FieldReplyQuote, []byte("quoted text"),
			"8131c40b71756f7465642074657874",
		},
		{
			"reaction", FieldReaction,
			map[any]any{ReactionTo: cloneBytes(reactionHash), ReactionContent: []byte("thumbs")},
			"81408200c420111111111111111111111111111111111111111111111111111111111111111101c4067468756d6273",
		},
		{
			"comment", FieldComment, map[any]any{CommentFor: cloneBytes(reactionHash)},
			"81418100c4201111111111111111111111111111111111111111111111111111111111111111",
		},
		{
			"continuation", FieldContinuation, map[any]any{ContinuationOf: cloneBytes(reactionHash)},
			"81428100c4201111111111111111111111111111111111111111111111111111111111111111",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fields := map[any]any{c.field: c.value}
			packed, err := msgpack.PackSorted(fields)
			if err != nil {
				t.Fatalf("PackSorted: %v", err)
			}
			if got := hexEncode(packed); got != c.hex {
				t.Fatalf("%s packed = %s, want %s", c.name, got, c.hex)
			}
		})
	}
}

// TestRelationFieldsCombinedPackGolden verifies that a fields map carrying
// all five relation fields packs (with PackSorted) to the golden
// combined bytes captured from CPython+umsgpack.
func TestRelationFieldsCombinedPackGolden(t *testing.T) {
	t.Parallel()
	fields := map[any]any{
		FieldReplyTo:      cloneBytes(reactionHash),
		FieldReplyQuote:   []byte("quoted text"),
		FieldReaction:     map[any]any{ReactionTo: cloneBytes(reactionHash), ReactionContent: []byte("thumbs")},
		FieldComment:      map[any]any{CommentFor: cloneBytes(reactionHash)},
		FieldContinuation: map[any]any{ContinuationOf: cloneBytes(reactionHash)},
	}
	packed, err := msgpack.PackSorted(fields)
	if err != nil {
		t.Fatalf("PackSorted: %v", err)
	}
	want := "8530c420111111111111111111111111111111111111111111111111111111111111111131c40b71756f7465642074657874408200c420111111111111111111111111111111111111111111111111111111111111111101c4067468756d6273418100c4201111111111111111111111111111111111111111111111111111111111111111428100c4201111111111111111111111111111111111111111111111111111111111111111"
	if got := hexEncode(packed); got != want {
		t.Fatalf("combined packed = %s, want %s", got, want)
	}
}

// TestRelationFieldsRoundTrip verifies that a message carrying all five
// relation fields packs and unpacks with the fields intact, including the
// nested reaction/comment/continuation dicts. Non-supporting clients receive
// the normal LXM content unchanged (the comment/continuation text lives in the
// content), so the round-trip preserves both content and fields.
func TestRelationFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	fields := map[any]any{
		FieldReplyTo:      cloneBytes(reactionHash),
		FieldReplyQuote:   []byte("quoted text"),
		FieldReaction:     map[any]any{ReactionTo: cloneBytes(reactionHash), ReactionContent: []byte("thumbs")},
		FieldComment:      map[any]any{CommentFor: cloneBytes(reactionHash)},
		FieldContinuation: map[any]any{ContinuationOf: cloneBytes(reactionHash)},
	}
	// The comment/continuation text is carried as normal LXM content so
	// non-supporting clients display it as a plain message.
	m := mustTestNewMessage(t, destination, source, "this is a comment", "comment-title", fields)
	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	unpacked, err := UnpackMessageFromBytes(ts, m.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}
	if !unpacked.SignatureValidated {
		t.Fatalf("signature not validated, reason=%v", unpacked.UnverifiedReason)
	}
	if unpacked.ContentString() != "this is a comment" {
		t.Fatalf("content=%q want %q", unpacked.ContentString(), "this is a comment")
	}

	// FIELD_REPLY_TO: bytes equal the hash.
	if got, ok := unpacked.Fields[int64(FieldReplyTo)].([]byte); !ok || !bytes.Equal(got, reactionHash) {
		t.Fatalf("FIELD_REPLY_TO mismatch: %#v", unpacked.Fields[int64(FieldReplyTo)])
	}
	// FIELD_REPLY_QUOTE: bytes equal the quoted text.
	if got, ok := unpacked.Fields[int64(FieldReplyQuote)].([]byte); !ok || string(got) != "quoted text" {
		t.Fatalf("FIELD_REPLY_QUOTE mismatch: %#v", unpacked.Fields[int64(FieldReplyQuote)])
	}
	// FIELD_REACTION: nested dict with REACTION_TO + REACTION_CONTENT.
	reaction, ok := unpacked.Fields[int64(FieldReaction)].(map[any]any)
	if !ok {
		t.Fatalf("FIELD_REACTION type=%T want map[any]any", unpacked.Fields[int64(FieldReaction)])
	}
	if got, ok := reaction[int64(ReactionTo)].([]byte); !ok || !bytes.Equal(got, reactionHash) {
		t.Fatalf("REACTION_TO mismatch: %#v", reaction[int64(ReactionTo)])
	}
	if got, ok := reaction[int64(ReactionContent)].([]byte); !ok || string(got) != "thumbs" {
		t.Fatalf("REACTION_CONTENT mismatch: %#v", reaction[int64(ReactionContent)])
	}
	// FIELD_COMMENT: nested dict with COMMENT_FOR.
	comment, ok := unpacked.Fields[int64(FieldComment)].(map[any]any)
	if !ok {
		t.Fatalf("FIELD_COMMENT type=%T want map[any]any", unpacked.Fields[int64(FieldComment)])
	}
	if got, ok := comment[int64(CommentFor)].([]byte); !ok || !bytes.Equal(got, reactionHash) {
		t.Fatalf("COMMENT_FOR mismatch: %#v", comment[int64(CommentFor)])
	}
	// FIELD_CONTINUATION: nested dict with CONTINUATION_OF.
	continuation, ok := unpacked.Fields[int64(FieldContinuation)].(map[any]any)
	if !ok {
		t.Fatalf("FIELD_CONTINUATION type=%T want map[any]any", unpacked.Fields[int64(FieldContinuation)])
	}
	if got, ok := continuation[int64(ContinuationOf)].([]byte); !ok || !bytes.Equal(got, reactionHash) {
		t.Fatalf("CONTINUATION_OF mismatch: %#v", continuation[int64(ContinuationOf)])
	}
}

// TestRelationFieldsContentFallback verifies that the comment and
// continuation text is carried as the normal LXM content, so a client that
// ignores the relation fields still receives the full text. Packing a comment
// message and unpacking it yields the content verbatim even when only the
// content is inspected.
func TestRelationFieldsContentFallback(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	fields := map[any]any{FieldComment: map[any]any{CommentFor: cloneBytes(reactionHash)}}
	m := mustTestNewMessage(t, destination, source, "plain comment body", "", fields)
	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	unpacked, err := UnpackMessageFromBytes(ts, m.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}
	// A non-supporting client ignores Fields and reads only the content.
	if unpacked.ContentString() != "plain comment body" {
		t.Fatalf("fallback content=%q want %q", unpacked.ContentString(), "plain comment body")
	}
}
