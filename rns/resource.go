// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	vendoredbzip2 "github.com/gmlewis/go-reticulum/compress/bzip2"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

const (
	// ResourceMapHashLen specifies the length of the map hash used for verifying resource parts.
	ResourceMapHashLen = 4
	// ResourceRandomHashSize specifies the length of the random hash used to uniquely identify a resource transfer.
	ResourceRandomHashSize = 4
	// ResourceAutoCompressMaxSize sets the upper limit on data size before auto-compression is skipped.
	ResourceAutoCompressMaxSize = 64 * 1024 * 1024
	// ResourceMaxParts is a defensive backstop on the number of parts a single
	// resource advertisement may declare. It prevents a hostile or buggy peer
	// from driving make([]*ResourcePart, n) into a makeslice panic (negative n)
	// or an unrecoverable out-of-memory throw (absurd n) before any part is
	// received. Legitimate transfers are gated by the size/consistency check
	// (part count cannot exceed total size in bytes, since each part carries at
	// least one byte), so this cap is only a last-resort bound.
	ResourceMaxParts = 10_000_000
)

// ResourceOptions configures optional behavior for new resource transmissions, such as compression and metadata.
type ResourceOptions struct {
	// AutoCompress determines if the data should be automatically compressed before transmission.
	AutoCompress bool
	// AutoCompressLimit sets the maximum byte size for data to be eligible for auto-compression.
	AutoCompressLimit int
	// CompressionLevel sets the algorithm-specific compression level.
	CompressionLevel int
	// Metadata contains optional metadata to be sent with the resource advertisement.
	Metadata map[string][]byte
}

func (o ResourceOptions) normalized() ResourceOptions {
	norm := o
	if norm.AutoCompressLimit <= 0 {
		norm.AutoCompressLimit = ResourceAutoCompressMaxSize
	}
	if norm.CompressionLevel == 0 {
		norm.CompressionLevel = vendoredbzip2.DefaultCompression
	}
	return norm
}

const (
	// ResourceStatusNone indicates an uninitialized or reset resource state.
	ResourceStatusNone = 0x00
	// ResourceStatusQueued indicates the resource is prepared but transmission hasn't started.
	ResourceStatusQueued = 0x01
	// ResourceStatusAdvertised indicates an advertisement has been sent, awaiting acceptance.
	ResourceStatusAdvertised = 0x02
	// ResourceStatusTransferring indicates parts are currently being exchanged.
	ResourceStatusTransferring = 0x03
	// ResourceStatusAwaitingProof indicates all parts were sent, awaiting final delivery proof.
	ResourceStatusAwaitingProof = 0x04
	// ResourceStatusAssembling indicates the resource is currently piecing together received parts.
	ResourceStatusAssembling = 0x05
	// ResourceStatusComplete indicates the resource transfer succeeded and data is ready.
	ResourceStatusComplete = 0x06
	// ResourceStatusFailed indicates the transfer failed or timed out.
	ResourceStatusFailed = 0x07
	// ResourceStatusCorrupt indicates the assembled data failed hash verification.
	ResourceStatusCorrupt = 0x08
	// ResourceStatusRejected indicates the receiver actively declined the resource.
	// It matches Python Resource.REJECTED (Resource.py:152); the value 0x09 sits
	// above Corrupt (0x08) as a terminal state and is distinct from None (0x00).
	ResourceStatusRejected = 0x09
)

const (
	// ResourceAdvOverhead defines the byte overhead size for a resource advertisement packet.
	ResourceAdvOverhead = 134
)

// Resource watchdog timing/state constants. These mirror the class-level
// constants on Python's RNS.Resource (Resource.py:97-137) and drive the
// transfer-loss-recovery state machine in WatchdogJob / watchdogStep. Golden
// values captured from a live `import RNS` run (rns 1.3.5); see
// TestResourceConstants.
const (
	// ResourceProcessingGrace is the extra grace added to the advertisement
	// timeout before the ADVERTISED-state watchdog resends (seconds).
	ResourceProcessingGrace = 1.0
	// ResourceRetryGraceTime is the base grace added to receiver part-timeout
	// computations (seconds).
	ResourceRetryGraceTime = 0.25
	// ResourcePerRetryDelay is the per-retry delay added to the receiver
	// part-timeout, accumulating with retries_used (seconds).
	ResourcePerRetryDelay = 0.5
	// ResourceWatchdogMaxSleep caps a single watchdog sleep (seconds).
	ResourceWatchdogMaxSleep = 1.0
	// ResourceProofTimeoutFactor scales rtt when waiting for the final
	// resource proof in the AWAITING_PROOF state (proof packets are small).
	ResourceProofTimeoutFactor = 3.0
	// ResourcePartTimeoutFactor scales the expected time-of-flight remaining
	// when waiting for outstanding parts (receiver, before RTT is measured).
	ResourcePartTimeoutFactor = 4.0
	// ResourcePartTimeoutFactorAfterRtt replaces PartTimeoutFactor once the
	// request/response RTT rate has been measured.
	ResourcePartTimeoutFactorAfterRtt = 2.0
	// ResourceSenderGraceTime is the grace added to sender-side timeouts
	// (seconds).
	ResourceSenderGraceTime = 10.0
	// ResourceHmuWaitFactor scales the expected hashmap-update wait when the
	// receiver is waiting for an HMU or has no outstanding parts.
	ResourceHmuWaitFactor = 3.5
	// ResourceMaxRetries is the maximum number of receiver part-request
	// retries before cancelling a transfer.
	ResourceMaxRetries = 16
	// ResourceMaxAdvRetries is the maximum number of advertisement resends
	// before cancelling an ADVERTISED resource.
	ResourceMaxAdvRetries = 4
	// ResourceWindowFlexibility is the minimum allowed spread between
	// windowMax and window; the watchdog never lets the gap shrink below this.
	ResourceWindowFlexibility = 4
)

// ResourceAdvertisement represents the payload of a resource advertisement packet, carrying metadata needed to initiate a transfer.
type ResourceAdvertisement struct {
	T int64  `msgpack:"t"` // Transfer size
	D int64  `msgpack:"d"` // Data size
	N int    `msgpack:"n"` // Number of parts
	H []byte `msgpack:"h"` // Resource hash
	R []byte `msgpack:"r"` // Resource random hash
	O []byte `msgpack:"o"` // Original hash
	I int    `msgpack:"i"` // Segment index
	L int    `msgpack:"l"` // Total segments
	Q []byte `msgpack:"q"` // Request ID
	F byte   `msgpack:"f"` // Resource flags
	M []byte `msgpack:"m"` // Resource hashmap

	// Decoded flags
	Encrypted   bool
	Compressed  bool
	Split       bool
	IsRequest   bool
	IsResponse  bool
	HasMetadata bool
}

// Pack serializes the ResourceAdvertisement into a compact MessagePack format suitable for network transmission.
func (adv *ResourceAdvertisement) Pack() ([]byte, error) {
	// Encode flags
	adv.F = 0
	if adv.Encrypted {
		adv.F |= 0x01
	}
	if adv.Compressed {
		adv.F |= 0x02
	}
	if adv.Split {
		adv.F |= 0x04
	}
	if adv.IsRequest {
		adv.F |= 0x08
	}
	if adv.IsResponse {
		adv.F |= 0x10
	}
	if adv.HasMetadata {
		adv.F |= 0x20
	}

	m := map[string]any{
		"t": adv.T,
		"d": adv.D,
		"n": adv.N,
		"h": adv.H,
		"r": adv.R,
		"o": adv.O,
		"i": adv.I,
		"l": adv.L,
		"q": adv.Q,
		"f": adv.F,
		"m": adv.M,
	}
	return msgpack.Pack(m)
}

// UnpackResourceAdvertisement deserializes a raw MessagePack byte slice into a structured ResourceAdvertisement.
func UnpackResourceAdvertisement(data []byte) (*ResourceAdvertisement, error) {
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return nil, err
	}

	m, ok := unpacked.(map[any]any)
	if !ok {
		return nil, fmt.Errorf("invalid resource advertisement format")
	}

	adv := &ResourceAdvertisement{}
	toInt64 := func(v any) (int64, bool) {
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return rv.Int(), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return int64(rv.Uint()), true
		default:
			return 0, false
		}
	}

	// Helper to extract values from map
	getVal := func(key string) any {
		return m[key]
	}

	// getBytes extracts a byte-slice field, returning an error if the field is
	// present but not a msgpack bin. A malicious or buggy peer can send a
	// str-typed value for h/r/o/q/m, which would otherwise panic on a bare
	// v.([]byte) type assertion.
	getBytes := func(key string) ([]byte, error) {
		v := getVal(key)
		if v == nil {
			return nil, nil
		}
		b, ok := v.([]byte)
		if !ok {
			return nil, fmt.Errorf("invalid resource advertisement: field %q is not bytes", key)
		}
		return b, nil
	}

	if v := getVal("t"); v != nil {
		if n, ok := toInt64(v); ok {
			adv.T = n
		}
	}
	if v := getVal("d"); v != nil {
		if n, ok := toInt64(v); ok {
			adv.D = n
		}
	}
	if v := getVal("n"); v != nil {
		if n, ok := toInt64(v); ok {
			adv.N = int(n)
		}
	}
	if adv.H, err = getBytes("h"); err != nil {
		return nil, err
	}
	if adv.R, err = getBytes("r"); err != nil {
		return nil, err
	}
	if adv.O, err = getBytes("o"); err != nil {
		return nil, err
	}
	if v := getVal("i"); v != nil {
		if n, ok := toInt64(v); ok {
			adv.I = int(n)
		}
	}
	if v := getVal("l"); v != nil {
		if n, ok := toInt64(v); ok {
			adv.L = int(n)
		}
	}
	if adv.Q, err = getBytes("q"); err != nil {
		return nil, err
	}
	if v := getVal("f"); v != nil {
		if n, ok := toInt64(v); ok {
			adv.F = byte(n)
		}
	}
	if adv.M, err = getBytes("m"); err != nil {
		return nil, err
	}

	adv.Encrypted = (adv.F & 0x01) != 0
	adv.Compressed = (adv.F & 0x02) != 0
	adv.Split = (adv.F & 0x04) != 0
	adv.IsRequest = (adv.F & 0x08) != 0
	adv.IsResponse = (adv.F & 0x10) != 0
	adv.HasMetadata = (adv.F & 0x20) != 0

	// Validate the part count before any caller feeds adv.N into make(). A
	// negative count panics makeslice; an absurd count fatal-throws OOM; and a
	// count exceeding the declared total size is structurally impossible
	// (each part carries at least one byte), indicating a malformed/hostile
	// advertisement. Reject here so Accept, Reject, and every other caller are
	// covered uniformly.
	if adv.N < 0 || uint64(adv.N) > ResourceMaxParts {
		return nil, fmt.Errorf("invalid resource advertisement: part count %d out of range", adv.N)
	}
	if adv.T > 0 && int64(adv.N) > adv.T {
		return nil, fmt.Errorf("invalid resource advertisement: part count %d exceeds total size %d", adv.N, adv.T)
	}

	return adv, nil
}

// Reject gracefully declines an incoming resource advertisement, informing the sender that the transfer will not proceed.
func Reject(packet *Packet) error {
	adv, err := UnpackResourceAdvertisement(packet.Data)
	if err != nil {
		return err
	}

	l, ok := packet.Destination.(*Link)
	if !ok {
		return fmt.Errorf("packet destination is not a link")
	}

	rejectPacket := NewPacket(l, adv.H)
	rejectPacket.Context = ContextResourceRcl
	return rejectPacket.Send()
}

// Accept accepts an incoming resource advertisement and begins the process of sequentially requesting and receiving its data parts.
func Accept(packet *Packet, callback func(*Resource), startedCallback func(*Resource), progressCallback func(*Resource)) (*Resource, error) {
	adv, err := UnpackResourceAdvertisement(packet.Data)
	if err != nil {
		return nil, err
	}

	l, ok := packet.Destination.(*Link)
	if !ok {
		return nil, fmt.Errorf("packet destination is not a link")
	}

	r := &Resource{
		link:              l,
		initiator:         false,
		status:            ResourceStatusTransferring,
		size:              adv.T,
		uncompressedSize:  adv.D,
		totalSize:         adv.D,
		hash:              adv.H,
		randomHash:        adv.R,
		originalHash:      adv.O,
		totalParts:        adv.N,
		callback:          callback,
		progressCallback:  progressCallback,
		requestID:         copyBytes(adv.Q),
		isResponse:        adv.IsResponse,
		encrypted:         adv.Encrypted,
		compressed:        adv.Compressed,
		lastActivity:      time.Now(),
		window:            4,
		windowMax:         10,
		windowMin:         2,
		windowFlexibility: ResourceWindowFlexibility,
		hasMetadata:       adv.HasMetadata,

		// Watchdog / loss-recovery state (Python Resource.accept
		// Resource.py:172-197 + __init__ Resource.py:341-365).
		maxRetries:          ResourceMaxRetries,
		maxAdvRetries:       ResourceMaxAdvRetries,
		retriesLeft:         ResourceMaxRetries,
		timeoutFactor:       l.trafficTimeoutFactor,
		timeout:             l.rtt * l.trafficTimeoutFactor,
		partTimeoutFactor:   ResourcePartTimeoutFactor,
		senderGraceTime:     ResourceSenderGraceTime,
		outstandingParts:    0,
		waitingForHmu:       false,
		advertisementPacket: packet,
	}
	// Mirror Python Resource.accept (Resource.py line 196):
	// started_transferring = last_activity = time.time().
	r.startedTransferring = r.lastActivity

	// Derive sdu as in Resource.__init__ (Resource.py:337-340).
	if l.mtu != 0 {
		r.sdu = l.mtu - HeaderMaxSize - IFACMinSize
	} else if l.mdu > 0 {
		r.sdu = l.mdu
	} else {
		r.sdu = MDU
	}

	r.parts = make([]*ResourcePart, r.totalParts)
	r.hashmap = make([][]byte, r.totalParts)
	for i := 0; i < r.totalParts; i++ {
		r.parts[i] = &ResourcePart{Index: i}
	}

	for i := 0; i+ResourceMapHashLen <= len(adv.M) && (i/ResourceMapHashLen) < r.totalParts; i += ResourceMapHashLen {
		idx := i / ResourceMapHashLen
		mh := copyBytes(adv.M[i : i+ResourceMapHashLen])
		r.hashmap[idx] = mh
		r.parts[idx].MapHash = mh
	}

	l.mu.Lock()
	l.incomingResources = append(l.incomingResources, r)
	l.mu.Unlock()

	r.link.logger.Debug("Accepted resource advertisement for %x", r.hash)

	if startedCallback != nil {
		go startedCallback(r)
	}

	go func() {
		if err := r.RequestNext(); err != nil {
			r.link.logger.Debug("Failed to request initial resource parts: %v", err)
		}
	}()

	// Mirror Python Resource.accept (Resource.py:234): launch the watchdog
	// job so receiver-side loss recovery (part-request retry, window
	// shrink, cancel on timeout) runs for the lifetime of the transfer.
	r.WatchdogJob()

	return r, nil
}

// Resource manages the state, sequencing, and reliable transmission of arbitrary amounts of data over a given link.
type Resource struct {
	link             *Link
	initiator        bool
	data             []byte
	uncompressedData []byte
	hash             []byte
	expectedProof    []byte
	randomHash       []byte
	originalHash     []byte
	status           int

	size             int64
	totalSize        int64
	uncompressedSize int64

	parts         []*ResourcePart
	hashmap       [][]byte
	totalParts    int
	receivedCount int

	window            int
	windowMax         int
	windowMin         int
	windowFlexibility int

	lastActivity        time.Time
	startedTransferring time.Time
	advSent             time.Time
	lastPartSent        time.Time

	// Watchdog / loss-recovery state. Mirrors Python Resource.__init__
	// (Resource.py:335-365) and accept (Resource.py:167-239). rtt/eifr/
	// previousEifr use pointers so nil faithfully models Python None.
	maxRetries          int
	maxAdvRetries       int
	retriesLeft         int
	timeoutFactor       float64
	timeout             float64
	partTimeoutFactor   float64
	senderGraceTime     float64
	sdu                 int
	outstandingParts    int
	waitingForHmu       bool
	reqRespRttRate      float64
	reqDataRttRate      float64
	rtt                 *float64
	eifr                *float64
	previousEifr        *float64
	watchdogJobID       int
	advertisementPacket *Packet
	watchdogOnce        sync.Once
	watchdogStop        chan struct{}
	watchdogDone        chan struct{}

	callback         func(*Resource)
	progressCallback func(*Resource)
	requestID        []byte
	isResponse       bool
	encrypted        bool
	compressed       bool
	sentParts        int
	metadata         map[string][]byte
	hasMetadata      bool

	// nextSegment is the next segment in a multi-segment resource transfer,
	// mirroring Python Resource.next_segment (Resource.py:255). It is prepared
	// asynchronously during the current segment's transfer and advertised once
	// the current segment completes. Cancel recurses into it (Resource.py:1087)
	// and the progress callback cascades to it (Resource.py:1137). Nil for a
	// single-segment resource.
	nextSegment *Resource

	// maxDecompressedSize caps the decompressed size of a compressed incoming
	// resource (Python Resource.max_decompressed_size, Resource.py:360). It
	// defaults to ResourceAutoCompressMaxSize (64 MiB) when unset, driving the
	// decompression-bomb guard in Assemble.
	maxDecompressedSize int

	mu sync.Mutex
}

// ResourcePart encapsulates a single chunk of data within a larger resource transfer, tracking its unique hash and transmission status.
type ResourcePart struct {
	Data         []byte // Original data for outgoing
	ReceivedData []byte // Data received for incoming
	Hash         []byte
	MapHash      []byte
	Index        int
	Sent         bool
}

// NewResource initializes a new resource transfer for the provided data over the specified link using default options.
func NewResource(data []byte, link *Link) (*Resource, error) {
	return NewResourceWithOptions(data, link, ResourceOptions{})
}

// NewResourceWithOptions initializes a new resource transfer, allowing explicit configuration of parameters like compression policy.
func NewResourceWithOptions(data []byte, link *Link, opts ResourceOptions) (*Resource, error) {
	return newResourceWithOptions(data, link, opts, rand.Read)
}

func newResourceWithOptions(data []byte, link *Link, opts ResourceOptions, randRead func([]byte) (int, error)) (*Resource, error) {
	if link.status != LinkActive {
		return nil, fmt.Errorf("link is not active")
	}
	if randRead == nil {
		randRead = rand.Read
	}

	r := &Resource{
		link:              link,
		initiator:         true,
		uncompressedData:  data,
		status:            ResourceStatusQueued,
		window:            4,
		windowMax:         10,
		windowMin:         2,
		windowFlexibility: ResourceWindowFlexibility,
		metadata:          opts.Metadata,

		// Watchdog / loss-recovery state (Python Resource.__init__
		// Resource.py:341-365).
		maxRetries:        ResourceMaxRetries,
		maxAdvRetries:     ResourceMaxAdvRetries,
		retriesLeft:       ResourceMaxRetries,
		timeoutFactor:     link.trafficTimeoutFactor,
		timeout:           link.rtt * link.trafficTimeoutFactor,
		partTimeoutFactor: ResourcePartTimeoutFactor,
		senderGraceTime:   ResourceSenderGraceTime,
	}

	normOpts := opts.normalized()
	payload := data
	r.uncompressedSize = int64(len(data))
	r.totalSize = r.uncompressedSize
	r.compressed = false
	if normOpts.AutoCompress && len(data) <= normOpts.AutoCompressLimit {
		compressedPayload, err := CompressBzip2(data, normOpts.CompressionLevel)
		if err != nil {
			return nil, fmt.Errorf("failed to compress resource payload: %w", err)
		}
		if len(compressedPayload) < len(data) {
			payload = compressedPayload
			r.compressed = true
		}
	}

	r.randomHash = make([]byte, ResourceRandomHashSize)
	if _, err := randRead(r.randomHash); err != nil {
		return nil, fmt.Errorf("failed to generate random hash for resource: %w", err)
	}

	hashMaterial := make([]byte, 0, len(data)+len(r.randomHash))
	hashMaterial = append(hashMaterial, data...)
	hashMaterial = append(hashMaterial, r.randomHash...)
	r.hash = FullHash(hashMaterial)
	r.expectedProof = FullHash(append(copyBytes(data), r.hash...))
	r.originalHash = r.hash

	// Handle metadata: pack and prepend to payload
	var metadataBytes []byte
	if len(opts.Metadata) > 0 {
		packedMetadata, err := msgpack.Pack(opts.Metadata)
		if err != nil {
			return nil, fmt.Errorf("failed to pack metadata: %w", err)
		}
		metadataSize := len(packedMetadata)
		if metadataSize > 0xFFFFFF {
			return nil, fmt.Errorf("metadata size exceeds maximum")
		}
		// 3-byte big-endian size (drop first byte of 4-byte int)
		metadataBytes = make([]byte, 3+metadataSize)
		metadataBytes[0] = byte((metadataSize >> 16) & 0xFF)
		metadataBytes[1] = byte((metadataSize >> 8) & 0xFF)
		metadataBytes[2] = byte(metadataSize & 0xFF)
		copy(metadataBytes[3:], packedMetadata)
		r.totalSize = int64(len(metadataBytes)) + int64(len(payload))
	}

	resourcePlaintext := make([]byte, 0, len(r.randomHash)+len(metadataBytes)+len(payload))
	resourcePlaintext = append(resourcePlaintext, r.randomHash...)
	if metadataBytes != nil {
		resourcePlaintext = append(resourcePlaintext, metadataBytes...)
	}
	resourcePlaintext = append(resourcePlaintext, payload...)

	encryptedStream, err := link.Encrypt(resourcePlaintext)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt resource stream: %w", err)
	}
	r.data = encryptedStream
	r.encrypted = true
	r.size = int64(len(r.data))

	// Segment data into parts. Python Resource.__init__ (Resource.py:
	// 337-340) derives sdu from the link mtu when set, else from mdu/SDU.
	sdu := link.mdu
	if link.mtu != 0 {
		sdu = link.mtu - HeaderMaxSize - IFACMinSize
	}
	if sdu <= 0 {
		sdu = MDU
	}
	r.sdu = sdu

	r.totalParts = int(math.Ceil(float64(r.size) / float64(sdu)))
	r.parts = make([]*ResourcePart, r.totalParts)
	r.hashmap = make([][]byte, r.totalParts)

	for i := 0; i < r.totalParts; i++ {
		start := i * sdu
		end := min((i+1)*sdu, int(r.size))

		partData := r.data[start:end]
		r.parts[i] = &ResourcePart{
			Data:    partData,
			Index:   i,
			MapHash: r.getMapHash(partData),
		}
		r.hashmap[i] = r.parts[i].MapHash
	}

	return r, nil
}

func (r *Resource) getMapHash(data []byte) []byte {
	hashMaterial := make([]byte, 0, len(data)+len(r.randomHash))
	hashMaterial = append(hashMaterial, data...)
	hashMaterial = append(hashMaterial, r.randomHash...)
	return FullHash(hashMaterial)[:ResourceMapHashLen]
}

// Hash returns the unique cryptographic identifier of the entire resource data payload.
func (r *Resource) Hash() []byte {
	return r.hash
}

// Status retrieves the current lifecycle state of the resource transfer.
func (r *Resource) Status() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Data provides a full copy of the internally assembled and verified payload data.
func (r *Resource) Data() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return copyBytes(r.data)
}

// GetProgress calculates the transfer progress as a float value spanning from 0.0 to 1.0.
// This represents the logical/application layer progress (percentage of uncompressed data assembled).
func (r *Resource) GetProgress() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.totalParts == 0 {
		return 0.0
	}
	if r.initiator {
		return float64(r.sentParts) / float64(r.totalParts)
	}
	return float64(r.receivedCount) / float64(r.totalParts)
}

// GetSegmentProgress calculates the physical layer transfer progress as a float value spanning from 0.0 to 1.0.
// This represents the percentage of encrypted segments actually transferred over the wire.
// For initiators (senders), it tracks sent parts; for receivers, it tracks received parts.
func (r *Resource) GetSegmentProgress() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.totalParts == 0 {
		return 0.0
	}
	if r.initiator {
		return float64(r.sentParts) / float64(r.totalParts)
	}
	return float64(r.receivedCount) / float64(r.totalParts)
}

// TotalSize yields the cumulative byte size of the resource as transmitted over the network.
func (r *Resource) TotalSize() int64 {
	return r.size
}

// Cancel prematurely terminates the resource transfer. It is the Go port of
// Python Resource.cancel (Resource.py:1084-1118). The pending next segment is
// recursively cancelled first (Resource.py:1087-1088), then:
//   - On CORRUPT status: cancel_incoming_resource + reject(advertisement) +
//     link.teardown (Resource.py:1090-1093).
//   - On any status below COMPLETE: the resource is marked FAILED and, when
//     the link is active, the initiator sends a RESOURCE_ICL packet while the
//     receiver sends a RESOURCE_RCL packet; the resource is removed from the
//     link's outgoing (initiator) or incoming (receiver) list; the conclude
//     callback (resource_concluded, i.e. recordExpectedRate) and the user
//     callback fire (Resource.py:1095-1118).
//
// A resource already at or above COMPLETE is left untouched. The cancel-context
// packet and the link-list removal happen outside r.mu (after the status flip),
// matching Python's lock-free cancel and preserving the r.mu -> link.mu lock
// ordering (cancel_outgoing/incoming and Teardown acquire link.mu).
func (r *Resource) Cancel() {
	r.mu.Lock()
	next := r.nextSegment
	r.mu.Unlock()
	// Recurse into the pending next segment first, mirroring Python
	// (Resource.py:1087-1088), so the cascade prevents any subsequent
	// segment from being advertised.
	if next != nil {
		next.Cancel()
	}

	r.mu.Lock()
	if r.status == ResourceStatusCorrupt {
		// CORRUPT branch (Resource.py:1090-1093). cancelCorruptLocked
		// assumes r.mu is held.
		r.cancelCorruptLocked()
		r.mu.Unlock()
		r.stopWatchdog()
		return
	}
	if r.status >= ResourceStatusComplete {
		// Already complete: Python's cancel has no branch for
		// COMPLETE-or-above, so leave the resource as-is.
		r.mu.Unlock()
		r.stopWatchdog()
		return
	}
	// FAILED branch (Resource.py:1095-1118).
	r.status = ResourceStatusFailed
	initiator := r.initiator
	link := r.link
	hash := append([]byte(nil), r.hash...)
	cb := r.callback
	r.mu.Unlock()

	// Send the cancel-context packet when the link is active. The
	// initiator sends RESOURCE_ICL; the receiver sends RESOURCE_RCL.
	if link != nil && link.GetStatus() == LinkActive {
		cancelPacket := NewPacket(link, hash)
		if initiator {
			cancelPacket.Context = ContextResourceIcl
		} else {
			cancelPacket.Context = ContextResourceRcl
		}
		if err := cancelPacket.Send(); err != nil {
			if link.logger != nil {
				link.logger.Debug("Could not send resource cancel packet for %x: %v", hash, err)
			}
		}
	}
	// Remove the resource from the link's outgoing/incoming list
	// (Python cancel_outgoing_resource / cancel_incoming_resource).
	if link != nil {
		if initiator {
			link.CancelOutgoingResource(r)
		} else {
			link.CancelIncomingResource(r)
		}
	}
	// Fire the conclude callback (Python link.resource_concluded, whose
	// expected-rate update is recordExpectedRate; its list removal is
	// already done above) and the user callback (Resource.py:1115-1118).
	if cb != nil {
		r.recordExpectedRate()
		go cb(r)
	}
	// Promptly tear down the background watchdog loop rather than waiting
	// for its next step to notice the terminal status.
	r.stopWatchdog()
}

// cancelLocked is the lock-held core of Cancel. It mirrors Python
// Resource.cancel (Resource.py:1086-1087): a resource below COMPLETE is
// marked FAILED; already-complete/corrupt resources are left as-is.
func (r *Resource) cancelLocked() {
	if r.status < ResourceStatusComplete {
		r.status = ResourceStatusFailed
	}
}

// cancelCorruptLocked runs the CORRUPT branch of Python Resource.cancel
// (Resource.py:1090-1093): cancel_incoming_resource + reject(advertisement) +
// link.teardown. It assumes r.mu is already held (the caller — currently
// Assemble's decompression-bomb guard — holds it) and that r.status has
// already been set to ResourceStatusCorrupt. The full Cancel rewrite
// (ICL/RCL packets, FAILED branch) is a separate Phase 9 task.
func (r *Resource) cancelCorruptLocked() {
	if r.link != nil {
		r.link.CancelIncomingResource(r)
	}
	if r.advertisementPacket != nil {
		if err := Reject(r.advertisementPacket); err != nil {
			if r.link != nil && r.link.logger != nil {
				r.link.logger.Debug("Failed to send resource reject for %x: %v", r.hash, err)
			}
		}
	}
	if r.link != nil {
		r.link.Teardown()
	}
}

// Metadata returns the metadata associated with this resource.
func (r *Resource) Metadata() map[string][]byte {
	return r.metadata
}

// SetRequestID sets the request ID for this resource response.
func (r *Resource) SetRequestID(requestID []byte) {
	r.requestID = copyBytes(requestID)
}

// SetResponse marks this resource as a response to a request.
func (r *Resource) SetResponse(isResponse bool) {
	r.isResponse = isResponse
}

// SetCallback registers a function to execute when the resource transfer achieves completion or fails permanently.
func (r *Resource) SetCallback(cb func(*Resource)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callback = cb
}

// SetProgressCallback registers a function to execute periodically as parts of the resource are successively delivered.
// It is the Go port of Python Resource.progress_callback (Resource.py:1136-
// 1138): the callback is stored on this resource and, when a next segment
// exists, forwarded to it recursively — so a multi-segment transfer reports
// progress from every segment via the single callback installed on the head.
// (The prepare-time propagation — setting the callback on nextSegment as it is
// created, Python Resource.py:785 — is handled by the same cascade once the
// segment-splitting machinery wires nextSegment during preparation.)
func (r *Resource) SetProgressCallback(cb func(*Resource)) {
	r.mu.Lock()
	r.progressCallback = cb
	next := r.nextSegment
	r.mu.Unlock()
	// Cascade to the pending next segment so every segment in the chain
	// reports progress through the one callback (Python Resource.py:1137).
	if next != nil {
		next.SetProgressCallback(cb)
	}
}

// RequestNext triggers a network request for the next optimal batch of missing data parts on an incoming transfer.
func (r *Resource) RequestNext() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requestNextLockedAt(time.Now())
}

// requestNextLockedAt is RequestNext's core assuming r.mu is already held,
// so the watchdog (which holds r.mu) can re-request parts without
// re-locking. now replaces time.time() so the watchdog's injected clock
// stays deterministic (Python request_next sets last_activity = time.time(),
// Resource.py:485).
func (r *Resource) requestNextLockedAt(now time.Time) error {
	if r.status == ResourceStatusFailed {
		return fmt.Errorf("resource transfer failed")
	}

	if r.initiator {
		return nil
	}

	if r.receivedCount >= r.totalParts {
		return nil
	}

	requestedHashes := make([]byte, 0, r.window*ResourceMapHashLen)
	requestedParts := 0
	for i := 0; i < r.totalParts && requestedParts < r.window; i++ {
		if r.parts[i] == nil || r.parts[i].ReceivedData != nil {
			continue
		}
		if len(r.hashmap[i]) != ResourceMapHashLen {
			continue
		}
		requestedHashes = append(requestedHashes, r.hashmap[i]...)
		requestedParts++
	}

	if len(requestedHashes) == 0 {
		return nil
	}

	requestData := make([]byte, 0, 1+len(r.hash)+len(requestedHashes))
	requestData = append(requestData, 0x00)
	requestData = append(requestData, r.hash...)
	requestData = append(requestData, requestedHashes...)

	p := NewPacket(r.link, requestData)
	p.Context = ContextResourceReq
	if err := p.Send(); err != nil {
		return err
	}

	r.lastActivity = now
	return nil
}

// Request processes an inbound packet requesting specific missing data parts and dispatches them directly over the link.
func (r *Resource) Request(requestData []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == ResourceStatusFailed {
		return fmt.Errorf("resource transfer failed")
	}

	if len(requestData) < 1 {
		return fmt.Errorf("invalid resource request packet")
	}

	offset := 1
	if requestData[0] == 0xFF {
		offset += ResourceMapHashLen
	}

	if len(requestData) < offset+len(r.hash) {
		return fmt.Errorf("resource request packet too short")
	}

	resourceHash := requestData[offset : offset+len(r.hash)]
	if !bytes.Equal(resourceHash, r.hash) {
		return fmt.Errorf("resource hash mismatch in request")
	}

	requestedHashes := requestData[offset+len(r.hash):]
	for i := 0; i+ResourceMapHashLen <= len(requestedHashes); i += ResourceMapHashLen {
		mapHash := requestedHashes[i : i+ResourceMapHashLen]
		for _, part := range r.parts {
			if !bytes.Equal(part.MapHash, mapHash) {
				continue
			}
			p := NewPacket(r.link, part.Data)
			p.Context = ContextResource
			if err := p.Send(); err != nil {
				return err
			}
			if !part.Sent {
				part.Sent = true
				r.sentParts++
			}
			break
		}
	}

	if r.sentParts >= r.totalParts {
		r.status = ResourceStatusAwaitingProof
	}

	return nil
}

// ValidateProof verifies an incoming cryptographic proof of delivery for an outgoing resource transfer, marking it as complete on success.
func (r *Resource) ValidateProof(proofData []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == ResourceStatusFailed || r.status == ResourceStatusComplete {
		return
	}

	hashLen := len(r.hash)
	if hashLen == 0 || len(proofData) != hashLen*2 {
		r.status = ResourceStatusFailed
		if r.callback != nil {
			go r.callback(r)
		}
		return
	}

	proofHash := proofData[:hashLen]
	proof := proofData[hashLen:]
	if !bytes.Equal(proofHash, r.hash) || !bytes.Equal(proof, r.expectedProof) {
		r.status = ResourceStatusFailed
		if r.callback != nil {
			go r.callback(r)
		}
		return
	}

	r.status = ResourceStatusComplete
	r.recordExpectedRate()
	if r.callback != nil {
		go r.callback(r)
	}
}

// updateEifr computes the Expected In-Flight Rate (eifr) and pushes it onto
// the link's expected_rate. It is the Go port of Python
// Resource.update_eifr (Resource.py:543-558). The precedence is:
//   - req_data_rtt_rate*8 when a measured data-RTT rate exists,
//   - else previous_eifr when a prior transfer's eifr is known,
//   - else link.establishment_cost*8 / rtt as a first-transfer estimate.
//
// rtt is the resource's own measured rtt if set, falling back to link.rtt.
func (r *Resource) updateEifr() {
	rtt := 0.0
	if r.rtt != nil {
		rtt = *r.rtt
	} else if r.link != nil {
		rtt = r.link.rtt
	}

	var eifr float64
	switch {
	case r.reqDataRttRate != 0:
		eifr = r.reqDataRttRate * 8
	case r.previousEifr != nil:
		eifr = *r.previousEifr
	case r.link != nil:
		eifr = r.link.establishmentCost * 8 / rtt
	}
	r.eifr = &eifr
	if r.link != nil {
		r.link.mu.Lock()
		r.link.expectedRate = eifr
		r.link.mu.Unlock()
	}
}

// line 1290): expected_rate = (resource.size*8) / max(concluded_at -
// started_transferring, 0.0001). It is invoked when an outgoing resource
// transfer is proven complete.
func (r *Resource) recordExpectedRate() {
	if r == nil || r.link == nil || r.size <= 0 || r.startedTransferring.IsZero() {
		return
	}
	elapsed := time.Since(r.startedTransferring).Seconds()
	if elapsed < 0.0001 {
		elapsed = 0.0001
	}
	r.link.mu.Lock()
	r.link.expectedRate = float64(r.size*8) / elapsed
	r.link.mu.Unlock()
}

// ReceivePart incorporates a newly arrived data part into the resource, triggering assembly if all parts have been accumulated.
func (r *Resource) ReceivePart(packet *Packet) error {
	r.mu.Lock()

	if r.status == ResourceStatusFailed {
		r.mu.Unlock()
		return fmt.Errorf("resource transfer failed")
	}

	r.status = ResourceStatusTransferring
	r.lastActivity = time.Now()

	partData := packet.Data
	partHash := r.getMapHash(partData)
	matched := false
	var progressCB func(*Resource)

	// Check if part matches any in our hashmap
	for i, mh := range r.hashmap {
		if bytes.Equal(mh, partHash) {
			matched = true
			if r.parts[i] != nil && r.parts[i].ReceivedData == nil {
				r.parts[i].ReceivedData = partData
				r.receivedCount++
				progressCB = r.progressCallback
			}
			break
		}
	}
	shouldAssemble := r.receivedCount == r.totalParts
	r.mu.Unlock()

	if progressCB != nil {
		progressCB(r)
	}

	if !matched {
		r.link.logger.Debug("Received resource part with unmatched maphash for %x", r.hash)
	}

	if shouldAssemble {
		r.link.logger.Debug("Received all %v resource parts for %x; assembling", r.totalParts, r.hash)
		go r.Assemble()
	} else {
		go func() {
			if err := r.RequestNext(); err != nil {
				r.link.logger.Debug("Failed to request next resource parts: %v", err)
			}
		}()
	}

	return nil
}

// Assemble reconstructs the original payload from received parts, verifies cryptographic integrity, decrypts, and decompresses as necessary.
func (r *Resource) Assemble() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.status = ResourceStatusAssembling

	var buf bytes.Buffer
	for _, p := range r.parts {
		if p == nil || p.ReceivedData == nil {
			r.status = ResourceStatusFailed
			return
		}
		buf.Write(p.ReceivedData)
	}

	assembled := buf.Bytes()
	if r.encrypted {
		plaintext, err := r.link.Decrypt(assembled)
		if err != nil {
			r.link.logger.Debug("Failed to decrypt assembled resource %x: %v", r.hash, err)
			r.status = ResourceStatusFailed
			return
		}
		assembled = plaintext
	}

	if len(assembled) < ResourceRandomHashSize {
		r.link.logger.Debug("Assembled resource %x too small to contain random hash", r.hash)
		r.status = ResourceStatusCorrupt
		return
	}

	rawPayload := assembled[ResourceRandomHashSize:]
	payload := rawPayload

	// Extract metadata if present (metadata is prepended to the data)
	if r.hasMetadata && len(rawPayload) >= 3 {
		metadataSize := int(rawPayload[0])<<16 | int(rawPayload[1])<<8 | int(rawPayload[2])
		if len(rawPayload) >= 3+metadataSize {
			packedMetadata := rawPayload[3 : 3+metadataSize]
			unpacked, err := msgpack.Unpack(packedMetadata)
			if err != nil {
				r.link.logger.Debug("Failed to unpack metadata: %v", err)
			} else {
				if m, ok := unpacked.(map[any]any); ok {
					r.metadata = make(map[string][]byte)
					for k, v := range m {
						if ks, ok := k.(string); ok {
							if vb, ok := v.([]byte); ok {
								r.metadata[ks] = vb
							}
						}
					}
				}
			}
			payload = rawPayload[3+metadataSize:]
		}
	}

	if r.compressed {
		maxLen := r.maxDecompressedSize
		if maxLen <= 0 {
			maxLen = ResourceAutoCompressMaxSize
		}
		decompressed, err := DecompressBzip2WithLimit(payload, maxLen)
		if err != nil {
			if errors.Is(err, ErrDecompressedTooLarge) {
				// Decompression-bomb guard (Python Resource.assemble,
				// Resource.py:690-696): the decompressed payload exceeded the
				// cap. Mark CORRUPT and run the CORRUPT branch of cancel —
				// cancel_incoming_resource + reject + link.teardown.
				if r.link != nil && r.link.logger != nil {
					r.link.logger.Error("Decompressed resource exceeded maximum decompressed size. The resource was rejected.")
				}
				r.status = ResourceStatusCorrupt
				r.cancelCorruptLocked()
				return
			}
			r.link.logger.Debug("Failed to decompress assembled resource %x: %v", r.hash, err)
			r.status = ResourceStatusFailed
			return
		}
		payload = decompressed
	}
	calculatedHash := FullHash(append(copyBytes(payload), r.randomHash...))
	if !bytes.Equal(calculatedHash, r.hash) {
		r.link.logger.Debug("Assembled resource %x failed payload hash validation", r.hash)
		r.status = ResourceStatusCorrupt
		return
	}

	r.data = copyBytes(payload)
	r.status = ResourceStatusComplete
	r.recordExpectedRate()
	if err := r.prove(); err != nil {
		r.link.logger.Debug("Failed to send resource proof for %x: %v", r.hash, err)
	} else {
		r.link.logger.Debug("Sent resource proof for %x", r.hash)
	}
	if r.callback != nil {
		go r.callback(r)
	}
}

func (r *Resource) prove() error {
	if r.link == nil || len(r.hash) == 0 {
		return fmt.Errorf("invalid resource proof state")
	}

	proofMaterial := make([]byte, 0, len(r.data)+len(r.hash))
	proofMaterial = append(proofMaterial, r.data...)
	proofMaterial = append(proofMaterial, r.hash...)
	proof := FullHash(proofMaterial)

	proofData := make([]byte, 0, len(r.hash)+len(proof))
	proofData = append(proofData, r.hash...)
	proofData = append(proofData, proof...)

	p := NewPacket(r.link, proofData)
	p.PacketType = PacketProof
	p.Context = ContextResourcePrf
	return p.Send()
}

// buildAdvertisementPacket constructs (without sending) the resource
// advertisement packet, mirroring the ResourceAdvertisement(self).pack()
// construction Python uses both in __advertise_job and in the ADVERTISED
// watchdog resend (Resource.py:521, 584).
func (r *Resource) buildAdvertisementPacket() (*Packet, error) {
	hashmapRaw := make([]byte, 0, len(r.hashmap)*ResourceMapHashLen)
	for _, mh := range r.hashmap {
		hashmapRaw = append(hashmapRaw, mh...)
	}

	adv := &ResourceAdvertisement{
		T:           r.size,
		D:           r.uncompressedSize,
		H:           r.hash,
		R:           r.randomHash,
		O:           r.hash, // Single segment for now
		N:           r.totalParts,
		L:           1, // Total segments
		I:           1, // Segment index
		Q:           r.requestID,
		M:           hashmapRaw,
		IsRequest:   r.requestID != nil && !r.isResponse,
		IsResponse:  r.requestID != nil && r.isResponse,
		Encrypted:   r.encrypted,
		Compressed:  r.compressed,
		HasMetadata: len(r.metadata) > 0,
	}

	data, err := adv.Pack()
	if err != nil {
		return nil, err
	}

	p := NewPacket(r.link, data)
	p.PacketType = PacketData
	p.Context = ContextResourceAdv
	return p, nil
}

// Advertise broadcasts a resource advertisement over the link to notify the remote peer of an impending transfer.
func (r *Resource) Advertise() error {
	p, err := r.buildAdvertisementPacket()
	if err != nil {
		return err
	}

	r.link.mu.Lock()
	r.link.outgoingResources = append(r.link.outgoingResources, r)
	// Mirror Python Resource.__advertise_job (Resource.py:527-534):
	// last_activity = started_transferring = adv_sent = time.time();
	// status = ADVERTISED; retries_left = max_adv_retries.
	now := time.Now()
	r.lastActivity = now
	r.startedTransferring = now
	r.advSent = now
	r.status = ResourceStatusAdvertised
	r.retriesLeft = r.maxAdvRetries
	r.advertisementPacket = p
	r.link.mu.Unlock()

	if err := p.Send(); err != nil {
		return err
	}

	// Mirror Python Resource.__advertise_job (Resource.py:541): start the
	// transfer-loss-recovery watchdog once the advertisement is on the wire.
	r.WatchdogJob()
	return nil
}

// GetDataSize returns the size in bytes of the resource's data
// payload. It is the Go port of Python's Resource.get_data_size().
func (r *Resource) GetDataSize() int {
	if r == nil {
		return 0
	}
	return len(r.data)
}

// GetParts returns the list of part hashes for the resource. It is
// the Go port of Python's Resource.get_parts().
func (r *Resource) GetParts() [][]byte {
	if r == nil {
		return nil
	}
	out := make([][]byte, len(r.parts))
	for i, p := range r.parts {
		if p == nil {
			continue
		}
		out[i] = append([]byte(nil), p.Hash...)
	}
	return out
}

// GetSegments returns the segment hashes for the resource. It is
// the Go port of Python's Resource.get_segments(). The Go port
// currently tracks segment hashes via ResourcePart and the
// per-resource map; we return the distinct hashes seen.
func (r *Resource) GetSegments() [][]byte {
	if r == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := [][]byte{}
	for _, p := range r.parts {
		if p == nil {
			continue
		}
		h := string(p.Hash)
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, append([]byte(nil), p.Hash...))
	}
	return out
}

// GetTransferSize returns the total transfer size of the resource.
// It is the Go port of Python's Resource.get_transfer_size().
func (r *Resource) GetTransferSize() int {
	if r == nil {
		return 0
	}
	return r.GetDataSize() + r.GetMetadataSize() + 16
}

// GetMetadataSize returns the cumulative size of all metadata
// entries in the resource, mirroring Python's resource-metadata
// accounting.
func (r *Resource) GetMetadataSize() int {
	if r == nil {
		return 0
	}
	total := 0
	for _, m := range r.metadata {
		total += len(m)
	}
	return total
}

// IsCompressed reports whether the resource's data was compressed
// before transfer. It is the Go port of Python's
// Resource.is_compressed().
func (r *Resource) IsCompressed() bool {
	if r == nil {
		return false
	}
	return r.compressed
}

// IsRequest reports whether the resource is a request (as opposed
// to a response). It is the Go port of Python's Resource.is_request().
func (r *Resource) IsRequest() bool {
	if r == nil {
		return false
	}
	return r.requestID != nil && !r.isResponse
}

// IsResponse reports whether the resource is a response. It is the
// Go port of Python's Resource.is_response().
func (r *Resource) IsResponse() bool {
	if r == nil {
		return false
	}
	return r.requestID != nil && r.isResponse
}

// currentRtt returns the resource's own measured rtt if set, falling back to
// the link's rtt (Python Resource.update_eifr / __watchdog_job use the same
// `self.rtt or self.link.rtt` resolution).
func (r *Resource) currentRtt() float64 {
	if r.rtt != nil {
		return *r.rtt
	}
	if r.link != nil {
		return r.link.rtt
	}
	return 0
}

// watchdogStep performs one iteration of the Python __watchdog_job loop
// (Resource.py:564-671) at instant now, returning the sleep duration (in
// seconds) the loop should wait before the next step and whether the loop
// should continue. It does not sleep. now replaces time.time() so golden
// tests can advance a virtual clock. It is the single-step analogue of the
// Go Link watchdog step.
//
// The four states mirror Python exactly:
//
//   - ADVERTISED: on adv_sent+timeout+PROCESSING_GRACE expiry, resend the
//     advertisement (decrementing retries_left from max_adv_retries) or cancel
//     when exhausted.
//   - TRANSFERRING (receiver): on part-timeout expiry, shrink the window and
//     re-request missing parts (request_next), or cancel when retries run out.
//   - TRANSFERRING (sender): on rtt*timeout_factor*max_retries +
//     sender_grace_time + max_extra_wait expiry, cancel.
//   - AWAITING_PROOF: on last_part_sent+rtt*PROOF_TIMEOUT_FACTOR+
//     sender_grace_time expiry, re-query the network cache for the expected
//     proof (cache_request) or cancel when exhausted.
func (r *Resource) watchdogStep(now time.Time) (sleep float64, cont bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status >= ResourceStatusAssembling {
		return 0, false
	}

	var sleepTime float64
	set := false

	switch r.status {
	case ResourceStatusAdvertised:
		set = true
		deadline := r.advSent.Add(time.Duration((r.timeout + ResourceProcessingGrace) * float64(time.Second)))
		sleepTime = deadline.Sub(now).Seconds()
		if sleepTime < 0 {
			if r.retriesLeft <= 0 {
				r.cancelLocked()
				sleepTime = 0.001
			} else {
				r.retriesLeft--
				p, err := r.buildAdvertisementPacket()
				if err != nil {
					r.cancelLocked()
					sleepTime = 0.001
				} else {
					r.advertisementPacket = p
					if err := p.Send(); err != nil {
						r.cancelLocked()
						sleepTime = 0.001
					} else {
						r.lastActivity = now
						r.advSent = now
						sleepTime = 0.001
					}
				}
			}
		}

	case ResourceStatusTransferring:
		if !r.initiator {
			// Receiver branch (Resource.py:594-629).
			set = true
			retriesUsed := r.maxRetries - r.retriesLeft
			extraWait := float64(retriesUsed) * ResourcePerRetryDelay
			r.updateEifr()
			eifr := 0.0
			if r.eifr != nil {
				eifr = *r.eifr
			}
			var expectedHmuWaitRemaining, expectedTofRemaining float64
			if eifr != 0 {
				if r.waitingForHmu || r.outstandingParts == 0 {
					expectedHmuWaitRemaining = (float64(r.sdu) * 8 * ResourceHmuWaitFactor) / eifr
				}
				expectedTofRemaining = (float64(r.outstandingParts) * float64(r.sdu) * 8) / eifr
			}
			if r.reqRespRttRate != 0 {
				deadline := r.lastActivity.Add(time.Duration((r.partTimeoutFactor*expectedTofRemaining + expectedHmuWaitRemaining + ResourceRetryGraceTime + extraWait) * float64(time.Second)))
				sleepTime = deadline.Sub(now).Seconds()
			} else {
				term := 0.0
				if eifr != 0 {
					term = r.partTimeoutFactor * ((3 * float64(r.sdu)) / eifr)
				}
				deadline := r.lastActivity.Add(time.Duration((term + ResourceRetryGraceTime + extraWait) * float64(time.Second)))
				sleepTime = deadline.Sub(now).Seconds()
			}
			if sleepTime < 0 {
				if r.retriesLeft > 0 {
					if r.window > r.windowMin {
						r.window--
						if r.windowMax > r.windowMin {
							r.windowMax--
							if (r.windowMax - r.window) > (r.windowFlexibility - 1) {
								r.windowMax--
							}
						}
					}
					sleepTime = 0.001
					r.retriesLeft--
					r.waitingForHmu = false
					_ = r.requestNextLockedAt(now)
				} else {
					r.cancelLocked()
					sleepTime = 0.001
				}
			}
		} else {
			// Sender branch (Resource.py:630-637).
			set = true
			maxExtraWait := 0.0
			for i := 0; i < r.maxRetries; i++ {
				maxExtraWait += float64(i+1) * ResourcePerRetryDelay
			}
			rtt := r.currentRtt()
			maxWait := rtt*r.timeoutFactor*float64(r.maxRetries) + r.senderGraceTime + maxExtraWait
			deadline := r.lastActivity.Add(time.Duration(maxWait * float64(time.Second)))
			sleepTime = deadline.Sub(now).Seconds()
			if sleepTime < 0 {
				r.cancelLocked()
				sleepTime = 0.001
			}
		}

	case ResourceStatusAwaitingProof:
		// Resource.py:639-658.
		set = true
		r.timeoutFactor = ResourceProofTimeoutFactor
		rtt := r.currentRtt()
		deadline := r.lastPartSent.Add(time.Duration((rtt*r.timeoutFactor + r.senderGraceTime) * float64(time.Second)))
		sleepTime = deadline.Sub(now).Seconds()
		if sleepTime < 0 {
			if r.retriesLeft <= 0 {
				r.cancelLocked()
				sleepTime = 0.001
			} else {
				r.retriesLeft--
				expectedData := append(append([]byte(nil), r.hash...), r.expectedProof...)
				expectedProofPacket := NewPacket(r.link, expectedData)
				expectedProofPacket.PacketType = PacketProof
				expectedProofPacket.Context = ContextResourcePrf
				if err := expectedProofPacket.Pack(); err == nil {
					if r.link != nil && r.link.transport != nil {
						r.link.transport.CacheRequest(expectedProofPacket.PacketHash, r.link)
					}
				}
				r.lastPartSent = now
				sleepTime = 0.001
			}
		}

	case ResourceStatusRejected:
		set = true
		sleepTime = 0.001
	}

	if !set {
		// Python: no branch matched -> sleep_time stays None -> cancel.
		r.cancelLocked()
		return 0, false
	}
	if sleepTime < 0 {
		// Python post-chain guard: "Timing error, cancelling resource transfer."
		r.cancelLocked()
		return 0.001, false
	}
	if sleepTime > ResourceWatchdogMaxSleep {
		sleepTime = ResourceWatchdogMaxSleep
	}
	return sleepTime, r.status < ResourceStatusAssembling
}

// WatchdogJob is the Go port of Python's Resource.watchdog_job
// (Resource.py:560-562): it spawns (once per resource) the watchdog loop
// that drives watchdogStep until the resource reaches a terminal state
// (status >= ASSEMBLING). The single-step state machine is golden-tested by
// TestWatchdogAdvertised / WatchdogTransferringReceiver /
// WatchdogTransferringSender / WatchdogAwaitingProof; the wired loop is
// exercised end-to-end by the lossy-link recovery test.
func (r *Resource) WatchdogJob() {
	if r == nil {
		return
	}
	r.watchdogOnce.Do(func() {
		r.mu.Lock()
		r.watchdogJobID++
		r.watchdogStop = make(chan struct{})
		r.watchdogDone = make(chan struct{})
		stop := r.watchdogStop
		done := r.watchdogDone
		r.mu.Unlock()
		go r.watchdogLoop(stop, done)
	})
}

// watchdogLoop is the background __watchdog_job loop (Resource.py:564-671).
// It repeatedly runs one watchdogStep at the current instant, sleeps for the
// returned duration (capped at WatchdogMaxSleep), and stops when the step
// reports the resource is no longer active (status >= ASSEMBLING) or when the
// resource is torn down via stopWatchdog. The now instant uses wall time;
// golden tests drive watchdogStep directly with an injected clock.
func (r *Resource) watchdogLoop(stop, done chan struct{}) {
	defer close(done)
	for {
		sleep, cont := r.watchdogStep(time.Now())
		if !cont {
			return
		}
		if sleep <= 0 {
			sleep = 0.001
		}
		if sleep > ResourceWatchdogMaxSleep {
			sleep = ResourceWatchdogMaxSleep
		}
		dur := time.Duration(sleep * float64(time.Second))
		if dur <= 0 {
			dur = time.Millisecond
		}
		timer := time.NewTimer(dur)
		select {
		case <-timer.C:
		case <-stop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

// stopWatchdog signals the background watchdog loop (if started) to exit and
// blocks until it has done so, mirroring the effect of Python's job-id
// invalidation when a resource is torn down. Waiting for the goroutine to
// exit prevents callers from racing the watchdog's in-flight field writes
// (e.g. eifr) when reading resource state after teardown.
func (r *Resource) stopWatchdog() {
	r.mu.Lock()
	stop := r.watchdogStop
	done := r.watchdogDone
	r.mu.Unlock()
	if stop != nil {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}
	if done != nil {
		<-done
	}
}
