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
	t.Helper()
	id, err := NewIdentity(createKeys)
	mustTest(t, err)
	return id
}

func mustTestNewDestination(t *testing.T, identity *Identity, direction, destType int, appName string, aspects ...string) *Destination {
	t.Helper()
	dest, err := NewDestination(identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewDestinationWithTransport(t *testing.T, ts *TransportSystem, identity *Identity, direction int, destType int, appName string, aspects ...string) *Destination {
	t.Helper()
	dest, err := NewDestinationWithTransport(ts, identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewLink(t *testing.T, destination *Destination) *Link {
	t.Helper()
	link, err := NewLink(destination)
	mustTest(t, err)
	return link
}

func mustTestNewLinkWithTransport(t *testing.T, ts *TransportSystem, destination *Destination) *Link {
	t.Helper()
	link, err := NewLinkWithTransport(ts, destination)
	mustTest(t, err)
	return link
}

func mustTestNewResourceWithOptions(t *testing.T, data []byte, link *Link, opts ResourceOptions) *Resource {
	t.Helper()
	resource, err := NewResourceWithOptions(data, link, opts)
	mustTest(t, err)
	return resource
}

func mustTestNewReticulum(t *testing.T, configDir string) *Reticulum {
	t.Helper()
	ret, err := NewReticulum(configDir)
	mustTest(t, err)
	return ret
}

func mustTestNewReticulumWithTransport(t *testing.T, configDir string, ts Transport) *Reticulum {
	t.Helper()
	ret, err := NewReticulumWithTransport(configDir, ts)
	mustTest(t, err)
	return ret
}
