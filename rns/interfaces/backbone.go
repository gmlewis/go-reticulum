// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

type BackboneInterface struct {
	*TCPServerInterface
}

func NewBackboneInterface(name, bindIP string, bindPort int, handler InboundHandler) (Interface, error) {
	inner, err := NewTCPServerInterface(name, bindIP, bindPort, handler)
	if err != nil {
		return nil, err
	}
	return &BackboneInterface{TCPServerInterface: inner}, nil
}

func (b *BackboneInterface) Type() string {
	return "BackboneInterface"
}

type BackboneClientInterface struct {
	*TCPClientInterface
}

func NewBackboneClientInterface(name, targetHost string, targetPort int, handler InboundHandler) (Interface, error) {
	inner, err := NewTCPClientInterface(name, targetHost, targetPort, false, handler)
	if err != nil {
		return nil, err
	}
	return &BackboneClientInterface{TCPClientInterface: inner}, nil
}

func (b *BackboneClientInterface) Type() string {
	return "BackboneClientInterface"
}
