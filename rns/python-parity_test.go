// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// pythonResourceConstants captures the Resource watchdog timing/state
// constants live from the installed RNS (RNS/Resource.py:99-137) as a
// name->value map (all values as float64). Gated on SkipIfNoPythonRNS.
func pythonResourceConstants(t *testing.T) map[string]float64 {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
import json
from RNS import Resource as R
out = {
 'PROCESSING_GRACE': R.PROCESSING_GRACE,
 'RETRY_GRACE_TIME': R.RETRY_GRACE_TIME,
 'PER_RETRY_DELAY': R.PER_RETRY_DELAY,
 'WATCHDOG_MAX_SLEEP': R.WATCHDOG_MAX_SLEEP,
 'PROOF_TIMEOUT_FACTOR': R.PROOF_TIMEOUT_FACTOR,
 'PART_TIMEOUT_FACTOR': R.PART_TIMEOUT_FACTOR,
 'PART_TIMEOUT_FACTOR_AFTER_RTT': R.PART_TIMEOUT_FACTOR_AFTER_RTT,
 'SENDER_GRACE_TIME': R.SENDER_GRACE_TIME,
 'HMU_WAIT_FACTOR': R.HMU_WAIT_FACTOR,
 'MAX_RETRIES': R.MAX_RETRIES,
 'MAX_ADV_RETRIES': R.MAX_ADV_RETRIES,
 'WINDOW_FLEXIBILITY': R.WINDOW_FLEXIBILITY,
}
print(json.dumps(out))
`
	out := testutils.RunPython(t, script)
	var m map[string]float64
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("parse resource constants output: %v\nraw: %s", err, out)
	}
	return m
}

// pythonResourceStatusConstants captures the Resource status constants live
// from the installed RNS (RNS/Resource.py: REJECTED, NONE, CORRUPT, COMPLETE,
// ASSEMBLING). Gated on SkipIfNoPythonRNS.
func pythonResourceStatusConstants(t *testing.T) (rejected, none, corrupt, complete, assembling int) {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
import json
from RNS import Resource as R
out = {
 'rejected': R.REJECTED,
 'none': R.NONE,
 'corrupt': R.CORRUPT,
 'complete': R.COMPLETE,
 'assembling': R.ASSEMBLING,
}
print(json.dumps(out))
`
	out := testutils.RunPython(t, script)
	var v struct {
		Rejected   int `json:"rejected"`
		None       int `json:"none"`
		Corrupt    int `json:"corrupt"`
		Complete   int `json:"complete"`
		Assembling int `json:"assembling"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		t.Fatalf("parse resource status constants output: %v\nraw: %s", err, out)
	}
	return v.Rejected, v.None, v.Corrupt, v.Complete, v.Assembling
}

// pythonLinkConstants captures the link capability constants live from the
// installed RNS: RNS.Reticulum.MTU, RNS.Link.MDU (Link.py:73 formula),
// RNS.Link.MODE_AES256_CBC (Link.py:126) and RNS.Link.ACTIVE. Gated on
// SkipIfNoPythonRNS.
func pythonLinkConstants(t *testing.T) (mtu, mdu, modeAES256CBC, active int) {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
import json
import RNS
from RNS import Link
out = {
 'mtu': RNS.Reticulum.MTU,
 'mdu': Link.MDU,
 'mode_aes256_cbc': Link.MODE_AES256_CBC,
 'active': Link.ACTIVE,
}
print(json.dumps(out))
`
	out := testutils.RunPython(t, script)
	var v struct {
		MTU           int `json:"mtu"`
		MDU           int `json:"mdu"`
		ModeAES256CBC int `json:"mode_aes256_cbc"`
		Active        int `json:"active"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		t.Fatalf("parse link constants output: %v\nraw: %s", err, out)
	}
	return v.MTU, v.MDU, v.ModeAES256CBC, v.Active
}

// pyRateCase is the JSON-encoded description of one link rate-accessor case:
// the raw establishment_rate / expected_rate field values (nil = Python None)
// to inject onto a __new__-constructed Link before calling the accessors.
type pyRateCase struct {
	EstablishmentRate *float64 `json:"establishment_rate"`
	ExpectedRate      *float64 `json:"expected_rate"`
}

// pyRateResult holds the live-captured accessor outputs for one case (nil =
// Python None).
type pyRateResult struct {
	EstablishmentRate *float64 `json:"establishment_rate"`
	ExpectedRate      *float64 `json:"expected_rate"`
}

// pythonLinkRates runs the real Python Link.get_establishment_rate /
// get_expected_rate (Link.py:573-612) over each supplied case, constructing a
// bare Link via __new__ with the given establishment_rate/expected_rate field
// values and status forced to ACTIVE (so get_expected_rate returns the value
// rather than None), and returns the accessor outputs captured live. Gated on
// SkipIfNoPythonRNS.
func pythonLinkRates(t *testing.T, cases []pyRateCase) []pyRateResult {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatalf("marshal link-rate cases: %v", err)
	}
	script := `
import sys, json
import RNS
from RNS import Link
cases = json.loads(sys.argv[1])
results = []
for c in cases:
    link = Link.__new__(Link)
    link.establishment_rate = c['establishment_rate']
    link.expected_rate = c['expected_rate']
    link.status = Link.ACTIVE
    results.append({
      'establishment_rate': link.get_establishment_rate(),
      'expected_rate': link.get_expected_rate(),
    })
print(json.dumps(results))
`
	out := testutils.RunPython(t, script, string(data))
	var results []pyRateResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &results); err != nil {
		t.Fatalf("parse link-rate output: %v\nraw: %s", err, out)
	}
	if len(results) != len(cases) {
		t.Fatalf("link-rate count mismatch: got %d want %d", len(results), len(cases))
	}
	return results
}

// pyEifrCase is the JSON-encoded description of one update_eifr case,
// consumed by the Python capture script.
type pyEifrCase struct {
	Rtt            *float64 `json:"rtt"`
	LinkRtt        float64  `json:"link_rtt"`
	Establishment  float64  `json:"establishment"`
	ReqDataRttRate float64  `json:"req_data_rtt_rate"`
	PreviousEifr   *float64 `json:"previous_eifr"`
}

// pythonUpdateEifr runs the real Python Resource.update_eifr
// (RNS/Resource.py:552-562) over each supplied case, constructing a bare
// Resource via __new__ (so __init__ side effects are avoided) with a fake
// link carrying rtt/establishment_cost/expected_rate, and returns the
// computed eifr values, captured live. Gated on SkipIfNoPythonRNS.
func pythonUpdateEifr(t *testing.T, cases []pyEifrCase) []float64 {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatalf("marshal update_eifr cases: %v", err)
	}
	script := `
import sys, json
import RNS
class FakeLink: pass
cases = json.loads(sys.argv[1])
results = []
for c in cases:
    r = RNS.Resource.__new__(RNS.Resource)
    r.rtt = c['rtt']
    r.req_data_rtt_rate = c['req_data_rtt_rate']
    r.previous_eifr = c['previous_eifr']
    link = FakeLink()
    link.rtt = c['link_rtt']
    link.establishment_cost = c['establishment']
    link.expected_rate = None
    r.link = link
    r.update_eifr()
    results.append(r.eifr)
print(json.dumps(results))
`
	out := testutils.RunPython(t, script, string(data))
	var results []float64
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &results); err != nil {
		t.Fatalf("parse update_eifr output: %v\nraw: %s", err, out)
	}
	if len(results) != len(cases) {
		t.Fatalf("update_eifr count mismatch: got %d want %d", len(results), len(cases))
	}
	return results
}
