// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// runRnodeconfHelp execs `python3 RNS/Utilities/rnodeconf.py --help` against
// the original-reticulum-repo (with PYTHONPATH pointed there) and returns the
// stdout, captured live. Gated on SkipIfNoPythonRNS.
func runRnodeconfHelp(t *testing.T) string {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "original-reticulum-repo"))
	if err != nil {
		t.Fatalf("resolve original repo root: %v", err)
	}
	cmd := exec.Command("python3", "RNS/Utilities/rnodeconf.py", "--help")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoRoot)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("rnodeconf --help failed: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.String()
}

// normalizeGornodeconfHelp collapses whitespace and rewrites the intentional
// port-only differences (program name "gornodeconf" vs "rnodeconf.py", and
// the "Go" prefix on the description line) so that the remaining text can be
// diffed against the live Python rnodeconf --help output.
func normalizeGornodeconfHelp(text string) string {
	text = strings.ReplaceAll(text, "gornodeconf", "rnodeconf")
	text = strings.ReplaceAll(text, "rnodeconf.py", "rnodeconf")
	text = strings.ReplaceAll(text, "Go RNode Configuration and firmware utility", "RNode Configuration and firmware utility")
	return strings.Join(strings.Fields(text), " ")
}

// pythonRnodeSerialSettings runs the real Python rnodeconf rnode_open_serial
// (RNS/Utilities/rnodeconf.py:1418-1432) with serial.Serial monkeypatched to a
// fake that records the constructor kwargs (so no real port is opened), and
// returns the captured serial settings as a serialSettings value (Port left
// empty for the caller to fill in). Gated on SkipIfNoPythonRNS.
func pythonRnodeSerialSettings(t *testing.T) serialSettings {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
import serial, json
import RNS.Utilities.rnodeconf as r
captured = {}
class FakeSerial:
    def __init__(self, **kw):
        captured.update(kw)
serial.Serial = FakeSerial
r.rnode_open_serial('/tmp/gornodeconf-parity-port')
out = {
  'baudrate': captured.get('baudrate'),
  'bytesize': captured.get('bytesize'),
  'parity': captured.get('parity'),
  'stopbits': captured.get('stopbits'),
  'xonxoff': captured.get('xonxoff'),
  'rtscts': captured.get('rtscts'),
  'timeout': captured.get('timeout'),
  'inter_byte_timeout': captured.get('inter_byte_timeout'),
  'write_timeout': captured.get('write_timeout'),
  'dsrdtr': captured.get('dsrdtr'),
}
print(json.dumps(out))
`
	out := testutils.RunPython(t, script)
	var s struct {
		Baudrate         int      `json:"baudrate"`
		Bytesize         int      `json:"bytesize"`
		Parity           string   `json:"parity"`
		Stopbits         int      `json:"stopbits"`
		Xonxoff          bool     `json:"xonxoff"`
		Rtscts           bool     `json:"rtscts"`
		Timeout          float64  `json:"timeout"`
		InterByteTimeout *float64 `json:"inter_byte_timeout"`
		WriteTimeout     *float64 `json:"write_timeout"`
		Dsrdtr           bool     `json:"dsrdtr"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); err != nil {
		t.Fatalf("parse rnode_open_serial output: %v\nraw: %s", err, out)
	}
	settings := serialSettings{
		BaudRate: s.Baudrate,
		ByteSize: s.Bytesize,
		Parity:   s.Parity,
		StopBits: s.Stopbits,
		XonXoff:  s.Xonxoff,
		RTSCTS:   s.Rtscts,
		Timeout:  time.Duration(s.Timeout) * time.Second,
		DSRDTR:   s.Dsrdtr,
	}
	if s.InterByteTimeout != nil {
		d := time.Duration(*s.InterByteTimeout) * time.Second
		settings.InterByteTimeout = &d
	}
	if s.WriteTimeout != nil {
		d := time.Duration(*s.WriteTimeout) * time.Second
		settings.WriteTimeout = &d
	}
	return settings
}

// pythonRomReadFrame returns the exact EEPROM ROM-read command frame that
// Python rnodeconf writes (rnodeconf.py:1010
// `bytes([KISS.FEND, KISS.CMD_ROM_READ, 0x00, KISS.FEND])`), captured live
// from the installed RNS KISS constants. Gated on SkipIfNoPythonRNS.
func pythonRomReadFrame(t *testing.T) []byte {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
from RNS.Utilities.rnodeconf import KISS
import sys
sys.stdout.write(bytes([KISS.FEND, KISS.CMD_ROM_READ, 0x00, KISS.FEND]).hex())
`
	out := testutils.RunPython(t, script)
	b, err := hex.DecodeString(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("decode python ROM-read frame %q: %v", out, err)
	}
	return b
}

// pythonKissEscape runs the real Python rnodeconf KISS.escape
// (RNS/Utilities/rnodeconf.py:136-139) over each supplied raw byte slice
// and returns the escaped output, captured live from the installed RNS.
// It is gated on SkipIfNoPythonRNS so it runs whenever Python+RNS are
// available and skips otherwise.
func pythonKissEscape(t *testing.T, raws [][]byte) [][]byte {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)

	hexIn := make([]string, len(raws))
	for i, r := range raws {
		hexIn[i] = hex.EncodeToString(r)
	}
	data, err := json.Marshal(hexIn)
	if err != nil {
		t.Fatalf("marshal kiss-escape inputs: %v", err)
	}
	dir, err := os.MkdirTemp("/tmp", "kiss-escape-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	jsonPath := filepath.Join(dir, "in.json")
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatalf("write in.json: %v", err)
	}

	script := `
import sys, json
from RNS.Utilities.rnodeconf import KISS
with open(sys.argv[1]) as f:
    hexs = json.load(f)
out = []
for h in hexs:
    esc = KISS.escape(bytes.fromhex(h))
    out.append(esc.hex())
print(json.dumps(out))
`
	out := testutils.RunPython(t, script, jsonPath)
	var hexOut []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &hexOut); err != nil {
		t.Fatalf("parse kiss-escape output: %v\nraw: %s", err, out)
	}
	if len(hexOut) != len(raws) {
		t.Fatalf("kiss-escape count mismatch: got %d want %d", len(hexOut), len(raws))
	}
	results := make([][]byte, len(raws))
	for i, h := range hexOut {
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatalf("decode kiss-escape hex %q: %v", h, err)
		}
		results[i] = b
	}
	return results
}
