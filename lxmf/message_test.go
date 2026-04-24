// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/gmlewis/go-reticulum/rns"
	rnscrypto "github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestMessagePackUnpackRoundTrip(t *testing.T) {
	t.Parallel()
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}

	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	fields := map[any]any{int64(FieldDebug): []byte("debug-data")}
	m := mustTestNewMessage(t, destination, source, "hello-content", "hello-title", fields)

	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if len(m.Packed) == 0 {
		t.Fatal("expected packed message bytes")
	}
	if len(m.Signature) != SignatureLength {
		t.Fatalf("signature length=%v want=%v", len(m.Signature), SignatureLength)
	}

	unpacked, err := UnpackMessageFromBytes(ts, m.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}

	if !bytes.Equal(unpacked.DestinationHash, m.DestinationHash) {
		t.Fatalf("destination hash mismatch")
	}
	if !bytes.Equal(unpacked.SourceHash, m.SourceHash) {
		t.Fatalf("source hash mismatch")
	}
	if unpacked.TitleString() != "hello-title" {
		t.Fatalf("title=%q want=%q", unpacked.TitleString(), "hello-title")
	}
	if unpacked.ContentString() != "hello-content" {
		t.Fatalf("content=%q want=%q", unpacked.ContentString(), "hello-content")
	}
	if !unpacked.SignatureValidated {
		t.Fatalf("expected signature to validate, reason=%v", unpacked.UnverifiedReason)
	}
	if got, ok := unpacked.Fields[int64(FieldDebug)].([]byte); !ok || !bytes.Equal(got, []byte("debug-data")) {
		t.Fatalf("fields mismatch: %#v", unpacked.Fields)
	}
}

func TestMessagePackIncludesStampAndUnpacksIt(t *testing.T) {
	t.Parallel()
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	m := mustTestNewMessage(t, destination, source, "content", "title", nil)
	stampCost := 4
	m.StampCost = &stampCost
	m.Stamp = []byte{0xAA, 0xBB, 0xCC}
	m.DeferStamp = false

	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	unpacked, err := UnpackMessageFromBytes(ts, m.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}

	if !bytes.Equal(unpacked.Stamp, []byte{0xAA, 0xBB, 0xCC}) {
		t.Fatalf("stamp mismatch: %x", unpacked.Stamp)
	}
	if !unpacked.SignatureValidated {
		t.Fatalf("expected stamped message signature to validate, reason=%v", unpacked.UnverifiedReason)
	}
	if len(unpacked.Payload) != 4 {
		t.Fatalf("unpacked payload length=%v want=4", len(unpacked.Payload))
	}
}

func TestMessageHashMatchesProtocolMaterial(t *testing.T) {
	t.Parallel()
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}

	m := mustTestNewMessage(t, destination, source, "abc", "def", map[any]any{})
	m.Timestamp = 1700000000.25

	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	payload, err := msgpack.Pack([]any{m.Timestamp, []byte("def"), []byte("abc"), map[any]any{}})
	if err != nil {
		t.Fatalf("Pack(payload): %v", err)
	}

	hashMaterial := make([]byte, 0, len(destination.Hash)+len(source.Hash)+len(payload))
	hashMaterial = append(hashMaterial, destination.Hash...)
	hashMaterial = append(hashMaterial, source.Hash...)
	hashMaterial = append(hashMaterial, payload...)

	wantHash := rns.FullHash(hashMaterial)
	if !bytes.Equal(m.Hash, wantHash) {
		t.Fatalf("hash mismatch\n got: %x\nwant: %x", m.Hash, wantHash)
	}
}

func TestWriteToDirectory(t *testing.T) {
	t.Parallel()
	destID, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	srcID, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	ts := rns.NewTransportSystem(nil)
	dest, err := rns.NewDestination(ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	mustTest(t, err)
	src, err := rns.NewDestination(ts, srcID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	mustTest(t, err)

	msg := mustTestNewMessage(t, dest, src, "hello", "greet", nil)

	dir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	path, err := msg.WriteToDirectory(dir)
	if err != nil {
		t.Fatalf("WriteToDirectory error = %v", err)
	}

	wantPath := dir + "/" + fmt.Sprintf("%x", msg.Hash)
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file error = %v", err)
	}

	// Verify it's valid msgpack with expected keys.
	v, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("unpack error = %v", err)
	}
	m, ok := v.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", v)
	}
	if _, ok := m["lxmf_bytes"]; !ok {
		t.Fatalf("missing 'lxmf_bytes' key in container")
	}
	if _, ok := m["state"]; !ok {
		t.Fatalf("missing 'state' key in container")
	}
	if _, ok := m["method"]; !ok {
		t.Fatalf("missing 'method' key in container")
	}
}

func TestMessagePropagatedPackProducesPropagationWireFormat(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	message := mustTestNewMessage(t, destination, source, "propagated-content", "propagated-title", map[any]any{FieldDebug: []byte("pn")})
	message.DesiredMethod = MethodPropagated
	message.Timestamp = 1700000000.25

	before := float64(time.Now().Add(-time.Second).UnixNano()) / 1e9
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	after := float64(time.Now().Add(time.Second).UnixNano()) / 1e9

	if got, want := message.Method, MethodPropagated; got != want {
		t.Fatalf("method=%v want=%v", got, want)
	}
	if got, want := message.Representation, RepresentationPacket; got != want {
		t.Fatalf("representation=%v want=%v", got, want)
	}
	if len(message.PropagationPacked) == 0 {
		t.Fatal("expected propagation-packed bytes")
	}
	if len(message.TransientID) == 0 {
		t.Fatal("expected propagated transient id")
	}

	unpackedAny, err := msgpack.Unpack(message.PropagationPacked)
	if err != nil {
		t.Fatalf("Unpack(propagation packed): %v", err)
	}
	unpacked, ok := unpackedAny.([]any)
	if !ok {
		t.Fatalf("propagation packed type=%T want []any", unpackedAny)
	}
	if len(unpacked) != 2 {
		t.Fatalf("propagation packed length=%v want=2", len(unpacked))
	}

	propagationTimestamp, err := payloadTimestamp(unpacked[0])
	if err != nil {
		t.Fatalf("payloadTimestamp: %v", err)
	}
	if propagationTimestamp < before || propagationTimestamp > after {
		t.Fatalf("propagation timestamp=%v outside [%v,%v]", propagationTimestamp, before, after)
	}

	items, ok := unpacked[1].([]any)
	if !ok {
		t.Fatalf("propagation entries type=%T want []any", unpacked[1])
	}
	if len(items) != 1 {
		t.Fatalf("propagation entries length=%v want=1", len(items))
	}
	lxmfData, ok := items[0].([]byte)
	if !ok {
		t.Fatalf("propagated lxmf data type=%T want []byte", items[0])
	}
	if len(lxmfData) <= DestinationLength {
		t.Fatalf("propagated lxmf data length=%v want > %v", len(lxmfData), DestinationLength)
	}
	if !bytes.Equal(lxmfData[:DestinationLength], message.DestinationHash) {
		t.Fatalf("propagated destination hash mismatch")
	}
	if got, want := message.TransientID, rns.FullHash(lxmfData); !bytes.Equal(got, want) {
		t.Fatalf("transient id=%x want=%x", got, want)
	}

	decrypted, err := destination.Decrypt(lxmfData[DestinationLength:])
	if err != nil {
		t.Fatalf("Decrypt propagated payload: %v", err)
	}
	if got, want := decrypted, message.Packed[DestinationLength:]; !bytes.Equal(got, want) {
		t.Fatalf("decrypted propagated payload mismatch")
	}
}

func TestDetermineTransportEncryption(t *testing.T) {
	t.Parallel()

	sourceID := mustTestNewIdentity(t, true)
	groupID := mustTestNewIdentity(t, true)
	singleID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	singleDestination := mustTestNewDestination(t, ts, singleID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	groupDestination := mustTestNewDestination(t, ts, groupID, rns.DestinationOut, rns.DestinationGroup, AppName, "delivery")
	plainDestination := mustTestNewDestination(t, ts, nil, rns.DestinationOut, rns.DestinationPlain, AppName, "delivery")

	testCases := []struct {
		name      string
		method    int
		dest      *rns.Destination
		encrypted bool
		mode      string
	}{
		{name: "propagated single", method: MethodPropagated, dest: singleDestination, encrypted: true, mode: EncryptionDescriptionEC},
		{name: "propagated group", method: MethodPropagated, dest: groupDestination, encrypted: true, mode: EncryptionDescriptionAES},
		{name: "propagated plain", method: MethodPropagated, dest: plainDestination, encrypted: false, mode: EncryptionDescriptionUnencrypted},
		{name: "direct", method: MethodDirect, dest: singleDestination, encrypted: true, mode: EncryptionDescriptionEC},
		{name: "unknown", method: RepresentationUnknown, dest: singleDestination, encrypted: false, mode: EncryptionDescriptionUnencrypted},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			message := mustTestNewMessage(t, tc.dest, source, "content", "title", nil)
			message.Method = tc.method

			message.DetermineTransportEncryption()

			if got, want := message.TransportEncrypted, tc.encrypted; got != want {
				t.Fatalf("transport encrypted=%v want=%v", got, want)
			}
			if got, want := message.TransportEncryption, tc.mode; got != want {
				t.Fatalf("transport encryption=%q want=%q", got, want)
			}
		})
	}
}

func TestPackedContainerUsesTransportState(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	message := mustTestNewMessage(t, destination, source, "container-content", "container-title", nil)
	message.DesiredMethod = MethodPropagated
	message.State = StateSent
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	message.DetermineTransportEncryption()

	containerBytes, err := message.PackedContainer()
	if err != nil {
		t.Fatalf("PackedContainer: %v", err)
	}

	unpackedAny, err := msgpack.Unpack(containerBytes)
	if err != nil {
		t.Fatalf("Unpack(container): %v", err)
	}
	container, ok := unpackedAny.(map[any]any)
	if !ok {
		t.Fatalf("container type=%T want map[any]any", unpackedAny)
	}

	if got, want := container["state"], int64(StateSent); got != want {
		t.Fatalf("container state=%#v want=%#v", got, want)
	}
	if got, want := container["lxmf_bytes"], message.Packed; !bytes.Equal(got.([]byte), want) {
		t.Fatalf("container lxmf bytes mismatch")
	}
	if got, want := container["transport_encrypted"], true; got != want {
		t.Fatalf("container transport_encrypted=%#v want=%#v", got, want)
	}
	if got, want := container["transport_encryption"], EncryptionDescriptionEC; got != want {
		t.Fatalf("container transport_encryption=%#v want=%#v", got, want)
	}
	if got, want := container["method"], int64(MethodPropagated); got != want {
		t.Fatalf("container method=%#v want=%#v", got, want)
	}
}

func TestMessageDeliveryDestination(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	message := mustTestNewMessage(t, destination, source, "packet-content", "packet-title", nil)
	message.DesiredMethod = MethodPropagated
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if _, err := message.asPacket(); err == nil {
		t.Fatal("expected packet synthesis without delivery destination to fail")
	}

	message.setDeliveryDestination(destination)

	packet, err := message.asPacket()
	if err != nil {
		t.Fatalf("asPacket: %v", err)
	}
	if packet.Destination != destination {
		t.Fatal("expected delivery destination override to be used")
	}
}

func TestMessagePropagatedAsPacket(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	message := mustTestNewMessage(t, destination, source, "packet-content", "packet-title", nil)
	message.DesiredMethod = MethodPropagated
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	message.setDeliveryDestination(destination)

	packet, err := message.asPacket()
	if err != nil {
		t.Fatalf("asPacket: %v", err)
	}

	if got, want := packet.Data, message.PropagationPacked; !bytes.Equal(got, want) {
		t.Fatalf("packet data mismatch")
	}
}

func TestMessagePropagatedAsResource(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	message := mustTestNewMessage(t, destination, source, strings.Repeat("resource-content", 40), "resource-title", nil)
	message.DesiredMethod = MethodPropagated
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	link, err := rns.NewLink(ts, destination)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	activateLink(t, link)
	message.setDeliveryDestination(link)

	resource, err := message.asResource()
	if err != nil {
		t.Fatalf("asResource: %v", err)
	}
	if resource == nil {
		t.Fatal("expected propagated resource")
	}
	if got, want := resource.Status(), rns.ResourceStatusQueued; got != want {
		t.Fatalf("resource status=%v want=%v", got, want)
	}
}

func TestMessageLinkGuards(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	message := mustTestNewMessage(t, destination, source, strings.Repeat("resource-content", 40), "resource-title", nil)
	message.DesiredMethod = MethodPropagated
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	message.setDeliveryDestination(destination)
	if _, err := message.asResource(); err == nil || !strings.Contains(err.Error(), "not a link") {
		t.Fatalf("asResource non-link error=%v, want not-a-link", err)
	}

	link, err := rns.NewLink(ts, destination)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	message.setDeliveryDestination(link)
	if _, err := message.asResource(); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("asResource inactive-link error=%v, want inactive-link", err)
	}
}

func setLinkStatus(t *testing.T, link *rns.Link, status int) {
	t.Helper()
	field := reflect.ValueOf(link).Elem().FieldByName("status")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetInt(int64(status))
}

func activateLink(t *testing.T, link *rns.Link) {
	t.Helper()
	setLinkStatus(t, link, rns.LinkActive)

	token, err := rnscrypto.NewToken(bytes.Repeat([]byte{0xA5}, 32))
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	field := reflect.ValueOf(link).Elem().FieldByName("token")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(token))
}
