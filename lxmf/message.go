// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/qr"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// Message represents a fully materialized LXMF message, encompassing routing metadata, cryptographic signatures, and the structured payload for network transport.
type Message struct {
	// Destination is the delivery destination the message is addressed to.
	Destination *rns.Destination
	// Source is the destination that signs and originates the message.
	Source *rns.Destination
	// DestinationHash is the truncated Reticulum hash of Destination.
	DestinationHash []byte
	// SourceHash is the truncated Reticulum hash of Source.
	SourceHash []byte

	// Title holds the optional message title as raw bytes.
	Title []byte
	// Content holds the main message body as raw bytes.
	Content []byte
	// Fields carries optional structured LXMF metadata fields.
	Fields map[any]any

	// Timestamp records when the message payload was created, in Unix seconds.
	Timestamp float64
	// Stamp holds an optional proof-of-work stamp attached to the payload.
	Stamp []byte
	// StampCost is the required proof-of-work cost for this message, or nil when
	// no stamp is required.
	StampCost *int
	// StampValue records the effective value of the attached delivery stamp.
	StampValue *int
	// OutboundTicket holds a cached remote reply ticket that can replace hashcash
	// work for outbound delivery.
	OutboundTicket []byte
	// DeferStamp mirrors Python's default behavior of postponing stamp
	// generation until the router decides it must happen immediately.
	DeferStamp bool
	// DeferPropagationStamp mirrors Python's LXMessage.defer_propagation_stamp
	// (LXMessage.py:164): propagation-node stamp generation is deferred until
	// the router processes the outbound queue. The Go port wires this in
	// Router.ProcessDeferredStamps (router.go), which mirrors Python's
	// LXMRouter.process_deferred_stamps (LXMRouter.py:2406-2498): when a
	// propagated message still lacks PropagationStamp, the router computes the
	// outbound propagation target cost, generates the stamp, clears this flag,
	// repacks, and queues the message for delivery. The wiring is golden-tested
	// by TestDeferredPropagationStamps and TestDeferPropagationStamp.
	DeferPropagationStamp bool
	// AutoCompress controls whether a DIRECT-delivery resource is
	// auto-compressed before transmission. It defaults to true and is
	// reconciled with the destination's recalled announce app-data by
	// DetermineCompressionSupport, mirroring Python's
	// LXMessage.auto_compress (LXMF/LXMessage.py:146, 510-513, v1.1.0).
	AutoCompress bool

	// Payload stores the unpacked LXMF payload elements used for packing or
	// validating the message.
	Payload []any

	// Hash is the LXMF message hash over addressing metadata and payload.
	Hash []byte
	// MessageID is the stable identifier used for message tracking.
	MessageID []byte
	// Signature holds the source destination's signature over the signed LXMF
	// material.
	Signature []byte
	// Packed contains the serialized LXMF wire representation.
	Packed []byte
	// TransientID tracks the propagated-delivery transient identifier derived from
	// the propagation payload.
	TransientID []byte
	// PropagationPacked stores the propagated-delivery msgpack wire payload.
	PropagationPacked []byte
	// PaperPacked stores the paper-message URI-ready wire payload.
	PaperPacked []byte
	// PropagationStamp holds the optional proof-of-work stamp appended to the
	// propagated transport payload.
	PropagationStamp []byte
	// PropagationStampValue records the effective value of PropagationStamp.
	PropagationStampValue *int
	// PropagationTargetCost stores the propagation-node target cost used when
	// generating PropagationStamp.
	PropagationTargetCost *int
	// StampGenerationFailed tracks Python's deferred-stamp failure marker.
	StampGenerationFailed bool

	// RatchetID records the ratchet ID used to encrypt this message for
	// its destination. It is populated by Pack() and on send, and read
	// by UnpackMessageFromBytes to surface the ratchet identity that the
	// remote peer used.
	RatchetID []byte

	// StampValid records whether the message's stamp has been validated
	// against a target cost (or against an inbound ticket). Mirrors
	// Python's LXMessage.stamp_valid.
	StampValid bool
	// StampChecked records whether the message's stamp has been
	// inspected at all. Mirrors Python's LXMessage.stamp_checked.
	StampChecked bool
	// PropagationStampValid records whether the message's
	// propagation stamp has been validated. Mirrors Python's
	// LXMessage.propagation_stamp_valid.
	PropagationStampValid bool

	deferredStampOrder uint64

	// state tracks the current lifecycle state of the message. It is mutated
	// only through SetState (which takes persistMu) or by code that already
	// holds persistMu; read via State. Unexported so downstream callers cannot
	// bypass the lock with a direct field write.
	state int

	// DesiredMethod is the preferred delivery method requested by the sender.
	DesiredMethod int
	// method is the delivery method actually used or observed. Guarded by
	// persistMu like state; mutate via SetMethod, read via Method.
	method int
	// Representation records whether the message traveled as a packet or as a
	// resource.
	Representation int
	// progress tracks Python-style outbound transfer progress in the range
	// 0.0-1.0. Guarded by persistMu like state; mutate via SetProgress, read
	// via Progress.
	progress float64
	// TransportEncrypted reports whether the outer transport layer encrypts the
	// message in transit.
	TransportEncrypted bool
	// TransportEncryption describes the transport encryption mode using Python's
	// human-readable strings.
	TransportEncryption string

	// DeliveryAttempts counts how many delivery attempts have been made.
	DeliveryAttempts int
	// NextDeliveryAttempt is the Unix timestamp for the next scheduled retry.
	NextDeliveryAttempt float64

	// Incoming reports whether the message was received from the network instead
	// of constructed locally for transmission.
	Incoming bool
	// SignatureValidated reports whether Signature was successfully verified.
	SignatureValidated bool
	// UnverifiedReason describes why signature validation could not succeed.
	UnverifiedReason int

	// SourceBlackholed reports whether the recalled source identity is on the
	// local blackhole list. It is set during unpack from
	// Transport.IsBlackholed, mirroring Python's
	// LXMessage.source_blackholed (LXMF/LXMessage.py:172, 804, v1.0.0+).
	// Routers drop inbound messages whose source is blackholed.
	SourceBlackholed bool

	// TryPropagationOnFail requests propagated delivery after direct delivery
	// fails.
	TryPropagationOnFail bool
	// IncludeTicket requests ticket metadata to be included when applicable.
	IncludeTicket bool

	// DeliveryCallback runs after successful delivery.
	DeliveryCallback func(*Message)
	// FailedCallback runs after the message permanently fails delivery.
	FailedCallback func(*Message)
	// PacketRepresentation stores the last synthesized packet representation used
	// for outbound transport.
	PacketRepresentation *rns.Packet
	// ResourceRepresentation stores the last synthesized resource representation
	// used for outbound transport.
	ResourceRepresentation *rns.Resource

	deliveryDestination      rns.PacketDestination
	propagationEncryptedData []byte
	rawStampCost             any

	// persistMu serializes persistence of this message to disk, mirroring
	// Python's LXMessage.__persist_lock (LXMessage.py:188, v0.9.9). It guards
	// WriteToDirectory (and the PackedContainer snapshot it captures) so
	// concurrent writes never interleave or leave a partial file.
	persistMu sync.Mutex
}

type pythonStampCostTypeError struct {
	message string
}

func (e pythonStampCostTypeError) Error() string {
	return e.message
}

// NewMessage constructs a fresh, outbound LXMF message bound for the specified destination, securely anchoring it to the originating source identity.
func NewMessage(destination, source *rns.Destination, content, title string, fields map[any]any) (*Message, error) {
	if destination == nil {
		return nil, errors.New("lxmf destination is required")
	}
	if source == nil {
		return nil, errors.New("lxmf source is required")
	}

	m := &Message{
		Destination:           destination,
		Source:                source,
		DestinationHash:       cloneBytes(destination.Hash),
		SourceHash:            cloneBytes(source.Hash),
		Title:                 []byte(title),
		Content:               []byte(content),
		Fields:                ensureFields(fields),
		state:                 StateGenerating,
		DesiredMethod:         MethodDirect,
		method:                RepresentationUnknown,
		Representation:        RepresentationUnknown,
		DeferStamp:            true,
		DeferPropagationStamp: true,
		AutoCompress:          true,
	}

	return m, nil
}

// GetStamp returns the current delivery stamp, generating it if needed from an
// outbound ticket or the configured stamp cost.
func (m *Message) GetStamp() ([]byte, error) {
	return m.GetStampWithContext(context.Background())
}

// GetStampWithContext is the cancellation-aware variant of GetStamp. The
// returned stamp slice and error are exactly those returned by GetStamp,
// but stamp generation can be aborted by canceling the provided context.
func (m *Message) GetStampWithContext(ctx context.Context) ([]byte, error) {
	if len(m.OutboundTicket) == TicketLength && len(m.MessageID) > 0 {
		material := make([]byte, 0, len(m.OutboundTicket)+len(m.MessageID))
		material = append(material, m.OutboundTicket...)
		material = append(material, m.MessageID...)
		stampValue := TicketCostValue
		m.StampValue = cloneOptionalInt(&stampValue)
		return rns.TruncatedHash(material), nil
	}

	if m.rawStampCost != nil {
		return nil, pythonStampCostError(m.rawStampCost)
	}

	if m.StampCost == nil {
		m.StampValue = nil
		return nil, nil
	}
	if len(m.Stamp) > 0 {
		return cloneBytes(m.Stamp), nil
	}

	stamp, stampValue, _, err := GenerateStampWithContext(ctx, m.MessageID, *m.StampCost, WorkblockExpandRounds)
	if err != nil {
		return nil, err
	}
	m.StampValue = cloneOptionalInt(&stampValue)
	return stamp, nil
}

func pythonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case int, int8, int16, int32, int64:
		return "int"
	case uint, uint8, uint16, uint32, uint64:
		return "int"
	case float32, float64:
		return "float"
	case string:
		return "str"
	case []byte:
		return "bytes"
	case []any:
		return "list"
	case map[any]any, map[string]any:
		return "dict"
	default:
		return reflect.TypeOf(value).String()
	}
}

func pythonStampCostError(value any) error {
	typeName := pythonTypeName(value)
	switch value.(type) {
	case float32, float64:
		return pythonStampCostTypeError{message: fmt.Sprintf("unsupported operand type(s) for <<: 'int' and '%v'", typeName)}
	default:
		return pythonStampCostTypeError{message: fmt.Sprintf("unsupported operand type(s) for -: 'int' and '%v'", typeName)}
	}
}

// GetPropagationStamp returns the current propagated-delivery stamp, generating
// it if needed for the configured propagation-node target cost.
func (m *Message) GetPropagationStamp(targetCost int) ([]byte, error) {
	return m.GetPropagationStampWithContext(context.Background(), targetCost)
}

// GetPropagationStampWithContext is the cancellation-aware variant of
// GetPropagationStamp. See GetStampWithContext for the cancellation contract.
func (m *Message) GetPropagationStampWithContext(ctx context.Context, targetCost int) ([]byte, error) {
	if len(m.PropagationStamp) > 0 {
		return cloneBytes(m.PropagationStamp), nil
	}

	m.PropagationTargetCost = cloneOptionalInt(&targetCost)
	if m.PropagationTargetCost == nil || *m.PropagationTargetCost <= 0 {
		return nil, fmt.Errorf("cannot generate propagation stamp without configured target propagation cost")
	}

	if len(m.TransientID) == 0 {
		if len(m.Packed) == 0 {
			if err := m.Pack(); err != nil {
				return nil, err
			}
		} else if err := m.packPropagated(); err != nil {
			return nil, err
		}
	}

	stamp, stampValue, _, err := GenerateStampWithContext(ctx, m.TransientID, *m.PropagationTargetCost, WorkblockExpandRoundsPN)
	if err != nil {
		return nil, err
	}
	m.PropagationStampValue = cloneOptionalInt(&stampValue)
	return stamp, nil
}

// SetTitleString intuitively mutates the underlying byte array representing the message title using a standard Go string.
func (m *Message) SetTitleString(title string) {
	m.Title = []byte(title)
}

// SetContentString injects a standard Go string directly into the message's primary content payload byte array.
func (m *Message) SetContentString(content string) {
	m.Content = []byte(content)
}

// TitleString safely decodes the underlying byte array of the message title into a human-readable Go string.
func (m *Message) TitleString() string {
	return string(m.Title)
}

// ContentString safely decodes the underlying byte array of the message content into a human-readable Go string.
func (m *Message) ContentString() string {
	return string(m.Content)
}

// Pack prepares the message for network transmission by assembling its payload, calculating its hash, and generating a cryptographic signature.
func (m *Message) Pack() error {
	if len(m.Packed) > 0 {
		return fmt.Errorf("lxmf message already packed")
	}
	if m.Destination == nil || m.Source == nil {
		return errors.New("lxmf pack requires destination and source destinations")
	}

	m.DestinationHash = cloneBytes(m.Destination.Hash)
	m.SourceHash = cloneBytes(m.Source.Hash)

	if len(m.DestinationHash) != DestinationLength {
		return fmt.Errorf("invalid destination hash length %v", len(m.DestinationHash))
	}
	if len(m.SourceHash) != DestinationLength {
		return fmt.Errorf("invalid source hash length %v", len(m.SourceHash))
	}

	if m.Timestamp == 0 {
		m.Timestamp = float64(time.Now().UnixNano()) / 1e9
	}

	basePayload := []any{m.Timestamp, m.Title, m.Content, ensureFields(m.Fields)}

	// The message hash must be deterministic so a message packed on one host
	// and unpacked on another yields the same identity hash (used as the
	// on-disk filename and the attachment-directory key). Go's randomized map
	// iteration order would otherwise make the fields-map encoding — and thus
	// the hash — differ across pack/unpack. Pack both the hashed part and the
	// wire payload with sorted map keys for a canonical encoding. The unpack
	// path uses UnpackPreserveBinMapKeyOrder + PackSorted, which preserves the
	// wire byte order via OrderedMap.MarshalMsgpack, so a Python-packed message
	// (insertion-order keys) verifies correctly while a Go-packed message
	// (sorted keys) also verifies correctly.
	packedPayload, err := msgpack.PackSorted(basePayload)
	if err != nil {
		return fmt.Errorf("pack lxmf payload: %w", err)
	}

	hashedPart := make([]byte, 0, len(m.DestinationHash)+len(m.SourceHash)+len(packedPayload))
	hashedPart = append(hashedPart, m.DestinationHash...)
	hashedPart = append(hashedPart, m.SourceHash...)
	hashedPart = append(hashedPart, packedPayload...)

	m.Hash = rns.FullHash(hashedPart)
	m.MessageID = cloneBytes(m.Hash)

	if !m.DeferStamp {
		stamp, err := m.GetStamp()
		if err != nil {
			if _, ok := errors.AsType[pythonStampCostTypeError](err); ok {
				return err
			}
			return fmt.Errorf("generate lxmf stamp: %w", err)
		}
		m.Stamp = cloneBytes(stamp)
	}

	m.Payload = basePayload
	if len(m.Stamp) > 0 {
		m.Payload = append(m.Payload, cloneBytes(m.Stamp))
	}

	signedPart := make([]byte, 0, len(hashedPart)+len(m.Hash))
	signedPart = append(signedPart, hashedPart...)
	signedPart = append(signedPart, m.Hash...)

	signature, err := m.Source.Sign(signedPart)
	if err != nil {
		return fmt.Errorf("sign lxmf message: %w", err)
	}
	if len(signature) != SignatureLength {
		return fmt.Errorf("unexpected signature length %v", len(signature))
	}
	m.Signature = signature
	m.SignatureValidated = true

	packedPayload, err = msgpack.PackSorted(m.Payload)
	if err != nil {
		return fmt.Errorf("pack stamped lxmf payload: %w", err)
	}

	m.Packed = make([]byte, 0, len(m.DestinationHash)+len(m.SourceHash)+len(m.Signature)+len(packedPayload))
	m.Packed = append(m.Packed, m.DestinationHash...)
	m.Packed = append(m.Packed, m.SourceHash...)
	m.Packed = append(m.Packed, m.Signature...)
	m.Packed = append(m.Packed, packedPayload...)

	if m.DesiredMethod == MethodOpportunistic {
		if len(m.Packed) > EncryptedPacketMaxContent {
			return fmt.Errorf("lxmf message desired opportunistic delivery method, but content of length %v exceeds single-packet content limit of %v", len(m.Packed), EncryptedPacketMaxContent)
		}
		m.method = MethodOpportunistic
		m.Representation = RepresentationPacket
	}

	if m.DesiredMethod == MethodPropagated {
		if err := m.packPropagated(); err != nil {
			return err
		}
	}

	if m.DesiredMethod == MethodPaper {
		encryptedData, err := m.Destination.Encrypt(m.Packed[DestinationLength:])
		if err != nil {
			return fmt.Errorf("encrypt paper payload: %w", err)
		}
		m.PaperPacked = make([]byte, 0, len(m.DestinationHash)+len(encryptedData))
		m.PaperPacked = append(m.PaperPacked, m.DestinationHash...)
		m.PaperPacked = append(m.PaperPacked, encryptedData...)
		m.RatchetID = m.Destination.LatestRatchetID()
		m.method = MethodPaper
		m.Representation = RepresentationPaper
	}

	return nil
}

// UnpackMessageFromBytes reconstructs a Message object from its raw binary representation and validates its cryptographic integrity.
func UnpackMessageFromBytes(ts rns.Transport, data []byte, originalMethod int) (*Message, error) {
	minimum := (2 * DestinationLength) + SignatureLength
	if len(data) < minimum {
		return nil, fmt.Errorf("lxmf bytes too short: got %v, need at least %v", len(data), minimum)
	}

	destinationHash := cloneBytes(data[:DestinationLength])
	sourceHash := cloneBytes(data[DestinationLength : 2*DestinationLength])
	signature := cloneBytes(data[2*DestinationLength : 2*DestinationLength+SignatureLength])
	packedPayload := cloneBytes(data[2*DestinationLength+SignatureLength:])

	unpackedPayloadAny, err := msgpack.UnpackPreserveBinMapKeyOrder(packedPayload)
	if err != nil {
		return nil, fmt.Errorf("unpack lxmf payload: %w", err)
	}

	unpackedPayload, ok := unpackedPayloadAny.([]any)
	if !ok {
		return nil, errors.New("invalid lxmf payload type")
	}
	unpackedPayload = normalizePayload(unpackedPayload)
	if len(unpackedPayload) < 4 {
		return nil, errors.New("invalid lxmf payload length")
	}

	stamp, payloadWithoutStamp := extractStamp(unpackedPayload)
	repackedPayload, err := msgpack.PackSorted(payloadWithoutStamp)
	if err != nil {
		return nil, fmt.Errorf("repack lxmf payload for hash validation: %w", err)
	}

	hashedPart := make([]byte, 0, len(destinationHash)+len(sourceHash)+len(repackedPayload))
	hashedPart = append(hashedPart, destinationHash...)
	hashedPart = append(hashedPart, sourceHash...)
	hashedPart = append(hashedPart, repackedPayload...)

	messageHash := rns.FullHash(hashedPart)
	signedPart := make([]byte, 0, len(hashedPart)+len(messageHash))
	signedPart = append(signedPart, hashedPart...)
	signedPart = append(signedPart, messageHash...)

	timestamp, err := payloadTimestamp(payloadWithoutStamp[0])
	if err != nil {
		return nil, err
	}
	title, err := payloadBytes(payloadWithoutStamp[1], "title")
	if err != nil {
		return nil, err
	}
	content, err := payloadBytes(payloadWithoutStamp[2], "content")
	if err != nil {
		return nil, err
	}
	fields, err := payloadMap(payloadWithoutStamp[3])
	if err != nil {
		return nil, err
	}

	destination := recalledDeliveryDestination(ts, destinationHash)
	source := recalledDeliveryDestination(ts, sourceHash)

	m := &Message{
		Destination:     destination,
		Source:          source,
		DestinationHash: destinationHash,
		SourceHash:      sourceHash,
		Title:           title,
		Content:         content,
		Fields:          fields,
		Timestamp:       timestamp,
		Stamp:           stamp,
		Payload:         payloadWithoutStamp,
		Hash:            messageHash,
		MessageID:       cloneBytes(messageHash),
		Signature:       signature,
		Packed:          cloneBytes(data),
		Incoming:        true,
		state:           originalMethod,
		method:          originalMethod,
		DesiredMethod:   originalMethod,
		Representation:  RepresentationUnknown,
	}

	if source != nil {
		if source.Verify(signature, signedPart) {
			m.SignatureValidated = true
		} else {
			m.SignatureValidated = false
			m.UnverifiedReason = ReasonSignatureInvalid
		}
	} else {
		m.SignatureValidated = false
		m.UnverifiedReason = ReasonSourceUnknown
	}

	// Mirror Python LXMessage.unpack_from_bytes (LXMessage.py:803-805,
	// v1.0.0+): when the source identity was recalled, query the local
	// blackhole list by identity hash so the router can drop the message.
	// An unrecalled source leaves the flag false, matching Python's
	// source_identity-is-None guard.
	if sourceIdentity := ts.RecallNoUse(sourceHash); sourceIdentity != nil {
		m.SourceBlackholed = ts.IsBlackholed(sourceIdentity.Hash)
	}

	return m, nil
}

// UnpackMessageFromFile reconstructs an LXMF message from a msgpack container
// written by WriteToDirectory, restoring the saved transport metadata fields
// that Python's unpack_from_file() also reapplies.
func UnpackMessageFromFile(ts rns.Transport, lxmfFile io.Reader) (*Message, error) {
	if lxmfFile == nil {
		return nil, errors.New("lxmf file reader is required")
	}

	data, err := io.ReadAll(lxmfFile)
	if err != nil {
		return nil, fmt.Errorf("read lxmf container: %w", err)
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return nil, fmt.Errorf("unpack lxmf container: %w", err)
	}

	container, ok := unpacked.(map[any]any)
	if !ok {
		return nil, fmt.Errorf("invalid lxmf container type %T", unpacked)
	}

	lxmfBytes, ok := container["lxmf_bytes"].([]byte)
	if !ok {
		return nil, fmt.Errorf("invalid lxmf container bytes type %T", container["lxmf_bytes"])
	}

	message, err := UnpackMessageFromBytes(ts, lxmfBytes, RepresentationUnknown)
	if err != nil {
		return nil, err
	}

	if state, ok := container["state"]; ok {
		parsedState, err := containerInt(state)
		if err != nil {
			return nil, err
		}
		message.state = parsedState
	}
	if transportEncrypted, ok := container["transport_encrypted"]; ok {
		parsedTransportEncrypted, ok := transportEncrypted.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid lxmf container transport_encrypted type %T", transportEncrypted)
		}
		message.TransportEncrypted = parsedTransportEncrypted
	}
	if transportEncryption, ok := container["transport_encryption"]; ok {
		parsedTransportEncryption, ok := transportEncryption.(string)
		if !ok {
			return nil, fmt.Errorf("invalid lxmf container transport_encryption type %T", transportEncryption)
		}
		message.TransportEncryption = parsedTransportEncryption
	}
	if method, ok := container["method"]; ok {
		parsedMethod, err := containerInt(method)
		if err != nil {
			return nil, err
		}
		message.method = parsedMethod
	}

	return message, nil
}

func ensureFields(fields map[any]any) map[any]any {
	if fields == nil {
		return map[any]any{}
	}
	return fields
}

func payloadTimestamp(v any) (float64, error) {
	if _, ok := v.(bool); ok {
		return 0, fmt.Errorf("invalid lxmf timestamp type %T value %#v", v, v)
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), nil
	default:
		return 0, fmt.Errorf("invalid lxmf timestamp type %T value %#v", v, v)
	}
}

func payloadBytes(v any, field string) ([]byte, error) {
	switch t := v.(type) {
	case []byte:
		return cloneBytes(t), nil
	case string:
		return []byte(t), nil
	default:
		return nil, fmt.Errorf("invalid lxmf %v type %T", field, v)
	}
}

func payloadMap(v any) (map[any]any, error) {
	if m, ok := v.(map[any]any); ok {
		return m, nil
	}
	if om, ok := v.(msgpack.OrderedMap); ok {
		return orderedMapToMap(om), nil
	}
	return nil, fmt.Errorf("invalid lxmf fields type %T", v)
}

// orderedMapToMap recursively converts an OrderedMap (and any nested OrderedMap
// values) to map[any]any, matching the map shape that Python umsgpack returns
// for nested dicts. This keeps the Message.Fields API stable as map[any]any
// while the hash computation uses the original wire order via OrderedMap's
// MarshalMsgpack.
func orderedMapToMap(om msgpack.OrderedMap) map[any]any {
	m := make(map[any]any, len(om))
	for _, e := range om {
		m[e.Key] = normalizeUnpackedValue(e.Value)
	}
	return m
}

// normalizeUnpackedValue converts any nested OrderedMap to map[any]any and
// recurses into slices. Other types pass through unchanged.
func normalizeUnpackedValue(v any) any {
	switch tv := v.(type) {
	case msgpack.OrderedMap:
		return orderedMapToMap(tv)
	case []any:
		for i, elem := range tv {
			tv[i] = normalizeUnpackedValue(elem)
		}
		return tv
	default:
		return v
	}
}

func containerInt(v any) (int, error) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()), nil
	default:
		return 0, fmt.Errorf("invalid lxmf container integer type %T", v)
	}
}

func extractStamp(payload []any) ([]byte, []any) {
	if len(payload) <= 4 {
		return nil, payload[:4]
	}
	if stamp, ok := payload[4].([]byte); ok {
		return cloneBytes(stamp), payload[:4]
	}
	return nil, payload[:4]
}

func normalizePayload(payload []any) []any {
	for {
		if len(payload) == 0 {
			return payload
		}
		if isTimestampType(payload[0]) {
			return payload
		}

		nested, ok := asAnySlice(payload[0])
		if !ok || len(nested) < 4 {
			return payload
		}

		payload = nested
	}
}

func asAnySlice(v any) ([]any, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return nil, false
	}

	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}

	return out, true
}

func isTimestampType(v any) bool {
	switch v.(type) {
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		return true
	default:
		return false
	}
}

func recalledDeliveryDestination(ts rns.Transport, destHash []byte) *rns.Destination {
	identity := ts.RecallNoUse(destHash)
	if identity == nil {
		return nil
	}

	d, err := rns.NewDestination(ts, identity, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		return nil
	}

	return d
}

// PackedContainer returns the msgpack-encoded container dict for this message,
// matching Python's LXMessage.packed_container() method. The snapshot is taken
// under the per-Message persist mutex so it is consistent with any concurrent
// persist and with callers that mutate message state under the same lock
// (Python __persist_lock, LXMessage.py:188, v1.0.0).
func (m *Message) PackedContainer() ([]byte, error) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	return m.packedContainerLocked()
}

// SetState updates m.state under the per-Message persist mutex so the write is
// synchronized with concurrent PackedContainer/WriteToDirectory snapshots,
// mirroring Python's __persist_lock guarding state mutation during persist
// (LXMessage.py:188). Callers that already hold persistMu (e.g. inside Pack or
// packPropagated when invoked from packedContainerLocked) may assign m.state
// directly instead of calling this method, since persistMu is a non-reentrant
// mutex and re-entering it would self-deadlock. The router holds its own mutex
// (r.mu) across these calls, so the consistent lock order is r.mu -> persistMu;
// PackedContainer/WriteToDirectory take only persistMu, so no lock cycle is
// possible.
func (m *Message) SetState(s int) {
	m.persistMu.Lock()
	m.state = s
	m.persistMu.Unlock()
}

// State returns the current lifecycle state under the per-Message persist mutex
// so the read is consistent with concurrent snapshots and cannot race a locked
// SetState write. See SetState for the lock-ordering rationale.
func (m *Message) State() int {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	return m.state
}

// SetMethod updates m.method under the per-Message persist mutex; see SetState.
func (m *Message) SetMethod(method int) {
	m.persistMu.Lock()
	m.method = method
	m.persistMu.Unlock()
}

// Method returns the delivery method under the per-Message persist mutex; see
// State for the locking rationale.
func (m *Message) Method() int {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	return m.method
}

// SetProgress updates m.progress under the per-Message persist mutex; see
// SetState. The router calls this for every progress change so an external
// Progress reader can never race a write.
func (m *Message) SetProgress(p float64) {
	m.persistMu.Lock()
	m.progress = p
	m.persistMu.Unlock()
}

// Progress returns the outbound transfer progress under the per-Message persist
// mutex; see State for the locking rationale.
func (m *Message) Progress() float64 {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	return m.progress
}

// packedContainerLocked builds the msgpack container dict assuming the
// caller holds persistMu. WriteToDirectory uses it to avoid re-entering the
// lock it already holds.
func (m *Message) packedContainerLocked() ([]byte, error) {
	if len(m.Packed) == 0 {
		if err := m.Pack(); err != nil {
			return nil, err
		}
	}
	container := map[string]any{
		"state":                m.state,
		"lxmf_bytes":           m.Packed,
		"transport_encrypted":  m.TransportEncrypted,
		"transport_encryption": m.TransportEncryption,
		"method":               m.method,
	}
	return msgpack.Pack(container)
}

func (m *Message) packPropagated() error {
	if len(m.Packed) <= DestinationLength {
		return errors.New("packed lxmf message too short for propagated payload")
	}

	if m.Destination == nil {
		return errors.New("cannot pack propagated payload without destination")
	}

	if len(m.propagationEncryptedData) == 0 {
		encryptedData, err := m.Destination.Encrypt(m.Packed[DestinationLength:])
		if err != nil {
			return fmt.Errorf("encrypt propagated payload: %w", err)
		}
		m.propagationEncryptedData = encryptedData
		m.RatchetID = m.Destination.LatestRatchetID()
	}

	lxmfData := make([]byte, 0, DestinationLength+len(m.propagationEncryptedData))
	lxmfData = append(lxmfData, m.DestinationHash...)
	lxmfData = append(lxmfData, m.propagationEncryptedData...)
	m.TransientID = rns.FullHash(lxmfData)
	if len(m.PropagationStamp) > 0 {
		lxmfData = append(lxmfData, m.PropagationStamp...)
	}

	propagationPayload := []any{
		float64(time.Now().UnixNano()) / 1e9,
		[]any{cloneBytes(lxmfData)},
	}
	propagationPacked, err := msgpack.Pack(propagationPayload)
	if err != nil {
		return fmt.Errorf("pack propagated lxmf payload: %w", err)
	}
	m.PropagationPacked = propagationPacked

	m.method = MethodPropagated
	if len(m.PropagationPacked) <= LinkPacketMaxContent {
		m.Representation = RepresentationPacket
	} else {
		m.Representation = RepresentationResource
	}

	return nil
}

// DetermineTransportEncryption mirrors Python's transport-encryption labeling
// for the message's selected delivery method.
func (m *Message) DetermineTransportEncryption() {
	switch m.method {
	case MethodOpportunistic, MethodPropagated, MethodPaper:
		switch {
		case m.Destination != nil && m.Destination.Type == rns.DestinationSingle:
			m.TransportEncrypted = true
			m.TransportEncryption = EncryptionDescriptionEC
		case m.Destination != nil && m.Destination.Type == rns.DestinationGroup:
			m.TransportEncrypted = true
			m.TransportEncryption = EncryptionDescriptionAES
		default:
			m.TransportEncrypted = false
			m.TransportEncryption = EncryptionDescriptionUnencrypted
		}
	case MethodDirect:
		m.TransportEncrypted = true
		m.TransportEncryption = EncryptionDescriptionEC
	default:
		m.TransportEncrypted = false
		m.TransportEncryption = EncryptionDescriptionUnencrypted
	}
}

// DetermineCompressionSupport reconciles m.AutoCompress with the destination's
// recalled announce app-data, mirroring Python's
// LXMessage.determine_compression_support (LXMF/LXMessage.py:510-513, v1.1.0).
// When appData is empty (no recalled announce) compression defaults to
// supported; otherwise it follows the peer's supported-functionality list via
// CompressionSupportFromAppData. A malformed payload yields an error rather
// than a silent default.
func (m *Message) DetermineCompressionSupport(appData []byte) error {
	if len(appData) == 0 {
		m.AutoCompress = true
		return nil
	}
	supported, _, err := CompressionSupportFromAppData(appData)
	if err != nil {
		return fmt.Errorf("determine lxmf compression support: %w", err)
	}
	m.AutoCompress = supported
	return nil
}

func (m *Message) setDeliveryDestination(destination rns.PacketDestination) {
	m.deliveryDestination = destination
}

func (m *Message) asPacket() (*rns.Packet, error) {
	if len(m.Packed) == 0 {
		if err := m.Pack(); err != nil {
			return nil, err
		}
	}
	if m.deliveryDestination == nil {
		return nil, errors.New("can't synthesize packet for lxmf message before delivery destination is known")
	}

	switch m.method {
	case MethodOpportunistic:
		if len(m.Packed) <= DestinationLength {
			return nil, errors.New("packed lxmf message too short for packet encoding")
		}
		return rns.NewPacket(m.deliveryDestination, m.Packed[DestinationLength:]), nil
	case MethodDirect:
		return rns.NewPacket(m.deliveryDestination, m.Packed), nil
	case MethodPropagated:
		if len(m.PropagationPacked) == 0 {
			if err := m.packPropagated(); err != nil {
				return nil, err
			}
		}
		return rns.NewPacket(m.deliveryDestination, m.PropagationPacked), nil
	default:
		return nil, fmt.Errorf("unsupported lxmf packet method %v", m.method)
	}
}

func (m *Message) asResource() (*rns.Resource, error) {
	if len(m.Packed) == 0 {
		if err := m.Pack(); err != nil {
			return nil, err
		}
	}
	if m.deliveryDestination == nil {
		return nil, errors.New("can't synthesize resource for lxmf message before delivery destination is known")
	}

	link, ok := m.deliveryDestination.(*rns.Link)
	if !ok {
		return nil, errors.New("tried to synthesize resource for lxmf message on a delivery destination that was not a link")
	}
	if link.GetStatus() != rns.LinkActive {
		return nil, errors.New("tried to synthesize resource for lxmf message on a link that was not active")
	}

	switch m.method {
	case MethodDirect:
		// DIRECT-delivery resources carry auto_compress from the peer's
		// announced supported-functionality list, mirroring Python's
		// LXMessage.__as_resource (LXMF/LXMessage.py:654, v1.1.0). The
		// PROPAGATED branch below intentionally omits it, matching Python.
		return rns.NewResourceWithOptions(m.Packed, link, rns.ResourceOptions{AutoCompress: m.AutoCompress})
	case MethodPropagated:
		if len(m.PropagationPacked) == 0 {
			if err := m.packPropagated(); err != nil {
				return nil, err
			}
		}
		return rns.NewResource(m.PropagationPacked, link)
	default:
		return nil, fmt.Errorf("unsupported lxmf resource method %v", m.method)
	}
}

// WriteToDirectory writes the message to the given directory as a msgpack
// container file named by the message hash hex. This mirrors Python's
// LXMessage.write_to_directory() method.
// WriteToDirectory atomically persists the message's packed container to
// dirPath under a per-message persist mutex, mirroring Python's
// LXMessage.write_to_directory (LXMessage.py:674-694, v0.9.9). It writes to a
// unique ".tmp.<pid>.<rand>" path, flushes and fsyncs, then renames the file
// into place so a concurrent reader never observes a partial container. On
// any error the temporary file is removed.
func (m *Message) WriteToDirectory(dirPath string) (string, error) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()

	// Snapshot the container under the persist lock so it is consistent with
	// any concurrent mutation that also holds the lock (Python takes the
	// whole write under __persist_lock, LXMessage.py:679). This also packs
	// if needed, populating m.Hash for the file name.
	container, err := m.packedContainerLocked()
	if err != nil {
		return "", fmt.Errorf("pack container: %w", err)
	}

	fileName := fmt.Sprintf("%x", m.Hash)
	filePath := filepath.Join(dirPath, fileName)

	// Unique tmp path matching Python's
	// file_path+".tmp."+pid+"."+hex(urandom(8)) (LXMessage.py:677).
	var randBuf [8]byte
	if _, rerr := rand.Read(randBuf[:]); rerr != nil {
		return "", fmt.Errorf("generate tmp suffix: %w", rerr)
	}
	tmpPath := filePath + ".tmp." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(randBuf[:])

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("create tmp file %v: %w", tmpPath, err)
	}
	if _, werr := f.Write(container); werr != nil {
		_ = f.Close()
		removeTmpFile(tmpPath)
		return "", fmt.Errorf("write lxmf message to %v: %w", tmpPath, werr)
	}
	// Mirror Python: fsync failures are logged but do not abort the replace
	// (LXMessage.py:684-685).
	if serr := f.Sync(); serr != nil {
		log.Printf("Error while waiting for persist fsync for %x: %v", m.Hash, serr)
	}
	if cerr := f.Close(); cerr != nil {
		removeTmpFile(tmpPath)
		return "", fmt.Errorf("close tmp file %v: %w", tmpPath, cerr)
	}
	if rerr := os.Rename(tmpPath, filePath); rerr != nil {
		removeTmpFile(tmpPath)
		return "", fmt.Errorf("rename tmp file into place: %w", rerr)
	}
	return filePath, nil
}

// removeTmpFile removes a leftover temporary file, swallowing errors that
// Python also swallows (LXMessage.py:691-693), logging only unexpected ones.
func removeTmpFile(tmpPath string) {
	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		log.Printf("Error while cleaning temporary file %v: %v", tmpPath, err)
	}
}

// atomicWriteFile writes data to path via a unique temporary file followed by
// os.Rename, so a concurrent reader never observes a truncated or partial
// file: the destination is either the previous contents or the new contents
// in full. It does not fsync, mirroring Python's LXMRouter save methods, which
// build temp_path = write_path+".tmp."+str(time.time()) and os.replace it
// without fsync (LXMRouter.py:1231,1411, v1.1.0) — unlike
// LXMessage.write_to_directory, which fsyncs before the replace. mode is the
// file mode to create the temporary file with.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	var randBuf [8]byte
	if _, rerr := rand.Read(randBuf[:]); rerr != nil {
		return fmt.Errorf("generate tmp suffix: %w", rerr)
	}
	tmpPath := path + ".tmp." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(randBuf[:])
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create tmp file %v: %w", tmpPath, err)
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		removeTmpFile(tmpPath)
		return fmt.Errorf("write state file %v: %w", tmpPath, werr)
	}
	if cerr := f.Close(); cerr != nil {
		removeTmpFile(tmpPath)
		return fmt.Errorf("close tmp file %v: %w", tmpPath, cerr)
	}
	if rerr := os.Rename(tmpPath, path); rerr != nil {
		removeTmpFile(tmpPath)
		return fmt.Errorf("rename tmp file into place: %w", rerr)
	}
	return nil
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func (m *Message) resetPackedState(preservePropagationEncryptedData bool) {
	m.Payload = nil
	m.Hash = nil
	m.MessageID = nil
	m.Signature = nil
	m.Packed = nil
	m.TransientID = nil
	m.PropagationPacked = nil
	m.PacketRepresentation = nil
	m.ResourceRepresentation = nil
	if !preservePropagationEncryptedData {
		m.propagationEncryptedData = nil
	}
}

// ValidateStamp is the Go port of Python's LXMessage.validate_stamp.
// It first checks whether the message's stamp matches the truncated
// hash of any of the provided tickets, and if so returns true with the
// stamp value set to TicketCostValue. Otherwise it falls back to a
// workblock stamp check.
func (m *Message) ValidateStamp(targetCost int, tickets [][]byte) bool {
	if m == nil {
		return false
	}
	for _, ticket := range tickets {
		if len(ticket) == 0 || len(m.MessageID) == 0 {
			continue
		}
		material := make([]byte, 0, len(ticket)+len(m.MessageID))
		material = append(material, ticket...)
		material = append(material, m.MessageID...)
		if bytes.Equal(m.Stamp, rns.TruncatedHash(material)) {
			value := TicketCostValue
			m.StampValue = cloneOptionalInt(&value)
			return true
		}
	}
	if len(m.Stamp) == 0 {
		return false
	}
	workblock, err := StampWorkblock(m.MessageID, WorkblockExpandRounds)
	if err != nil {
		return false
	}
	if StampValid(m.Stamp, targetCost, workblock) {
		value := StampValue(workblock, m.Stamp)
		m.StampValue = cloneOptionalInt(&value)
		return true
	}
	return false
}

// AsURI returns the paper-message URI encoding of this message. The
// caller must have set DesiredMethod to MethodPaper and called Pack()
// first. This is the Go port of Python's LXMessage.as_uri.
func (m *Message) AsURI(finalise bool) (string, error) {
	if m == nil {
		return "", errors.New("nil message")
	}
	if len(m.Packed) == 0 {
		if err := m.Pack(); err != nil {
			return "", err
		}
	}
	if m.DesiredMethod != MethodPaper {
		return "", errors.New("attempt to represent LXM with non-paper delivery method as URI")
	}
	if m.method == MethodPaper && m.PaperPacked != nil {
		encoded := base64.RawURLEncoding.EncodeToString(m.PaperPacked)
		uri := URISchema + "://" + encoded
		if finalise {
			m.DetermineTransportEncryption()
			m.markPaperGenerated()
		}
		return uri, nil
	}
	return "", errors.New("paper-packed payload not yet generated; call Pack with MethodPaper first")
}

// markPaperGenerated mirrors Python's LXMessage.__mark_paper_generated: it
// records that a paper message (e.g. a QR code) was successfully generated by
// setting State to StatePaper (0x05, the PAPER method constant reused as a
// state), Progress to 1.0, and invoking the delivery callback if one is set.
// A panic in the callback is recovered so a misbehaving handler cannot abort
// generation, matching Python's try/except around the callback invocation.
func (m *Message) markPaperGenerated() {
	m.state = StatePaper
	m.progress = 1.0
	if m.DeliveryCallback != nil {
		defer func() { _ = recover() }()
		m.DeliveryCallback(m)
	}
}

// qrBoxSize is the number of image pixels per QR module. It matches the
// default box_size=10 used by Python's qrcode.make, so the returned image has
// the same pixel dimensions as the Python PIL image for an equivalent matrix.
const qrBoxSize = 10

// qrBorder is the quiet-zone width in modules around the QR matrix. Python's
// as_qr calls qrcode.make(border=1, ...), giving a 1-module border on each
// side.
const qrBorder = 1

// AsQR generates a QR-code image of the paper-message URI, mirroring Python's
// LXMessage.as_qr (LXMessage.py:718-738). It packs the message if needed,
// requires DesiredMethod == MethodPaper with a populated PaperPacked payload,
// encodes AsURI(false) at error-correction level L (QRErrorCorrection) with a
// 1-module quiet zone, then applies DetermineTransportEncryption +
// markPaperGenerated and returns the image.
//
// Border nuance: Python calls qrcode.make(border=1, ...) (a 1-module quiet
// zone), while the vendored rsc.io/qr encoder's Code.Image() hard-codes a
// 4-module-per-side quiet zone with no knob. To match Python's geometry this
// renders the image from the raw Code.Black(x,y) matrix over [0,Size) with a
// 1-module border rather than calling Code.Image() directly.
func (m *Message) AsQR() (image.Image, error) {
	if m == nil {
		return nil, errors.New("nil message")
	}
	if len(m.Packed) == 0 {
		if err := m.Pack(); err != nil {
			return nil, err
		}
	}
	if m.DesiredMethod != MethodPaper || m.PaperPacked == nil {
		return nil, errors.New("attempt to represent LXM with non-paper delivery method as QR-code")
	}

	uri, err := m.AsURI(false)
	if err != nil {
		return nil, err
	}

	code, err := qr.Encode(uri, qr.L)
	if err != nil {
		return nil, fmt.Errorf("qr encode: %w", err)
	}

	side := code.Size + 2*qrBorder
	pixSide := side * qrBoxSize
	img := image.NewGray(image.Rect(0, 0, pixSide, pixSide))
	// image.NewGray zeroes Pix to 0 (black); fill the whole canvas white first
	// so only the black modules are drawn on a white quiet zone.
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if !code.Black(x, y) {
				continue
			}
			x0 := (qrBorder + x) * qrBoxSize
			y0 := (qrBorder + y) * qrBoxSize
			for dy := range qrBoxSize {
				rowStart := (y0 + dy) * img.Stride
				row := img.Pix[rowStart+x0 : rowStart+x0+qrBoxSize]
				for dx := range row {
					row[dx] = 0x00
				}
			}
		}
	}

	m.DetermineTransportEncryption()
	m.markPaperGenerated()

	return img, nil
}
