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

A plugin is an ordinary WebAssembly module. The host calls these exports; every
pointer is an offset into the module's own linear memory:

| Export | Signature | Purpose |
|--------|-----------|---------|
| `wagoplugin_alloc` | `(len i32) -> (ptr i32)` | Reserve guest memory for the host's request payload |
| `handle_command` | `(req_ptr i32, req_len i32) -> (resp_ptr i32, resp_len i32)` | `gorrcd` / `gornx`: run a command; the response bytes become the output |
| `render_page` | `(req_ptr i32, req_len i32) -> (resp_ptr i32, resp_len i32)` | `gonomadnet`: render a `.wasm` page to Micron markup |
| `filter_inbound` | `(msg_ptr i32, msg_len i32) -> (action i32)` | `golxmd`: 1 accepts the message, 0 drops it |
| `on_announce` | `(ann_ptr i32, ann_len i32) -> (status i32)` | `gornsd`: observe an announce; 0 acknowledges it |

A plugin implements the export its host calls, and each host wires only that one.

Host capabilities are wired one by one as well, and a module importing anything
else fails to instantiate. Wired today, all in the `rns` namespace:

- `rns.log(ptr, len)` — forward a message to the host logger.
- `rns.kv_set(key_ptr, key_len, val_ptr, val_len) -> status` — write to a
  per-plugin scratch store.
- `rns.kv_get(key_ptr, key_len, out_ptr, out_cap) -> n` — read it back. The store
  is capped at 10 MiB per plugin, and its keys are filenames under
  `<plugins-dir>/data/<plugin>/`.

---

## Compiling a Plugin in Go

Using TinyGo for ultra-compact WebAssembly binaries:

```bash
tinygo build -o myplugin.wasm -target=wasi myplugin.go
```

The resulting `.wasm` binary can then be loaded by compatible Reticulum daemons.
