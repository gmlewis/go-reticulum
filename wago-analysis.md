# Design: In-Process wago via Per-Command Nested Modules

**Status:** design reference for implementation — not yet coded  
**Repos:** `go-reticulum`, `go-nomadnet` (asic-reticulum / pocket devices out of scope)  
**Engine:** [wago](https://github.com/wago-org/wago), pinned at `v0.1.0-beta.8` or newer tagged beta

This document specifies how to embed the wago WebAssembly runtime **in-process**
in selected Reticulum Go tools, while the repository root module remains
strictly Go standard library.

---

## 1. Intent

Third-party extensions (chat commands, dynamic pages, LXMF filters, announce
observers, remote tools) must run in a real sandbox. wago provides that
sandbox as a pure-Go, no-cgo JIT with deny-by-default host imports and
per-instance resource policy.

`go-reticulum`'s root module may not take external dependencies. The design
that satisfies both requirements is:

> **Each tool that needs wago becomes its own nested Go module under
> `cmd/<name>/`, with its own `go.mod` / `go.sum`, importing the parent
> library via a `replace` directive and linking wago in-process.**
>
> The root `go.mod` never sees wago.

There is **no** helper daemon, **no** stdio RPC, and **no** cross-process
plugin host. Plugin execution happens inside the same process as the tool.

---

## 2. Feasibility (verified 2026-09-11)

A scratch multi-module experiment (parent stdlib module + `cmd/wago-tool`
nested module importing `github.com/wago-org/wago` behind `//go:build wago`)
confirmed:

| Check | Result |
|---|---|
| Parent `go list ./...` / `go test ./...` | Nested `cmd/` packages are **excluded** from the parent module |
| Parent `go.mod` after nested `go mod tidy` **and** parent `go mod tidy` | Stays **zero requires** |
| Nested build without `-tags wago` | Stub compiles; no wago linked |
| Nested build with `-tags wago` | Compiles, links wago, **executes a wasm module** (`answer` → `42`) |
| `go.work` with `use .` + `use ./cmd/<tool>` | Multi-module development works |
| Cross-compile nested module `linux/arm64` | Works |
| Binary size | ~2.4 MB stub → ~10.2 MB with wago (+~7.8 MB) |
| `golang.org/x/sys` | **Not** pulled into the nested module for embedding (wago `src/` is stdlib-only; x/sys is CLI-only upstream) |

**Pin wago explicitly.** Bare `go mod tidy` resolved a **retracted canary**
(`v0.1.0-canary.ge844da4`). Always `go get github.com/wago-org/wago@v0.1.0-beta.8`
(or the current tagged beta) before tidy.

---

## 3. Module layout

### 3.1 Root module (unchanged policy)

```
go-reticulum/
  go.mod          # module github.com/gmlewis/go-reticulum — stdlib only
  go.sum          # empty / absent of external requires
  rns/ lxmf/ rrc/ compress/ micron/ ...
  cmd/
    gornsd/       # stays in root module (unless promoted, §5)
    gornstatus/   # stays in root module
    ...           # other stdlib-only tools
```

Root `AGENTS.md` rule stands: **no external dependencies in the root module.**

### 3.2 Nested wago-enabled command module

```
go-reticulum/
  cmd/
    gorrcd/
      go.mod      # module github.com/gmlewis/go-reticulum/cmd/gorrcd
      go.sum      # pins wago (+ any indirects the engine actually needs)
      main.go     # package main — same CLI as today
      plugins_wago.go   # //go:build wago && (linux||darwin||windows) && (amd64||arm64)
      plugins_stub.go   # //go:build !wago  → "plugins unavailable"
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

- Module path is `github.com/gmlewis/go-reticulum/cmd/<tool>` (nested under
  the parent path; standard for in-repo tool modules).
- `replace => ../..` makes local builds and CI use the working tree, not a
  published parent version.
- Do **not** publish these nested modules as separate versioned modules unless
  there is a concrete need; releases are built from source in CI.

### 3.3 Workspace (development)

Root `go.work` (new or extended):

```
go 1.26.4

use .
use ./cmd/gorrcd
use ./cmd/gornx
use ./cmd/gornsh
# ... every nested wago tool
```

`go-nomadnet`'s existing `go.work` stays `use .` + `use ../go-reticulum`.
If go-nomadnet ever needs a nested wago module, add it there separately.

With `go.work` present, `go build -tags wago ./cmd/gorrcd` from the repo root
works. Without `go.work`, build from inside the command directory:

```
cd cmd/gorrcd && go build -tags wago -o ../../bin/gorrcd .
```

---

## 4. Build tags

| Tag | Meaning |
|---|---|
| *(none)* | Tool builds without plugin host; wago not linked; `plugins_stub.go` active |
| `wago` | Enable in-process wago plugin host (`plugins_wago.go`) |
| `wago` + platform | Host file constraint: `//go:build wago && (linux \|\| darwin \|\| windows) && (amd64 \|\| arm64)` |

Rules:

1. The nested `go.mod` **always requires wago** (module graph honesty). Tags
   only control whether wago *code* is compiled into a given binary.
2. Desktop and `pocket_terminal-linux-arm64` release builds that should ship
   plugins pass `-tags=wago` (plus existing pocket/asic tags as needed).
3. linux/arm7, linux/riscv64, and freebsd builds of the same tool **omit**
   `wago` (or use a stub file so `-tags wago` still compiles). They must never
   fail to build because of the platform constraint.
4. Root-module tools never mention the `wago` tag at all.

Existing tags (`embedded`, `pocket_communicator`, `pocket_hub`,
`pocket_terminal`, `reticulum_asic`, `reticulum_fpga`, `integration`) continue
to work unchanged and combine with `wago` where the platform allows.

---

## 5. Which commands become nested modules

Promote **only** tools that link wago. Everything else stays in the root module.

| Command | Promote? | Why |
|---|---|---|
| `cmd/gorrcd` | **yes** | RRC slash-command plugins |
| `cmd/gornsd` | **yes** | LXMF inbound filters/notifiers, announce observers, service plugins |
| `cmd/gornx` | **yes** | Sandbox remote tools (replace unscoped shell execution) |
| `cmd/gornsh` | **yes** | Optional wasm tools in remote sessions |
| `cmd/golxmd` | optional | Only if it hosts filters independently of gornsd |
| `cmd/gornpkg` | later | Plugin install/signing; keep stub until distribution is real |
| `gornstatus`, `gornpath`, `gornid`, `gornprobe`, `gorncp`, `gorngit`, … | **no** | No plugin host required |
| `cmd/publish-github-release-artifacts` | **no** | Build tooling; must stay able to orchestrate all modules |

`go-nomadnet` is a **different repo** with its own root `go.mod` that already
allows external deps. It adds wago **directly** to `go.mod` (no nested module
required), still behind the `wago` build tag for optional/stub builds.

### 5.1 Release-matrix impact (must fix when promoting)

`cmd/publish-github-release-artifacts` currently does, from the repo root:

```go
buildArgs = append(buildArgs, "-o", j.outPath, "./cmd/"+j.binaryName)
cmd := exec.CommandContext(ctx, "go", buildArgs...)
```

(`main.go` ~line 597). That path is invalid for nested modules.

Required change: if `cmd/<name>/go.mod` exists, run the equivalent of

```
go build -trimpath -p 1 -tags=<tags> -o <out> .
```

with working directory `cmd/<name>` (or `go build -C cmd/<name> ...`). Binary
discovery (`discoverBinaryNames`) can stay directory-based; only the build
invocation and any `./cmd/...` package paths need the nested-module branch.

Desktop targets (linux/darwin/windows × amd64/arm64) of promoted tools add
`wago` to `buildTags`. Hardware targets for arm7/riscv64 omit it.

---

## 6. Dependency inversion (stdlib stays clean)

Wago-linked code lives **only** in nested `cmd` modules (and in go-nomadnet).
Library packages under the parent module define plain Go hooks; commands inject
implementations.

### 6.1 Existing seams (no new library API required for M1)

| Seam | Package | Injection |
|---|---|---|
| Slash commands | `rrc.CommandHandlerHooks` / command table | Nested `gorrcd` supplies extra handlers |
| LXMF delivery | `lxmf.Router.RegisterDeliveryCallback` | Nested `gornsd` / go-nomadnet registers filter/notifier |
| Announces | `rns.Transport.RegisterAnnounceHandler` | Nested `gornsd` registers observers |
| Request handlers | `rns.Destination.RegisterRequestHandler` | Nested tools / go-nomadnet node serve wasm pages |
| Remote execute | `cmd/gornx` request handler | Nested `gornx` dispatches to wasm instead of raw shell |

### 6.2 Library-side interfaces (stdlib, when a shared ABI is needed)

If multiple tools must share plugin types, add **interfaces only** under the
parent module (no wago import), e.g. in `rns` / `rrc` / a small
`pluginapi` package:

```go
// Conceptual — exact types chosen at implementation time.
type SlashCommandPlugin interface {
	Name() string
	Handle(ctx context.Context, req SlashCommandRequest) (string, error)
}

type InboundFilter interface {
	// Return keep=false to drop before ingest.
	Filter(ctx context.Context, msg *lxmf.Message) (keep bool, err error)
}
```

Nested modules implement these with wago. The parent module never imports wago.

go-nomadnet may define the same interfaces locally or depend on the parent
module's copies via its existing `go-reticulum` require.

---

## 7. Plugin ABI (guest ⇄ host)

Guests are `.wasm` modules. Host imports are **only** what the tool grants.

### Guest exports

| Export | Role |
|---|---|
| `wagoplugin_alloc(len i32) → ptr` | Host→guest buffer |
| `plugin_manifest() → (ptr, len)` | JSON `{api, kind, name, grants[]}` |
| `plugin_start() → i32` / `plugin_stop()` | Lifecycle |
| `handle_command(req_ptr, req_len) → (ptr, len)` | Slash-command reply |
| `filter_inbound(msg_ptr, len) → i32` | Accept / deny / flag |
| `on_announce(a_ptr, a_len) → i32` | Announce observer |
| `render_page(req_ptr, len) → (ptr, len)` | Micron markup |
| `update_telemetry(snapshot_ptr, len) → (ptr, len)` | Telemetry JSON for host merge |

Kinds: `command | service | telemetry | filter | page`.

### Host imports (deny-by-default)

| Import | Notes |
|---|---|
| `rns.log(ptr, len)` | Always available |
| `rns.reply(...)` | Scope-limited by the granting tool |
| `rns.announce(...)` | Service plugins |
| `rns.kv_get` / `rns.kv_set` | Per-plugin scratch store |
| `rns.now() → i64` | Clock |

No WASI. No Component Model. Map grants onto wago `Policy.AllowedCapabilities`
and memory/table caps. Per-call `context.WithTimeout`. Instance-per-request
when hooks must run concurrently.

### Artifact rules

- Load **raw** `.wasm` via `Compile`/`Load` only.
- Cache **self-produced** `.wago` artifacts for startup speed
  (`LoadTrustedArtifact`).
- **Never** accept third-party compiled artifacts (native code).
- SHA-256 pin plugin modules in the tool's config; plugins off by default.

---

## 8. go-nomadnet

Because that repo already takes external dependencies:

1. Add `github.com/wago-org/wago` to `go.mod` (pinned beta).
2. Host code behind
   `//go:build wago && (linux || darwin || windows) && (amd64 || arm64)`
   with a `!wago` stub.
3. Primary integration: **wasm executable pages** in `nomadnet/node` —
   pass request context into the page handler; guest returns micron. This
   closes the documented "executable pages not supported" gap.
4. Secondary: inbound filters on the delivery funnel (multi-subscriber
   callbacks; drop **before** `Ingest`), slash-command default branch,
   outbound/print intercepts.
5. Do **not** add a Plugins item to the parity-pinned TUI menu.

No nested module is required in go-nomadnet unless a future tool there wants
to isolate wago the same way.

---

## 9. asic-reticulum and device builds

- asic-reticulum compiles no Go; no module change.
- ESP32-C5 / riscv64 / arm7 pocket artifacts: never pass `-tags wago`.
- `pocket_terminal-linux-arm64` (RPi Zero 2W): **may** ship `-tags wago`
  binaries of nested tools (gorrcd, gornsd, …).
- Hardware Projects Guide may later note which pocket artifacts include the
  plugin host; asic-reticulum itself needs no code change.

---

## 10. Prioritized use cases

1. **go-nomadnet wasm pages** — parity gap, in-process, highest user value.
2. **gorrcd slash-command plugins** — first nested-module proof.
3. **gornsd inbound filters / notifiers** — multi-subscriber delivery.
4. **Announce observers** in gornsd.
5. **gornx sandboxed tools**.
6. **Telemetry enrichers** (node event hooks in go-nomadnet / gornsd).
7. **gornsh wasm tools**.
8. **gornpkg signed distribution** — after the package manager exists.

Deferred: interface/transport plugins on the packet path; WASI; Component
Model guests; freebsd/arm7/riscv64 hosts.

---

## 11. Implementation roadmap

### M1 — one nested tool end-to-end

1. Pick **`cmd/gorrcd`** (or `cmd/gornsd` if that is the active mission).
2. Add `cmd/<tool>/go.mod` + `go.sum` with `replace => ../..` and pinned
   wago `v0.1.0-beta.8`.
3. Split plugin host into `plugins_wago.go` (tagged) and `plugins_stub.go`.
4. Wire one slash-command (or filter) through an existing hook seam.
5. Update `cmd/publish-github-release-artifacts` for nested-module builds
   (`-C cmd/<tool>` / `Dir`).
6. Update `run-all-tests.sh` / `scripts/test-all.sh` to `go test` nested
   modules (with and without `-tags wago` on supported platforms).
7. Update root `go.work` with `use ./cmd/<tool>`.
8. Update `AGENTS.md`: root module still stdlib-only; nested `cmd/*` modules
   may require wago.
9. Verify: root `go test ./...` clean and wago-free; nested default build
   works; nested `-tags wago` build runs a golden plugin test; arm7/riscv64
   hardware targets still build **without** `wago`.

### M2 — second host + go-nomadnet pages

- Promote `gornsd` and/or `gornx` the same way.
- go-nomadnet `-tags wago` page renderer with request context.

### M3 — breadth

- Filters, announce observers, telemetry, gornsh tools.
- Plugin config UX, SHA-256 pins, artifact cache.
- Optional: pocket_terminal-linux-arm64 release notes.

### Checklist for promoting any `cmd/<name>`

- [ ] `cmd/<name>/go.mod` + pinned `go.sum`
- [ ] `replace github.com/gmlewis/go-reticulum => ../..`
- [ ] `plugins_wago.go` / `plugins_stub.go` (or equivalent) split
- [ ] `go.work` entry
- [ ] Nested `go test` in CI (default + `-tags wago` where supported)
- [ ] Release builder nested-module branch
- [ ] Root `go test ./...` still passes with no wago in root `go.mod`
- [ ] arm7/riscv64/freebsd builds of that tool succeed without `-tags wago`

---

## 12. Risks and constraints

| Risk | Mitigation |
|---|---|
| Nested modules drop out of root `./...` | Explicit nested test/build in CI; document in AGENTS.md |
| `go build ./cmd/<name>` from root fails | Use `go.work` or `-C cmd/<name>`; fix release builder |
| `go mod tidy` selects retracted wago canary | Always pin `@v0.1.0-beta.N` (or newer tag) first |
| wago beta API/artifact churn | Pin tags; use engine + hand-provided imports only |
| +~8 MB binary | Only wago-enabled desktop/arm64 artifacts |
| No fuel metering | Context deadlines; non-blocking host imports |
| amd64 CPU baseline (SSSE3/AVX) | Document for old fleets; fine for known hardware |
| VA reservation per instance | `Policy.MaxMemoryBytes` + instance limits |
| Single-callback delivery fields (go-nomadnet) | Fan-out list before enabling filter plugins |
| Parity-pinned TUI menu | No new menu items in parity builds |
| `cli-contract_test.go` walks `cmd/` | Still works (filesystem); nested main packages remain valid mains |

---

## 13. Worked embed example

Validated against `wago v0.1.0-beta.8` inside a nested module:

```go
//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

package main

import (
	"context"

	wago "github.com/wago-org/wago"
)

// (module (func (export "answer") (result i32) i32.const 42))
var minwasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0a, 0x01, 0x06, 'a', 'n', 's', 'w', 'e', 'r', 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x2a, 0x0b,
}

func runAnswer(ctx context.Context) (int32, error) {
	rt := wago.NewRuntime()
	defer rt.Close()
	mod, err := rt.Compile(minwasm)
	if err != nil {
		return 0, err
	}
	inst, err := rt.Instantiate(ctx, mod)
	if err != nil {
		return 0, err
	}
	defer inst.Close()
	out, err := inst.Call(ctx, "answer")
	if err != nil {
		return 0, err
	}
	return out[0].I32(), nil
}
```

Host import (guest memory, zero-copy) is the same `wago.HostFunc` +
`m.Memory()[ptr:ptr+n]` pattern used for `rns.log` / `rns.reply`.

---

## 14. References

- wago: `https://github.com/wago-org/wago` — `src/wago/{api,runtime,policy,config}.go`,
  `FEATURES.md`, `CHANGELOG.md`, examples 02 (typed runtime), 03 (host import),
  06–07 (service/limits), 16 (serialize), 17 (managed instances), 22 (languages).
- Parent hooks: `rns/transport.go` (`RegisterAnnounceHandler`),
  `lxmf/router.go` (`RegisterDeliveryCallback`), `rns/destination.go`
  (`RegisterRequestHandler`), `rrc/commands.go` (`CommandHandlerHooks`),
  `cmd/gornx`, `cmd/gornsh`, `cmd/gornsd`.
- go-nomadnet: `nomadnet/app/app.go` (delivery funnel),
  `nomadnet/node/node.go`, `nomadnet/browser/browser.go` (executable-page gap),
  `tui/room-widget.go` (slash-command default branch).
- Release orchestration: `cmd/publish-github-release-artifacts/main.go`
  (`discoverBinaryNames`, `buildAll`, hardware targets).
- Policy text to amend when implementing: root `AGENTS.md` (stdlib-only root;
  nested `cmd` modules may require wago), `README.md` interface-plugin note.
- Sideband plugin contract (semantic reference only):
  `~/src/github.com/markqvist/Sideband/sbapp/sideband/plugins.py`.
- NomadNet executable pages (Python reference): `nomadnet/Node.py`.
