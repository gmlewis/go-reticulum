// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// blackholeListApp is the app-name/aspects of the blackhole list publishing
// destination (Python Transport.APP_NAME, "info", "blackhole").
var blackholeListApp = []string{"rnstransport", "info", "blackhole"}

// blackholeListEntriesFromMap converts a msgpack-decoded /list response
// (map[any]any keyed by binary identity hashes, values
// {source, until, reason}) into a []blackholeListEntry for the merger.
func blackholeListEntriesFromMap(m map[any]any) []blackholeListEntry {
	out := make([]blackholeListEntry, 0, len(m))
	for k, v := range m {
		ih := blackholeMapKey(k)
		if ih == nil {
			continue
		}
		se, ok := v.(map[any]any)
		if !ok {
			continue
		}
		source, _ := se["source"].([]byte)
		out = append(out, blackholeListEntry{
			identityHash: copyBytes(ih),
			source:       copyBytes(source),
			until:        blackholeUntil(se),
			reason:       blackholeReason(se),
		})
	}
	return out
}

// blackholeFetch is the real /list retrieval used by EnableBlackholeUpdater.
// It mirrors the fetch half of Discovery.BlackholeUpdater
// (Discovery.py:683-712): derive the rnstransport.info.blackhole
// destination hash from the source identity hash, await a path, recall the
// source identity, establish a link, request "/list", and return the parsed
// list. Each stage is bounded by BlackholeSourceTimeout.
func (ts *TransportSystem) blackholeFetch(sourceIdentityHash []byte) ([]blackholeListEntry, error) {
	destHash := CalculateHash(&Identity{Hash: sourceIdentityHash}, blackholeListApp[0], blackholeListApp[1:]...)

	if _, err := ts.AwaitPath(destHash, BlackholeSourceTimeout); err != nil {
		return nil, fmt.Errorf("no path to blackhole source %x: %v", sourceIdentityHash, err)
	}
	// AwaitPath returns nil error with a nil-ish result on timeout; re-check
	// the path is actually known.
	if !ts.HasPath(destHash) {
		return nil, fmt.Errorf("no path available for blackhole source %x", sourceIdentityHash)
	}

	remoteIdentity := ts.Recall(destHash)
	if remoteIdentity == nil {
		return nil, fmt.Errorf("could not recall identity for blackhole source %x", sourceIdentityHash)
	}

	dest, err := NewDestination(ts, remoteIdentity, DestinationOut, DestinationSingle, blackholeListApp[0], blackholeListApp[1:]...)
	if err != nil {
		return nil, fmt.Errorf("constructing blackhole destination: %v", err)
	}

	link, err := NewLink(ts, dest)
	if err != nil {
		return nil, fmt.Errorf("creating blackhole link: %v", err)
	}

	established := make(chan struct{}, 1)
	link.SetLinkEstablishedCallback(func(l *Link) {
		select {
		case established <- struct{}{}:
		default:
		}
	})
	if err := link.Establish(); err != nil {
		return nil, fmt.Errorf("establishing blackhole link: %v", err)
	}

	select {
	case <-established:
	case <-time.After(BlackholeSourceTimeout):
		link.Teardown()
		return nil, fmt.Errorf("blackhole link establishment timed out for %x", sourceIdentityHash)
	}

	responseCh := make(chan any, 1)
	if _, err := link.Request("/list", nil, func(rr *RequestReceipt) {
		if rr.Status == RequestReady {
			responseCh <- rr.Response
		}
	}, func(rr *RequestReceipt) {
		select {
		case responseCh <- nil:
		default:
		}
	}, nil, BlackholeSourceTimeout); err != nil {
		link.Teardown()
		return nil, fmt.Errorf("requesting /list: %v", err)
	}

	var resp any
	select {
	case resp = <-responseCh:
	case <-time.After(BlackholeSourceTimeout):
		link.Teardown()
		return nil, fmt.Errorf("/list request timed out for %x", sourceIdentityHash)
	}
	link.Teardown()

	if resp == nil {
		return nil, fmt.Errorf("blackhole /list request failed for %x", sourceIdentityHash)
	}
	m, ok := resp.(map[any]any)
	if !ok {
		return nil, fmt.Errorf("unexpected /list response type %T for %x", resp, sourceIdentityHash)
	}
	return blackholeListEntriesFromMap(m), nil
}

// BlackholeUpdater timing constants, captured from Python
// RNS.Discovery.BlackholeUpdater (Discovery.py:636-639, rns 1.3.5). These
// govern the fetch/update loop and must match Python exactly so that
// per-source update cadence is identical.
const (
	// BlackholeInitialWait is the delay before the first job pass runs
	// (Python INITIAL_WAIT = 20).
	BlackholeInitialWait = 20 * time.Second
	// BlackholeJobInterval is the interval between job passes
	// (Python JOB_INTERVAL = 60).
	BlackholeJobInterval = 60 * time.Second
	// BlackholeUpdateInterval is the minimum interval between fetches of any
	// single source (Python UPDATE_INTERVAL = 1*60*60).
	BlackholeUpdateInterval = 1 * 60 * 60 * time.Second
	// BlackholeSourceTimeout is the link-establishment timeout for a fetch
	// (Python SOURCE_TIMEOUT = 25). Declared for parity; the value is used by
	// the real fetch implementation wired in EnableBlackholeUpdater.
	BlackholeSourceTimeout = 25 * time.Second
)

// blackholeFetchFunc fetches a blackhole list from a remote source identity.
// It mirrors the link half of Discovery.BlackholeUpdater.update_link_established
// (Discovery.py:658-712): await the path to the source's
// rnstransport.info.blackhole destination, establish a link, request "/list",
// and return the parsed list. The concrete implementation is injected by
// EnableBlackholeUpdater (so the loop core is unit-testable with a fake
// source).
type blackholeFetchFunc func(sourceIdentityHash []byte) ([]blackholeListEntry, error)

// BlackholeUpdater is the Go port of RNS.Discovery.BlackholeUpdater
// (Discovery.py:635-720). Every JOB_INTERVAL it iterates the configured
// blackhole_sources and, for each source whose last update is older than
// UPDATE_INTERVAL, fetches its "/list" RPC, merges previously-unseen
// entries into Transport.blackholed_identities, and persists the fetched
// list to blackholepath/<hex(source)>.
type BlackholeUpdater struct {
	ts *TransportSystem

	mu          sync.Mutex
	lastUpdates map[string]time.Time
	shouldRun   bool

	initialWait    time.Duration
	jobInterval    time.Duration
	updateInterval time.Duration

	// nowFn is the clock used by the production loop (Start/job). Unit tests
	// drive runJobPass directly with explicit times and bypass nowFn.
	nowFn     func() time.Time
	sourcesFn func() [][]byte
	fetch     blackholeFetchFunc

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewBlackholeUpdater constructs a BlackholeUpdater bound to the given
// transport system. sourcesFn returns the current blackhole_sources set
// (Python RNS.Reticulum.blackhole_sources); fetch performs a single "/list"
// retrieval from a source identity.
func NewBlackholeUpdater(ts *TransportSystem, sourcesFn func() [][]byte, fetch blackholeFetchFunc) *BlackholeUpdater {
	if sourcesFn == nil {
		sourcesFn = func() [][]byte { return nil }
	}
	if fetch == nil {
		fetch = func([]byte) ([]blackholeListEntry, error) {
			return nil, fmt.Errorf("blackhole fetch not configured")
		}
	}
	return &BlackholeUpdater{
		ts:             ts,
		lastUpdates:    make(map[string]time.Time),
		initialWait:    BlackholeInitialWait,
		jobInterval:    BlackholeJobInterval,
		updateInterval: BlackholeUpdateInterval,
		nowFn:          time.Now,
		sourcesFn:      sourcesFn,
		fetch:          fetch,
	}
}

// SetClock overrides the wall-clock used by the production loop. It is
// intended for tests that exercise Start/job with a controlled clock.
func (u *BlackholeUpdater) SetClock(now func() time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if now != nil {
		u.nowFn = now
	}
}

// SetUpdateInterval sets the minimum interval between fetches of any single
// blackhole source (Python RNS.Reticulum.__blackhole_update_interval,
// Reticulum.py:601-604 / Discovery.py:824). runJobPass reads the live value
// each pass, so a change takes effect on the next job tick.
func (u *BlackholeUpdater) SetUpdateInterval(d time.Duration) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if d > 0 {
		u.updateInterval = d
	}
}

// UpdateInterval returns the currently configured blackhole source fetch
// interval (Python RNS.Reticulum.blackhole_update_interval()).
func (u *BlackholeUpdater) UpdateInterval() time.Duration {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.updateInterval
}

// Start launches the updater loop (Python BlackholeUpdater.start,
// Discovery.py:642-651). It is idempotent: a second call while running is a
// no-op.
func (u *BlackholeUpdater) Start() {
	u.mu.Lock()
	if u.shouldRun {
		u.mu.Unlock()
		return
	}
	u.shouldRun = true
	u.stopCh = make(chan struct{})
	u.doneCh = make(chan struct{})
	stopCh := u.stopCh
	u.mu.Unlock()

	go u.job(stopCh)
}

// Stop signals the loop to exit and blocks until it has (Python
// BlackholeUpdater.stop, Discovery.py:653).
func (u *BlackholeUpdater) Stop() {
	u.mu.Lock()
	if !u.shouldRun {
		u.mu.Unlock()
		return
	}
	u.shouldRun = false
	close(u.stopCh)
	done := u.doneCh
	u.mu.Unlock()
	<-done
}

// job is the production loop (Python BlackholeUpdater.job,
// Discovery.py:655-720): wait INITIAL_WAIT, then every JOB_INTERVAL run a
// pass over the sources.
func (u *BlackholeUpdater) job(stopCh <-chan struct{}) {
	doneCh := u.doneCh
	defer close(doneCh)
	select {
	case <-stopCh:
		return
	case <-time.After(u.initialWait):
	}
	u.mu.Lock()
	now := u.nowFn
	u.mu.Unlock()
	u.runJobPass(now())
	ticker := time.NewTicker(u.jobInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			u.mu.Lock()
			run := u.shouldRun
			now = u.nowFn
			u.mu.Unlock()
			if !run {
				return
			}
			u.runJobPass(now())
		}
	}
}

// runJobPass executes one pass over the configured sources at instant now,
// returning the count of newly-merged entries. It mirrors the body of
// BlackholeUpdater.job's while-loop (Discovery.py:656-718): for each source
// whose last update is older than UPDATE_INTERVAL, fetch its list and apply
// it. The clock is supplied by the caller so the timing logic is
// deterministic and unit-testable.
func (u *BlackholeUpdater) runJobPass(now time.Time) int {
	sources := u.sourcesFn()
	applied := 0
	for _, src := range sources {
		// Python: last_update defaults to 0; fetch only if
		// now > last_update + UPDATE_INTERVAL. A zero-value lastUpdate
		// (never fetched) is always older than UPDATE_INTERVAL for any
		// realistic clock, so the first pass fetches every source.
		u.mu.Lock()
		lastUpdate := u.lastUpdates[string(src)]
		updateInterval := u.updateInterval
		u.mu.Unlock()
		if !now.After(lastUpdate.Add(updateInterval)) {
			continue
		}

		list, err := u.fetch(src)
		if err != nil {
			u.ts.logger.Error("Error fetching blackhole list from %x: %v", src, err)
			// lastUpdates is intentionally not advanced on failure so the
			// source is retried on the next pass (Python does not set
			// last_updates when await_path/link raises).
			continue
		}

		applied += u.applyUpdate(src, list)

		// The fetch was initiated successfully; record the time so this
		// source is not refetched until UPDATE_INTERVAL elapses (Python
		// sets last_updates immediately after RNS.Link is initiated).
		u.mu.Lock()
		u.lastUpdates[string(src)] = now
		u.mu.Unlock()
	}
	return applied
}

// applyUpdate merges previously-unseen entries from a fetched list into the
// in-memory blackhole set and, if any were added, persists the full fetched
// list to blackholepath/<hex(source)> (Python
// BlackholeUpdater.update_link_established, Discovery.py:660-705). Only
// entries not already present are merged, matching Python's
// `if not identity_hash in RNS.Transport.blackholed_identities` guard.
func (u *BlackholeUpdater) applyUpdate(sourceIdentityHash []byte, list []blackholeListEntry) int {
	u.ts.mu.Lock()
	u.ts.ensureStateLocked()
	added := 0
	for _, e := range list {
		key := string(e.identityHash)
		if _, exists := u.ts.blackholedIdentities[key]; exists {
			continue
		}
		entry := BlackholeIdentityEntry{
			IdentityHash: copyBytes(e.identityHash),
			Source:       copyBytes(e.source),
			Reason:       e.reason,
		}
		if e.until != nil {
			t := *e.until
			entry.Until = &t
		}
		u.ts.blackholedIdentities[key] = entry
		added++
	}
	blackholePath := u.ts.blackholePath
	u.ts.mu.Unlock()

	if added > 0 {
		u.persistSourceList(blackholePath, sourceIdentityHash, list)
	}
	return added
}

// persistSourceList atomically writes a fetched blackhole list to
// blackholepath/<hex(sourceIdentityHash)> as a msgpack map with binary
// identity-hash keys (Python writes umsgpack.packb(blackhole_list) to that
// path, Discovery.py:681-690).
func (u *BlackholeUpdater) persistSourceList(blackholePath string, sourceIdentityHash []byte, list []blackholeListEntry) {
	if blackholePath == "" {
		return
	}
	packed, err := packBlackholeList(list)
	if err != nil {
		u.ts.logger.Error("Error packing fetched blackhole list from %x: %v", sourceIdentityHash, err)
		return
	}
	path := filepath.Join(blackholePath, hex.EncodeToString(sourceIdentityHash))
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, packed, 0o600); err != nil {
		u.ts.logger.Error("Error writing fetched blackhole list from %x: %v", sourceIdentityHash, err)
		return
	}
	// os.Rename atomically replaces an existing destination on POSIX, so no
	// prior Remove is needed (Python opens the path with "wb", truncating in
	// place; the rename is an atomic equivalent).
	if err := os.Rename(tmpPath, path); err != nil {
		u.ts.logger.Error("Error persisting fetched blackhole list from %x: %v", sourceIdentityHash, err)
	}
}
