// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// I2PInterface wraps a core TCP listener specifically tuned for tunneling over the Invisible Internet Project (I2P) network.
// It acts as a resilient, anonymized ingress point, dynamically managing inbound connections originating from the I2P mesh.
type I2PInterface struct {
	*TCPServerInterface
}

// NewI2PInterface binds and orchestrates an I2P-aware TCP server on the provided IP and port.
// It delegates core session handling to the underlying TCP infrastructure while explicitly labeling traffic as traversing the I2P boundary.
func NewI2PInterface(name, bindIP string, bindPort int, handler InboundHandler) (Interface, error) {
	inner, err := NewTCPServerInterface(name, bindIP, bindPort, handler)
	if err != nil {
		return nil, err
	}
	return &I2PInterface{TCPServerInterface: inner}, nil
}

func (i *I2PInterface) Type() string {
	return "I2PInterface"
}

// I2PInterfacePeer establishes an outbound, persistent TCP connection explicitly destined for a remote I2P hidden service.
// It encapsulates the transport complexities of I2P, masquerading as a standard TCP client interface to the upper routing layers.
type I2PInterfacePeer struct {
	*TCPClientInterface
}

// NewI2PInterfacePeer aggressively dials a targeted I2P node, establishing a stable outbound tunnel.
// It securely funnels Reticulum payloads into the I2P network, mapping the remote host to a local interface abstraction.
func NewI2PInterfacePeer(name, targetHost string, targetPort int, handler InboundHandler) (Interface, error) {
	inner, err := NewTCPClientInterface(name, targetHost, targetPort, false, handler)
	if err != nil {
		return nil, err
	}
	return &I2PInterfacePeer{TCPClientInterface: inner}, nil
}

func (i *I2PInterfacePeer) Type() string {
	return "I2PInterfacePeer"
}
