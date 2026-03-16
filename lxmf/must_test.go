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
	id, err := rns.NewIdentity(createKeys)
	mustTest(t, err)
	return id
}

func mustTestNewDestination(t *testing.T, identity *rns.Identity, direction, destType int, appName string, aspects ...string) *rns.Destination {
	t.Helper()
	dest, err := rns.NewDestination(identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewMessage(t *testing.T, destination, source *rns.Destination, content, title string, fields map[any]any) *Message {
	t.Helper()
	msg, err := NewMessage(destination, source, content, title, fields)
	mustTest(t, err)
	return msg
}

func mustTestNewRouter(t *testing.T, identity *rns.Identity, storagePath string) *Router {
	t.Helper()
	router, err := NewRouter(identity, storagePath)
	mustTest(t, err)
	return router
}

func mustTestNewRouterWithConfig(t *testing.T, identity *rns.Identity, storagePath string, policyConfig map[string]any) *Router {
	t.Helper()
	router, err := NewRouterWithConfig(identity, storagePath, policyConfig)
	mustTest(t, err)
	return router
}

func mustTestNewRouterFromConfig(t *testing.T, cfg RouterConfig) *Router {
	t.Helper()
	router, err := NewRouterFromConfig(cfg)
	mustTest(t, err)
	return router
}
