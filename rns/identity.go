// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"fmt"
	"os"

	"github.com/gmlewis/go-reticulum/rns/crypto"
)

const (
	// IdentityCurve specifies the elliptic curve standard used for Ephemeral Diffie-Hellman key exchanges within Reticulum.
	IdentityCurve = "Curve25519"
	// IdentityKeySize specifies the combined total size in bits of both the encryption and signing keypairs.
	IdentityKeySize = 256 * 2
)

// Identity encapsulates the fundamental cryptographic material representing a unique node or user within the Reticulum network.
type Identity struct {
	logger *Logger
	prv    *crypto.X25519PrivateKey
	pub    *crypto.X25519PublicKey
	sigPrv *crypto.Ed25519PrivateKey
	sigPub *crypto.Ed25519PublicKey

	Hash    []byte
	HexHash string
	AppData []byte
}

// NewIdentity allocates a new structural container, optionally auto-generating pristine cryptographic keys.
func NewIdentity(createKeys bool, logger *Logger) (*Identity, error) {
	id := &Identity{logger: logger}
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
func FromBytes(prvBytes []byte, logger *Logger) (*Identity, error) {
	id, err := NewIdentity(false, logger)
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
func ValidateAnnounce(ts Transport, packet *Packet) bool {
	if packet.PacketType != PacketAnnounce {
		return false
	}
	logger := ts.GetLogger()

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
	logger.Debug("ValidateAnnounce: Raw[:120]=%x", packet.Raw[:120])
	logger.Debug("ValidateAnnounce: Data size=%v, offset=%v, context_flag=%v", len(packet.Data), offset, packet.ContextFlag)
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

	id, err := NewIdentity(false, logger)
	if err != nil {
		return false
	}
	if err := id.LoadPublicKey(publicKey); err != nil {
		return false
	}

	if !id.Verify(signature, signedData) {
		logger.Debug("Announce validation failed: signature mismatch")
		return false
	}

	// Validate destination hash
	// hash_material = name_hash+announced_identity.hash
	hashMaterial := make([]byte, 0, len(nameHash)+len(id.Hash))
	hashMaterial = append(hashMaterial, nameHash...)
	hashMaterial = append(hashMaterial, id.Hash...)
	expectedHash := FullHash(hashMaterial)[:TruncatedHashLength/8]

	if string(packet.DestinationHash) != string(expectedHash) {
		logger.Debug("Announce validation failed: hash mismatch. Expected %x, got %x", expectedHash, packet.DestinationHash)
		return false
	}

	if ts != nil {
		ts.Remember(packet.PacketHash, packet.DestinationHash, publicKey, appData)
		if len(ratchet) > 0 {
			logger.Debug("Learned ratchet %x for %x", ratchet, packet.DestinationHash)
			ts.SetRatchet(packet.DestinationHash, ratchet)
		}
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
	id.logger.Debug("Encrypt: Ephemeral Pub: %x", ephemeralPubBytes)

	sharedKey, err := ephemeralKey.Exchange(targetPub)
	if err != nil {
		return nil, err
	}

	derivedKey, err := crypto.HKDF(64, sharedKey, id.Hash, nil)
	if err != nil {
		return nil, err
	}
	id.logger.Debug("Encrypt: Derived Key for %x: %x (salt: %x, shared: %x)", id.Hash, derivedKey, id.Hash, sharedKey)

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
		id.logger.Debug("Decrypt: Trial Derived Key for %x: %x (salt: %x)", id.Hash, derivedKey, id.Hash)

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
func FromFile(path string, logger *Logger) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	id, err := NewIdentity(false, logger)
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

// Prove generates and sends a cryptographic proof for the given packet.
func (id *Identity) Prove(packet *Packet, destination PacketDestination) {
	if id.sigPrv == nil {
		id.logger.Error("Identity cannot sign proof: no private key")
		return
	}
	signature := id.sigPrv.Sign(packet.PacketHash)

	var proofData []byte
	// TODO: Respect use_implicit_proof config
	proofData = make([]byte, 0, len(packet.PacketHash)+len(signature))
	proofData = append(proofData, packet.PacketHash...)
	proofData = append(proofData, signature...)

	if destination == nil {
		destination = packet.GenerateProofDestination()
	}

	proof := NewPacketWithTransport(packet.transport, destination, proofData)
	proof.PacketType = PacketProof
	proof.ReceivingInterface = packet.ReceivingInterface
	if err := proof.Send(); err != nil {
		id.logger.Debug("Failed to send proof: %v", err)
	}
}

// String returns a bracketed hex representation of the identity hash,
// matching Python's str(identity) output.
func (id *Identity) String() string {
	return PrettyHex(id.Hash)
}
