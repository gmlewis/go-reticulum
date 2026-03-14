// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

// Transport is a global proxy to the singleton TransportSystem.
var Transport = transportProxy{}

type transportProxy struct{}

func (p transportProxy) RegisterDestination(d *Destination) {
	GetTransport().RegisterDestination(d)
}

func (p transportProxy) RegisterLink(l *Link) {
	GetTransport().RegisterLink(l)
}

func (p transportProxy) Outbound(packet *Packet) error {
	return GetTransport().Outbound(packet)
}

func (p transportProxy) HasPath(destHash []byte) bool {
	return GetTransport().HasPath(destHash)
}

func (p transportProxy) RequestPath(destHash []byte) error {
	return GetTransport().RequestPath(destHash)
}

func (p transportProxy) HopsTo(destHash []byte) int {
	return GetTransport().HopsTo(destHash)
}

func (p transportProxy) RegisterAnnounceHandler(handler *AnnounceHandler) {
	GetTransport().RegisterAnnounceHandler(handler)
}

func (p transportProxy) AnnounceHandlers() []*AnnounceHandler {
	return GetTransport().AnnounceHandlers()
}
