// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math"
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
	ResourceStatusRejected = 0x00
)

const (
	// ResourceAdvOverhead defines the byte overhead size for a resource advertisement packet.
	ResourceAdvOverhead = 134
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
		switch n := v.(type) {
		case int:
			return int64(n), true
		case int8:
			return int64(n), true
		case int16:
			return int64(n), true
		case int32:
			return int64(n), true
		case int64:
			return n, true
		case uint:
			return int64(n), true
		case uint8:
			return int64(n), true
		case uint16:
			return int64(n), true
		case uint32:
			return int64(n), true
		case uint64:
			return int64(n), true
		default:
			return 0, false
		}
	}

	// Helper to extract values from map
	getVal := func(key string) any {
		return m[key]
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
	if v := getVal("h"); v != nil {
		adv.H = v.([]byte)
	}
	if v := getVal("r"); v != nil {
		adv.R = v.([]byte)
	}
	if v := getVal("o"); v != nil {
		adv.O = v.([]byte)
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
	if v := getVal("q"); v != nil {
		adv.Q = v.([]byte)
	}
	if v := getVal("f"); v != nil {
		if n, ok := toInt64(v); ok {
			adv.F = byte(n)
		}
	}
	if v := getVal("m"); v != nil {
		adv.M = v.([]byte)
	}

	adv.Encrypted = (adv.F & 0x01) != 0
	adv.Compressed = (adv.F & 0x02) != 0
	adv.Split = (adv.F & 0x04) != 0
	adv.IsRequest = (adv.F & 0x08) != 0
	adv.IsResponse = (adv.F & 0x10) != 0
	adv.HasMetadata = (adv.F & 0x20) != 0

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
		link:             l,
		initiator:        false,
		status:           ResourceStatusTransferring,
		size:             adv.T,
		uncompressedSize: adv.D,
		totalSize:        adv.D,
		hash:             adv.H,
		randomHash:       adv.R,
		originalHash:     adv.O,
		totalParts:       adv.N,
		callback:         callback,
		progressCallback: progressCallback,
		requestID:        copyBytes(adv.Q),
		isResponse:       adv.IsResponse,
		encrypted:        adv.Encrypted,
		compressed:       adv.Compressed,
		lastActivity:     time.Now(),
		window:           4,
		windowMax:        10,
		windowMin:        2,
		hasMetadata:      adv.HasMetadata,
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

	window    int
	windowMax int
	windowMin int

	lastActivity time.Time

	callback         func(*Resource)
	progressCallback func(*Resource)
	requestID        []byte
	isResponse       bool
	encrypted        bool
	compressed       bool
	sentParts        int
	metadata         map[string][]byte
	hasMetadata      bool

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
		link:             link,
		initiator:        true,
		uncompressedData: data,
		status:           ResourceStatusQueued,
		window:           4,
		windowMax:        10,
		windowMin:        2,
		metadata:         opts.Metadata,
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

	// Segment data into parts
	sdu := link.mdu
	if sdu <= 0 {
		sdu = MDU
	}

	r.totalParts = int(math.Ceil(float64(r.size) / float64(sdu)))
	r.parts = make([]*ResourcePart, r.totalParts)
	r.hashmap = make([][]byte, r.totalParts)

	for i := 0; i < r.totalParts; i++ {
		start := i * sdu
		end := (i + 1) * sdu
		if end > int(r.size) {
			end = int(r.size)
		}

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

// Cancel prematurely terminates the resource transfer and updates its status to failed.
func (r *Resource) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = ResourceStatusFailed
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
func (r *Resource) SetProgressCallback(cb func(*Resource)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progressCallback = cb
}

// RequestNext triggers a network request for the next optimal batch of missing data parts on an incoming transfer.
func (r *Resource) RequestNext() error {
	r.mu.Lock()
	defer r.mu.Unlock()

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

	r.lastActivity = time.Now()
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
	if r.callback != nil {
		go r.callback(r)
	}
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
		decompressed, err := DecompressBzip2(payload)
		if err != nil {
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

// Advertise broadcasts a resource advertisement over the link to notify the remote peer of an impending transfer.
func (r *Resource) Advertise() error {
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
		return err
	}

	p := NewPacket(r.link, data)
	p.PacketType = PacketData
	p.Context = ContextResourceAdv

	r.link.mu.Lock()
	r.link.outgoingResources = append(r.link.outgoingResources, r)
	r.link.mu.Unlock()

	return p.Send()
}
