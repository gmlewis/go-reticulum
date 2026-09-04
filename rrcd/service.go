// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file implements the hub service: the bring-up sequence, the
// announce/ping/prune/resource-cleanup workers, the link callbacks, the
// outgoing packet drain, the config reload, and shutdown for the RRC hub,
// mirroring Python's HubService.

package rrcd

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// hubCleanupIntervalS is the fixed resource-expectation cleanup interval.
const hubCleanupIntervalS = 30.0

// HubService ties the RRC hub managers to a live RNS instance, mirroring
// Python's HubService. Python's re-entrant state lock maps to the
// manager-level mutexes plus the hub StateLock for hub-level fields.
type HubService struct {
	Config HubConfig

	Router          *Router
	SessionManager  *SessionManager
	CommandHandler  *CommandHandler
	ResourceManager *ResourceManager
	RoomManager     *RoomManager
	StatsManager    *StatsManager
	TrustManager    *TrustManager
	MessageHelper   *MessageHelper

	// StateLock guards the hub-level fields (identity, destination,
	// config swaps) that Python protected with its RLock.
	StateLock sync.Mutex
	// Shutdown closes when Stop runs.
	Shutdown chan struct{}
	// ShutdownOnce guards the close.
	ShutdownOnce sync.Once

	identity    *rns.Identity
	destination *rns.Destination
	ts          *rns.TransportSystem
	reticulum   *rns.Reticulum
	rnsLogger   *rns.Logger

	// workerStarted flags each loop that Start spawned; ReloadConfigAndRooms
	// consults them through ensureWorkerGoroutines.
	announceRunning        bool
	pingRunning            bool
	pruneRunning           bool
	resourceCleanupRunning bool

	// nowWall and nowMono are injectable clocks (wall and monotonic
	// seconds).
	nowWall func() float64
	nowMono func() float64
	// sendPacket sends one payload to a link immediately.
	sendPacket func(link *rns.Link, payload []byte) error
	// announce sends the announce packet with the given app data.
	announce func(appData []byte) error
	// sleep waits for one loop interval and reports whether the loop
	// should continue (false = the shutdown signal fired during the
	// wait).
	sleep func(d float64) bool
	// logf logs through the configured std log writer.
	logf func(format string, args ...any)
	// startReticulum brings up the RNS stack (injectable for tests).
	startReticulum func(configDir string) error
}

// NewHubService wires the hub managers the way Python's HubService
// constructor does, forcing the destination name to the hub constant.
func NewHubService(config HubConfig) *HubService {
	if config.DestName != HubDestName {
		config.DestName = HubDestName
	}
	h := &HubService{
		Config:   config,
		Shutdown: make(chan struct{}),
	}
	h.nowWall = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	h.nowMono = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	h.sendPacket = h.sendPacketRNS
	h.announce = h.announceRNS
	h.sleep = func(d float64) bool {
		if d <= 0 {
			select {
			case <-h.Shutdown:
				return false
			default:
				return true
			}
		}
		timer := time.NewTimer(time.Duration(d * float64(time.Second)))
		defer timer.Stop()
		select {
		case <-h.Shutdown:
			return false
		case <-timer.C:
			return true
		}
	}
	h.logf = func(format string, args ...any) { log.Printf(format, args...) }
	h.startReticulum = h.startReticulumDefault

	stats := NewStatsManager(func() float64 { return h.nowWall() }, func() float64 { return h.nowMono() })
	h.StatsManager = stats

	trust := NewTrustManager(TrustHooks{
		ConfigPath: func() string {
			if h.Config.ConfigPath == nil || *h.Config.ConfigPath == "" {
				return ""
			}
			return ExpandPath(*h.Config.ConfigPath)
		},
		Notice: func(link *rns.Link, room, text string) {
			h.MessageHelper.NoticeTo(link, &room, text)
		},
	})
	h.TrustManager = trust

	rooms := NewRoomManager(RoomHooks{
		IsServerOp: trust.IsServerOp,
		RegistryPath: func() string {
			if h.Config.RoomRegistryPath == nil || *h.Config.RoomRegistryPath == "" {
				return ""
			}
			return ExpandPath(*h.Config.RoomRegistryPath)
		},
		Notice: func(link *rns.Link, room, text string) {
			h.MessageHelper.NoticeTo(link, &room, text)
		},
		BroadcastNotice: func(outgoing *OutgoingList, link *rns.Link, room, text string) {
			h.MessageHelper.EmitNotice(outgoing, link, &room, text)
		},
		Now: func() float64 { return h.nowWall() },
	})
	h.RoomManager = rooms

	sessions := NewSessionManager(SessionHooks{
		IsBanned:               trust.IsBanned,
		GetRoomMembers:         rooms.GetRoomMembers,
		RemoveMember:           rooms.RemoveMember,
		RateLimitMsgsPerMinute: func() int { return h.Config.RateLimitMsgsPerMinute },
		IncludeJoinedMemberList: func() bool {
			return h.Config.IncludeJoinedMemberList
		},
		IdentityHash: func() []byte { return h.IdentityHash() },
		NowMonotonic: func() float64 { return h.nowMono() },
		QueueWelcome: func(outgoing *OutgoingList, link *rns.Link, peerHash []byte) {
			h.MessageHelper.QueueWelcome(outgoing, link, peerHash)
		},
		Greeting: func() string {
			if h.Config.Greeting == nil {
				return ""
			}
			return *h.Config.Greeting
		},
		SendTextSmart: func(link *rns.Link, msgType int64, text string, room *string, kind string) {
			h.MessageHelper.SendTextSmart(link, msgType, text, room, kind)
		},
		SendPacket: h.safeSendPacket,
		FmtLinkID:  h.fmtLinkID,
		FmtHash:    h.fmtHash,
		Logf:       h.logf,
		StatsInc:   stats.Inc,
	})
	h.SessionManager = sessions

	messageHelper := NewMessageHelper(MessageHooks{
		IdentityHash:           func() []byte { return h.IdentityHash() },
		StatsInc:               stats.Inc,
		SendPacket:             h.safeSendPacket,
		EnableResourceTransfer: func() bool { return h.Config.EnableResourceTransfer },
		SendViaResource: func(link *rns.Link, kind string, payload []byte, room *string, encoding string) bool {
			return h.ResourceManager.SendViaResource(link, kind, payload, room, encoding)
		},
		HubName:                func() string { return h.Config.HubName },
		Greeting:               func() string { return strDeref(h.Config.Greeting) },
		MaxNickBytes:           func() int { return h.Config.MaxNickBytes },
		MaxRoomNameBytes:       func() int { return h.Config.MaxRoomNameBytes },
		MaxMsgBodyBytes:        func() int { return h.Config.MaxMsgBodyBytes },
		MaxRoomsPerSession:     func() int { return h.Config.MaxRoomsPerSession },
		RateLimitMsgsPerMinute: func() int { return h.Config.RateLimitMsgsPerMinute },
		FmtLinkID:              h.fmtLinkID,
		FmtHash:                h.fmtHash,
		Logf:                   h.logf,
	})
	h.MessageHelper = messageHelper

	resources := NewResourceManager(ResourceHooks{
		Logf:                           h.logf,
		FmtLinkID:                      h.fmtLinkID,
		StatsInc:                       stats.Inc,
		EnableResourceTransfer:         func() bool { return h.Config.EnableResourceTransfer },
		MaxResourceBytes:               func() int { return h.Config.MaxResourceBytes },
		MaxPendingResourceExpectations: func() int { return h.Config.MaxPendingResourceExpectations },
		ResourceExpectationTTLs:        func() float64 { return h.Config.ResourceExpectationTTLs },
		HasSession: func(link *rns.Link) bool {
			return sessions.GetSession(link) != nil
		},
		GetSessionPeer: func(link *rns.Link) []byte {
			if sess := sessions.GetSession(link); sess != nil {
				return sess.Peer
			}
			return nil
		},
		GetRoomMembers: rooms.GetRoomMembers,
		SendPacket:     h.safeSendPacket,
		IdentityHash:   func() []byte { return h.IdentityHash() },
		Now:            func() float64 { return h.nowWall() },
		SendResource:   h.sendResourceRNS,
	})
	h.ResourceManager = resources

	chat := NewCommandHandler(CommandHandlerHooks{
		TrustManager:   func() *TrustManager { return trust },
		SessionManager: func() *SessionManager { return sessions },
		RoomManager:    func() *RoomManager { return rooms },
		MessageHelper:  func() *MessageHelper { return messageHelper },
		IdentityHash:   func() []byte { return h.IdentityHash() },
		NormRoom:       h.NormRoom,
		ParseIdentityHash: func(text string) ([]byte, error) {
			return ParseIdentityHash(text)
		},
		FmtHash: func(hash []byte, prefix int) string { return fmtHashPrefix(hash, prefix) },
		ReloadConfigAndRooms: func(link *rns.Link, room *string, outgoing *OutgoingList) {
			h.ReloadConfigAndRooms(link, room, outgoing)
		},
		FormatStats: func() string { return h.formatStats() },
		RegistryPath: func() string {
			if h.Config.RoomRegistryPath == nil {
				return ""
			}
			return *h.Config.RoomRegistryPath
		},
		RoomInviteTimeoutS: func() float64 { return h.Config.RoomInviteTimeoutS },
		Now:                func() float64 { return h.nowWall() },
		Logf:               h.logf,
	})
	h.CommandHandler = chat

	router := NewRouter(RouterHooks{
		Sessions:                func() *SessionManager { return sessions },
		RoomManager:             func() *RoomManager { return rooms },
		TrustManager:            func() *TrustManager { return trust },
		StatsInc:                stats.Inc,
		IdentityHash:            func() []byte { return h.IdentityHash() },
		MaxNickBytes:            func() int { return h.Config.MaxNickBytes },
		MaxRoomNameBytes:        func() int { return h.Config.MaxRoomNameBytes },
		MaxRoomsPerSession:      func() int { return h.Config.MaxRoomsPerSession },
		MaxMsgBodyBytes:         func() int { return h.Config.MaxMsgBodyBytes },
		IncludeJoinedMemberList: func() bool { return h.Config.IncludeJoinedMemberList },
		FmtHash:                 h.fmtHash,
		FmtLinkID:               h.fmtLinkID,
		DebugEnabled:            func() bool { return false },
		Debugf:                  func(string, ...any) {},
		Infof:                   h.logf,
		SendPacket:              h.safeSendPacket,
		PersistRoomState:        rooms.PersistRoomState,
		QueuePayload:            messageHelper.QueuePayload,
		QueueEnv:                messageHelper.QueueEnv,
		EmitNotice:              messageHelper.EmitNotice,
		EmitError:               messageHelper.EmitError,
		AddResourceExpectation:  resources.AddResourceExpectation,
		HandleOperatorCommand:   chat.HandleOperatorCommand,
		SendWelcome:             sessions.SendWelcome,
	})
	h.Router = router

	return h
}

// SetLogger wires the RNS logger used for bring-up (called before Start).
func (h *HubService) SetLogger(logger *rns.Logger) {
	h.rnsLogger = logger
}

// IdentityHash returns the hub identity hash, or nil before Start.
func (h *HubService) IdentityHash() []byte {
	h.StateLock.Lock()
	defer h.StateLock.Unlock()
	if h.identity == nil {
		return nil
	}
	return h.identity.Hash
}

// DestinationHash returns the hub destination hash, or nil before Start.
func (h *HubService) DestinationHash() []byte {
	h.StateLock.Lock()
	defer h.StateLock.Unlock()
	if h.destination == nil {
		return nil
	}
	return h.destination.Hash
}

// StartedWallTime reports the stats start time, or nil before it was set.
func (h *HubService) StartedWallTime() *float64 { return h.StatsManager.StartedWallTime() }

// fmtLinkID renders a link id for logs, mirroring _fmt_link_id.
func (h *HubService) fmtLinkID(link *rns.Link) string {
	if link == nil {
		return "-"
	}
	return hexOf(link.GetHash())
}

// fmtHash renders a hash for logs with the default 12-character prefix,
// mirroring _fmt_hash.
func (h *HubService) fmtHash(hash []byte) string { return fmtHashPrefix(hash, 12) }

// fmtHashPrefix renders a hash with at most prefix hex characters (0
// renders all of it), mirroring _fmt_hash; "-" for nil.
func fmtHashPrefix(hash []byte, prefix int) string {
	if hash == nil {
		return "-"
	}
	s := hexOf(hash)
	if prefix <= 0 {
		return s
	}
	if prefix > len(s) {
		prefix = len(s)
	}
	return s[:prefix]
}

// sendPacketRNS sends one payload to a link immediately over RNS,
// mirroring RNS.Packet(link, payload).send().
func (h *HubService) sendPacketRNS(link *rns.Link, payload []byte) error {
	packet := rns.NewPacketWithTransport(h.ts, link, payload)
	return packet.Send()
}

// safeSendPacket sends immediately, swallowing the error the way the
// callers that ignore send failures expect.
func (h *HubService) safeSendPacket(link *rns.Link, payload []byte) error {
	if err := h.sendPacket(link, payload); err != nil {
		h.logf("Send failed link_id=%v bytes=%v err=%v", h.fmtLinkID(link), len(payload), err)
	}
	return nil
}

// announceRNS announces the hub destination with the given app data.
func (h *HubService) announceRNS(appData []byte) error {
	if h.destination == nil {
		return errors.New("destination is not set")
	}
	return h.destination.Announce(appData)
}

// sendResourceRNS creates and advertises an outgoing RNS resource,
// mirroring RNS.Resource(payload, link, advertise=True, auto_compress=False).
func (h *HubService) sendResourceRNS(payload []byte, link *rns.Link) (ResourceHandle, error) {
	res, err := rns.NewResourceWithOptions(payload, link, rns.ResourceOptions{AutoCompress: false})
	if err != nil {
		return nil, err
	}
	if err := res.Advertise(); err != nil {
		return nil, err
	}
	return res, nil
}

// Start runs the Python start sequence: the RNS bring-up, the identity
// load, the trust and registry loads, the destination, the announce, the
// startup logs, and the worker goroutines.
func (h *HubService) Start() error {
	h.logf("Starting Reticulum")
	if h.StatsManager.StartedWallTime() == nil {
		h.StatsManager.SetStartTime()
	}

	configDir := ""
	if h.Config.Configdir != nil {
		configDir = *h.Config.Configdir
	}
	if err := h.startReticulum(configDir); err != nil {
		return err
	}

	if h.Config.IdentityPath == nil || *h.Config.IdentityPath == "" {
		return errors.New("identity_path is not set")
	}
	identity, err := h.loadIdentity(*h.Config.IdentityPath)
	if err != nil {
		return err
	}
	h.StateLock.Lock()
	h.identity = identity
	h.StateLock.Unlock()

	if err := h.TrustManager.LoadFromConfig(
		append([]string{}, h.Config.TrustedIdentities...),
		append([]string{}, h.Config.BannedIdentities...),
	); err != nil {
		return err
	}

	h.loadRegisteredRoomsFromRegistry()

	destination, err := rns.NewDestination(h.ts, identity,
		rns.DestinationIn, rns.DestinationSingle, "rrc", "hub")
	if err != nil {
		return err
	}
	destination.SetLinkEstablishedCallback(h.OnLink)
	h.StateLock.Lock()
	h.destination = destination
	h.StateLock.Unlock()

	if h.Config.AnnounceOnStart {
		h.AnnounceOnce()
	}

	if h.Config.AnnouncePeriodS > 0 {
		h.announceRunning = true
		go h.AnnounceLoop()
	}

	h.logf("Hub running at dest_hash=%v", fmtHashPrefix(h.DestinationHash(), 0))
	h.logf("Policy max_nick_bytes=%v max_rooms=%v max_room_name_bytes=%v rate_limit_msgs_per_minute=%v",
		h.Config.MaxNickBytes, h.Config.MaxRoomsPerSession, h.Config.MaxRoomNameBytes,
		h.Config.RateLimitMsgsPerMinute)

	if h.Config.PingIntervalS > 0 {
		h.pingRunning = true
		go h.PingLoop()
	}

	if h.Config.RoomRegistryPruneIntervalS > 0 && h.Config.RoomRegistryPruneAfterS > 0 {
		h.pruneRunning = true
		go h.PruneLoop()
	}

	if h.Config.EnableResourceTransfer {
		h.resourceCleanupRunning = true
		go func() {
			h.ResourceManager.StartResourceCleanupLoop(h.Shutdown, h.sleep)
		}()
	}

	return nil
}

// startReticulumDefault brings up the RNS stack standalone, mirroring
// RNS.Reticulum(configdir=..., require_shared_instance=False).
func (h *HubService) startReticulumDefault(configDir string) error {
	h.ts = rns.NewTransportSystem(h.rnsLogger)
	ret, err := rns.NewReticulumWithLogger(h.ts, configDir, h.rnsLogger)
	if err != nil {
		return err
	}
	h.reticulum = ret
	return nil
}

// loadIdentity loads the hub identity, mirroring _load_identity.
func (h *HubService) loadIdentity(path string) (*rns.Identity, error) {
	p := ExpandPath(path)
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("Identity not found at %v", p)
	}
	ident, err := rns.FromFile(p, h.rnsLogger)
	if err != nil || ident == nil {
		return nil, fmt.Errorf("Failed to load identity from %v", p)
	}
	return ident, nil
}

// loadRegisteredRoomsFromRegistry loads the room registry from the
// configured path, mirroring _load_registered_rooms_from_registry (errors
// are silent).
func (h *HubService) loadRegisteredRoomsFromRegistry() {
	regPath := h.registryPathForWrites()
	if regPath == "" {
		return
	}
	registry, regErr := h.RoomManager.LoadRegistryFromPath(regPath)
	if regErr != "" {
		return
	}
	h.RoomManager.ReplaceRegistry(registry)
}

// registryPathForWrites resolves the config_path/room_registry_path for
// write operations, mirroring get_config_path_for_writes and
// get_registry_path_for_writes.
func (h *HubService) registryPathForWrites() string {
	if h.Config.RoomRegistryPath == nil || *h.Config.RoomRegistryPath == "" {
		return ""
	}
	return ExpandPath(*h.Config.RoomRegistryPath)
}

// configPathForWrites resolves the rrcd.toml path for reloads.
func (h *HubService) configPathForWrites() string {
	if h.Config.ConfigPath == nil || *h.Config.ConfigPath == "" {
		return ""
	}
	return ExpandPath(*h.Config.ConfigPath)
}

// AnnounceOnce announces once with the app_data CBOR map, mirroring
// _announce_once.
func (h *HubService) AnnounceOnce() {
	if h.destination == nil {
		return
	}
	appData := buildAnnounceAppData(h.Config.HubName)
	if err := h.announce(appData); err != nil {
		h.logf("Announce failed: %v", err)
		return
	}
	h.StatsManager.Inc("announces", 1)
}

// buildAnnounceAppData encodes the announce app_data map with the Python
// key order proto, v, hub.
func buildAnnounceAppData(hubName string) []byte {
	m := cbor.NewMap()
	m.Set("proto", "rrc")
	m.Set("v", int64(1))
	m.Set("hub", hubName)
	return cbor.Encode(m)
}

// AnnounceLoop re-announces on the configured period, sleeping first and
// re-reading the config each iteration, mirroring _announce_loop.
func (h *HubService) AnnounceLoop() {
	for {
		select {
		case <-h.Shutdown:
			return
		default:
		}
		period := h.Config.AnnouncePeriodS
		if period <= 0 {
			if !h.sleep(1.0) {
				return
			}
			continue
		}
		if !h.sleep(period) {
			return
		}
		h.AnnounceOnce()
	}
}

// OnLink initializes a newly established link, mirroring _on_link.
func (h *HubService) OnLink(link *rns.Link) {
	h.SessionManager.OnLinkEstablished(link)
	h.ResourceManager.OnLinkEstablished(link)

	link.SetPacketCallback(func(data []byte, _ *rns.Packet) { h.OnPacket(link, data) })
	link.SetLinkClosedCallback(func(closedLink *rns.Link) { h.OnClose(closedLink) })
	link.SetRemoteIdentifiedCallback(func(identifiedLink *rns.Link, ident *rns.Identity) {
		h.OnRemoteIdentified(identifiedLink, ident)
	})
	h.configureResourceCallbacks(link)

	h.logf("Link established link_id=%v", h.fmtLinkID(link))
}

// configureResourceCallbacks wires the resource accept/started/concluded
// callbacks when resource transfer is enabled.
func (h *HubService) configureResourceCallbacks(link *rns.Link) {
	if !h.Config.EnableResourceTransfer {
		return
	}
	_ = link.SetResourceStrategy(rns.AcceptApp)
	link.SetResourceCallback(func(adv *rns.ResourceAdvertisement) bool {
		return h.ResourceManager.AcceptAdvertisedResource(link, int(adv.D))
	})
	link.SetResourceStartedCallback(func(res *rns.Resource) {
		h.ResourceManager.BindStartedResource(link, res)
	})
	link.SetResourceConcludedCallback(func(res *rns.Resource) {
		h.ResourceManager.OnResourceConcluded(link, res)
	})
}

// OnRemoteIdentified handles a remote identity being established,
// mirroring _on_remote_identified: banned peers are disconnected with the
// `banned` ERROR and a teardown.
func (h *HubService) OnRemoteIdentified(link *rns.Link, identity *rns.Identity) {
	var peerHash []byte
	banned := false
	if identity != nil {
		peerHash = identity.Hash
	}
	banned, peerHash = h.SessionManager.OnRemoteIdentified(link, peerHash)
	if !banned {
		return
	}
	h.logf("Disconnecting banned peer peer=%v link_id=%v", fmtHashPrefix(peerHash, 12), h.fmtLinkID(link))
	if idHash := h.IdentityHash(); idHash != nil {
		h.MessageHelper.Error(link, idHash, "banned", nil)
	}
	link.Teardown()
}

// OnClose cleans up a closed link, mirroring _on_close.
func (h *HubService) OnClose(link *rns.Link) {
	h.ResourceManager.OnLinkClosed(link)
	peer, nick, roomsCount := h.SessionManager.OnLinkClosed(link)
	h.logf("Link closed peer=%v nick=%v rooms=%v link_id=%v",
		fmtHashPrefix(peer, 12), nickReprForLog(nick), roomsCount, h.fmtLinkID(link))
}

// nickReprForLog renders an optional nick the way Python repr does.
func nickReprForLog(nick *string) string {
	if nick == nil {
		return "None"
	}
	return pythonQuote(*nick)
}

// OnPacket routes one incoming packet and drains the outgoing queue,
// mirroring _on_packet.
func (h *HubService) OnPacket(link *rns.Link, data []byte) {
	outgoing := &OutgoingList{}
	h.Router.RoutePacket(link, data, outgoing)
	h.drainOutgoing(outgoing)
}

// drainOutgoing sends the queued payloads with per-item bytes_out
// counting, then runs the post-send callbacks.
func (h *HubService) drainOutgoing(outgoing *OutgoingList) {
	for _, item := range outgoing.Queue {
		h.StatsManager.Inc("bytes_out", len(item.Payload))
		if err := h.sendPacket(item.Link, item.Payload); err != nil {
			h.logf("Send failed link_id=%v bytes=%v err=%v",
				h.fmtLinkID(item.Link), len(item.Payload), err)
		}
	}
	for _, callback := range outgoing.PostSendCallbacks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					h.logf("Post-send callback failed: %v", r)
				}
			}()
			callback()
		}()
	}
}

// IsConnectedToSharedInstance reports whether the RNS stack attached to a
// shared instance; standalone operation is required.
func (h *HubService) IsConnectedToSharedInstance() bool {
	h.StateLock.Lock()
	defer h.StateLock.Unlock()
	if h.reticulum == nil {
		return false
	}
	return h.reticulum.IsConnectedToSharedInstance()
}

// setIdentityForTest injects an identity without running Start.
func (h *HubService) setIdentityForTest(identity *rns.Identity) {
	h.StateLock.Lock()
	h.identity = identity
	h.StateLock.Unlock()
}

// PingLoop pings welcomed sessions on the configured interval, mirroring
// _ping_loop: the timeout bookkeeping tears down silent links and PING
// envelopes carry a monotonic float64 body.
func (h *HubService) PingLoop() {
	for {
		select {
		case <-h.Shutdown:
			return
		default:
		}
		interval := h.Config.PingIntervalS
		timeout := h.Config.PingTimeoutS
		if interval <= 0 {
			if !h.sleep(1.0) {
				return
			}
			continue
		}
		if !h.sleep(interval) {
			return
		}
		idHash := h.IdentityHash()
		if idHash == nil {
			continue
		}

		now := h.nowMono()
		var toTeardown []*rns.Link
		var toPing []*rns.Link
		for _, link := range h.SessionManager.WelcomedSessions() {
			awaiting := h.SessionManager.AwaitingPong(link)
			if timeout > 0 && awaiting != nil && (now-*awaiting) > timeout {
				toTeardown = append(toTeardown, link)
				continue
			}
			if awaiting == nil {
				h.SessionManager.SetAwaitingPong(link, now)
				toPing = append(toPing, link)
			}
		}

		for _, link := range toTeardown {
			link.Teardown()
		}
		for _, link := range toPing {
			ping := MakeEnvelope(int(TPing), idHash, WithBody(now))
			h.StatsManager.Inc("pings_out", 1)
			h.MessageHelper.Send(link, ping)
		}
	}
}

// PruneLoop prunes unused registered rooms on the configured interval,
// mirroring _prune_loop: the dummy-link guard only deletes registry
// entries when at least one session exists.
func (h *HubService) PruneLoop() {
	for {
		select {
		case <-h.Shutdown:
			return
		default:
		}
		interval := h.Config.RoomRegistryPruneIntervalS
		pruneAfter := h.Config.RoomRegistryPruneAfterS
		if interval <= 0 || pruneAfter <= 0 {
			if !h.sleep(1.0) {
				return
			}
			continue
		}
		if !h.sleep(interval) {
			return
		}
		h.pruneOnce()
	}
}

// pruneOnce runs one prune pass: the dummy-link guard deletes registry
// entries only when at least one session exists.
func (h *HubService) pruneOnce() {
	dummyLink := h.SessionManager.FirstSessionLink()
	startedWall := h.nowWall()
	if started := h.StatsManager.StartedWallTime(); started != nil {
		startedWall = *started
	}
	roomsToPrune := h.RoomManager.PruneUnusedRegisteredRooms(h.Config.RoomRegistryPruneAfterS, startedWall)

	if dummyLink != nil {
		for _, room := range roomsToPrune {
			h.RoomManager.DeleteRoomFromRegistry(dummyLink, room)
		}
	}
	for _, room := range roomsToPrune {
		h.logf("Pruned unused registered room %v", room)
	}
}

// Stop shuts the hub down: the workers observe the closed channel, the
// manager state clears under the state lock, and the links teardown
// outside the lock, mirroring stop.
func (h *HubService) Stop() {
	h.ShutdownOnce.Do(func() { close(h.Shutdown) })

	h.StateLock.Lock()
	links := h.SessionManager.ClearAll()
	h.RoomManager.ClearAll()
	h.ResourceManager.ClearAll()
	h.StateLock.Unlock()

	for _, link := range links {
		link.Teardown()
	}
}

// ensureWorkerGoroutines restarts any worker loop that is not running and
// whose config knob enables it, mirroring _ensure_worker_threads.
func (h *HubService) ensureWorkerGoroutines() {
	if !h.announceRunning && h.Config.AnnouncePeriodS > 0 {
		h.announceRunning = true
		go h.AnnounceLoop()
	}
	if !h.pingRunning && h.Config.PingIntervalS > 0 {
		h.pingRunning = true
		go h.PingLoop()
	}
	if !h.pruneRunning && h.Config.RoomRegistryPruneIntervalS > 0 && h.Config.RoomRegistryPruneAfterS > 0 {
		h.pruneRunning = true
		go h.PruneLoop()
	}
}

// NormRoom normalizes a room name the way _norm_room does: Python's
// Unicode strip and lower, then the UTF-8 byte-length check.
func (h *HubService) NormRoom(room string) (string, error) {
	nr := strings.TrimFunc(room, isUnicodeSpace)
	nr = pythonLower(nr)
	if nr == "" {
		return "", errors.New("room name must not be empty")
	}
	roomBytes := len(nr)
	if roomBytes > h.Config.MaxRoomNameBytes {
		return "", fmt.Errorf("room name too long: %v bytes > %v bytes", roomBytes, h.Config.MaxRoomNameBytes)
	}
	return nr, nil
}

// formatStats renders the /stats body.
func (h *HubService) formatStats() string {
	sessStats := h.SessionManager.GetStats()
	roomStats := h.RoomManager.GetRoomStats()
	trustStats := h.TrustManager.GetStats()
	snap := StatsSnapshot{
		SessionsTotal:      sessStats.Total,
		SessionsWelcomed:   sessStats.Welcomed,
		SessionsIdentified: sessStats.Identified,
		RoomsTotal:         roomStats.RoomsTotal,
		Memberships:        roomStats.Memberships,
		TopRooms:           roomStats.TopRooms,
		TrustedCount:       trustStats.TrustedCount,
		BannedCount:        trustStats.BannedCount,
	}
	cfg := StatsConfig{
		RateLimitMsgsPerMinute: h.Config.RateLimitMsgsPerMinute,
		MaxRoomsPerSession:     h.Config.MaxRoomsPerSession,
		MaxRoomNameBytes:       h.Config.MaxRoomNameBytes,
		MaxNickBytes:           h.Config.MaxNickBytes,
		PingIntervalS:          h.Config.PingIntervalS,
		PingTimeoutS:           h.Config.PingTimeoutS,
		AnnounceOnStart:        h.Config.AnnounceOnStart,
		AnnouncePeriodS:        h.Config.AnnouncePeriodS,
	}
	return h.StatsManager.FormatStats(cfg, snap)
}

// ResolveIdentityHash resolves a token to an identity hash, mirroring
// _resolve_identity_hash.
func (h *HubService) ResolveIdentityHash(token string, room *string) []byte {
	targetLink := h.CommandHandler.FindTargetLink(token, room)
	if targetLink != nil {
		if sess := h.SessionManager.GetSession(targetLink); sess != nil && sess.Peer != nil {
			return sess.Peer
		}
	}
	hash, err := ParseIdentityHash(token)
	if err != nil {
		return nil
	}
	return hash
}

// ReloadConfigAndRooms performs the /reload operation, mirroring
// _reload_config_and_rooms: the exact failure notices, the snapshot/swap
// under the state lock, the trust parse, the registry reload and merge,
// the worker restart, and the success NOTICE joined with newlines.
func (h *HubService) ReloadConfigAndRooms(link *rns.Link, room *string, outgoing *OutgoingList) {
	cfgPath := h.configPathForWrites()
	if cfgPath == "" {
		h.MessageHelper.EmitNotice(outgoing, link, room, "reload failed: config_path not set or missing")
		return
	}
	if _, err := os.Stat(cfgPath); err != nil {
		h.MessageHelper.EmitNotice(outgoing, link, room, "reload failed: config_path not set or missing")
		return
	}

	oldCfg := h.Config
	oldTrusted := h.TrustManager.TrustedHexSet()
	oldBanned := h.TrustManager.BannedHexList()
	oldRegistry := h.RoomManager.RegistrySnapshot()

	data, err := loadConfigTOML(cfgPath)
	if err != nil {
		h.MessageHelper.EmitNotice(outgoing, link, room, fmt.Sprintf("reload failed: config parse error: %v", err))
		return
	}
	newCfg := ApplyConfigData(oldCfg, data)

	newTrusted, trustedErr := parseIdentityHashList(newCfg.TrustedIdentities)
	if trustedErr != "" {
		h.MessageHelper.EmitNotice(outgoing, link, room, fmt.Sprintf("reload failed: identity list parse error: %v", trustedErr))
		return
	}
	newBanned, bannedErr := parseIdentityHashList(newCfg.BannedIdentities)
	if bannedErr != "" {
		h.MessageHelper.EmitNotice(outgoing, link, room, fmt.Sprintf("reload failed: identity list parse error: %v", bannedErr))
		return
	}

	regPath := ""
	if newCfg.RoomRegistryPath != nil && *newCfg.RoomRegistryPath != "" {
		regPath = ExpandPath(*newCfg.RoomRegistryPath)
	}
	newRegistry, regErr := h.RoomManager.LoadRegistryFromPath(regPath)
	if regErr != "" {
		h.MessageHelper.EmitNotice(outgoing, link, room, fmt.Sprintf("reload failed: %v", regErr))
		return
	}

	h.StateLock.Lock()
	h.Config = newCfg
	h.TrustManager.ReplaceIdentities(newTrusted, newBanned)
	h.RoomManager.ReplaceRegistry(newRegistry)
	h.RoomManager.MergeRegistryIntoState(newRegistry)
	h.StateLock.Unlock()

	h.ensureWorkerGoroutines()

	cfgChanges := DiffConfigSummary(oldCfg, newCfg)
	roomChanges := DiffRegistrySummary(oldRegistry, newRegistry)

	lines := make([]string, 0, 8)
	lines = append(lines, fmt.Sprintf("reloaded: trusted=%v->%v banned=%v->%v registered_rooms=%v->%v",
		len(oldTrusted), len(newTrusted), len(oldBanned), len(newBanned), len(oldRegistry), len(newRegistry)))
	lines = append(lines, fmt.Sprintf("policy: max_nick_bytes=%v", newCfg.MaxNickBytes))
	if len(cfgChanges) > 0 {
		lines = append(lines, "config_changes:")
		preview := cfgChanges
		if len(preview) > 12 {
			preview = preview[:12]
		}
		for _, change := range preview {
			lines = append(lines, "- "+change)
		}
		if len(cfgChanges) > 12 {
			lines = append(lines, fmt.Sprintf("- (+%v more)", len(cfgChanges)-12))
		}
	} else {
		lines = append(lines, "config_changes: (none)")
	}
	lines = append(lines, "rooms_changes:")
	for _, change := range roomChanges {
		lines = append(lines, "- "+change)
	}

	h.MessageHelper.EmitNotice(outgoing, link, room, strings.Join(lines, "\n"))
}

// parseIdentityHashList parses a config identity list into raw hashes,
// reporting the first parse error text.
func parseIdentityHashList(list []string) ([][]byte, string) {
	out := make([][]byte, 0, len(list))
	for _, item := range list {
		if strings.TrimSpace(item) == "" {
			continue
		}
		hash, err := ParseIdentityHash(item)
		if err != nil {
			return nil, err.Error()
		}
		out = append(out, hash)
	}
	return out, ""
}

// loadConfigTOML reads and parses an rrcd.toml file into the nested map
// ApplyConfigData consumes.
func loadConfigTOML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := toml.Parse(string(data))
	if err != nil {
		return nil, err
	}
	return ConfigDataFromDoc(doc), nil
}

// ConfigDataFromDoc converts a parsed TOML document into the nested map
// ApplyConfigData consumes.
func ConfigDataFromDoc(doc *toml.Doc) map[string]any {
	out := map[string]any{}
	var walk func(t *toml.Table, dst map[string]any)
	walk = func(t *toml.Table, dst map[string]any) {
		if t == nil {
			return
		}
		for i := range t.Keys {
			kv := &t.Keys[i]
			if kv.IsRaw {
				continue
			}
			dst[kv.Key] = tomlValueToAny(kv.Value)
		}
		for _, sub := range t.Tables {
			child, ok := dst[sub.Path[len(sub.Path)-1]].(map[string]any)
			if !ok {
				child = map[string]any{}
				dst[sub.Path[len(sub.Path)-1]] = child
			}
			walk(sub, child)
		}
	}
	walk(doc.Root(), out)
	return out
}

// tomlValueToAny converts a TOML value to its plain Go representation.
func tomlValueToAny(v toml.Value) any {
	switch v.Kind {
	case toml.KindString:
		return v.Str
	case toml.KindInt:
		return v.Int
	case toml.KindFloat:
		return v.Flt
	case toml.KindBool:
		return v.Bool
	case toml.KindArray:
		out := make([]any, 0, len(v.Arr))
		for _, item := range v.Arr {
			out = append(out, tomlValueToAny(item))
		}
		return out
	case toml.KindInlineTable:
		m := map[string]any{}
		for i := range v.Tbl {
			kv := &v.Tbl[i]
			m[kv.Key] = tomlValueToAny(kv.Value)
		}
		return m
	}
	return nil
}
