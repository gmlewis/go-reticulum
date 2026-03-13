// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

type I2PInterface struct {
	*TCPServerInterface
}

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

type I2PInterfacePeer struct {
	*TCPClientInterface
}

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
