// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file is the wago announce observer host, compiled with -tags wago on
// the supported desktop platforms (Linux, Darwin, and Windows on
// amd64/arm64). Each loaded plugin is a sandboxed wasm instance running
// in-process: deny-by-default host imports (rns.log plus the per-plugin KV
// scratch store), bounded linear memory and tables, and a hard
// per-invocation execution budget enforced by the runtime's interrupt
// mechanism.

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

// observerMaxMemoryBytes caps an observer module's linear memory at
// admission.
const observerMaxMemoryBytes = 16 * 1024 * 1024

// observerMaxTableEntries caps an observer module's table entries at
// admission.
const observerMaxTableEntries = 1024

// defaultObserverTimeout is the per-invocation execution budget applied
// when the caller passes a non-positive timeout.
const defaultObserverTimeout = 2 * time.Second

// ObserverHost hosts one sandboxed wasm announce observer in-process.
type ObserverHost struct {
	mu      sync.Mutex
	rt      *wago.Runtime
	mod     *wago.Module
	inst    *wago.Instance
	store   *pluginstore.Store
	timeout time.Duration
	logf    func(format string, args ...any)
}

// NewObserverHost creates an empty observer host with the given
// per-invocation execution budget (2s when non-positive) and optional
// logger.
func NewObserverHost(timeout time.Duration, logf func(format string, args ...any)) *ObserverHost {
	if timeout <= 0 {
		timeout = defaultObserverTimeout
	}
	return &ObserverHost{
		rt:      wago.NewRuntime(),
		timeout: timeout,
		logf:    logf,
	}
}

// LoadPlugin compiles and instantiates the wasm module at path and gives it
// a KV scratch store scoped by the plugin's base name. A host carries at
// most one plugin; a second load is refused until Close.
func (h *ObserverHost) LoadPlugin(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst != nil {
		return errors.New("observer host already carries a loaded plugin")
	}
	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("observer read %v: %w", path, err)
	}

	pluginName := observerNameFromPath(path)
	if h.store, err = pluginstore.New(filepath.Join(filepath.Dir(path), "data"), pluginName); err != nil {
		return fmt.Errorf("observer store %v: %w", pluginName, err)
	}

	mod, err := h.rt.Compile(wasmBytes)
	if err != nil {
		return fmt.Errorf("observer compile %v: %w", path, err)
	}

	inst, err := h.instantiate(mod)
	if err != nil {
		_ = mod.Close()
		return fmt.Errorf("observer instantiate %v: %w", path, err)
	}
	h.mod, h.inst = mod, inst
	return nil
}

// observerNameFromPath strips the .wasm extension and returns the base
// name, which also scopes the plugin's KV store.
func observerNameFromPath(path string) string {
	base := filepath.Base(path)
	return base[:len(base)-len(filepath.Ext(base))]
}

// instantiate wires the policy and host imports and instantiates the module.
func (h *ObserverHost) instantiate(mod *wago.Module) (*wago.Instance, error) {
	policy := wago.Policy{
		MaxMemoryBytes:  observerMaxMemoryBytes,
		MaxTableEntries: observerMaxTableEntries,
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
			logf("observer plugin: %s", string(mem[ptr:ptr+length]))
		}),
	}
	h.addStoreImports(imports)
	instantiateCtx, cancel := context.WithTimeout(context.Background(), defaultObserverTimeout)
	defer cancel()
	return h.rt.Instantiate(instantiateCtx, mod, wago.WithPolicy(policy), wago.WithImports(imports))
}

// addStoreImports wires the plugin's KV scratch store into the import
// surface: rns.kv_set stores a value (status 0 = ok, 1 = error) and
// rns.kv_get reads one (n = bytes written, 0 = missing key, -1 = output
// buffer too small; nothing is written partially). Keys and pointers are
// bounds-checked against guest memory.
func (h *ObserverHost) addStoreImports(imports wago.Imports) {
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

// Active reports whether an observer plugin is loaded.
func (h *ObserverHost) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inst != nil
}

// invoke calls a named export under the given execution budget. It is the
// direct path the timeout tests exercise.
func (h *ObserverHost) invoke(export string, timeout time.Duration, args ...wago.Value) ([]wago.Value, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return nil, errors.New("observer host has no loaded plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.inst.Call(ctx, export, args...)
}

// HandleAnnounce runs the plugin's on_announce export with the serialized
// announce event and reports an error for plugin failures or a nonzero
// status (0 = acknowledged).
func (h *ObserverHost) HandleAnnounce(event []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return errors.New("observer host has no loaded plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	allocRes, err := h.inst.Call(ctx, "wagoplugin_alloc", wago.ValueI32(int32(len(event))))
	if err != nil {
		return fmt.Errorf("observer alloc: %w", err)
	}
	if len(allocRes) < 1 {
		return errors.New("plugin wagoplugin_alloc must return a pointer")
	}
	inPtr := uint32(allocRes[0].I32())
	if !h.inst.Write(inPtr, event) {
		return fmt.Errorf("observer memory write at %v (%v bytes) failed", inPtr, len(event))
	}

	res, err := h.inst.Call(ctx, "on_announce", allocRes[0], wago.ValueI32(int32(len(event))))
	if err != nil {
		return fmt.Errorf("observer on_announce: %w", err)
	}
	if len(res) < 1 {
		return errors.New("plugin on_announce must return a status")
	}
	if status := res[0].I32(); status != 0 {
		return fmt.Errorf("plugin on_announce reported status %v", status)
	}
	return nil
}

// Close releases the instance, module, and runtime.
func (h *ObserverHost) Close() {
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
