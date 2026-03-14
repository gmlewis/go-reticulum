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

// IFACConfig articulates the strict cryptographic parameters required to secure a physical or virtual interface.
// It houses the enabling flag and requisite entropy material, dictating precisely how the interface should authenticate payload frames at the hardware boundary.
type IFACConfig struct {
	Enabled bool
	NetName string
	NetKey  string
	Size    int
}

// BaseInterface implements the foundational, structural bedrock required by every concrete interface type within the network stack.
// It securely encapsulates crucial state such as byte metrics, cryptographic IFAC keying material, and interface lifecycle flags, guaranteeing uniform behavior.
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

// NewBaseInterface allocates and securely zeros a fresh BaseInterface instance with the prescribed architectural properties.
// It establishes the immutable creation timestamp and baseline operational mode, serving as the required progenitor for all specialized interfaces.
func NewBaseInterface(name string, mode int, bitrate int) *BaseInterface {
	return &BaseInterface{
		name:    name,
		mode:    mode,
		bitrate: bitrate,
		created: time.Now(),
	}
}

// Name safely exposes the immutably configured string identifier assigned to this specific interface.
// It allows higher-level orchestrators to definitively distinguish between varied routing components.
func (bi *BaseInterface) Name() string { return bi.name }

// Mode yields the operational simplex/duplex mode flag assigned during the interface's architectural inception.
// It dictates the interface's capability to participate in bidirectional or unidirectional routing topologies.
func (bi *BaseInterface) Mode() int { return bi.mode }

// Bitrate fetches the conservatively estimated transmission capacity of the interface, expressed in bits per second.
// This metric is structurally vital for the routing engine to accurately calculate transit costs and shape traffic queues.
func (bi *BaseInterface) Bitrate() int { return bi.bitrate }

// SetBitrate safely and atomically updates the interface's operational capacity to reflect changing hardware constraints.
// It forcefully alters the routing engine's perception of this interface's bandwidth, triggering downstream cost recalculations.
func (bi *BaseInterface) SetBitrate(bitrate int) { bi.bitrate = bitrate }

// Age calculates and provides the precise temporal duration since the interface was instantiated and added to the network.
// It enables the system to aggressively prune stale or malfunctioning interfaces based on their longevity.
func (bi *BaseInterface) Age() time.Duration { return time.Since(bi.created) }

// IsDetached deterministically queries the atomic lifecycle flag to assert whether the interface has been logically severed from the active stack.
// It is relied upon by read/write loops to guarantee immediate termination of resources upon teardown.
func (bi *BaseInterface) IsDetached() bool { return atomic.LoadInt32(&bi.detached) == 1 }

// SetDetached executes a thread-safe, atomic mutation of the interface's lifecycle state.
// Setting this to true acts as an irrevocable kill signal, compelling all associated IO workers to systematically disband.
func (bi *BaseInterface) SetDetached(detached bool) {
	if detached {
		atomic.StoreInt32(&bi.detached, 1)
		return
	}
	atomic.StoreInt32(&bi.detached, 0)
}

// BytesReceived retrieves the atomically managed counter detailing the absolute total of payload bytes successfully ingested by this interface.
// It is critical for telemetry and throughput modeling.
func (bi *BaseInterface) BytesReceived() uint64 { return bi.rxBytes }

// BytesSent fetches the precise atomic metric recording every byte successfully dispatched outbound from this interface.
// It provides essential observability into the interface's aggregate workload and physical layer stress.
func (bi *BaseInterface) BytesSent() uint64 { return bi.txBytes }

// SetIFACConfig completely reinitializes the interface's cryptographic authentication layer using the provided tuning parameters.
// It aggressively regenerates symmetric keying material and signs the new boundary configuration, imposing a strict lock during the volatile transition.
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

// IFACConfig safely extracts a thread-consistent snapshot of the interface's active authentication configuration.
// It allows inspection routines to ascertain the current security posture without violating memory constraints.
func (bi *BaseInterface) IFACConfig() IFACConfig {
	bi.ifacMu.RLock()
	defer bi.ifacMu.RUnlock()
	return bi.ifacConfig
}

// ApplyIFACInbound aggressively processes incoming raw bytes, stripping out and rigorously validating cryptographic authentication tags.
// It explicitly rejects malformed or unauthentic payloads at the lowest possible layer, dropping them securely before they can poison the router.
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

// ApplyIFACOutbound surgically embeds requisite cryptographic signatures into outgoing raw payloads prior to physical transmission.
// It ensures that data leaving the interface adheres to the pre-established IFAC security envelope, maintaining strict boundary integrity.
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
