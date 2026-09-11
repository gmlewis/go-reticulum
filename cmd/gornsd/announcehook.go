// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file glues the announce observer host to gornsd's transport: it
// resolves the plugins directory, loads every *.wasm observer plugin, and
// registers one announce handler that serializes incoming announces and
// dispatches them to the loaded hosts. It compiles for both the stub and the
// wago build; the per-build behavior lives in plugins-stub.go and
// plugins-wago.go.

package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// announceTimeout is the per-invocation plugin execution budget.
const announceTimeout = 2 * time.Second

// announceEvent is the serialized payload passed to an observer plugin's
// on_announce export. It carries only owned leaf data; app_data is a lossy
// UTF-8 string, and hashes are hex.
type announceEvent struct {
	DestinationHash string  `json:"destination_hash,omitempty"`
	IdentityHash    string  `json:"identity_hash,omitempty"`
	AppData         string  `json:"app_data,omitempty"`
	IsPathResponse  bool    `json:"is_path_response,omitempty"`
	ReceivedAt      float64 `json:"received_at"`
}

// announceEventJSON serializes one incoming announce for the observer
// plugins.
func announceEventJSON(destinationHash []byte, announcedIdentity *rns.Identity, appData []byte, isPathResponse bool, receivedAt float64) []byte {
	evt := announceEvent{
		AppData:        string(appData),
		IsPathResponse: isPathResponse,
		ReceivedAt:     receivedAt,
	}
	if len(destinationHash) > 0 {
		evt.DestinationHash = hex.EncodeToString(destinationHash)
	}
	if announcedIdentity != nil && len(announcedIdentity.Hash) > 0 {
		evt.IdentityHash = hex.EncodeToString(announcedIdentity.Hash)
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return []byte("{}")
	}
	return data
}

// newAnnounceHosts scans dir for *.wasm observer plugins and returns one
// host per successfully loaded plugin, in file-name order. Load failures are
// logged and skipped so a broken plugin never keeps the daemon down.
func newAnnounceHosts(dir string, timeout time.Duration, logf func(format string, args ...any)) []*ObserverHost {
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

	var hosts []*ObserverHost
	for _, name := range names {
		h := NewObserverHost(timeout, logf)
		if err := h.LoadPlugin(filepath.Join(dir, name)); err != nil {
			logf("observer load failed for %v: %v", name, err)
			h.Close()
			continue
		}
		hosts = append(hosts, h)
	}
	return hosts
}

// setupAnnounceHosts loads the observer plugins from configDir/plugins and
// registers a single announce handler on the transport that dispatches every
// incoming announce to the loaded hosts. With no hosts loaded it registers
// nothing and returns nil. It returns the loaded hosts so the caller can
// release them at shutdown.
func setupAnnounceHosts(ts rns.Transport, configDir string, logger *rns.Logger) []*ObserverHost {
	pluginsDir := filepath.Join(configDir, "plugins")
	logf := func(format string, args ...any) { logger.Info("observers: "+format, args...) }
	hosts := newAnnounceHosts(pluginsDir, announceTimeout, logf)
	if len(hosts) == 0 {
		return nil
	}
	ts.RegisterAnnounceHandler(&rns.AnnounceHandler{
		ReceivedAnnounceWithContext: func(destinationHash []byte, announcedIdentity *rns.Identity, appData []byte, isPathResponse bool) {
			data := announceEventJSON(destinationHash, announcedIdentity, appData, isPathResponse, float64(time.Now().UnixNano())/1e9)
			for _, h := range hosts {
				if !h.Active() {
					continue
				}
				if err := h.HandleAnnounce(data); err != nil {
					logf("observer error on announce from %v: %v", hex.EncodeToString(destinationHash), err)
				}
			}
		},
	})
	logf("%v announce observer plugin(s) active", len(hosts))
	return hosts
}

// closeAnnounceHosts releases every loaded observer host.
func closeAnnounceHosts(hosts []*ObserverHost) {
	for _, h := range hosts {
		h.Close()
	}
}
