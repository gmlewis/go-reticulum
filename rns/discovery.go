// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/bits"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	rcrypto "github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

const (
	discoveryAppName              = "rnstransport"
	discoveryAnnouncerJobInterval = 60 * time.Second
	discoveryDefaultStampValue    = 14
	discoveryWorkblockRounds      = 20
	discoveryStampSize            = 32
	discoveryFlagSigned           = 0b00000001
	discoveryFlagEncrypted        = 0b00000010
	discoveryFieldName            = 0xFF
	discoveryFieldTransportID     = 0xFE
	discoveryFieldInterfaceType   = 0x00
	discoveryFieldTransport       = 0x01
	discoveryFieldReachableOn     = 0x02
	discoveryFieldLatitude        = 0x03
	discoveryFieldLongitude       = 0x04
	discoveryFieldHeight          = 0x05
	discoveryFieldPort            = 0x06
	discoveryFieldIFACNetname     = 0x07
	discoveryFieldIFACNetkey      = 0x08
	discoveryFieldFrequency       = 0x09
	discoveryFieldBandwidth       = 0x0A
	discoveryFieldSpreadingFactor = 0x0B
	discoveryFieldCodingRate      = 0x0C
	discoveryFieldModulation      = 0x0D
	discoveryFieldChannel         = 0x0E
)

// InterfaceAnnouncer manages the periodic broadcast of local interface availability to dynamically discoverable peers on the network.
type InterfaceAnnouncer struct {
	logger *Logger
	owner  *Reticulum

	mu                   sync.Mutex
	discoveryDestination *Destination
	lastAnnounced        map[interfaces.Interface]time.Time
	stampCache           map[string][]byte
	jobInterval          time.Duration
	stopCh               chan struct{}
	running              bool
}

// NewInterfaceAnnouncer initializes a new announcer component bound to the provided local Reticulum instance.
func NewInterfaceAnnouncer(owner *Reticulum, logger *Logger) *InterfaceAnnouncer {
	return &InterfaceAnnouncer{
		logger:        logger,
		owner:         owner,
		lastAnnounced: make(map[interfaces.Interface]time.Time),
		stampCache:    make(map[string][]byte),
		jobInterval:   discoveryAnnouncerJobInterval,
	}
}

// Start triggers the underlying background mechanism that begins transmitting interface presence announcements.
func (ia *InterfaceAnnouncer) Start() {
	if ia == nil || ia.owner == nil || ia.owner.transport == nil {
		return
	}

	ia.mu.Lock()
	if ia.running {
		ia.mu.Unlock()
		return
	}

	if ia.discoveryDestination == nil {
		identity := ia.owner.networkIdentity
		if identity == nil {
			identity = ia.owner.transport.Identity()
		}
		if identity == nil {
			ia.mu.Unlock()
			if ia.logger != nil {
				ia.logger.Error("could not start discovery announcer: no transport identity available")
			}
			return
		}

		destination, err := NewDestination(ia.owner.transport, identity, DestinationIn, DestinationSingle, discoveryAppName, "discovery", "interface")
		if err != nil {
			ia.mu.Unlock()
			if ia.logger != nil {
				ia.logger.Error("could not start discovery announcer: %v", err)
			}
			return
		}
		ia.discoveryDestination = destination
	}

	stopCh := make(chan struct{})
	interval := ia.jobInterval
	if interval <= 0 {
		interval = discoveryAnnouncerJobInterval
	}
	ia.stopCh = stopCh
	ia.running = true
	ia.mu.Unlock()

	go ia.job(stopCh, interval)
}

// Stop halts the background discovery announce job.
func (ia *InterfaceAnnouncer) Stop() {
	if ia == nil {
		return
	}

	ia.mu.Lock()
	defer ia.mu.Unlock()
	if ia.stopCh != nil {
		close(ia.stopCh)
		ia.stopCh = nil
	}
	ia.running = false
}

func (ia *InterfaceAnnouncer) job(stopCh <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case now := <-ticker.C:
			ia.announceOnce(now)
		}
	}
}

func (ia *InterfaceAnnouncer) announceOnce(now time.Time) {
	if ia == nil || ia.owner == nil || ia.owner.transport == nil {
		return
	}

	type dueInterface struct {
		iface   interfaces.Interface
		overdue time.Duration
	}

	ia.mu.Lock()
	destination := ia.discoveryDestination
	due := make([]dueInterface, 0)
	for _, iface := range ia.owner.transport.GetInterfaces() {
		cfg, ok := discoveryConfigForInterface(iface)
		if !ok || !cfg.SupportsDiscovery || !cfg.Discoverable {
			continue
		}

		interval := cfg.AnnounceInterval
		if interval <= 0 {
			interval = 6 * time.Hour
		}
		last := ia.lastAnnounced[iface]
		if last.IsZero() || now.After(last.Add(interval)) {
			due = append(due, dueInterface{
				iface:   iface,
				overdue: now.Sub(last),
			})
		}
	}

	sort.Slice(due, func(i, j int) bool {
		return due[i].overdue > due[j].overdue
	})

	var selected interfaces.Interface
	if len(due) > 0 {
		selected = due[0].iface
		ia.lastAnnounced[selected] = now
	}
	ia.mu.Unlock()

	if selected == nil || destination == nil {
		return
	}

	appData, err := ia.getInterfaceAnnounceData(selected)
	if err != nil {
		if ia.logger != nil {
			ia.logger.Error("failed generating discovery announce for %v: %v", selected.Name(), err)
		}
		return
	}
	if len(appData) == 0 {
		return
	}
	if err := destination.Announce(appData); err != nil && ia.logger != nil {
		ia.logger.Error("failed sending discovery announce for %v: %v", selected.Name(), err)
	}
}

func discoveryConfigForInterface(iface interfaces.Interface) (interfaces.DiscoveryConfig, bool) {
	if iface == nil {
		return interfaces.DiscoveryConfig{}, false
	}
	getter, ok := iface.(interface {
		DiscoveryConfig() interfaces.DiscoveryConfig
	})
	if !ok {
		return interfaces.DiscoveryConfig{}, false
	}
	return getter.DiscoveryConfig(), true
}

func sanitizeDiscoveryString(v string) string {
	v = strings.ReplaceAll(v, "\n", "")
	v = strings.ReplaceAll(v, "\r", "")
	return strings.TrimSpace(v)
}

func (ia *InterfaceAnnouncer) resolveReachableOn(raw string) (string, error) {
	reachableOn := sanitizeDiscoveryString(raw)
	if reachableOn == "" {
		return "", nil
	}

	if runtime.GOOS != "windows" {
		execPath, err := expandUserPath(reachableOn)
		if err == nil {
			if info, statErr := os.Stat(execPath); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				output, err := exec.Command(execPath).Output()
				if err != nil {
					return "", fmt.Errorf("evaluating reachable_on executable %q: %w", raw, err)
				}
				reachableOn = sanitizeDiscoveryString(string(output))
			}
		}
	}

	if !isReachableOnValue(reachableOn) {
		return "", fmt.Errorf("invalid reachable_on value %q", reachableOn)
	}
	return reachableOn, nil
}

func discoveryInterfaceType(iface interfaces.Interface) string {
	if iface == nil {
		return ""
	}

	switch iface.Type() {
	case "BackboneInterface", "I2PInterface", "RNodeInterface", "WeaveInterface", "KISSInterface":
		return iface.Type()
	case "TCPServerInterface", "TCPClientInterface":
		return iface.Type()
	case "TCPInterface":
		if getter, ok := iface.(interface{ BindPort() int }); ok && getter.BindPort() > 0 {
			return "TCPServerInterface"
		}
		return "TCPClientInterface"
	default:
		return ""
	}
}

func isKISSFramedInterface(iface interfaces.Interface) bool {
	if iface == nil {
		return false
	}
	getter, ok := iface.(interface{ KISSFraming() bool })
	return ok && getter.KISSFraming()
}

func (ia *InterfaceAnnouncer) getInterfaceAnnounceData(iface interfaces.Interface) ([]byte, error) {
	if ia == nil || ia.owner == nil || ia.owner.transport == nil {
		return nil, fmt.Errorf("no Reticulum transport available")
	}

	cfg, ok := discoveryConfigForInterface(iface)
	if !ok || !cfg.SupportsDiscovery || !cfg.Discoverable {
		return nil, nil
	}

	interfaceType := discoveryInterfaceType(iface)
	advertisedType := interfaceType
	if interfaceType == "TCPClientInterface" && isKISSFramedInterface(iface) {
		advertisedType = "KISSInterface"
	}

	switch advertisedType {
	case "BackboneInterface", "TCPServerInterface", "I2PInterface", "RNodeInterface", "WeaveInterface", "KISSInterface":
	default:
		return nil, nil
	}

	transportIdentity := ia.owner.transport.Identity()
	if transportIdentity == nil || len(transportIdentity.Hash) == 0 {
		return nil, fmt.Errorf("missing transport identity")
	}

	stampValue := cfg.StampValue
	if stampValue <= 0 {
		stampValue = discoveryDefaultStampValue
	}

	name := sanitizeDiscoveryString(cfg.Name)

	info := map[any]any{
		discoveryFieldInterfaceType: advertisedType,
		discoveryFieldTransport:     ia.owner.transport.Enabled(),
		discoveryFieldTransportID:   append([]byte(nil), transportIdentity.Hash...),
		discoveryFieldName:          name,
		discoveryFieldLatitude:      nil,
		discoveryFieldLongitude:     nil,
		discoveryFieldHeight:        nil,
	}

	if cfg.Latitude != nil {
		info[discoveryFieldLatitude] = *cfg.Latitude
	}
	if cfg.Longitude != nil {
		info[discoveryFieldLongitude] = *cfg.Longitude
	}
	if cfg.Height != nil {
		info[discoveryFieldHeight] = *cfg.Height
	}

	reachableOn, err := ia.resolveReachableOn(cfg.ReachableOn)
	if err != nil {
		return nil, err
	}
	if reachableOn == "" {
		return nil, fmt.Errorf("missing reachable_on")
	}
	info[discoveryFieldReachableOn] = reachableOn

	switch advertisedType {
	case "BackboneInterface", "TCPServerInterface":
		portGetter, ok := iface.(interface{ BindPort() int })
		if !ok || portGetter.BindPort() <= 0 {
			return nil, fmt.Errorf("missing bind port")
		}
		info[discoveryFieldPort] = portGetter.BindPort()
	case "I2PInterface":
	case "RNodeInterface":
		if cfg.Frequency == nil || cfg.Bandwidth == nil || cfg.SpreadingFactor == nil || cfg.CodingRate == nil {
			return nil, fmt.Errorf("missing RNode discovery radio parameters")
		}
		info[discoveryFieldFrequency] = *cfg.Frequency
		info[discoveryFieldBandwidth] = *cfg.Bandwidth
		info[discoveryFieldSpreadingFactor] = *cfg.SpreadingFactor
		info[discoveryFieldCodingRate] = *cfg.CodingRate
	case "WeaveInterface":
		if cfg.Frequency == nil || cfg.Bandwidth == nil || cfg.Channel == nil {
			return nil, fmt.Errorf("missing Weave discovery radio parameters")
		}
		info[discoveryFieldFrequency] = *cfg.Frequency
		info[discoveryFieldBandwidth] = *cfg.Bandwidth
		info[discoveryFieldChannel] = *cfg.Channel
		info[discoveryFieldModulation] = cfg.Modulation
	case "KISSInterface":
		if cfg.Frequency == nil || cfg.Bandwidth == nil {
			return nil, fmt.Errorf("missing KISS discovery radio parameters")
		}
		info[discoveryFieldFrequency] = *cfg.Frequency
		info[discoveryFieldBandwidth] = *cfg.Bandwidth
		info[discoveryFieldModulation] = sanitizeDiscoveryString(cfg.Modulation)
	}

	if cfg.PublishIFAC {
		if ifacGetter, ok := iface.(interface{ IFACConfig() interfaces.IFACConfig }); ok {
			ifacCfg := ifacGetter.IFACConfig()
			if ifacCfg.NetName != "" {
				info[discoveryFieldIFACNetname] = sanitizeDiscoveryString(ifacCfg.NetName)
			}
			if ifacCfg.NetKey != "" {
				info[discoveryFieldIFACNetkey] = sanitizeDiscoveryString(ifacCfg.NetKey)
			}
		}
	}

	packed, err := msgpack.Pack(info)
	if err != nil {
		return nil, err
	}

	infoHash := FullHash(packed)
	cacheKey := hex.EncodeToString(infoHash)

	ia.mu.Lock()
	stamp := append([]byte(nil), ia.stampCache[cacheKey]...)
	ia.mu.Unlock()
	if len(stamp) == 0 {
		generated, _, err := generateDiscoveryStamp(infoHash, stampValue)
		if err != nil {
			return nil, err
		}
		stamp = generated
		ia.mu.Lock()
		ia.stampCache[cacheKey] = append([]byte(nil), stamp...)
		ia.mu.Unlock()
	}

	var flags byte
	payload := append([]byte(nil), packed...)
	payload = append(payload, stamp...)
	if cfg.Encrypt {
		if ia.owner.networkIdentity == nil {
			return nil, fmt.Errorf("discovery encryption requested without network identity")
		}
		encrypted, err := ia.owner.networkIdentity.Encrypt(payload, nil)
		if err != nil {
			return nil, err
		}
		payload = encrypted
		flags |= discoveryFlagEncrypted
	}

	appData := make([]byte, 1, 1+len(payload))
	appData[0] = flags
	appData = append(appData, payload...)
	return appData, nil
}

// InterfaceAnnounceHandler validates and decodes interface discovery announces.
type InterfaceAnnounceHandler struct {
	owner         *Reticulum
	requiredValue int
	callback      func(map[string]any)
}

// NewInterfaceAnnounceHandler creates a discovery announce handler with Python's
// default stamp cost when requiredValue is zero.
func NewInterfaceAnnounceHandler(owner *Reticulum, requiredValue int, callback func(map[string]any)) *InterfaceAnnounceHandler {
	if requiredValue <= 0 {
		requiredValue = discoveryDefaultStampValue
	}
	return &InterfaceAnnounceHandler{
		owner:         owner,
		requiredValue: requiredValue,
		callback:      callback,
	}
}

// AnnounceHandler adapts the handler to the transport announce callback API.
func (h *InterfaceAnnounceHandler) AnnounceHandler() *AnnounceHandler {
	if h == nil {
		return nil
	}
	return &AnnounceHandler{
		AspectFilter:     discoveryAppName + ".discovery.interface",
		ReceivedAnnounce: h.receivedAnnounce,
	}
}

func (h *InterfaceAnnounceHandler) receivedAnnounce(destinationHash []byte, announcedIdentity *Identity, appData []byte) {
	if h == nil || len(appData) <= discoveryStampSize+1 {
		return
	}

	if len(h.owner.interfaceSources) > 0 {
		if announcedIdentity == nil || !hasDiscoverySource(h.owner.interfaceSources, announcedIdentity.Hash) {
			return
		}
	}

	flags := appData[0]
	payload := appData[1:]
	if flags&discoveryFlagEncrypted != 0 {
		var networkIdentity *Identity
		if h.owner != nil {
			networkIdentity = h.owner.networkIdentity
			if networkIdentity == nil && h.owner.transport != nil {
				if getter, ok := h.owner.transport.(interface{ NetworkIdentity() *Identity }); ok {
					networkIdentity = getter.NetworkIdentity()
				}
			}
		}
		if networkIdentity == nil {
			return
		}
		decrypted, err := networkIdentity.Decrypt(payload, nil, false)
		if err != nil || len(decrypted) == 0 {
			return
		}
		payload = decrypted
	}
	if len(payload) <= discoveryStampSize {
		return
	}

	stamp := payload[len(payload)-discoveryStampSize:]
	packed := payload[:len(payload)-discoveryStampSize]
	infohash := FullHash(packed)
	workblock, err := discoveryStampWorkblock(infohash, discoveryWorkblockRounds)
	if err != nil {
		return
	}
	value := discoveryStampValue(workblock, stamp)
	if !discoveryStampValid(stamp, h.requiredValue, workblock) || value < h.requiredValue {
		return
	}

	info, err := h.decodeDiscoveryInfo(destinationHash, announcedIdentity, packed, stamp, value)
	if err != nil || info == nil {
		return
	}

	h.invokeCallback(info)
}

func (h *InterfaceAnnounceHandler) invokeCallback(info map[string]any) {
	if h == nil || h.callback == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil && h.owner != nil && h.owner.logger != nil {
			h.owner.logger.Error("error while processing interface discovery callback: %v", recovered)
		}
	}()
	h.callback(info)
}

// Discovered-interface age thresholds and status values.
const (
	// ThresholdUnknown is the age in seconds after which an interface is marked
	// unknown.
	ThresholdUnknown = 24 * 60 * 60
	// ThresholdStale is the age in seconds after which an interface is marked
	// stale.
	ThresholdStale = 3 * 24 * 60 * 60
	// ThresholdRemove is the age in seconds after which cached discovery data is
	// removed.
	ThresholdRemove = 7 * 24 * 60 * 60

	// StatusStale marks an interface as stale.
	StatusStale = 0
	// StatusUnknown marks an interface as unknown.
	StatusUnknown = 100
	// StatusAvailable marks an interface as available.
	StatusAvailable = 1000
)

// DiscoveredInterface describes one interface discovered from cached announce
// data.
type DiscoveredInterface struct {
	// Name is the discovered interface name.
	Name string `json:"name"`
	// Type is the discovered interface type.
	Type string `json:"type"`
	// Status is the human-readable availability state.
	Status string `json:"status"`
	// StatusCode is the numeric availability state.
	StatusCode int `json:"status_code"`
	// Hops is the reported hop count to the interface.
	Hops int `json:"hops"`
	// Discovered is the Unix timestamp when the interface was first recorded.
	Discovered float64 `json:"discovered"`
	// LastHeard is the Unix timestamp of the latest discovery update.
	LastHeard float64 `json:"last_heard"`
	// Transport reports whether the interface acts as a transport node.
	Transport bool     `json:"transport"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Height    *float64 `json:"height"`
	// Value is the discovery ranking value carried in the cache entry.
	Value int `json:"value"`
	// ConfigEntry is a generated config snippet for recreating the interface.
	ConfigEntry string `json:"config_entry"`
	NetworkID   string `json:"network_id,omitempty"`
	TransportID string `json:"transport_id,omitempty"`
	ReachableOn string `json:"reachable_on,omitempty"`
	Port        *int   `json:"port,omitempty"`
	Frequency   *int   `json:"frequency,omitempty"`
	Bandwidth   *int   `json:"bandwidth,omitempty"`
	SF          *int   `json:"sf,omitempty"`
	CR          *int   `json:"cr,omitempty"`
	Modulation  string `json:"modulation,omitempty"`
	IFACNetname string `json:"ifac_netname,omitempty"`
	IFACNetkey  string `json:"ifac_netkey,omitempty"`
}

// InterfaceDiscovery actively listens for and processes inbound presence announcements from remote nodes to establish automatic connections.
type InterfaceDiscovery struct {
	owner *Reticulum

	handler *InterfaceAnnounceHandler

	callbackMu        sync.RWMutex
	discoveryCallback func(map[string]any)

	monitorMu              sync.Mutex
	monitoredInterfaces    []interfaces.Interface
	autoconnectDownSince   map[interfaces.Interface]time.Time
	monitoringAutoconnects bool
	initialAutoconnectRan  bool
	monitorInterval        time.Duration
	detachThreshold        time.Duration
	monitorStopCh          chan struct{}
	shuffleCandidates      func([]DiscoveredInterface)
}

// NewInterfaceDiscovery initializes a discovery listener bound to the provided local Reticulum configuration.
func NewInterfaceDiscovery(owner *Reticulum) *InterfaceDiscovery {
	return &InterfaceDiscovery{
		owner:                owner,
		autoconnectDownSince: make(map[interfaces.Interface]time.Time),
		monitorInterval:      5 * time.Second,
		detachThreshold:      12 * time.Second,
		shuffleCandidates:    shuffleDiscoveredInterfaces,
	}
}

// SetDiscoveryCallback registers the external callback invoked after a
// discovered interface has been persisted and processed for autoconnect.
func (id *InterfaceDiscovery) SetDiscoveryCallback(callback func(map[string]any)) {
	if id == nil {
		return
	}
	id.callbackMu.Lock()
	defer id.callbackMu.Unlock()
	id.discoveryCallback = callback
}

func (id *InterfaceDiscovery) invokeDiscoveryCallback(info map[string]any) {
	if id == nil {
		return
	}
	id.callbackMu.RLock()
	callback := id.discoveryCallback
	id.callbackMu.RUnlock()
	if callback == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil && id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("error while processing external interface discovery callback: %v", recovered)
		}
	}()
	callback(info)
}

func shuffleDiscoveredInterfaces(candidates []DiscoveredInterface) {
	for i := len(candidates) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return
		}
		j := int(n.Int64())
		candidates[i], candidates[j] = candidates[j], candidates[i]
	}
}

// Start initializes on-disk discovery storage and registers the announce
// handler used for inbound interface discovery updates.
func (id *InterfaceDiscovery) Start(requiredValue int) error {
	if id == nil || id.owner == nil {
		return fmt.Errorf("no Reticulum instance")
	}

	storagePath := filepath.Join(id.owner.configDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return err
	}
	if id.handler != nil {
		return nil
	}

	id.handler = NewInterfaceAnnounceHandler(id.owner, requiredValue, func(info map[string]any) {
		if err := id.persistDiscoveredInterface(info); err != nil && id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("failed to persist discovered interface: %v", err)
			return
		} else if err != nil {
			return
		}
		if discovered, ok := mapToDiscoveredInterface(info); ok {
			if err := id.autoconnect(discovered); err != nil && id.owner != nil && id.owner.logger != nil {
				id.owner.logger.Error("failed to auto-connect discovered interface %v: %v", discovered.Name, err)
			}
		}
		id.invokeDiscoveryCallback(info)
	})
	if id.owner.transport != nil {
		id.owner.transport.RegisterAnnounceHandler(id.handler.AnnounceHandler())
	}
	return nil
}

// Stop halts any background autoconnect monitoring started by the discovery
// subsystem.
func (id *InterfaceDiscovery) Stop() {
	if id == nil {
		return
	}

	id.monitorMu.Lock()
	defer id.monitorMu.Unlock()
	if id.monitorStopCh != nil {
		close(id.monitorStopCh)
		id.monitorStopCh = nil
	}
	id.monitoringAutoconnects = false
}

func (id *InterfaceDiscovery) persistDiscoveredInterface(info map[string]any) error {
	if id.owner == nil {
		return fmt.Errorf("no Reticulum instance")
	}

	discoveryHash, err := discoveryHashFilename(info["discovery_hash"])
	if err != nil {
		return err
	}

	receivedAt := asFloat64(info["received"])
	if receivedAt == 0 {
		return fmt.Errorf("missing received timestamp")
	}

	storagePath := filepath.Join(id.owner.configDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return err
	}

	filePath := filepath.Join(storagePath, discoveryHash+".data")
	persisted := cloneStringAnyMap(info)
	persisted["last_heard"] = receivedAt

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		persisted["discovered"] = receivedAt
		persisted["heard_count"] = 0
		info["last_heard"] = receivedAt
		info["discovered"] = receivedAt
		info["heard_count"] = 0
		data, err := msgpack.Pack(persisted)
		if err != nil {
			return err
		}
		return os.WriteFile(filePath, data, 0o644)
	} else if err != nil {
		return err
	}

	lastData, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	unpacked, err := msgpack.Unpack(lastData)
	if err != nil {
		return err
	}
	lastInfo := asAnyMap(unpacked)
	if lastInfo == nil {
		return fmt.Errorf("unexpected discovery cache type %T", unpacked)
	}

	discoveredValue, ok := lastInfo["discovered"]
	if !ok || discoveredValue == nil {
		return fmt.Errorf("corrupt discovery cache missing discovered")
	}
	heardCountValue, ok := lastInfo["heard_count"]
	if !ok {
		return fmt.Errorf("corrupt discovery cache missing heard_count")
	}

	discoveredAt := asFloat64(discoveredValue)
	heardCount := 1
	if heardCountValue != nil {
		heardCount = asInt(heardCountValue) + 1
	}

	persisted["discovered"] = discoveredAt
	persisted["heard_count"] = heardCount
	info["last_heard"] = receivedAt
	info["discovered"] = discoveredAt
	info["heard_count"] = heardCount

	data, err := msgpack.Pack(persisted)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0o644)
}

// ListDiscoveredInterfaces returns a list of discovered interfaces.
func (id *InterfaceDiscovery) ListDiscoveredInterfaces(onlyAvailable, onlyTransport bool) ([]DiscoveredInterface, error) {
	if id.owner == nil {
		return nil, fmt.Errorf("no Reticulum instance")
	}

	storagePath := filepath.Join(id.owner.configDir, "discovery", "interfaces")
	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(storagePath)
	if err != nil {
		return nil, err
	}

	now := float64(time.Now().UnixNano()) / 1e9
	discoverySources := id.owner.interfaceSources
	var discovered []DiscoveredInterface

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(storagePath, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			id.owner.logger.Warning("failed to read discovery file %q: %v", path, err)
			continue
		}

		unpacked, err := msgpack.Unpack(data)
		if err != nil {
			id.owner.logger.Warning("failed to unpack discovery file %q: %v", path, err)
			continue
		}
		m, ok := unpacked.(map[any]any)
		if !ok {
			continue
		}

		heardAt := asFloat64(lookupAnyValue(m, "last_heard"))
		heardDelta := now - heardAt

		shouldRemove := false
		if heardDelta > ThresholdRemove {
			shouldRemove = true
		} else if len(discoverySources) > 0 {
			networkIDHex := asString(lookupAnyValue(m, "network_id"))
			if networkIDHex == "" {
				shouldRemove = true
			} else {
				networkID, err := hex.DecodeString(networkIDHex)
				if err != nil {
					id.owner.logger.Error("error while loading discovered interface data: %v", err)
					id.owner.logger.Error("the interface data file %v may be corrupt", path)
					continue
				}
				if !hasDiscoverySource(discoverySources, networkID) {
					shouldRemove = true
				}
			}
		}
		if !shouldRemove {
			reachableOn := asString(lookupAnyValue(m, "reachable_on"))
			if reachableOn != "" && !isReachableOnValue(reachableOn) {
				shouldRemove = true
			}
		}

		if shouldRemove {
			if err := os.Remove(path); err != nil {
				id.owner.logger.Warning("failed to remove expired discovery file %q: %v", path, err)
			}
			continue
		}

		// Status calculation
		status := "available"
		statusCode := StatusAvailable
		if heardDelta > ThresholdStale {
			status = "stale"
			statusCode = StatusStale
		} else if heardDelta > ThresholdUnknown {
			status = "unknown"
			statusCode = StatusUnknown
		}

		if onlyAvailable && status != "available" {
			continue
		}

		transport := asBool(lookupAnyValue(m, "transport"))
		if onlyTransport && !transport {
			continue
		}

		di := DiscoveredInterface{
			Name:        asString(lookupAnyValue(m, "name")),
			Type:        asString(lookupAnyValue(m, "type")),
			Status:      status,
			StatusCode:  statusCode,
			Hops:        asInt(lookupAnyValue(m, "hops")),
			Discovered:  asFloat64(lookupAnyValue(m, "discovered")),
			LastHeard:   heardAt,
			Transport:   transport,
			Value:       asInt(lookupAnyValue(m, "value")),
			ConfigEntry: asString(lookupAnyValue(m, "config_entry")),
			NetworkID:   asString(lookupAnyValue(m, "network_id")),
			TransportID: asString(lookupAnyValue(m, "transport_id")),
			ReachableOn: asString(lookupAnyValue(m, "reachable_on")),
			Modulation:  asString(lookupAnyValue(m, "modulation")),
			IFACNetname: asString(lookupAnyValue(m, "ifac_netname")),
			IFACNetkey:  asString(lookupAnyValue(m, "ifac_netkey")),
		}

		di.Latitude = lookupOptFloat64(m, "latitude")
		di.Longitude = lookupOptFloat64(m, "longitude")
		di.Height = lookupOptFloat64(m, "height")
		di.Port = lookupOptInt(m, "port")
		di.Frequency = lookupOptInt(m, "frequency")
		di.Bandwidth = lookupOptInt(m, "bandwidth")
		di.SF = lookupOptInt(m, "sf")
		di.CR = lookupOptInt(m, "cr")

		discovered = append(discovered, di)
	}

	sort.Slice(discovered, func(i, j int) bool {
		left := discovered[i]
		right := discovered[j]
		if left.StatusCode != right.StatusCode {
			return left.StatusCode > right.StatusCode
		}
		if left.Value != right.Value {
			return left.Value > right.Value
		}
		return left.LastHeard > right.LastHeard
	})

	return discovered, nil
}

func hasDiscoverySource(sources [][]byte, networkID []byte) bool {
	for _, source := range sources {
		if bytes.Equal(source, networkID) {
			return true
		}
	}
	return false
}

func isReachableOnValue(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if net.ParseIP(v) != nil {
		return true
	}
	return isHostname(v)
}

func isHostname(v string) bool {
	if len(v) == 0 || len(v) > 253 {
		return false
	}
	labels := strings.Split(v, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func discoveryHashFilename(v any) (string, error) {
	switch t := v.(type) {
	case []byte:
		if len(t) == 0 {
			return "", fmt.Errorf("missing discovery hash")
		}
		return hex.EncodeToString(t), nil
	case string:
		if t == "" {
			return "", fmt.Errorf("missing discovery hash")
		}
		return t, nil
	default:
		return "", fmt.Errorf("unsupported discovery hash type %T", v)
	}
}

func cloneStringAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mapToDiscoveredInterface(info map[string]any) (DiscoveredInterface, bool) {
	if info == nil {
		return DiscoveredInterface{}, false
	}

	out := DiscoveredInterface{
		Name:        asString(info["name"]),
		Type:        asString(info["type"]),
		Status:      asString(info["status"]),
		StatusCode:  asInt(info["status_code"]),
		Hops:        asInt(info["hops"]),
		Discovered:  asFloat64(info["discovered"]),
		LastHeard:   asFloat64(info["last_heard"]),
		Transport:   asBool(info["transport"]),
		Value:       asInt(info["value"]),
		ConfigEntry: asString(info["config_entry"]),
		NetworkID:   asString(info["network_id"]),
		TransportID: asString(info["transport_id"]),
		ReachableOn: asString(info["reachable_on"]),
		Modulation:  asString(info["modulation"]),
		IFACNetname: asString(info["ifac_netname"]),
		IFACNetkey:  asString(info["ifac_netkey"]),
	}

	if v, ok := info["latitude"]; ok && v != nil {
		f := asFloat64(v)
		out.Latitude = &f
	}
	if v, ok := info["longitude"]; ok && v != nil {
		f := asFloat64(v)
		out.Longitude = &f
	}
	if v, ok := info["height"]; ok && v != nil {
		f := asFloat64(v)
		out.Height = &f
	}
	if v, ok := info["port"]; ok && v != nil {
		i := asInt(v)
		out.Port = &i
	}
	if v, ok := info["frequency"]; ok && v != nil {
		i := asInt(v)
		out.Frequency = &i
	}
	if v, ok := info["bandwidth"]; ok && v != nil {
		i := asInt(v)
		out.Bandwidth = &i
	}
	if v, ok := info["sf"]; ok && v != nil {
		i := asInt(v)
		out.SF = &i
	}
	if v, ok := info["cr"]; ok && v != nil {
		i := asInt(v)
		out.CR = &i
	}

	return out, out.Type != ""
}

func (id *InterfaceDiscovery) connectDiscovered() {
	if id == nil || id.owner == nil || !id.owner.shouldAutoconnectDiscoveredInterfaces() {
		return
	}
	discovered, err := id.ListDiscoveredInterfaces(false, true)
	if err != nil {
		if id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("failed to load discovered interfaces for autoconnect: %v", err)
		}
		return
	}

	for _, info := range discovered {
		if id.autoconnectCount() >= id.owner.maxAutoconnectedInterfaces() {
			break
		}
		if err := id.autoconnect(info); err != nil && id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("failed to auto-connect discovered interface %v: %v", info.Name, err)
		}
	}

	id.monitorMu.Lock()
	id.initialAutoconnectRan = true
	id.monitorMu.Unlock()
}

func (id *InterfaceDiscovery) autoconnectCount() int {
	if id == nil || id.owner == nil || id.owner.transport == nil {
		return 0
	}

	count := 0
	for _, iface := range id.owner.transport.GetInterfaces() {
		if _, ok := iface.(interface{ AutoconnectHash() []byte }); ok {
			count++
		}
	}
	return count
}

func (id *InterfaceDiscovery) autoconnect(info DiscoveredInterface) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if id != nil && id.owner != nil && id.owner.logger != nil {
				id.owner.logger.Error("error while auto-connecting discovered interface: %v", recovered)
			}
			err = nil
		}
	}()

	if id == nil || id.owner == nil || id.owner.transport == nil || !id.owner.shouldAutoconnectDiscoveredInterfaces() {
		return nil
	}
	if id.autoconnectCount() >= id.owner.maxAutoconnectedInterfaces() {
		return nil
	}
	if info.Type != "BackboneInterface" {
		return nil
	}
	if info.ReachableOn == "" || info.Port == nil || *info.Port <= 0 {
		return fmt.Errorf("missing reachable_on/port")
	}
	if id.interfaceExists(info) {
		return nil
	}

	handler := func(data []byte, iface interfaces.Interface) {
		id.owner.transport.Inbound(data, iface)
	}
	iface, err := interfaces.NewBackboneClientInterface(info.Name, info.ReachableOn, *info.Port, handler)
	if err != nil {
		return err
	}

	if setter, ok := iface.(interface{ SetAutoconnect([]byte, string) }); ok {
		setter.SetAutoconnect(id.endpointHash(info), info.NetworkID)
	}
	if setter, ok := iface.(interface{ SetBitrate(int) }); ok {
		setter.SetBitrate(5_000_000)
	}
	if info.IFACNetname != "" || info.IFACNetkey != "" {
		if setter, ok := iface.(interface{ SetIFACConfig(interfaces.IFACConfig) }); ok {
			setter.SetIFACConfig(interfaces.IFACConfig{
				Enabled: true,
				NetName: info.IFACNetname,
				NetKey:  info.IFACNetkey,
				Size:    16,
			})
		}
	}
	id.owner.transport.RegisterInterface(iface)
	id.monitorInterface(iface)
	return nil
}

func (id *InterfaceDiscovery) interfaceExists(info DiscoveredInterface) bool {
	if id == nil || id.owner == nil || id.owner.transport == nil {
		return false
	}

	endpointHash := id.endpointHash(info)
	for _, iface := range id.owner.transport.GetInterfaces() {
		if meta, ok := iface.(interface{ AutoconnectHash() []byte }); ok && bytes.Equal(meta.AutoconnectHash(), endpointHash) {
			return true
		}

		hostPortMatcher, ok := iface.(interface {
			TargetHost() string
			TargetPort() int
		})
		if ok && hostPortMatcher.TargetHost() == info.ReachableOn {
			if info.Port == nil || hostPortMatcher.TargetPort() == *info.Port {
				return true
			}
		}

		b32Matcher, ok := iface.(interface{ B32() string })
		if ok && b32Matcher.B32() == info.ReachableOn {
			return true
		}
	}
	return false
}

func (id *InterfaceDiscovery) endpointHash(info DiscoveredInterface) []byte {
	endpoint := info.ReachableOn
	if info.Port != nil {
		endpoint = fmt.Sprintf("%v:%v", endpoint, *info.Port)
	}
	return FullHash([]byte(endpoint))
}

func (id *InterfaceDiscovery) monitorInterface(iface interfaces.Interface) {
	if id == nil || iface == nil {
		return
	}

	id.monitorMu.Lock()
	for _, existing := range id.monitoredInterfaces {
		if existing == iface {
			id.monitorMu.Unlock()
			return
		}
	}
	id.monitoredInterfaces = append(id.monitoredInterfaces, iface)
	if id.monitorInterval <= 0 || id.monitoringAutoconnects {
		id.monitorMu.Unlock()
		return
	}

	id.monitoringAutoconnects = true
	stopCh := make(chan struct{})
	id.monitorStopCh = stopCh
	interval := id.monitorInterval
	id.monitorMu.Unlock()

	go id.monitorLoop(stopCh, interval)
}

func (id *InterfaceDiscovery) monitorLoop(stopCh <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case now := <-ticker.C:
			id.monitorAutoconnectsOnce(now)
		}
	}
}

func (id *InterfaceDiscovery) monitorAutoconnectsOnce(now time.Time) {
	if id == nil {
		return
	}

	id.monitorMu.Lock()
	interfacesSnapshot := append([]interfaces.Interface(nil), id.monitoredInterfaces...)
	detached := make([]interfaces.Interface, 0)
	initialAutoconnectRan := id.initialAutoconnectRan
	onlineInterfaces := 0
	for _, iface := range interfacesSnapshot {
		if iface == nil {
			continue
		}
		id.checkMonitoredInterfaceState(now, iface, &onlineInterfaces, &detached)
	}
	id.monitorMu.Unlock()

	autoconnectedInterfaces := id.autoconnectCount()
	if id.owner != nil && id.owner.transport != nil {
		maxAutoconnectedInterfaces := id.owner.maxAutoconnectedInterfaces()
		if maxAutoconnectedInterfaces > 0 && onlineInterfaces >= maxAutoconnectedInterfaces {
			for _, iface := range id.owner.transport.GetInterfaces() {
				if !interfaceBootstrapOnly(iface) || containsInterface(detached, iface) {
					continue
				}
				detached = append(detached, iface)
			}
		}
		if onlineInterfaces == 0 && id.bootstrapInterfaceCount() == 0 {
			id.owner.reenableBootstrapInterfaces()
		}
	}
	if initialAutoconnectRan && id.owner != nil && id.owner.shouldAutoconnectDiscoveredInterfaces() {
		maxAutoconnectedInterfaces := id.owner.maxAutoconnectedInterfaces()
		freeSlots := max(0, maxAutoconnectedInterfaces-autoconnectedInterfaces)
		reservedSlots := maxAutoconnectedInterfaces / 4
		if freeSlots > reservedSlots {
			candidates, err := id.ListDiscoveredInterfaces(true, true)
			if err != nil {
				if id.owner.logger != nil {
					id.owner.logger.Error("failed loading discovered interfaces for monitor autoconnect: %v", err)
				}
			} else if len(candidates) > 0 {
				if id.shuffleCandidates != nil {
					id.shuffleCandidates(candidates)
				}
				candidate := candidates[0]
				if !id.interfaceExists(candidate) {
					if err := id.autoconnect(candidate); err != nil {
						if id.owner.logger != nil {
							id.owner.logger.Error("failed auto-connecting monitored discovered interface %v: %v", candidate.Name, err)
						}
					}
				}
			}
		}
	}

	for _, iface := range detached {
		id.teardownMonitoredInterface(iface)
	}
}

func (id *InterfaceDiscovery) checkMonitoredInterfaceState(now time.Time, iface interfaces.Interface, onlineInterfaces *int, detached *[]interfaces.Interface) {
	defer func() {
		if recovered := recover(); recovered != nil && id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("error while checking auto-connected interface state for %v: %v", iface, recovered)
		}
	}()

	if iface.Status() {
		*onlineInterfaces = *onlineInterfaces + 1
		delete(id.autoconnectDownSince, iface)
		return
	}

	downSince, ok := id.autoconnectDownSince[iface]
	if !ok {
		id.autoconnectDownSince[iface] = now
		return
	}
	if now.Sub(downSince) >= id.detachThreshold {
		*detached = append(*detached, iface)
	}
}

func (id *InterfaceDiscovery) teardownMonitoredInterface(iface interfaces.Interface) {
	defer func() {
		if recovered := recover(); recovered != nil && id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("error while de-registering auto-connected interface from transport: %v", recovered)
		}
	}()

	id.teardownInterface(iface)
}

func interfaceBootstrapOnly(iface interfaces.Interface) bool {
	if iface == nil {
		return false
	}
	getter, ok := iface.(interface{ BootstrapOnly() bool })
	return ok && getter.BootstrapOnly()
}

func (id *InterfaceDiscovery) bootstrapInterfaceCount() int {
	if id == nil || id.owner == nil || id.owner.transport == nil {
		return 0
	}
	count := 0
	for _, iface := range id.owner.transport.GetInterfaces() {
		if interfaceBootstrapOnly(iface) {
			count++
		}
	}
	return count
}

func containsInterface(interfacesList []interfaces.Interface, target interfaces.Interface) bool {
	for _, iface := range interfacesList {
		if iface == target {
			return true
		}
	}
	return false
}

func (id *InterfaceDiscovery) teardownInterface(iface interfaces.Interface) {
	if id == nil || iface == nil {
		return
	}

	if err := iface.Detach(); err != nil && id.owner != nil && id.owner.logger != nil {
		id.owner.logger.Error("failed detaching auto-connected interface %v: %v", iface.Name(), err)
	}
	if remover, ok := id.owner.transport.(interface{ RemoveInterface(interfaces.Interface) }); ok {
		remover.RemoveInterface(iface)
	}

	id.monitorMu.Lock()
	delete(id.autoconnectDownSince, iface)
	for i, existing := range id.monitoredInterfaces {
		if existing == iface {
			id.monitoredInterfaces = append(id.monitoredInterfaces[:i], id.monitoredInterfaces[i+1:]...)
			break
		}
	}
	id.monitorMu.Unlock()
}

func (h *InterfaceAnnounceHandler) decodeDiscoveryInfo(destinationHash []byte, announcedIdentity *Identity, packed, stamp []byte, value int) (map[string]any, error) {
	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		return nil, err
	}
	m := asAnyMap(unpacked)
	if m == nil {
		return nil, fmt.Errorf("unexpected discovery announce type %T", unpacked)
	}

	interfaceType := asString(lookupDiscoveryValue(m, discoveryFieldInterfaceType))
	if interfaceType == "" {
		return nil, fmt.Errorf("missing interface type")
	}
	requiredValue := func(field byte, name string) (any, error) {
		v, ok := lookupDiscovery(m, int(field))
		if !ok || v == nil {
			return nil, fmt.Errorf("missing %s", name)
		}
		return v, nil
	}
	transportID := asBytes(lookupDiscoveryValue(m, discoveryFieldTransportID))
	if len(transportID) == 0 {
		return nil, fmt.Errorf("missing transport ID")
	}
	transportValue, err := requiredValue(discoveryFieldTransport, "transport")
	if err != nil {
		return nil, err
	}
	nameValue, err := requiredValue(discoveryFieldName, "name")
	if err != nil {
		return nil, err
	}
	name := asString(nameValue)
	if name == "" {
		name = fmt.Sprintf("Discovered %v", interfaceType)
	}

	info := map[string]any{
		"type":         interfaceType,
		"transport":    asBool(transportValue),
		"name":         name,
		"received":     float64(time.Now().UnixNano()) / 1e9,
		"stamp":        append([]byte(nil), stamp...),
		"value":        value,
		"transport_id": hex.EncodeToString(transportID),
		"hops":         PathfinderM,
	}
	if h.owner != nil && h.owner.transport != nil {
		info["hops"] = h.owner.transport.HopsTo(destinationHash)
	}
	if announcedIdentity != nil {
		info["network_id"] = hex.EncodeToString(announcedIdentity.Hash)
	}
	if v, ok := lookupDiscovery(m, discoveryFieldLatitude); ok && v != nil {
		info["latitude"] = asFloat64(v)
	}
	if v, ok := lookupDiscovery(m, discoveryFieldLongitude); ok && v != nil {
		info["longitude"] = asFloat64(v)
	}
	if v, ok := lookupDiscovery(m, discoveryFieldHeight); ok && v != nil {
		info["height"] = asFloat64(v)
	}

	if reachableOn := asString(lookupDiscoveryValue(m, discoveryFieldReachableOn)); reachableOn != "" {
		if !isReachableOnValue(reachableOn) {
			return nil, fmt.Errorf("invalid reachable_on value")
		}
		info["reachable_on"] = reachableOn
	}
	if ifacNetname := asString(lookupDiscoveryValue(m, discoveryFieldIFACNetname)); ifacNetname != "" {
		info["ifac_netname"] = ifacNetname
	}
	if ifacNetkey := asString(lookupDiscoveryValue(m, discoveryFieldIFACNetkey)); ifacNetkey != "" {
		info["ifac_netkey"] = ifacNetkey
	}
	if v, ok := lookupDiscovery(m, discoveryFieldPort); ok && v != nil {
		info["port"] = asInt(v)
	}
	if v, ok := lookupDiscovery(m, discoveryFieldFrequency); ok && v != nil {
		info["frequency"] = asInt(v)
	}
	if v, ok := lookupDiscovery(m, discoveryFieldBandwidth); ok && v != nil {
		info["bandwidth"] = asInt(v)
	}
	if v, ok := lookupDiscovery(m, discoveryFieldSpreadingFactor); ok && v != nil {
		info["sf"] = asInt(v)
	}
	if v, ok := lookupDiscovery(m, discoveryFieldCodingRate); ok && v != nil {
		info["cr"] = asInt(v)
	}
	if modulation := asString(lookupDiscoveryValue(m, discoveryFieldModulation)); modulation != "" {
		info["modulation"] = modulation
	}
	if v, ok := lookupDiscovery(m, discoveryFieldChannel); ok && v != nil {
		info["channel"] = asInt(v)
	}

	requiredReachableOn := func() (string, error) {
		v, err := requiredValue(discoveryFieldReachableOn, "reachable_on")
		if err != nil {
			return "", err
		}
		reachableOn := asString(v)
		if !isReachableOnValue(reachableOn) {
			return "", fmt.Errorf("invalid reachable_on value")
		}
		return reachableOn, nil
	}

	switch interfaceType {
	case "BackboneInterface", "TCPServerInterface":
		reachableOn, err := requiredReachableOn()
		if err != nil {
			return nil, err
		}
		portValue, err := requiredValue(discoveryFieldPort, "port")
		if err != nil {
			return nil, err
		}
		info["reachable_on"] = reachableOn
		info["port"] = asInt(portValue)
	case "I2PInterface":
		reachableOn, err := requiredReachableOn()
		if err != nil {
			return nil, err
		}
		info["reachable_on"] = reachableOn
	case "RNodeInterface":
		frequency, err := requiredValue(discoveryFieldFrequency, "frequency")
		if err != nil {
			return nil, err
		}
		bandwidth, err := requiredValue(discoveryFieldBandwidth, "bandwidth")
		if err != nil {
			return nil, err
		}
		sf, err := requiredValue(discoveryFieldSpreadingFactor, "sf")
		if err != nil {
			return nil, err
		}
		cr, err := requiredValue(discoveryFieldCodingRate, "cr")
		if err != nil {
			return nil, err
		}
		info["frequency"] = asInt(frequency)
		info["bandwidth"] = asInt(bandwidth)
		info["sf"] = asInt(sf)
		info["cr"] = asInt(cr)
	case "WeaveInterface":
		frequency, err := requiredValue(discoveryFieldFrequency, "frequency")
		if err != nil {
			return nil, err
		}
		bandwidth, err := requiredValue(discoveryFieldBandwidth, "bandwidth")
		if err != nil {
			return nil, err
		}
		channel, err := requiredValue(discoveryFieldChannel, "channel")
		if err != nil {
			return nil, err
		}
		modulation, err := requiredValue(discoveryFieldModulation, "modulation")
		if err != nil {
			return nil, err
		}
		info["frequency"] = asInt(frequency)
		info["bandwidth"] = asInt(bandwidth)
		info["channel"] = asInt(channel)
		info["modulation"] = asString(modulation)
	case "KISSInterface":
		frequency, err := requiredValue(discoveryFieldFrequency, "frequency")
		if err != nil {
			return nil, err
		}
		bandwidth, err := requiredValue(discoveryFieldBandwidth, "bandwidth")
		if err != nil {
			return nil, err
		}
		modulation, err := requiredValue(discoveryFieldModulation, "modulation")
		if err != nil {
			return nil, err
		}
		info["frequency"] = asInt(frequency)
		info["bandwidth"] = asInt(bandwidth)
		info["modulation"] = asString(modulation)
	}

	info["config_entry"] = discoveryConfigEntry(info)
	info["discovery_hash"] = FullHash([]byte(info["transport_id"].(string) + name))
	return info, nil
}

func discoveryConfigEntry(info map[string]any) string {
	interfaceType := asString(info["type"])
	name := asString(info["name"])
	transportID := asString(info["transport_id"])
	reachableOn := asString(info["reachable_on"])
	ifacNetname := asString(info["ifac_netname"])
	ifacNetkey := asString(info["ifac_netkey"])
	cfgNetname := ""
	if ifacNetname != "" {
		cfgNetname = "\n  network_name = " + ifacNetname
	}
	cfgNetkey := ""
	if ifacNetkey != "" {
		cfgNetkey = "\n  passphrase = " + ifacNetkey
	}
	cfgIdentity := ""
	if transportID != "" {
		cfgIdentity = "\n  transport_identity = " + transportID
	}

	switch interfaceType {
	case "BackboneInterface", "TCPServerInterface":
		connectionType := "BackboneInterface"
		remoteKey := "remote"
		if runtime.GOOS == "windows" {
			connectionType = "TCPClientInterface"
			remoteKey = "target_host"
		}
		return fmt.Sprintf("[[%v]]\n  type = %v\n  enabled = yes\n  %v = %v\n  target_port = %v%v%v%v",
			name, connectionType, remoteKey, reachableOn, asInt(info["port"]), cfgIdentity, cfgNetname, cfgNetkey)
	case "I2PInterface":
		return fmt.Sprintf("[[%v]]\n  type = I2PInterface\n  enabled = yes\n  peers = %v%v%v%v",
			name, reachableOn, cfgIdentity, cfgNetname, cfgNetkey)
	case "RNodeInterface":
		return fmt.Sprintf("[[%v]]\n  type = RNodeInterface\n  enabled = yes\n  port = \n  frequency = %v\n  bandwidth = %v\n  spreadingfactor = %v\n  codingrate = %v\n  txpower = %v%v",
			name, asInt(info["frequency"]), asInt(info["bandwidth"]), asInt(info["sf"]), asInt(info["cr"]), cfgNetname, cfgNetkey)
	case "WeaveInterface":
		return fmt.Sprintf("[[%v]]\n  type = WeaveInterface\n  enabled = yes\n  port = %v%v",
			name, cfgNetname, cfgNetkey)
	case "KISSInterface":
		return fmt.Sprintf("[[%v]]\n  type = KISSInterface\n  enabled = yes\n  port = \n  # Frequency: %v\n  # Bandwidth: %v\n  # Modulation: %v%v%v%v",
			name, asInt(info["frequency"]), asInt(info["bandwidth"]), asString(info["modulation"]), cfgIdentity, cfgNetname, cfgNetkey)
	default:
		return ""
	}
}

func lookupDiscoveryValue(m map[any]any, key int) any {
	v, _ := lookupDiscovery(m, key)
	return v
}

func lookupDiscovery(m map[any]any, key int) (any, bool) {
	if m == nil {
		return nil, false
	}
	for mk, mv := range m {
		switch k := mk.(type) {
		case int:
			if k == key {
				return mv, true
			}
		case int8:
			if int(k) == key {
				return mv, true
			}
		case int16:
			if int(k) == key {
				return mv, true
			}
		case int32:
			if int(k) == key {
				return mv, true
			}
		case int64:
			if int(k) == key {
				return mv, true
			}
		case uint8:
			if int(k) == key {
				return mv, true
			}
		case uint16:
			if int(k) == key {
				return mv, true
			}
		case uint32:
			if int(k) == key {
				return mv, true
			}
		case uint64:
			if int(k) == key {
				return mv, true
			}
		}
	}
	return nil, false
}

func discoveryStampWorkblock(material []byte, expandRounds int) ([]byte, error) {
	if len(material) == 0 {
		return nil, fmt.Errorf("stamp workblock material is required")
	}
	if expandRounds <= 0 {
		expandRounds = discoveryWorkblockRounds
	}

	workblock := make([]byte, 0, expandRounds*256)
	for n := 0; n < expandRounds; n++ {
		nPacked, err := msgpack.Pack(int64(n))
		if err != nil {
			return nil, fmt.Errorf("pack round value %v: %w", n, err)
		}
		saltMaterial := make([]byte, 0, len(material)+len(nPacked))
		saltMaterial = append(saltMaterial, material...)
		saltMaterial = append(saltMaterial, nPacked...)
		salt := FullHash(saltMaterial)

		part, err := rcrypto.HKDF(256, material, salt, nil)
		if err != nil {
			return nil, fmt.Errorf("derive workblock round %v: %w", n, err)
		}
		workblock = append(workblock, part...)
	}

	return workblock, nil
}

func discoveryStampValue(workblock, stamp []byte) int {
	material := make([]byte, 0, len(workblock)+len(stamp))
	material = append(material, workblock...)
	material = append(material, stamp...)
	h := FullHash(material)
	return leadingZeroBits(h)
}

func discoveryStampValid(stamp []byte, targetCost int, workblock []byte) bool {
	if targetCost <= 0 {
		return true
	}
	if targetCost > 256 {
		return false
	}
	return discoveryStampValue(workblock, stamp) >= targetCost
}

func generateDiscoveryStamp(material []byte, targetCost int) ([]byte, int, error) {
	if targetCost <= 0 {
		return nil, 0, nil
	}
	workblock, err := discoveryStampWorkblock(material, discoveryWorkblockRounds)
	if err != nil {
		return nil, 0, err
	}
	for {
		candidate := make([]byte, discoveryStampSize)
		if _, err := rand.Read(candidate); err != nil {
			return nil, 0, err
		}
		if discoveryStampValid(candidate, targetCost, workblock) {
			return candidate, discoveryStampValue(workblock, candidate), nil
		}
	}
}

func leadingZeroBits(data []byte) int {
	count := 0
	for _, b := range data {
		if b == 0 {
			count += 8
			continue
		}
		count += bits.LeadingZeros8(uint8(b))
		break
	}
	return count
}
