;; filter-accept.wat — inbound LXMF delivery filter for golxmd.
;;
;; golxmd serializes every inbound LXMF message (destination/source hashes,
;; title, content, timestamp) as JSON and passes it to each loaded filter
;; plugin's filter_inbound export. Return 1 to accept the message (the normal
;; delivery handler runs) or 0 to drop it (the message is never written).
;; Any other return value is treated as a plugin error and fails open.
;;
;; This example accepts every message. Replace it (e.g. built with TinyGo)
;; with a real content filter; the drop variant differs only in the returned
;; constant (i32.const 0).
;;
;; Build:  wat2wasm filter-accept.wat -o filter-accept.wasm
;; Install: cp filter-accept.wasm ~/.lxmd/plugins/   (or your golxmd config dir)
;; Use:     run golxmd; every inbound message passes the filter.
(module
  (memory (export "memory") 1 1)
  (func (export "wagoplugin_alloc") (param i32) (result i32)
    i32.const 1024)
  (func (export "filter_inbound") (param i32 i32) (result i32)
    i32.const 1))
