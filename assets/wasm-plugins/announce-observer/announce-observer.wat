;; announce-observer.wat — network announce observer plugin for gornsd.
;;
;; The daemon registers one announce handler that serializes every incoming
;; network announce (destination/identity hashes, app_data, path-response
;; flag, receive time) as JSON and passes it to each loaded observer plugin's
;; on_announce export. Status 0 means acknowledged; any nonzero status is
;; logged as a plugin-reported issue (never fatal).
;;
;; This example acknowledges every announce. Extend it (e.g. in TinyGo) to
;; keep node census state via the rns.kv_set / rns.kv_get imports.
;;
;; Build:  wat2wasm announce-observer.wat -o announce-observer.wasm
;; Install: cp announce-observer.wasm ~/.reticulum/plugins/
;; Use:     run gornsd; every network announce is delivered to on_announce.
(module
  (memory (export "memory") 1 1)
  (func (export "wagoplugin_alloc") (param i32) (result i32)
    i32.const 1024)
  (func (export "on_announce") (param i32 i32) (result i32)
    i32.const 0))
