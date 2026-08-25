// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build integration

package interfaces

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// rnodeCaptureScript drives the REAL Python RNS.RNodeInterface methods against a
// recording fake serial and prints "name<TAB>hex" for every outbound KISS
// command, so the Go byte builders can be diffed against the source of truth.
// It bypasses __init__ (which opens a physical port) with __new__ and sets only
// the attributes the command methods read.
const rnodeCaptureScript = `
from RNS.Interfaces.RNodeInterface import RNodeInterface, KISS

rec = []
class FakeSerial:
    is_open = True
    in_waiting = False
    def write(self, b):
        rec.append(bytes(b)); return len(b)
    def read(self, n): return b""
    def close(self): pass

r = RNodeInterface.__new__(RNodeInterface)
r.serial = FakeSerial()
r.frequency = 915000000
r.bandwidth = 125000
r.txpower = 17
r.sf = 8
r.cr = 5
r.state = KISS.RADIO_STATE_OFF
r.st_alock = 15.5
r.lt_alock = 30.0
r.display = True

def cap(name, fn):
    fn()
    print(name + "\t" + rec[-1].hex())

cap("setFrequency",        r.setFrequency)
cap("setBandwidth",        r.setBandwidth)
cap("setTXPower",           r.setTXPower)
cap("setSpreadingFactor",  r.setSpreadingFactor)
cap("setCodingRate",       r.setCodingRate)
cap("setSTALock",          r.setSTALock)
cap("setLTALock",          r.setLTALock)
cap("setRadioStateOn",     lambda: r.setRadioState(KISS.RADIO_STATE_ON))
cap("detect",              r.detect)
cap("leave",               r.leave)
cap("hardReset",           r.hard_reset)
cap("enableFB",            r.enable_external_framebuffer)
cap("disableFB",           r.disable_external_framebuffer)
cap("readFB",              r.read_framebuffer)
cap("readDisplay",         r.read_display)
cap("writeFB",             lambda: r.write_framebuffer(3, bytes([0xC0,0xDB,0x01,0x02])))
`

// TestRNodeOutboundCommandByteParity asserts every Go RNode KISS command byte
// builder produces bytes identical to the real Python RNodeInterface methods.
// This is the wire-format foundation for full RNode behavioral parity: if the
// Go interface ever sends different bytes than Python, the radio will be
// misconfigured. Python is the source of truth; values are captured live, not
// committed as literals.
func TestRNodeOutboundCommandByteParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	out := testutils.RunPython(t, rnodeCaptureScript)
	pyBytes := make(map[string][]byte)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("unexpected Python capture line: %q", line)
		}
		b, err := hex.DecodeString(parts[1])
		if err != nil {
			t.Fatalf("bad hex in capture %q: %v", parts[0], err)
		}
		pyBytes[parts[0]] = b
	}

	sta := 15.5
	lt := 30.0
	goBytes := map[string][]byte{
		"setFrequency":       RNodeSetFrequency(915000000),
		"setBandwidth":       RNodeSetBandwidth(125000),
		"setTXPower":         RNodeSetTXPower(17),
		"setSpreadingFactor": RNodeSetSpreadingFactor(8),
		"setCodingRate":      RNodeSetCodingRate(5),
		"setSTALock":         RNodeSetSTALock(&sta),
		"setLTALock":         RNodeSetLTALock(&lt),
		"setRadioStateOn":    RNodeSetRadioState(RadioStateOn),
		"detect":             RNodeDetect(),
		"leave":              RNodeLeave(),
		"hardReset":          RNodeHardReset(),
		"enableFB":           RNodeEnableExternalFramebuffer(),
		"disableFB":          RNodeDisableExternalFramebuffer(),
		"readFB":             RNodeReadFramebuffer(),
		"readDisplay":        RNodeReadDisplay(),
		"writeFB":            RNodeWriteFramebuffer(3, []byte{0xC0, 0xDB, 0x01, 0x02}),
	}

	for name, gb := range goBytes {
		pb, ok := pyBytes[name]
		if !ok {
			t.Fatalf("Python capture missing command %q", name)
		}
		if fmt.Sprintf("%x", gb) != fmt.Sprintf("%x", pb) {
			t.Errorf("command %q byte mismatch:\n  go = %x\n  py = %x", name, gb, pb)
		}
	}
}
