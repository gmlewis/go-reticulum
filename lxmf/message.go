// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

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
	// DeferPropagationStamp mirrors Python's propagation-node stamp deferral
	// flag, even though propagated deferred stamping is not yet fully wired.
	DeferPropagationStamp bool

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

	// State tracks the current lifecycle state of the message.
	State int

	// DesiredMethod is the preferred delivery method requested by the sender.
	DesiredMethod int
	// Method is the delivery method actually used or observed.
	Method int
	// Representation records whether the message traveled as a packet or as a
	// resource.
	Representation int
	// Progress tracks Python-style outbound transfer progress in the range
	// 0.0-1.0.
	Progress float64
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
		State:                 StateGenerating,
		DesiredMethod:         MethodDirect,
		Method:                RepresentationUnknown,
		Representation:        RepresentationUnknown,
		DeferStamp:            true,
		DeferPropagationStamp: true,
	}

	return m, nil
}

// GetStamp returns the current delivery stamp, generating it if needed from an
// outbound ticket or the configured stamp cost.
func (m *Message) GetStamp() ([]byte, error) {
	if len(m.OutboundTicket) == TicketLength && len(m.MessageID) > 0 {
		material := make([]byte, 0, len(m.OutboundTicket)+len(m.MessageID))
		material = append(material, m.OutboundTicket...)
		material = append(material, m.MessageID...)
		stampValue := TicketCostValue
		m.StampValue = cloneOptionalInt(&stampValue)
		return rns.TruncatedHash(material), nil
	}

	if m.StampCost == nil || *m.StampCost <= 0 {
		m.StampValue = nil
		return nil, nil
	}
	if len(m.Stamp) > 0 {
		return cloneBytes(m.Stamp), nil
	}

	stamp, stampValue, _, err := GenerateStamp(m.MessageID, *m.StampCost, WorkblockExpandRounds)
	if err != nil {
		return nil, err
	}
	m.StampValue = cloneOptionalInt(&stampValue)
	return stamp, nil
}

// GetPropagationStamp returns the current propagated-delivery stamp, generating
// it if needed for the configured propagation-node target cost.
func (m *Message) GetPropagationStamp(targetCost int) ([]byte, error) {
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

	stamp, stampValue, _, err := GenerateStamp(m.TransientID, *m.PropagationTargetCost, WorkblockExpandRoundsPN)
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

	packedPayload, err := msgpack.Pack(basePayload)
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

	packedPayload, err = msgpack.Pack(m.Payload)
	if err != nil {
		return fmt.Errorf("pack stamped lxmf payload: %w", err)
	}

	m.Packed = make([]byte, 0, len(m.DestinationHash)+len(m.SourceHash)+len(m.Signature)+len(packedPayload))
	m.Packed = append(m.Packed, m.DestinationHash...)
	m.Packed = append(m.Packed, m.SourceHash...)
	m.Packed = append(m.Packed, m.Signature...)
	m.Packed = append(m.Packed, packedPayload...)

	if m.DesiredMethod == MethodPropagated {
		if err := m.packPropagated(); err != nil {
			return err
		}
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

	unpackedPayloadAny, err := msgpack.Unpack(packedPayload)
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
	repackedPayload, err := msgpack.Pack(payloadWithoutStamp)
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
		State:           originalMethod,
		Method:          originalMethod,
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
		message.State = parsedState
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
		message.Method = parsedMethod
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
	m, ok := v.(map[any]any)
	if !ok {
		return nil, fmt.Errorf("invalid lxmf fields type %T", v)
	}
	return m, nil
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
	identity := ts.Recall(destHash)
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
// matching Python's LXMessage.packed_container() method.
func (m *Message) PackedContainer() ([]byte, error) {
	if len(m.Packed) == 0 {
		if err := m.Pack(); err != nil {
			return nil, err
		}
	}
	container := map[string]any{
		"state":                m.State,
		"lxmf_bytes":           m.Packed,
		"transport_encrypted":  m.TransportEncrypted,
		"transport_encryption": m.TransportEncryption,
		"method":               m.Method,
	}
	return msgpack.Pack(container)
}

func (m *Message) packPropagated() error {
	if len(m.Packed) <= DestinationLength {
		return errors.New("packed lxmf message too short for propagated payload")
	}

	if len(m.propagationEncryptedData) == 0 {
		encryptedData, err := m.Destination.Encrypt(m.Packed[DestinationLength:])
		if err != nil {
			return fmt.Errorf("encrypt propagated payload: %w", err)
		}
		m.propagationEncryptedData = encryptedData
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

	m.Method = MethodPropagated
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
	switch m.Method {
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

	switch m.Method {
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
		return nil, fmt.Errorf("unsupported lxmf packet method %v", m.Method)
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

	switch m.Method {
	case MethodDirect:
		return rns.NewResource(m.Packed, link)
	case MethodPropagated:
		if len(m.PropagationPacked) == 0 {
			if err := m.packPropagated(); err != nil {
				return nil, err
			}
		}
		return rns.NewResource(m.PropagationPacked, link)
	default:
		return nil, fmt.Errorf("unsupported lxmf resource method %v", m.Method)
	}
}

// WriteToDirectory writes the message to the given directory as a msgpack
// container file named by the message hash hex. This mirrors Python's
// LXMessage.write_to_directory() method.
func (m *Message) WriteToDirectory(dirPath string) (string, error) {
	container, err := m.PackedContainer()
	if err != nil {
		return "", fmt.Errorf("pack container: %w", err)
	}
	fileName := fmt.Sprintf("%x", m.Hash)
	filePath := dirPath + "/" + fileName
	if err := os.WriteFile(filePath, container, 0o600); err != nil {
		return "", fmt.Errorf("write lxmf message to %v: %w", filePath, err)
	}
	return filePath, nil
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
