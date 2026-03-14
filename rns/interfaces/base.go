// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"sync"
	"sync/atomic"
	"time"

	rnscrypto "github.com/gmlewis/go-reticulum/rns/crypto"
)

// IFACConfig describes the cryptographic parameters required to secure a physical
// or virtual interface. It contains an enable flag and entropy material used to
// authenticate payload frames at the hardware boundary.
type IFACConfig struct {
	Enabled bool
	NetName string
	NetKey  string
	Size    int
}

// BaseInterface implements the foundational structure used by all concrete
// interface types. It encapsulates state such as byte metrics, IFAC keying
// material, and lifecycle flags to guarantee consistent behavior across
// interface implementations.
type BaseInterface struct {
	name     string
	mode     int
	bitrate  int
	created  time.Time
	detached int32

	rxBytes uint64
	txBytes uint64
	ifacMu  sync.RWMutex

	ifacConfig IFACConfig
	ifacKey    []byte
	ifacSigner *rnscrypto.Ed25519PrivateKey
}

// NewBaseInterface allocates and initializes a BaseInterface with the given
// name, mode, and bitrate. It records a creation timestamp and provides the
// baseline state required by specialized interfaces.
func NewBaseInterface(name string, mode int, bitrate int) *BaseInterface {
	return &BaseInterface{
		name:    name,
		mode:    mode,
		bitrate: bitrate,
		created: time.Now(),
	}
}

// Name returns the immutably configured identifier assigned to this interface.
// It allows higher-level orchestrators to distinguish between different routing
// components.
func (bi *BaseInterface) Name() string { return bi.name }

// Mode returns the operational simplex/duplex mode flag for this interface.
// It indicates whether the interface can participate in bidirectional or
// unidirectional routing topologies.
func (bi *BaseInterface) Mode() int { return bi.mode }

// Bitrate returns the estimated transmission capacity of the interface in bits
// per second. The routing engine uses this metric to calculate transit costs and
// shape traffic queues.
func (bi *BaseInterface) Bitrate() int { return bi.bitrate }

// SetBitrate atomically updates the interface's operational bitrate to reflect
// changing hardware constraints. Updating this value influences routing cost
// calculations downstream.
func (bi *BaseInterface) SetBitrate(bitrate int) { bi.bitrate = bitrate }

// Age returns the duration since the interface was created and added to the
// network. It is used to identify and prune stale or malfunctioning
// interfaces.
func (bi *BaseInterface) Age() time.Duration { return time.Since(bi.created) }

// IsDetached returns true if the interface has been logically severed from the
// active stack. Readers and writers use this flag to terminate work and release
// resources.
func (bi *BaseInterface) IsDetached() bool { return atomic.LoadInt32(&bi.detached) == 1 }

// SetDetached atomically updates the interface lifecycle flag. Setting this to
// true signals workers to stop and release resources.
func (bi *BaseInterface) SetDetached(detached bool) {
	if detached {
		atomic.StoreInt32(&bi.detached, 1)
		return
	}
	atomic.StoreInt32(&bi.detached, 0)
}

// BytesReceived returns the atomically managed counter of payload bytes
// ingested by this interface. It is used for telemetry and throughput modeling.
func (bi *BaseInterface) BytesReceived() uint64 { return bi.rxBytes }

// BytesSent returns the atomic metric recording bytes dispatched by this
// interface. It provides observability into the interface's workload.
func (bi *BaseInterface) BytesSent() uint64 { return bi.txBytes }

// SetIFACConfig reinitializes the interface's cryptographic authentication
// layer using the provided parameters. It regenerates keying material and
// updates signing state while holding a lock for thread safety.
func (bi *BaseInterface) SetIFACConfig(cfg IFACConfig) {
	bi.ifacMu.Lock()
	defer bi.ifacMu.Unlock()

	bi.ifacConfig = cfg
	bi.ifacKey = nil
	bi.ifacSigner = nil

	if !cfg.Enabled {
		return
	}

	if bi.ifacConfig.Size < 1 {
		bi.ifacConfig.Size = 16
	}

	origin := make([]byte, 0, 64)
	if cfg.NetName != "" {
		origin = append(origin, rnscrypto.SHA256([]byte(cfg.NetName))...)
	}
	if cfg.NetKey != "" {
		origin = append(origin, rnscrypto.SHA256([]byte(cfg.NetKey))...)
	}
	if len(origin) == 0 {
		bi.ifacConfig.Enabled = false
		return
	}

	originHash := rnscrypto.SHA256(origin)
	ifacKey, err := rnscrypto.HKDF(64, originHash, ifacSalt, nil)
	if err != nil || len(ifacKey) != 64 {
		bi.ifacConfig.Enabled = false
		return
	}

	signer, err := rnscrypto.NewEd25519PrivateKeyFromBytes(ifacKey[32:])
	if err != nil {
		bi.ifacConfig.Enabled = false
		return
	}

	bi.ifacKey = ifacKey
	bi.ifacSigner = signer
}

// IFACConfig returns a thread-consistent snapshot of the interface's active
// authentication configuration. It enables inspection without violating memory
// safety.
func (bi *BaseInterface) IFACConfig() IFACConfig {
	bi.ifacMu.RLock()
	defer bi.ifacMu.RUnlock()
	return bi.ifacConfig
}

// ApplyIFACInbound processes incoming raw bytes and validates cryptographic
// authentication tags. Malformed or unauthentic payloads are rejected at the
// lowest possible layer.
func (bi *BaseInterface) ApplyIFACInbound(data []byte) ([]byte, bool) {
	if len(data) <= 2 {
		return nil, false
	}

	bi.ifacMu.RLock()
	ifacConfig := bi.ifacConfig
	ifacSigner := bi.ifacSigner
	ifacKey := make([]byte, len(bi.ifacKey))
	copy(ifacKey, bi.ifacKey)
	bi.ifacMu.RUnlock()

	ifacEnabled := ifacConfig.Enabled && ifacSigner != nil && len(ifacKey) == 64
	hasIFACFlag := (data[0] & 0x80) == 0x80

	if !ifacEnabled {
		if hasIFACFlag {
			return nil, false
		}
		out := make([]byte, len(data))
		copy(out, data)
		return out, true
	}

	if !hasIFACFlag {
		return nil, false
	}

	ifacSize := ifacConfig.Size
	if len(data) <= 2+ifacSize {
		return nil, false
	}

	ifac := make([]byte, ifacSize)
	copy(ifac, data[2:2+ifacSize])

	mask, err := rnscrypto.HKDF(len(data), ifac, ifacKey, nil)
	if err != nil {
		return nil, false
	}

	unmasked := make([]byte, len(data))
	for i := range len(data) {
		if i <= 1 || i > ifacSize+1 {
			unmasked[i] = data[i] ^ mask[i]
		} else {
			unmasked[i] = data[i]
		}
	}

	newRaw := make([]byte, 0, len(data)-ifacSize)
	newRaw = append(newRaw, unmasked[0]&0x7f, unmasked[1])
	newRaw = append(newRaw, unmasked[2+ifacSize:]...)

	sig := ifacSigner.Sign(newRaw)
	expectedIFAC := sig[len(sig)-ifacSize:]
	if !bytes.Equal(ifac, expectedIFAC) {
		return nil, false
	}

	return newRaw, true
}

// ApplyIFACOutbound embeds cryptographic signatures into outgoing payloads
// before physical transmission. It ensures outgoing data adheres to the IFAC
// security envelope.
func (bi *BaseInterface) ApplyIFACOutbound(data []byte) ([]byte, error) {
	bi.ifacMu.RLock()
	ifacConfig := bi.ifacConfig
	ifacSigner := bi.ifacSigner
	ifacKey := make([]byte, len(bi.ifacKey))
	copy(ifacKey, bi.ifacKey)
	bi.ifacMu.RUnlock()

	if len(data) <= 2 || !ifacConfig.Enabled || ifacSigner == nil || len(ifacKey) != 64 {
		out := make([]byte, len(data))
		copy(out, data)
		return out, nil
	}

	ifacSize := ifacConfig.Size
	sig := ifacSigner.Sign(data)
	ifac := make([]byte, ifacSize)
	copy(ifac, sig[len(sig)-ifacSize:])

	mask, err := rnscrypto.HKDF(len(data)+ifacSize, ifac, ifacKey, nil)
	if err != nil {
		return nil, err
	}

	newRaw := make([]byte, 0, len(data)+ifacSize)
	newRaw = append(newRaw, data[0]|0x80, data[1])
	newRaw = append(newRaw, ifac...)
	newRaw = append(newRaw, data[2:]...)

	masked := make([]byte, len(newRaw))
	for i := range len(newRaw) {
		if i == 0 {
			masked[i] = (newRaw[i] ^ mask[i]) | 0x80
		} else if i == 1 || i > ifacSize+1 {
			masked[i] = newRaw[i] ^ mask[i]
		} else {
			masked[i] = newRaw[i]
		}
	}

	return masked, nil
}

var ifacSalt = []byte{
	0xad, 0xf5, 0x4d, 0x88, 0x2c, 0x9a, 0x9b, 0x80,
	0x77, 0x1e, 0xb4, 0x99, 0x5d, 0x70, 0x2d, 0x4a,
	0x3e, 0x73, 0x33, 0x91, 0xb2, 0xa0, 0xf5, 0x3f,
	0x41, 0x6d, 0x9f, 0x90, 0x7e, 0x55, 0xcf, 0xf8,
}
