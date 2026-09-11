// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file glues the sandboxed LXMF filter plugins to the golxmd delivery
// callback: it scans the plugins directory at bring-up and wraps the router's
// delivery callback so inbound messages run through the plugins before the
// normal delivery handler. It compiles for both the stub and the wago build;
// the per-build behavior lives in plugins_stub.go and plugins_wago.go.

package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
)

// filterTimeout is the per-invocation plugin execution budget.
const filterTimeout = 2 * time.Second

// filterMessage is the serialized payload passed to a filter plugin's
// filter_inbound export. It carries only owned leaf data (no lxmf.Message
// references); content and title are lossy UTF-8 strings, and hashes are hex.
type filterMessage struct {
	DestinationHash string  `json:"destination_hash,omitempty"`
	SourceHash      string  `json:"source_hash,omitempty"`
	Title           string  `json:"title,omitempty"`
	Content         string  `json:"content,omitempty"`
	Timestamp       float64 `json:"timestamp"`
}

// messageToFilterJSON serializes one inbound LXMF message for the filter
// plugins.
func messageToFilterJSON(lxm *lxmf.Message) []byte {
	payload := filterMessage{Timestamp: lxm.Timestamp}
	if len(lxm.DestinationHash) > 0 {
		payload.DestinationHash = hex.EncodeToString(lxm.DestinationHash)
	}
	if len(lxm.SourceHash) > 0 {
		payload.SourceHash = hex.EncodeToString(lxm.SourceHash)
	}
	payload.Title = string(lxm.Title)
	payload.Content = string(lxm.Content)
	data, err := json.Marshal(payload)
	if err != nil {
		return []byte("{}")
	}
	return data
}

// newFilterHosts scans dir for *.wasm filter plugin files and returns one
// host per successfully loaded plugin, in file-name order. Load failures are
// logged and skipped so a broken plugin file never keeps the daemon down.
func newFilterHosts(dir string, timeout time.Duration, logf func(format string, args ...any)) []*FilterHost {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".wasm" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var hosts []*FilterHost
	for _, name := range names {
		h := NewFilterHost(timeout, logf)
		if err := h.LoadPlugin(filepath.Join(dir, name)); err != nil {
			logf("filter load failed for %v: %v", name, err)
			h.Close()
			continue
		}
		hosts = append(hosts, h)
	}
	return hosts
}

// filteredDelivery wraps the daemon's normal delivery handler with the
// loaded filter plugins. Every active host receives the serialized message;
// an explicit drop (plugin returns 0) skips the base handler, a plugin error
// fails open (the delivery proceeds and the failure is logged), and with no
// active host the delivery runs unchanged.
func filteredDelivery(base func(*lxmf.Message), hosts []*FilterHost, lxm *lxmf.Message, logf func(format string, args ...any)) {
	for _, h := range hosts {
		if !h.Active() {
			continue
		}
		accept, err := h.HandleFilter(messageToFilterJSON(lxm))
		switch {
		case err != nil:
			if logf != nil {
				logf("filter error on inbound message (failing open): %v", err)
			}
		case !accept:
			if logf != nil {
				logf("filter plugin dropped inbound message from %v", hex.EncodeToString(lxm.SourceHash))
			}
			return
		}
	}
	base(lxm)
}

// setupFilterHosts scans configDir/plugins for filter plugins and installs a
// filtering wrapper around the router's delivery callback (replacing the
// base handler, which RegisterDeliveryCallback overwrites). It returns the
// loaded hosts (empty when none loaded or the wago runtime is not linked) so
// the caller can release them at shutdown.
func setupFilterHosts(router *lxmf.Router, base func(*lxmf.Message), configDir string, logger *rns.Logger) []*FilterHost {
	pluginsDir := filepath.Join(configDir, "plugins")
	logf := func(format string, args ...any) { logger.Info("filters: "+format, args...) }
	hosts := newFilterHosts(pluginsDir, filterTimeout, logf)
	if len(hosts) == 0 {
		router.RegisterDeliveryCallback(base)
		return nil
	}
	router.RegisterDeliveryCallback(func(lxm *lxmf.Message) {
		filteredDelivery(base, hosts, lxm, logf)
	})
	return hosts
}

// closeFilterHosts releases every loaded filter host at shutdown.
func closeFilterHosts(hosts []*FilterHost) {
	for _, h := range hosts {
		h.Close()
	}
}
