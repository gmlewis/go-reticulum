// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

func mustTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustTestNewIdentity(t *testing.T, createKeys bool) *Identity {
	id, err := NewIdentity(createKeys)
	mustTest(t, err)
	return id
}

func mustTestNewDestination(t *testing.T, identity *Identity, direction int, destType int, appName string, aspects ...string) *Destination {
	dest, err := NewDestination(identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewDestinationWithTransport(t *testing.T, ts *TransportSystem, identity *Identity, direction int, destType int, appName string, aspects ...string) *Destination {
	dest, err := NewDestinationWithTransport(ts, identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewLink(t *testing.T, destination *Destination) *Link {
	link, err := NewLink(destination)
	mustTest(t, err)
	return link
}

func mustTestNewLinkWithTransport(t *testing.T, ts *TransportSystem, destination *Destination) *Link {
	link, err := NewLinkWithTransport(ts, destination)
	mustTest(t, err)
	return link
}
