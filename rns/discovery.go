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
	"math"
	"math/big"
	"math/bits"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
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

func advertisedI2PReachableOn(iface interfaces.Interface, fallback string) string {
	if iface == nil {
		return fallback
	}
	connectable, ok := iface.(interface{ Connectable() bool })
	if !ok || !connectable.Connectable() {
		return fallback
	}
	b32Getter, ok := iface.(interface{ B32() string })
	if !ok {
		return fallback
	}
	if b32 := sanitizeDiscoveryString(b32Getter.B32()); b32 != "" {
		return b32
	}
	return fallback
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
	case "BackboneInterface", "TCPServerInterface", "TCPClientInterface", "I2PInterface", "RNodeInterface", "WeaveInterface", "KISSInterface":
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

	switch advertisedType {
	case "BackboneInterface", "TCPServerInterface":
		info[discoveryFieldReachableOn] = reachableOn
		portGetter, ok := iface.(interface{ BindPort() int })
		if !ok || portGetter.BindPort() <= 0 {
			return nil, fmt.Errorf("missing bind port")
		}
		info[discoveryFieldPort] = portGetter.BindPort()
	case "I2PInterface":
		info[discoveryFieldReachableOn] = advertisedI2PReachableOn(iface, reachableOn)
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
			info[discoveryFieldIFACNetname] = sanitizeDiscoveryString(ifacCfg.NetName)
			info[discoveryFieldIFACNetkey] = sanitizeDiscoveryString(ifacCfg.NetKey)
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

	hasConfigEntry bool
	endpoint       discoveryEndpoint
}

type discoveredRecord struct {
	item      DiscoveredInterface
	sortValue float64
	rawValue  any
}

type discoveryBackboneClientConfig struct {
	Name       string
	TargetHost any
	TargetPort any
}

type discoveryEndpoint struct {
	hasReachableOnValue       bool
	rawReachableOn            string
	rawReachableOnValue       any
	stringReachableOn         string
	hasStringReachableOnMatch bool
	rawPort                   string
	rawPortValue              any
	comparablePort            *int
	hasSpecifiedPort          bool
}

func cloneDiscoveryEndpointValue(v any) any {
	if b, ok := v.([]byte); ok {
		return append([]byte(nil), b...)
	}
	return v
}

func newDiscoveryEndpoint(reachableOn any, hasReachableOn bool, port any, hasPort bool) discoveryEndpoint {
	var endpoint discoveryEndpoint

	if hasReachableOn {
		endpoint.hasReachableOnValue = true
		endpoint.rawReachableOn = discoveryDisplayString(reachableOn, true)
		endpoint.rawReachableOnValue = cloneDiscoveryEndpointValue(reachableOn)
		if reachableOnString, ok := reachableOn.(string); ok {
			endpoint.stringReachableOn = reachableOnString
			endpoint.hasStringReachableOnMatch = true
		}
	}

	if hasPort {
		endpoint.hasSpecifiedPort = true
		endpoint.rawPort = pythonDiscoveryValueString(port)
		endpoint.rawPortValue = cloneDiscoveryEndpointValue(port)
		if comparablePort, ok := discoveryComparablePortValue(port); ok {
			endpoint.comparablePort = &comparablePort
		}
	}

	return endpoint
}

func (e discoveryEndpoint) hashInput(fallbackReachableOn string, fallbackPort *int) string {
	endpoint := fallbackReachableOn
	if e.hasReachableOnValue {
		endpoint = e.rawReachableOn
	}

	port := ""
	if e.hasSpecifiedPort {
		port = e.rawPort
	} else if fallbackPort != nil {
		port = fmt.Sprintf("%v", *fallbackPort)
	}

	if port != "" {
		endpoint = fmt.Sprintf("%v:%v", endpoint, port)
	}

	return endpoint
}

func (e discoveryEndpoint) allowsStringReachableOnMatch(fallbackReachableOn string) bool {
	return e.hasStringReachableOnMatch || (fallbackReachableOn != "" && !e.hasReachableOnValue)
}

func (e discoveryEndpoint) reachableOnForMatch(fallbackReachableOn string) string {
	if e.hasStringReachableOnMatch {
		return e.stringReachableOn
	}
	return fallbackReachableOn
}

func (e discoveryEndpoint) matchesTargetPort(targetPort int, fallbackPort *int) bool {
	if !e.hasSpecifiedPort {
		return fallbackPort == nil || (fallbackPort != nil && targetPort == *fallbackPort)
	}
	return e.comparablePort != nil && targetPort == *e.comparablePort
}

func (e discoveryEndpoint) backboneClientConfig(name, fallbackReachableOn string, fallbackPort *int) (discoveryBackboneClientConfig, bool) {
	if !e.hasReachableOnValue && fallbackReachableOn == "" {
		return discoveryBackboneClientConfig{}, false
	}
	if !e.hasSpecifiedPort && fallbackPort == nil {
		return discoveryBackboneClientConfig{}, false
	}

	cfg := discoveryBackboneClientConfig{Name: name}
	if e.hasReachableOnValue {
		cfg.TargetHost = cloneDiscoveryEndpointValue(e.rawReachableOnValue)
	} else {
		cfg.TargetHost = fallbackReachableOn
	}
	if e.hasSpecifiedPort {
		cfg.TargetPort = cloneDiscoveryEndpointValue(e.rawPortValue)
	} else if fallbackPort != nil {
		cfg.TargetPort = *fallbackPort
	}

	return cfg, true
}

type discoveryBackboneFactory func(discoveryBackboneClientConfig, interfaces.InboundHandler) (interfaces.Interface, error)

func defaultDiscoveryBackboneClientInterface(config discoveryBackboneClientConfig, handler interfaces.InboundHandler) (interfaces.Interface, error) {
	targetHost, ok := discoveryBackboneTargetHostValue(config.TargetHost)
	if !ok {
		return nil, fmt.Errorf("missing reachable_on/port")
	}
	targetPort, err := discoveryBackboneTargetPortValue(config.TargetPort)
	if err != nil {
		return nil, err
	}
	return interfaces.NewBackboneClientInterface(config.Name, targetHost, targetPort, handler)
}

func discoveryBackboneTargetHostValue(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case []byte:
		return string(t), true
	default:
		return "", false
	}
}

func discoveryBackboneTargetPortValue(v any) (int, error) {
	switch t := v.(type) {
	case nil:
		return 0, fmt.Errorf("int() argument must be a string, a bytes-like object or a real number, not 'NoneType'")
	case bool:
		return boolToInt(t), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, fmt.Errorf("invalid literal for int() with base 10: %q", t)
		}
		return i, nil
	case []byte:
		i, err := strconv.Atoi(strings.TrimSpace(string(t)))
		if err != nil {
			return 0, fmt.Errorf("invalid literal for int() with base 10: %v", pythonDiscoveryValueString(t))
		}
		return i, nil
	case float32:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("cannot convert float %v to integer", t)
		}
		return int(t), nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, fmt.Errorf("cannot convert float %v to integer", t)
		}
		return int(t), nil
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()), nil
	default:
		return 0, fmt.Errorf("int() argument must be a string, a bytes-like object or a real number, not %q", rv.Type())
	}
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
	backboneFactory        discoveryBackboneFactory
}

// NewInterfaceDiscovery initializes a discovery listener bound to the provided local Reticulum configuration.
func NewInterfaceDiscovery(owner *Reticulum) *InterfaceDiscovery {
	return &InterfaceDiscovery{
		owner:                owner,
		autoconnectDownSince: make(map[interfaces.Interface]time.Time),
		monitorInterval:      5 * time.Second,
		detachThreshold:      12 * time.Second,
		shuffleCandidates:    shuffleDiscoveredInterfaces,
		backboneFactory:      defaultDiscoveryBackboneClientInterface,
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

func (id *InterfaceDiscovery) logDiscoveredInterface(info map[string]any) {
	if id == nil || id.owner == nil || id.owner.logger == nil {
		return
	}

	hops := asInt(info["hops"])
	suffix := "s"
	if hops == 1 {
		suffix = ""
	}

	id.owner.logger.Debug(
		"Discovered %v %v hop%v away with stamp value %v: %v",
		pythonDiscoveryValueString(info["type"]),
		hops,
		suffix,
		pythonDiscoveryValueString(info["value"]),
		pythonDiscoveryValueString(info["name"]),
	)
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
		if err := validateDiscoveredInfoForProcessing(info); err != nil {
			if id.owner != nil && id.owner.logger != nil {
				id.owner.logger.Error("Error processing discovered interface data: %v", err)
			}
			return
		}
		id.logDiscoveredInterface(info)
		if err := id.persistDiscoveredInterface(info); err != nil && id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("Error while persisting discovered interface data: %v", err)
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

	storagePath := filepath.Join(id.owner.configDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return err
	}

	filePath := filepath.Join(storagePath, discoveryHash+".data")
	persisted := cloneStringAnyMap(info)
	receivedValue, receivedPresent := info["received"]
	receivedAt, receivedOK := discoveryReceivedTimestamp(receivedValue)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if !receivedPresent {
			if err := createEmptyDiscoveryCacheFile(filePath); err != nil {
				return err
			}
			return fmt.Errorf("'received'")
		}
		if receivedValue == nil {
			persisted["last_heard"] = nil
			persisted["discovered"] = nil
			persisted["heard_count"] = 0
			info["last_heard"] = nil
			info["discovered"] = nil
			info["heard_count"] = 0
			data, err := msgpack.Pack(persisted)
			if err != nil {
				return err
			}
			return os.WriteFile(filePath, data, 0o644)
		}
		if !receivedOK {
			persisted["last_heard"] = receivedValue
			persisted["discovered"] = receivedValue
			persisted["heard_count"] = 0
			info["last_heard"] = receivedValue
			info["discovered"] = receivedValue
			info["heard_count"] = 0
			data, err := msgpack.Pack(persisted)
			if err != nil {
				return err
			}
			return os.WriteFile(filePath, data, 0o644)
		}
		persisted["last_heard"] = receivedAt
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
	if !ok {
		return fmt.Errorf("'discovered'")
	}
	heardCountValue, ok := lastInfo["heard_count"]
	if !ok {
		return fmt.Errorf("'heard_count'")
	}

	discoveredPersisted := discoveredValue
	if discoveredPersisted == nil {
		replacement, ok := info["discovered"]
		if !ok {
			return fmt.Errorf("'discovered'")
		}
		discoveredPersisted = replacement
	}
	if !receivedPresent {
		if err := createEmptyDiscoveryCacheFile(filePath); err != nil {
			return err
		}
		return fmt.Errorf("'received'")
	}
	lastHeardPersisted := receivedValue
	if receivedOK {
		lastHeardPersisted = receivedAt
	}

	var heardCountPersisted any = 1
	if heardCountValue != nil {
		var ok bool
		heardCountPersisted, ok = incrementDiscoveryHeardCount(heardCountValue)
		if !ok {
			info["discovered"] = discoveredPersisted
			info["last_heard"] = lastHeardPersisted
			if err := createEmptyDiscoveryCacheFile(filePath); err != nil {
				return err
			}
			return discoveryHeardCountIncrementError(heardCountValue)
		}
	}

	if receivedValue == nil {
		persisted["last_heard"] = nil
		persisted["discovered"] = discoveredPersisted
		persisted["heard_count"] = heardCountPersisted
		info["last_heard"] = nil
		info["discovered"] = discoveredPersisted
		info["heard_count"] = heardCountPersisted
		data, err := msgpack.Pack(persisted)
		if err != nil {
			return err
		}
		return os.WriteFile(filePath, data, 0o644)
	}
	if !receivedOK {
		persisted["last_heard"] = receivedValue
		persisted["discovered"] = discoveredPersisted
		persisted["heard_count"] = heardCountPersisted
		info["last_heard"] = receivedValue
		info["discovered"] = discoveredPersisted
		info["heard_count"] = heardCountPersisted
		data, err := msgpack.Pack(persisted)
		if err != nil {
			return err
		}
		return os.WriteFile(filePath, data, 0o644)
	}

	persisted["last_heard"] = receivedAt
	persisted["discovered"] = discoveredPersisted
	persisted["heard_count"] = heardCountPersisted
	info["last_heard"] = receivedAt
	info["discovered"] = discoveredPersisted
	info["heard_count"] = heardCountPersisted

	data, err := msgpack.Pack(persisted)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0o644)
}

func createEmptyDiscoveryCacheFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
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
	var discovered []discoveredRecord

	for _, entry := range entries {
		path := filepath.Join(storagePath, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			id.logDiscoveryFileLoadError(path, err)
			continue
		}

		unpacked, err := msgpack.Unpack(data)
		if err != nil {
			id.logDiscoveryFileLoadError(path, err)
			continue
		}
		m, ok := unpacked.(map[any]any)
		if !ok {
			id.logDiscoveryFileLoadError(path, fmt.Errorf("unexpected discovery cache type %T", unpacked))
			continue
		}

		heardValue, ok := lookupAny(m, "last_heard")
		if !ok || heardValue == nil {
			id.logDiscoveryFileLoadError(path, fmt.Errorf("corrupt discovery cache missing last_heard"))
			continue
		}
		heardAt, ok := numericFloat64Value(heardValue)
		if !ok {
			id.logDiscoveryFileLoadError(path, fmt.Errorf("invalid discovery cache last_heard type %T", heardValue))
			continue
		}
		heardDelta := now - heardAt

		shouldRemove := false
		if heardDelta > ThresholdRemove {
			shouldRemove = true
		} else if len(discoverySources) > 0 {
			networkIDValue, ok := lookupAny(m, "network_id")
			if !ok || networkIDValue == nil {
				shouldRemove = true
			} else {
				networkIDHex, ok := networkIDValue.(string)
				if !ok {
					id.logDiscoveryFileLoadError(path, fmt.Errorf("invalid discovery cache network_id type %T", networkIDValue))
					continue
				}
				networkID, err := hex.DecodeString(networkIDHex)
				if err != nil {
					id.logDiscoveryFileLoadError(path, err)
					continue
				}
				if !hasDiscoverySource(discoverySources, networkID) {
					shouldRemove = true
				}
			}
		}
		if !shouldRemove {
			if reachableValue, ok := lookupAny(m, "reachable_on"); ok {
				reachableOn, validateString, err := discoveryReachableOnCacheValue(reachableValue)
				if err != nil {
					id.logDiscoveryFileLoadError(path, err)
					continue
				}
				if validateString && !isReachableOnValue(reachableOn) {
					shouldRemove = true
				}
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

		transportValue, hasTransport := lookupAny(m, "transport")
		if (onlyAvailable || onlyTransport) && !hasTransport {
			id.logDiscoveryFileLoadError(path, fmt.Errorf("corrupt discovery cache missing transport"))
			continue
		}

		if onlyAvailable && status != "available" {
			continue
		}

		transport := discoveryTruthyBool(transportValue)
		if onlyTransport && !transport {
			continue
		}

		valueField, ok := lookupAny(m, "value")
		if !ok {
			return nil, fmt.Errorf("corrupt discovery cache missing value in %v", path)
		}
		value, _ := numericIntValue(valueField)
		sortValue, _ := numericFloat64Value(valueField)

		nameValue, hasName := lookupAny(m, "name")
		typeValue, hasType := lookupAny(m, "type")
		configEntryValue, hasConfigEntry := lookupAny(m, "config_entry")
		ifacNetnameValue, hasIFACNetname := lookupAny(m, "ifac_netname")
		ifacNetkeyValue, hasIFACNetkey := lookupAny(m, "ifac_netkey")
		modulationValue, hasModulation := lookupAny(m, "modulation")
		networkIDValue, hasNetworkID := lookupAny(m, "network_id")
		transportIDValue, hasTransportID := lookupAny(m, "transport_id")

		di := DiscoveredInterface{
			Name:        discoveryDisplayString(nameValue, hasName),
			Type:        discoveryDisplayString(typeValue, hasType),
			Status:      status,
			StatusCode:  statusCode,
			Hops:        asInt(lookupAnyValue(m, "hops")),
			Discovered:  asFloat64(lookupAnyValue(m, "discovered")),
			LastHeard:   heardAt,
			Transport:   transport,
			Value:       value,
			ConfigEntry: discoveryDisplayString(configEntryValue, hasConfigEntry),
			NetworkID:   discoveryDisplayString(networkIDValue, hasNetworkID),
			TransportID: discoveryDisplayString(transportIDValue, hasTransportID),
			ReachableOn: discoveryReachableOnDisplayValue(lookupAnyValue(m, "reachable_on")),
			Modulation:  discoveryDisplayString(modulationValue, hasModulation),
			IFACNetname: discoveryDisplayString(ifacNetnameValue, hasIFACNetname),
			IFACNetkey:  discoveryDisplayString(ifacNetkeyValue, hasIFACNetkey),

			hasConfigEntry: hasConfigEntry,
			endpoint:       newDiscoveryEndpoint(lookupAnyValue(m, "reachable_on"), hasAnyKey(m, "reachable_on"), lookupAnyValue(m, "port"), hasAnyKey(m, "port")),
		}

		di.Latitude = lookupOptFloat64(m, "latitude")
		di.Longitude = lookupOptFloat64(m, "longitude")
		di.Height = lookupOptFloat64(m, "height")
		di.Port = lookupOptInt(m, "port")
		di.Frequency = lookupOptInt(m, "frequency")
		di.Bandwidth = lookupOptInt(m, "bandwidth")
		di.SF = lookupOptInt(m, "sf")
		di.CR = lookupOptInt(m, "cr")

		discovered = append(discovered, discoveredRecord{
			item:      di,
			sortValue: sortValue,
			rawValue:  valueField,
		})
	}

	if err := sortDiscoveredRecords(discovered); err != nil {
		return nil, err
	}

	out := make([]DiscoveredInterface, 0, len(discovered))
	for _, record := range discovered {
		out = append(out, record.item)
	}
	return out, nil
}

func sortDiscoveredRecords(records []discoveredRecord) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			switch t := recovered.(type) {
			case error:
				err = t
			default:
				panic(recovered)
			}
		}
	}()

	sort.Slice(records, func(i, j int) bool {
		left := records[i]
		right := records[j]
		if left.item.StatusCode != right.item.StatusCode {
			return left.item.StatusCode > right.item.StatusCode
		}
		if cmp, cmpErr := compareDiscoverySortValues(left.rawValue, right.rawValue); cmpErr != nil {
			panic(cmpErr)
		} else if cmp != 0 {
			return cmp > 0
		}
		return left.item.LastHeard > right.item.LastHeard
	})
	return nil
}

func compareDiscoverySortValues(left, right any) (int, error) {
	if left == nil || right == nil {
		switch {
		case left == nil && right == nil:
			return 0, nil
		default:
			return 0, fmt.Errorf("invalid mixed discovery cache value types %T and %T", left, right)
		}
	}

	if lf, lok := numericFloat64Value(left); lok {
		if rf, rok := numericFloat64Value(right); rok {
			switch {
			case lf > rf:
				return 1, nil
			case lf < rf:
				return -1, nil
			default:
				return 0, nil
			}
		}
	}

	switch l := left.(type) {
	case string:
		r, ok := right.(string)
		if !ok {
			return 0, fmt.Errorf("invalid mixed discovery cache value types %T and %T", left, right)
		}
		return strings.Compare(l, r), nil
	case []byte:
		r, ok := right.([]byte)
		if !ok {
			return 0, fmt.Errorf("invalid mixed discovery cache value types %T and %T", left, right)
		}
		return bytes.Compare(l, r), nil
	}

	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() {
		return 0, fmt.Errorf("invalid mixed discovery cache value types %T and %T", left, right)
	}
	if !isDiscoverySequenceKind(leftValue.Kind()) || !isDiscoverySequenceKind(rightValue.Kind()) {
		return 0, fmt.Errorf("invalid discovery cache value type %T", left)
	}
	return compareDiscoverySequences(leftValue, rightValue)
}

func isDiscoverySequenceKind(kind reflect.Kind) bool {
	return kind == reflect.Slice || kind == reflect.Array
}

func compareDiscoverySequences(left, right reflect.Value) (int, error) {
	minLen := min(left.Len(), right.Len())
	for i := 0; i < minLen; i++ {
		cmp, err := compareDiscoverySortValues(left.Index(i).Interface(), right.Index(i).Interface())
		if err != nil {
			return 0, err
		}
		if cmp != 0 {
			return cmp, nil
		}
	}
	switch {
	case left.Len() > right.Len():
		return 1, nil
	case left.Len() < right.Len():
		return -1, nil
	default:
		return 0, nil
	}
}

func hasDiscoverySource(sources [][]byte, networkID []byte) bool {
	for _, source := range sources {
		if bytes.Equal(source, networkID) {
			return true
		}
	}
	return false
}

func (id *InterfaceDiscovery) logDiscoveryFileLoadError(path string, err error) {
	if err == nil || id == nil || id.owner == nil || id.owner.logger == nil {
		return
	}
	id.owner.logger.Error("error while loading discovered interface data: %v", err)
	id.owner.logger.Error("the interface data file %v may be corrupt", path)
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

func discoveryTruthyBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case int32:
		return t != 0
	case uint:
		return t != 0
	case uint64:
		return t != 0
	case uint32:
		return t != 0
	case float64:
		return t != 0
	case float32:
		return t != 0
	case string:
		return t != ""
	case []byte:
		return len(t) != 0
	case []any:
		return len(t) != 0
	case map[any]any:
		return len(t) != 0
	case map[string]any:
		return len(t) != 0
	default:
		return v != nil
	}
}

func discoveryReachableOnCacheValue(v any) (string, bool, error) {
	switch t := v.(type) {
	case string:
		if t == "" {
			return "", true, fmt.Errorf("invalid discovery cache reachable_on value")
		}
		return t, true, nil
	default:
		if s, ok := discoveryScalarString(v); ok {
			return s, false, nil
		}
		return "", true, fmt.Errorf("invalid discovery cache reachable_on type %T", v)
	}
}

func discoveryReachableOnDisplayValue(v any) string {
	if s, ok := discoveryScalarString(v); ok {
		return s
	}
	return ""
}

func isHostname(v string) bool {
	if len(v) == 0 {
		return false
	}
	if v[len(v)-1] == '.' {
		v = v[:len(v)-1]
	}
	if len(v) == 0 || len(v) > 253 {
		return false
	}
	labels := strings.Split(v, ".")
	last := labels[len(labels)-1]
	allDigits := len(last) > 0
	for _, r := range last {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return false
	}
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
		if s, ok := discoveryHexLikeString(v); ok {
			if s == "" {
				return "", fmt.Errorf("missing discovery hash")
			}
			return s, nil
		}
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

func discoveryReceivedTimestamp(v any) (float64, bool) {
	if _, ok := v.(bool); ok {
		return 0, false
	}
	return numericFloat64Value(v)
}

func discoveryScalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	default:
		return "", false
	}
}

func discoveryNumericHexString(v any) (string, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%02x", rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%02x", rv.Uint()), true
	default:
		return "", false
	}
}

func incrementDiscoveryHeardCount(v any) (any, bool) {
	switch t := v.(type) {
	case bool:
		return boolToInt(t) + 1, true
	case int, int8, int16, int32:
		return incrementDiscoverySignedInt(v)
	case int64:
		return t + 1, true
	case uint, uint8, uint16, uint32, uint64:
		return incrementDiscoveryUnsignedInt(v)
	case float32, float64:
		return incrementDiscoveryFloat(v)
	default:
		return nil, false
	}
}

func discoveryHeardCountIncrementError(v any) error {
	switch v.(type) {
	case string:
		return fmt.Errorf(`can only concatenate str (not "int") to str`)
	default:
		return fmt.Errorf("invalid heard_count type %T", v)
	}
}

func incrementDiscoverySignedInt(v any) (int, bool) {
	i, ok := numericIntValue(v)
	if !ok {
		return 0, false
	}
	return i + 1, true
}

func incrementDiscoveryUnsignedInt(v any) (any, bool) {
	switch t := v.(type) {
	case uint:
		return t + 1, true
	case uint8:
		return uint(t) + 1, true
	case uint16:
		return uint(t) + 1, true
	case uint32:
		return uint64(t) + 1, true
	case uint64:
		return t + 1, true
	default:
		return nil, false
	}
}

func incrementDiscoveryFloat(v any) (float64, bool) {
	f, ok := numericFloat64Value(v)
	if !ok {
		return 0, false
	}
	return f + 1, true
}

func validateDiscoveredInfoForProcessing(info map[string]any) error {
	if info == nil {
		return fmt.Errorf("'NoneType' object is not subscriptable")
	}
	for _, key := range []string{"name", "value", "type", "discovery_hash", "hops"} {
		if _, ok := info[key]; !ok {
			return fmt.Errorf("'%v'", key)
		}
	}
	if info["discovery_hash"] == nil {
		return fmt.Errorf("unsupported format string passed to NoneType.__format__")
	}
	if _, ok := info["discovery_hash"].(string); ok {
		return fmt.Errorf("Unknown format code 'x' for object of type 'str'")
	}
	if !processableDiscoveryHashValue(info["discovery_hash"]) {
		return fmt.Errorf("invalid discovery_hash type %T", info["discovery_hash"])
	}
	return nil
}

func processableDiscoveryHashValue(v any) bool {
	if _, ok := v.(string); ok {
		return false
	}
	_, ok := discoveryHexLikeString(v)
	return ok
}

func mapToDiscoveredInterface(info map[string]any) (DiscoveredInterface, bool) {
	if info == nil {
		return DiscoveredInterface{}, false
	}

	nameValue, hasName := info["name"]
	typeValue, hasType := info["type"]
	ifacNetnameValue, hasIFACNetname := info["ifac_netname"]
	ifacNetkeyValue, hasIFACNetkey := info["ifac_netkey"]
	networkIDValue, hasNetworkID := info["network_id"]
	reachableOnValue, hasReachableOn := info["reachable_on"]

	_, hasConfigEntry := info["config_entry"]

	out := DiscoveredInterface{
		Name:        discoveryDisplayString(nameValue, hasName),
		Type:        discoveryDisplayString(typeValue, hasType),
		Status:      asString(info["status"]),
		StatusCode:  asInt(info["status_code"]),
		Hops:        asInt(info["hops"]),
		Discovered:  asFloat64(info["discovered"]),
		LastHeard:   asFloat64(info["last_heard"]),
		Transport:   discoveryTruthyBool(info["transport"]),
		Value:       asInt(info["value"]),
		ConfigEntry: asString(info["config_entry"]),
		NetworkID:   discoveryDisplayString(networkIDValue, hasNetworkID),
		TransportID: asString(info["transport_id"]),
		ReachableOn: asString(reachableOnValue),
		Modulation:  asString(info["modulation"]),
		IFACNetname: discoveryDisplayString(ifacNetnameValue, hasIFACNetname),
		IFACNetkey:  discoveryDisplayString(ifacNetkeyValue, hasIFACNetkey),

		hasConfigEntry: hasConfigEntry,
		endpoint:       newDiscoveryEndpoint(reachableOnValue, hasReachableOn, info["port"], hasStringKey(info, "port")),
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

	return out, hasType
}

func discoveryDisplayString(v any, present bool) string {
	if !present {
		return ""
	}
	return pythonDiscoveryValueString(v)
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
	if id.interfaceExists(info) {
		return nil
	}
	if !info.hasConfigEntry {
		return fmt.Errorf("missing config_entry")
	}
	config, ok := info.endpoint.backboneClientConfig(info.Name, info.ReachableOn, info.Port)
	if !ok {
		return fmt.Errorf("missing reachable_on/port")
	}

	handler := func(data []byte, iface interfaces.Interface) {
		id.owner.transport.Inbound(data, iface)
	}
	iface, err := id.backboneFactory(config, handler)
	if err != nil {
		if id != nil && id.owner != nil && id.owner.logger != nil {
			id.owner.logger.Error("error while auto-connecting discovered interface: %v", err)
		}
		return nil
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
	allowStringReachableOnMatch := info.endpoint.allowsStringReachableOnMatch(info.ReachableOn)
	for _, iface := range id.owner.transport.GetInterfaces() {
		if meta, ok := iface.(interface{ AutoconnectHash() []byte }); ok && bytes.Equal(meta.AutoconnectHash(), endpointHash) {
			return true
		}

		if allowStringReachableOnMatch {
			hostPortMatcher, ok := iface.(interface {
				TargetHost() string
				TargetPort() int
			})
			if ok && hostPortMatcher.TargetHost() == info.endpoint.reachableOnForMatch(info.ReachableOn) {
				if info.endpoint.matchesTargetPort(hostPortMatcher.TargetPort(), info.Port) {
					return true
				}
			}

			b32Matcher, ok := iface.(interface{ B32() string })
			if ok && b32Matcher.B32() == info.endpoint.reachableOnForMatch(info.ReachableOn) {
				return true
			}
		}
	}
	return false
}

func (id *InterfaceDiscovery) endpointHash(info DiscoveredInterface) []byte {
	return FullHash([]byte(info.endpoint.hashInput(info.ReachableOn, info.Port)))
}

func discoveryComparablePortValue(v any) (int, bool) {
	switch t := v.(type) {
	case bool:
		return boolToInt(t), true
	case float32:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
			return 0, false
		}
		return int(f), true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || math.Trunc(t) != t {
			return 0, false
		}
		return int(t), true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()), true
	default:
		return 0, false
	}
}

func hasAnyKey(m map[any]any, key string) bool {
	_, ok := lookupAny(m, key)
	return ok
}

func hasStringKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
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
		if onlineInterfaces >= maxAutoconnectedInterfaces {
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
	if announcedIdentity == nil {
		return nil, fmt.Errorf("missing announced identity")
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
	transportIDValue, ok := lookupDiscovery(m, discoveryFieldTransportID)
	if !ok {
		return nil, fmt.Errorf("missing transport ID")
	}
	transportID, ok := discoveryHexValue(transportIDValue)
	if !ok {
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
	name, err := discoveryAnnounceNameValue(nameValue, interfaceType)
	if err != nil {
		return nil, err
	}

	info := map[string]any{
		"type":         interfaceType,
		"transport":    transportValue,
		"name":         name,
		"received":     float64(time.Now().UnixNano()) / 1e9,
		"stamp":        append([]byte(nil), stamp...),
		"value":        value,
		"transport_id": transportID,
		"hops":         PathfinderM,
	}
	if h.owner != nil && h.owner.transport != nil {
		info["hops"] = h.owner.transport.HopsTo(destinationHash)
	}
	info["network_id"] = hex.EncodeToString(announcedIdentity.Hash)
	for _, field := range []struct {
		key  int
		name string
	}{
		{key: discoveryFieldLatitude, name: "latitude"},
		{key: discoveryFieldLongitude, name: "longitude"},
		{key: discoveryFieldHeight, name: "height"},
	} {
		v, ok := lookupDiscovery(m, field.key)
		if !ok {
			return nil, fmt.Errorf("missing %v", field.name)
		}
		info[field.name] = v
	}

	if reachableOn, ok := lookupDiscovery(m, discoveryFieldReachableOn); ok {
		if !validDiscoveryAnnounceReachableOn(reachableOn) {
			return nil, fmt.Errorf("invalid reachable_on value")
		}
	}
	if ifacNetname, ok := lookupDiscovery(m, discoveryFieldIFACNetname); ok {
		info["ifac_netname"] = ifacNetname
	}
	if ifacNetkey, ok := lookupDiscovery(m, discoveryFieldIFACNetkey); ok {
		info["ifac_netkey"] = ifacNetkey
	}

	requiredReachableOn := func() (any, error) {
		v, err := requiredValue(discoveryFieldReachableOn, "reachable_on")
		if err != nil {
			return nil, err
		}
		if !validDiscoveryAnnounceReachableOn(v) {
			return nil, fmt.Errorf("invalid reachable_on value")
		}
		return v, nil
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
		info["port"] = portValue
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
		info["frequency"] = frequency
		info["bandwidth"] = bandwidth
		info["sf"] = sf
		info["cr"] = cr
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
		info["frequency"] = frequency
		info["bandwidth"] = bandwidth
		info["channel"] = channel
		info["modulation"] = modulation
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
		info["frequency"] = frequency
		info["bandwidth"] = bandwidth
		info["modulation"] = modulation
	}

	if configEntry := discoveryConfigEntry(info); configEntry != "" {
		info["config_entry"] = configEntry
	}
	info["discovery_hash"] = FullHash([]byte(info["transport_id"].(string) + name))
	return info, nil
}

func discoveryConfigEntry(info map[string]any) string {
	interfaceType := asString(info["type"])
	name := asString(info["name"])
	transportID := asString(info["transport_id"])
	reachableOn := pythonDiscoveryValueString(info["reachable_on"])
	cfgNetname := ""
	if ifacNetname, ok := info["ifac_netname"]; ok && discoveryTruthyBool(ifacNetname) {
		cfgNetname = "\n  network_name = " + pythonDiscoveryValueString(ifacNetname)
	}
	cfgNetkey := ""
	if ifacNetkey, ok := info["ifac_netkey"]; ok && discoveryTruthyBool(ifacNetkey) {
		cfgNetkey = "\n  passphrase = " + pythonDiscoveryValueString(ifacNetkey)
	}
	cfgIdentity := "\n  transport_identity = " + transportID

	switch interfaceType {
	case "BackboneInterface", "TCPServerInterface":
		connectionType := "BackboneInterface"
		remoteKey := "remote"
		if runtime.GOOS == "windows" {
			connectionType = "TCPClientInterface"
			remoteKey = "target_host"
		}
		return fmt.Sprintf("[[%v]]\n  type = %v\n  enabled = yes\n  %v = %v\n  target_port = %v%v%v%v",
			name, connectionType, remoteKey, reachableOn, pythonDiscoveryValueString(info["port"]), cfgIdentity, cfgNetname, cfgNetkey)
	case "I2PInterface":
		return fmt.Sprintf("[[%v]]\n  type = I2PInterface\n  enabled = yes\n  peers = %v%v%v%v",
			name, reachableOn, cfgIdentity, cfgNetname, cfgNetkey)
	case "RNodeInterface":
		return fmt.Sprintf("[[%v]]\n  type = RNodeInterface\n  enabled = yes\n  port = \n  frequency = %v\n  bandwidth = %v\n  spreadingfactor = %v\n  codingrate = %v\n  txpower = %v%v",
			name, pythonDiscoveryValueString(info["frequency"]), pythonDiscoveryValueString(info["bandwidth"]), pythonDiscoveryValueString(info["sf"]), pythonDiscoveryValueString(info["cr"]), cfgNetname, cfgNetkey)
	case "WeaveInterface":
		return fmt.Sprintf("[[%v]]\n  type = WeaveInterface\n  enabled = yes\n  port = %v%v",
			name, cfgNetname, cfgNetkey)
	case "KISSInterface":
		return fmt.Sprintf("[[%v]]\n  type = KISSInterface\n  enabled = yes\n  port = \n  # Frequency: %v\n  # Bandwidth: %v\n  # Modulation: %v%v%v%v",
			name, pythonDiscoveryValueString(info["frequency"]), pythonDiscoveryValueString(info["bandwidth"]), pythonDiscoveryValueString(info["modulation"]), cfgIdentity, cfgNetname, cfgNetkey)
	default:
		return ""
	}
}

func pythonDiscoveryValueString(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case []byte:
		return pythonDiscoveryBytesString(t)
	default:
		rv := reflect.ValueOf(v)
		if !rv.IsValid() {
			return "None"
		}
		switch rv.Kind() {
		case reflect.Float32, reflect.Float64:
			return pythonDiscoveryFloatString(rv.Float())
		case reflect.Slice, reflect.Array:
			return pythonDiscoveryListString(rv)
		case reflect.Map:
			return pythonDiscoveryMapString(rv)
		default:
			return fmt.Sprintf("%v", v)
		}
	}
}

func pythonDiscoveryListString(rv reflect.Value) string {
	parts := make([]string, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		parts[i] = pythonDiscoveryReprString(rv.Index(i).Interface())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func pythonDiscoveryReprString(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return pythonDiscoveryQuotedString(t)
	case []byte:
		return pythonDiscoveryBytesString(t)
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return "None"
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%v", v)
	case reflect.Float32, reflect.Float64:
		return pythonDiscoveryFloatString(rv.Float())
	case reflect.Slice, reflect.Array:
		return pythonDiscoveryListString(rv)
	case reflect.Map:
		return pythonDiscoveryMapString(rv)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func pythonDiscoveryMapString(rv reflect.Value) string {
	if rv.Len() == 0 {
		return "{}"
	}
	type mapEntry struct {
		keyRepr   string
		valueRepr string
	}
	entries := make([]mapEntry, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		entries = append(entries, mapEntry{
			keyRepr:   pythonDiscoveryReprString(iter.Key().Interface()),
			valueRepr: pythonDiscoveryReprString(iter.Value().Interface()),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].keyRepr < entries[j].keyRepr
	})
	parts := make([]string, len(entries))
	for i, entry := range entries {
		parts[i] = entry.keyRepr + ": " + entry.valueRepr
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func pythonDiscoveryQuotedString(v string) string {
	quoted := strings.ReplaceAll(v, "\\", "\\\\")
	quoted = strings.ReplaceAll(quoted, "'", "\\'")
	return "'" + quoted + "'"
}

func pythonDiscoveryBytesString(v []byte) string {
	if len(v) == 0 {
		return "b''"
	}
	var b strings.Builder
	b.WriteString("b'")
	for _, c := range v {
		switch {
		case c == '\\':
			b.WriteString("\\\\")
		case c == '\'':
			b.WriteString("\\'")
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			b.WriteString(fmt.Sprintf("\\x%02x", c))
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func pythonDiscoveryFloatString(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if strings.ContainsAny(s, ".eE") {
		return s
	}
	return s + ".0"
}

func validDiscoveryAnnounceReachableOn(v any) bool {
	switch t := v.(type) {
	case string:
		return isReachableOnValue(t)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func discoveryHexValue(v any) (string, bool) {
	return discoveryHexLikeString(v)
}

func discoveryHexLikeString(v any) (string, bool) {
	switch t := v.(type) {
	case []byte:
		return hex.EncodeToString(t), true
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return "", false
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%02x", rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%02x", rv.Uint()), true
	case reflect.Bool:
		return fmt.Sprintf("%02x", boolToInt(rv.Bool())), true
	case reflect.Slice, reflect.Array:
		var b strings.Builder
		for i := 0; i < rv.Len(); i++ {
			part, ok := discoveryHexScalarString(rv.Index(i).Interface())
			if !ok {
				return "", false
			}
			b.WriteString(part)
		}
		return b.String(), true
	default:
		return "", false
	}
}

func discoveryHexScalarString(v any) (string, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return "", false
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%02x", rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%02x", rv.Uint()), true
	case reflect.Bool:
		return fmt.Sprintf("%02x", boolToInt(rv.Bool())), true
	default:
		return "", false
	}
}

func discoveryAnnounceNameValue(v any, interfaceType string) (string, error) {
	if s, ok := v.(string); ok {
		if s == "" {
			return fmt.Sprintf("Discovered %v", interfaceType), nil
		}
		return s, nil
	}
	if b, ok := v.([]byte); ok {
		if len(b) == 0 {
			return fmt.Sprintf("Discovered %v", interfaceType), nil
		}
		return "", fmt.Errorf("invalid name type %T", v)
	}
	if !discoveryTruthyBool(v) {
		return fmt.Sprintf("Discovered %v", interfaceType), nil
	}
	return "", fmt.Errorf("invalid name type %T", v)
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
