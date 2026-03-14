// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// BackboneInterface establishes a robust, highly available TCP listener acting as a core routing nexus.
// It encapsulates standard TCP server logic, accepting incoming point-to-point links from downstream clients.
type BackboneInterface struct {
	*TCPServerInterface
}

// NewBackboneInterface securely binds and initializes a TCP-based BackboneInterface on the specified address and port.
// It acts as the primary ingress vector, spinning up a persistent listener and dispatching incoming frames to the designated router logic.
func NewBackboneInterface(name, bindIP string, bindPort int, handler InboundHandler) (Interface, error) {
	inner, err := NewTCPServerInterface(name, bindIP, bindPort, handler)
	if err != nil {
		return nil, err
	}
	return &BackboneInterface{TCPServerInterface: inner}, nil
}

// Type confidently returns the exact string literal representing the architectural role of this interface, explicitly "BackboneInterface".
// This acts as a reliable identification mechanism for runtime interface type introspection.
func (b *BackboneInterface) Type() string {
	return "BackboneInterface"
}

// BackboneClientInterface establishes an outbound, persistent TCP session targeting a remote BackboneInterface listener.
// It guarantees reliable point-to-point payload delivery to core network nodes.
type BackboneClientInterface struct {
	*TCPClientInterface
}

// NewBackboneClientInterface initiates a robust TCP connection to the specified target host, acting as an active participant in the backbone fabric.
// It strictly orchestrates the connection sequence and wires up the inbound payload handler to process server-side data.
func NewBackboneClientInterface(name, targetHost string, targetPort int, handler InboundHandler) (Interface, error) {
	inner, err := NewTCPClientInterface(name, targetHost, targetPort, false, handler)
	if err != nil {
		return nil, err
	}
	return &BackboneClientInterface{TCPClientInterface: inner}, nil
}

// Type definitively returns the string literal "BackboneClientInterface", distinguishing this outbound link from incoming server connections.
// It is heavily relied upon by the router to make smart topology decisions based on interface classifications.
func (b *BackboneClientInterface) Type() string {
	return "BackboneClientInterface"
}
