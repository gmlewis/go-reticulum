// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file is the wago command plugin host, compiled with -tags wago on the
// supported desktop platforms (Linux, Darwin, and Windows on amd64/arm64).
// Each loaded plugin is a sandboxed wasm instance running in-process:
// deny-by-default host imports (rns.log plus the per-plugin KV scratch
// store), bounded linear memory and tables, and a hard per-invocation
// execution budget enforced by the runtime's interrupt mechanism.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/pluginstore"
	wago "github.com/wago-org/wago/src/wago"
)

// pluginMaxMemoryBytes caps a plugin module's linear memory at admission.
const pluginMaxMemoryBytes = 16 * 1024 * 1024

// pluginMaxTableEntries caps a plugin module's table entries at admission.
const pluginMaxTableEntries = 1024

// PluginHost hosts one sandboxed wasm command plugin in-process.
type PluginHost struct {
	mu      sync.Mutex
	rt      *wago.Runtime
	mod     *wago.Module
	inst    *wago.Instance
	store   *pluginstore.Store
	timeout time.Duration
	logf    func(format string, args ...any)
}

// NewPluginHost creates an empty plugin host with the given per-invocation
// execution budget (2s when non-positive) and optional logger.
func NewPluginHost(timeout time.Duration, logf func(format string, args ...any)) *PluginHost {
	if timeout <= 0 {
		timeout = defaultPluginTimeout
	}
	return &PluginHost{
		rt:      wago.NewRuntime(),
		timeout: timeout,
		logf:    logf,
	}
}

// LoadPlugin compiles and instantiates the wasm module at path and gives it
// a KV scratch store scoped by the plugin's base name. A host carries at
// most one plugin; a second load is refused until Close.
func (h *PluginHost) LoadPlugin(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst != nil {
		return errors.New("plugin host already carries a loaded plugin")
	}
	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("plugin read %v: %w", path, err)
	}

	pluginName := pluginNameFromPath(path)
	if h.store, err = pluginstore.New(filepath.Join(filepath.Dir(path), "data"), pluginName); err != nil {
		return fmt.Errorf("plugin store %v: %w", pluginName, err)
	}

	mod, err := h.rt.Compile(wasmBytes)
	if err != nil {
		return fmt.Errorf("plugin compile %v: %w", path, err)
	}

	inst, err := h.instantiate(mod)
	if err != nil {
		_ = mod.Close()
		return fmt.Errorf("plugin instantiate %v: %w", path, err)
	}
	h.mod, h.inst = mod, inst
	return nil
}

// instantiate wires the policy and host imports and instantiates the module.
func (h *PluginHost) instantiate(mod *wago.Module) (*wago.Instance, error) {
	policy := wago.Policy{
		MaxMemoryBytes:  pluginMaxMemoryBytes,
		MaxTableEntries: pluginMaxTableEntries,
	}
	imports := wago.Imports{
		// Deny by default: only explicitly wired capabilities reach the
		// guest. rns.log forwards a (ptr, len) message to the host logger.
		"rns.log": wago.HostFunc(func(m wago.HostModule, params, results []uint64) {
			logf := h.logf
			if logf == nil {
				return
			}
			mem := m.Memory()
			ptr, length := uint32(params[0]), uint32(params[1])
			if int(ptr)+int(length) > len(mem) || length == 0 {
				return
			}
			logf("plugin: %s", string(mem[ptr:ptr+length]))
		}),
	}
	h.addStoreImports(imports)
	instantiateCtx, cancel := context.WithTimeout(context.Background(), defaultPluginTimeout)
	defer cancel()
	return h.rt.Instantiate(instantiateCtx, mod, wago.WithPolicy(policy), wago.WithImports(imports))
}

// Active reports whether a plugin is loaded.
func (h *PluginHost) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inst != nil
}

// addStoreImports wires the plugin's KV scratch store into the import
// surface: rns.kv_set stores a value (status 0 = ok, 1 = error) and
// rns.kv_get reads one (n = bytes written, 0 = missing key, -1 = output
// buffer too small; nothing is written partially). Keys and pointers are
// bounds-checked against guest memory.
func (h *PluginHost) addStoreImports(imports wago.Imports) {
	store := h.store
	imports["rns.kv_set"] = wago.HostFunc(func(m wago.HostModule, params, results []uint64) {
		results[0] = 1
		if store == nil {
			return
		}
		mem := m.Memory()
		kPtr, kLen, vPtr, vLen := uint32(params[0]), uint32(params[1]), uint32(params[2]), uint32(params[3])
		if int(kPtr)+int(kLen) > len(mem) || int(vPtr)+int(vLen) > len(mem) {
			return
		}
		if err := store.Set(string(mem[kPtr:kPtr+kLen]), mem[vPtr:vPtr+vLen]); err != nil {
			return
		}
		results[0] = 0
	})
	imports["rns.kv_get"] = wago.HostFunc(func(m wago.HostModule, params, results []uint64) {
		results[0] = 0
		if store == nil {
			return
		}
		mem := m.Memory()
		kPtr, kLen, outPtr, outCap := uint32(params[0]), uint32(params[1]), uint32(params[2]), uint32(params[3])
		if int(kPtr)+int(kLen) > len(mem) {
			results[0] = 0xFFFFFFFF // -1 as i32
			return
		}
		value, ok, err := store.Get(string(mem[kPtr : kPtr+kLen]))
		if err != nil || !ok {
			return
		}
		if int(outCap) < len(value) || int(outPtr)+int(outCap) > len(mem) {
			results[0] = 0xFFFFFFFF // -1 as i32: caller retries with a bigger buffer
			return
		}
		copy(mem[outPtr:outPtr+uint32(len(value))], value)
		results[0] = uint64(uint32(len(value)))
	})
}

// invoke calls a named export under the given execution budget. It is the
// direct path the timeout tests exercise.
func (h *PluginHost) invoke(export string, timeout time.Duration, args ...wago.Value) ([]wago.Value, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return nil, errors.New("plugin host has no loaded plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.inst.Call(ctx, export, args...)
}

// HandleCommand runs the plugin's handle_command export: the request bytes
// are allocated in guest memory through wagoplugin_alloc, the response
// (ptr, len) pair is read back, and the response bytes (the command's
// stdout) are returned.
func (h *PluginHost) HandleCommand(req []byte, timeout time.Duration) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return nil, errors.New("plugin host has no loaded plugin")
	}
	if timeout <= 0 {
		timeout = h.timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	allocRes, err := h.inst.Call(ctx, "wagoplugin_alloc", wago.ValueI32(int32(len(req))))
	if err != nil {
		return nil, fmt.Errorf("plugin alloc: %w", err)
	}
	if len(allocRes) < 1 {
		return nil, errors.New("plugin wagoplugin_alloc must return a pointer")
	}
	inPtr := uint32(allocRes[0].I32())
	if !h.inst.Write(inPtr, req) {
		return nil, fmt.Errorf("plugin memory write at %v (%v bytes) failed", inPtr, len(req))
	}

	res, err := h.inst.Call(ctx, "handle_command", allocRes[0], wago.ValueI32(int32(len(req))))
	if err != nil {
		return nil, fmt.Errorf("plugin handle_command: %w", err)
	}
	if len(res) < 2 {
		return nil, errors.New("plugin handle_command must return (ptr, len)")
	}
	outPtr, outLen := uint32(res[0].I32()), uint32(res[1].I32())
	outBytes, ok := h.inst.Read(outPtr, outLen)
	if !ok {
		return nil, fmt.Errorf("plugin memory read at %v (%v bytes) failed", outPtr, outLen)
	}
	return outBytes, nil
}

// Close releases the instance, module, and runtime.
func (h *PluginHost) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst != nil {
		_ = h.inst.Close()
		h.inst = nil
	}
	if h.mod != nil {
		_ = h.mod.Close()
		h.mod = nil
	}
	if h.rt != nil {
		_ = h.rt.Close()
		h.rt = nil
	}
}
