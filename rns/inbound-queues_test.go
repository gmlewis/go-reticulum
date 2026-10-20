// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func testQueues(sizes [NumTrafficClasses]int) *InboundQueues[int] {
	return NewInboundQueues[int](sizes)
}

// TestInboundQueuesPriorityOrder pins Python's priority scan
// (Transport.py:72-74): Get returns TCData, then TCAnnounce, then
// TCPathRequest, then TCIngressLimited, regardless of insertion order.
func TestInboundQueuesPriorityOrder(t *testing.T) {
	t.Parallel()
	q := testQueues([NumTrafficClasses]int{4, 4, 4, 4})
	// Insert in reverse priority order so FIFO alone cannot pass.
	for _, class := range []int{TCIngressLimited, TCPathRequest, TCAnnounce, TCData} {
		if err := q.Put(class, class*10); err != nil {
			t.Fatalf("Put(%v) failed: %v", class, err)
		}
	}
	for _, want := range []int{TCData, TCAnnounce, TCPathRequest, TCIngressLimited} {
		got, err := q.TryGet()
		if err != nil {
			t.Fatalf("TryGet after Put(%v) failed: %v", want, err)
		}
		if got != want*10 {
			t.Errorf("TryGet = %v, want the TC %v item (%v)", got, want, want*10)
		}
	}
	if _, err := q.TryGet(); !errors.Is(err, ErrInboundQueueEmpty) {
		t.Errorf("TryGet on an empty queue = %v, want ErrInboundQueueEmpty", err)
	}
}

// TestInboundQueuesPreservesPerClassFIFO checks that the scan only changes the
// order BETWEEN classes: within a class the queue is FIFO.
func TestInboundQueuesPreservesPerClassFIFO(t *testing.T) {
	t.Parallel()
	q := testQueues([NumTrafficClasses]int{8, 8, 8, 8})
	for i := range 5 {
		if err := q.Put(TCData, i); err != nil {
			t.Fatalf("Put(%v) failed: %v", i, err)
		}
	}
	for want := range 5 {
		got, err := q.TryGet()
		if err != nil {
			t.Fatalf("TryGet #%v failed: %v", want, err)
		}
		if got != want {
			t.Errorf("TryGet #%v = %v, want %v", want, got, want)
		}
	}
}

// TestInboundQueuesFullDropAccounting pins Python's Put
// (Transport.py:55-66): a full class drops the item, counts the drop on that
// class only, and leaves the other classes untouched.
func TestInboundQueuesFullDropAccounting(t *testing.T) {
	t.Parallel()
	q := testQueues([NumTrafficClasses]int{1, 2, 1, 1})
	if err := q.Put(TCData, 1); err != nil {
		t.Fatalf("first Put failed: %v", err)
	}
	if err := q.Put(TCData, 2); !errors.Is(err, ErrInboundQueueFull) {
		t.Fatalf("Put into a full queue = %v, want ErrInboundQueueFull", err)
	}
	if err := q.Put(TCAnnounce, 3); err != nil {
		t.Fatalf("Put into another class failed: %v", err)
	}

	total, heights, dropped := q.Snapshot()
	if want := (struct {
		total   int
		heights [NumTrafficClasses]int
		dropped [NumTrafficClasses]int
	}{2, [NumTrafficClasses]int{1, 1, 0, 0}, [NumTrafficClasses]int{1, 0, 0, 0}}); total != want.total ||
		heights != want.heights || dropped != want.dropped {
		t.Errorf("Snapshot = (%v, %v, %v), want (%v, %v, %v)",
			total, heights, dropped, want.total, want.heights, want.dropped)
	}
	if got := q.QSizeOf(TCData); got != 1 {
		t.Errorf("QSizeOf(TCData) = %v, want 1", got)
	}
	if got := q.QSize(); got != 2 {
		t.Errorf("QSize() = %v, want 2", got)
	}
}

// TestInboundQueuesSnapshotIsAConsistentInstant checks the shapes the stats
// path relies on: the total is the sum of the heights, and dropped is
// cumulative.
func TestInboundQueuesSnapshotIsAConsistentInstant(t *testing.T) {
	t.Parallel()
	q := testQueues([NumTrafficClasses]int{2, 2, 2, 2})
	for _, class := range []int{TCData, TCData, TCAnnounce, TCPathRequest, TCIngressLimited} {
		item := class
		_ = q.Put(class, item) // capacity is ample except TCData at 2
	}
	_ = q.Put(TCData, 99)
	total, heights, dropped := q.Snapshot()
	sum := 0
	for _, h := range heights {
		sum += h
	}
	if total != sum {
		t.Errorf("Snapshot total %v != sum of heights %v", total, sum)
	}
	if heights != [NumTrafficClasses]int{2, 1, 1, 1} {
		t.Errorf("heights = %v, want [2 1 1 1]", heights)
	}
	if dropped != [NumTrafficClasses]int{1, 0, 0, 0} {
		t.Errorf("dropped = %v, want [1 0 0 0]", dropped)
	}
}

// TestInboundQueuesRejectsInvalidTrafficClass checks that an out-of-range
// class is reported rather than panicking the way Python's list index would.
func TestInboundQueuesRejectsInvalidTrafficClass(t *testing.T) {
	t.Parallel()
	q := testQueues([NumTrafficClasses]int{1, 1, 1, 1})
	for _, class := range []int{-1, NumTrafficClasses, 17} {
		if err := q.Put(class, 1); !errors.Is(err, ErrInvalidTrafficClass) {
			t.Errorf("Put(%v) = %v, want ErrInvalidTrafficClass", class, err)
		}
		if got := q.QSizeOf(class); got != 0 {
			t.Errorf("QSizeOf(%v) = %v, want 0", class, got)
		}
	}
}

// TestInboundQueuesGetWithZeroTimeoutFailsImmediately pins the non-positive
// budget branch of Python's get (Transport.py:68-79): an expired deadline
// raises Empty instead of waiting.
func TestInboundQueuesGetWithZeroTimeoutFailsImmediately(t *testing.T) {
	t.Parallel()
	q := testQueues([NumTrafficClasses]int{1, 1, 1, 1})
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := q.GetWithTimeout(timeout); !errors.Is(err, ErrInboundQueueEmpty) {
			t.Errorf("GetWithTimeout(%v) on an empty queue = %v, want ErrInboundQueueEmpty", timeout, err)
		}
	}
}

// TestInboundQueuesBlockingGetWakesOnPut pins the wait/notify path: a Get
// blocked with no item returns as soon as any class receives one, preserving
// priority across the wake-up.
func TestInboundQueuesBlockingGetWakesOnPut(t *testing.T) {
	testutils.RunInBubble(t, func(t *testing.T) {
		q := testQueues([NumTrafficClasses]int{4, 4, 4, 4})
		got := make(chan int, 1)
		go func() {
			item, err := q.Get()
			if err != nil {
				got <- -1
				return
			}
			got <- item
		}()
		testutils.Wait()
		// A lower-priority class must still wake the drainer.
		if err := q.Put(TCAnnounce, 42); err != nil {
			t.Fatalf("Put failed: %v", err)
		}
		if item := <-got; item != 42 {
			t.Errorf("blocked Get returned %v, want 42", item)
		}
	})
}

// TestInboundQueuesGetWithTimeoutWaitsThenFails pins the timed wait: an empty
// queue fails only after the budget has elapsed, and a Put inside the budget
// wins.
func TestInboundQueuesGetWithTimeoutWaitsThenFails(t *testing.T) {
	testutils.RunInBubble(t, func(t *testing.T) {
		q := testQueues([NumTrafficClasses]int{4, 4, 4, 4})
		start := time.Now()
		if _, err := q.GetWithTimeout(100 * time.Millisecond); !errors.Is(err, ErrInboundQueueEmpty) {
			t.Fatalf("GetWithTimeout on an empty queue = %v, want ErrInboundQueueEmpty", err)
		}
		if elapsed := time.Since(start); elapsed != 100*time.Millisecond {
			t.Errorf("GetWithTimeout waited %v, want 100ms (the bubble clock is deterministic)", elapsed)
		}

		go func() {
			time.Sleep(50 * time.Millisecond)
			if err := q.Put(TCData, 7); err != nil {
				t.Errorf("Put failed: %v", err)
			}
		}()
		item, err := q.GetWithTimeout(time.Second)
		if err != nil {
			t.Fatalf("GetWithTimeout with a put inside the budget failed: %v", err)
		}
		if item != 7 {
			t.Errorf("GetWithTimeout = %v, want 7", item)
		}
	})
}

// TestInboundQueuesConcurrentProducers feeds the queue from several producers
// while a drainer empties it; nothing may be lost or duplicated while no class
// is full.
func TestInboundQueuesConcurrentProducers(t *testing.T) {
	testutils.RunInBubble(t, func(t *testing.T) {
		q := testQueues([NumTrafficClasses]int{64, 64, 64, 64})
		const producers, perProducer = 4, 16
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range producers * perProducer {
				if _, err := q.Get(); err != nil {
					t.Errorf("Get failed: %v", err)
					return
				}
			}
		}()
		for p := range producers {
			go func() {
				for i := range perProducer {
					if err := q.Put(p, i); err != nil {
						t.Errorf("Put failed: %v", err)
						return
					}
				}
			}()
		}
		<-done
		if total := q.QSize(); total != 0 {
			t.Errorf("QSize after draining = %v, want 0", total)
		}
		if _, _, dropped := q.Snapshot(); dropped != [NumTrafficClasses]int{} {
			t.Errorf("dropped = %v, want none (no class was full)", dropped)
		}
	})
}
