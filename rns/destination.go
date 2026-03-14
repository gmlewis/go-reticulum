// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// Destination types
const (
	DestinationSingle = 0x00
	DestinationGroup  = 0x01
	DestinationPlain  = 0x02
	DestinationLink   = 0x03
)

// Proof strategies
const (
	ProveNone = 0x21
	ProveApp  = 0x22
	ProveAll  = 0x23
)

// Request policies
const (
	AllowNone = 0x00
	AllowAll  = 0x01
	AllowList = 0x02
)

// Directions
const (
	DestinationIn  = 0x11
	DestinationOut = 0x12
)

// Callbacks holds function pointers for various destination events.
type Callbacks struct {
	LinkEstablished func(*Link)
	Packet          func([]byte, *Packet)
	ProofRequested  func(*Packet) bool
}

// RequestHandler handles incoming requests.
type RequestHandler struct {
	Path              string
	ResponseGenerator func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any
	Allow             int
	AllowedList       [][]byte
	AutoCompress      bool
	AutoCompressLimit int
}

// Destination represents an endpoint in a Reticulum network.
type Destination struct {
	identity      *Identity
	direction     int
	Type          int
	appName       string
	aspects       []string
	Hash          []byte
	HexHash       string
	name          string
	nameHash      []byte
	callbacks     Callbacks
	proofStrategy int

	mu sync.Mutex

	acceptLinkRequests bool
	transport          *TransportSystem
	requestHandlers    map[string]*RequestHandler

	ratchets          []*crypto.X25519PrivateKey
	ratchetsPath      string
	ratchetInterval   time.Duration
	latestRatchetTime time.Time
	latestRatchetID   []byte
	enforceRatchets   bool
	retainedRatchets  int
}

// NewDestination creates a new destination.
func NewDestination(identity *Identity, direction int, destType int, appName string, aspects ...string) (*Destination, error) {
	return NewDestinationWithTransport(GetTransport(), identity, direction, destType, appName, aspects...)
}

// NewDestinationWithTransport creates a new destination with a specific transport system.
func NewDestinationWithTransport(ts *TransportSystem, identity *Identity, direction int, destType int, appName string, aspects ...string) (*Destination, error) {
	if strings.Contains(appName, ".") {
		return nil, errors.New("dots can't be used in app names")
	}
	for _, aspect := range aspects {
		if strings.Contains(aspect, ".") {
			return nil, errors.New("dots can't be used in aspects")
		}
	}

	d := &Destination{
		identity:           identity,
		direction:          direction,
		Type:               destType,
		appName:            appName,
		aspects:            aspects,
		proofStrategy:      ProveNone,
		acceptLinkRequests: true,
		transport:          ts,
		requestHandlers:    make(map[string]*RequestHandler),
		ratchetInterval:    30 * time.Minute,
		retainedRatchets:   512,
	}

	if identity == nil && direction == DestinationIn && destType != DestinationPlain {
		var err error
		d.identity, err = NewIdentity(true)
		if err != nil {
			return nil, err
		}
		d.aspects = append(d.aspects, d.identity.HexHash)
	}

	if identity == nil && direction == DestinationOut && destType != DestinationPlain {
		return nil, errors.New("can't create outbound SINGLE destination without an identity")
	}

	if identity != nil && destType == DestinationPlain {
		return nil, errors.New("selected destination type PLAIN cannot hold an identity")
	}

	d.name = ExpandName(d.identity, d.appName, d.aspects...)
	d.Hash = CalculateHash(d.identity, d.appName, d.aspects...)
	d.HexHash = fmt.Sprintf("%x", d.Hash)

	// nameHash is used for announces
	d.nameHash = FullHash([]byte(ExpandName(nil, d.appName, d.aspects...)))[:NameHashLength/8]

	// Register with Transport
	if d.transport != nil {
		d.transport.RegisterDestination(d)
	}

	return d, nil
}

// Announce creates and broadcasts an announce packet for this destination.
func (d *Destination) Announce(appData []byte) error {
	p, err := d.buildAnnouncePacket(appData)
	if err != nil {
		return err
	}
	return p.Send()
}

// BuildAnnouncePacket constructs an announce packet for this destination
// with the given app_data, without sending it. This is useful for testing.
func (d *Destination) BuildAnnouncePacket(appData []byte) (*Packet, error) {
	return d.buildAnnouncePacket(appData)
}

func (d *Destination) buildAnnouncePacket(appData []byte) (*Packet, error) {
	if d.Type != DestinationSingle {
		return nil, errors.New("only SINGLE destination types can be announced")
	}
	if d.direction != DestinationIn {
		return nil, errors.New("only IN destination types can be announced")
	}

	randomHash := make([]byte, 10)
	if _, err := rand.Read(randomHash); err != nil {
		return nil, err
	}

	var ratchet []byte
	d.mu.Lock()
	if d.ratchets != nil {
		d.mu.Unlock()
		if err := d.RotateRatchets(); err != nil {
			return nil, err
		}
		d.mu.Lock()
		ratchet = d.ratchets[0].PublicKey().PublicBytes()
		RememberRatchet(d.Hash, ratchet)
	}
	d.mu.Unlock()

	// signed_data = self.hash+self.identity.get_public_key()+self.name_hash+random_hash+ratchet
	signedData := make([]byte, 0, 128)
	signedData = append(signedData, d.Hash...)
	signedData = append(signedData, d.identity.GetPublicKey()...)
	signedData = append(signedData, d.nameHash...)
	signedData = append(signedData, randomHash...)
	signedData = append(signedData, ratchet...)

	if appData != nil {
		signedData = append(signedData, appData...)
	}

	signature, err := d.identity.Sign(signedData)
	if err != nil {
		return nil, err
	}

	// announce_data = self.identity.get_public_key()+self.name_hash+random_hash+ratchet+signature
	announceData := make([]byte, 0, 256)
	announceData = append(announceData, d.identity.GetPublicKey()...)
	announceData = append(announceData, d.nameHash...)
	announceData = append(announceData, randomHash...)
	announceData = append(announceData, ratchet...)
	announceData = append(announceData, signature...)

	if appData != nil {
		announceData = append(announceData, appData...)
	}

	p := NewPacket(d, announceData)
	p.PacketType = PacketAnnounce
	if len(ratchet) > 0 {
		p.ContextFlag = FlagSet
	}
	return p, nil
}

// EnableRatchets enables ratchets on the destination.
func (d *Destination) EnableRatchets(path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if path == "" {
		return errors.New("no ratchet file path specified")
	}

	d.ratchetsPath = path
	d.latestRatchetTime = time.Time{} // Force rotation on first use if empty
	return d.reloadRatchets(path)
}

func (d *Destination) reloadRatchets(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		Logf("No existing ratchet data found, initializing new ratchet file for %v", LogDebug, false, d.name)
		d.ratchets = make([]*crypto.X25519PrivateKey, 0)
		return d.persistRatchets()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return err
	}

	m, ok := unpacked.(map[any]any)
	if !ok {
		return errors.New("invalid ratchet file format")
	}

	signature := m["signature"].([]byte)
	packedRatchets := m["ratchets"].([]byte)

	if !d.identity.Verify(signature, packedRatchets) {
		return errors.New("invalid ratchet file signature")
	}

	unpackedRatchets, err := msgpack.Unpack(packedRatchets)
	if err != nil {
		return err
	}

	ratchetList, ok := unpackedRatchets.([]any)
	if !ok {
		return errors.New("invalid ratchets list format")
	}

	d.ratchets = make([]*crypto.X25519PrivateKey, 0, len(ratchetList))
	for _, r := range ratchetList {
		prv, err := crypto.NewX25519PrivateKeyFromBytes(r.([]byte))
		if err != nil {
			continue
		}
		d.ratchets = append(d.ratchets, prv)
	}

	if len(d.ratchets) > 0 {
		d.latestRatchetID = RatchetID(d.ratchets[0].PublicKey().PublicBytes())
	}

	return nil
}

func (d *Destination) persistRatchets() error {
	if d.ratchetsPath == "" {
		return nil
	}

	ratchetBytes := make([][]byte, 0, len(d.ratchets))
	for _, r := range d.ratchets {
		ratchetBytes = append(ratchetBytes, r.PrivateBytes())
	}

	packedRatchets, err := msgpack.Pack(ratchetBytes)
	if err != nil {
		return err
	}

	signature, err := d.identity.Sign(packedRatchets)
	if err != nil {
		return err
	}

	persistedData := map[string]any{
		"signature": signature,
		"ratchets":  packedRatchets,
	}

	data, err := msgpack.Pack(persistedData)
	if err != nil {
		return err
	}

	tempPath := d.ratchetsPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tempPath, d.ratchetsPath)
}

// RotateRatchets generates a new ratchet key pair if the interval has passed.
func (d *Destination) RotateRatchets() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.ratchets == nil {
		return errors.New("ratchets are not enabled")
	}

	if time.Since(d.latestRatchetTime) < d.ratchetInterval {
		return nil
	}

	Logf("Rotating ratchets for %v", LogDebug, false, d.name)
	newRatchet, err := crypto.GenerateX25519PrivateKey()
	if err != nil {
		return err
	}

	// Prepend
	d.ratchets = append([]*crypto.X25519PrivateKey{newRatchet}, d.ratchets...)
	d.latestRatchetTime = time.Now()
	d.latestRatchetID = RatchetID(newRatchet.PublicKey().PublicBytes())

	if len(d.ratchets) > d.retainedRatchets {
		d.ratchets = d.ratchets[:d.retainedRatchets]
	}

	return d.persistRatchets()
}

// SetPacketCallback sets the callback for received packets.
func (d *Destination) SetPacketCallback(callback func([]byte, *Packet)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.callbacks.Packet = callback
}

// SetLinkEstablishedCallback sets the callback for established links.
func (d *Destination) SetLinkEstablishedCallback(callback func(*Link)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.callbacks.LinkEstablished = callback
}

// RegisterRequestHandler registers a request handler for the given path.
func (d *Destination) RegisterRequestHandler(path string, responseGenerator func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any, allow int, allowedList [][]byte, autoCompress bool) {
	autoCompressLimit := 0
	if autoCompress {
		autoCompressLimit = ResourceAutoCompressMaxSize
	}
	d.RegisterRequestHandlerWithAutoCompressLimit(path, responseGenerator, allow, allowedList, autoCompress, autoCompressLimit)
}

// RegisterRequestHandlerWithAutoCompressLimit registers a request handler for the given path with explicit auto-compression limit.
func (d *Destination) RegisterRequestHandlerWithAutoCompressLimit(path string, responseGenerator func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any, allow int, allowedList [][]byte, autoCompress bool, autoCompressLimit int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	pathHash := TruncatedHash([]byte(path))
	d.requestHandlers[string(pathHash)] = &RequestHandler{
		Path:              path,
		ResponseGenerator: responseGenerator,
		Allow:             allow,
		AllowedList:       allowedList,
		AutoCompress:      autoCompress,
		AutoCompressLimit: autoCompressLimit,
	}
}

// HasRequestHandler reports whether a handler is registered for the given path.
func (d *Destination) HasRequestHandler(path string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	pathHash := TruncatedHash([]byte(path))
	_, ok := d.requestHandlers[string(pathHash)]
	return ok
}

// DeregisterRequestHandler removes a request handler for the given path.
func (d *Destination) DeregisterRequestHandler(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	pathHash := TruncatedHash([]byte(path))
	delete(d.requestHandlers, string(pathHash))
}

// ExpandName returns the expanded name for the destination.
func ExpandName(identity *Identity, appName string, aspects ...string) string {
	name := appName
	for _, aspect := range aspects {
		name += "." + aspect
	}
	if identity != nil {
		name += "." + identity.HexHash
	}
	return name
}

// CalculateHash returns the destination hash for the given identity, appName and aspects.
func CalculateHash(identity *Identity, appName string, aspects ...string) []byte {
	nameHash := FullHash([]byte(ExpandName(nil, appName, aspects...)))[:NameHashLength/8]
	material := nameHash
	if identity != nil {
		material = append(material, identity.Hash...)
	}
	return FullHash(material)[:TruncatedHashLength/8]
}

func (d *Destination) String() string {
	return fmt.Sprintf("<%v:%v>", d.name, d.HexHash)
}

// Encrypt encrypts data for the destination.
func (d *Destination) Encrypt(plaintext []byte) ([]byte, error) {
	if d.Type == DestinationPlain {
		return plaintext, nil
	}
	if d.identity == nil {
		return nil, errors.New("destination does not hold an identity")
	}

	selectedRatchet := GetRatchet(d.Hash)
	if selectedRatchet != nil {
		d.latestRatchetID = RatchetID(selectedRatchet)
	}
	return d.identity.Encrypt(plaintext, selectedRatchet)
}

// Decrypt decrypts data for the destination.
func (d *Destination) Decrypt(ciphertext []byte) ([]byte, error) {
	if d.Type == DestinationPlain {
		return ciphertext, nil
	}
	if d.identity == nil {
		return nil, errors.New("destination does not hold an identity")
	}

	if len(d.ratchets) > 0 {
		decrypted, err := d.identity.Decrypt(ciphertext, d.ratchets, d.enforceRatchets)
		if err == nil {
			return decrypted, nil
		}
		// If decryption failed, try reloading ratchets from storage and retrying
		if d.ratchetsPath != "" {
			Logf("Decryption with ratchets failed on %v, reloading from storage", LogDebug, false, d.name)
			d.mu.Lock()
			if reloadErr := d.reloadRatchets(d.ratchetsPath); reloadErr != nil {
				Logf("Failed reloading ratchets for %v from %v: %v", LogWarning, false, d.name, d.ratchetsPath, reloadErr)
			}
			ratchets := d.ratchets
			d.mu.Unlock()
			return d.identity.Decrypt(ciphertext, ratchets, d.enforceRatchets)
		}
		return nil, err
	}

	return d.identity.Decrypt(ciphertext, nil, d.enforceRatchets)
}

// Sign signs data using the destination's identity.
func (d *Destination) Sign(data []byte) ([]byte, error) {
	if d.identity == nil {
		return nil, errors.New("destination does not hold an identity")
	}
	return d.identity.Sign(data)
}

// Verify verifies a signature using the destination's identity.
func (d *Destination) Verify(signature, data []byte) bool {
	if d.identity == nil {
		return false
	}
	return d.identity.Verify(signature, data)
}
