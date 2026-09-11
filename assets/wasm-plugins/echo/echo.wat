;; echo.wat — a "slash command" / remote-command plugin for the Go Reticulum
;; tools (works with gorrcd and gornx).
;;
;; What it does: any unrecognized command whose name matches this plugin's
;; file name (echo.wasm -> /echo for gorrcd, "echo ..." for gornx) is run
;; inside the sandboxed wasm runtime. The plugin receives a JSON request and
;; returns bytes that become the command's output. This example echoes the
;; request JSON back, which shows exactly what metadata the host passes.
;;
;; Build:  wat2wasm echo.wat -o echo.wasm
;; Install (gorrcd):  cp echo.wasm ~/.rrcd/plugins/        (or $RRCD_HOME/plugins)
;; Install (gornx):   cp echo.wasm ~/.rnx/plugins/         (or ~/.config/rnx/plugins)
;; Use (gorrcd):      /echo hello world    -> NOTICE with the request JSON
;; Use (gornx):       gornx -l             -> remote "echo hello world" runs sandboxed
(module
  (memory (export "memory") 1 1)
  ;; The host asks the plugin to reserve guest memory for the request payload.
  (func (export "wagoplugin_alloc") (param i32) (result i32)
    i32.const 1024)
  ;; Optional manifest hook (unused by the current hosts; returns 32 zero bytes).
  (func (export "plugin_manifest") (result i32 i32)
    i32.const 0
    i32.const 32)
  ;; The command entry point: echo the request bytes back as the response.
  (func (export "handle_command") (param i32 i32) (result i32 i32)
    i32.const 1024
    local.get 0
    local.get 1
    memory.copy
    i32.const 1024
    local.get 1))
