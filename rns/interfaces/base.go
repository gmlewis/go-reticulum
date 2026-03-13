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

// IFAC stands for Interface Authentication and Control.
// IFACConfig holds per-interface authentication configuration.
// This is currently used for configuration plumbing and transport hook wiring.
type IFACConfig struct {
	Enabled bool
	NetName string
	NetKey  string
	Size    int
}

// BaseInterface provides common functionality for all interfaces.
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

// NewBaseInterface creates a new BaseInterface instance.
func NewBaseInterface(name string, mode int, bitrate int) *BaseInterface {
	return &BaseInterface{
		name:    name,
		mode:    mode,
		bitrate: bitrate,
		created: time.Now(),
	}
}

// Name returns the interface name.
func (bi *BaseInterface) Name() string { return bi.name }

// Mode returns the interface mode.
func (bi *BaseInterface) Mode() int { return bi.mode }

// Bitrate returns the interface bitrate.
func (bi *BaseInterface) Bitrate() int { return bi.bitrate }

// SetBitrate updates interface bitrate.
func (bi *BaseInterface) SetBitrate(bitrate int) { bi.bitrate = bitrate }

// Age returns the time since the interface was created.
func (bi *BaseInterface) Age() time.Duration { return time.Since(bi.created) }

// IsDetached returns true if the interface has been detached.
func (bi *BaseInterface) IsDetached() bool { return atomic.LoadInt32(&bi.detached) == 1 }

// SetDetached updates detached lifecycle state.
func (bi *BaseInterface) SetDetached(detached bool) {
	if detached {
		atomic.StoreInt32(&bi.detached, 1)
		return
	}
	atomic.StoreInt32(&bi.detached, 0)
}

// BytesReceived returns the total number of bytes received.
func (bi *BaseInterface) BytesReceived() uint64 { return bi.rxBytes }

// BytesSent returns the total number of bytes sent.
func (bi *BaseInterface) BytesSent() uint64 { return bi.txBytes }

// SetIFACConfig updates IFAC configuration for an interface.
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

// IFACConfig returns the current IFAC configuration.
func (bi *BaseInterface) IFACConfig() IFACConfig {
	bi.ifacMu.RLock()
	defer bi.ifacMu.RUnlock()
	return bi.ifacConfig
}

// ApplyIFACInbound processes ingress bytes before packet decoding.
// Current implementation is passthrough and serves as a hook point.
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

// ApplyIFACOutbound processes egress bytes before transmission.
// Current implementation is passthrough and serves as a hook point.
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
