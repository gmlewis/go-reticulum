// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestIntegrationCompileStatsShapePythonToGo is the Phase I.2 parity gate:
// it captures a real Python LXMRouter.compile_stats response and a Go
// Router.compileStatsLocked response, both unpacked by the same Go msgpack
// unpacker, and asserts the Go response is shape-compatible with Python's —
// every key Python emits is present in the Go response with the same type,
// recursively through the messagestore and clients sub-maps, and the peers
// maps have the same size. This is what a NomadNet/lxmf-nomadnet client sees
// over the /pn/get/stats RPC: no degraded fields.
func TestIntegrationCompileStatsShapePythonToGo(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Capture the Python compile_stats response.
	pyScript := filepath.Join(tmpDir, "run_control_stats.py")
	if err := os.WriteFile(pyScript, []byte(lxmfRunControlStatsPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}
	pyResponse := filepath.Join(tmpDir, "control_stats_response.msgpack")
	pyStore := filepath.Join(tmpDir, "py_store")
	cmd := exec.Command("python3", pyScript, pyResponse, pyStore)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python control stats flow failed: %v output=%v", err, string(out))
	}
	pyData, err := os.ReadFile(pyResponse)
	if err != nil {
		t.Fatalf("read python response: %v", err)
	}
	pyUnpacked, err := msgpack.Unpack(pyData)
	if err != nil {
		t.Fatalf("unpack python response: %v", err)
	}
	pyResponses, ok := pyUnpacked.([]any)
	if !ok || len(pyResponses) != 3 {
		t.Fatalf("python response shape=%T len=%v", pyUnpacked, len(pyResponses))
	}
	pyStats, ok := pyResponses[2].(map[any]any)
	if !ok {
		t.Fatalf("python allowed stats type=%T want map", pyResponses[2])
	}

	// Build a Go router in the same shape: propagation node enabled, no
	// peers, default config — mirroring the Python script's state.
	ts := rns.NewTransportSystem(nil)
	goStore := filepath.Join(tmpDir, "go_store")
	router := mustTestNewRouter(t, ts, nil, goStore)
	router.propagationEnabled = true
	router.propagationNodeStart = router.now()
	caller := mustTestNewIdentity(t, true)
	router.controlAllowed[string(caller.Hash)] = struct{}{}

	goRespAny := router.statsGetRequest("", nil, nil, nil, caller, router.now())
	goResp, ok := goRespAny.(map[string]any)
	if !ok {
		t.Fatalf("go compileStats type=%T want map", goRespAny)
	}
	goData, err := msgpack.Pack(goResp)
	if err != nil {
		t.Fatalf("pack go response: %v", err)
	}
	goUnpacked, err := msgpack.Unpack(goData)
	if err != nil {
		t.Fatalf("unpack go response: %v", err)
	}
	goStats, ok := goUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("go unpacked stats type=%T want map", goUnpacked)
	}

	// Assert every Python key is present in Go with the same (Go-unpacked)
	// type, recursively. Values are timing/state-dependent so only shape is
	// compared.
	if mismatches := shapeSubset("", pyStats, goStats); len(mismatches) > 0 {
		t.Fatalf("Go compile_stats shape is not a superset of Python's:\n%v", mismatches)
	}

	// Both nodes have zero peers; the peers maps must both be empty.
	pyPeers, _ := pyStats["peers"].(map[any]any)
	goPeers, _ := goStats["peers"].(map[any]any)
	if len(pyPeers) != len(goPeers) {
		t.Fatalf("peers count: python=%d go=%d", len(pyPeers), len(goPeers))
	}
	t.Logf("Go compile_stats shape matches Python across %d top-level keys", len(pyStats))
}

// shapeSubset walks want and reports every key/type that is missing or
// type-mismatched in got. It recurses into maps. A non-map value only needs
// its type to match (values may differ across runs). Returns a multiline
// description of the mismatches (empty if none).
func shapeSubset(path string, want, got map[any]any) string {
	keys := make([]any, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })

	var mismatches string
	for _, k := range keys {
		wv := want[k]
		keyPath := path + "." + fmt.Sprint(k)
		gv, present := got[k]
		if !present {
			mismatches += fmt.Sprintf("  %s: missing in Go\n", keyPath)
			continue
		}
		if wMap, ok := wv.(map[any]any); ok {
			if gMap, ok := gv.(map[any]any); ok {
				mismatches += shapeSubset(keyPath, wMap, gMap)
				continue
			}
			mismatches += fmt.Sprintf("  %s: Go type %T is not a map (Python is map)\n", keyPath, gv)
			continue
		}
		if reflect.TypeOf(wv) != reflect.TypeOf(gv) {
			mismatches += fmt.Sprintf("  %s: type mismatch python=%T go=%T\n", keyPath, wv, gv)
		}
	}
	return mismatches
}
