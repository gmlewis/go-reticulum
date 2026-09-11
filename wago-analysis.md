# Design: In-Process wago via Per-Command Nested Modules

**Status:** authoritative architecture and TDD implementation specification  
**Repos:** `go-reticulum`, `go-nomadnet` (asic-reticulum / pocket devices out of scope)  
**Engine:** [wago](https://github.com/wago-org/wago), pinned at `v0.1.0-beta.8` (or current tagged beta)  
**Verification Date:** 2026-09-11 (engine source analyzed & native loop cancellation verified)

---

## 1. Intent & Core Constraints

Third-party extensions (chat slash commands, dynamic pages, LXMF filters, announce
observers, remote execution tools) must run in a secure, resource-bounded sandbox.
[wago](https://github.com/wago-org/wago) provides that sandbox as a pure-Go, no-CGO
JIT runtime with deny-by-default host imports and per-instance resource policy.

Simultaneously, `go-reticulum` enforces a strict repository invariant:
**The root module (`go.mod`) must NEVER take external dependencies; it is strictly Go standard library.**

The architecture that reconciles both requirements is:

> **Each tool that links wago becomes its own nested Go module under
> `cmd/<name>/`, with its own `go.mod` and `go.sum`, importing the parent
> library via a `replace` directive and embedding wago in-process.**
>
> The root `go.mod` never imports wago or any other external package.

There is **no** helper daemon, **no** stdio RPC, and **no** cross-process
plugin host. Plugin execution happens inside the same process as the tool.

---

## 2. Deep Dive Feasibility & Engine Analysis (Verified 2026-09-11)

A deep inspection of `~/src/github.com/wago-org/wago` source code and test execution confirmed:

### 2.1 Context Cancellation & Native Loop Interruption (CONFIRMED SAFE)

A central security concern for any in-process WebAssembly runtime is whether a guest plugin
with a runaway computation loop (e.g. `loop { br 0 }` without host calls) can lock up
a host goroutine or thread.

**Findings from `wago` source inspection:**
1. **Darwin / Windows / Fallbacks (`src/core/runtime/interrupt_stub.go` & `src/wago/api.go:1448`)**:
   - `wruntime.HostInterruptSupported()` returns `false` on Darwin and Windows.
   - Consequently, the Railshot compiler compiles modules with `Interruptible: !wruntime.HostInterruptSupported()` = **`true`**.
   - In `Interruptible` mode, Railshot automatically inserts **safepoint polls at every native function entry and loop header** (`src/core/compiler/backend/railshot/{amd64,arm64}/compile.go`).
   - When `in.Call(ctx, ...)` or `in.InvokeContext(ctx, ...)` runs with a deadline, `wago` arms an interrupt watcher (`startCancellationWatch`).
   - When the context deadline expires or `cancel()` is called, `wruntime.RequestInterrupt` marks the instance's active trap cell with `TrapInterrupted`.
   - The compiled loop header detects `TrapInterrupted` within **at most one loop iteration**, unwinds the native call tree along the cold trap path, resets the trap cell cleanly, and returns `context.DeadlineExceeded` or `context.Canceled`.
2. **Linux `amd64`/`arm64` (`src/core/runtime/interrupt_linux.go`)**:
   - `HostInterruptSupported()` is `true`. Linux uses signal-based asynchronous thread interruption (`tgkill` / `SIGURG`), unwinding native code without needing compiler polls.
3. **Empirical Verification**:
   - Running `TestCallContextInterruptsNativeLoop` and `TestInvokeContextInterruptsNativeLoop` in `src/wago/cancellation_test.go` on macOS ARM64 confirms that a tight infinite WASM loop is interrupted in **20ms** and returns `context.DeadlineExceeded`, leaving the instance clean for subsequent calls.
   - **Conclusion:** Runaway guest loops do NOT hang the Go host. Standard Go `context.WithTimeout` on `inst.Call` provides complete, deterministic execution time bounds.

### 2.2 Feasibility Summary

| Check | Result |
|---|---|
| Parent `go list ./...` / `go test ./...` | Nested `cmd/` packages are **automatically excluded** from the parent *module* (verified; `go.sum`-free root untouched). With a committed `go.work`, workspace *mode* re-includes them in root-level `./...` patterns, which is intentional: it gives CI nested-module coverage |
| Parent `go.mod` after nested `go mod tidy` **and** parent `go mod tidy` | Stays **zero external requires** |
| Nested build without `-tags wago` | Stub compiles; wago runtime is not linked |
| Nested build with `-tags wago` | Compiles, links wago, and executes wasm in-process |
| Native loop timeout | Preempts in <25ms via compiler loop safepoints / signals; returns `context.DeadlineExceeded` |
| Resource limits | `Policy.MaxMemoryBytes` enforced during admission and execution |
| Cross-compilation | `CGO_ENABLED=0` works for all desktop/arm64 targets |
| Binary size impact | Measured on `gorrcd` (2026-09-11, `CGO_ENABLED=0`, `-trimpath`, Go 1.27): ≈13.4 MiB stub → ≈20.1 MiB with wago (+≈6.7 MiB). Absolute sizes scale with the tool binary; the wago delta is the stable part |
| `golang.org/x/sys` | Not pulled in for runtime embedding (stdlib-only; x/sys is CLI-only upstream). Confirmed: `cmd/gorrcd/go.sum` contains only wago entries |

> [!IMPORTANT]
> **Pin wago explicitly.** Bare `go mod tidy` can resolve a retracted canary
> (`v0.1.0-canary.ge844da4`). Always run `go get github.com/wago-org/wago@v0.1.0-beta.8`
> (or current tagged beta) before `go mod tidy`.

---

## 3. Module Layout & Workspace Architecture

### 3.1 Root Module (Unchanged Policy)

```
go-reticulum/
  go.mod          # module github.com/gmlewis/go-reticulum — stdlib only
  go.sum          # empty / absent of external requires
  rns/ lxmf/ rrc/ compress/ micron/ ...
  cmd/
    gornstatus/   # stays in root module
    gornpath/     # stays in root module
    ...           # other stdlib-only tools
```

Root `AGENTS.md` rule stands: **no external dependencies in the root module.**

### 3.2 Nested Command Modules

```
go-reticulum/
  cmd/
    gorrcd/
      go.mod              # module github.com/gmlewis/go-reticulum/cmd/gorrcd
      go.sum              # pins wago v0.1.0-beta.8
      main.go             # package main
      flags.go            # package main (flags & usageText)
      plugins_wago.go     # //go:build wago && (linux||darwin||windows) && (amd64||arm64)
      plugins_stub.go     # //go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)
      plugins_test.go     # unit tests with embedded wasm test fixtures
```

Template `cmd/<tool>/go.mod`:

```go
module github.com/gmlewis/go-reticulum/cmd/<tool>

go 1.26.0

require (
	github.com/gmlewis/go-reticulum v0.0.0
	github.com/wago-org/wago v0.1.0-beta.8
)

replace github.com/gmlewis/go-reticulum => ../..
```

Notes:
- `replace github.com/gmlewis/go-reticulum => ../..` forces builds to use the local working tree.
- The module path `github.com/gmlewis/go-reticulum/cmd/<tool>` ensures clear naming without publishing separate packages.

> [!IMPORTANT]
> **`replace` scope and external consumers.** Go honors `replace` directives only in the
> *main* module's `go.mod`; they are ignored in every dependency
> ([go.dev/ref/mod](https://go.dev/ref/mod)). Therefore this replace:
> 1. applies only when `<tool>` itself is the main module (repo-local builds and the
>    nested test/build loops) — the local working tree is used as intended;
> 2. is silently ignored for anyone who consumes the published nested module
>    (`go get`/`go install github.com/gmlewis/go-reticulum/cmd/<tool>@vX.Y.Z`) — they
>    resolve the parent normally from the module proxy;
> 3. never propagates into the root module or other modules, so root-module consumers
>    (e.g. go-nomadnet) are unaffected by it entirely.
>
> The `v0.0.0` parent requirement is the real external-consumption caveat: it only
> resolves once the parent is required at a **published tag**. Until the release flow
> bumps `cmd/<tool>/go.mod`'s parent requirement to the released version at tag time
> (or documents checkout-only builds), external users should install the tool from the
> GitHub release artifacts or from a source checkout, not via
> `go install .../cmd/<tool>@vX.Y.Z`. Note `go install pkg@version` also cannot pass
> `-tags wago`, so a proxy-installed binary always runs the stub; the wago-enabled
> binaries come from the release builder.

### 3.3 Workspace Development (`go.work`)

For IDE support and `gopls` across multiple modules, maintain a committed root `go.work`
(committing it is what gives CI's root `go test ./...` coverage of the nested modules;
workspace mode resolves member modules by directory, which makes the per-module
`replace` redundant but harmless):

```
go 1.26.0

use .
use ./cmd/gorrcd
# Additional promoted tools added here as implemented
```

The workspace `go` directive must be ≥ every member module's directive (all are
`go 1.26.0` here). No `go.work.sum` is generated for this layout (checksums come from
each member module's own `go.sum`); should one ever appear, it is developer-local and
gitignored.

> [!NOTE]
> CI and release scripts must **not** depend on `go.work` being present. Build and test
> scripts should operate cleanly with `GOWORK=off` or by entering each directory directly.

---

## 4. Build Tags & Platform Support Matrix

| Target Architecture | Build Tags | Result |
|---|---|---|
| Desktop (Linux, macOS, Windows) `amd64` / `arm64` | `-tags wago` | Full in-process wago plugin host (`plugins_wago.go`) |
| Desktop without plugin support | *(none)* | Clean stub compilation (`plugins_stub.go`); 0 MB overhead |
| `pocket_terminal-linux-arm64` (RPi Zero 2W) | `-tags wago,pocket_terminal` | Plugin support enabled on ARM64 pocket terminal |
| Embedded / Hardware (ESP32, ARMv7, RISC-V, FreeBSD) | *(none)* or tags without `wago` | Compiles `plugins_stub.go`; no build failure |

### Build Tag Constraints in Source Files

In `plugins_wago.go`:
```go
//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

package main
```

In `plugins_stub.go`:
```go
//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

package main
```

This ensures that even if `-tags wago` is accidentally passed to a 32-bit or unsupported
platform, `plugins_stub.go` compiles gracefully rather than failing compilation.

---

## 5. Promotion List & Tooling Impact

### 5.1 Command Promotion Decision Matrix

| Command | Promote? | Rationale |
|---|---|---|
| `cmd/gorrcd` | **M1** | RRC slash-command plugins. Clean, discrete request/response seam. |
| `cmd/gornsd` | **M2** | LXMF inbound filters, delivery callbacks; announce observers move to M3 (Step 3.2). Promoted in Milestone 2 per §10. |
| `cmd/gornx` | **M3** | Sandboxed remote execution tools (replaces raw shell execution). |
| `cmd/gornsh` | **M3** | Optional wasm execution tools in remote terminal sessions. |
| `cmd/golxmd` | optional | Only if standalone filter hosting is needed outside `gornsd`. |
| `cmd/gornpkg` | later | Signed plugin package distribution; keep stub until format is finalized. |
| `gornstatus`, `gornpath`, `gornid`, `gornprobe`, `gorncp`, `gorngit`, ... | **no** | CLI utilities; no plugin host required. |
| `cmd/publish-github-release-artifacts` | **no** | Build orchestrator; stays in root module. |

### 5.2 Release Builder Fix (`cmd/publish-github-release-artifacts/main.go`)

In `cmd/publish-github-release-artifacts/main.go` around line 597:
```go
// Current root-relative invocation fails for nested modules:
buildArgs = append(buildArgs, "-o", j.outPath, "./cmd/"+j.binaryName)
cmd := exec.CommandContext(ctx, "go", buildArgs...)
```

**Required fix when promoting tools:**
If `cmd/<binaryName>/go.mod` exists:
1. Set `cmd.Dir = filepath.Join("cmd", j.binaryName)` (or pass `-C cmd/<binaryName>`).
2. Build `.` instead of `./cmd/<binaryName>`.
3. Append `-tags=wago` to `buildTags` only when targeting `(linux, darwin, windows) × (amd64, arm64)`.
4. Set `GOWORK=off` in the build environment so a nested-module build resolves its own
   module graph instead of the workspace.

> [!NOTE]
> **Status (Milestone 1):** implemented in `cmd/publish-github-release-artifacts/main.go`
> via `nestedModuleDir`, `wagoSupportedTarget`, and `buildTagsWithWago` (covered by
> `TestNestedModuleDir`, `TestWagoSupportedTarget`, and `TestBuildTagsWithWago`); the
> pocket-terminal matrix (`pocket_terminal,wago` on linux/arm64) matches §4.

---

## 6. Test Suite & CI Integration (Preventing "Silent Skips")

When `cmd/<tool>` has its own `go.mod`, running `go test ./...` from the repo root
**silently skips the nested module**. The test scripts must be updated to prevent regressions:

### 6.1 `scripts/test-all.sh` Update

Update [scripts/test-all.sh](file:///Users/glenn/go/src/github.com/gmlewis/go-reticulum/scripts/test-all.sh) to test root and all nested modules:

```bash
# Run root tests
go test -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" ./...
go vet ./...

# Run nested module tests
for modfile in cmd/*/go.mod; do
  if [ -f "$modfile" ]; then
    moddir=$(dirname "$modfile")
    echo "Testing nested module: ${moddir} (default)..."
    (cd "${moddir}" && go test -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" .)
    
    # Also test with -tags wago on supported host platforms
    if [[ "$(go env GOOS)" =~ ^(linux|darwin|windows)$ ]] && [[ "$(go env GOARCH)" =~ ^(amd64|arm64)$ ]]; then
      echo "Testing nested module: ${moddir} (-tags wago)..."
      (cd "${moddir}" && go test -tags=wago -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" .)
    fi
  fi
done
```

### 6.2 `run-all-tests.sh` Update

In [run-all-tests.sh](file:///Users/glenn/go/src/github.com/gmlewis/go-reticulum/run-all-tests.sh), extend static analysis checks (`errcheck`, `modernize`, `staticcheck`)
to loop over any `cmd/*/go.mod` subdirectories so nested module code meets the same
cleanliness standards.

---

## 7. Dependency Inversion Seams in Core Libraries

Core library packages (`rrc`, `rns`, `lxmf`) **never** import `wago`. They provide standard
Go hooks into which nested command modules inject their wago-backed implementations.

### 7.1 Seam 1: RRC Slash Commands (`rrc/commands.go`)

In [rrc/commands.go](file:///Users/glenn/go/src/github.com/gmlewis/go-reticulum/rrc/commands.go#L23):
Add an optional `CustomHandler` hook to `CommandHandlerHooks`:

```go
type CommandHandlerHooks struct {
    // ... existing fields ...
    
    // CustomHandler is an optional hook for unknown slash commands.
    // If set and it returns true, the command was handled.
    CustomHandler func(link *rns.Link, peerHash []byte, room *string, parts []string, outgoing *OutgoingList) bool
}
```

In `CommandHandler.HandleOperatorCommand`:
```go
    switch pythonLower(parts[0]) {
    case "reload":
        // ... existing cases ...
    case "invite":
        c.handleInvite(link, peerHash, parts, room, outgoing)
        return true
    default:
        if c.hooks.CustomHandler != nil && c.hooks.CustomHandler(link, peerHash, room, parts, outgoing) {
            return true
        }
    }
    return false
```

### 7.2 Seam 2: NomadNet Executable Pages (`nomadnet/node/node.go` & `browser/browser.go`)

In `nomadnet/node/node.go` (`makePageHandler`):
If `strings.HasSuffix(filePath, ".wasm")`, invoke the page renderer adapter passing
request metadata, returning dynamic Micron markup.

In `nomadnet/browser/browser.go` (`ServeLocalPage`):
Similarly delegate `.wasm` files to the local page renderer adapter.

### 7.3 Seam 3: Inbound LXMF Delivery Filters (`lxmf/router.go`)

`lxmf.Router.RegisterDeliveryCallback` already accepts a callback function:
The plugin host registers a callback that parses inbound messages and passes them
to `filter_inbound(msg_ptr, len) -> i32` (1 = accept, 0 = drop).

---

## 8. Plugin ABI & Guest/Host Protocol

### 8.1 Guest Exports

| Export | Signature | Role |
|---|---|---|
| `wagoplugin_alloc` | `(len i32) -> (ptr i32)` | Allocates memory inside guest for host input payload |
| `wagoplugin_free` | `(ptr i32, len i32) -> ()` | Optional guest memory deallocator |
| `plugin_manifest` | `() -> (ptr i32, len i32)` | Returns JSON manifest `{"name": "...", "version": "...", "commands": ["..."]}` |
| `handle_command` | `(req_ptr i32, req_len i32) -> (resp_ptr i32, resp_len i32)` | RRC slash-command handler |
| `render_page` | `(req_ptr i32, req_len i32) -> (resp_ptr i32, resp_len i32)` | NomadNet dynamic page renderer |
| `filter_inbound` | `(msg_ptr i32, msg_len i32) -> (action i32)` | LXMF message filter (1=pass, 0=drop) |
| `on_announce` | `(ann_ptr i32, ann_len i32) -> (status i32)` | Announce packet observer |

> [!NOTE]
> Multi-value return `(result i32 i32)` is fully supported in `wago`. The host reads results
> via `out[0].I32()` (`ptr`) and `out[1].I32()` (`len`).

### 8.2 Host Imports (Deny-by-Default Capabilities)

| Host Function | Import Key | Signature | Semantics |
|---|---|---|---|
| `rns.log` | `rns.log` | `(ptr i32, len i32) -> ()` | Logs a message through host logger |
| `rns.now` | `rns.now` | `() -> (ts i64)` | Returns host UNIX timestamp in seconds |
| `rns.kv_get` | `rns.kv_get` | `(k_ptr, k_len, out_ptr, out_cap) -> (n i32)` | Reads key from plugin's isolated KV store |
| `rns.kv_set` | `rns.kv_set` | `(k_ptr, k_len, v_ptr, v_len) -> (status i32)` | Writes key to plugin's isolated KV store |

### 8.3 Data Passing Protocol (Host ⇄ Guest)

To pass a string/bytes `data` from host to guest:
1. Call `ptr, err := inst.Call(ctx, "wagoplugin_alloc", wago.ValueI32(int32(len(data))))`.
2. Write bytes into linear memory: `inst.Write(uint32(ptr[0].I32()), data)`.
3. Call execution entry point: `res, err := inst.Call(ctx, "handle_command", ptr[0], wago.ValueI32(int32(len(data))))`.
4. Read response from guest memory: `outBytes, ok := inst.Read(uint32(res[0].I32()), uint32(res[1].I32()))`.

### 8.4 Isolated Scratch Store (`rns.kv_*`)

Each plugin receives a scoped directory on disk: `~/.reticulum/plugins/data/<plugin_name>/`.
Path traversal (`..`, `/`, `\`) is rejected. Total store size is capped at 10 MB per plugin.

### 8.5 Resource Policy Limits

Every instantiated plugin is constrained via `wago.Policy`:
```go
policy := wago.Policy{
    MaxMemoryBytes: 16 * 1024 * 1024, // 16 MB max linear memory
    MaxTableEntries: 1024,
}
```
Invocations always pass a bounded context:
```go
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()
```

---

## 9. Embedded WASM Fixtures for TDD

To enable test-driven development without requiring external WASM compilers (`wat2wasm`)
during test runs, unit tests use self-contained embedded bytecode slices:

### 9.1 Minimal "Answer 42" Module
`(module (func (export "answer") (result i32) i32.const 42))`
```go
var MinWasmAnswer = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0a, 0x01, 0x06, 'a', 'n', 's', 'w', 'e', 'r', 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}
```

### 9.2 Infinite Loop Module (For Timeout / Cancellation Tests)
`(module (func (export "spin") loop br 0 end))`
```go
var MinWasmSpin = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x08, 0x01, 0x04, 's', 'p', 'i', 'n', 0x00, 0x00,
	// Code section: body size 7 (locals 00, loop 03 40, br 0c 00, end 0b, end 0b), section size 9.
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b,
}
```

### 9.3 RRC Slash Command Echo Plugin Module
Exports `wagoplugin_alloc`, `plugin_manifest`, and `handle_command` with 1 page of memory
(declared max 1 page — a max-less memory is rejected by the 16 MiB admission policy in
§8.5, which computes the unbounded maximum as 4 GiB). `handle_command` echoes its request
bytes back with `memory.copy` so tests can assert real content:
```go
// Echo plugin: handle_command copies the request to offset 1024 and returns
// (ptr=1024, len=request length), so the response bytes equal the request.
var MinWasmCommandPlugin = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// Type section (size 0x15 = 1 + 3 + 5 + 7 + 5):
	// 0: () -> (), 1: (i32) -> (i32), 2: (i32, i32) -> (i32, i32), 3: () -> (i32, i32)
	0x01, 0x15, 0x04,
	0x60, 0x00, 0x00,
	0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x02, 0x7f, 0x7f,
	0x60, 0x00, 0x02, 0x7f, 0x7f,
	// Function section: 0: alloc, 1: manifest, 2: handle_command
	0x03, 0x04, 0x03, 0x01, 0x03, 0x02,
	// Memory section: 1 page (64 KiB), max 1 page
	0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
	// Export section (size 0x40 = 1 + 9 + 19 + 18 + 17)
	0x07, 0x40, 0x04,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x10, 'w', 'a', 'g', 'o', 'p', 'l', 'u', 'g', 'i', 'n', '_', 'a', 'l', 'l', 'o', 'c', 0x00, 0x00,
	0x0f, 'p', 'l', 'u', 'g', 'i', 'n', '_', 'm', 'a', 'n', 'i', 'f', 'e', 's', 't', 0x00, 0x01,
	0x0e, 'h', 'a', 'n', 'd', 'l', 'e', '_', 'c', 'o', 'm', 'm', 'a', 'n', 'd', 0x00, 0x02,
	// Code section (size 0x21 = 1 + 6 + 7 + 19); each body size byte is 1 + body length
	0x0a, 0x21, 0x03,
	// func 0 (alloc): returns the fixed offset 1024 (body: 00 41 80 08 0b)
	0x05, 0x00, 0x41, 0x80, 0x08, 0x0b,
	// func 1 (manifest): returns (offset 0, len 32) (body: 00 41 00 41 20 0b)
	0x06, 0x00, 0x41, 0x00, 0x41, 0x20, 0x0b,
	// func 2 (handle_command): memory.copy input to 1024, returns (1024, len)
	// (body: 00 41 80 08 20 00 20 01 fc 0a 00 00 41 80 08 20 01 0b)
	0x12, 0x00, 0x41, 0x80, 0x08, 0x20, 0x00, 0x20, 0x01, 0xfc, 0x0a, 0x00, 0x00, 0x41, 0x80, 0x08, 0x20, 0x01, 0x0b,
}
```

> [!IMPORTANT]
> **Fixture sizes must be verified by the engine, not by eye.** The original draft of
> §9.2 and §9.3 had off-by-one section/body sizes and an unbounded memory declaration;
> wago's compiler rejects such modules at decode ("section size mismatch") or at
> admission ("module maximum memory total 4294967296 bytes exceeds policy limit").
> The corrected bytes above are validated by `TestPluginHostWagoCommandEcho` and the
> other plugin-host tests in `cmd/gorrcd`.

---

## 10. Autonomous Implementation Roadmap (TDD Driven)

### Milestone 1: End-to-End Proof of Concept (`cmd/gorrcd`)

*Goal:* Enable `/test` slash command execution in `gorrcd` via an in-process wago plugin,
while `go-reticulum` root module remains 100% stdlib.

#### Step 1.1: Core Seam (`rrc/commands.go`)
- **Test first:** In `rrc/commands_test.go`, add `TestCommandHandler_CustomHandlerHook`:
  Verify that when an unrecognized command (e.g. `/custom hello`) is passed to `HandleOperatorCommand`,
  the `CustomHandler` hook is called with the expected arguments and returns `true`.
- **Implement:** Add `CustomHandler` to `rrc.CommandHandlerHooks` and dispatch in `HandleOperatorCommand`.
- **Verify:** `go test ./rrc -run TestCommandHandler_CustomHandlerHook` passes.

#### Step 1.2: Nested Module Setup (`cmd/gorrcd`)
- Create `cmd/gorrcd/go.mod` with:
  ```go
  module github.com/gmlewis/go-reticulum/cmd/gorrcd
  go 1.26.0
  require (
      github.com/gmlewis/go-reticulum v0.0.0
      github.com/wago-org/wago v0.1.0-beta.8
  )
  replace github.com/gmlewis/go-reticulum => ../..
  ```
- **Sequencing matters:** `go mod tidy` prunes requirements that no source file imports,
  so run `go get github.com/wago-org/wago@v0.1.0-beta.8` only after (or together with)
  the first file that imports the package, then `(cd cmd/gorrcd && go mod tidy)`.
  Verify `cmd/gorrcd/go.sum` is created and pins `wago v0.1.0-beta.8` (a bare tidy with
  nothing importing wago removes the require entirely; a tidy with no pinned version
  can resolve the retracted canary — see §2).
- Verify root `go.mod` and `go.sum` remain completely untouched!

#### Step 1.3: Plugin Host Implementation
- **Both build variants must expose the identical API** — the untagged glue code
  (`pluginshook.go`) is compiled under both tags and would not compile otherwise:
  `func NewPluginHost(timeout time.Duration, logf func(format string, args ...any)) *PluginHost`,
  `func (h *PluginHost) LoadPlugin(path string) error`,
  `func (h *PluginHost) Active() bool`,
  `func (h *PluginHost) HandleCommand(cmdLine string) (string, error)`,
  `func (h *PluginHost) Close()`.
- Create `cmd/gorrcd/plugins_stub.go` with `//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)`:
  inactive host; `LoadPlugin`/`HandleCommand` fail with `errPluginsNotLinked`.
- Create `cmd/gorrcd/plugins_wago.go` with `//go:build wago && (linux || darwin || windows) && (amd64 || arm64)`:
  Implement `PluginHost` embedding `wago.NewRuntime()`, `LoadPlugin(path string)`,
  `Call("handle_command", ...)`, with `context.WithTimeout(ctx, 2*time.Second)`,
  `wago.Policy{MaxMemoryBytes: 16<<20, MaxTableEntries: 1024}` admission bounds, and
  `inst.Read`/`inst.Write` bounds checking.
- Connect `PluginHost` into `cmd/gorrcd/pluginshook.go` (scan `RRCD_HOME/plugins/*.wasm`,
  adapt to the rrc `CustomHandler` hook via `HubService.SetCustomCommandHandler`) and
  `main.go` (load at bring-up, release hosts at shutdown).

#### Step 1.4: TDD Unit Tests (split by build constraint — a single file cannot compile in
both modes while asserting mode-specific behavior; fixtures live in the untagged
`cmd/gorrcd/wasm-fixtures_test.go` so both tagged test files share them)
- **Test 1 (`plugins_stub_test.go`, stub constraint):** verify `NewPluginHost` returns an
  inactive host, `LoadPlugin`/`HandleCommand` fail, and the command hook returns false.
- **Test 2 (`plugins_wago_test.go`, `-tags wago`):** instantiate `PluginHost` with
  `MinWasmCommandPlugin`, send slash command, assert the echoed output.
- **Test 3 (Timeout/Cancellation, `-tags wago`):** load `MinWasmSpin`, trigger execution
  with a 50ms timeout, assert the call returns `context.DeadlineExceeded` and does not
  block (measured ≈60ms wall; the assertion uses a 1s bound to tolerate CI jitter while
  still proving the loop was preempted).
- Also covered: the `rns.log` host import forwarding guest memory to the host logger, and
  deny-by-default rejection of an unwired import (`rns.kv_get`) at instantiation.

#### Step 1.5: Script & CI Updates
- Update `scripts/test-all.sh` and `run-all-tests.sh` per §6.
- Update `cmd/publish-github-release-artifacts/main.go` per §5.2.
- Add `use ./cmd/gorrcd` to `go.work`.

#### Step 1.6: Verification
- Run `./run-all-tests.sh`. Verify the final line reports:
  "Repo is squeaky-clean (errcheck + gopls check + modernize + staticcheck + all tests)."
- Verify root `go.mod` has zero requires (and no root `go.sum`).
- Also run `./scripts/test-all.sh`, which now covers the nested module in both the
  default (stub) and `-tags wago` build modes (see §6.1).

---

### Milestone 2: `go-nomadnet` Executable Pages & `gornsd` Promotion

*Goal:* Enable `.wasm` executable page serving in `go-nomadnet` and promote `cmd/gornsd`.

#### Step 2.1: `go-nomadnet` Executable Pages
- Add `github.com/wago-org/wago v0.1.0-beta.8` to `go-nomadnet/go.mod` (behind `wago` build tag).
- In `nomadnet/node/node.go`, wire `.wasm` page requests to `render_page(req_ptr, req_len) -> (ptr, len)`.
- Write unit tests in `nomadnet/node/node_test.go` verifying dynamic Micron markup generation.
- Write unit tests in `nomadnet/browser/browser_test.go` verifying local `.wasm` page rendering.

#### Step 2.2: Promote `cmd/gornsd`
- Add `cmd/gornsd/go.mod` + `go.sum` with `replace => ../..`.
- Split into `plugins_wago.go` and `plugins_stub.go`.
- Wire `lxmf.Router.RegisterDeliveryCallback` to inbound filter plugins.
- Add nested test loop to `run-all-tests.sh`.

---

### Milestone 3: Breadth & Sandboxing (`gornx`, Announce Observers, KV Store)

*Goal:* Full sandboxing breadth across the ecosystem.

#### Step 3.1: Sandboxed `gornx`
- Promote `cmd/gornx` to nested module.
- Instead of raw `exec.Command` subprocess execution, execute requested commands in sandboxed WASM runtime.

#### Step 3.2: Announce Observers in `gornsd`
- Register `Transport.RegisterAnnounceHandler` to notify WASM observer plugins of incoming network announcements.

#### Step 3.3: Per-Plugin KV Scratch Store
- Implement `rns.kv_get` / `rns.kv_set` host imports backed by `~/.reticulum/plugins/data/<plugin>/` with 10MB quota and path sanitation.

---

## 11. Production-Grade Host Implementation Reference

Below is the production implementation pattern for `plugins_wago.go`. The shipped
Milestone 1 implementation (`cmd/gorrcd/plugins_wago.go`) follows this pattern with one
structural difference: the constructor is infallible (`NewPluginHost(timeout, logf)
*PluginHost`) and `LoadPlugin(path)` performs the compile/instantiate steps, so load
errors are explicit and the shared API stays identical across the stub and wago builds.
The reference here takes the wasm bytes at construction; both forms satisfy §8.

```go
//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	// The wasm runtime API lives under src/wago: the wago module has no
	// package at its root, so "github.com/wago-org/wago" does not resolve.
	wago "github.com/wago-org/wago/src/wago"
)

type PluginHost struct {
	mu      sync.RWMutex
	rt      *wago.Runtime
	mod     *wago.Module
	inst    *wago.Instance
	timeout time.Duration
}

func NewPluginHost(wasmBytes []byte, timeout time.Duration) (*PluginHost, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	rt := wago.NewRuntime()
	mod, err := rt.Compile(wasmBytes)
	if err != nil {
		rt.Close()
		return nil, fmt.Errorf("plugin compile: %w", err)
	}

	policy := wago.Policy{
		MaxMemoryBytes:  16 * 1024 * 1024, // 16 MB max memory
		MaxTableEntries: 1024,
	}

	// Host imports (deny-by-default)
	logImport := wago.HostFunc(func(m wago.HostModule, params, results []uint64) {
		ptr, n := uint32(params[0]), uint32(params[1])
		mem := m.Memory()
		if int(ptr)+int(n) <= len(mem) {
			// Forward to host logger
		}
	})

	imports := wago.Imports{
		"rns.log": logImport,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inst, err := rt.Instantiate(ctx, mod, wago.WithPolicy(policy), wago.WithImports(imports))
	if err != nil {
		_ = mod.Close() // the compiled module must be released, not just the runtime
		_ = rt.Close()
		return nil, fmt.Errorf("plugin instantiate: %w", err)
	}

	return &PluginHost{
		rt:      rt,
		mod:     mod,
		inst:    inst,
		timeout: timeout,
	}, nil
}

func (h *PluginHost) HandleCommand(cmdLine string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	data := []byte(cmdLine)
	allocRes, err := h.inst.Call(ctx, "wagoplugin_alloc", wago.ValueI32(int32(len(data))))
	if err != nil {
		return "", fmt.Errorf("plugin alloc failed: %w", err)
	}
	inPtr := uint32(allocRes[0].I32())

	if !h.inst.Write(inPtr, data) {
		return "", fmt.Errorf("plugin memory write failed")
	}

	res, err := h.inst.Call(ctx, "handle_command", allocRes[0], wago.ValueI32(int32(len(data))))
	if err != nil {
		return "", fmt.Errorf("plugin execution failed: %w", err)
	}
	if len(res) < 2 {
		return "", fmt.Errorf("plugin handle_command must return (ptr, len)")
	}

	outPtr, outLen := uint32(res[0].I32()), uint32(res[1].I32())
	outBytes, ok := h.inst.Read(outPtr, outLen)
	if !ok {
		return "", fmt.Errorf("plugin memory read failed")
	}

	return string(outBytes), nil
}

func (h *PluginHost) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inst != nil {
		_ = h.inst.Close()
		h.inst = nil
	}
	if h.mod != nil {
		_ = h.mod.Close() // modules are owned separately from the runtime
		h.mod = nil
	}
	if h.rt != nil {
		_ = h.rt.Close()
		h.rt = nil
	}
	return nil
}
```

---

## 12. References

- **wago Engine**: `https://github.com/wago-org/wago`
  - Safepoint compiler insertion: `src/core/compiler/backend/railshot/{amd64,arm64}/compile.go` (`Interruptible: !wruntime.HostInterruptSupported()`).
  - Native loop cancellation tests: `src/wago/cancellation_test.go` (`TestCallContextInterruptsNativeLoop`).
  - Policy & capability limits: `src/wago/policy.go`.
  - Memory bounds & safe access: `src/wago/memory_access.go` (`Read`, `Write`).
- **Parent Hooks**:
  - `rrc/commands.go`: `CommandHandlerHooks`, `HandleOperatorCommand`.
  - `lxmf/router.go`: `RegisterDeliveryCallback`.
  - `rns/destination.go`: `RegisterRequestHandler`.
  - `rns/transport.go`: `RegisterAnnounceHandler`.
- **NomadNet Page Seams**:
  - `nomadnet/node/node.go`: `makePageHandler`, `ServePage`.
  - `nomadnet/browser/browser.go`: `ServeLocalPage`.
- **Release Builder**:
  - `cmd/publish-github-release-artifacts/main.go`: nested-module build path (`nestedModuleDir`, `wagoSupportedTarget`, `buildTagsWithWago`) and the concurrent build loop in `buildAll`.
