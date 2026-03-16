// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

const (
	// IdentityCurve specifies the elliptic curve standard used for Ephemeral Diffie-Hellman key exchanges within Reticulum.
	IdentityCurve = "Curve25519"
	// IdentityKeySize specifies the combined total size in bits of both the encryption and signing keypairs.
	IdentityKeySize = 256 * 2
)

// Identity encapsulates the fundamental cryptographic material representing a unique node or user within the Reticulum network.
type Identity struct {
	prv    *crypto.X25519PrivateKey
	pub    *crypto.X25519PublicKey
	sigPrv *crypto.Ed25519PrivateKey
	sigPub *crypto.Ed25519PublicKey

	Hash    []byte
	HexHash string
	AppData []byte
}

var (
	knownDestinations  = make(map[string][]any)
	knownRatchets      = make(map[string][]byte)
	identityMu         sync.Mutex
	currentStoragePath string
)

// Remember caches a newly discovered identity and its associated routing context in local ephemeral or persistent storage.
func Remember(packetHash, destHash, publicKey, appData []byte) {
	identityMu.Lock()
	knownDestinations[string(destHash)] = []any{
		float64(time.Now().UnixNano()) / 1e9,
		packetHash,
		publicKey,
		appData,
	}
	path := currentStoragePath
	identityMu.Unlock()

	if path != "" {
		SaveKnownDestinations(path)
	}
}

// RememberRatchet securely registers and optionally persists a forward-secrecy ratchet public key associated with a specific destination.
func RememberRatchet(destHash, ratchetPub []byte) {
	identityMu.Lock()
	destHashStr := string(destHash)
	if bytes.Equal(knownRatchets[destHashStr], ratchetPub) {
		identityMu.Unlock()
		return
	}
	knownRatchets[destHashStr] = ratchetPub
	path := currentStoragePath
	identityMu.Unlock()

	if path != "" {
		persistRatchet(path, destHash, ratchetPub)
	}
}

func persistRatchet(storagePath string, destHash, ratchetPub []byte) {
	ratchetDir := filepath.Join(storagePath, "ratchets")
	if err := os.MkdirAll(ratchetDir, 0700); err != nil {
		Logf("Failed to create ratchet directory: %v", LogError, false, err)
		return
	}

	hexHash := fmt.Sprintf("%x", destHash)
	outPath := filepath.Join(ratchetDir, hexHash+".out")
	finalPath := filepath.Join(ratchetDir, hexHash)

	ratchetData := map[string]any{
		"ratchet":  ratchetPub,
		"received": float64(time.Now().UnixNano()) / 1e9,
	}

	data, err := msgpack.Pack(ratchetData)
	if err != nil {
		Logf("Failed to pack ratchet data for %v: %v", LogError, false, hexHash, err)
		return
	}

	if err := os.WriteFile(outPath, data, 0600); err != nil {
		Logf("Failed to write ratchet file for %v: %v", LogError, false, hexHash, err)
		return
	}

	if err := os.Rename(outPath, finalPath); err != nil {
		Logf("Failed to finalize ratchet file for %v: %v", LogError, false, hexHash, err)
	}
}

// GetRatchet retrieves the most recently observed valid forward-secrecy ratchet public key for a known destination.
func GetRatchet(destHash []byte) []byte {
	identityMu.Lock()
	destHashStr := string(destHash)
	if pub, ok := knownRatchets[destHashStr]; ok {
		identityMu.Unlock()
		return pub
	}
	path := currentStoragePath
	identityMu.Unlock()

	if path == "" {
		return nil
	}

	// Try to load from storage
	hexHash := fmt.Sprintf("%x", destHash)
	ratchetPath := filepath.Join(path, "ratchets", hexHash)
	if _, err := os.Stat(ratchetPath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(ratchetPath)
	if err != nil {
		Logf("Failed to read ratchet file for %v: %v", LogError, false, hexHash, err)
		return nil
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		Logf("Failed to unpack ratchet data for %v: %v", LogError, false, hexHash, err)
		return nil
	}

	if m, ok := unpacked.(map[any]any); ok {
		ratchetPub := m["ratchet"].([]byte)
		received := m["received"].(float64)

		// Check expiry (30 days)
		if float64(time.Now().UnixNano())/1e9 < received+30*24*3600 {
			identityMu.Lock()
			knownRatchets[destHashStr] = ratchetPub
			identityMu.Unlock()
			return ratchetPub
		}
		// Expired
		if err := os.Remove(ratchetPath); err != nil && !os.IsNotExist(err) {
			Logf("Failed to remove expired ratchet file for %v: %v", LogError, false, hexHash, err)
		}
	}

	return nil
}

// CleanRatchets scans persistent storage and aggressively purges any expired forward-secrecy ratchet data.
func CleanRatchets() {
	identityMu.Lock()
	path := currentStoragePath
	identityMu.Unlock()

	if path == "" {
		return
	}

	ratchetDir := filepath.Join(path, "ratchets")
	entries, err := os.ReadDir(ratchetDir)
	if err != nil {
		if !os.IsNotExist(err) {
			Logf("Failed to read ratchet directory: %v", LogError, false, err)
		}
		return
	}

	now := float64(time.Now().UnixNano()) / 1e9
	expiry := float64(30 * 24 * 3600)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filePath := filepath.Join(ratchetDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		unpacked, err := msgpack.Unpack(data)
		if err != nil {
			if rmErr := os.Remove(filePath); rmErr != nil && !os.IsNotExist(rmErr) {
				Logf("Failed to remove invalid ratchet file %v: %v", LogError, false, filePath, rmErr)
			}
			continue
		}

		if m, ok := unpacked.(map[any]any); ok {
			received, _ := m["received"].(float64)
			if now > received+expiry {
				if rmErr := os.Remove(filePath); rmErr != nil && !os.IsNotExist(rmErr) {
					Logf("Failed to remove expired ratchet file %v: %v", LogError, false, filePath, rmErr)
				}
			}
		}
	}
}

// Recall searches for a known identity matching the given target hash.
// If fromIdentityHash is true, the hash is compared against identity hashes;
// otherwise, it is compared against destination hashes.
func Recall(targetHash []byte, fromIdentityHash bool, ts Transport) *Identity {
	identityMu.Lock()
	defer identityMu.Unlock()

	if fromIdentityHash {
		for _, data := range knownDestinations {
			pubKey := data[2].([]byte)
			if bytes.Equal(targetHash, TruncatedHash(pubKey)) {
				id, err := NewIdentity(false)
				if err != nil {
					Logf("Failed to create identity during recall: %v", LogError, false, err)
					return nil
				}
				if err := id.LoadPublicKey(pubKey); err != nil {
					Logf("Failed to load recalled public key: %v", LogError, false, err)
					return nil
				}
				if data[3] != nil {
					id.AppData = data[3].([]byte)
				}
				return id
			}
		}
		return nil
	}

	if data, ok := knownDestinations[string(targetHash)]; ok {
		pubKey := data[2].([]byte)
		id, err := NewIdentity(false)
		if err != nil {
			Logf("Failed to create identity during recall: %v", LogError, false, err)
			return nil
		}
		if err := id.LoadPublicKey(pubKey); err != nil {
			Logf("Failed to load recalled public key: %v", LogError, false, err)
			return nil
		}
		if data[3] != nil {
			id.AppData = data[3].([]byte)
		}
		return id
	}

	// Also check registered destinations in transport if provided
	if ts != nil {
		tsSys := ts.(*TransportSystem)
		for _, d := range tsSys.destinations {
			if bytes.Equal(targetHash, d.Hash) {
				id, err := NewIdentity(false)
				if err != nil {
					Logf("Failed to create identity during transport recall: %v", LogError, false, err)
					return nil
				}
				if err := id.LoadPublicKey(d.identity.GetPublicKey()); err != nil {
					Logf("Failed to load transport destination public key: %v", LogError, false, err)
					return nil
				}
				return id
			}
		}
	}

	return nil
}

// LoadKnownDestinations populates the local identity cache using serialized data retrieved from disk.
func LoadKnownDestinations(storagePath string) {
	identityMu.Lock()
	currentStoragePath = storagePath
	identityMu.Unlock()

	path := filepath.Join(storagePath, "known_destinations")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		Logf("Failed to read known destinations: %v", LogError, false, err)
		return
	}

	unpacked, err := Unpack(data)
	if err != nil {
		Logf("Failed to unpack known destinations: %v", LogError, false, err)
		return
	}

	if m, ok := unpacked.(map[any]any); ok {
		identityMu.Lock()
		for k, v := range m {
			knownDestinations[k.(string)] = v.([]any)
		}
		identityMu.Unlock()
		Logf("Loaded %v known destination from storage", LogVerbose, false, len(knownDestinations))
	}
}

// SaveKnownDestinations serializes and safely flushes the currently cached known network identities to persistent storage.
func SaveKnownDestinations(storagePath string) {
	path := filepath.Join(storagePath, "known_destinations")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		Logf("Failed to create known destinations directory: %v", LogError, false, err)
		return
	}

	identityMu.Lock()
	data, err := msgpack.Pack(knownDestinations)
	count := len(knownDestinations)
	identityMu.Unlock()

	if err != nil {
		Logf("Failed to pack known destinations: %v", LogError, false, err)
		return
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		Logf("Failed to save known destinations: %v", LogError, false, err)
		return
	}
	Logf("Saved %v known destinations to storage", LogDebug, false, count)
}

// NewIdentity allocates a new structural container, optionally auto-generating pristine cryptographic keys.
func NewIdentity(createKeys bool) (*Identity, error) {
	id := &Identity{}
	if createKeys {
		if err := id.CreateKeys(); err != nil {
			return nil, err
		}
	}
	return id, nil
}

// CreateKeys securely computes fresh, tightly coupled encryption and signature keypairs for this identity.
func (id *Identity) CreateKeys() error {
	prv, err := crypto.GenerateX25519PrivateKey()
	if err != nil {
		return err
	}
	id.prv = prv
	id.pub = prv.PublicKey()

	sigPrv, err := crypto.GenerateEd25519PrivateKey()
	if err != nil {
		return err
	}
	id.sigPrv = sigPrv
	id.sigPub = sigPrv.PublicKey()

	id.UpdateHashes()
	return nil
}

// UpdateHashes re-calculates the internal cryptographic hash values corresponding to the underlying public key.
func (id *Identity) UpdateHashes() {
	id.Hash = TruncatedHash(id.GetPublicKey())
	id.HexHash = fmt.Sprintf("%x", id.Hash)
}

// GetPublicKey extracts and concatenates the strict byte representations of the public encryption and signature keys.
func (id *Identity) GetPublicKey() []byte {
	if id.pub == nil || id.sigPub == nil {
		return nil
	}
	return append(id.pub.PublicBytes(), id.sigPub.PublicBytes()...)
}

// GetPrivateKey extracts and concatenates the strict byte representations of the private encryption and signature keys.
func (id *Identity) GetPrivateKey() []byte {
	if id.prv == nil || id.sigPrv == nil {
		return nil
	}
	return append(id.prv.PrivateBytes(), id.sigPrv.PrivateBytes()...)
}

// FromBytes creates a new Identity from private key bytes, matching
// Python's Identity.from_bytes. It returns an error if the bytes
// cannot be loaded as a valid private key.
func FromBytes(prvBytes []byte) (*Identity, error) {
	id, err := NewIdentity(false)
	if err != nil {
		return nil, err
	}
	if err := id.LoadPrivateKey(prvBytes); err != nil {
		return nil, err
	}
	return id, nil
}

// LoadPrivateKey meticulously parses raw bytes to securely reinstantiate the underlying private key materials.
func (id *Identity) LoadPrivateKey(data []byte) error {
	half := IdentityKeySize / 8 / 2
	if len(data) != half*2 {
		return fmt.Errorf("invalid private key length: %v", len(data))
	}

	prv, err := crypto.NewX25519PrivateKeyFromBytes(data[:half])
	if err != nil {
		return err
	}
	id.prv = prv
	id.pub = prv.PublicKey()

	sigPrv, err := crypto.NewEd25519PrivateKeyFromBytes(data[half:])
	if err != nil {
		return err
	}
	id.sigPrv = sigPrv
	id.sigPub = sigPrv.PublicKey()

	id.UpdateHashes()
	return nil
}

// LoadPublicKey safely interprets raw network bytes to populate the associated public verification materials.
func (id *Identity) LoadPublicKey(data []byte) error {
	half := IdentityKeySize / 8 / 2
	if len(data) != half*2 {
		return fmt.Errorf("invalid public key length: %v", len(data))
	}

	pub, err := crypto.NewX25519PublicKeyFromBytes(data[:half])
	if err != nil {
		return err
	}
	id.pub = pub

	sigPub, err := crypto.NewEd25519PublicKeyFromBytes(data[half:])
	if err != nil {
		return err
	}
	id.sigPub = sigPub

	id.UpdateHashes()
	return nil
}

// FullHash computes an unmodified SHA-256 digest over arbitrary binary data.
func FullHash(data []byte) []byte {
	return crypto.SHA256(data)
}

// TruncatedHash computes a SHA-256 digest but aggressively truncates it to align with internal routing lengths.
func TruncatedHash(data []byte) []byte {
	return FullHash(data)[:TruncatedHashLength/8]
}

// CurrentRatchetID calculates the short unique identifier corresponding to the active forward-secrecy key.
func CurrentRatchetID(destHash []byte) []byte {
	ratchet := GetRatchet(destHash)
	if ratchet == nil {
		return nil
	}
	return RatchetID(ratchet)
}

// RatchetID generates the unique internal identifier corresponding directly to a specific ratchet public key.
func RatchetID(ratchetPubBytes []byte) []byte {
	return FullHash(ratchetPubBytes)[:NameHashLength/8]
}

// Sign delegates the generation of an Ed25519 cryptographic signature utilizing the identity's private signing key.
func (id *Identity) Sign(message []byte) ([]byte, error) {
	if id.sigPrv == nil {
		return nil, errors.New("identity does not hold a private signing key")
	}
	return id.sigPrv.Sign(message), nil
}

// Verify securely validates an Ed25519 cryptographic signature against an arbitrary message utilizing the identity's public key.
func (id *Identity) Verify(signature, message []byte) bool {
	if id.sigPub == nil {
		return false
	}
	return id.sigPub.Verify(signature, message)
}

// ValidateAnnounce exhaustively processes a newly received announce packet, verifying cryptographic proofs and logical constraints.
func ValidateAnnounce(packet *Packet) bool {
	if packet.PacketType != PacketAnnounce {
		return false
	}

	keySize := IdentityKeySize / 8
	nameHashLen := NameHashLength / 8
	sigLen := 64 // Ed25519 signature length

	if len(packet.Data) < keySize+nameHashLen+10+sigLen {
		return false
	}

	publicKey := packet.Data[:keySize]

	var nameHash, randomHash, ratchet, signature, appData []byte

	nameHash = packet.Data[keySize : keySize+nameHashLen]
	randomHash = packet.Data[keySize+nameHashLen : keySize+nameHashLen+10]

	offset := keySize + nameHashLen + 10
	Logf("ValidateAnnounce: Raw[:120]=%x", LogDebug, false, packet.Raw[:120])
	Logf("ValidateAnnounce: Data size=%v, offset=%v, context_flag=%v", LogDebug, false, len(packet.Data), offset, packet.ContextFlag)
	if packet.ContextFlag == FlagSet {
		ratchetsize := 32 // X25519 public key size
		ratchet = packet.Data[offset : offset+ratchetsize]
		offset += ratchetsize
	}

	signature = packet.Data[offset : offset+sigLen]
	offset += sigLen

	if len(packet.Data) > offset {
		appData = packet.Data[offset:]
	}

	// signed_data = destination_hash+public_key+name_hash+random_hash+ratchet+app_data
	signedData := make([]byte, 0, len(packet.DestinationHash)+len(publicKey)+len(nameHash)+len(randomHash)+len(ratchet)+len(appData))
	signedData = append(signedData, packet.DestinationHash...)
	signedData = append(signedData, publicKey...)
	signedData = append(signedData, nameHash...)
	signedData = append(signedData, randomHash...)
	signedData = append(signedData, ratchet...)
	signedData = append(signedData, appData...)

	id, err := NewIdentity(false)
	if err != nil {
		return false
	}
	if err := id.LoadPublicKey(publicKey); err != nil {
		return false
	}

	if !id.Verify(signature, signedData) {
		Log("Announce validation failed: signature mismatch", LogDebug, false)
		return false
	}

	// Validate destination hash
	// hash_material = name_hash+announced_identity.hash
	hashMaterial := make([]byte, 0, len(nameHash)+len(id.Hash))
	hashMaterial = append(hashMaterial, nameHash...)
	hashMaterial = append(hashMaterial, id.Hash...)
	expectedHash := FullHash(hashMaterial)[:TruncatedHashLength/8]

	if string(packet.DestinationHash) != string(expectedHash) {
		Logf("Announce validation failed: hash mismatch. Expected %x, got %x", LogDebug, false, expectedHash, packet.DestinationHash)
		return false
	}

	Remember(packet.PacketHash, packet.DestinationHash, publicKey, appData)
	if len(ratchet) > 0 {
		Logf("Learned ratchet %x for %x", LogDebug, false, ratchet, packet.DestinationHash)
		RememberRatchet(packet.DestinationHash, ratchet)
	}

	return true
}

// Encrypt constructs a highly secure cipher envelope over the payload, bootstrapping session keys via ephemeral Diffie-Hellman handshakes.
func (id *Identity) Encrypt(plaintext []byte, ratchetPubBytes []byte) ([]byte, error) {
	var targetPub *crypto.X25519PublicKey
	var err error

	if len(ratchetPubBytes) > 0 {
		targetPub, err = crypto.NewX25519PublicKeyFromBytes(ratchetPubBytes)
		if err != nil {
			return nil, err
		}
	} else {
		if id.pub == nil {
			return nil, errors.New("identity does not hold a public key")
		}
		targetPub = id.pub
	}

	ephemeralKey, err := crypto.GenerateX25519PrivateKey()
	if err != nil {
		return nil, err
	}
	ephemeralPubBytes := ephemeralKey.PublicKey().PublicBytes()
	Logf("Encrypt: Ephemeral Pub: %x", LogDebug, false, ephemeralPubBytes)

	sharedKey, err := ephemeralKey.Exchange(targetPub)
	if err != nil {
		return nil, err
	}

	derivedKey, err := crypto.HKDF(64, sharedKey, id.Hash, nil)
	if err != nil {
		return nil, err
	}
	Logf("Encrypt: Derived Key for %x: %x (salt: %x, shared: %x)", LogDebug, false, id.Hash, derivedKey, id.Hash, sharedKey)

	token, err := crypto.NewToken(derivedKey)
	if err != nil {
		return nil, err
	}

	ciphertext, err := id.encryptWithToken(token, plaintext)
	if err != nil {
		return nil, err
	}

	return append(ephemeralPubBytes, ciphertext...), nil
}

func (id *Identity) encryptWithToken(token *crypto.Token, plaintext []byte) ([]byte, error) {
	return token.Encrypt(plaintext)
}

// Decrypt attempts to symmetrically invert an encrypted payload utilizing dynamic fallback between ephemeral ratchets and static keys.
func (id *Identity) Decrypt(ciphertext []byte, ratchets []*crypto.X25519PrivateKey, enforceRatchets bool) ([]byte, error) {
	half := IdentityKeySize / 8 / 2
	if len(ciphertext) < half {
		return nil, errors.New("ciphertext too short")
	}

	ephemeralPubBytes := ciphertext[:half]
	tokenCiphertext := ciphertext[half:]

	ephemeralPub, err := crypto.NewX25519PublicKeyFromBytes(ephemeralPubBytes)
	if err != nil {
		return nil, err
	}

	// Try ratchets first if available
	for _, ratchet := range ratchets {
		sharedKey, err := ratchet.Exchange(ephemeralPub)
		if err != nil {
			continue
		}

		derivedKey, err := crypto.HKDF(64, sharedKey, id.Hash, nil)
		if err != nil {
			continue
		}
		Logf("Decrypt: Trial Derived Key for %x: %x (salt: %x)", LogDebug, false, id.Hash, derivedKey, id.Hash)

		token, err := crypto.NewToken(derivedKey)
		if err != nil {
			continue
		}

		plaintext, err := token.Decrypt(tokenCiphertext)
		if err == nil {
			return plaintext, nil
		}
	}

	if enforceRatchets {
		return nil, errors.New("decryption failed with ratchet enforcement")
	}

	if id.prv == nil {
		return nil, errors.New("identity does not hold a private key")
	}

	sharedKey, err := id.prv.Exchange(ephemeralPub)
	if err != nil {
		return nil, err
	}

	derivedKey, err := crypto.HKDF(64, sharedKey, id.Hash, nil)
	if err != nil {
		return nil, err
	}

	token, err := crypto.NewToken(derivedKey)
	if err != nil {
		return nil, err
	}

	return token.Decrypt(tokenCiphertext)
}

// FromFile instantiates a fully operational Identity context strictly by loading raw material from disk.
func FromFile(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	id, err := NewIdentity(false)
	if err != nil {
		return nil, err
	}
	if err := id.LoadPrivateKey(data); err != nil {
		return nil, err
	}
	return id, nil
}

// ToFile safely exports the core private identity bytes directly to a specified system path, restricted strictly for owner access.
func (id *Identity) ToFile(path string) error {
	data := id.GetPrivateKey()
	if data == nil {
		return errors.New("identity does not hold a private key")
	}
	return os.WriteFile(path, data, 0600)
}
