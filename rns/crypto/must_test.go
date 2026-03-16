// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import "testing"

func mustTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustTestNewToken(t *testing.T, key []byte) *Token {
	t.Helper()
	token, err := NewToken(key)
	mustTest(t, err)
	return token
}
