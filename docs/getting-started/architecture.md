# Architecture & Security Policy

Go Reticulum is engineered as a robust, auditable, and resilient network implementation tailored for mission-critical and off-grid communications.

---

## Go Standard Library Architecture

The root Go module (`go.mod`) depends on the **Go Standard Library** and one external
module:

- **One External Runtime Dependency**: `golang.org/x/sys`, for the `poll(2)` check that
  tells a serial port which has hung up from one that is merely idle. A read reports both
  the same way, so the RNode read loop has to ask the port, and the standard library
  cannot ask. The core stack (`rns/`), messaging layer (`lxmf/`), and CLI binaries (`cmd/*`)
  otherwise rely on no external Go modules.
- **Zero Cgo**: Compiles with `CGO_ENABLED=0` to create pure static binaries with no external shared library dependencies (glibc, musl, libssl, etc.).
- **Small Supply Chain**: One pinned module to audit leaves little room for upstream dependency hijacking, typo-squatting, and dependency-tree supply chain attacks (such as the XZ/liblzma backdoor).

All protocol encoding and cryptographic primitives are implemented using Go's standard library packages (`crypto/ed25519`, `crypto/ecdh`, `crypto/aes`, `crypto/cipher`, `crypto/sha256`, `crypto/sha512`, `crypto/hmac`, `crypto/rand`, `encoding/binary`).

---

## Interface Security & Plugin Policy

In Python Reticulum, custom interface types can be loaded dynamically from arbitrary Python script files (`<configdir>/interfaces/<Type>.py`).

In Go Reticulum, **external runtime interface scripts are intentionally not supported**:

1. **Deterministic Execution**: Arbitrary code execution during network stack startup presents severe operational risk in remote or unattended deployments.
2. **Memory Safety**: Go's type-safe, bounds-checked memory runtime protects against buffer overflows and memory corruption vulnerabilities.
3. **Sandboxed Extensibility**: Where runtime extensibility is required, Go Reticulum provides an optional **WebAssembly (Wasm) Plugin Sandbox** (`-tags=wago`), ensuring third-party extensions execute inside isolated memory sandboxes with restricted host capabilities.

---

## Concurrency & Performance

Unlike single-threaded Python RNS which relies on cooperative multitasking and global interpreter locking (GIL), Go Reticulum takes full advantage of multi-core hardware:

- **Goroutine-per-Interface Concurrency**: Every active interface runs isolated I/O loops communicating via typed channels.
- **Non-blocking Dispatch**: Packet queues, link handshakes, and resource transfers proceed concurrently without blocking packet processing on other interfaces.
- **Minimal Memory Overhead**: Base memory footprint is typically under 20 MB RAM, allowing operation on tiny single-board computers (Raspberry Pi Zero, BeagleBone, RISC-V SBCs) where Python RNS would face severe memory pressure.
