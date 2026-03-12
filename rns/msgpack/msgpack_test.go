// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package msgpack

import (
	"bytes"
	"reflect"
	"testing"
)

func TestMsgPack(t *testing.T) {
	testCases := []any{
		nil,
		true,
		false,
		int64(123),
		int64(-123),
		float64(1.23),
		"hello world",
		[]byte{1, 2, 3},
		[]any{int64(1), "two", true},
		map[any]any{"key": int64(42), int64(1): "value"},
	}

	for _, tc := range testCases {
		packed, err := Pack(tc)
		if err != nil {
			t.Errorf("Pack failed for %v: %v", tc, err)
			continue
		}

		unpacked, err := Unpack(packed)
		if err != nil {
			t.Errorf("Unpack failed for %v: %v", tc, err)
			continue
		}

		if !reflect.DeepEqual(tc, unpacked) {
			t.Errorf("expected %v, got %v", tc, unpacked)
		}
	}
}

func TestMsgPackTypes(t *testing.T) {
	// Special case for byte slices which are unpacked as []byte
	b := []byte{1, 2, 3}
	packed, _ := Pack(b)
	unpacked, _ := Unpack(packed)
	if !bytes.Equal(b, unpacked.([]byte)) {
		t.Errorf("expected %v, got %v", b, unpacked)
	}
}
