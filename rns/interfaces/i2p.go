// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// I2PInterface is a TCP server listener that receives connections tunneled
// over the Invisible Internet Project (I2P) network.
type I2PInterface struct {
	*TCPServerInterface
}

// NewI2PInterface creates a TCP server listener on the given address that
// presents itself as an I2P interface.
func NewI2PInterface(name, bindIP string, bindPort int, handler InboundHandler, onConnect ConnectHandler) (Interface, error) {
	inner, err := NewTCPServerInterface(name, bindIP, bindPort, handler, onConnect)
	if err != nil {
		return nil, err
	}
	return &I2PInterface{TCPServerInterface: inner}, nil
}

// Type identifies this listener as an I2P-backed server interface.
func (i *I2PInterface) Type() string {
	return "I2PInterface"
}

// I2PInterfacePeer is a TCP client interface whose connection is carried over
// I2P to a remote hidden service.
type I2PInterfacePeer struct {
	*TCPClientInterface
}

// NewI2PInterfacePeer returns a TCP client interface that connects to a remote
// I2P node.
func NewI2PInterfacePeer(name, targetHost string, targetPort int, handler InboundHandler) (Interface, error) {
	inner, err := NewTCPClientInterface(name, targetHost, targetPort, false, handler)
	if err != nil {
		return nil, err
	}
	return &I2PInterfacePeer{TCPClientInterface: inner}, nil
}

// Type identifies this peer as an outbound I2P-backed interface.
func (i *I2PInterfacePeer) Type() string {
	return "I2PInterfacePeer"
}
