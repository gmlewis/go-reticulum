// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestGetAnnounceAppDataIncludesSFCompression verifies that the
// delivery-announce app-data payload appends [SF_COMPRESSION] as the third
// peer_data element, matching Python LXMRouter.get_announce_app_data
// (LXMF/LXMRouter.py, v1.1.0) which packs
// [display_name, stamp_cost, supported_functionality] where
// supported_functionality = [SF_COMPRESSION]. Golden bytes are captured from
// CPython+umsgpack: msgpack.packb([b"Alice", 100, [0]]) and
// msgpack.packb([b"Bob", None, [0]]).
func TestGetAnnounceAppDataIncludesSFCompression(t *testing.T) {
	t.Parallel()

	// With a stamp cost: [b"Alice", 100, [0]].
	ts1 := rns.NewTransportSystem(nil)
	tmpDir1 := testutils.TempDir(t, tempDirPrefix)
	router1 := mustTestNewRouter(t, ts1, nil, tmpDir1)
	id1 := mustTestNewIdentity(t, true)
	cost100 := 100
	dest1, err := router1.RegisterDeliveryIdentity(id1, "", &cost100)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}
	router1.SetDisplayName(dest1.Hash, "Alice")
	appData1 := router1.GetAnnounceAppData(dest1.Hash)
	if appData1 == nil {
		t.Fatal("expected non-nil app data with stamp cost")
	}
	wantHex1 := "93c405416c696365649100"
	if got := hexEncode(appData1); got != wantHex1 {
		t.Fatalf("app data with stamp cost = %s, want %s", got, wantHex1)
	}

	// Without a stamp cost: [b"Bob", None, [0]]. A separate router is used
	// because RegisterDeliveryIdentity allows only one delivery identity per
	// router.
	ts2 := rns.NewTransportSystem(nil)
	tmpDir2 := testutils.TempDir(t, tempDirPrefix)
	router2 := mustTestNewRouter(t, ts2, nil, tmpDir2)
	id2 := mustTestNewIdentity(t, true)
	var zero int
	dest2, err := router2.RegisterDeliveryIdentity(id2, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}
	router2.SetDisplayName(dest2.Hash, "Bob")
	appData2 := router2.GetAnnounceAppData(dest2.Hash)
	if appData2 == nil {
		t.Fatal("expected non-nil app data without stamp cost")
	}
	wantHex2 := "93c403426f62c09100"
	if got := hexEncode(appData2); got != wantHex2 {
		t.Fatalf("app data without stamp cost = %s, want %s", got, wantHex2)
	}

	// The third element unpacks to a functionality list containing
	// SFCompression.
	unpacked, err := msgpack.Unpack(appData1)
	if err != nil {
		t.Fatalf("unpack app data: %v", err)
	}
	peerData, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", unpacked)
	}
	if len(peerData) != 3 {
		t.Fatalf("peer data length=%v want=3", len(peerData))
	}
	fnList, ok := peerData[2].([]any)
	if !ok {
		t.Fatalf("peer_data[2] type=%T want=[]any", peerData[2])
	}
	if len(fnList) != 1 {
		t.Fatalf("functionality list length=%v want=1", len(fnList))
	}
	if !functionalityCodeEquals(fnList[0], SFCompression) {
		t.Fatalf("functionality code=%v want SFCompression(%v)", fnList[0], SFCompression)
	}
}

// hexEncode returns the lowercase hexadecimal encoding of b.
func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}
