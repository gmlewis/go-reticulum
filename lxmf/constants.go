// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import "github.com/gmlewis/go-reticulum/rns"

const (
	AppName = "lxmf"

	FieldEmbeddedLXMS    = 0x01
	FieldTelemetry       = 0x02
	FieldTelemetryStream = 0x03
	FieldIconAppearance  = 0x04
	FieldFileAttachments = 0x05
	FieldImage           = 0x06
	FieldAudio           = 0x07
	FieldThread          = 0x08
	FieldCommands        = 0x09
	FieldResults         = 0x0A
	FieldGroup           = 0x0B
	FieldTicket          = 0x0C
	FieldEvent           = 0x0D
	FieldRNRRefs         = 0x0E
	FieldRenderer        = 0x0F

	FieldCustomType = 0xFB
	FieldCustomData = 0xFC
	FieldCustomMeta = 0xFD

	FieldNonSpecific = 0xFE
	FieldDebug       = 0xFF
)

const (
	StateGenerating = 0x00
	StateOutbound   = 0x01
	StateSending    = 0x02
	StateSent       = 0x04
	StateDelivered  = 0x08
	StateRejected   = 0xFD
	StateCancelled  = 0xFE
	StateFailed     = 0xFF
)

const (
	RepresentationUnknown  = 0x00
	RepresentationPacket   = 0x01
	RepresentationResource = 0x02
)

const (
	MethodOpportunistic = 0x01
	MethodDirect        = 0x02
	MethodPropagated    = 0x03
	MethodPaper         = 0x05
)

const (
	ReasonSourceUnknown    = 0x01
	ReasonSignatureInvalid = 0x02
)

const (
	DestinationLength = rns.TruncatedHashLength / 8
	SignatureLength   = 64
	StampSize         = 32
	TicketLength      = rns.TruncatedHashLength / 8
)

const (
	PRIdle             = 0x00
	PRPathRequested    = 0x01
	PRLinkEstablishing = 0x02
	PRLinkEstablished  = 0x03
	PRRequestSent      = 0x04
	PRReceiving        = 0x05
	PRResponseReceived = 0x06
	PRComplete         = 0x07
	PRNoPath           = 0xf0
	PRLinkFailed       = 0xf1
	PRTransferFailed   = 0xf2
	PRNoIdentityRcvd   = 0xf3
	PRNoAccess         = 0xf4
	PRFailed           = 0xfe
)

const (
	WorkblockExpandRounds        = 3000
	WorkblockExpandRoundsPN      = 1000
	WorkblockExpandRoundsPeering = 25
	PNValidationPoolMinSize      = 256
	DefaultStampTimeoutSeconds   = 0
	DefaultTicketExpirySeconds   = 21 * 24 * 60 * 60
	DefaultTicketGraceSeconds    = 5 * 24 * 60 * 60
	DefaultTicketRenewSeconds    = 14 * 24 * 60 * 60
	DefaultTicketIntervalSeconds = 1 * 24 * 60 * 60
	TicketCostValue              = 0x100
)
