// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Structural guard for the control-flow class of divergence: wait-and-retry
// loops whose *ordering* of condition tests is part of the protocol.
//
// The path-wait bug was purely an ordering divergence — the deadline was tested
// before has_path and never re-tested after the loop, so a path that arrived as
// the wait elapsed was discarded (Python tests has_path first in the loop
// condition and again after it, LXMRouter.py:1463,1466). No timeout value was
// wrong, so no timeout test could have caught it.
//
// This guard scans every non-test source file for functions that sleep (the
// shape of every wait/retry loop in the port) and requires each one to be
// registered below, either with the ordered condition sequence it must keep or
// with a reason it has no Python counterpart. A newly added wait loop therefore
// fails this test until someone records why its ordering is safe, which is the
// only way a future reordering cannot slip through unnoticed.

// waitLoopEntry records one protocol wait loop's Python ordering contract.
type waitLoopEntry struct {
	// python cites the Python source whose test order this loop mirrors.
	python string
	// ordered lists substrings that must appear in this relative order inside
	// the function body, expressing Python's test order. Each is searched for
	// after the previous match, so a substring may repeat to express "tested
	// again after the loop".
	ordered []string
}

// orderedWaitLoops are the wait loops whose *test order* is part of the
// protocol, each with the Python source it mirrors.
var orderedWaitLoops = map[string]waitLoopEntry{
	"Router.requestMessagesPathJob": {
		python: "LXMRouter.py:1461-1470 (__request_messages_path_job)",
		ordered: []string{
			// Inside the loop the path is tested before the deadline...
			"r.hasPath(from)",
			"deadline.IsZero()",
			// ...and again after it, so a path that arrives as the last wait
			// elapses is used rather than discarded.
			"r.hasPath(from)",
		},
	},
	"TransportSystem.AwaitPath": {
		python: "Transport.py:2842-2856 (await_path: test the path, then the deadline, then re-test on exit)",
		ordered: []string{
			`ts.pathTable[string(destinationHash)]`,
			"remaining <= 0",
		},
	},
	"TransportSystem.inbound": {
		python: "Transport.py:1430-1437 (the READY_WAIT gate: become ready on the last poll rather than dropped)",
		ordered: []string{
			"running && !ready",
			"ts.ready",
			"ready || !running",
			"readyWaitTimeout",
		},
	},
	"RequestReceipt.responseTimeoutJob": {
		python: "Link.py:1383-1390 (__response_timeout_job: status first, deadline second, then sleep)",
		ordered: []string{
			"status != RequestDelivered",
			"time.Now().After(deadline)",
		},
	},
	"Reticulum.attachLocalInterface": {
		python: "Reticulum.py:395-435 (connect retries before reporting that no shared instance is running)",
		ordered: []string{
			"attachAttempts",
			"client.Status()",
			"attachRetryWait",
		},
	},
}

// goOnlyWaitLoops are the port's own I/O pumps, reconnect loops, scheduler
// ticks and bounded teardown waits. Each blocks on its own socket, timer or
// goroutine and makes no protocol ordering decision, so there is no Python test
// order to mirror: Python reaches the same behaviour through select()/
// socketserver/threading rather than a poll loop, or the loop is a Go-only
// implementation detail. They are listed so that a NEW wait loop is a deliberate
// addition rather than something the guard silently tolerates.
var goOnlyWaitLoops = []string{
	// Interface I/O pumps and their reconnect/accept loops: these block on the
	// transport itself; Python's equivalents are socket read loops.
	"AutoInterface.dataLoop",
	"AutoInterface.discoveryLoop",
	"AutoInterface.finalInitBarrier",
	"LocalClientInterface.reconnectLoop",
	"LocalServerInterface.acceptLoop",
	"RNodeInterface.readLoopOnce",
	"RNodeInterface.reconnectPort",
	"RNodeInterface.configureDevice",
	"TCPClientInterface.reconnectLoop",
	"TCPServerInterface.acceptLoop",
	"UDPInterface.listenLoop",
	"ax25KISSInterface.readLoop",
	"ax25KISSInterface.readLoopOnce",
	"ax25KISSInterface.configureDevice",
	"kissInterface.readLoop",
	"kissInterface.readLoopOnce",
	"pipeSubprocessInterface.readFrames",
	"serialInterface.readLoop",
	"serialInterface.readLoopOnce",
	// Scheduler and maintenance ticks, and cache/table sweeps that pace
	// themselves so a large pass stays low priority.
	"InterfaceAnnouncer.job",
	"TransportSystem.cleanAnnounceCacheFiles",
	"TransportSystem.cleanCacheDir",
	"TransportSystem.savePacketHashlist",
	"TransportSystem.RegisterDestination",
	"TransportSystem.cleanAnnounceCache",
	// Bounded teardown wait: the port joins its own workers; Python joins a job
	// thread. No wire-visible ordering.
	"TransportSystem.Stop",
	// Announce and propagation pacing, where the cadence (not the test order)
	// is the contract and is covered by the announce tests.
	"Router.AnnouncePropagationNode",
	"Router.handleDeliveryAnnounce",
	"Router.handlePropagationAnnounceWithContext",
	"Peer.Sync",
	"Reticulum.callRPC",
	// Stamp-cost lookup retry: bounded, and its outcome (the cost value) is
	// asserted by the stamp-cost tests.
	"Router.getOutboundPropagationStampCost",
}

// waitLoopDirs are the module's source directories, relative to this package.
var waitLoopDirs = []string{".", "./interfaces", "../lxmf"}

// TestPythonWaitLoopOrderingIsDeliberate requires every sleeping function in
// the module to be registered, and every registered ordering contract to hold.
func TestPythonWaitLoopOrderingIsDeliberate(t *testing.T) {
	t.Parallel()

	found := map[string][]byte{}
	for _, dir := range waitLoopDirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %v: %v", dir, err)
		}
		for _, path := range matches {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			bodies, funcs := sleepingFunctions(t, path)
			for _, fn := range funcs {
				found[fn] = bodies[fn]
			}
		}
	}

	registered := map[string]bool{}
	for name := range orderedWaitLoops {
		registered[name] = true
	}
	for _, name := range goOnlyWaitLoops {
		registered[name] = true
	}

	var unregistered []string
	for fn := range found {
		if !registered[fn] {
			unregistered = append(unregistered, fn)
		}
	}
	sort.Strings(unregistered)
	if len(unregistered) > 0 {
		t.Errorf("these functions sleep but are not registered:\n  %v\n"+
			"Add each one to orderedWaitLoops with the Python test order it must keep, or to goOnlyWaitLoops "+
			"if it makes no protocol ordering decision. A wait loop's test order is part of the protocol "+
			"(the path-wait bug was pure ordering: LXMRouter.py:1463,1466).",
			strings.Join(unregistered, "\n  "))
	}

	names := make([]string, 0, len(orderedWaitLoops))
	for name := range orderedWaitLoops {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := orderedWaitLoops[name]
		body, ok := found[name]
		if !ok {
			t.Errorf("orderedWaitLoops names %v, which no longer sleeps (or was renamed); remove or update the entry", name)
			continue
		}
		if entry.python == "" {
			t.Errorf("%v: an ordered entry must cite the Python source it mirrors", name)
		}
		if len(entry.ordered) == 0 {
			t.Errorf("%v: an ordered entry must list the conditions whose order matters", name)
			continue
		}
		offset := 0
		for i, want := range entry.ordered {
			idx := strings.Index(string(body[offset:]), want)
			if idx < 0 {
				t.Errorf("%v: condition %v (%q, mirroring %v) is missing or out of order.\n"+
					"Python's test order is load-bearing; a reordering here is the path-wait bug all over again.",
					name, i+1, want, entry.python)
				break
			}
			offset += idx + len(want)
		}
	}

	for _, name := range goOnlyWaitLoops {
		if _, ok := found[name]; !ok {
			t.Errorf("goOnlyWaitLoops names %v, which no longer sleeps (or was renamed); remove or update the entry", name)
		}
	}
}

// sleepingFunctions parses one source file and returns, for every function
// whose body sleeps, the function's source text keyed by "Receiver.Name".
func sleepingFunctions(t *testing.T, path string) (map[string][]byte, []string) {
	t.Helper()

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %v: %v", path, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parse %v: %v", path, err)
	}

	out := map[string][]byte{}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !bodySleeps(fn.Body) {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			if ident, ok := recv.(*ast.Ident); ok {
				name = ident.Name + "." + name
			}
		}
		start := fset.Position(fn.Pos()).Offset
		end := fset.Position(fn.End()).Offset
		out[name] = src[start:end]
		names = append(names, name)
	}
	return out, names
}

// sleepCallNames are the callees that mean "this function waits".
var sleepCallNames = map[string]bool{
	"Sleep":                    true,
	"sleep":                    true,
	"pathWaitSleep":            true,
	"cacheCleanSleep":          true,
	"stopFlushSleep":           true,
	"wait":                     true,
	"propagationAnnounceSleep": true,
	"outboundTriggerSleep":     true,
}

// bodySleeps reports whether a function body contains a wait call.
func bodySleeps(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if sleepCallNames[f.Name] {
				found = true
			}
		case *ast.SelectorExpr:
			if sleepCallNames[f.Sel.Name] {
				found = true
			}
		}
		return true
	})
	return found
}
