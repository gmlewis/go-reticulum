// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

// Transport is a global proxy to the singleton TransportSystem.
var Transport = transportProxy{}

type transportProxy struct{}

// RegisterDestination registers a destination with the underlying transport system.
func (p transportProxy) RegisterDestination(d *Destination) {
	GetTransport().RegisterDestination(d)
}

// RegisterLink registers a link with the underlying transport system.
func (p transportProxy) RegisterLink(l *Link) {
	GetTransport().RegisterLink(l)
}

// Outbound sends a packet outbound through the underlying transport system.
func (p transportProxy) Outbound(packet *Packet) error {
	return GetTransport().Outbound(packet)
}

// HasPath checks if a path to the specified destination hash is known by the underlying transport system.
func (p transportProxy) HasPath(destHash []byte) bool {
	return GetTransport().HasPath(destHash)
}

// RequestPath requests a path to the specified destination hash from the network via the underlying transport system.
func (p transportProxy) RequestPath(destHash []byte) error {
	return GetTransport().RequestPath(destHash)
}

// HopsTo returns the number of hops to the specified destination hash from the underlying transport system.
func (p transportProxy) HopsTo(destHash []byte) int {
	return GetTransport().HopsTo(destHash)
}

// RegisterAnnounceHandler registers an announce handler with the underlying transport system.
func (p transportProxy) RegisterAnnounceHandler(handler *AnnounceHandler) {
	GetTransport().RegisterAnnounceHandler(handler)
}

// AnnounceHandlers returns the list of registered announce handlers from the underlying transport system.
func (p transportProxy) AnnounceHandlers() []*AnnounceHandler {
	return GetTransport().AnnounceHandlers()
}
