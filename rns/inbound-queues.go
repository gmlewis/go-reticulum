// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"sync"
	"time"
)

// NumTrafficClasses is the number of inbound traffic classes, matching the
// length of Python's Transport.InboundQueues size tuple (Transport.py:44-105).
const NumTrafficClasses = 4

// Default inbound queue lengths, matching Python's Transport constants
// (Transport.py:139-142). They are the fallback when the corresponding
// qlen_in_* config key is not a positive integer (Reticulum.py:706-721).
const (
	DefaultInboundDataQueueLength     = 4096
	DefaultInboundAnnounceQueueLength = 256
	DefaultInboundPRQueueLength       = 256
	DefaultInboundILQueueLength       = 128
)

// Errors returned by InboundQueues. Python raises queue.Full and queue.Empty;
// Go reports them as values so callers can treat a full queue as an ordinary
// drop.
var (
	// ErrInboundQueueFull reports that a Put found its traffic class at
	// capacity. The item was not queued and the class's drop counter was
	// incremented.
	ErrInboundQueueFull = errors.New("inbound queue is full")

	// ErrInboundQueueEmpty reports that a non-blocking or timed-out Get found
	// every traffic class empty.
	ErrInboundQueueEmpty = errors.New("inbound queue is empty")

	// ErrInboundQueueStopped reports that GetUntil stopped waiting because its
	// stop channel closed.
	ErrInboundQueueStopped = errors.New("inbound queue stopped")

	// ErrInvalidTrafficClass reports a traffic class outside
	// [0, NumTrafficClasses).
	ErrInvalidTrafficClass = errors.New("invalid traffic class")
)

// InboundQueues is the bounded, per-traffic-class inbound packet queue of
// Python Transport.InboundQueues (RNS/Transport.py:42-105).
//
// Each traffic class has its own bounded FIFO and its own dropped counter. Get
// scans the classes in priority order — TCData first, then TCAnnounce,
// TCPathRequest and finally TCIngressLimited — so a busy data stream never
// starves announces, and a queue under pressure drops from the class that is
// full rather than blocking the interface read loop.
//
// Put is always non-blocking, exactly as Python's only caller invokes it
// (Transport.py:1802, block=False); Python's block and timeout parameters are
// accepted but never consulted, so the port omits them.
//
// InboundQueues is safe for concurrent use by multiple producers and a single
// drainer, matching Python's Condition-based implementation.
type InboundQueues[T any] struct {
	mu      sync.Mutex
	queues  [][]T
	sizes   []int
	dropped []int

	// notify is closed and replaced by every successful Put. A Get captures
	// the current channel under mu, releases the lock and waits on it; the
	// close is therefore never missed.
	notify chan struct{}
}

// NewInboundQueues returns an InboundQueues whose traffic classes are bounded
// by sizes, in traffic-class order.
func NewInboundQueues[T any](sizes [NumTrafficClasses]int) *InboundQueues[T] {
	q := &InboundQueues[T]{
		queues:  make([][]T, NumTrafficClasses),
		sizes:   make([]int, NumTrafficClasses),
		dropped: make([]int, NumTrafficClasses),
		notify:  make(chan struct{}),
	}
	copy(q.sizes, sizes[:])
	return q
}

// Put appends item to the queue for trafficClass. It never blocks: when the
// class is at capacity the item is dropped, the class's drop counter is
// incremented, and ErrInboundQueueFull is returned (Python's
// InboundQueues.put, Transport.py:55-66).
func (q *InboundQueues[T]) Put(trafficClass int, item T) error {
	if trafficClass < 0 || trafficClass >= NumTrafficClasses {
		return ErrInvalidTrafficClass
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.queues[trafficClass]) >= q.sizes[trafficClass] {
		q.dropped[trafficClass]++
		return ErrInboundQueueFull
	}
	q.queues[trafficClass] = append(q.queues[trafficClass], item)
	// Wake every waiting drainer. Python's single drainer only needs
	// notify(), but its comment already anticipates notify_all
	// (Transport.py:62-64).
	close(q.notify)
	q.notify = make(chan struct{})
	return nil
}

// Get removes and returns the highest-priority queued item, blocking until one
// is available (Python InboundQueues.get with block=True, timeout=None,
// Transport.py:68-79).
func (q *InboundQueues[T]) Get() (T, error) {
	return q.get(true, 0, false)
}

// TryGet removes and returns the highest-priority queued item without
// blocking. It returns ErrInboundQueueEmpty when every class is empty (Python
// get(block=False)).
func (q *InboundQueues[T]) TryGet() (T, error) {
	return q.get(false, 0, false)
}

// GetWithTimeout removes and returns the highest-priority queued item, waiting
// at most timeout. A timeout of zero or less fails immediately after the first
// scan, exactly as a non-positive remaining budget does in Python.
func (q *InboundQueues[T]) GetWithTimeout(timeout time.Duration) (T, error) {
	return q.get(true, timeout, true)
}

// GetUntil removes and returns the highest-priority queued item, blocking
// until one is available or stop is closed. It is the drainer's form of Get:
// the transport's drainer must exit promptly when the transport stops, which
// Python's daemon thread simply does by being killed at process exit.
func (q *InboundQueues[T]) GetUntil(stop <-chan struct{}) (T, error) {
	var zero T
	for {
		q.mu.Lock()
		for class := range q.queues {
			if len(q.queues[class]) > 0 {
				item := q.queues[class][0]
				q.queues[class] = q.queues[class][1:]
				q.mu.Unlock()
				return item, nil
			}
		}
		ch := q.notify
		q.mu.Unlock()

		select {
		case <-ch:
		case <-stop:
			// Re-scan once so an item queued before the stop is still
			// processed, matching the ordering a drainer that was already
			// awake would see.
			q.mu.Lock()
			for class := range q.queues {
				if len(q.queues[class]) > 0 {
					item := q.queues[class][0]
					q.queues[class] = q.queues[class][1:]
					q.mu.Unlock()
					return item, nil
				}
			}
			q.mu.Unlock()
			return zero, ErrInboundQueueStopped
		}
	}
}

// get implements Python's scan-then-wait loop (Transport.py:68-79). When
// hasTimeout is false it waits without a deadline.
func (q *InboundQueues[T]) get(block bool, timeout time.Duration, hasTimeout bool) (T, error) {
	var zero T
	var deadline time.Time
	if hasTimeout {
		deadline = time.Now().Add(timeout)
	}
	for {
		q.mu.Lock()
		for class := range q.queues {
			if len(q.queues[class]) > 0 {
				item := q.queues[class][0]
				q.queues[class] = q.queues[class][1:]
				q.mu.Unlock()
				return item, nil
			}
		}
		if !block {
			q.mu.Unlock()
			return zero, ErrInboundQueueEmpty
		}
		if hasTimeout && !time.Now().Before(deadline) {
			q.mu.Unlock()
			return zero, ErrInboundQueueEmpty
		}
		ch := q.notify
		q.mu.Unlock()

		if !hasTimeout {
			<-ch
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return zero, ErrInboundQueueEmpty
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ch:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			// Re-scan once more before giving up: a Put racing the timer
			// close may have queued an item Python would still return.
		}
	}
}

// QSize reports the total number of queued items across every traffic class
// (Python qsize(None)). Like Python's, the read takes the lock.
func (q *InboundQueues[T]) QSize() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	total := 0
	for class := range q.queues {
		total += len(q.queues[class])
	}
	return total
}

// QSizeOf reports the number of queued items in trafficClass (Python
// qsize(traffic_class)).
func (q *InboundQueues[T]) QSizeOf(trafficClass int) int {
	if trafficClass < 0 || trafficClass >= NumTrafficClasses {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queues[trafficClass])
}

// Snapshot returns a consistent single-instant view of the queues: the total
// height, the per-class heights, and the per-class cumulative drop counts
// (Python snapshot, Transport.py:85-90).
func (q *InboundQueues[T]) Snapshot() (total int, heights [NumTrafficClasses]int, dropped [NumTrafficClasses]int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for class := range q.queues {
		heights[class] = len(q.queues[class])
		dropped[class] = q.dropped[class]
		total += heights[class]
	}
	return total, heights, dropped
}
