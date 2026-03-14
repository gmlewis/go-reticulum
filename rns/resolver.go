// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

// Resolver provides identity and path resolution services.
type Resolver struct{}

// ResolveIdentity resolves a full name to an identity.
// The original Python implementation is intentionally a no-op (returns None).
func (r *Resolver) ResolveIdentity(fullName string) *Identity {
	return nil
}
