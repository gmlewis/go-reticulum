# Wago-powered WebAssembly plugins for go-reticulum / go-nomadnet

**Analysis only — no code has been written or merged.**

- Original feasibility pass preserved **2026-09-09** (sections 1–9).
- Independent re-review completed **2026-09-11** (section 10): architecture
  recommendation still stands; several facts were corrected and several new
  integration opportunities were found. Read §10 before acting on §1–9.

This document records a feasibility analysis of supporting sandboxed
WebAssembly plugins in `go-reticulum` and `go-nomadnet`, using
[wago](https://github.com/wago-org/wago) (local checkout:
`/Users/glenn/src/github.com/wago-org/wago`) as the wasm engine, with Go build
tags so that asic/embedded builds are unaffected. All facts marked ✅ were
verified empirically (code built and executed, scratch tests) rather than
assumed; other facts carry file:line citations from the analyzed repos.

The conclusion was: **feasible and a natural fit — but not pursued right now.**
This document exists so the design work does not have to be redone.

The 2026-09-11 re-review **agrees with that conclusion and with the core
architecture** (subprocess `gowagod` for go-reticulum; optional in-process
`-tags wago` for go-nomadnet). It does **not** overturn the design — it
tightens the facts and widens the opportunity set.

---

## TL;DR

- Feasible and a natural fit. wago embeds cleanly in Go (proved end-to-end with
  a PoC) and has a real sandbox: deny-by-default imports, per-instance
  resource policies, context-deadline preemption, and an explicit
  trusted/untrusted artifact split in its API.
- The interesting constraint is **not wago** — it is (a) the hard
  no-external-dependencies rule on go-reticulum's root module, and (b) parts
  of the device build matrix (linux/arm7, linux/riscv64, 386, freebsd),
  where **no wasm JIT can exist** (wago fails to cross-compile there —
  verified). Note (§10.2): **linux/arm64 pocket_terminal (RPi Zero 2W) *can*
  host wago**; only the arm7/riscv64/ESP32-class artifacts are hard-out.
- Both are solved cleanly with build tags + one nested module. Recommended:
  **subprocess `gowagod` helper daemon for go-reticulum** (root go.mod stays
  100% pristine — verified), and **optional in-process wago for go-nomadnet**
  behind a `wago` build tag (that repo already takes external deps, so it is
  rule-legal there).
- `../asic-reticulum`'s own compile/CI graph is unaffected by construction
  (it compiles no Go). Sibling-repo **pocket binaries** stay unaffected only
  as long as they never pass the `wago` tag; `pocket_terminal-linux-arm64`
  could opt in later (§10.5).
- The wasm plugin system would mirror the hook contract of the official Python
  ecosystem (Sideband's Command/Service/Telemetry plugins, NomadNet's
  executable pages) while **upgrading** the trust model from raw `exec()` of
  user Python to an actual sandbox.

---

## 1. What wago is (verified)

wago (`github.com/wago-org/wago`, local checkout
`/Users/glenn/src/github.com/wago-org/wago`) is a **pure-Go, no-cgo
WebAssembly JIT engine** (single-pass native codegen, Valent-Block/WARP
derived): Apache-2.0. At the 2026-09-09 pass the checkout was early 0.1.0
beta; at the 2026-09-11 re-review it is `v0.1.0-beta.7-7-g963b74d9`, with
`CHANGELOG.md` documenting `v0.1.0-beta.8` (2026-09-10). **Pin a tagged
release**, not `main`.

Conformance pedigree (from its own docs; numbers refreshed in §10): MVP spec
suite fully passing; Core 3.0 suite now cited as 2,226 modules / 58,038
assertions with zero failures/skips (`FEATURES.md`), plus Component Model
Preview 2 as an Authority-gated external plugin (`wago-org/component-model`)
and WASI kept out of core (`wago-org/wasi`).

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
| Cross-compile matrix | linux/arm64 ✅, linux/amd64 ✅, darwin/amd64 ✅, darwin/arm64 ✅, windows/amd64 ✅, windows/arm64 ✅; **linux/arm (GOARM=7) ❌, linux/riscv64 ❌, linux/386 ❌, freebsd/* ❌** (re-verified 2026-09-11; see §10.2 for the pocket_terminal arm64 nuance) |
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

---

## 10. Re-review findings (2026-09-11)

Independent re-analysis of the local wago checkout (`v0.1.0-beta.7-7-g963b74d9`),
`go-reticulum`, `go-nomadnet`, and `asic-reticulum`, against sections 1–9.

### 10.1 Verdict on the original analysis

**Agree.** The core conclusions still hold and should be followed if/when this
is revisited:

| Claim | Verdict |
|---|---|
| wago is a pure-Go, no-cgo, embeddable wasm JIT with a real sandbox | **Confirmed** (`src/wago/policy.go:8-32`, `runtime.go` deny-by-default imports, `Load` vs `LoadTrustedArtifact`) |
| go-reticulum root `go.mod` must stay stdlib-only | **Confirmed** (`AGENTS.md:54,75`; `go.mod` still has zero `require`s) |
| go-nomadnet may take external deps | **Confirmed** (`go.mod` already requires tcell/tview forks, `go-reticulum v0.100.0`, etc.; `go.work` = `.` + `../go-reticulum`) |
| Recommended shape: subprocess `gowagod` for go-reticulum; optional in-process `-tags wago` for go-nomadnet | **Confirmed** — still the right split |
| Device/ASIC builds unaffected by construction | **Confirmed for the compile graph**, with one important nuance (§10.2) |
| v1 should hook event surfaces, not the packet/interface path | **Confirmed** — still the right scope cut |
| Highest-value first target: rrc chat slash-commands | **Still valid**, but see §10.4 — go-nomadnet executable pages are now the *parity-motivated* #1 |

### 10.2 Corrections to sections 1 and 3

1. **Pocket matrix is not uniformly hostile to wago.** The release matrix
   (`cmd/publish-github-release-artifacts/main.go:98-132`) builds
   `pocket_terminal` / `pocket_hub` / `pocket_communicator` for
   **linux/arm64**, linux/arm7, and linux/riscv64 (plus linux/amd64 for
   pocket_hub). Project 1 in `asic-reticulum/Hardware-Projects-Guide.md` is a
   **Raspberry Pi Zero 2W = linux/arm64** — a wago-capable GOARCH. Only
   arm7 (some pocket builds), riscv64 (ESP32-C5-class), and 386 are hard-out.
   Implication: **`gowagod` / a `wago`-tagged `gonomadnet` can legitimately
   ship for `pocket_terminal-linux-arm64`**, while remaining stubbed for
   arm7/riscv64 artifacts. The original text lumped all pocket tags together.
2. **Desktop matrix also includes `freebsd/amd64`** (`main.go:80`), which wago
   does not support. Tagged builds must degrade gracefully there too — the
   existing `//go:build wago && … && (amd64 \|\| arm64)` constraint already
   handles this if freebsd is excluded (wago docs list only
   linux/darwin/windows). Prefer an explicit
   `(linux \|\| darwin || windows)` guard so freebsd builds never fail.
3. **Version / conformance numbers are stale.** Local checkout is
   `v0.1.0-beta.7-7-g963b74d9`; `CHANGELOG.md` has `v0.1.0-beta.8`
   (2026-09-10) with callback re-entry, resource policies, and Core 3/GC work.
   `FEATURES.md` now cites Core 3: **2,226 modules / 58,038 assertions, zero
   fail/skip**. The 2026-09-09 numbers (16,592 MVP / 48,248 v2 / 96,972
   checks) remain historical, not current.
4. **wago's own plugin system is Go-native host composition, not third-party
   guest wasm.** `plugin/contracts.go` + examples 08–13 implement typed
   Contracts, Authority grants, Lifecycle, and lease-based `Ref[T].With`.
   That is for *trusted host-side Go plugins* composing capabilities inside a
   wago Runtime. It does **not** replace the untrusted-guest ABI in §6, and
   the 2026-09-09 decision to treat wago as a pure engine for untrusted
   `.wasm` is still correct. It *can* be reused inside `gowagost`/`gowagod`
   to compose the trusted `rns.*` host-import implementations.
5. **Component Model Preview 2 and WASI are external, Authority-gated
   plugins** (`wago-org/component-model`, `wago-org/wasi`; `ROADMAP.md:73-78`).
   Still correct to install neither in v1. Component Model is the obvious
   future richer ABI if JSON-buffer hooks prove limiting — deferred.
6. **`Policy.MaxInvokeDuration` remains deprecated** (`policy.go:28-31`
   returns `ErrUnsupported`). Deadline-only CPU control is unchanged. Beta.8
   adds callback re-entry (relevant if a host import must call back into the
   guest); keep host imports non-blocking and ctx-checked.
7. **Standalone native executables** (`wago compile` → tiny native binary;
   README "Standalone executables") is an additional distribution path the
   original analysis did not name: a trusted host could AOT-compile a pinned
   `.wasm` to a native tool for `gornsh`/`gornx` without keeping the JIT
   resident. Useful later; not required for `gowagod`.
8. **Managed instances / prepared calls** (examples 06, 07, 17) are the right
   building blocks for instance-per-request page serving — worth citing in
   the runtime envelope of §6.

### 10.3 go-reticulum: confirmed surfaces + new ones

Confirmed still present (file:line re-checked 2026-09-11):

- `rns.Transport.RegisterAnnounceHandler` — `rns/transport.go:110-111,1365`
- `lxmf.Router.RegisterDeliveryCallback` — `lxmf/router.go:2100-2101`
- `Destination.RegisterRequestHandler` — `rns/destination.go:493-503`
- `rrc.CommandHandlerHooks` + closed slash-command switch —
  `rrc/commands.go:19-58,74-109`
- `cmd/gornsh` executes whatever program the initiator requests
- `cmd/gornpkg` still a stub (init Reticulum and exit) — still the future
  distribution channel
- README/TODO policy still bans external Python interface plugins
  (`README.md:44-47`; `TODO.md:433-437`)

**New hook surfaces the original analysis underweighted:**

| Surface | Why it matters for wasm plugins |
|---|---|
| `cmd/gornx` — rnx-compatible remote command execution (`cmd/gornx/main.go`, `RegisterRequestHandler("command", …)` at `:343`) | A sandboxed wasm tool host is a *better* fit than gornsh for untrusted remote invocation: the listener already does identity allow-lists; wasm replaces "run a shell command" with "run a pinned plugin". Elevate next to use-case #6. |
| `cmd/gorncp` `fetch_file` handler (`cmd/gorncp/listen.go:244`) | Optional policy hooks (quota, path rewrite, audit) — lower priority. |
| `cmd/gorngit` request handlers + micron page tree (`cmd/gorngit/server.go:302-313`, `pages-handlers2.go:1274-1289`) | Dynamic wasm-rendered pages or release-notes generators on top of an already-large page surface. Deferred. |
| Blackhole updater `/list` handler (`rns/transport.go:1081`) | Not a plugin target; listed only so it is not mistaken for an extension point. |

No other callback/hook registries were found. Zero `.go` files still contain
"plugin"/"wasm"/"wago" (other than this document). The subprocess + nested
module recommendation is unchanged.

### 10.4 go-nomadnet: confirmed surfaces, gaps, and new opportunities

**Confirmed exactly as documented:**

- Delivery funnel order is still
  `Ingest → notify → print → DeliveryCallback → UIChangeCallback`
  (`nomadnet/app/app.go:1084-1109`). A drop-filter still must run *before*
  `Ingest`, not on `DeliveryCallback`.
- External deps already allowed; a `wago` build tag fits existing precedent
  (`integration`, `no_tui`, `embedded`, `pocket_*`).

**Critical gap the original analysis assumed away:** the Go port has **no
executable-page runtime at all**. `node.ServePage` is a bare `os.ReadFile`
(`nomadnet/node/node.go:582-590`); `makePageHandler` (`node.go:251-269`)
receives request `data` + `remoteIdentity` and **discards the data**. The
browser even documents the gap:
`nomadnet/browser/browser.go:719-723` — *"Executable pages … are NOT executed
here: the Go port does not support executable pages at all."* Client-side
`request_data` (`var_*`) *is* parsed and sent (`browser.go:216`,
`partials.go:103`).

This **raises use-case #2 (wasm pages) to co-#1 with rrc slash-commands**:
wasm would not merely replace Python `chmod +x` subprocesses — it would close
a live NomadNet parity hole, with a better trust model, and the request
context plumbing already exists on the client.

**Structural constraints for any plugin host:**

- `DeliveryCallback` / `UIChangeCallback` are **single func fields**, not
  lists (`app.go:197-199`). A plugin host needs multi-subscriber semantics
  (chain, or a fan-out field).
- TUI slash commands are a closed switch (`tui/room-widget.go:534-664`);
  unknown commands hit a single default branch at `:661` — the insertion
  point for plugin-provided `/cmd`s. Server-side RRC commands remain the
  better first target (gorrcd).
- The TUI menu is a **static 8-item slice pinned by parity tests**
  (`tui/theme.go:196-205`, `tui/menu-structure_test.go`). Do **not** add a
  Plugins menu item in a parity-sensitive build; inject into existing
  displays or gate behind a non-parity build tag/config.
- Micron renderer is mature (`nomadnet/micron/` Parse/RenderToTView) — wasm
  guests returning micron markup plug straight in.

**New opportunities beyond the original list (all go-nomadnet-local):**

1. **Outbound send hook** — `nomadnet/conversation/send.go:119` wires
   `MessageNotification`; plugins could stamp, log, or encrypt-to-self
   outbound traffic (the original list was inbound-only).
2. **Print pipeline intercept** — `nomadnet/app/printing.go:50-66` already
   shells out to a config `PrintCommand`; a wasm formatter beside
   `ShouldPrint`/`PrintMessage` is a second natural seam and a safer
   alternative to arbitrary shell.
3. **Announce enrichment** — four closed announce handlers
   (`app.go:1112-1200`: LXMF/node/PN/RRC) could feed a wasm observer for
   directory enrichment (Sideband-like).
4. **Partials composition** — `browser/partials.go` `FetchPartial` is a clean
   content-composition point for plugin-authored partials.
5. **Node event hooks** — `OnPeerConnected/OnPageServed/OnFileServed/
   OnAnnounced` (`node.go:94-97`) are already one-way App←Node seams; a
   telemetry-style plugin can hang off them without touching RNS.

**Not a goal today:** go-nomadnet has **zero** mentions of plugin/wasm/wago/
Sideband/telemetry in sources or TODO. Sideband plugin parity is not part of
the current Python-parity mission. This document remains forward-looking.

### 10.5 asic-reticulum: intersection is real only for pocket_terminal arm64

asic-reticulum is **not a Go project**: SpinalHDL (Scala) RTL for a
fixed-function Reticulum crypto coprocessor (SHA-256 IFAC stamper, X25519
ladder, AES-128-CBC + HMAC-SHA256 token engine, QSPI slave) plus ESP32-C5
**C** firmware (`fw/esp32c5/`), Python HIL (`tools/hil/`), and ScalaTest
parity against checked-in golden vectors. No `go.mod`, no Go in CI.

| Layer | wago intersection |
|---|---|
| ASIC / FPGA fabric | **None** |
| ESP32-C5 firmware (C, RISC-V MCU) | **None** — wago cannot target it |
| HIL (`tools/hil/*.py`) | **None** — Python runner, not a wago host |
| SpinalSim / golden vectors | **None** on the asic side; a future go-reticulum `gen-golden-vectors` wasm plugin is a *go-reticulum* tooling idea only |
| Pocket Linux Terminal (RPi Zero 2W, `linux/arm64`) | **Yes, optionally** — this is a normal wago GOOS/GOARCH. `gowagod` or a `-tags wago` gonomadnet/gorrcd is architecturally allowed here |
| Pocket Communicator / Hub on arm7 or riscv64 | **None** (same as §3.2) |

The 2026-09-09 claim "asic-reticulum builds are unaffected by construction"
remains **true for asic-reticulum's own compile/CI graph** (it compiles no
Go). It was **over-broad as an ecosystem claim**: `Hardware-Projects-Guide.md`
documents users `curl`ing `gonomadnet-pocket_terminal-linux-arm64` and
`gorrcd-pocket_*` artifacts from the sibling Go repos. If those binaries ever
gain optional wago support, the *guide* may need a one-line note; asic-reticulum
itself still needs no change. (Also: go-nomadnet currently has **no** publish
workflow — the guide's gonomadnet URLs are ahead of the automation.)

### 10.6 Updated recommended use-case ranking

1. **go-nomadnet wasm executable pages** — closes a documented parity gap;
   request context already parsed client-side; highest user-visible value.
2. **rrc chat slash-command plugins** (gorrcd) — as before; lowest risk,
   exercises the full `gowagod` stack.
3. **LXMF inbound filters/notifiers** (gonomadnet/golxmd) — still novel vs
   Sideband; must insert before `Ingest`; requires multi-subscriber delivery
   hooks.
4. **Announce observers / directory enrichment.**
5. **gornx sandboxed tools** — replace or wrap "execute a shell command"
   with pinned wasm tools on an existing identity-allow-listed listener.
   (New; elevates former #6 gornsh.)
6. **Telemetry enrichers** (Sideband TelemetryPlugin parity) — hang off node
   event hooks and/or a periodic tick.
7. **Outbound send + print intercepts** (new, go-nomadnet-local).
8. **gornsh wasm tools** — as before; `wago compile` standalone exes are an
   optional AOT variant.
9. **Signed plugin distribution** via rsm/rsg/`gornpkg`/`gorngit` — still the
   endgame; `gornpkg` remains a stub.
10. **Explicitly deferred:** interface/transport plugins on the packet path;
    Component Model guests; WASI; asic-reticulum HIL; freebsd/arm7/riscv64
    hosts.

### 10.7 What would change the recommendation

Re-evaluate (do not just execute §4/§9) if any of these become true:

- wago **drops** linux/arm64 or changes license.
- wago's artifact format or embed API breaks in a way that pinning cannot
  absorb (beta churn is expected — pin tags).
- A fuel-metering primitive appears that is cheaper/safer than context
  deadlines for our host-import style.
- go-reticulum's no-external-deps rule is relaxed — then variant B (in-process
  behind a tag) becomes legal for desktop `gornsd`/`gorrcd` too, and the
  subprocess daemon is optional.
- go-nomadnet's mission starts requiring Sideband plugin parity.

### 10.8 Suggested M1 delta vs section 9

Section 9's M1 is still correct, with three amendments:

1. Build the guest-facing page hook **first inside go-nomadnet's node**
   (make `makePageHandler` pass request context; wasm behind `-tags wago`),
   not only rrc commands — it is the parity-driven win.
2. Wire `gowagod` as the go-reticulum-side host and point **both** gorrcd
   slash-commands and go-nomadnet pages at the same JSON-RPC ABI, so one
   daemon serves the fleet on linux/arm64 pocket_terminals and desktops.
3. Keep `//go:build wago && (linux || darwin || windows) && (amd64 || arm64)`
   and verify **three** matrix rows explicitly: linux/amd64 (host),
   linux/arm64 (pocket_terminal), freebsd/amd64 (must compile *without* wago,
   plugins stubbed).
