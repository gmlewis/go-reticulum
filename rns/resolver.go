// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

// Resolver provides core identity and path resolution services within the network.
type Resolver struct{}

// ResolveIdentity resolves a fully qualified network name to its corresponding
// cryptographic identity. It is intentionally unimplemented and always returns
// nil; the type is reserved for future extensibility.
func (r *Resolver) ResolveIdentity(fullName string) *Identity {
	return nil
}
