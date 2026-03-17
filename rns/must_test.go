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

func mustTestNewDestination(t *testing.T, ts Transport, identity *Identity, direction int, destType int, appName string, aspects ...string) *Destination {
	t.Helper()
	dest, err := NewDestination(ts, identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewLink(t *testing.T, ts Transport, destination *Destination) *Link {
	t.Helper()
	link, err := NewLink(ts, destination)
	mustTest(t, err)
	return link
}

func mustTestNewResourceWithOptions(t *testing.T, data []byte, link *Link, opts ResourceOptions) *Resource {
	t.Helper()
	resource, err := NewResourceWithOptions(data, link, opts)
	mustTest(t, err)
	return resource
}

func mustTestNewReticulum(t *testing.T, ts Transport, configDir string) *Reticulum {
	t.Helper()
	ret, err := NewReticulum(ts, configDir)
	mustTest(t, err)
	return ret
}
