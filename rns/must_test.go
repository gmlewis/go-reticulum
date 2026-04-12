// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func mustTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustTestNewIdentity(t *testing.T, createKeys bool) *Identity {
	t.Helper()
	id, err := NewIdentity(createKeys, nil)
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
	// Ensure isolation if no config exists
	configPath := filepath.Join(configDir, "config")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		writeConfig(t, configDir, "[reticulum]\nshare_instance = No\n")
	}
	ret, err := NewReticulum(ts, configDir)
	mustTest(t, err)
	return ret
}

func mustTestNewReticulumWithLogger(t *testing.T, ts Transport, configDir string, logger *Logger) *Reticulum {
	t.Helper()
	// Ensure isolation if no config exists
	configPath := filepath.Join(configDir, "config")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		writeConfig(t, configDir, "[reticulum]\nshare_instance = No\n")
	}
	ret, err := NewReticulumWithLogger(ts, configDir, logger)
	mustTest(t, err)
	return ret
}

func mustTestLogger(t *testing.T, level int) *Logger {
	t.Helper()
	logger := NewLogger()
	logger.SetLogLevel(level)
	return logger
}

func mustMsgpackPack(v any) []byte {
	data, err := msgpack.Pack(v)
	if err != nil {
		panic(err)
	}
	return data
}
