// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import "github.com/gmlewis/go-reticulum/rns"

const (
	// AppName defines the core application name for LXMF routing and addressing.
	AppName = "lxmf"
	// PNMetaName matches Python's LXMF.PN_META_NAME metadata key.
	PNMetaName = 0x01

	// FieldEmbeddedLXMS indicates an embedded LXMS field type in an LXMF message.
	FieldEmbeddedLXMS = 0x01
	// FieldTelemetry represents a telemetry data field in the message payload.
	FieldTelemetry = 0x02
	// FieldTelemetryStream represents a continuous telemetry stream field.
	FieldTelemetryStream = 0x03
	// FieldIconAppearance specifies icon appearance data for UI representation.
	FieldIconAppearance = 0x04
	// FieldFileAttachments indicates the presence of file attachments in the message.
	FieldFileAttachments = 0x05
	// FieldImage represents an embedded image field.
	FieldImage = 0x06
	// FieldAudio represents an embedded audio field.
	FieldAudio = 0x07
	// FieldThread specifies message threading information to group conversations.
	FieldThread = 0x08
	// FieldCommands defines an executable command field within the message.
	FieldCommands = 0x09
	// FieldResults represents the results of a previously executed command.
	FieldResults = 0x0A
	// FieldGroup specifies group chat metadata for the message.
	FieldGroup = 0x0B
	// FieldTicket contains ticket data for paid or stamped message delivery.
	FieldTicket = 0x0C
	// FieldEvent represents a calendar or scheduling event field.
	FieldEvent = 0x0D
	// FieldRNRRefs contains Reticulum Name Resolution references.
	FieldRNRRefs = 0x0E
	// FieldRenderer specifies custom rendering instructions for the message.
	FieldRenderer = 0x0F

	// FieldCustomType allows for user-defined custom field types.
	FieldCustomType = 0xFB
	// FieldCustomData allows for user-defined custom data payloads.
	FieldCustomData = 0xFC
	// FieldCustomMeta allows for user-defined custom metadata.
	FieldCustomMeta = 0xFD

	// FieldNonSpecific acts as a catch-all for non-specific field types.
	FieldNonSpecific = 0xFE
	// FieldDebug is reserved for debugging and diagnostic field data.
	FieldDebug = 0xFF
)

const (
	// StateGenerating indicates that the message is currently being constructed.
	StateGenerating = 0x00
	// StateOutbound indicates that the message is ready and queued for outbound delivery.
	StateOutbound = 0x01
	// StateSending indicates that the message is actively being transmitted over the network.
	StateSending = 0x02
	// StateSent confirms that the message has been successfully sent to the next hop.
	StateSent = 0x04
	// StateDelivered confirms that the destination has successfully received the message.
	StateDelivered = 0x08
	// StateRejected indicates that the destination or a relay rejected the message.
	StateRejected = 0xFD
	// StateCancelled indicates that the message delivery was aborted locally.
	StateCancelled = 0xFE
	// StateFailed indicates that the message delivery failed after exhausting all retries.
	StateFailed = 0xFF
)

const (
	// RepresentationUnknown indicates that the optimal transport representation is not yet determined.
	RepresentationUnknown = 0x00
	// RepresentationPacket specifies that the message fits within a single Reticulum packet.
	RepresentationPacket = 0x01
	// RepresentationResource specifies that the message requires a multi-packet Reticulum resource for transfer.
	RepresentationResource = 0x02
)

const (
	// MethodOpportunistic attempts delivery with minimal overhead without establishing a guaranteed link.
	MethodOpportunistic = 0x01
	// MethodDirect establishes a direct Reticulum link to the destination for reliable delivery.
	MethodDirect = 0x02
	// MethodPropagated leverages propagation nodes for asynchronous store-and-forward delivery.
	MethodPropagated = 0x03
	// MethodPaper represents a message encoded for physical media transport like QR codes.
	MethodPaper = 0x05
)

const (
	// ReasonSourceUnknown indicates a message was rejected because its source identity could not be verified.
	ReasonSourceUnknown = 0x01
	// ReasonSignatureInvalid indicates a message was rejected due to cryptographic signature validation failure.
	ReasonSignatureInvalid = 0x02
)

const (
	// DestinationLength defines the byte length of a standard Reticulum truncated destination hash.
	DestinationLength = rns.TruncatedHashLength / 8
	// SignatureLength defines the byte length of an Ed25519 signature used in LXMF messages.
	SignatureLength = 64
	// StampSize defines the byte length of an anti-spam hashcash stamp.
	StampSize = 32
	// TicketLength defines the byte length of a delivery ticket.
	TicketLength = rns.TruncatedHashLength / 8
)

const (
	// TimestampSize mirrors Python's fixed LXMF timestamp size accounting.
	TimestampSize = 8
	// StructOverhead mirrors Python's msgpack structure overhead accounting.
	StructOverhead = 8
	// LXMFOverhead is Python's fixed non-content overhead for an LXMF payload.
	LXMFOverhead = (2 * DestinationLength) + SignatureLength + TimestampSize + StructOverhead
	// LinkPacketMaxContent matches Python's single-packet content limit over a link.
	LinkPacketMaxContent = rns.MDU - LXMFOverhead
)

const (
	// EncryptionDescriptionAES matches Python's group-transport encryption label.
	EncryptionDescriptionAES = "AES-128"
	// EncryptionDescriptionEC matches Python's public-key transport encryption label.
	EncryptionDescriptionEC = "Curve25519"
	// EncryptionDescriptionUnencrypted matches Python's plaintext transport label.
	EncryptionDescriptionUnencrypted = "Unencrypted"
)

const (
	// PRIdle indicates that the propagation node sync process is currently inactive.
	PRIdle = 0x00
	// PRPathRequested indicates that a path to the propagation node has been requested from the network.
	PRPathRequested = 0x01
	// PRLinkEstablishing indicates that a link to the propagation node is currently being established.
	PRLinkEstablishing = 0x02
	// PRLinkEstablished indicates that a link to the propagation node has been successfully established.
	PRLinkEstablished = 0x03
	// PRRequestSent indicates that the sync request has been transmitted to the propagation node.
	PRRequestSent = 0x04
	// PRReceiving indicates that the local node is actively receiving sync data from the propagation node.
	PRReceiving = 0x05
	// PRResponseReceived indicates that the sync response has been fully received.
	PRResponseReceived = 0x06
	// PRComplete indicates that the propagation node sync process completed successfully.
	PRComplete = 0x07
	// PRNoPath indicates that the sync failed because no network path to the propagation node could be found.
	PRNoPath = 0xf0
	// PRLinkFailed indicates that the sync failed because the link to the propagation node could not be established.
	PRLinkFailed = 0xf1
	// PRTransferFailed indicates that the sync failed during data transfer.
	PRTransferFailed = 0xf2
	// PRNoIdentityRcvd indicates that the sync failed because the remote identity could not be resolved.
	PRNoIdentityRcvd = 0xf3
	// PRNoAccess indicates that the sync was denied due to missing authorization or permissions.
	PRNoAccess = 0xf4
	// PRFailed is a generic error state indicating that the propagation node sync failed.
	PRFailed = 0xfe
)

const (
	// WorkblockExpandRounds defines the default number of expansion rounds for anti-spam workblocks.
	WorkblockExpandRounds = 3000
	// WorkblockExpandRoundsPN defines the expansion rounds for propagation node anti-spam workblocks.
	WorkblockExpandRoundsPN = 1000
	// WorkblockExpandRoundsPeering defines the expansion rounds for peering key anti-spam workblocks.
	WorkblockExpandRoundsPeering = 25
	// PNValidationPoolMinSize sets the minimum pool size for propagation node validation operations.
	PNValidationPoolMinSize = 256
	// DefaultStampTimeoutSeconds specifies the default expiration time for a generated stamp, in seconds.
	DefaultStampTimeoutSeconds = 0
	// DefaultTicketExpirySeconds defines the default lifespan of a generated delivery ticket.
	DefaultTicketExpirySeconds = 21 * 24 * 60 * 60
	// DefaultTicketGraceSeconds defines the default grace period for an expired ticket before it is fully invalid.
	DefaultTicketGraceSeconds = 5 * 24 * 60 * 60
	// DefaultTicketRenewSeconds defines the threshold at which a ticket should be proactively renewed.
	DefaultTicketRenewSeconds = 14 * 24 * 60 * 60
	// DefaultTicketIntervalSeconds specifies the minimum interval between issuing tickets to the same destination.
	DefaultTicketIntervalSeconds = 1 * 24 * 60 * 60
	// TicketCostValue sets the nominal computational cost value required to generate a valid ticket.
	TicketCostValue = 0x100
)
