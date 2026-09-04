// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file implements the resource-transfer manager: expectation
// tracking, accept/reject decisions, and resource-based sends for the
// RRC hub, mirroring Python's ResourceManager.

package rrcd

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// ResourceExpectation tracks an expected incoming Resource transfer,
// mirroring Python's _ResourceExpectation.
type ResourceExpectation struct {
	ID        []byte
	Kind      string
	Size      int
	SHA256    []byte
	Encoding  string
	CreatedAt float64
	ExpiresAt float64
	Room      *string
}

// ResourceHandle is the minimal view of an RNS resource transfer the
// manager's callbacks consume (satisfied by *rns.Resource).
type ResourceHandle interface {
	Status() int
	Data() []byte
}

// ResourceHooks wires a ResourceManager to the hub services it needs.
// The config accessors re-read the live configuration on every call,
// matching Python's fresh self.hub.config reads.
type ResourceHooks struct {
	// Logf logs a hub message.
	Logf func(format string, args ...any)
	// FmtLinkID formats a link id for logs.
	FmtLinkID func(link *rns.Link) string
	// StatsInc increments a stats counter.
	StatsInc func(key string, delta int)
	// EnableResourceTransfer reports the resource-transfer setting.
	EnableResourceTransfer func() bool
	// MaxResourceBytes returns the configured resource size limit.
	MaxResourceBytes func() int
	// MaxPendingResourceExpectations returns the per-link expectation
	// cap.
	MaxPendingResourceExpectations func() int
	// ResourceExpectationTTLs returns the expectation time-to-live in
	// seconds.
	ResourceExpectationTTLs func() float64
	// HasSession reports whether a link has a hub session.
	HasSession func(link *rns.Link) bool
	// GetSessionPeer returns a link's session peer identity hash, or
	// nil when the link has no identified session.
	GetSessionPeer func(link *rns.Link) []byte
	// GetRoomMembers returns the member links of a room (room
	// manager).
	GetRoomMembers func(room string) map[*rns.Link]bool
	// SendPacket sends a payload to a link immediately.
	SendPacket func(link *rns.Link, payload []byte) error
	// IdentityHash returns the hub identity hash, or nil when the hub
	// identity is unavailable.
	IdentityHash func() []byte
	// Now returns the current wall-clock reading in seconds
	// (injectable in tests).
	Now func() float64
	// SendResource creates and advertises an outgoing resource
	// transfer, mirroring RNS.Resource(payload, link, advertise=True,
	// auto_compress=False). The returned handle joins the
	// active-resource set until the link closes.
	SendResource func(payload []byte, link *rns.Link) (ResourceHandle, error)
}

// expectationSet is one link's insertion-ordered expectation map,
// mirroring Python's bytes-keyed dict ordering.
type expectationSet struct {
	byID  map[string]*ResourceExpectation
	order []string
}

func newExpectationSet() *expectationSet {
	return &expectationSet{byID: map[string]*ResourceExpectation{}}
}

func (s *expectationSet) get(rid string) *ResourceExpectation {
	return s.byID[rid]
}

// set stores exp under its id, updating an existing entry in place
// without moving its position.
func (s *expectationSet) set(exp *ResourceExpectation) {
	key := string(exp.ID)
	if _, ok := s.byID[key]; !ok {
		s.order = append(s.order, key)
	}
	s.byID[key] = exp
}

func (s *expectationSet) pop(rid string) *ResourceExpectation {
	exp, ok := s.byID[rid]
	if !ok {
		return nil
	}
	delete(s.byID, rid)
	for i, key := range s.order {
		if key == rid {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return exp
}

func (s *expectationSet) len() int { return len(s.byID) }

// list returns the expectations in insertion order.
func (s *expectationSet) list() []*ResourceExpectation {
	out := make([]*ResourceExpectation, 0, len(s.order))
	for _, key := range s.order {
		if exp := s.byID[key]; exp != nil {
			out = append(out, exp)
		}
	}
	return out
}

// ResourceManager manages RNS Resource transfers for the hub, mirroring
// Python's ResourceManager.
type ResourceManager struct {
	hooks ResourceHooks

	mu              sync.Mutex
	expectations    map[*rns.Link]*expectationSet
	activeResources map[*rns.Link]map[ResourceHandle]bool
	// bindings map an in-flight resource to its expectation id.
	bindings map[ResourceHandle]string
	// pending carries the accept-time matched expectation id from the
	// advertisement callback to the started callback, where the Go
	// link API first exposes the *rns.Resource (Python binds the
	// resource directly in its advertise callback).
	pending map[*rns.Link]string
}

// NewResourceManager creates a resource manager wired to the given
// hooks. A nil Now hook defaults to the wall clock.
func NewResourceManager(hooks ResourceHooks) *ResourceManager {
	if hooks.Now == nil {
		hooks.Now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	return &ResourceManager{
		hooks:           hooks,
		expectations:    map[*rns.Link]*expectationSet{},
		activeResources: map[*rns.Link]map[ResourceHandle]bool{},
		bindings:        map[ResourceHandle]string{},
		pending:         map[*rns.Link]string{},
	}
}

// OnLinkEstablished initializes resource tracking for a new link,
// mirroring on_link_established.
func (m *ResourceManager) OnLinkEstablished(link *rns.Link) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expectations[link] = newExpectationSet()
	m.activeResources[link] = map[ResourceHandle]bool{}
}

// OnLinkClosed cleans up resource state when a link closes, mirroring
// on_link_closed.
func (m *ResourceManager) OnLinkClosed(link *rns.Link) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.expectations, link)
	delete(m.activeResources, link)
}

// ClearAll clears all resource state, mirroring clear_all (called
// during shutdown).
func (m *ResourceManager) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expectations = map[*rns.Link]*expectationSet{}
	m.activeResources = map[*rns.Link]map[ResourceHandle]bool{}
}

// CleanupExpiredExpectations removes expired resource expectations for
// a link, mirroring cleanup_expired_expectations.
func (m *ResourceManager) CleanupExpiredExpectations(link *rns.Link) {
	now := m.hooks.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredLocked(link, now)
}

func (m *ResourceManager) cleanupExpiredLocked(link *rns.Link, now float64) {
	set := m.expectations[link]
	if set == nil || set.len() == 0 {
		return
	}
	var expired []string
	for _, key := range set.order {
		if exp := set.byID[key]; exp.ExpiresAt <= now {
			expired = append(expired, key)
		}
	}
	for _, key := range expired {
		set.pop(key)
		m.logf("Expired resource expectation link_id=%v rid=%v",
			m.hooks.FmtLinkID(link), hex.EncodeToString([]byte(key)))
	}
}

// CleanupAllExpiredExpectations cleans up expired resource expectations
// across all links, mirroring cleanup_all_expired_expectations.
func (m *ResourceManager) CleanupAllExpiredExpectations() {
	now := m.hooks.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for link, set := range m.expectations {
		if set.len() == 0 {
			continue
		}
		m.cleanupExpiredLocked(link, now)
	}
}

// AddResourceExpectation adds a resource expectation and reports
// whether capacity remained, mirroring add_resource_expectation. An
// empty encoding or nil sha256/room mirror Python's None.
func (m *ResourceManager) AddResourceExpectation(link *rns.Link, rid []byte, kind string, size int, sha256 []byte, encoding string, room *string) bool {
	m.CleanupExpiredExpectations(link)

	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.expectations[link]
	if set == nil {
		set = newExpectationSet()
		m.expectations[link] = set
	}
	if set.len() >= m.hooks.MaxPendingResourceExpectations() {
		m.logf("Max pending expectations exceeded link_id=%v", m.hooks.FmtLinkID(link))
		return false
	}

	now := m.hooks.Now()
	set.set(&ResourceExpectation{
		ID:        rid,
		Kind:      kind,
		Size:      size,
		SHA256:    sha256,
		Encoding:  encoding,
		CreatedAt: now,
		ExpiresAt: now + m.hooks.ResourceExpectationTTLs(),
		Room:      room,
	})

	m.logf("Added resource expectation link_id=%v rid=%v kind=%v size=%v",
		m.hooks.FmtLinkID(link), hex.EncodeToString(rid), kind, size)
	return true
}

// PopResourceExpectation removes and returns a resource expectation,
// mirroring pop_resource_expectation.
func (m *ResourceManager) PopResourceExpectation(link *rns.Link, rid []byte) *ResourceExpectation {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.expectations[link]
	if set == nil {
		return nil
	}
	return set.pop(string(rid))
}

func (m *ResourceManager) logf(format string, args ...any) {
	if m.hooks.Logf != nil {
		m.hooks.Logf(format, args...)
	}
}

// newResourceID returns a fresh 8-byte random resource id.
func newResourceID() []byte {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is effectively unreachable; an all-zero
		// id is still a valid CBOR byte string.
		return b
	}
	return b
}

// sha256Digest returns the SHA-256 digest of the payload.
func sha256Digest(payload []byte) []byte {
	sum := sha256.Sum256(payload)
	return sum[:]
}

// encodeEnvelope serializes an envelope the way Python's codec.encode
// does.
func encodeEnvelope(env *cbor.Map) []byte {
	return cbor.Encode(env)
}

// GetResourceExpectationByRID looks up an expectation by RID without
// removing it, mirroring get_resource_expectation.
// getExpectationLocked returns the expectation with the given RID for the
// link, without removing it.
func (m *ResourceManager) getExpectationLocked(link *rns.Link, rid []byte) *ResourceExpectation {
	if set := m.expectations[link]; set != nil {
		return set.get(string(rid))
	}
	return nil
}

func (m *ResourceManager) GetResourceExpectationByRID(link *rns.Link, rid []byte) *ResourceExpectation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getExpectationLocked(link, rid)
}

// MatchResourceExpectation finds the expectation that should satisfy a
// completed resource, mirroring match_resource_expectation: expired
// expectations are pruned first, then a bound-rid match wins over an exact
// rid lookup, which wins over the first size match whose sha256 (when both
// sides have one) matches.
func (m *ResourceManager) MatchResourceExpectation(link *rns.Link, rid []byte, size int, sha256 []byte) *ResourceExpectation {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredLocked(link, m.hooks.Now())

	if rid != nil {
		if exp := m.getExpectationLocked(link, rid); exp != nil {
			return exp
		}
	}

	set := m.expectations[link]
	if set == nil || set.len() == 0 {
		return nil
	}
	for _, exp := range set.list() {
		if exp.Size != size {
			continue
		}
		if len(exp.SHA256) > 0 && len(sha256) > 0 && string(exp.SHA256) != string(sha256) {
			continue
		}
		return exp
	}
	return nil
}

// FindResourceExpectation finds a matching expectation by size,
// mirroring find_resource_expectation (the advertisement fallback match).
func (m *ResourceManager) FindResourceExpectation(link *rns.Link, size int) *ResourceExpectation {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredLocked(link, m.hooks.Now())

	set := m.expectations[link]
	if set == nil || set.len() == 0 {
		return nil
	}
	for _, exp := range set.list() {
		if exp.Size == size {
			return exp
		}
	}
	return nil
}

// AcceptAdvertisedResource is the accept vote for an advertised incoming
// resource, mirroring _resource_advertised: the disabled/oversized/
// session-less/expectation-less rejections each count resources_rejected,
// and an accepted resource is registered as active with its expectation
// id bound.
func (m *ResourceManager) AcceptAdvertisedResource(link *rns.Link, size int) bool {
	if !m.hooks.EnableResourceTransfer() {
		m.logf("Rejecting resource (disabled) link_id=%v", m.hooks.FmtLinkID(link))
		m.hooks.StatsInc("resources_rejected", 1)
		return false
	}
	if size > m.hooks.MaxResourceBytes() {
		m.logf("Rejecting resource (too large: %v > %v) link_id=%v",
			size, m.hooks.MaxResourceBytes(), m.hooks.FmtLinkID(link))
		m.hooks.StatsInc("resources_rejected", 1)
		return false
	}
	if !m.hooks.HasSession(link) {
		m.logf("Rejecting resource (no session) link_id=%v", m.hooks.FmtLinkID(link))
		m.hooks.StatsInc("resources_rejected", 1)
		return false
	}
	exp := m.FindResourceExpectation(link, size)
	if exp == nil {
		m.logf("Rejecting resource (no matching expectation) link_id=%v size=%v",
			m.hooks.FmtLinkID(link), size)
		m.hooks.StatsInc("resources_rejected", 1)
		return false
	}
	m.logf("Accepting resource link_id=%v size=%v kind=%v", m.hooks.FmtLinkID(link), size, exp.Kind)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[link] = string(exp.ID)
	return true
}

// BindStartedResource binds a started incoming resource to the
// expectation accepted at advertisement time, mirroring the
// _resource_bindings assignment in the accept path (the Go link API only
// exposes the *rns.Resource at start time).
func (m *ResourceManager) BindStartedResource(link *rns.Link, res ResourceHandle) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.activeResources[link]
	if set == nil {
		set = map[ResourceHandle]bool{}
		m.activeResources[link] = set
	}
	set[res] = true
	if expID, ok := m.pending[link]; ok {
		m.bindings[res] = expID
		delete(m.pending, link)
	}
}

// HasActiveResource reports whether a resource is tracked as active for
// the link.
func (m *ResourceManager) HasActiveResource(link *rns.Link, res ResourceHandle) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeResources[link][res]
}

// OnResourceConcluded handles a concluded incoming resource transfer,
// mirroring _resource_concluded: unbinding, the status gate, the sha256
// match, the pop + stats, and the dispatch.
func (m *ResourceManager) OnResourceConcluded(link *rns.Link, res ResourceHandle) {
	m.mu.Lock()
	if set := m.activeResources[link]; set != nil {
		delete(set, res)
	}
	boundRid := m.bindings[res]
	delete(m.bindings, res)
	m.mu.Unlock()

	var boundRidBytes []byte
	if boundRid != "" {
		boundRidBytes = []byte(boundRid)
	}

	if res.Status() != rns.ResourceStatusComplete {
		m.logf("Resource transfer failed link_id=%v status=%v", m.hooks.FmtLinkID(link), res.Status())
		return
	}
	payload := res.Data()
	size := len(payload)
	actualHash := sha256Digest(payload)

	exp := m.MatchResourceExpectation(link, boundRidBytes, size, actualHash)
	if exp == nil {
		m.logf("Received resource without expectation link_id=%v size=%v", m.hooks.FmtLinkID(link), size)
		return
	}
	if len(exp.SHA256) > 0 && !sameBytes(actualHash, exp.SHA256) {
		m.logf("Resource SHA256 mismatch link_id=%v expected=%v actual=%v",
			m.hooks.FmtLinkID(link), hex.EncodeToString(exp.SHA256), hex.EncodeToString(actualHash))
		return
	}

	m.PopResourceExpectation(link, exp.ID)
	m.hooks.StatsInc("resources_received", 1)
	m.hooks.StatsInc("resource_bytes_received", size)
	m.logf("Resource received link_id=%v size=%v kind=%v", m.hooks.FmtLinkID(link), size, exp.Kind)

	m.DispatchReceivedResource(link, exp, payload)
}

// DispatchReceivedResource dispatches a received resource payload to the
// matching handler, mirroring _dispatch_received_resource.
func (m *ResourceManager) DispatchReceivedResource(link *rns.Link, exp *ResourceExpectation, payload []byte) {
	switch exp.Kind {
	case ResKindNotice:
		encoding := exp.Encoding
		if encoding == "" {
			encoding = "utf-8"
		}
		text, err := decodeText(payload, encoding)
		if err != nil {
			m.logf("Failed to decode notice resource link_id=%v encoding=%v: %v",
				m.hooks.FmtLinkID(link), encoding, err)
			return
		}
		m.logf("Received large NOTICE via resource link_id=%v room=%v chars=%v",
			m.hooks.FmtLinkID(link), pythonQuote(roomDeref(exp.Room)), utf8.RuneCountInString(text))

		if exp.Room != nil && m.hooks.IdentityHash() != nil {
			peerHash := m.hooks.GetSessionPeer(link)
			roomMembers := m.hooks.GetRoomMembers(*exp.Room)
			if peerHash != nil && len(roomMembers) > 0 {
				noticeEnv := MakeEnvelope(int(TNotice), peerHash, WithRoom(*exp.Room), WithBody(text))
				noticePayload := encodeEnvelope(noticeEnv)
				forwarded := 0
				for other := range roomMembers {
					if other == link {
						continue
					}
					if err := m.hooks.SendPacket(other, noticePayload); err != nil {
						m.logf("Failed to forward NOTICE resource link_id=%v: %v",
							m.hooks.FmtLinkID(other), err)
						continue
					}
					forwarded++
				}
				if forwarded > 0 {
					m.hooks.StatsInc("notices_forwarded", 1)
					m.logf("Forwarded NOTICE resource to %v members room=%v", forwarded, *exp.Room)
				}
			}
		}
	case ResKindMOTD:
		encoding := exp.Encoding
		if encoding == "" {
			encoding = "utf-8"
		}
		text, err := decodeText(payload, encoding)
		if err != nil {
			m.logf("Failed to decode MOTD resource link_id=%v: %v", m.hooks.FmtLinkID(link), err)
			return
		}
		m.logf("Received MOTD via resource link_id=%v chars=%v", m.hooks.FmtLinkID(link), utf8.RuneCountInString(text))
	case ResKindBlob:
		m.logf("Received BLOB via resource link_id=%v bytes=%v", m.hooks.FmtLinkID(link), len(payload))
	default:
		m.logf("Unknown resource kind link_id=%v kind=%v", m.hooks.FmtLinkID(link), exp.Kind)
	}
}

// SendViaResource sends a large payload via an RNS Resource, mirroring
// send_via_resource: the envelope goes out immediately, then the resource
// is created and advertised through the SendResource hook.
func (m *ResourceManager) SendViaResource(link *rns.Link, kind string, payload []byte, room *string, encoding string) bool {
	if !m.hooks.EnableResourceTransfer() {
		return false
	}
	size := len(payload)
	if size > m.hooks.MaxResourceBytes() {
		m.logf("Payload too large for resource transfer: %v > %v", size, m.hooks.MaxResourceBytes())
		return false
	}
	rid := newResourceID()
	sha := sha256Digest(payload)
	if m.hooks.IdentityHash() == nil {
		return false
	}

	body := cbor.NewMap()
	body.Set(BResID, rid)
	body.Set(BResKind, kind)
	body.Set(BResSize, int64(size))
	body.Set(BResSHA256, sha)
	if encoding != "" {
		body.Set(BResEncoding, encoding)
	}
	envelope := MakeEnvelope(int(TResource), m.hooks.IdentityHash(), WithRoomPtr(room), WithBody(body))
	envelopePayload := encodeEnvelope(envelope)
	if err := m.hooks.SendPacket(link, envelopePayload); err != nil {
		m.logf("Failed to send resource envelope link_id=%v: %v", m.hooks.FmtLinkID(link), err)
		return false
	}
	m.hooks.StatsInc("bytes_out", len(envelopePayload))
	m.logf("Sent resource envelope link_id=%v rid=%v kind=%v size=%v",
		m.hooks.FmtLinkID(link), hex.EncodeToString(rid), kind, size)

	res, err := m.hooks.SendResource(payload, link)
	if err != nil {
		m.logf("Failed to create resource link_id=%v: %v", m.hooks.FmtLinkID(link), err)
		return false
	}
	m.mu.Lock()
	set := m.activeResources[link]
	if set == nil {
		set = map[ResourceHandle]bool{}
		m.activeResources[link] = set
	}
	set[res] = true
	m.mu.Unlock()

	m.hooks.StatsInc("resources_sent", 1)
	m.hooks.StatsInc("resource_bytes_sent", size)
	m.logf("Sent resource link_id=%v rid=%v kind=%v size=%v",
		m.hooks.FmtLinkID(link), hex.EncodeToString(rid), kind, size)
	return true
}

// StartResourceCleanupLoop runs the resource-expectation cleanup loop at
// the fixed 30-second interval until stop closes, mirroring
// _resource_cleanup_loop. The sleep hook waits for one interval and
// reports whether the loop should continue (false = the shutdown signal
// fired during the wait).
func (m *ResourceManager) StartResourceCleanupLoop(stop <-chan struct{}, sleep func(interval float64) bool) {
	for {
		if !sleep(30.0) {
			return
		}
		select {
		case <-stop:
			return
		default:
		}
		m.cleanupOnce()
	}
}

// cleanupOnce runs one cleanup pass, logging a panic the way Python logs
// the cleanup exception.
func (m *ResourceManager) cleanupOnce() {
	defer func() {
		if r := recover(); r != nil {
			m.logf("Resource cleanup failed: %v", r)
		}
	}()
	m.CleanupAllExpiredExpectations()
}

// decodeText decodes payload with a Python str.decode-compatible codec:
// utf-8, ascii (strict), and latin-1 are supported.
func decodeText(payload []byte, encoding string) (string, error) {
	switch strings.ToLower(encoding) {
	case "utf-8", "utf8":
		return string(payload), nil
	case "ascii", "us-ascii":
		for _, b := range payload {
			if b > 0x7f {
				return "", fmt.Errorf("'ascii' codec can't decode byte 0x%02x in position %v: ordinal not in range(128)", b, bytes.IndexByte(payload, b))
			}
		}
		return string(payload), nil
	case "latin-1", "iso-8859-1":
		runes := make([]rune, len(payload))
		for i, b := range payload {
			runes[i] = rune(b)
		}
		return string(runes), nil
	default:
		return "", fmt.Errorf("unknown encoding: %v", encoding)
	}
}
