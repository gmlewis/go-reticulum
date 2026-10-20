// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/json"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// queueOp is one operation in the parity battery: "put" appends its class
// value to that class, "get" removes the highest-priority item.
type queueOp struct {
	Op    string `json:"op"`
	Class int    `json:"class"`
}

// queueCase is a queue size tuple plus the operations driven through it.
type queueCase struct {
	Name  string    `json:"name"`
	Sizes [4]int    `json:"sizes"`
	Ops   []queueOp `json:"ops"`
}

// queueCaseResult is the observation both implementations report: the order of
// successful gets, the snapshot, and the five pressure ratios.
type queueCaseResult struct {
	Gets     []int `json:"gets"`
	Total    int   `json:"total"`
	Heights  []int `json:"heights"`
	Dropped  []int `json:"dropped"`
	Pressure []any `json:"pressure"`
}

// queueParityCases is the insert/get battery. Every size is positive, so the
// pressure ratios are total/sum and height/size, exactly Python's arithmetic.
func queueParityCases() []queueCase {
	fill := func(times, class int) []queueOp {
		ops := make([]queueOp, times)
		for i := range ops {
			ops[i] = queueOp{Op: "put", Class: class}
		}
		return ops
	}
	get := func(times int) []queueOp {
		ops := make([]queueOp, times)
		for i := range ops {
			ops[i] = queueOp{Op: "get"}
		}
		return ops
	}
	concat := func(parts ...[]queueOp) []queueOp {
		var out []queueOp
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	return []queueCase{
		{
			Name:  "one per class then drain",
			Sizes: [4]int{4, 4, 4, 4},
			Ops: concat(
				fill(1, TCData), fill(1, TCAnnounce), fill(1, TCPathRequest), fill(1, TCIngressLimited),
				get(5),
			),
		},
		{
			Name:  "priority order ignores insertion order",
			Sizes: [4]int{4, 4, 4, 4},
			Ops: concat(
				fill(1, TCIngressLimited), fill(1, TCPathRequest), fill(1, TCAnnounce), fill(1, TCData),
				get(4),
			),
		},
		{
			Name:  "overflow drops per class",
			Sizes: [4]int{2, 1, 3, 1},
			Ops: concat(
				fill(5, TCData), fill(2, TCAnnounce), fill(5, TCPathRequest), fill(2, TCIngressLimited),
				get(1), fill(1, TCData), get(20),
			),
		},
		{
			Name:  "interleaved puts and gets",
			Sizes: [4]int{3, 2, 2, 1},
			Ops: concat(
				fill(2, TCData), get(1), fill(2, TCAnnounce), get(2),
				fill(1, TCIngressLimited), fill(2, TCPathRequest), get(10),
			),
		},
	}
}

const queueParityScript = `
import json, sys
from queue import Full, Empty
from RNS.Transport import InboundQueues

results = []
for case in json.loads(sys.argv[1]):
    sizes = tuple(case["sizes"])
    q = InboundQueues(sizes)
    gets = []
    for op in case["ops"]:
        if op["op"] == "put":
            try: q.put(op["class"], op["class"])
            except Full: pass
        else:
            try: gets.append(q.get(block=False))
            except Empty: pass
    total, heights, dropped = q.snapshot()
    tql = sum(sizes)
    pressure = [
        total / tql if total else 0,
        heights[0] / sizes[0] if heights[0] else 0,
        heights[1] / sizes[1] if heights[1] else 0,
        heights[2] / sizes[2] if heights[2] else 0,
        heights[3] / sizes[3] if heights[3] else 0,
    ]
    results.append({
        "gets": gets,
        "total": total,
        "heights": list(heights),
        "dropped": list(dropped),
        "pressure": pressure,
    })
json.dump(results, sys.stdout)
`

// TestInboundQueuesPythonParity drives the installed 1.5.0 reference's
// InboundQueues over the same battery as the port and compares the pop order,
// the consistent snapshot (total, heights, drops) and the pressure arithmetic
// of get_interface_stats (Reticulum.py:1539-1554).
func TestInboundQueuesPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	cases := queueParityCases()
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	var want []queueCaseResult
	if err := json.Unmarshal([]byte(runPythonRNS15(t, queueParityScript, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("python returned %v results, want %v", len(want), len(cases))
	}

	for i, tc := range cases {
		q := NewInboundQueues[int](tc.Sizes)
		var gets []int
		for _, op := range tc.Ops {
			if op.Op == "put" {
				_ = q.Put(op.Class, op.Class) // a full queue drops, as Python's Full does
				continue
			}
			if item, err := q.TryGet(); err == nil {
				gets = append(gets, item)
			}
		}
		total, heights, dropped := q.Snapshot()
		got := queueCaseResult{
			Gets:    gets,
			Total:   total,
			Heights: heights[:],
			Dropped: dropped[:],
			Pressure: []any{
				queuePressure(total, tc.Sizes[0]+tc.Sizes[1]+tc.Sizes[2]+tc.Sizes[3]),
				queuePressure(heights[0], tc.Sizes[0]),
				queuePressure(heights[1], tc.Sizes[1]),
				queuePressure(heights[2], tc.Sizes[2]),
				queuePressure(heights[3], tc.Sizes[3]),
			},
		}
		w := want[i]
		if !equalIntSlices(got.Gets, w.Gets) {
			t.Errorf("%v: get order = %v, want %v (Python)", tc.Name, got.Gets, w.Gets)
		}
		if got.Total != w.Total || !equalIntSlices(got.Heights, w.Heights) || !equalIntSlices(got.Dropped, w.Dropped) {
			t.Errorf("%v: snapshot = (%v, %v, %v), want (%v, %v, %v) (Python)",
				tc.Name, got.Total, got.Heights, got.Dropped, w.Total, w.Heights, w.Dropped)
		}
		for j, gp := range got.Pressure {
			if !equalNumbers(gp, w.Pressure[j]) {
				t.Errorf("%v: pressure[%v] = %v, want %v (Python)", tc.Name, j, gp, w.Pressure[j])
			}
		}
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalNumbers compares the int-or-float values JSON decoding produces.
func equalNumbers(a, b any) bool {
	fa, oka := asFloat(a)
	fb, okb := asFloat(b)
	return oka && okb && fa == fb
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case nil:
		return 0, false
	}
	return 0, false
}
