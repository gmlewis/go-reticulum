// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// BackboneDefaultIFACSize is the DEFAULT_IFAC_SIZE for Backbone server and
// client interfaces (RNS/Interfaces/BackboneInterface.py:54,569).
const BackboneDefaultIFACSize = 16

// BackboneHWMTU is the hardware MTU ceiling for Backbone interfaces
// (RNS/Interfaces/BackboneInterface.py HW_MTU = 1048576). Backbone links
// carry aggregated traffic and permit larger frames than a plain TCP
// interface (TCPHWMTU = 262144); the inbound HDLC frame-length gate uses this
// value so Backbone does not reject frames Python would accept.
const BackboneHWMTU = 1048576

// BackboneInterface provides a robust, highly available TCP listener used as a
// core routing nexus. It encapsulates TCP server logic and accepts point-to-
// point links from downstream clients.
type BackboneInterface struct {
	*TCPServerInterface
}

// NewBackboneInterface binds and initializes a TCP-based BackboneInterface on the
// given address and port. It creates a persistent listener and dispatches
// incoming frames to router logic. Spawned clients inherit BackboneHWMTU for
// inbound HDLC frame-length validation.
func NewBackboneInterface(name, bindIP string, bindPort int, handler InboundHandler, onConnect ConnectHandler) (Interface, error) {
	inner, err := newTCPServerInterface(name, bindIP, bindPort, handler, onConnect, BackboneHWMTU)
	if err != nil {
		return nil, err
	}
	return &BackboneInterface{TCPServerInterface: inner}, nil
}

// Type returns the string "BackboneInterface" as the runtime type name.
func (b *BackboneInterface) Type() string { return "BackboneInterface" }

// BackboneClientInterface represents an outbound TCP session that connects to
// a remote BackboneInterface listener, providing reliable point-to-point
// delivery to core network nodes.
type BackboneClientInterface struct {
	*TCPClientInterface
}

// NewBackboneClientInterface initiates a TCP connection to the target host and
// registers the inbound payload handler to process server-side data. The
// client uses BackboneHWMTU for inbound HDLC frame-length validation.
func NewBackboneClientInterface(name, targetHost string, targetPort int, handler InboundHandler) (Interface, error) {
	inner, err := newTCPClientInterface(name, targetHost, targetPort, false, handler, BackboneHWMTU)
	if err != nil {
		return nil, err
	}
	return &BackboneClientInterface{TCPClientInterface: inner}, nil
}

// NewDormantBackboneClientInterface returns an unconnected Backbone client used
// for discovery records that Python registers without an initial target.
func NewDormantBackboneClientInterface(name string, handler InboundHandler) Interface {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	bi.setDefaultIFACSize(BackboneDefaultIFACSize)
	return &BackboneClientInterface{
		TCPClientInterface: &TCPClientInterface{
			BaseInterface:  bi,
			inboundHandler: handler,
			hwmtu:          BackboneHWMTU,
		},
	}
}

// Type returns the string "BackboneClientInterface" as the runtime type name.
func (b *BackboneClientInterface) Type() string { return "BackboneClientInterface" }
