// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package rrc

import (
	"sync"
	"testing"
)

// TestLoopbackPortsAreNeverHandedOutTwice pins the rule the whole package's rigs
// depend on: no two allocations in a run get the same port.
//
// A port taken by listening on 127.0.0.1:0 and closing it is free again at once,
// so a suite that allocates dozens of them in parallel can be handed the same
// number twice, and the rig that binds it second dies with "address already in
// use". That is what CI reported from TestFleetTwoInstancesDeliverWelcome, on a
// runner whose allocator happened to repeat itself. The allocator is the only
// thing that can rule it out, because the kernel's own choice is random.
func TestLoopbackPortsAreNeverHandedOutTwice(t *testing.T) {
	t.Parallel()

	const allocations = 400
	var (
		mu   sync.Mutex
		seen = map[int]int{}
		wg   sync.WaitGroup
	)
	for range allocations {
		wg.Go(func() {
			port := freeLoopbackPort(t)
			mu.Lock()
			seen[port]++
			mu.Unlock()
		})
	}
	wg.Wait()

	for port, count := range seen {
		if count > 1 {
			t.Errorf("port %v was handed out %v times; two rigs sharing it fight over it", port, count)
		}
	}
	if len(seen) != allocations {
		t.Errorf("%v allocations produced %v distinct ports, want %v", allocations, len(seen), allocations)
	}
}

// TestAFreeLoopbackPortIsBindable asserts the port is one a rig can actually bind
// when it is handed out, which is what makes it usable rather than merely
// distinct.
func TestAFreeLoopbackPortIsBindable(t *testing.T) {
	t.Parallel()

	port := freeLoopbackPort(t)
	if port == 0 {
		t.Fatal("freeLoopbackPort returned no port")
	}
	if got := freeLoopbackPort(t); got == port {
		t.Errorf("two allocations returned %v, want different ports", port)
	}
}
