# Wago-powered WebAssembly plugins for go-reticulum / go-nomadnet

**Analysis only — no code has been written or merged. Preserved 2026-09-09 for
future revisit.**

This document records a feasibility analysis of supporting sandboxed
WebAssembly plugins in `go-reticulum` and `go-nomadnet`, using
[wago](https://github.com/wago-org/wago) (local checkout:
`/Users/glenn/src/github.com/wago-org/wago`) as the wasm engine, with Go build
tags so that asic/embedded builds are unaffected. All facts marked ✅ were
verified empirically (code built and executed, scratch tests) rather than
assumed; other facts carry file:line citations from the analyzed repos.

The conclusion was: **feasible and a natural fit — but not pursued right now.**
This document exists so the design work does not have to be redone.

---

## TL;DR

- Feasible and a natural fit. wago embeds cleanly in Go (proved end-to-end with
  a PoC) and has a real sandbox: deny-by-default imports, per-instance
  resource policies, context-deadline preemption, and an explicit
  trusted/untrusted artifact split in its API.
- The interesting constraint is **not wago** — it is (a) the hard
  no-external-dependencies rule on go-reticulum's root module, and (b) the
  device build matrix (linux/arm7, linux/riscv64, 386), where **no wasm JIT can
  exist at all** (wago fails to cross-compile there — verified).
- Both are solved cleanly with build tags + one nested module. Recommended:
  **subprocess `gowagod` helper daemon for go-reticulum** (root go.mod stays
  100% pristine — verified), and **optional in-process wago for go-nomadnet**
  behind a `wago` build tag (that repo already takes external deps, so it is
  rule-legal there).
- `../asic-reticulum` (and pocket/embedded device) builds are unaffected by
  construction: they never pass the tag, no wago code compiles into them, and
  their go.mod/go.sum/binary size are untouched.
- The wasm plugin system would mirror the hook contract of the official Python
  ecosystem (Sideband's Command/Service/Telemetry plugins, NomadNet's
  executable pages) while **upgrading** the trust model from raw `exec()` of
  user Python to an actual sandbox.

---

## 1. What wago is (verified)

wago (`github.com/wago-org/wago`, local checkout
`/Users/glenn/src/github.com/wago-org/wago`) is a **pure-Go, no-cgo
WebAssembly JIT engine** (single-pass native codegen, Valent-Block/WARP
derived): version 0.1.0, beta, Apache-2.0.

Conformance pedigree (from its own docs): MVP spec suite fully passing
(16,592 assertions); spec v2: 1,600 modules / 48,248 assertions, zero skips;
spec v3 clean on linux/amd64, linux/arm64, darwin/arm64; 96,972 checks pass /
0 fail / 22 skip, 86.1% coverage (`VERIFICATION.md`).

### 1.1 Embedding API (verified by PoC)

```go
rt := wago.NewRuntime()                       // high-level runtime
mod, err := rt.Compile(wasmBytes)             // decode+validate+compile
inst, err := rt.Instantiate(ctx, mod)
out, err := inst.Call(ctx, "answer")          // typed: → 42, i32
```

- Host imports (guest → host calls) use one reflection-free shape:

  ```go
  log := wago.HostFunc(func(m wago.HostModule, params, results []uint64) {
      mem := m.Memory()                         // zero-copy view of linear memory
      got = string(mem[ptr : ptr+n])            // read guest data
  })
  inst, err := wago.Instantiate(compiled, wago.InstantiateOptions{
      Imports: wago.Imports{"host.log": log}})
  ```

  **Deny-by-default**: only imports the host passes are satisfied; a module
  with unsatisfied imports fails instantiation with a clear
  `ErrMissingImport`-wrapped error (`src/wago/runtime.go:1574-1580`).
- Multi-value `(ptr, len)` returns work (`inst.Call → []Value`).
- `Load(b []byte)` (raw wasm, untrusted — refuses compiled artifacts) vs
  `LoadTrustedArtifact(b []byte)` (native-code artifacts, must be
  self-produced or authenticated) — the trust boundary is built into the API
  (`src/wago/api.go:4233-4245`).

### 1.2 Sandboxing and resource control (verified)

- A module with **no satisfied imports has zero ambient authority** — the
  engine has no built-in fs/net/clock/random surface at all.
- Reserved import module names cannot be shadowed by guests:
  `wasi_snapshot_preview1`, `env`, `wago_process`, `wago_runtime`, …
  (`src/wago/runtime.go:1561-1570`; test `src/wago/reserved_modules_test.go`).
- Per-instance `Policy` (`src/wago/policy.go:8-32`):
  `AllowedCapabilities` (exclusive allow-list), `DeniedCapabilities`,
  `MaxMemoryBytes`, `MaxMemories`, `MaxTableEntries`, `MaxTags`.
  Builtin capability vocabulary: `timer.read`, `net.outbound`, `fs.read`,
  `fs.write`, `http.client`, `kv.read`, `kv.write`, `metrics.write`,
  `compiler.codegen`.
- `RuntimeConfig` fluent limits (`src/wago/config.go`):
  `WithMemoryLimitPages`, `WithInstanceLimits(maxInstances, maxMemoryBytes)`,
  `WithNativeMemoryMappingLimit` (hard Linux ceiling 4,096 mappings),
  `WithMaxModuleBytes` (default 64 MiB), `WithMaxNativeCodeBytes`,
  `WithNativeStackBytes` (default 4 MiB + 256 KiB fence).
- **CPU DoS control = context deadlines, not fuel.** `Policy.MaxInvokeDuration`
  is deprecated and unsupported (nonzero → `ErrUnsupported`). On linux/amd64
  preemption is a thread-directed signal with CPU-context rewrite (works even
  in a straight-line hot loop); on other targets the compiler emits
  **safepoints at function entries and loop headers**
  (`src/core/compiler/backend/railshot/amd64/compile.go:290`). Runaway
  recursion traps (`TrapStackFenceBreached`). Caveat: a **blocking host
  import** parks the guest and cannot be interrupted from within — host
  imports must be non-blocking and should check ctx
  (`src/core/runtime/engine.go:335-341` documents the intentional design).
- Traps surface as `*TrapError{Code, Frames}` with 22 codes; sentinels are
  `errors.Is`-able: `ErrPermissionDenied`, `ErrMissingImport`,
  `ErrResourceLimit`, `ErrUnsupported`, `ErrImplementationLimit`, …

### 1.3 Compiled-artifact caching (verified by PoC)

`.wago` artifact format v2 (magic `WAGO`, strict section stream, W^X sealed):

- Measured: **2.6 ms source compile → 109 µs artifact load** (24× faster
  startup; delta grows with real module size).
- API: `compiled.MarshalBinary()` → blob; `wago.LoadTrustedArtifact(blob)` to
  reload. Artifacts contain **host-native machine code** — never accept
  third-party artifacts; compile raw `.wasm` from untrusted sources via
  `Load`/`Compile`, and cache only self-produced artifacts.

### 1.4 Platforms, size, dependencies (all measured)

| Item | Result |
|---|---|
| Cross-compile matrix | linux/arm64 ✅, linux/amd64 ✅, windows/arm64 ✅; **linux/arm (GOARM=7) ❌, linux/riscv64 ❌, linux/386 ❌** |
| Supported targets (docs) | linux/darwin/windows × amd64/arm64 only; guard pages (opt-in `-tags wago_guardpage`) linux/amd64, linux/arm64, darwin/arm64; default = portable explicit bounds checks |
| Binary size | plain Go binary 1.8 MB → **10.2 MB with wago linked** (+8.4 MB) |
| Library deps | `src/` (the embeddable runtime) is **pure stdlib**; the single `golang.org/x/sys` require is used only by its CLI — embedding links zero x/sys code |
| WASI | **Not in core.** Separate Authority-gated plugins: `wago-org/wasi` (on top of `wago-org/component-model`). A host that never installs WASI has no fs/net escape hatch — a feature for this design |
| Other sharp edges | per-instance calls serialized (concurrency = one instance per in-flight call); every memory reserves its *max* virtually (default full 4 GiB `MAP_NORESERVE`, ~8.3 GiB in guard-page mode — cap with `Policy.MaxMemoryBytes` + `WithInstanceLimits`); amd64 baseline assumes SSSE3/AVX **with no CPUID gate**; beta API/artifact churn → pin the engine version |

---

## 2. What "Python plugins" actually are in the official ecosystem

This defines the contract a wasm port must satisfy, and reframes the goal:
the Python plugins are **full-trust, in-process, application-level hooks**
plus a **subprocess-per-request page scripting model**. A wasm plugin system
mirrors the hook contract while replacing the trust model with a real sandbox
— a strict security upgrade, not mere parity.

### 2.1 Sideband (the only official tool with a plugin system)

Application-level, full-trust in-process Python:

- Three base classes in `sbapp/sideband/plugins.py`:
  `SidebandCommandPlugin`, `SidebandServicePlugin`,
  `SidebandTelemetryPlugin`.
- Loaded by **raw `exec()`** of `.py` files from a user-chosen directory
  (`sbapp/sideband/core.py:832-905`); registration convention is a
  module-level `plugin_class = <subclass>` variable.
- The **entire** hook surface:
  - `start()` / `stop()` / `is_running()` lifecycle;
  - `handle_command(arguments, lxm)` — chat-driven RPC over the LXMF
    `FIELD_COMMANDS = 0x09` field, gated on signature validation +
    allow-lists, args parsed with `shlex.split` (`core.py:5211-5237`);
  - `update_telemetry(telemeter)` — ~60 s tick with a live shared Telemeter
    to mutate in place (`core.py:3350-3357`).
- Hook returns are **ignored**; effects happen by calling back into the core
  (`get_sideband().send_message(...)`) or mutating the Telemeter.
- Config keys: `command_plugins_path` (default `None` ⇒ plugins entirely
  off), `command_plugins_enabled`, `service_plugins_enabled`
  (`core.py:758-760`). Quirk: the load gate is `service_plugins_enabled`
  alone (`core.py:836`).
- Trust model: the UI states *"Loaded plugins have full access to your
  Sideband application — Take extreme caution!"* (`sbapp/main.py:4518`).
- Error isolation has real bugs: `exec()` load errors are caught per file,
  but an exception in `plugin_class(self)` / `start()` **kills the loader
  thread and silently skips all remaining plugins** (`core.py:857-858`); no
  unload/reload, no per-hook timeouts.

### 2.2 NomadNet (no in-process plugins; config-driven scripting)

- **Executable pages**: a `chmod +x` page under `pages/` is run per request
  via `subprocess` with context as **environment variables** (`link_id`,
  `remote_identity`, form fields `field_*`/`var_*`) and its **stdout is
  served** (`nomadnet/Node.py:130-152`); failure serves nothing.
- **Executable `.allowed` ACL files** run to produce allow-lists
  (`Node.py:117-137`).
- Companion processes: standalone LXMF peers that write into `.mu` pages
  (`nomadnet/examples/messageboard/`).

### 2.3 RNS / LXMF

No plugin mechanisms at all — only aspect-filter announce handlers
(`RNS.Transport.register_announce_handler`) and delivery callbacks
(`LXMF/LXMessage.py:267-271`, `LXMF/LXMRouter.py:375-376,1913-1916`). The Go
port already has parity for these (see §5).

---

## 3. The Go-side constraints (all verified)

### 3.1 The dependency rule and what `go mod tidy` actually does

go-reticulum root `go.mod` has **zero requires**, and AGENTS.md makes that a
hard rule with one sanctioned escape hatch: *"The `examples/go.mod` module
may use external dependencies"* (that module does not exist yet — the hatch
is anticipated but uncreated). go-nomadnet **already takes external deps**
(tcell/tview forks, qrterminal, `go-reticulum v0.89.0` as a module
dependency, `go.work` for local dev), so in-process wago is rule-legal
there today.

Empirical go.mod experiments (scratch module, since cleaned):

- Files behind `//go:build wago` are invisible to default builds → plain
  `go build`/`go test` succeed with a pristine go.mod; a tagged build fails
  loudly with "no required module provides package …" (self-documenting
  opt-in).
- `go mod tidy` **scans tagged files** and pulls the dependency into root
  go.mod — so tagged-in-root ⇒ go.mod gains wago (+"x/sys // indirect").
- A **nested module + `go.work`** keeps root go.mod pristine and both plain
  and tagged builds succeed — but `go mod tidy` tries to resolve the
  cross-module import over the network (noisy 404s, exit 0 while
  unpublished).
- The only variant that keeps go-reticulum's go.mod *and* tidy *and* every
  build config pristine is **no cross-module import at all**: subprocess.

### 3.2 The device build matrix is the real reason for build tags

`cmd/publish-github-release-artifacts/main.go` builds `gornsd`, `gorrcd`,
`gornstatus`, `gonomadnet` for **linux/arm64, linux/arm (GOARM=7),
linux/riscv64** with tags `pocket_terminal`, `pocket_communicator`,
`pocket_hub`, `reticulum_asic`, `reticulum_fpga` — stock Go cross-compiles,
`CGO_ENABLED=0 -trimpath`. wago **cannot compile for arm7, riscv64, or 386**
(verified). Existing stub pattern to mirror:

```go
//go:build !embedded && !pocket_communicator && !pocket_hub
// (rns/discovery-exec-embedded.go, rns/interfaces/pipe-subprocess-embedded.go)
```

Proposed tagged-file constraint (degrades gracefully even if someone passes
`-tags wago` on riscv64/arm7 — plugins disabled instead of compile error):

```go
//go:build wago && (linux || darwin || windows) && (amd64 || arm64)
```

The `pocket_terminal` tag currently gates no source file (reserved/no-op in
the release matrices); a dedicated `wago` tag is clearer than reusing it.

### 3.3 A standing policy to amend, not just a feature to add

`README.md:44-47` / `TODO.md:433-437`: *"No External Interface Plugin
Runtime — external interface plugins (like Python's
`<configdir>/interfaces/<Type>.py`) are intentionally not supported"* (out of
supply-chain caution). A wasm host is exactly the safe way to reopen that
door, but it should be framed explicitly as a policy revision (sandboxed
event hooks ≠ arbitrary Python interfaces). Regardless, the
**interface/transport data path should stay out of scope for v1** — a wasm
hop on the packet path is a latency hazard and the wrong first target. Hook
the *event* surfaces, not the wire.

### 3.4 Existing plugin-ish surfaces (context)

- Zero occurrences of "plugin" in any `.go` file in either repo; no wasm
  runtime referenced anywhere.
- Interface instantiation is a closed switch in `rns/rns.go`
  (`initInterfaces`, lines ~1004-2114) — the Python plugin point; reopening
  it is the deferred policy question (§3.3).
- `cmd/gornsh` is not a shell with a command registry — it executes whatever
  program the initiator requests (`session-runtime.go:260`), so wasm tools
  slot in with zero library change.
- `rrc`'s chat hub has a closed 13-case slash-command switch
  (`rrc/commands.go:74-109`) plus the `CommandHandlerHooks` service-locator
  struct — the cleanest "command registry a plugin could extend" candidate.
- `cmd/gornpkg` (Reticulum Meta Package Manager port) is a stub with a
  reserved binary name — a natural future plugin distribution channel.
- `cmd/gorns` does not exist (AGENTS.md naming rule is aspirational); the
  daemon-ish tools are `gornsd`, `gorrcd`, `golxmd`.

---

## 4. Recommended architecture

Three viable shapes; the differences are all about where the wago
dependency lives:

| Variant | go-reticulum go.mod | Latency | Isolation | Complexity |
|---|---|---|---|---|
| **A. `gowagod` helper daemon (subprocess)** — nested module owns wago; root binaries just exec it | **100% pristine, zero changes** | one-time spawn + ~100-200 µs IPC/call | engine crash isolated from daemon; wasm sandbox on top | low |
| **B. In-process wago behind `-tags wago`, root imports it** | gains `wago` (+`x/sys // indirect`) requires — **violates the AGENTS.md rule** | lowest (~µs) | same process | lowest |
| **C. Nested module + workspace import** | pristine while tidy noise tolerated / once published | lowest | same process | medium |

**Recommendation: A for go-reticulum; B-in-a-nested-module for go-nomadnet.**

```
go-reticulum/
  wagoplugins/            # NEW root package, pure stdlib — the ABI + client
    wagoplugins.go        #   kinds, hook contract, manifest schema, JSON protocol
    wago-client.go        #   exec gowago, JSON-RPC over stdio
    wago-client-embedded.go  # //go:build embedded || pocket_communicator || pocket_hub → "not supported" stub
  wagohost/               # NEW nested module github.com/gmlewis/go-reticulum/wagohost
    go.mod                #   requires github.com/wago-org/wago (its only dep)
    engine.go             #   wago Runtime wiring: Policy, limits, rns.* host imports
    cmd/gowago/           #   the daemon: loads plugin set, serves JSON-RPC over stdio/unix socket
go-nomadnet/
  (uses wagoplugins via its existing go-reticulum dependency)
  wagohost-*.go           # //go:build wago && … && (amd64 || arm64) — optional in-process mode
```

- go-reticulum's binaries (`gornsd`, `gorrcd`, `gornsh`, gonomadnet's
  daemon) never import wago. The `wagoplugins` client is stdlib-only and
  compiles everywhere; device builds keep the existing embedded-stub
  pattern; desktop builds get plugin support as soon as a `gowago` binary is
  present — mirroring how RNS itself is already a multi-process ecosystem
  (rnsd, rnstatus, rnsh…).
- go-nomadnet may link wago in-process behind `wago` (deps allowed there) —
  that is where the hot path (TUI, per-message hooks) lives anyway.
- **One wago Runtime hosts all plugins**: compile once per plugin, one
  instance per plugin (or per request, `WithInstanceLimits`), per-call
  `context.WithTimeout`, per-plugin `Policy` (memory pages, table caps,
  capability allow-list), **no WASI installed**. On daemon crash: respawn,
  re-grant, circuit-break (fixing Sideband's loader-thread bug by design).
- **Build tags**: `wago` (new, opt-in) + the established
  `!embedded && !pocket_communicator && !pocket_hub` guard, plus
  `(linux || darwin || windows) && (amd64 || arm64)` per §3.2. Verify with a
  new `-tags=wago` test line in `run-all-tests.sh` (mirroring the existing
  no-race specials) and/or `GO_TEST_TAGS` in `scripts/test-integration.sh`.

**Why `../asic-reticulum` builds are unaffected by construction:** the
pocket/ASIC/FPGA builds are just `go build -trimpath -tags=<pocket tags>`
cross-compiles of `./cmd/*`. (a) they never pass `wago`, so no wago code
compiles into them; (b) the embedded stubs keep the client package from
pulling in anything; (c) root go.mod stays untouched so nothing appears in
their module graph or go.sum; (d) binary size for device artifacts is
unchanged (wago's +8.4 MB appears only in wago-tagged hosted builds).

---

## 5. Where the hooks attach (verified against current code)

- `rns.Transport.RegisterAnnounceHandler(handler *AnnounceHandler)`
  (`rns/transport.go:111,552-557,1365`) — announce observers.
  `AnnounceHandler{AspectFilter string; ReceivedAnnounce func(destinationHash
  []byte, announcedIdentity *Identity, appData []byte)}`.
- `lxmf.Router.RegisterDeliveryCallback(callback func(*Message))`
  (`lxmf/router.go:2100-2104`, invoked outside the router lock at
  `router.go:4422-4429`); per-message `DeliveryCallback`/`FailedCallback`
  (`lxmf/message.go:183-186`).
- go-nomadnet's exported funnel: `App.lxmfDelivery`
  (`nomadnet/app/app.go:1085-1109`) — order: `Ingest` → notify → print →
  **`a.DeliveryCallback(msg)`** → `UIChangeCallback`. The `DeliveryCallback`
  hook fires *after ingest/print, before UI refresh* — the natural plugin
  seam; **a filter that drops messages must be inserted before `Ingest`**,
  not on this hook.
- Page serving: `Destination.RegisterRequestHandler(path, responseGenerator,
  allow, allowedList, autoCompress)` (`rns/destination.go:494`) — where wasm
  "executable pages" replace NomadNet's `chmod +x` subprocess scripts.
- rrc chat: `CommandHandler.HandleOperatorCommand` switch
  (`rrc/commands.go:74-109`) + `CommandHandlerHooks` (14 accessor fields,
  `commands.go:19-58`) — turn the switch into a registry for plugin-provided
  slash commands.
- gornsh listener executes arbitrary programs
  (`cmd/gornsh/session-runtime.go:260`, PTY variant `pty-unix.go:57`) — wasm
  tools slot in with zero library change.
- `cmd/gornpkg` is a reserved, stubbed package-manager entrypoint for future
  plugin distribution.

---

## 6. Plugin ABI v0 — concrete proposal

Guest modules (wat, AssemblyScript, TinyGo, or Rust — wago's example
`examples/22-language-guests` exercises wat/AS/TinyGo against the *same*
host import) export JSON-buffer hooks; the host grants `rns.*` imports per
plugin:

**Guest exports**

- `wagoplugin_alloc(len i32) → ptr` — host→guest input buffer.
- `plugin_manifest() → (ptr, len)` — JSON:
  `{api: 1, kind: "command|service|telemetry|filter|page", name: "...",
  command_name?: "...", grants: [...]}`.
- Lifecycle: `plugin_start() → i32` (0 = ok), `plugin_stop()`.
- Per-kind hooks:
  - `handle_command(req_ptr, req_len) → (ptr, len)` — reply text to send.
  - `update_telemetry(snapshot_ptr, len) → (ptr, len)` — Sideband mutates a
    shared Telemeter; in a sandbox the plugin *returns* telemetry JSON for
    the host to merge.
  - `on_announce(a_ptr, a_len) → i32` — observe announces.
  - `filter_inbound(msg_ptr, len) → i32` — verdict (accept/deny/flag);
    something the Python ecosystem does not have.
  - `render_page(req_ptr, len) → (ptr, len)` — NomadNet executable-page
    analog; returns micron markup.
- Multi-value `(ptr, len)` returns are supported by wago (`inst.Call →
  []Value`).

**Host imports (granted, deny-by-default)**

- `rns.log(ptr, len)` — structured logging (always available).
- `rns.reply(dest_ptr, content_ptr, len) → i32` — scope: reply-to-caller vs
  any destination.
- `rns.announce(app_data_ptr, len) → i32` — service plugins announce a
  destination.
- `rns.kv_get / rns.kv_set` — per-plugin namespaced scratch store.
- `rns.now() → i64` — clock.
- **No WASI.** Capabilities map naturally onto wago's
  `Policy.AllowedCapabilities` and its builtin capability vocabulary.

**Runtime envelope**

- Per-call deadline + per-plugin memory-page cap + module-size cap +
  host-import allow-list.
- SHA-256 pinning of allowed plugins in config; Sideband-style
  `command_plugins_path = None ⇒ plugins off` default.
- Distribution reuses the ecosystem's own signing machinery: `.rsm`
  manifests + `rsg` signatures verified by `gornid`, fetched via `gorngit`,
  installed by `gornpkg`. **Never accept third-party `.wago` artifacts**
  (they are native code); compile raw `.wasm` from untrusted sources via
  `Load`/`Compile`; cache self-produced artifacts for fast daemon startup.

---

## 7. Use-case proposals, ranked

1. **rrc chat slash-command plugins** (Sideband CommandPlugin parity) —
   `/weather`, `/dice`, `/translate` in gorrcd rooms; replies via granted
   `rns.reply`. Lowest risk, immediate visible payoff, exercises the whole
   stack.
2. **NomadNet wasm pages** — sandboxed replacement for executable `.mu`
   pages: same env-var-shaped request context (link_id, remote_identity,
   `field_*`), micron markup returned from guest memory. NomadNet's own
   extension model, upgraded.
3. **LXMF inbound filters/notifiers** in gonomadnet/golxmd (spam scoring,
   keyword routing, auto-acknowledge) — something the Python ecosystem does
   not have (Sideband has no inbound filter hook), enabled via the
   `App.DeliveryCallback` seam (with the §5 ordering caveat).
4. **Announce observers** — presence bots, directory enrichment; announce
   rates make the daemon-resident (not spawn-per-event) model the right
   one.
5. **Telemetry enrichers** — host passes a sensor snapshot; the wasm plugin
   computes derived sensors (Sideband TelemetryPlugin parity).
6. **gornsh wasm tools** — remote sessions invoke `gowago run <plugin>`
   with no library change.
7. **Signed plugin distribution over RNS itself** — the rsm/rsg/`gornpkg`
   synergy; a genuinely Reticulum-native plugin economy.
8. **Explicitly deferred**: interface/transport plugins (the closed
   `initInterfaces` switch) — v1 should not put wasm on the packet path.

---

## 8. Risks and sharp edges

- **Wago is beta (0.1.0)** — pin the engine version; expect artifact-format
  and API churn. We would use wago as a pure engine (hand-provided `rns.*`
  imports), not its Go-level plugin/authority system, so most of its
  surface churn does not affect us.
- **No fuel metering** — deadlines only; keep host imports non-blocking and
  check ctx inside them.
- **amd64 CPU baseline has no CPUID gate** (SSSE3/AVX assumed) — fine for
  the known fleet, worth a note for old-CPU fleets.
- **Virtual address space** — cap `Policy.MaxMemoryBytes` / instance limits
  so a dozen plugins do not reserve tens of GiB of VA.
- **Per-instance call serialization** — concurrency = one instance per
  in-flight call; use instance-per-request for concurrent hooks
  (`examples/06-runtime-service`).
- **Rule tension, explicit**: if wago is ever linked *inside*
  go-reticulum binaries in-process (variant B), that is an AGENTS.md rule
  change (two go.mod lines: `wago` + `x/sys // indirect` — the linked engine
  itself stays stdlib-only). The subprocess design avoids that decision
  entirely, and go-nomadnet does not have to make it at all.
- **Licensing**: wago is Apache-2.0; no conflict with the Reticulum-License
  core (permissive, one-directional embedding is fine).

---

## 9. If/when revisited — concrete first step (M1)

1. `wagohost/` nested module (requires wago; engine wiring: `Policy`,
   limits, `rns.*` host imports) + `cmd/gowago` daemon (JSON-RPC over
   stdio/unix socket, lock file like `gornsd`).
2. Root `wagoplugins/` stdlib client package (exec-based, embedded-stubbed,
   off by default unless `gowago` present and configured).
3. Wire first into the **rrc command registry** (use case #1); golden tests
   for the ABI; a `-tags=wago` line in `run-all-tests.sh`; device builds
   verified unchanged via the release matrix.

The PoC that validated the embed (scratch module, since removed):

```go
// Minimal wasm module: (module (func (export "answer") (result i32) i32.const 42))
var minwasm = []byte{
    0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
    0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,       // type: () -> i32
    0x03, 0x02, 0x01, 0x00,                          // func: type 0
    0x07, 0x0a, 0x01, 0x06, 'a', 'n', 's', 'w', 'e', 'r', 0x00, 0x00,
    0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,  // code: i32.const 42, end
}
// rt := wago.NewRuntime(); mod, _ := rt.Compile(minwasm)
// inst, _ := rt.Instantiate(ctx, mod); out, _ := inst.Call(ctx, "answer")
// → out[0].I32() == 42
```

Host-import demo (guest → host call with guest-memory access) also verified
against a hand-assembled module importing `host.log(ptr, len)` with a data
section — the exact primitive the `rns.*` host-import ABI would use.

### Reference pointers

- wago: `https://github.com/wago-org/wago` (docs: `https://docs.wago.sh`,
  plugin registry: `https://plugins.wago.sh`); local checkout
  `/Users/glenn/src/github.com/wago-org/wago`. Key files: `wago.go`
  (generated facade), `src/wago/{api,runtime,policy,config,extension}.go`,
  `examples/` (02 typed runtime, 03 host import, 06 runtime service,
  07 runtime limits, 08-13 plugin/contracts, 16 serialize, 22 language
  guests), `ARCHITECTURE.md`, `FEATURES.md`, `SPECTEST.md`.
- Sideband plugin system: `sbapp/sideband/plugins.py`,
  `sbapp/sideband/core.py` (loader ~832-905; command dispatch ~5211-5237;
  telemetry ~3350-3357), `sbapp/main.py:4518-4566`,
  `docs/example_plugins/`.
- NomadNet executable pages: `nomadnet/Node.py:117-158`.
- Upstream Reticulum (checked at 1.5.x): no plugin mechanism in RNS/LXMF
  proper; Sideband is the plugin system.