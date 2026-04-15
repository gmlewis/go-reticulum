// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

const ifacParityPy = `import RNS
import sys

def build_ifac_context(netname, netkey):
    ifac_origin = b""
    if netname != "":
        ifac_origin += RNS.Identity.full_hash(netname.encode("utf-8"))
    if netkey != "":
        ifac_origin += RNS.Identity.full_hash(netkey.encode("utf-8"))

    ifac_origin_hash = RNS.Identity.full_hash(ifac_origin)
    ifac_key = RNS.Cryptography.hkdf(
        length=64,
        derive_from=ifac_origin_hash,
        salt=RNS.Reticulum.IFAC_SALT,
        context=None,
    )
    ifac_identity = RNS.Identity.from_bytes(ifac_key)
    return ifac_key, ifac_identity

def encode(raw_hex, ifac_size, netname, netkey):
    raw = bytes.fromhex(raw_hex)
    ifac_key, ifac_identity = build_ifac_context(netname, netkey)

    ifac = ifac_identity.sign(raw)[-ifac_size:]
    mask = RNS.Cryptography.hkdf(
        length=len(raw)+ifac_size,
        derive_from=ifac,
        salt=ifac_key,
        context=None,
    )

    new_header = bytes([raw[0] | 0x80, raw[1]])
    new_raw = new_header + ifac + raw[2:]

    i = 0
    masked_raw = b""
    for byte in new_raw:
        if i == 0:
            masked_raw += bytes([byte ^ mask[i] | 0x80])
        elif i == 1 or i > ifac_size+1:
            masked_raw += bytes([byte ^ mask[i]])
        else:
            masked_raw += bytes([byte])
        i += 1

    print(masked_raw.hex())

def decode(masked_hex, ifac_size, netname, netkey):
    raw = bytes.fromhex(masked_hex)
    ifac_key, ifac_identity = build_ifac_context(netname, netkey)

    if raw[0] & 0x80 != 0x80:
        print("ERR:missing-ifac-flag")
        sys.exit(1)

    if len(raw) <= 2+ifac_size:
        print("ERR:short")
        sys.exit(1)

    ifac = raw[2:2+ifac_size]
    mask = RNS.Cryptography.hkdf(
        length=len(raw),
        derive_from=ifac,
        salt=ifac_key,
        context=None,
    )

    i = 0
    unmasked_raw = b""
    for byte in raw:
        if i <= 1 or i > ifac_size+1:
            unmasked_raw += bytes([byte ^ mask[i]])
        else:
            unmasked_raw += bytes([byte])
        i += 1

    new_header = bytes([unmasked_raw[0] & 0x7f, unmasked_raw[1]])
    new_raw = new_header + unmasked_raw[2+ifac_size:]

    expected_ifac = ifac_identity.sign(new_raw)[-ifac_size:]
    if ifac != expected_ifac:
        print("ERR:ifac-mismatch")
        sys.exit(1)

    print(new_raw.hex())

if __name__ == "__main__":
    mode = sys.argv[1]
    payload_hex = sys.argv[2]
    ifac_size = int(sys.argv[3])
    netname = sys.argv[4]
    netkey = sys.argv[5]

    if mode == "encode":
        encode(payload_hex, ifac_size, netname, netkey)
    elif mode == "decode":
        decode(payload_hex, ifac_size, netname, netkey)
    else:
        print("ERR:bad-mode")
        sys.exit(1)
`

func runPythonIFAC(t *testing.T, scriptPath, mode string, payload []byte, size int, netname, netkey string) []byte {
	t.Helper()
	cmd := exec.Command("python3", scriptPath, mode, fmt.Sprintf("%x", payload), fmt.Sprintf("%v", size), netname, netkey)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python IFAC %v failed: %v\nOutput: %v", mode, err, string(out))
	}
	hexOut := strings.TrimSpace(string(out))
	result, err := HexToBytes(hexOut)
	if err != nil {
		t.Fatalf("invalid python IFAC %v hex output %q: %v", mode, hexOut, err)
	}
	return result
}

func TestIFACParityWithPython(t *testing.T) {
	testutils.SkipShortIntegration(t)

	tmpDir, cleanup := testutils.TempDir(t, "go-reticulum-ifac-parity-*")
	defer cleanup()

	scriptPath := filepath.Join(tmpDir, "ifac_parity.py")
	if err := os.WriteFile(scriptPath, []byte(ifacParityPy), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := []byte{0x10, 0x01, 0xAB, 0xCD, 0xEF, 0x01, 0x02}
	netname := "mesh-alpha"
	netkey := "shared-secret"
	ifacSize := 16

	bi := interfaces.NewBaseInterface("ifac-parity", interfaces.ModeFull, 1000)
	bi.SetIFACConfig(interfaces.IFACConfig{Enabled: true, NetName: netname, NetKey: netkey, Size: ifacSize})

	goEncoded, err := bi.ApplyIFACOutbound(raw)
	if err != nil {
		t.Fatalf("go IFAC encode failed: %v", err)
	}
	pyEncoded := runPythonIFAC(t, scriptPath, "encode", raw, ifacSize, netname, netkey)

	if string(goEncoded) != string(pyEncoded) {
		t.Fatalf("IFAC encode mismatch between Go and Python")
	}

	goDecoded, ok := bi.ApplyIFACInbound(pyEncoded)
	if !ok {
		t.Fatalf("go IFAC decode rejected python-encoded frame")
	}
	if string(goDecoded) != string(raw) {
		t.Fatalf("go IFAC decode payload mismatch")
	}

	pyDecoded := runPythonIFAC(t, scriptPath, "decode", goEncoded, ifacSize, netname, netkey)
	if string(pyDecoded) != string(raw) {
		t.Fatalf("python IFAC decode payload mismatch")
	}
}
