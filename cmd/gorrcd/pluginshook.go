// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file glues the plugin host to the hub: it scans the plugins
// directory at bring-up and adapts the loaded hosts to the rrc
// CustomHandler hook, so unknown slash commands run through the sandboxed
// plugins in-process. It compiles for both the stub and the wago build; the
// per-build behavior lives in plugins_stub.go and plugins_wago.go.

package main

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// pluginTimeout is the per-invocation plugin execution budget.
const pluginTimeout = 2 * time.Second

// newPluginHosts scans dir for *.wasm plugin files and returns one host per
// successfully loaded plugin, in file-name order. Load failures are logged
// and skipped so a broken plugin file never keeps the hub down.
func newPluginHosts(dir string, timeout time.Duration, logf func(format string, args ...any)) []*PluginHost {
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

	var hosts []*PluginHost
	for _, name := range names {
		h := NewPluginHost(timeout, logf)
		if err := h.LoadPlugin(filepath.Join(dir, name)); err != nil {
			logf("plugin load failed for %v: %v", name, err)
			h.Close()
			continue
		}
		hosts = append(hosts, h)
	}
	return hosts
}

// pluginCommandHook returns an rrc CustomHandler hook that forwards unknown
// slash commands to the loaded plugin hosts. The first host that returns a
// non-empty response wins: the response text is emitted to the requesting
// link as a NOTICE and the command counts as handled. An error is emitted
// the same way and counts as handled; with no active host the hook returns
// false and the hub keeps its normal unknown-command behavior.
func pluginCommandHook(mh *rrc.MessageHelper, hosts []*PluginHost, logf func(format string, args ...any)) func(*rns.Link, []byte, *string, []string, *rrc.OutgoingList) bool {
	return func(link *rns.Link, _ []byte, room *string, parts []string, outgoing *rrc.OutgoingList) bool {
		handled := false
		for _, h := range hosts {
			if !h.Active() {
				continue
			}
			resp, err := h.HandleCommand(joinParts(parts))
			switch {
			case err != nil:
				if mh != nil {
					mh.EmitNotice(outgoing, link, room, "plugin error: "+err.Error())
				}
				if logf != nil {
					logf("plugin command %q failed: %v", joinParts(parts), err)
				}
				handled = true
			case resp != "":
				if mh != nil {
					mh.EmitNotice(outgoing, link, room, resp)
				}
				handled = true
			}
		}
		return handled
	}
}

// joinParts re-joins the parsed command fields with single spaces; it is
// the request payload passed to the plugin's handle_command export.
func joinParts(parts []string) string {
	return strings.Join(parts, " ")
}

// setupPluginHosts builds the plugin hosts under the state home's plugins
// directory and installs the custom-command hook on the hub service. It
// returns the loaded hosts (empty when none loaded or the wago runtime is
// not linked) so the caller can release them at shutdown.
func setupPluginHosts(svc *rrc.HubService) []*PluginHost {
	pluginsDir := filepath.Join(rrc.DefaultRrcdDir(), "plugins")
	logf := func(format string, args ...any) { log.Printf("plugins: "+format, args...) }
	hosts := newPluginHosts(pluginsDir, pluginTimeout, logf)
	if len(hosts) == 0 {
		return nil
	}
	svc.SetCustomCommandHandler(pluginCommandHook(svc.MessageHelper, hosts, logf))
	return hosts
}

// closePluginHosts releases every loaded plugin host at shutdown.
func closePluginHosts(hosts []*PluginHost) {
	for _, h := range hosts {
		h.Close()
	}
}
