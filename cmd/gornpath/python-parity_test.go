// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// pythonPathRateJSON seeds RNS.Transport.path_table and announce_rate_table
// with a known entry, then calls the real Reticulum.get_path_table() and
// get_rate_table() (the exact code path `rnpath --table --json` / `--rates
// --json` uses) and json.dumps the result, captured live from the installed
// RNS. The seeded values match the Go test fixtures so both the field
// names+order (the structural parity claim) and the values can be diffed.
func pythonPathRateJSON(t *testing.T) (pathsJSON, ratesJSON string) {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
import os, json, tempfile, shutil
import RNS

cfg = tempfile.mkdtemp(prefix="rnpath-json-", dir="/tmp")
try:
    os.makedirs(os.path.join(cfg, "storage"), exist_ok=True)
    with open(os.path.join(cfg, "config"), "w") as f:
        f.write("[reticulum]\nenable_transport = True\nshare_instance = No\n\n[interfaces]\n")
    ret = RNS.Reticulum(cfg)
    dst = bytes.fromhex("aabb")
    class Iface:
        def __str__(self): return "test[eth0]"
    RNS.Transport.path_table[dst] = [123, bytes.fromhex("ccdd"), 2, 456, None, Iface()]
    RNS.Transport.announce_rate_table[dst] = {"last":123, "rate_violations":3, "blocked_until":456, "timestamps":[111,222]}
    pt = ret.get_path_table()
    for e in pt:
        for k in e:
            if isinstance(e[k], bytes): e[k] = RNS.hexrep(e[k], delimit=False)
    rt = ret.get_rate_table()
    for e in rt:
        for k in e:
            if isinstance(e[k], bytes): e[k] = RNS.hexrep(e[k], delimit=False)
    print("PATHS="+json.dumps(pt))
    print("RATES="+json.dumps(sorted(rt, key=lambda e: e["last"])))
finally:
    shutil.rmtree(cfg, ignore_errors=True)
`
	out := testutils.RunPython(t, script)
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "PATHS="):
			pathsJSON = strings.TrimPrefix(line, "PATHS=")
		case strings.HasPrefix(line, "RATES="):
			ratesJSON = strings.TrimPrefix(line, "RATES=")
		}
	}
	if pathsJSON == "" || ratesJSON == "" {
		t.Fatalf("python capture missing PATHS/RATES output:\n%s", out)
	}
	return pathsJSON, ratesJSON
}

// jsonFirstObjectKeys decodes a JSON array string and returns the ordered
// keys of its first object, preserving field order (encoding/json into a map
// would lose order). Used to structurally diff Go's JSON field order against
// the live Python capture.
func jsonFirstObjectKeys(t *testing.T, s string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("json decode array open: %v\nraw: %s", err, s)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		t.Fatalf("expected JSON array, got %v\nraw: %s", tok, s)
	}
	tok, err = dec.Token()
	if err != nil {
		t.Fatalf("json decode first token: %v\nraw: %s", err, s)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("expected JSON object in array, got %v\nraw: %s", tok, s)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("json decode key: %v", err)
		}
		keys = append(keys, k.(string))
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("json decode value: %v", err)
		}
	}
	return keys
}

// jsonFirstObjectValues decodes a JSON array string and returns the first
// object as an order-independent map[string]any for value comparison.
func jsonFirstObjectValues(t *testing.T, s string) map[string]any {
	t.Helper()
	var arr []map[string]any
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		t.Fatalf("json unmarshal array: %v\nraw: %s", err, s)
	}
	if len(arr) == 0 {
		t.Fatalf("json array empty\nraw: %s", s)
	}
	return arr[0]
}

// assertJSONParity diffs Go's JSON against the live Python JSON for field
// names+order (structural) and values.
func assertJSONParity(t *testing.T, got, wantPython string) {
	t.Helper()
	gotKeys := jsonFirstObjectKeys(t, got)
	wantKeys := jsonFirstObjectKeys(t, wantPython)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("JSON field names/order mismatch with live Python:\n got keys: %v\nwant keys: %v\n got JSON: %s\nwant JSON: %s",
			gotKeys, wantKeys, got, wantPython)
	}
	gotVals := jsonFirstObjectValues(t, got)
	wantVals := jsonFirstObjectValues(t, wantPython)
	if !reflect.DeepEqual(gotVals, wantVals) {
		t.Fatalf("JSON values mismatch with live Python:\n got: %v\nwant: %v", gotVals, wantVals)
	}
}
