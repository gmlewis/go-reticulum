// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file is the wago plugin host, compiled with -tags wago on the
// supported desktop platforms (Linux, Darwin, and Windows on amd64/arm64).
// Each loaded plugin is a sandboxed wasm instance running in-process:
// deny-by-default host imports, bounded linear memory and tables, and a
// hard per-invocation execution budget enforced by the runtime's interrupt
// mechanism (loop safepoints or platform signals).

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	wago "github.com/wago-org/wago/src/wago"
)

// pluginMaxMemoryBytes caps a plugin module's linear memory at admission.
const pluginMaxMemoryBytes = 16 * 1024 * 1024

// pluginMaxTableEntries caps a plugin module's table entries at admission.
const pluginMaxTableEntries = 1024

// defaultPluginTimeout is the per-invocation execution budget applied when
// the caller passes a non-positive timeout.
const defaultPluginTimeout = 2 * time.Second

// PluginHost hosts one sandboxed wasm plugin in-process.
type PluginHost struct {
	mu      sync.Mutex
	rt      *wago.Runtime
	mod     *wago.Module
	inst    *wago.Instance
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

// LoadPlugin compiles and instantiates the wasm module at path. A host
// carries at most one plugin; a second load is refused until Close.
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

// invoke calls a named export under the host's execution budget and
// validates the argument and result plumbing. It is the direct path the
// timeout tests exercise.
func (h *PluginHost) invoke(export string, args ...wago.Value) ([]wago.Value, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return nil, errors.New("plugin host has no loaded plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()
	return h.inst.Call(ctx, export, args...)
}

// HandleCommand runs the plugin's handle_command export: the command line
// is allocated in guest memory through wagoplugin_alloc, the response
// (ptr, len) pair is read back, and the response bytes are returned.
func (h *PluginHost) HandleCommand(cmdLine string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.inst == nil {
		return "", errors.New("plugin host has no loaded plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	data := []byte(cmdLine)
	allocRes, err := h.inst.Call(ctx, "wagoplugin_alloc", wago.ValueI32(int32(len(data))))
	if err != nil {
		return "", fmt.Errorf("plugin alloc: %w", err)
	}
	if len(allocRes) < 1 {
		return "", errors.New("plugin wagoplugin_alloc must return a pointer")
	}
	inPtr := uint32(allocRes[0].I32())
	if !h.inst.Write(inPtr, data) {
		return "", fmt.Errorf("plugin memory write at %v (%v bytes) failed", inPtr, len(data))
	}

	res, err := h.inst.Call(ctx, "handle_command", allocRes[0], wago.ValueI32(int32(len(data))))
	if err != nil {
		return "", fmt.Errorf("plugin handle_command: %w", err)
	}
	if len(res) < 2 {
		return "", errors.New("plugin handle_command must return (ptr, len)")
	}
	outPtr, outLen := uint32(res[0].I32()), uint32(res[1].I32())
	outBytes, ok := h.inst.Read(outPtr, outLen)
	if !ok {
		return "", fmt.Errorf("plugin memory read at %v (%v bytes) failed", outPtr, outLen)
	}
	return string(outBytes), nil
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
