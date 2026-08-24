// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func mustTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustTestNewIdentity(t *testing.T, createKeys bool) *rns.Identity {
	t.Helper()
	id, err := rns.NewIdentity(createKeys, nil)
	mustTest(t, err)
	return id
}

func mustTestNewDestination(t *testing.T, ts rns.Transport, identity *rns.Identity, direction, destType int, appName string, aspects ...string) *rns.Destination {
	t.Helper()
	dest, err := rns.NewDestination(ts, identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewMessage(t *testing.T, destination, source *rns.Destination, content, title string, fields map[any]any) *Message {
	t.Helper()
	msg, err := NewMessage(destination, source, content, title, fields)
	mustTest(t, err)
	return msg
}

func mustTestNewRouter(t *testing.T, ts rns.Transport, identity *rns.Identity, storagePath string) *Router {
	t.Helper()
	router, err := NewRouter(ts, identity, storagePath)
	mustTest(t, err)
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func mustTestNewRouterWithConfig(t *testing.T, ts rns.Transport, identity *rns.Identity, storagePath string, policyConfig map[string]any) *Router {
	t.Helper()
	router, err := NewRouterWithConfig(ts, identity, storagePath, policyConfig)
	mustTest(t, err)
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func mustTestNewRouterFromConfig(t *testing.T, ts rns.Transport, cfg RouterConfig) *Router {
	t.Helper()
	router, err := NewRouterFromConfig(ts, cfg)
	mustTest(t, err)
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	return router
}

// mustTestSetupDirectLink pre-populates router.directLinks with a fake active
// link for the given destination hash and mocks the link-related function
// seams so ProcessOutbound's MethodDirect path sends over the link without
// trying to establish a real one. Returns the fake link so tests can assert
// on it.
func mustTestSetupDirectLink(t *testing.T, router *Router, ts rns.Transport, dest *rns.Destination) *rns.Link {
	t.Helper()
	link, err := rns.NewLink(ts, dest)
	mustTest(t, err)
	router.directLinks[string(dest.Hash)] = link
	router.linkStatus = func(l *rns.Link) int {
		if l == link {
			return rns.LinkActive
		}
		return l.GetStatus()
	}
	router.teardownLink = func(*rns.Link) {}
	return link
}
