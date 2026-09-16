# WebAssembly (Wasm) Plugin Sandbox

Go Reticulum includes an optional, highly secure **WebAssembly (Wasm) Plugin Sandbox** (`-tags=wago`), providing safe, isolated runtime extensibility without external Python scripts.

---

## Why WebAssembly?

In Reticulum deployments:
1. **Safety First**: Untrusted third-party extensions must never crash the host network stack or compromise private cryptographic keys.
2. **Deterministic Isolation**: Wasm modules execute in strict linear memory sandboxes without direct access to the filesystem, network, or host memory.
3. **Language Agnostic**: Plugins can be authored in **Go**, **Rust**, **C/C++**, or **Zig** and compiled to standard `.wasm` bytecode.

---

## Plugin ABI Contract

Plugins export standard entry points that the host runtime invokes:

```c
// Initialize the plugin with configuration
int32_t plugin_init(const char* config_ptr, int32_t config_len);

// Process an incoming event or message
int32_t plugin_handle(const char* input_ptr, int32_t input_len, char* out_buf, int32_t out_max);
```

Host functions provide controlled, read-only telemetry:
- `host_get_time()`: Return current monotonic timestamp.
- `host_log(level, ptr, len)`: Write a log entry through the host logger.

---

## Compiling a Plugin in Go

Using TinyGo for ultra-compact WebAssembly binaries:

```bash
tinygo build -o myplugin.wasm -target=wasi myplugin.go
```

The resulting `.wasm` binary can then be loaded by compatible Reticulum daemons.
