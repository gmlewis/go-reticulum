# `recovery_esptool.py`

This directory intentionally contains one checked-in Python file:
`recovery_esptool.py`.

The rest of this repository is still a Go port. This file is the current
exception because `gornodeconf` needs an ESP recovery/extract helper that the
original Python utility also generated dynamically.

## Where it came from

The immediate source of truth is the original Reticulum utility:

- `github.com/markqvist/Reticulum/RNS/Utilities/rnodeconf.py`
- `RT_PATH = CNF_DIR+"/recovery_esptool.py"` at original line 369
- `extract_recovery_esptool()` at original line 4513

The original Python utility calls `extract_recovery_esptool()` before using the
helper for firmware extraction, and then invokes the generated file for flash
reads:

- `extract_recovery_esptool()` call at original line 1728
- helper subprocess invocation at original line 1744

The Go port follows that same model, but makes it explicit instead of relying on
an out-of-band file to appear somewhere on disk.

## What it is used for

`gornodeconf` uses this helper for ESP32-family recovery operations:

1. `runFirmwareExtract()` materializes the helper into the config directory and
   uses it for `read_flash` extraction.
2. `runFirmwareFlash()` materializes the helper into the firmware directory and
   uses it for `write_flash`.

Relevant Go code:

- `extract_recovery.go`
- `extract_firmware_live.go`
- `flash_linux.go`
- `flasher_command.go`

## What this file appears to be

This file is **not** byte-identical to the raw upstream Espressif
`esptool.py` v3.1 script.

What it appears to be instead:

1. a packed/minified Python helper emitted by Reticulum's
   `extract_recovery_esptool()`,
2. derived from an `esptool.py` v3.1-era codebase,
3. with embedded compressed payload data inside the generated helper.

In particular, the generated helper contains an embedded ESP32 stub payload via
an assignment like:

```python
ESP32ROM.STUB_CODE = eval(zlib.decompress(base64.b64decode(...)))
```

So there are two layers of embedded data:

1. Reticulum embeds the helper inside `rnodeconf.py`.
2. The helper embeds the ESP stub it uploads during recovery operations.

## Why this file is checked in

Without checking this file in, the Go port would depend on a hidden external
artifact being created somewhere else before extract/flash operations work.

The Go code now embeds this file and materializes it on demand:

- config directory for extract workflows,
- firmware directory for flash workflows.

That makes the dependency explicit, reviewable, and testable.

## Current known hash

The checked-in helper currently has SHA-256:

```text
3ff47c999807a1ecf732016f65b6b863c3c80f56e39464694490c4a0c456b943
```

`extract_recovery.go` stores the same hash in `recoveryEsptoolSHA256`.

## Regenerating or updating the file

Do **not** hand-edit this file.

If the original Reticulum source changes, regenerate it from the original
Python source-of-truth:

1. extract the body of `extract_recovery_esptool()` from the original
   `rnodeconf.py`,
2. execute it in an isolated sandbox with a fake `RT_PATH`,
3. capture the emitted `recovery_esptool.py`,
4. replace this file with that emitted output,
5. update the SHA-256 constant in `extract_recovery.go`,
6. rerun the `cmd/gornodeconf` tests.

The important rule is: the checked-in helper must match what the original
Reticulum source emits, not what seems convenient locally.

## Important caveats

1. This helper still depends on host Python support, including `pyserial`.
2. `gornodeconf` resolves a Python interpreter that can actually import
   `serial` before launching the helper. It first honors
   `GORNODECONF_RECOVERY_PYTHON`, then checks the active virtualenv, standard
   `python3`/`python`, and finally a `pipx install pyserial` interpreter if one
   can be found.
3. If no usable interpreter is found, extract/flash will fail with an explicit
   error telling the caller to set `GORNODECONF_RECOVERY_PYTHON`, activate a
   virtualenv, or install `pyserial` for `python3`.
4. If a board fails during extract/flash, that can still be a helper/device
   compatibility problem even when helper materialization is working correctly.
