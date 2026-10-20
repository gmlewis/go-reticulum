// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package netinfo

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestInterfacesPythonParity derives the host adapter names from the installed
// reference (RNS.Interfaces.util.netinfo.interfaces) and compares them as a
// set: both implementations enumerate the same machine, so a divergence means
// the Go net-based adapter model lost or invented an adapter.
func TestInterfacesPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	const script = `
import sys, json
import RNS.Interfaces.util.netinfo as netinfo
json.dump(netinfo.interfaces(), sys.stdout)
`
	var want []string
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script)), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	got, err := Interfaces()
	if err != nil {
		t.Fatalf("Interfaces: %v", err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("Interfaces = %v, want %v (Python netinfo)", got, want)
	}
}

// TestIndexesAndAddresses pins the index map and the per-family address shape.
func TestIndexesAndAddresses(t *testing.T) {
	t.Parallel()

	indexes, err := InterfaceNamesToIndexes()
	if err != nil {
		t.Fatalf("InterfaceNamesToIndexes: %v", err)
	}
	if len(indexes) == 0 {
		t.Skip("no network adapters on this host")
	}

	var loopback string
	for name, idx := range indexes {
		if idx == 1 {
			loopback = name
		}
	}
	if loopback == "" {
		t.Skip("no loopback adapter found")
	}
	if _, ok := InterfaceNameToNiceName(loopback); !ok {
		t.Errorf("InterfaceNameToNiceName(%q) not found", loopback)
	}

	addrs, err := IfAddresses(loopback)
	if err != nil {
		t.Fatalf("IfAddresses: %v", err)
	}
	v4 := addrs[AFInet]
	if len(v4) == 0 {
		t.Skipf("loopback %q has no IPv4 address", loopback)
	}
	if !v4[0].IsIPv4 || v4[0].Prefix <= 0 || v4[0].Broadcast == "" {
		t.Errorf("IPv4 address = %+v, want addr with prefix and broadcast", v4[0])
	}
}
