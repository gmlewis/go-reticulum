// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPNAnnounceDataPythonParity derives pn_announce_data_is_valid,
// pn_name_from_app_data and pn_stamp_cost_from_app_data from the installed
// reference (LXMF/LXMF.py:202-252) over a battery of propagation announce
// payloads, including the accepted integer node-state (Python 1 == True).
func TestPNAnnounceDataPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	base := func() []any {
		return []any{
			int64(0),
			int64(1700000000),
			true,
			int64(10240),
			int64(1000),
			[]any{int64(8), int64(0), int64(0)},
			map[any]any{PNMetaName: []byte("Test Node"), int64(0): int64(1)},
		}
	}
	with := func(edit func([]any)) []byte {
		data := base()
		edit(data)
		packed, err := msgpack.Pack(data)
		if err != nil {
			t.Fatalf("pack: %v", err)
		}
		return packed
	}

	cases := map[string][]byte{
		"valid named":            with(func([]any) {}),
		"valid unnamed":          with(func(d []any) { d[6] = map[any]any{} }),
		"node state false":       with(func(d []any) { d[2] = false }),
		"node state integer 1":   with(func(d []any) { d[2] = int64(1) }),
		"node state integer 2":   with(func(d []any) { d[2] = int64(2) }),
		"metadata not a dict":    with(func(d []any) { d[6] = []any{} }),
		"stamp list too short":   with(func(d []any) { d[5] = []any{int64(8)} }),
		"timebase not an int":    with(func(d []any) { d[1] = "time" }),
		"numeric timebase text":  with(func(d []any) { d[1] = "1700000000" }),
		"transfer limit a float": with(func(d []any) { d[3] = 1.5 }),
		"name not utf8": with(func(d []any) {
			d[6] = map[any]any{PNMetaName: []byte{0xff, 0xfe}}
		}),
		"empty": {},
	}

	// The too-short case cannot be built through `with` because slicing inside
	// the edit does not shorten the outer list; build it directly.
	short, err := msgpack.Pack(base()[:6])
	if err != nil {
		t.Fatal(err)
	}
	cases["too short"] = short

	names := make([]string, 0, len(cases))
	payloads := make([]string, 0, len(cases))
	for name, payload := range cases {
		names = append(names, name)
		payloads = append(payloads, hex.EncodeToString(payload))
	}
	in, err := json.Marshal(payloads)
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import sys, json
import LXMF
out = []
for h in json.loads(sys.argv[1]):
    data = bytes.fromhex(h)
    out.append({
        "valid": LXMF.pn_announce_data_is_valid(data),
        "name": LXMF.pn_name_from_app_data(data),
        "cost": LXMF.pn_stamp_cost_from_app_data(data),
    })
json.dump(out, sys.stdout)
`
	var want []struct {
		Valid bool    `json:"valid"`
		Name  *string `json:"name"`
		Cost  any     `json:"cost"`
	}
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}

	for i, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			payload := cases[name]
			w := want[i]
			if got := PNAnnounceDataIsValid(payload); got != w.Valid {
				t.Errorf("PNAnnounceDataIsValid = %v, want %v (Python)", got, w.Valid)
			}
			gotName, gotOK := PNNameFromAppData(payload)
			wantName, wantOK := "", w.Name != nil
			if w.Name != nil {
				wantName = *w.Name
			}
			if gotName != wantName || gotOK != wantOK {
				t.Errorf("PNNameFromAppData = (%q, %v), want (%q, %v) (Python)", gotName, gotOK, wantName, wantOK)
			}
			gotCost, gotCostOK := PNStampCostFromAppData(payload)
			wantCostOK := w.Cost != nil
			if gotCostOK != wantCostOK {
				t.Errorf("PNStampCostFromAppData ok = %v, want %v (Python)", gotCostOK, wantCostOK)
			}
			if wantCostOK {
				// JSON numbers decode as float64; compare their integral value.
				wantInt := int64(w.Cost.(float64))
				if gotInt, ok := pnIntValue(gotCost); !ok || gotInt != wantInt {
					t.Errorf("PNStampCostFromAppData = %v, want %v (Python)", gotCost, wantInt)
				}
			}
		})
	}
}

// pnIntValue converts a decoded msgpack integer to int64.
func pnIntValue(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case uint64:
		return int64(x), true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	}
	return 0, false
}
