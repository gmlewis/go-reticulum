// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
)

func TestTransportInterfaceExists(t *testing.T) {
	// This test will fail to compile if the Transport interface is not defined.
	var _ Transport = (*TransportSystem)(nil)
}
