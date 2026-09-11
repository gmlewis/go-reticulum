;; kv-demo.wat — per-plugin scratch key/value store demo for gorrcd (and any
;; wago tool host). Demonstrates the rns.kv_set / rns.kv_get host imports.
;;
;; The host gives every plugin its own store directory scoped by the plugin's
;; file name under <plugins-dir>/data/<plugin>/, with a 10 MiB total quota.
;; Keys are filenames (no separators, no traversal, max 128 bytes).
;;
;; What it does: handle_command stores "v" under "k" via rns.kv_set, reads it
;; back via rns.kv_get, and returns the stored bytes as the command output —
;; proving the value round-tripped through the host's on-disk store.
;;
;; Build:  wat2wasm kv-demo.wat -o kv-demo.wasm
;; Install: cp kv-demo.wasm ~/.rrcd/plugins/
;; Use:     /kv-demo anything    -> NOTICE with "v"; the value is persisted
;;          on disk at ~/.rrcd/plugins/data/kv-demo/k
(module
  (import "rns" "kv_set" (func $kv_set (param i32 i32 i32 i32) (result i32)))
  (import "rns" "kv_get" (func $kv_get (param i32 i32 i32 i32) (result i32)))
  (memory 1 1)
  (data (i32.const 64) "k")
  (data (i32.const 96) "v")
  (func $store_and_load (result i32 i32) (local $n i32)
    ;; kv_set(key="k" @64, len 1, value="v" @96, len 1); drop the status.
    i32.const 64  i32.const 1  i32.const 96  i32.const 1
    call $kv_set
    drop
    ;; kv_get(key="k" @64, len 1, out @128, cap 32) -> n bytes written.
    i32.const 64  i32.const 1  i32.const 128  i32.const 32
    call $kv_get
    local.set $n
    ;; Return (out_ptr, n): the host reads the stored value back.
    i32.const 128
    local.get $n)
  (func (export "wagoplugin_alloc") (param i32) (result i32) i32.const 1024)
  (func (export "handle_command") (param i32 i32) (result i32 i32)
    call $store_and_load))
