// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// ApplyPolicyConfig parses a generic configuration map and applies its policies to the router, enabling dynamic configuration of peering costs, static peers, and authentication requirements for LXMF communication.
func (r *Router) ApplyPolicyConfig(cfg map[string]any) error {
	if cfg == nil {
		return nil
	}

	if rawCost, ok := cfg["peering_cost"]; ok {
		cost, err := anyToInt(rawCost)
		if err != nil {
			return fmt.Errorf("parse peering_cost: %w", err)
		}
		if err := r.SetPeeringCost(cost); err != nil {
			return fmt.Errorf("apply peering_cost: %w", err)
		}
	}

	if rawStaticOnly, ok := cfg["from_static_only"]; ok {
		enabled, ok := rawStaticOnly.(bool)
		if !ok {
			return fmt.Errorf("parse from_static_only: expected bool, got %T", rawStaticOnly)
		}
		r.SetFromStaticOnly(enabled)
	}

	if rawStaticPeers, ok := cfg["static_peers"]; ok {
		peerHashes, err := parsePeerHashList(rawStaticPeers)
		if err != nil {
			return fmt.Errorf("parse static_peers: %w", err)
		}
		if err := r.SetStaticPeers(peerHashes); err != nil {
			return fmt.Errorf("apply static_peers: %w", err)
		}
	}

	if rawAuthRequired, ok := cfg["auth_required"]; ok {
		enabled, ok := rawAuthRequired.(bool)
		if !ok {
			return fmt.Errorf("parse auth_required: expected bool, got %T", rawAuthRequired)
		}
		r.SetAuthRequired(enabled)
	}

	if rawAllowedList, ok := cfg["allowed_list"]; ok {
		allowedHashes, err := parsePeerHashList(rawAllowedList)
		if err != nil {
			return fmt.Errorf("parse allowed_list: %w", err)
		}
		if err := r.SetAllowedList(allowedHashes); err != nil {
			return fmt.Errorf("apply allowed_list: %w", err)
		}
	}

	if rawBackoff, ok := cfg["peer_sync_backoff"]; ok {
		backoff, err := anyToDurationSeconds(rawBackoff)
		if err != nil {
			return fmt.Errorf("parse peer_sync_backoff: %w", err)
		}
		if err := r.SetPeerSyncBackoff(backoff); err != nil {
			return fmt.Errorf("apply peer_sync_backoff: %w", err)
		}
	}

	if rawMaxAge, ok := cfg["peer_max_age"]; ok {
		maxAge, err := anyToDurationSeconds(rawMaxAge)
		if err != nil {
			return fmt.Errorf("parse peer_max_age: %w", err)
		}
		if err := r.SetPeerMaxAge(maxAge); err != nil {
			return fmt.Errorf("apply peer_max_age: %w", err)
		}
	}

	return nil
}

func anyToInt(value any) (int, error) {
	switch n := value.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("expected numeric value, got %T", value)
	}
}

func anyToDurationSeconds(value any) (time.Duration, error) {
	seconds, err := anyToFloat64(value)
	if err != nil {
		return 0, err
	}
	if seconds < 0 {
		return 0, fmt.Errorf("duration seconds must be >= 0, got %v", seconds)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func anyToFloat64(value any) (float64, error) {
	switch n := value.(type) {
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case float64:
		return n, nil
	default:
		return 0, fmt.Errorf("expected numeric value, got %T", value)
	}
}

func parsePeerHashList(value any) ([][]byte, error) {
	switch peers := value.(type) {
	case [][]byte:
		out := make([][]byte, 0, len(peers))
		for _, peer := range peers {
			if len(peer) != rns.TruncatedHashLength/8 {
				return nil, fmt.Errorf("invalid hash byte length %v", len(peer))
			}
			out = append(out, append([]byte{}, peer...))
		}
		return out, nil
	case []any:
		out := make([][]byte, 0, len(peers))
		for _, peer := range peers {
			switch v := peer.(type) {
			case []byte:
				if len(v) != rns.TruncatedHashLength/8 {
					return nil, fmt.Errorf("invalid hash byte length %v", len(v))
				}
				out = append(out, append([]byte{}, v...))
			case string:
				decoded, err := hex.DecodeString(v)
				if err != nil {
					return nil, fmt.Errorf("decode hex hash %q: %w", v, err)
				}
				if len(decoded) != rns.TruncatedHashLength/8 {
					return nil, fmt.Errorf("invalid hash byte length %v", len(decoded))
				}
				out = append(out, decoded)
			default:
				return nil, fmt.Errorf("unsupported static peer type %T", peer)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected []any or [][]byte, got %T", value)
	}
}
