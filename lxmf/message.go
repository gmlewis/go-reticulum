// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

type Message struct {
	Destination     *rns.Destination
	Source          *rns.Destination
	DestinationHash []byte
	SourceHash      []byte

	Title   []byte
	Content []byte
	Fields  map[any]any

	Timestamp float64
	Stamp     []byte

	Payload []any

	Hash      []byte
	MessageID []byte
	Signature []byte
	Packed    []byte

	State int

	DesiredMethod  int
	Method         int
	Representation int

	DeliveryAttempts    int
	NextDeliveryAttempt float64

	Incoming           bool
	SignatureValidated bool
	UnverifiedReason   int

	TryPropagationOnFail bool
	IncludeTicket        bool

	DeliveryCallback func(*Message)
	FailedCallback   func(*Message)
}

func NewMessage(destination, source *rns.Destination, content, title string, fields map[any]any) (*Message, error) {
	if destination == nil {
		return nil, errors.New("lxmf destination is required")
	}
	if source == nil {
		return nil, errors.New("lxmf source is required")
	}

	m := &Message{
		Destination:     destination,
		Source:          source,
		DestinationHash: cloneBytes(destination.Hash),
		SourceHash:      cloneBytes(source.Hash),
		Title:           []byte(title),
		Content:         []byte(content),
		Fields:          ensureFields(fields),
		State:           StateGenerating,
		DesiredMethod:   MethodDirect,
		Method:          RepresentationUnknown,
		Representation:  RepresentationUnknown,
	}

	return m, nil
}

func (m *Message) SetTitleString(title string) {
	m.Title = []byte(title)
}

func (m *Message) SetContentString(content string) {
	m.Content = []byte(content)
}

func (m *Message) TitleString() string {
	return string(m.Title)
}

func (m *Message) ContentString() string {
	return string(m.Content)
}

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

	m.Payload = []any{m.Timestamp, m.Title, m.Content, ensureFields(m.Fields)}
	if len(m.Stamp) > 0 {
		m.Payload = append(m.Payload, cloneBytes(m.Stamp))
	}

	packedPayload, err := msgpack.Pack(m.Payload)
	if err != nil {
		return fmt.Errorf("pack lxmf payload: %w", err)
	}

	hashedPart := make([]byte, 0, len(m.DestinationHash)+len(m.SourceHash)+len(packedPayload))
	hashedPart = append(hashedPart, m.DestinationHash...)
	hashedPart = append(hashedPart, m.SourceHash...)
	hashedPart = append(hashedPart, packedPayload...)

	m.Hash = rns.FullHash(hashedPart)
	m.MessageID = cloneBytes(m.Hash)

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

	m.Packed = make([]byte, 0, len(m.DestinationHash)+len(m.SourceHash)+len(m.Signature)+len(packedPayload))
	m.Packed = append(m.Packed, m.DestinationHash...)
	m.Packed = append(m.Packed, m.SourceHash...)
	m.Packed = append(m.Packed, m.Signature...)
	m.Packed = append(m.Packed, packedPayload...)

	return nil
}

func UnpackMessageFromBytes(data []byte, originalMethod int) (*Message, error) {
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

	destination := recalledDeliveryDestination(destinationHash)
	source := recalledDeliveryDestination(sourceHash)

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

func ensureFields(fields map[any]any) map[any]any {
	if fields == nil {
		return map[any]any{}
	}
	return fields
}

func payloadTimestamp(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case int:
		return float64(t), nil
	case int32:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case uint:
		return float64(t), nil
	case uint32:
		return float64(t), nil
	case uint64:
		return float64(t), nil
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

func recalledDeliveryDestination(destHash []byte) *rns.Destination {
	identity := rns.Recall(destHash, false)
	if identity == nil {
		return nil
	}

	d, err := rns.NewDestination(identity, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
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
		"transport_encrypted":  false,
		"transport_encryption": 0,
		"method":               m.Method,
	}
	return msgpack.Pack(container)
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
