// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

// equivalenceFixture describes one representative CLI scenario for the
// behavioral comparison harness.
type equivalenceFixture struct {
	name string
}

func defaultEquivalenceFixtures() []equivalenceFixture {
	return []equivalenceFixture{
		{name: "local-table"},
		{name: "discovery"},
		{name: "drop"},
		{name: "rates"},
		{name: "blackhole"},
		{name: "remote-link"},
	}
}
