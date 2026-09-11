// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file is the wago LXMF filter host, compiled with -tags wago on the
// supported desktop platforms (Linux, Darwin, and Windows on amd64/arm64).
// Each loaded plugin is a sandboxed wasm instance running in-process:
// deny-by-default host imports, bounded linear memory and tables, and a hard
// per-invocation execution budget enforced by the runtime's interrupt
// mechanism (loop safepoints or platform signals).

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/pluginstore"
	wago "github.com/wago-org/wago/src/wago"
)

// filterMaxMemoryBytes caps a filter module's linear memory at admission.
const filterMaxMemoryBytes = 16 * 1024 * 1024

// filterMaxTableEntries caps a filter module's table entries at admission.
const filterMaxTableEntries = 1024

// defaultFilterTimeout is the per-invocation execution budget applied when
// the caller passes a non-positive timeout.
const defaultFilterTimeout = 2 * time.Second

// FilterHost hosts one sandboxed wasm filter plugin in-process.
type FilterHost struct {
	mu      sync.Mutex
	rt      *wago.Runtime
	mod     *wago.Module
	inst    *wago.Instance
	store   *pluginstore.Store
	timeout time.Duration
	logf    func(format string, args ...any)
}

// NewFilterHost creates an empty filter host with the given per-invocation
// execution budget (2s when non-positive) and optional logger.
func NewFilterHost(timeout time.Duration, logf func(format string, args ...any)) *FilterHost {
	if timeout <= 0 {
		timeout = defaultFilterTimeout
	}
	return &FilterHost{
		rt:      wago.NewRuntime(),
		timeout: timeout,
		logf:    logf,
	}
}

// LoadPlugin compiles and instantiates the wasm module at path. A host
// carries at most one plugin; a second load is refused until Close.
func (h *FilterHost) LoadPlugin(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst != nil {
		return errors.New("filter host already carries a loaded plugin")
	}
	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("filter read %v: %w", path, err)
	}

	// Each filter gets its own KV scratch store scoped by the plugin file's
	// base name, backed by <pluginsDir>/data.
	pluginName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if h.store, err = pluginstore.New(filepath.Join(filepath.Dir(path), "data"), pluginName); err != nil {
		return fmt.Errorf("filter store %v: %w", pluginName, err)
	}

	mod, err := h.rt.Compile(wasmBytes)
	if err != nil {
		return fmt.Errorf("filter compile %v: %w", path, err)
	}

	inst, err := h.instantiate(mod)
	if err != nil {
		_ = mod.Close()
		return fmt.Errorf("filter instantiate %v: %w", path, err)
	}
	h.mod, h.inst = mod, inst
	return nil
}

// instantiate wires the policy and host imports and instantiates the module.
func (h *FilterHost) instantiate(mod *wago.Module) (*wago.Instance, error) {
	policy := wago.Policy{
		MaxMemoryBytes:  filterMaxMemoryBytes,
		MaxTableEntries: filterMaxTableEntries,
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
			logf("filter plugin: %s", string(mem[ptr:ptr+length]))
		}),
	}
	h.addStoreImports(imports)
	instantiateCtx, cancel := context.WithTimeout(context.Background(), defaultFilterTimeout)
	defer cancel()
	return h.rt.Instantiate(instantiateCtx, mod, wago.WithPolicy(policy), wago.WithImports(imports))
}

// Active reports whether a filter plugin is loaded.
func (h *FilterHost) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inst != nil
}

// addStoreImports wires the filter's KV scratch store into the import
// surface: rns.kv_set stores a value (status 0 = ok, 1 = error) and
// rns.kv_get reads one (n = bytes written, 0 = missing key, -1 = output
// buffer too small; nothing is written partially). Keys and pointers are
// bounds-checked against guest memory.
func (h *FilterHost) addStoreImports(imports wago.Imports) {
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

// invoke calls a named export under the host's execution budget. It is the
// direct path the timeout tests exercise.
func (h *FilterHost) invoke(export string, args ...wago.Value) ([]wago.Value, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return nil, errors.New("filter host has no loaded plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()
	return h.inst.Call(ctx, export, args...)
}

// HandleFilter runs the plugin's filter_inbound export: the serialized
// message is allocated in guest memory through wagoplugin_alloc, the action
// result is read back, and true (pass) or false (drop) is returned. Only 0
// and 1 are valid actions; anything else is an error.
func (h *FilterHost) HandleFilter(msg []byte) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return false, errors.New("filter host has no loaded plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	allocRes, err := h.inst.Call(ctx, "wagoplugin_alloc", wago.ValueI32(int32(len(msg))))
	if err != nil {
		return false, fmt.Errorf("filter alloc: %w", err)
	}
	if len(allocRes) < 1 {
		return false, errors.New("plugin wagoplugin_alloc must return a pointer")
	}
	inPtr := uint32(allocRes[0].I32())
	if !h.inst.Write(inPtr, msg) {
		return false, fmt.Errorf("filter memory write at %v (%v bytes) failed", inPtr, len(msg))
	}

	res, err := h.inst.Call(ctx, "filter_inbound", allocRes[0], wago.ValueI32(int32(len(msg))))
	if err != nil {
		return false, fmt.Errorf("filter_inbound: %w", err)
	}
	if len(res) < 1 {
		return false, errors.New("plugin filter_inbound must return an action")
	}
	switch action := res[0].I32(); action {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("plugin filter_inbound returned unsupported action %v", action)
	}
}

// Close releases the instance, module, and runtime.
func (h *FilterHost) Close() {
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
