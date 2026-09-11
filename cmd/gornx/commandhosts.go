// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file glues the command plugin host to gornx's listen mode: it
// resolves the plugins directory, loads every <cmd>.wasm plugin, and adapts
// plugin-backed commands onto the rnx result array. It compiles for both the
// stub and the wago build; the per-build behavior lives in plugins-stub.go
// and plugins-wago.go.

package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// defaultPluginTimeout is the per-invocation plugin execution budget.
const defaultPluginTimeout = 2 * time.Second

// resolveRnxPluginsDir mirrors resolveAllowedIdentitiesPath's candidate
// order (/etc/rnx, ~/.config/rnx, ~/.rnx) with /plugins appended, returning
// the first existing plugins directory or "" when none exists (no plugins —
// every command keeps the raw-exec path).
func resolveRnxPluginsDir(home string) string {
	for _, candidate := range []string{
		"/etc/rnx/plugins",
		filepath.Join(home, ".config", "rnx", "plugins"),
		filepath.Join(home, ".rnx", "plugins"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// commandHosts maps a command name to its loaded plugin host. The plugin
// file's base name (minus .wasm) is the command it serves.
type commandHosts struct {
	hosts map[string]*PluginHost
	logf  func(format string, args ...any)
}

// newCommandHosts scans dir for *.wasm plugins and loads one host per file,
// keyed by the file's base name. Load failures are logged and skipped so a
// broken plugin never keeps the daemon down.
func newCommandHosts(dir string, timeout time.Duration, logf func(format string, args ...any)) *commandHosts {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return &commandHosts{logf: logf}
	}
	ch := &commandHosts{hosts: map[string]*PluginHost{}, logf: logf}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".wasm" {
			continue
		}
		h := NewPluginHost(timeout, logf)
		if err := h.LoadPlugin(filepath.Join(dir, entry.Name())); err != nil {
			logf("plugin load failed for %v: %v", entry.Name(), err)
			h.Close()
			continue
		}
		ch.hosts[pluginNameFromPath(entry.Name())] = h
	}
	return ch
}

// pluginNameFromPath strips the .wasm extension and returns the base name,
// which also names the command the plugin serves and its KV scope.
func pluginNameFromPath(path string) string {
	base := filepath.Base(path)
	return base[:len(base)-len(filepath.Ext(base))]
}

// forCommand returns the plugin host for a command name, or nil when no
// plugin serves it (the caller then keeps the raw-exec path).
func (ch *commandHosts) forCommand(cmd string) *PluginHost {
	if ch == nil || ch.hosts == nil {
		return nil
	}
	return ch.hosts[strings.ToLower(cmd)]
}

// close releases every loaded plugin host.
func (ch *commandHosts) close() {
	if ch == nil {
		return
	}
	var names []string
	for name := range ch.hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ch.hosts[name].Close()
	}
	ch.hosts = nil
}

// execWasmCommand runs a plugin-backed command and maps the sandboxed
// execution onto the rnx result array
// [executed, returncode, stdout, stderr, stdout_len, stderr_len, started,
// concluded]: a successful plugin run yields executed=true with
// returncode=0 and the plugin's response bytes as stdout; a plugin failure
// (including a deadline preemption) yields executed=false, matching the
// "command not executed" contract.
func execWasmCommand(host *PluginHost, cmdStr string, stdinBytes []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time, timeout float64, stdoutLimit *int, logf func(format string, args ...any)) any {
	started := float64(time.Now().UnixNano()) / 1e9
	result := []any{
		false, // 0: Command was executed
		nil,   // 1: Return value
		nil,   // 2: Stdout
		nil,   // 3: Stderr
		nil,   // 4: Total stdout length
		nil,   // 5: Total stderr length
		started,
		nil, // 7: Concluded
	}

	req := map[string]any{
		"command":      cmdStr,
		"requested_at": requestedAt.Unix(),
	}
	if len(stdinBytes) > 0 {
		req["stdin"] = string(stdinBytes)
	}
	if len(linkID) > 0 {
		req["link_id"] = hex.EncodeToString(linkID)
	}
	if remoteIdentity != nil && len(remoteIdentity.Hash) > 0 {
		req["remote_identity"] = hex.EncodeToString(remoteIdentity.Hash)
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		logf("plugin request marshal: %v", err)
		return result
	}

	budget := defaultPluginTimeout
	if timeout > 0 && time.Duration(timeout*float64(time.Second)) < budget {
		budget = time.Duration(timeout * float64(time.Second))
	}
	out, err := host.HandleCommand(reqBytes, budget)
	concluded := float64(time.Now().UnixNano()) / 1e9
	result[7] = concluded
	if err != nil {
		if logf != nil {
			logf("plugin execution of %q failed: %v", cmdStr, err)
		}
		return result
	}

	result[0] = true
	result[1] = int64(0)
	result[3] = []byte{}
	result[5] = int64(0)
	if stdoutLimit != nil && len(out) > *stdoutLimit {
		if *stdoutLimit == 0 {
			result[2] = []byte{}
		} else {
			result[2] = append([]byte{}, out[:*stdoutLimit]...)
		}
	} else {
		result[2] = append([]byte{}, out...)
	}
	result[4] = int64(len(out))
	return result
}
