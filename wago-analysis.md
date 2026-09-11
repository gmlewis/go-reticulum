# Architectural Proposal: Sandboxed WebAssembly Plugins via wago

**Status:** proposal / design reference — not implemented  
**Scope:** `go-reticulum`, `go-nomadnet`, and their relationship to
`asic-reticulum` / pocket device builds  
**Engine:** [wago](https://github.com/wago-org/wago) (local checkout:
`~/src/github.com/wago-org/wago`)

This document specifies how a WebAssembly plugin runtime can be added to the
Reticulum Go ecosystem without violating `go-reticulum`'s standard-library-only
root-module rule, and how the same plugin ABI can deliver the most user value
across daemon, TUI, and pocket-arm64 targets.

---

## 1. Goals and non-goals

### Goals

- Provide a **sandboxed** extension surface for third-party logic (chat
  commands, dynamic Micron pages, LXMF filters, announce observers, remote
  tools) with deny-by-default host imports, per-plugin resource limits, and
  deadline-based CPU control.
- Preserve `go-reticulum`'s **root `go.mod` as pure stdlib** (zero external
  requires). External engine code may live only in a nested module that root
  binaries do not import.
- Keep **device builds unchanged**: arm7 / riscv64 / ESP32-class / ASIC-FPGA
  artifacts never compile wago and never grow in size.
- Mirror the hook contract the official Python ecosystem already understands
  (Sideband Command/Service/Telemetry plugins; NomadNet executable pages)
  while **upgrading the trust model** from full-trust `exec()` / subprocess
  scripts to a real sandbox.
- One ABI, two host shapes: a **subprocess daemon** for stdlib-only
  go-reticulum binaries, and optional **in-process** hosting for go-nomadnet
  (where external deps are already allowed).

### Non-goals (v1)

- Interface / transport plugins on the RNS packet path (`initInterfaces`).
- WASI, Component Model guests, or any fs/net escape hatch.
- Accepting third-party compiled `.wago` native artifacts.
- Sideband plugin-API byte-for-byte compatibility.
- Plugin support on linux/arm7, linux/riscv64, freebsd, ESP32, or the ASIC/FPGA
  fabric.

---

## 2. Why wago

[wago](https://github.com/wago-org/wago) is a pure-Go, no-cgo WebAssembly JIT
(single-pass native codegen, WARP/Valent-Block derived), Apache-2.0. At the
time of this proposal the local checkout is `v0.1.0-beta.7-7-g963b74d9`;
`CHANGELOG.md` documents `v0.1.0-beta.8` (2026-09-10). **Pin a tagged
release**, not `main`.

Properties that make it the right engine:

| Property | Detail |
|---|---|
| Embedding | `wago.NewRuntime()` → `Compile` → `Instantiate` → `Call`/`Invoke`. Host imports use one reflection-free `wago.HostFunc` shape with zero-copy linear-memory views. |
| Sandbox | Deny-by-default imports. A module with no satisfied imports has **zero ambient authority** — core has no fs/net/clock/random. Reserved module names (`wasi_snapshot_preview1`, `env`, `wago_process`, `wago_runtime`, …) cannot be shadowed. |
| Policy | Per-instance `Policy`: `AllowedCapabilities` (exclusive allow-list), `DeniedCapabilities`, `MaxMemoryBytes`, `MaxMemories`, `MaxTableEntries`, `MaxTags`. |
| Runtime limits | `WithMemoryLimitPages`, `WithInstanceLimits`, `WithMaxModuleBytes` (default 64 MiB), `WithMaxNativeCodeBytes`, `WithNativeStackBytes`. |
| CPU control | Context deadlines only (`Policy.MaxInvokeDuration` is deprecated → `ErrUnsupported`). linux/amd64 uses thread-directed preemption; other targets emit safepoints. Host imports must stay non-blocking and honor ctx. |
| Artifacts | `Load` accepts raw untrusted `.wasm` only; `LoadTrustedArtifact` accepts self-produced native artifacts. Measured ~24× faster startup on artifact reload. Never ship third-party artifacts. |
| WASI / CM | Not in core. Optional Authority-gated plugins (`wago-org/wasi`, `wago-org/component-model`). v1 installs **neither**. |
| Host composition | Optional Go-native Contracts (`plugin/contracts.go`, examples 08–13) for *trusted* host-side capability wiring inside `gowagod` — not a substitute for the untrusted guest ABI. |
| Conformance | Core 3.0 suite: 2,226 modules / 58,038 assertions, zero fail/skip (`FEATURES.md`). MVP/v2/v3 work is mature for a beta engine. |
| Size | ~+8.4 MB to a linked binary. Acceptable for desktop / pocket-linux-arm64 hosts; irrelevant for devices that never pass the tag. |
| Deps | `src/` (embeddable runtime) is pure stdlib; the single `golang.org/x/sys` require is CLI-only. |

### Platform matrix (hard constraint)

| GOOS/GOARCH | wago | Typical target |
|---|---|---|
| linux/amd64, linux/arm64 | yes | desktop, servers, **pocket_terminal (RPi Zero 2W)** |
| darwin/amd64, darwin/arm64 | yes | developer desktops |
| windows/amd64, windows/arm64 | yes | desktop |
| linux/arm (GOARM=7) | **no** | some pocket builds |
| linux/riscv64 | **no** | ESP32-C5-class pocket hub/communicator |
| linux/386 | **no** | legacy |
| freebsd/* | **no** | `freebsd/amd64` appears in the release matrix |

`pocket_terminal-linux-arm64` **is** a first-class wago host. arm7 and
riscv64 pocket artifacts are not. Tagged source must therefore encode:

```go
//go:build wago && (linux || darwin || windows) && (amd64 || arm64)
```

so freebsd/arm7/riscv64 builds degrade to "plugins unavailable" rather than a
compile error.

---

## 3. Constraints in this codebase

### 3.1 go-reticulum: stdlib-only root module

`AGENTS.md` is explicit: no external dependencies in the root module; only
`examples/go.mod` (not yet created) may use them. Root `go.mod` currently has
zero `require`s. Consequences:

- Root packages and `cmd/*` binaries **must not import wago**.
- `go mod tidy` scans files behind `//go:build wago` and would pull wago into
  root `go.mod` — so wago cannot live as tagged files in the root module.
- The only arrangement that keeps root `go.mod`, tidy, and every build config
  pristine is **no cross-module import from root into an engine module**:
  i.e. a **subprocess boundary**.

### 3.2 go-nomadnet: external deps already allowed

`go.mod` already requires tcell/tview forks, `go-reticulum`, clipboard, etc.
`go.work` uses `.` + `../go-reticulum`. In-process wago behind a build tag is
rule-legal here today.

### 3.3 asic-reticulum and device isolation

`asic-reticulum` is SpinalHDL + ESP32-C5 C firmware + Python HIL — it compiles
no Go and has no module graph shared with the Go repos. Pocket/FPGA/ASIC
release artifacts are ordinary cross-compiles of `./cmd/*` that simply never
pass `-tags wago`. Binary size, `go.mod`, and `go.sum` for those artifacts
remain untouched by construction.

The Hardware Projects Guide documents users fetching
`gonomadnet-pocket_terminal-linux-arm64` and `gorrcd-pocket_*` from the Go
repos. Those arm64 artifacts *may* optionally enable wago later; asic-reticulum
itself needs no change.

### 3.4 Existing policy on interface plugins

`README.md` / `TODO.md` intentionally reject external Python interface plugins
(supply-chain caution). A wasm host is the safe way to reopen application-level
hooks. The **interface/transport data path stays closed** in v1.

### 3.5 Hook surfaces available today

| Surface | Location | Plugin role |
|---|---|---|
| Announce handlers | `rns/transport.go` `RegisterAnnounceHandler` | observers / directory enrichment |
| LXMF delivery callback | `lxmf/router.go` `RegisterDeliveryCallback` | inbound filters, notifiers |
| Destination request handlers | `rns/destination.go` `RegisterRequestHandler` | sandboxed "executable pages" |
| RRC slash commands | `rrc/commands.go` closed switch + `CommandHandlerHooks` | plugin-provided `/cmds` |
| go-nomadnet delivery funnel | `nomadnet/app/app.go` `Ingest → notify → print → DeliveryCallback → UIChangeCallback` | app-level hooks; drop-filters must run **before** `Ingest` |
| go-nomadnet page serving | `nomadnet/node` static `ServePage`; request `data` currently discarded | wasm pages close a live parity gap (`browser.go` documents executable pages as unsupported) |
| gornsh | `cmd/gornsh/session-runtime.go` executes the requested program | wasm tools with zero library change |
| **gornx** | `cmd/gornx` rnx remote-command listener (`RegisterRequestHandler("command", …)`) | sandboxed remote tools on an identity allow-list |
| gornpkg | `cmd/gornpkg` stub | future signed plugin distribution |

go-nomadnet specifics that shape the host design:

- `DeliveryCallback` / `UIChangeCallback` are **single func fields** — a plugin
  host needs multi-subscriber fan-out.
- TUI menu is a static 8-item slice pinned by parity tests — do not add a
  Plugins menu item in parity builds.
- Slash-command default branch is the client-side insertion point; server-side
  RRC (gorrcd) remains the better first command target.
- Micron renderer is mature; guests may return micron markup directly.

---

## 4. Recommended architecture

Three placement options; the differences are where the wago dependency lives:

| Variant | go-reticulum `go.mod` | Latency | Isolation | Complexity |
|---|---|---|---|---|
| **A. `gowagod` helper daemon (subprocess)** | **pristine, zero changes** | spawn once + ~100–200 µs IPC/call | engine crash isolated; wasm sandbox on top | low |
| **B. In-process behind `-tags wago` in root** | gains `wago` + indirect requires — **violates AGENTS.md** | lowest | same process | lowest |
| **C. Nested module + workspace import** | pristine only while tidy noise is tolerated / once published | lowest | same process | medium |

**Decision: A for go-reticulum; B inside go-nomadnet (not in go-reticulum
root).**

```
go-reticulum/
  wagoplugins/                 # NEW — pure stdlib client + ABI types
    wagoplugins.go             #   kinds, hook contract, manifest schema
    wago-client.go             #   exec gowago; JSON-RPC over stdio/unix socket
    wago-client-embedded.go    #   //go:build embedded || pocket_communicator || pocket_hub
                               #   → "not supported" stub
  wagohost/                    # NEW nested module github.com/gmlewis/go-reticulum/wagohost
    go.mod                     #   requires github.com/wago-org/wago (engine only)
    engine.go                  #   Runtime wiring: Policy, limits, rns.* host imports
    cmd/gowago/                #   daemon: load plugin set; serve JSON-RPC
go-nomadnet/
  (consumes wagoplugins via go-reticulum)
  wagohost-*.go                # //go:build wago && (linux||darwin||windows) && (amd64||arm64)
                               # optional in-process mode
```

### Runtime rules (both host shapes)

- **One wago Runtime** hosts all plugins.
- Compile once per plugin; **one instance per plugin**, or
  **instance-per-request** for concurrent hooks (`WithInstanceLimits`,
  managed instances).
- Per-call `context.WithTimeout`.
- Per-plugin `Policy` (memory pages, table caps, capability allow-list).
- **No WASI installed.**
- On daemon crash: respawn, re-grant, circuit-break (fixes Sideband's
  loader-thread failure mode by design).
- SHA-256 pin of allowed plugin modules in config; plugins **off** unless
  path/config explicitly enables them (Sideband-style default).

### Build tags and verification

- New opt-in tag: `wago`, always combined with
  `(linux || darwin || windows) && (amd64 || arm64)`.
- Embedded/pocket stubs follow the existing
  `!embedded && !pocket_communicator && !pocket_hub` pattern.
- Add a `-tags=wago` line to `run-all-tests.sh` / `GO_TEST_TAGS` in
  `scripts/test-integration.sh`.
- Explicitly verify three matrix rows before shipping: linux/amd64 (host),
  linux/arm64 (pocket_terminal), freebsd/amd64 (must build **without** wago,
  plugins stubbed).

---

## 5. Plugin ABI v0

Guest modules (wat, AssemblyScript, TinyGo, or Rust — wago example 22
exercises multiple languages against the same host import) export JSON-buffer
hooks. The host grants `rns.*` imports per plugin.

### Guest exports

| Export | Purpose |
|---|---|
| `wagoplugin_alloc(len i32) → ptr` | host→guest input buffer |
| `plugin_manifest() → (ptr, len)` | JSON: `{api, kind, name, command_name?, grants[]}` |
| `plugin_start() → i32` / `plugin_stop()` | lifecycle; 0 = ok |
| `handle_command(req_ptr, req_len) → (ptr, len)` | reply text |
| `update_telemetry(snapshot_ptr, len) → (ptr, len)` | plugin **returns** telemetry JSON; host merges |
| `on_announce(a_ptr, a_len) → i32` | observe announces |
| `filter_inbound(msg_ptr, len) → i32` | accept / deny / flag |
| `render_page(req_ptr, len) → (ptr, len)` | micron markup (executable-page analog) |

Kinds: `command | service | telemetry | filter | page`.

Multi-value `(ptr, len)` returns are supported by wago (`inst.Call → []Value`).

### Host imports (deny-by-default)

| Import | Notes |
|---|---|
| `rns.log(ptr, len)` | always available |
| `rns.reply(dest_ptr, content_ptr, len) → i32` | scope: reply-to-caller vs any destination |
| `rns.announce(app_data_ptr, len) → i32` | service plugins announce a destination |
| `rns.kv_get` / `rns.kv_set` | per-plugin namespaced scratch store |
| `rns.now() → i64` | clock |

Capabilities map onto wago's `Policy.AllowedCapabilities` vocabulary
(`timer.read`, `net.outbound`, `fs.read`, `kv.read`, `kv.write`, …).

### Distribution

- Pin plugin `.wasm` by SHA-256 in config.
- Compile raw `.wasm` via `Load`/`Compile`; cache **self-produced** `.wago`
  artifacts for fast daemon startup. Never accept third-party artifacts.
- Long-term: `.rsm` manifests + `rsg` signatures verified by `gornid`, fetched
  via `gorngit`, installed by `gornpkg`.

---

## 6. Prioritized use cases

1. **go-nomadnet wasm executable pages** — closes a documented parity gap
   (Go port serves static pages only; browser already parses `request_data`).
   Highest user-visible value; returns micron markup.
2. **RRC chat slash-command plugins** (gorrcd) — `/weather`, `/dice`, etc.;
   replies via granted `rns.reply`. Lowest risk; exercises the full stack.
3. **LXMF inbound filters / notifiers** (gonomadnet, golxmd) — spam scoring,
   keyword routing, auto-ack. Requires multi-subscriber delivery hooks and a
   seam **before** `Ingest` for drop decisions.
4. **Announce observers / directory enrichment** — presence bots; daemon-resident
   (not spawn-per-event).
5. **gornx sandboxed tools** — replace "run a shell command" with pinned wasm
   tools on the existing identity-allow-listed listener.
6. **Telemetry enrichers** — host passes a sensor snapshot; plugin returns
   derived sensors (Sideband TelemetryPlugin parity). Hang off node event
   hooks (`OnPageServed`, `OnAnnounced`, …).
7. **Outbound send + print intercepts** (go-nomadnet) — stamp/log outbound
   LXMF; optional wasm formatter beside `ShouldPrint` (safer than config
   `PrintCommand` shell-out).
8. **gornsh wasm tools** — zero library change; optional AOT via
   `wago compile` standalone natives on trusted hosts.
9. **Signed plugin distribution** over RNS itself (rsm/rsg/gornpkg/gorngit).
10. **Deferred:** interface/transport plugins; Component Model guests; WASI;
    freebsd/arm7/riscv64 hosts; asic HIL.

---

## 7. Risks and mitigations

| Risk | Mitigation |
|---|---|
| wago is beta; API/artifact churn | Pin tagged versions; use as a pure engine (hand-provided `rns.*` imports), not its high-level plugin registry |
| No fuel metering | Context deadlines only; keep host imports non-blocking and ctx-aware |
| amd64 baseline assumes SSSE3/AVX (no CPUID gate) | Fine for the known fleet; document for old-CPU fleets |
| Large virtual address reservations | Cap `Policy.MaxMemoryBytes` + `WithInstanceLimits` |
| Per-instance call serialization | Instance-per-request for concurrent hooks |
| Rule tension if someone links wago into go-reticulum root | Forbidden by design (variant A avoids the decision); go-nomadnet does not inherit the rule |
| Licensing | wago is Apache-2.0; compatible with Reticulum-License core (one-directional embedding) |
| Multi-subscriber delivery | Extend app-level callbacks to a chain before enabling filter plugins |
| Parity-pinned TUI menu | Inject into existing displays; gate any new UI behind non-parity config/tag |

---

## 8. Implementation roadmap

### M1 — prove the stack on the highest-value seams

1. `wagohost/` nested module: wago Runtime, `Policy`, limits, `rns.*` host
   imports; `cmd/gowago` daemon (JSON-RPC over stdio/unix socket, lock file
   like `gornsd`).
2. Root `wagoplugins/` stdlib client (exec-based, embedded-stubbed, off by
   default unless `gowago` present and configured).
3. First integrations:
   - go-nomadnet **wasm pages**: pass request context into
     `makePageHandler`; render guest micron behind `-tags wago`.
   - gorrcd **slash-command registry**: turn the closed switch's unknown
     branch into a plugin table.
4. Golden tests for the ABI; `-tags=wago` test line; device builds verified
   unchanged via the release matrix (including freebsd stub).

### M2 — filters, observers, tools

- Multi-subscriber delivery hooks + `filter_inbound` before `Ingest`.
- Announce observers; telemetry enrichers on node event hooks.
- gornx wasm tool mode.

### M3 — distribution

- SHA-256 pinning + config UX; artifact cache for daemon startup.
- rsm/rsg signing + gornpkg install path once `gornpkg` leaves stub state.
- Optional pocket_terminal-linux-arm64 `gowagod` documentation in the
  Hardware Projects Guide.

### Re-evaluate this proposal if

- wago drops linux/arm64 or changes license.
- Embedding API breaks in a way pinning cannot absorb.
- A cheaper fuel-metering primitive appears.
- go-reticulum relaxes the no-external-deps rule (then variant B becomes
  legal for desktop `gornsd`/`gorrcd` too).
- go-nomadnet's mission starts requiring Sideband plugin parity.

---

## 9. Worked example (validated primitive)

Minimal guest (returns 42) against the typed runtime API — the same shape
`render_page` / `handle_command` would use, plus a host-import demo with
guest-memory access for `rns.log` / `rns.reply`:

```go
// Minimal wasm: (module (func (export "answer") (result i32) i32.const 42))
var minwasm = []byte{
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
    0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
    0x03, 0x02, 0x01, 0x00,
    0x07, 0x0a, 0x01, 0x06, 'a', 'n', 's', 'w', 'e', 'r', 0x00, 0x00,
    0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}
// rt := wago.NewRuntime(); mod, _ := rt.Compile(minwasm)
// inst, _ := rt.Instantiate(ctx, mod); out, _ := inst.Call(ctx, "answer")
// → out[0].I32() == 42
```

Host import (guest → host, zero-copy memory):

```go
log := wago.HostFunc(func(m wago.HostModule, params, results []uint64) {
    mem := m.Memory()
    ptr, n := uint64(params[0]), uint64(params[1])
    line := string(mem[ptr : ptr+n]) // rns.log
})
inst, err := wago.Instantiate(compiled, wago.InstantiateOptions{
    Imports: wago.Imports{"rns.log": log},
})
```

---

## 10. Reference pointers

- **wago:** `https://github.com/wago-org/wago` (docs `https://docs.wago.sh`).
  Key files: `wago.go`, `src/wago/{api,runtime,policy,config,services,hooks}.go`,
  `plugin/contracts.go`, `FEATURES.md`, `ROADMAP.md`, `ARCHITECTURE.md`,
  `CHANGELOG.md`. Examples: 02 typed runtime, 03 host import, 06 runtime
  service, 07 limits, 08–13 plugins/contracts/hooks, 16 serialize, 17 managed
  instances, 22 language guests.
- **Sideband plugin contract (reference only):**
  `~/src/github.com/markqvist/Sideband/sbapp/sideband/plugins.py` —
  Command/Service/Telemetry base classes; raw `exec()` loader; full-trust
  warning in UI.
- **NomadNet executable pages (Python reference):**
  `nomadnet/Node.py` — `chmod +x` pages run as subprocess with env-var request
  context; stdout served.
- **go-reticulum hooks:** `rns/transport.go`, `lxmf/router.go`,
  `rns/destination.go`, `rrc/commands.go`, `cmd/gornx`, `cmd/gornsh`,
  `cmd/gornpkg`, `cmd/publish-github-release-artifacts/main.go`.
- **go-nomadnet hooks:** `nomadnet/app/app.go`, `nomadnet/node/node.go`,
  `nomadnet/browser/browser.go`, `nomadnet/conversation/send.go`,
  `tui/room-widget.go`.
- **Policy to amend when implementing:** `go-reticulum/README.md` ("No
  External Interface Plugin Runtime") and the matching `TODO.md` entry —
  frame the change as *sandboxed application hooks*, not arbitrary interface
  plugins.
