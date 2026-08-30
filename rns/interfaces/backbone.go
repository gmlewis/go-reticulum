// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"log"
	"sync"
	"time"
)

// BackboneDefaultIFACSize is the DEFAULT_IFAC_SIZE for Backbone server and
// client interfaces (RNS/Interfaces/BackboneInterface.py:54,569).
const BackboneDefaultIFACSize = 16

// BackboneHWMTU is the hardware MTU ceiling for Backbone interfaces
// (RNS/Interfaces/BackboneInterface.py HW_MTU = 1048576). Backbone links
// carry aggregated traffic and permit larger frames than a plain TCP
// interface (TCPHWMTU = 262144); the inbound HDLC frame-length gate uses this
// value so Backbone does not reject frames Python would accept.
const BackboneHWMTU = 1048576

// Fast-flap blocking defaults (RNS/Interfaces/BackboneInterface.py:57-60,
// v1.3.9). A spawned BackboneClientInterface whose connected time is below
// FastFlapThreshold seconds counts as a fast flap; once an IP exceeds
// FastFlapGrace flaps within the FastFlapExpiry window it is blocked.
const (
	// BackboneBlockFastFlapping is the Python BLOCK_FAST_FLAPPING default.
	BackboneBlockFastFlapping = true
	// BackboneFastFlapThreshold mirrors FAST_FLAP_THRESHOLD (seconds).
	BackboneFastFlapThreshold = 20.0
	// BackboneFastFlapGrace mirrors FAST_FLAP_GRACE (flap count).
	BackboneFastFlapGrace = 5
	// BackboneFastFlapExpiry mirrors FAST_FLAP_EXPIRY (seconds).
	BackboneFastFlapExpiry = 12 * 60 * 60.0
)

// fastFlapEntry records the flap history for one remote IP, mirroring Python's
// fast_flapping[remote_id] = [started_flapping, last_flap, flaps]
// (RNS/Interfaces/BackboneInterface.py:836-842, v1.3.9).
type fastFlapEntry struct {
	startedFlapping time.Time
	lastFlap        time.Time
	flaps           int
}

// BackboneInterface provides a robust, highly available TCP listener used as a
// core routing nexus. It encapsulates TCP server logic and accepts point-to-
// point links from downstream clients.
type BackboneInterface struct {
	*TCPServerInterface

	// Aggregate burst-state cache (BackboneInterface.py:173-225). Each
	// aggregate getter recomputes over the spawned peers at most once per
	// 2s window; aggMu guards the cache fields.
	aggMu                       sync.Mutex
	lastICBurstCheck            time.Time
	lastICBurstState            bool
	lastICBurstActivatedCheck   time.Time
	lastICBurstActivated        time.Time
	lastICPrBurstCheck          time.Time
	lastICPrBurstState          bool
	lastICPrBurstActivatedCheck time.Time
	lastICPrBurstActivated      time.Time

	// Fast-flap blocking state (BackboneInterface.py:57-62,126-145,820-843).
	// The registry is per-instance for test isolation under -race; Python uses
	// a class-level dict shared across all BackboneInterface instances, which
	// is observationally identical for the single-interface case.
	blockFastFlapping bool
	fastFlapThreshold float64 // seconds
	fastFlapGrace     int
	fastFlapExpiry    float64 // seconds
	fastFlappingMu    sync.Mutex
	fastFlapping      map[string]*fastFlapEntry
	// nowFn is the clock used by recordFlap / isBlocked / blockedCount so the
	// fast-flap logic is time-injectable for tests. It defaults to time.Now.
	nowFn func() time.Time
}

// NewBackboneInterface binds and initializes a TCP-based BackboneInterface on the
// given address and port. It creates a persistent listener and dispatches
// incoming frames to router logic. Spawned clients inherit BackboneHWMTU for
// inbound HDLC frame-length validation.
func NewBackboneInterface(name, bindIP string, bindPort int, handler InboundHandler, onConnect ConnectHandler) (Interface, error) {
	inner, err := newTCPServerInterface(name, bindIP, bindPort, handler, onConnect, BackboneHWMTU)
	if err != nil {
		return nil, err
	}
	b := &BackboneInterface{TCPServerInterface: inner}
	b.ConfigureFastFlapping(BackboneBlockFastFlapping, BackboneFastFlapThreshold, BackboneFastFlapGrace, BackboneFastFlapExpiry)
	b.setNowFn(time.Now)
	// Wire the incoming-connection gate (reject blocked IPs before spawning)
	// and the spawned-teardown hook (record a flap on fast disconnect), so the
	// fast-flap mechanism rides the generic TCPServerInterface accept/teardown
	// paths (BackboneInterface.py:397,420-435,820-843). The hooks are guarded by
	// the server mutex so the accept loop (already started by
	// newTCPServerInterface) reads them safely.
	inner.mu.Lock()
	inner.incomingGate = func(remoteIP string) bool { return !b.isBlocked(remoteIP) }
	inner.onSpawnedDown = func(remoteIP string, spawnedAt time.Time) { b.recordFlap(remoteIP, spawnedAt) }
	inner.mu.Unlock()
	return b, nil
}

// Type returns the string "BackboneInterface" as the runtime type name.
func (b *BackboneInterface) Type() string { return "BackboneInterface" }

// HashString reproduces Python BackboneInterface.__str__
// (RNS/Interfaces/BackboneInterface.py:560-563), which Interface.get_hash
// hashes:
//
//	"BackboneInterface["+name+"/"+ip_str(bind_ip)+":"+str(bind_port)+"]"
//
// where ip_str brackets an IPv6 literal. This shadows the embedded
// TCPServerInterface.HashString ("TCPServerInterface[...]") so the hash
// matches Python for a Python-written destination_table lookup.
func (b *BackboneInterface) HashString() string {
	return "BackboneInterface[" + b.Name() + "/" + tcpHostPort(b.bindIP, b.bindPort) + "]"
}

// ConfigureFastFlapping sets the fast-flap blocking parameters and lazily
// initialises the per-instance registry. It mirrors Python's config parsing of
// block_fast_flapping / fast_flapping_threshold / fast_flapping_grace /
// fast_flapping_block_time (BackboneInterface.py:126-145, v1.3.9). The
// threshold and expiry are in seconds; the registry is left untouched so a
// reconfiguration does not clear existing flap history. The config fields are
// guarded by fastFlappingMu so a reconfigure racing with the accept-loop's
// gate check is safe.
func (b *BackboneInterface) ConfigureFastFlapping(block bool, threshold float64, grace int, expiry float64) {
	if b == nil {
		return
	}
	b.fastFlappingMu.Lock()
	defer b.fastFlappingMu.Unlock()
	b.blockFastFlapping = block
	b.fastFlapThreshold = threshold
	b.fastFlapGrace = grace
	b.fastFlapExpiry = expiry
	if b.fastFlapping == nil {
		b.fastFlapping = map[string]*fastFlapEntry{}
	}
}

// setNowFn installs the clock used by the fast-flap methods, under the same
// mutex that guards the config it reads alongside, so a test clock swap races
// safely with the accept loop.
func (b *BackboneInterface) setNowFn(now func() time.Time) {
	if b == nil {
		return
	}
	b.fastFlappingMu.Lock()
	b.nowFn = now
	b.fastFlappingMu.Unlock()
}

// recordFlap records a fast flap for remoteIP when the spawned client that
// connected at spawnedAt tore down within the fast-flap threshold. It mirrors
// Python's BackboneClientInterface.teardown fast-flap branch
// (BackboneInterface.py:826-853, v1.3.9/1.4.0): only connected_time < threshold
// counts; a missing record for the remote IP is created fresh
// ([now, now, 0]) rather than treated as an error; the flap is logged via the
// standard logger (debug), and once the count exceeds the grace a warning is
// logged. The whole update is wrapped in a recover guard mirroring Python's
// try/except so an unexpected error is logged, never a panic/trace path.
func (b *BackboneInterface) recordFlap(remoteIP string, spawnedAt time.Time) {
	if b == nil || remoteIP == "" {
		return
	}
	// Mirror Python's try/except (BackboneInterface.py:831-853): an unexpected
	// error in the statistics update is logged via the standard logger, not a
	// panic/trace path.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Error while updating fast-flapping interface statistics: %v", r)
		}
	}()
	b.fastFlappingMu.Lock()
	defer b.fastFlappingMu.Unlock()
	if !b.blockFastFlapping {
		return
	}
	now := b.now()
	connectedTime := now.Sub(spawnedAt)
	if connectedTime.Seconds() >= b.fastFlapThreshold {
		return
	}
	// None-check: a remote IP with no prior flap record gets a fresh entry
	// [now, now, 0] (BackboneInterface.py:836-837).
	entry, ok := b.fastFlapping[remoteIP]
	if !ok {
		entry = &fastFlapEntry{startedFlapping: now, lastFlap: now}
	}
	entry.lastFlap = now
	entry.flaps++
	b.fastFlapping[remoteIP] = entry
	// Log the flap (BackboneInterface.py:851). Python gates this behind
	// LOG_DEBUG; the Go port has no leveled logger in this package, so it is
	// emitted unconditionally via the standard logger.
	log.Printf("BackboneInterface %s is fast flapping, connection time was %s, %d fast flaps",
		b.Name(), connectedTime.Round(time.Second), entry.flaps)
	if entry.flaps > b.fastFlapGrace {
		// Grace exceeded: warn that further connections are ignored
		// (BackboneInterface.py:852).
		log.Printf("Ignoring further connections from %s due to fast-flapping", remoteIP)
	}
}

// isBlocked reports whether remoteIP should be rejected on an incoming
// connection, mirroring Python's incoming_connection gate
// (BackboneInterface.py:420-435, v1.3.9): an IP is blocked when its flap count
// exceeds the grace within the expiry window. A stale entry (last flap older
// than expiry) is purged and no longer blocks.
func (b *BackboneInterface) isBlocked(remoteIP string) bool {
	if b == nil || remoteIP == "" {
		return false
	}
	b.fastFlappingMu.Lock()
	defer b.fastFlappingMu.Unlock()
	if !b.blockFastFlapping {
		return false
	}
	entry := b.fastFlapping[remoteIP]
	if entry == nil {
		return false
	}
	if b.now().Sub(entry.lastFlap).Seconds() > b.fastFlapExpiry {
		delete(b.fastFlapping, remoteIP)
		return false
	}
	return entry.flaps > b.fastFlapGrace
}

// BlockedIPCount returns the number of IPs currently blocked (flaps > grace and
// not expired), purging stale entries first. It mirrors Python's
// blocked_ip_count property (BackboneInterface.py:537-560, v1.3.9): when
// blocking is disabled it returns 0; otherwise it scans the registry under the
// fast-flap lock, deletes entries whose last flap is older than the expiry
// window, and counts the remaining entries whose flap count exceeds the grace.
// The current time is read via the installed clock (nowFn) so the property is
// time-injectable for tests.
func (b *BackboneInterface) BlockedIPCount() int {
	if b == nil {
		return 0
	}
	b.fastFlappingMu.Lock()
	defer b.fastFlappingMu.Unlock()
	if !b.blockFastFlapping {
		return 0
	}
	now := b.now()
	count := 0
	for ip, entry := range b.fastFlapping {
		if now.Sub(entry.lastFlap).Seconds() > b.fastFlapExpiry {
			delete(b.fastFlapping, ip)
			continue
		}
		if entry.flaps > b.fastFlapGrace {
			count++
		}
	}
	return count
}

// incomingGate returns the connection-accept gate bound to this interface,
// returning true to accept a remote IP and false to reject a blocked one. The
// TCPServerInterface accept path consults it before spawning a client
// (BackboneInterface.py:397,420-435, v1.3.9).
func (b *BackboneInterface) incomingGate() func(string) bool {
	if b == nil {
		return nil
	}
	return func(remoteIP string) bool { return !b.isBlocked(remoteIP) }
}

// BlockedIPList returns the registry keys (every IP that has flapped and not
// been purged), mirroring Python's blocked_ip_list property
// (BackboneInterface.py:532-534, v1.4.0): when blocking is disabled it returns
// an empty slice; otherwise it returns the raw registry keys without purging,
// so the list contains every IP that has ever flapped within the expiry window,
// not just those currently over the grace threshold.
func (b *BackboneInterface) BlockedIPList() []string {
	if b == nil {
		return nil
	}
	b.fastFlappingMu.Lock()
	defer b.fastFlappingMu.Unlock()
	if !b.blockFastFlapping {
		return nil
	}
	out := make([]string, 0, len(b.fastFlapping))
	for ip := range b.fastFlapping {
		out = append(out, ip)
	}
	return out
}

// now returns the current time via the installed clock. Callers must hold
// fastFlappingMu, since nowFn is guarded by it.
func (b *BackboneInterface) now() time.Time {
	if b.nowFn != nil {
		return b.nowFn()
	}
	return time.Now()
}

// BackboneInterface reduces the per-spawned-peer ingress-control burst state
// into aggregate cached properties (BackboneInterface.py:173-225), so the
// Backbone server reports a burst as active when ANY spawned client is in a
// burst, and the activation time as the EARLIEST (min) activation among the
// burst-active spawned clients. Each aggregate is cached for 2 seconds to
// avoid scanning the spawned list on every read (the same TTL Python uses).

const backboneAggregateCacheTTL = 2 * time.Second

// icBurstActiveAt is the time-injectable core of ICBurstActive. It recomputes
// the any-reduction over the spawned peers when the cache is older than 2s,
// otherwise returns the cached state (BackboneInterface.py:174-180).
func (b *BackboneInterface) icBurstActiveAt(now time.Time) bool {
	if b == nil || b.TCPServerInterface == nil {
		return false
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICBurstCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICBurstCheck = now
		b.lastICBurstState = false
		for _, peer := range b.snapshotSpawned() {
			if peer.ICBurstActive() {
				b.lastICBurstState = true
				break
			}
		}
	}
	return b.lastICBurstState
}

// ICBurstActive reports whether any spawned client is currently in an
// announce-burst (BackboneInterface.py:174-180).
func (b *BackboneInterface) ICBurstActive() bool {
	return b.icBurstActiveAt(time.Now())
}

// icBurstActivatedAt is the time-injectable core of ICBurstActivated. It
// recomputes the min activation time over the burst-active spawned peers when
// the cache is older than 2s, otherwise returns the cached value
// (BackboneInterface.py:186-194). With no burst-active peers the cached value
// stays at the zero time (Python's 0).
func (b *BackboneInterface) icBurstActivatedAt(now time.Time) time.Time {
	if b == nil || b.TCPServerInterface == nil {
		return time.Time{}
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICBurstActivatedCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICBurstActivatedCheck = now
		b.lastICBurstActivated = time.Time{}
		for _, peer := range b.snapshotSpawned() {
			if peer.ICBurstActive() {
				if b.lastICBurstActivated.IsZero() || peer.ICBurstActivated().Before(b.lastICBurstActivated) {
					b.lastICBurstActivated = peer.ICBurstActivated()
				}
			}
		}
	}
	return b.lastICBurstActivated
}

// ICBurstActivated reports the earliest activation time among the burst-active
// spawned clients (BackboneInterface.py:186-194).
func (b *BackboneInterface) ICBurstActivated() time.Time {
	return b.icBurstActivatedAt(time.Now())
}

// icPrBurstActiveAt is the time-injectable core of ICPrBurstActive, the
// path-request-burst any-reduction (BackboneInterface.py:202-208).
func (b *BackboneInterface) icPrBurstActiveAt(now time.Time) bool {
	if b == nil || b.TCPServerInterface == nil {
		return false
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICPrBurstCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICPrBurstCheck = now
		b.lastICPrBurstState = false
		for _, peer := range b.snapshotSpawned() {
			if peer.ICPrBurstActive() {
				b.lastICPrBurstState = true
				break
			}
		}
	}
	return b.lastICPrBurstState
}

// ICPrBurstActive reports whether any spawned client is currently in a
// path-request burst (BackboneInterface.py:202-208).
func (b *BackboneInterface) ICPrBurstActive() bool {
	return b.icPrBurstActiveAt(time.Now())
}

// icPrBurstActivatedAt is the time-injectable core of ICPrBurstActivated, the
// path-request-burst min activation time (BackboneInterface.py:214-222).
func (b *BackboneInterface) icPrBurstActivatedAt(now time.Time) time.Time {
	if b == nil || b.TCPServerInterface == nil {
		return time.Time{}
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICPrBurstActivatedCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICPrBurstActivatedCheck = now
		b.lastICPrBurstActivated = time.Time{}
		for _, peer := range b.snapshotSpawned() {
			if peer.ICPrBurstActive() {
				if b.lastICPrBurstActivated.IsZero() || peer.ICPrBurstActivated().Before(b.lastICPrBurstActivated) {
					b.lastICPrBurstActivated = peer.ICPrBurstActivated()
				}
			}
		}
	}
	return b.lastICPrBurstActivated
}

// ICPrBurstActivated reports the earliest activation time among the
// path-request-burst-active spawned clients (BackboneInterface.py:214-222).
func (b *BackboneInterface) ICPrBurstActivated() time.Time {
	return b.icPrBurstActivatedAt(time.Now())
}

// snapshotSpawned returns a copy of the spawned-interfaces list under the
// TCPServerInterface lock, so the aggregate reductions see a consistent view
// even as peers connect/disconnect.
func (b *BackboneInterface) snapshotSpawned() []*TCPClientInterface {
	if b == nil || b.TCPServerInterface == nil {
		return nil
	}
	b.TCPServerInterface.mu.Lock()
	defer b.TCPServerInterface.mu.Unlock()
	out := make([]*TCPClientInterface, len(b.spawnedInterfaces))
	copy(out, b.spawnedInterfaces)
	return out
}

// BackboneClientInterface represents an outbound TCP session that connects to
// a remote BackboneInterface listener, providing reliable point-to-point
// delivery to core network nodes.
type BackboneClientInterface struct {
	*TCPClientInterface
}

// NewBackboneClientInterface initiates a TCP connection to the target host and
// registers the inbound payload handler to process server-side data. The
// client uses BackboneHWMTU for inbound HDLC frame-length validation.
func NewBackboneClientInterface(name, targetHost string, targetPort int, handler InboundHandler) (Interface, error) {
	inner, err := newTCPClientInterface(name, targetHost, targetPort, false, handler, BackboneHWMTU)
	if err != nil {
		return nil, err
	}
	return &BackboneClientInterface{TCPClientInterface: inner}, nil
}

// NewDormantBackboneClientInterface returns an unconnected Backbone client used
// for discovery records that Python registers without an initial target.
func NewDormantBackboneClientInterface(name string, handler InboundHandler) Interface {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	bi.setDefaultIFACSize(BackboneDefaultIFACSize)
	return &BackboneClientInterface{
		TCPClientInterface: &TCPClientInterface{
			BaseInterface:  bi,
			inboundHandler: handler,
			hwmtu:          BackboneHWMTU,
		},
	}
}

// Type returns the string "BackboneClientInterface" as the runtime type name.
func (b *BackboneClientInterface) Type() string { return "BackboneClientInterface" }

// HashString reproduces Python BackboneClientInterface.__str__
// (RNS/Interfaces/BackboneInterface.py:869-873), which Interface.get_hash
// hashes:
//
//	"BackboneInterface["+name+"/"+ip_str(target_ip)+":"+str(target_port)+"]"
//
// Note the prefix is "BackboneInterface", NOT "BackboneClientInterface" —
// this shadows the embedded TCPClientInterface.HashString
// ("TCPInterface[...]"), which would otherwise never match hashes in a
// Python-written destination_table. A server-spawned client uses the peer's
// remote address, like Python (BackboneInterface.py: spawned handler sets
// target_ip/target_port from client_address).
func (b *BackboneClientInterface) HashString() string {
	host := b.targetHost
	port := b.targetPort
	if b.spawned {
		host = b.remoteIP
		port = b.remotePort
	}
	return "BackboneInterface[" + b.Name() + "/" + tcpHostPort(host, port) + "]"
}
