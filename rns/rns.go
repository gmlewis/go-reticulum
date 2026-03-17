// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package rns implements the Reticulum Network Stack (RNS).
//
// Reticulum is the cryptography-based networking stack for building
// wide-area networks with readily available hardware. Reticulum can
// operate even with very low bandwidth and high latency.
//
// The core of RNS is based on several fundamental components:
//   - Identity: Handles public/private key pairs for encryption and signing.
//   - Destination: Represents addressable endpoints in the network.
//   - Packet: The basic unit of communication, handling routing and encryption.
//   - Link: Manages end-to-end encrypted sessions between peers.
//   - Transport: Handles routing, packet forwarding, and interface management.
//
// Reticulum uses modern cryptographic primitives (X25519, Ed25519, AES-CBC,
// and HKDF) to ensure all communication is secure and private.
package rns

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// HexToBytes converts a hex string to a byte slice.
func HexToBytes(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// Unpack deserializes MessagePack data.
func Unpack(data []byte) (any, error) {
	return msgpack.Unpack(data)
}

// RecallIdentity recalls an identity from its hash using the provided transport.
func RecallIdentity(ts Transport, hash []byte) *Identity {
	return Recall(ts, hash, false)
}

// PacketDestination is an interface for types that can be a packet destination.
type PacketDestination interface {
	GetHash() []byte
	GetType() int
	GetTransport() Transport
}

// GetHash returns the destination hash.
func (d *Destination) GetHash() []byte { return d.Hash }

// GetType returns the destination type.
func (d *Destination) GetType() int { return d.Type }

// GetTransport returns the destination transport.
func (d *Destination) GetTransport() Transport { return d.transport }

// GetHash returns the link ID.
func (l *Link) GetHash() []byte { return l.linkID }

// GetType returns the link destination type.
func (l *Link) GetType() int { return DestinationLink }

// GetTransport returns the link destination transport.
func (l *Link) GetTransport() Transport { return l.transport }

// Reticulum is the main entry point for the Reticulum Network Stack.
type Reticulum struct {
	config    *Config
	configDir string
	transport Transport

	networkIdentity     *Identity
	networkIdentityPath string
	linkMTUDiscovery    bool
	useImplicitProof    bool
	allowProbes         bool
	remoteMgmtEnabled   bool
	remoteMgmtAllowed   [][]byte
	forceSharedBitrate  int
	panicOnIfaceError   bool
	discoverInterfaces  bool
	requiredDiscoveryV  int
	publishBlackhole    bool
	blackholeSources    [][]byte
	interfaceSources    [][]byte
	autoconnectDiscover int

	mu                          sync.Mutex
	shareInstance               bool
	sharedInstanceType          string
	localInterfacePort          int
	localControlPort            int
	localSocketPath             string
	rpcKey                      []byte
	rpcListener                 net.Listener
	isSharedInstance            bool
	isStandaloneInstance        bool
	isConnectedToSharedInstance bool
	sharedInstanceInterface     interfaces.Interface
}

// IsSharedInstance reports whether this Reticulum instance is running as
// the local shared instance (server).
func (r *Reticulum) IsSharedInstance() bool { return r.isSharedInstance }

// IsStandaloneInstance reports whether this Reticulum instance is running
// in standalone mode (not sharing or connecting to a shared instance).
func (r *Reticulum) IsStandaloneInstance() bool { return r.isStandaloneInstance }

// IsConnectedToSharedInstance reports whether this Reticulum instance is
// connected to an existing shared instance (client).
func (r *Reticulum) IsConnectedToSharedInstance() bool { return r.isConnectedToSharedInstance }

// Transport returns the transport system for this Reticulum instance.
// Transport returns the transport system instance associated with this Reticulum stack.
func (r *Reticulum) Transport() Transport {
	return r.transport
}

// Close tears down the Reticulum instance, detaching the shared-instance
// interface and closing the RPC listener if active.
func (r *Reticulum) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var closeErr error
	if r.sharedInstanceInterface != nil {
		if err := r.sharedInstanceInterface.Detach(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
		r.sharedInstanceInterface = nil
	}
	if r.rpcListener != nil {
		if err := r.rpcListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = errors.Join(closeErr, err)
		}
		r.rpcListener = nil
	}
	return closeErr
}

const systemConfigDir = "/etc/reticulum"

// NewReticulum initializes a new Reticulum stack with a specific transport system.
func NewReticulum(ts Transport, configDir string) (*Reticulum, error) {
	resolvedConfigDir, err := resolveConfigDir(configDir)
	if err != nil {
		return nil, err
	}
	configDir = resolvedConfigDir

	r := &Reticulum{
		configDir:            configDir,
		transport:            ts,
		shareInstance:        true,
		sharedInstanceType:   "",
		linkMTUDiscovery:     true,
		useImplicitProof:     true,
		allowProbes:          false,
		remoteMgmtEnabled:    false,
		remoteMgmtAllowed:    nil,
		forceSharedBitrate:   0,
		panicOnIfaceError:    false,
		discoverInterfaces:   false,
		requiredDiscoveryV:   0,
		publishBlackhole:     false,
		blackholeSources:     nil,
		interfaceSources:     nil,
		autoconnectDiscover:  0,
		localInterfacePort:   37428,
		localControlPort:     37429,
		localSocketPath:      "",
		isSharedInstance:     false,
		isStandaloneInstance: false,
	}

	if err := ensureStartupLayout(configDir); err != nil {
		return nil, err
	}

	configPath := filepath.Join(configDir, "config")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		Log("Could not load config file, creating default configuration file...", LogNotice, false)
		// Create default config
		if err := r.createDefaultConfig(configPath); err != nil {
			return nil, err
		}
		Logf("Default config file created. Make any necessary changes in %v and restart Reticulum if needed.", LogNotice, false, configPath)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	r.config = cfg

	// Apply configuration
	if err := r.applyConfig(); err != nil {
		return nil, err
	}

	if err := r.initNetworkIdentity(); err != nil {
		return nil, err
	}
	storagePath := filepath.Join(configDir, "storage")
	if err := r.transport.Start(storagePath); err != nil {
		return nil, err
	}

	r.startLocalInterface()
	if err := r.startRPCListener(); err != nil {
		_ = r.Close()
		return nil, err
	}

	LoadKnownDestinations(storagePath)

	if r.isSharedInstance || r.isStandaloneInstance {
		// Initialize interfaces from config
		if err := r.initInterfaces(); err != nil {
			_ = r.Close()
			return nil, err
		}
		if r.discoverInterfaces && r.transport != nil {
			r.transport.DiscoverInterfaces()
		}
	}

	return r, nil
}

func (r *Reticulum) createDefaultConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultRNSConfig), 0600)
}

func (r *Reticulum) applyConfig() error {
	if logSection, ok := r.config.GetSection("logging"); ok {
		if lvlStr, ok := logSection.GetProperty("loglevel"); ok {
			var lvl int
			if _, err := fmt.Sscanf(lvlStr, "%v", &lvl); err != nil {
				Logf("Invalid loglevel value %q in config: %v", LogWarning, false, lvlStr, err)
			} else {
				SetLogLevel(lvl)
			}
		}
	}

	if reticulumSection, ok := r.config.GetSection("reticulum"); ok {
		if v, ok := reticulumSection.GetProperty("share_instance"); ok {
			r.shareInstance = parseBoolLike(v)
		}
		if v, ok := reticulumSection.GetProperty("instance_name"); ok {
			r.localSocketPath = strings.TrimSpace(v)
		}
		if v, ok := reticulumSection.GetProperty("shared_instance_type"); ok {
			r.sharedInstanceType = strings.ToLower(strings.TrimSpace(v))
		}
		if v, ok := reticulumSection.GetProperty("shared_instance_port"); ok {
			if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 {
				r.localInterfacePort = p
			}
		}
		if v, ok := reticulumSection.GetProperty("instance_control_port"); ok {
			if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 {
				r.localControlPort = p
			}
		}
		if v, ok := reticulumSection.GetProperty("rpc_key"); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				if key, err := hex.DecodeString(v); err == nil {
					r.rpcKey = key
				} else {
					Log("Invalid shared instance RPC key specified, falling back to default key", LogError, false)
				}
			}
		}
		if v, ok := reticulumSection.GetProperty("network_identity"); ok {
			r.networkIdentityPath = strings.TrimSpace(v)
		}
		if v, ok := reticulumSection.GetProperty("link_mtu_discovery"); ok {
			r.linkMTUDiscovery = parseBoolLike(v)
			if r.transport != nil {
				r.transport.SetLinkMTUDiscovery(r.linkMTUDiscovery)
			}
		}
		if v, ok := reticulumSection.GetProperty("use_implicit_proof"); ok {
			r.useImplicitProof = parseBoolLike(v)
		}
		if v, ok := reticulumSection.GetProperty("enable_remote_management"); ok {
			r.remoteMgmtEnabled = parseBoolLike(v)
		}
		if v, ok := reticulumSection.GetProperty("enable_transport"); ok {
			enabled := parseBoolLike(v)
			if r.transport != nil {
				r.transport.SetEnabled(enabled)
			}
		}
		if v, ok := reticulumSection.GetProperty("respond_to_probes"); ok {
			r.allowProbes = parseBoolLike(v)
		}
		if v, ok := reticulumSection.GetProperty("remote_management_allowed"); ok {
			for _, hexHash := range parseListProperty(v) {
				hexHash = strings.TrimSpace(hexHash)
				requiredHexLen := (TruncatedHashLength / 8) * 2
				if len(hexHash) != requiredHexLen {
					return fmt.Errorf("identity hash length for remote management ACL %v is invalid, must be %v hexadecimal characters (%v bytes)", hexHash, requiredHexLen, requiredHexLen/2)
				}
				allowedHash, err := hex.DecodeString(hexHash)
				if err != nil {
					return fmt.Errorf("invalid identity hash for remote management ACL: %v", hexHash)
				}

				exists := false
				for _, existing := range r.remoteMgmtAllowed {
					if strings.EqualFold(hex.EncodeToString(existing), hexHash) {
						exists = true
						break
					}
				}
				if !exists {
					r.remoteMgmtAllowed = append(r.remoteMgmtAllowed, allowedHash)
				}
			}
		}
		if v, ok := reticulumSection.GetProperty("force_shared_instance_bitrate"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				r.forceSharedBitrate = n
			}
		}
		if v, ok := reticulumSection.GetProperty("panic_on_interface_error"); ok {
			r.panicOnIfaceError = parseBoolLike(v)
		}
		if v, ok := reticulumSection.GetProperty("discover_interfaces"); ok {
			r.discoverInterfaces = parseBoolLike(v)
		}
		if v, ok := reticulumSection.GetProperty("required_discovery_value"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				if n > 0 {
					r.requiredDiscoveryV = n
				} else {
					r.requiredDiscoveryV = 0
				}
			}
		}
		if v, ok := reticulumSection.GetProperty("publish_blackhole"); ok {
			r.publishBlackhole = parseBoolLike(v)
		}
		if v, ok := reticulumSection.GetProperty("blackhole_sources"); ok {
			for _, hexHash := range parseListProperty(v) {
				hexHash = strings.TrimSpace(hexHash)
				requiredHexLen := (TruncatedHashLength / 8) * 2
				if len(hexHash) != requiredHexLen {
					return fmt.Errorf("identity hash length for blackhole source %v is invalid, must be %v hexadecimal characters (%v bytes)", hexHash, requiredHexLen, requiredHexLen/2)
				}
				sourceHash, err := hex.DecodeString(hexHash)
				if err != nil {
					return fmt.Errorf("invalid identity hash for remote blackhole source: %v", hexHash)
				}

				exists := false
				for _, existing := range r.blackholeSources {
					if strings.EqualFold(hex.EncodeToString(existing), hexHash) {
						exists = true
						break
					}
				}
				if !exists {
					r.blackholeSources = append(r.blackholeSources, sourceHash)
				}
			}
		}
		if v, ok := reticulumSection.GetProperty("interface_discovery_sources"); ok {
			for _, hexHash := range parseListProperty(v) {
				hexHash = strings.TrimSpace(hexHash)
				requiredHexLen := (TruncatedHashLength / 8) * 2
				if len(hexHash) != requiredHexLen {
					return fmt.Errorf("identity hash length for interface discovery source %v is invalid, must be %v hexadecimal characters (%v bytes)", hexHash, requiredHexLen, requiredHexLen/2)
				}
				sourceHash, err := hex.DecodeString(hexHash)
				if err != nil {
					return fmt.Errorf("invalid identity hash for interface discovery source: %v", hexHash)
				}

				exists := false
				for _, existing := range r.interfaceSources {
					if strings.EqualFold(hex.EncodeToString(existing), hexHash) {
						exists = true
						break
					}
				}
				if !exists {
					r.interfaceSources = append(r.interfaceSources, sourceHash)
				}
			}
		}
		if v, ok := reticulumSection.GetProperty("autoconnect_discovered_interfaces"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				r.autoconnectDiscover = n
			}
		}
	}

	r.transport.SetLinkMTUDiscovery(r.linkMTUDiscovery)
	// TODO: Investigate:
	// r.transport.SetUseImplicitProof(r.useImplicitProof)
	// r.transport.SetPanicOnInterfaceErrorEnabled(r.panicOnIfaceError)

	return nil
}

func expandUserPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	if trimmed == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~/")), nil
	}
	return trimmed, nil
}

func (r *Reticulum) initNetworkIdentity() error {
	if r.networkIdentityPath == "" {
		return nil
	}

	identityPath, err := expandUserPath(r.networkIdentityPath)
	if err != nil {
		return fmt.Errorf("could not expand network identity path %q: %w", r.networkIdentityPath, err)
	}

	id, err := FromFile(identityPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not load network identity from %q: %w", identityPath, err)
		}

		id, err = NewIdentity(true)
		if err != nil {
			return fmt.Errorf("could not create network identity for %q: %w", identityPath, err)
		}
		if err := os.MkdirAll(filepath.Dir(identityPath), 0700); err != nil {
			return fmt.Errorf("could not create network identity directory for %q: %w", identityPath, err)
		}
		if err := id.ToFile(identityPath); err != nil {
			return fmt.Errorf("could not persist network identity to %q: %w", identityPath, err)
		}
		Logf("Network identity generated and persisted to %v", LogVerbose, false, identityPath)
	} else {
		Logf("Network identity loaded from %v", LogVerbose, false, identityPath)
	}

	r.networkIdentity = id
	if r.transport != nil {
		r.transport.SetNetworkIdentity(id)
	}
	return nil
}

func (r *Reticulum) useAFUnix() bool {
	if r.sharedInstanceType == "unix" {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	return r.sharedInstanceType != "tcp"
}

func (r *Reticulum) startLocalInterface() {
	if !r.shareInstance {
		r.isSharedInstance = false
		r.isStandaloneInstance = true
		r.isConnectedToSharedInstance = false
		return
	}

	handler := func(data []byte, iface interfaces.Interface) {
		r.transport.Inbound(data, iface)
	}

	useUnix := r.useAFUnix()
	localPath := ""
	if useUnix {
		instance := r.localSocketPath
		if instance == "" {
			instance = "default"
		}
		if runtime.GOOS == "linux" {
			localPath = "@rns/" + instance
		} else {
			localPath = filepath.Join(r.configDir, ".rns-"+instance+".sock")
		}
	}

	server, err := interfaces.NewLocalServerInterface("Local shared instance", localPath, r.localInterfacePort, handler)
	if err == nil {
		r.applyForcedSharedBitrate(server)
		r.transport.RegisterInterface(server)
		r.mu.Lock()
		r.sharedInstanceInterface = server
		r.mu.Unlock()
		r.isSharedInstance = true
		r.isStandaloneInstance = false
		r.isConnectedToSharedInstance = false
		return
	}

	client, err := interfaces.NewLocalClientInterface("Local shared instance", localPath, r.localInterfacePort, handler)
	if err == nil && client.Status() {
		r.applyForcedSharedBitrate(client)
		r.transport.RegisterInterface(client)
		r.mu.Lock()
		r.sharedInstanceInterface = client
		r.mu.Unlock()
		r.isSharedInstance = false
		r.isStandaloneInstance = false
		r.isConnectedToSharedInstance = true
		return
	}
	if err == nil {
		if detachErr := client.Detach(); detachErr != nil {
			Logf("Failed to detach inactive shared-instance client: %v", LogError, false, detachErr)
		}
		r.isSharedInstance = false
		r.isStandaloneInstance = true
		r.isConnectedToSharedInstance = false
		return
	}

	Logf("Local shared instance appears to be running, but it could not be connected: %v", LogError, false, err)
	r.isSharedInstance = false
	r.isStandaloneInstance = true
	r.isConnectedToSharedInstance = false
}

func (r *Reticulum) applyForcedSharedBitrate(iface interfaces.Interface) {
	if iface == nil || r.forceSharedBitrate <= 0 {
		return
	}
	setter, ok := iface.(interface {
		SetBitrate(int)
	})
	if !ok {
		return
	}
	setter.SetBitrate(r.forceSharedBitrate)
}

func (r *Reticulum) initInterfaces() error {
	// Try [interfaces] section first
	interfacesSection, ok := r.config.GetSection("interfaces")
	if !ok {
		// Fallback to [reticulum] subsections
		interfacesSection, ok = r.config.GetSection("reticulum")
	}

	if !ok {
		return nil
	}

	for _, sub := range interfacesSection.Subsections {
		if sub.Name == "" {
			continue
		}

		ifaceType, ok := sub.GetProperty("type")
		if !ok {
			continue
		}

		enabled, ok := sub.GetProperty("enabled")
		if ok && (strings.ToLower(enabled) == "no" || strings.ToLower(enabled) == "false") {
			continue
		}

		ifacConfig := parseIFACConfig(sub)

		switch ifaceType {
		case "AutoInterface":
			autoCfg := interfaces.AutoInterfaceConfig{}
			if v, ok := sub.GetProperty("group_id"); ok {
				autoCfg.GroupID = v
			}
			if v, ok := sub.GetProperty("discovery_scope"); ok {
				autoCfg.DiscoveryScope = v
			}
			if v, ok := sub.GetProperty("multicast_address_type"); ok {
				autoCfg.MulticastAddressType = v
			}
			if v, ok := sub.GetProperty("devices"); ok {
				autoCfg.Devices = parseListProperty(v)
			}
			if v, ok := sub.GetProperty("ignored_devices"); ok {
				autoCfg.IgnoredDevices = parseListProperty(v)
			}
			if v, ok := sub.GetProperty("discovery_port"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					autoCfg.DiscoveryPort = n
				}
			}
			if v, ok := sub.GetProperty("data_port"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					autoCfg.DataPort = n
				}
			}
			if v, ok := sub.GetProperty("configured_bitrate"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					autoCfg.ConfiguredBitrate = n
				}
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewAutoInterface(sub.Name, autoCfg, handler, func(peer interfaces.Interface) {
				r.transport.RegisterInterface(peer)
			})
			if err != nil {
				Logf("Failed to initialize Auto interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started Auto interface %v", LogInfo, false, sub.Name)

		case "UDPInterface":
			// Parse UDP interface properties
			listenIP, _ := sub.GetProperty("listen_ip")
			if listenIP == "" {
				listenIP = "0.0.0.0"
			}
			var listenPort int
			if _, err := fmt.Sscanf(sub.Properties["listen_port"], "%v", &listenPort); err != nil {
				Logf("Invalid listen_port for UDP interface %v: %v", LogWarning, false, sub.Name, err)
				continue
			}

			forwardIP, _ := sub.GetProperty("forward_ip")
			var forwardPort int
			if _, err := fmt.Sscanf(sub.Properties["forward_port"], "%v", &forwardPort); err != nil {
				Logf("Invalid forward_port for UDP interface %v: %v", LogWarning, false, sub.Name, err)
				continue
			}

			// Simple handler that passes to Transport.Inbound
			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewUDPInterface(sub.Name, listenIP, listenPort, forwardIP, forwardPort, handler)
			if err != nil {
				Logf("Failed to initialize UDP interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started UDP interface %v", LogInfo, false, sub.Name)

		case "TCPClientInterface":
			targetHost, _ := sub.GetProperty("target_host")
			var targetPort int
			if _, err := fmt.Sscanf(sub.Properties["target_port"], "%v", &targetPort); err != nil {
				Logf("Invalid target_port for TCP client interface %v: %v", LogWarning, false, sub.Name, err)
				continue
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewTCPClientInterface(sub.Name, targetHost, targetPort, false, handler)
			if err != nil {
				Logf("Failed to initialize TCP client interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started TCP client interface %v to %v:%v", LogInfo, false, sub.Name, targetHost, targetPort)

		case "TCPServerInterface":
			listenIP, _ := sub.GetProperty("listen_ip")
			if listenIP == "" {
				listenIP = "0.0.0.0"
			}
			var listenPort int
			if _, err := fmt.Sscanf(sub.Properties["listen_port"], "%v", &listenPort); err != nil {
				Logf("Invalid listen_port for TCP server interface %v: %v", LogWarning, false, sub.Name, err)
				continue
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewTCPServerInterface(sub.Name, listenIP, listenPort, handler)
			if err != nil {
				Logf("Failed to initialize TCP server interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started TCP server interface %v on %v:%v", LogInfo, false, sub.Name, listenIP, listenPort)

		case "I2PInterface":
			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			connectable := false
			if v, ok := sub.GetProperty("connectable"); ok {
				connectable = parseBoolLike(v)
			}

			registeredAny := false
			if connectable {
				listenIP, _ := sub.GetProperty("bind_ip")
				if strings.TrimSpace(listenIP) == "" {
					listenIP, _ = sub.GetProperty("listen_ip")
				}
				if strings.TrimSpace(listenIP) == "" {
					listenIP = "127.0.0.1"
				}

				listenPort := 0
				if v, ok := sub.GetProperty("bind_port"); ok {
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
						listenPort = n
					}
				}
				if v, ok := sub.GetProperty("listen_port"); ok {
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
						listenPort = n
					}
				}

				if listenPort <= 0 {
					Logf("Failed to initialize I2P interface %v: connectable requires bind_port/listen_port", LogError, false, sub.Name)
				} else {
					iface, err := interfaces.NewI2PInterface(sub.Name, listenIP, listenPort, handler)
					if err != nil {
						Logf("Failed to initialize I2P interface %v: %v", LogError, false, sub.Name, err)
					} else {
						applyIFACConfig(iface, ifacConfig)
						r.transport.RegisterInterface(iface)
						registeredAny = true
						Logf("Started I2P interface %v on %v:%v", LogInfo, false, sub.Name, listenIP, listenPort)
					}
				}
			}

			peerTargets := parseListProperty(sub.Properties["peers"])
			targetHost, hasTargetHost := sub.GetProperty("target_host")
			targetPort := 0
			if v, ok := sub.GetProperty("target_port"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					targetPort = n
				}
			}
			if hasTargetHost && strings.TrimSpace(targetHost) != "" && targetPort > 0 {
				peerTargets = append(peerTargets, fmt.Sprintf("%v:%v", strings.TrimSpace(targetHost), targetPort))
			}

			for _, peer := range peerTargets {
				peer = strings.TrimSpace(peer)
				if peer == "" {
					continue
				}

				host, portText, ok := strings.Cut(peer, ":")
				if !ok || strings.TrimSpace(host) == "" {
					Logf("Failed to initialize I2P peer for %v: invalid peer target %q (expected host:port)", LogError, false, sub.Name, peer)
					continue
				}

				port, err := strconv.Atoi(strings.TrimSpace(portText))
				if err != nil || port <= 0 {
					Logf("Failed to initialize I2P peer for %v: invalid peer port in %q", LogError, false, sub.Name, peer)
					continue
				}

				peerName := fmt.Sprintf("%v to %v", sub.Name, peer)
				iface, err := interfaces.NewI2PInterfacePeer(peerName, strings.TrimSpace(host), port, handler)
				if err != nil {
					Logf("Failed to initialize I2P peer interface %v: %v", LogError, false, peerName, err)
					continue
				}

				applyIFACConfig(iface, ifacConfig)
				r.transport.RegisterInterface(iface)
				registeredAny = true
				Logf("Started I2P peer interface %v", LogInfo, false, peerName)
			}

			if !registeredAny {
				Logf("Failed to initialize I2P interface %v: no connectable endpoint or valid peers configured", LogError, false, sub.Name)
			}

		case "BackboneInterface":
			listenIP, _ := sub.GetProperty("listen_ip")
			if strings.TrimSpace(listenIP) == "" {
				listenIP = "0.0.0.0"
			}

			listenPort := 0
			if v, ok := sub.GetProperty("port"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					listenPort = n
				}
			}
			if v, ok := sub.GetProperty("listen_port"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					listenPort = n
				}
			}
			if listenPort <= 0 {
				Logf("Failed to initialize Backbone interface %v: missing listen_port/port", LogError, false, sub.Name)
				continue
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewBackboneInterface(sub.Name, listenIP, listenPort, handler)
			if err != nil {
				Logf("Failed to initialize Backbone interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started Backbone interface %v on %v:%v", LogInfo, false, sub.Name, listenIP, listenPort)

		case "BackboneClientInterface":
			targetHost, _ := sub.GetProperty("target_host")
			if strings.TrimSpace(targetHost) == "" {
				Logf("Failed to initialize Backbone client interface %v: missing target_host", LogError, false, sub.Name)
				continue
			}

			targetPort := 0
			if v, ok := sub.GetProperty("target_port"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					targetPort = n
				}
			}
			if targetPort <= 0 {
				Logf("Failed to initialize Backbone client interface %v: missing target_port", LogError, false, sub.Name)
				continue
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewBackboneClientInterface(sub.Name, targetHost, targetPort, handler)
			if err != nil {
				Logf("Failed to initialize Backbone client interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started Backbone client interface %v to %v:%v", LogInfo, false, sub.Name, targetHost, targetPort)

		case "KISSInterface":
			port, _ := sub.GetProperty("port")
			if strings.TrimSpace(port) == "" {
				Logf("Failed to initialize KISS interface %v: missing port", LogError, false, sub.Name)
				continue
			}

			speed := interfaces.KISSDefaultSpeed
			if v, ok := sub.GetProperty("speed"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					speed = n
				}
			}

			databits := interfaces.KISSDefaultDataBits
			if v, ok := sub.GetProperty("databits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					databits = n
				}
			}

			stopbits := interfaces.KISSDefaultStopBits
			if v, ok := sub.GetProperty("stopbits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					stopbits = n
				}
			}

			parity := interfaces.KISSDefaultParity
			if v, ok := sub.GetProperty("parity"); ok {
				if strings.TrimSpace(v) != "" {
					parity = v
				}
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewKISSInterface(sub.Name, port, speed, databits, stopbits, parity, handler)
			if err != nil {
				Logf("Failed to initialize KISS interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started KISS interface %v on %v at %v bps", LogInfo, false, sub.Name, port, speed)

		case "RNodeInterface":
			port, _ := sub.GetProperty("port")
			if strings.TrimSpace(port) == "" {
				Logf("Failed to initialize RNode interface %v: missing port", LogError, false, sub.Name)
				continue
			}

			parseRequiredInt := func(prop string) (int, bool) {
				v, ok := sub.GetProperty(prop)
				if !ok || strings.TrimSpace(v) == "" {
					return 0, false
				}
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					return 0, false
				}
				return n, true
			}

			frequency, ok := parseRequiredInt("frequency")
			if !ok {
				Logf("Failed to initialize RNode interface %v: missing/invalid frequency", LogError, false, sub.Name)
				continue
			}
			bandwidth, ok := parseRequiredInt("bandwidth")
			if !ok {
				Logf("Failed to initialize RNode interface %v: missing/invalid bandwidth", LogError, false, sub.Name)
				continue
			}
			txpower, ok := parseRequiredInt("txpower")
			if !ok {
				Logf("Failed to initialize RNode interface %v: missing/invalid txpower", LogError, false, sub.Name)
				continue
			}
			spreadingFactor, ok := parseRequiredInt("spreadingfactor")
			if !ok {
				Logf("Failed to initialize RNode interface %v: missing/invalid spreadingfactor", LogError, false, sub.Name)
				continue
			}
			codingRate, ok := parseRequiredInt("codingrate")
			if !ok {
				Logf("Failed to initialize RNode interface %v: missing/invalid codingrate", LogError, false, sub.Name)
				continue
			}

			speed := interfaces.RNodeDefaultSpeed
			if v, ok := sub.GetProperty("speed"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					speed = n
				}
			}

			databits := interfaces.RNodeDefaultDataBits
			if v, ok := sub.GetProperty("databits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					databits = n
				}
			}

			stopbits := interfaces.RNodeDefaultStopBits
			if v, ok := sub.GetProperty("stopbits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					stopbits = n
				}
			}

			parity := interfaces.RNodeDefaultParity
			if v, ok := sub.GetProperty("parity"); ok {
				if strings.TrimSpace(v) != "" {
					parity = v
				}
			}

			flowControl := false
			if v, ok := sub.GetProperty("flow_control"); ok {
				flowControl = parseBoolLike(v)
			}

			idInterval := 0
			if v, ok := sub.GetProperty("id_interval"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					idInterval = n
				}
			}
			idCallsign, _ := sub.GetProperty("id_callsign")

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewRNodeInterface(sub.Name, port, speed, databits, stopbits, parity, frequency, bandwidth, txpower, spreadingFactor, codingRate, flowControl, idInterval, idCallsign, handler)
			if err != nil {
				Logf("Failed to initialize RNode interface %v: %v", LogError, false, sub.Name, err)
				continue
			}

			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started RNode interface %v on %v", LogInfo, false, sub.Name, port)

		case "RNodeMultiInterface":
			port, _ := sub.GetProperty("port")
			if strings.TrimSpace(port) == "" {
				Logf("Failed to initialize RNodeMulti interface %v: missing port", LogError, false, sub.Name)
				continue
			}

			speed := interfaces.RNodeDefaultSpeed
			if v, ok := sub.GetProperty("speed"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					speed = n
				}
			}

			databits := interfaces.RNodeDefaultDataBits
			if v, ok := sub.GetProperty("databits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					databits = n
				}
			}

			stopbits := interfaces.RNodeDefaultStopBits
			if v, ok := sub.GetProperty("stopbits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					stopbits = n
				}
			}

			parity := interfaces.RNodeDefaultParity
			if v, ok := sub.GetProperty("parity"); ok {
				if strings.TrimSpace(v) != "" {
					parity = v
				}
			}

			idInterval := 0
			if v, ok := sub.GetProperty("id_interval"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					idInterval = n
				}
			}
			idCallsign, _ := sub.GetProperty("id_callsign")

			subCfgs := make([]interfaces.RNodeMultiSubinterfaceConfig, 0, len(sub.Subsections))
			for subName, nested := range sub.Subsections {
				if nested == nil {
					continue
				}

				enabled := true
				if v, ok := nested.GetProperty("interface_enabled"); ok {
					enabled = parseBoolLike(v)
				} else if v, ok := nested.GetProperty("enabled"); ok {
					enabled = parseBoolLike(v)
				}

				parseRequiredInt := func(prop string) (int, bool) {
					v, ok := nested.GetProperty(prop)
					if !ok || strings.TrimSpace(v) == "" {
						return 0, false
					}
					n, err := strconv.Atoi(strings.TrimSpace(v))
					if err != nil {
						return 0, false
					}
					return n, true
				}

				frequency, ok := parseRequiredInt("frequency")
				if !ok {
					continue
				}
				bandwidth, ok := parseRequiredInt("bandwidth")
				if !ok {
					continue
				}
				txpower, ok := parseRequiredInt("txpower")
				if !ok {
					continue
				}
				spreadingFactor, ok := parseRequiredInt("spreadingfactor")
				if !ok {
					continue
				}
				codingRate, ok := parseRequiredInt("codingrate")
				if !ok {
					continue
				}

				flowControl := false
				if v, ok := nested.GetProperty("flow_control"); ok {
					flowControl = parseBoolLike(v)
				}

				subCfgs = append(subCfgs, interfaces.RNodeMultiSubinterfaceConfig{
					Name:            subName,
					Enabled:         enabled,
					Frequency:       frequency,
					Bandwidth:       bandwidth,
					TXPower:         txpower,
					SpreadingFactor: spreadingFactor,
					CodingRate:      codingRate,
					FlowControl:     flowControl,
				})
			}

			if len(subCfgs) == 0 {
				Logf("Failed to initialize RNodeMulti interface %v: no valid subinterfaces configured", LogError, false, sub.Name)
				continue
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewRNodeMultiInterface(sub.Name, port, speed, databits, stopbits, parity, idInterval, idCallsign, subCfgs, handler)
			if err != nil {
				Logf("Failed to initialize RNodeMulti interface %v: %v", LogError, false, sub.Name, err)
				continue
			}

			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started RNodeMulti interface %v on %v", LogInfo, false, sub.Name, port)

		case "AX25KISSInterface":
			port, _ := sub.GetProperty("port")
			if strings.TrimSpace(port) == "" {
				Logf("Failed to initialize AX.25 KISS interface %v: missing port", LogError, false, sub.Name)
				continue
			}

			speed := interfaces.AX25KISSDefaultSpeed
			if v, ok := sub.GetProperty("speed"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					speed = n
				}
			}

			databits := interfaces.AX25KISSDefaultDataBits
			if v, ok := sub.GetProperty("databits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					databits = n
				}
			}

			stopbits := interfaces.AX25KISSDefaultStopBits
			if v, ok := sub.GetProperty("stopbits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					stopbits = n
				}
			}

			parity := interfaces.AX25KISSDefaultParity
			if v, ok := sub.GetProperty("parity"); ok {
				if strings.TrimSpace(v) != "" {
					parity = v
				}
			}

			callsign, _ := sub.GetProperty("callsign")
			if strings.TrimSpace(callsign) == "" {
				Logf("Failed to initialize AX.25 KISS interface %v: missing callsign", LogError, false, sub.Name)
				continue
			}

			ssid := -1
			if v, ok := sub.GetProperty("ssid"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					ssid = n
				}
			}

			preamble := interfaces.AX25KISSDefaultPreambleMS
			if v, ok := sub.GetProperty("preamble"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					preamble = n
				}
			}

			txTail := interfaces.AX25KISSDefaultTxTailMS
			if v, ok := sub.GetProperty("txtail"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					txTail = n
				}
			}

			persistence := interfaces.AX25KISSDefaultPersistence
			if v, ok := sub.GetProperty("persistence"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					persistence = n
				}
			}

			slotTime := interfaces.AX25KISSDefaultSlotTimeMS
			if v, ok := sub.GetProperty("slottime"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					slotTime = n
				}
			}

			flowControl := false
			if v, ok := sub.GetProperty("flow_control"); ok {
				flowControl = parseBoolLike(v)
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewAX25KISSInterface(sub.Name, port, speed, databits, stopbits, parity, callsign, ssid, preamble, txTail, persistence, slotTime, flowControl, handler)
			if err != nil {
				Logf("Failed to initialize AX.25 KISS interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started AX.25 KISS interface %v on %v at %v bps", LogInfo, false, sub.Name, port, speed)

		case "PipeInterface":
			command, _ := sub.GetProperty("command")
			if strings.TrimSpace(command) == "" {
				Logf("Failed to initialize Pipe interface %v: missing command", LogError, false, sub.Name)
				continue
			}

			respawnDelay := interfaces.PipeDefaultRespawnDelay
			if v, ok := sub.GetProperty("respawn_delay"); ok {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > 0 {
					respawnDelay = time.Duration(f * float64(time.Second))
				}
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewPipeSubprocessInterface(sub.Name, command, respawnDelay, handler)
			if err != nil {
				Logf("Failed to initialize Pipe interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started Pipe interface %v", LogInfo, false, sub.Name)

		case "WeaveInterface":
			port, _ := sub.GetProperty("port")
			if strings.TrimSpace(port) == "" {
				Logf("Failed to initialize Weave interface %v: missing port", LogError, false, sub.Name)
				continue
			}

			configuredBitrate := 0
			if v, ok := sub.GetProperty("configured_bitrate"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					configuredBitrate = n
				}
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewWeaveInterface(sub.Name, port, configuredBitrate, handler)
			if err != nil {
				Logf("Failed to initialize Weave interface %v: %v", LogError, false, sub.Name, err)
				continue
			}

			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started Weave interface %v on %v", LogInfo, false, sub.Name, port)

		case "SerialInterface":
			port, _ := sub.GetProperty("port")
			if strings.TrimSpace(port) == "" {
				Logf("Failed to initialize Serial interface %v: missing port", LogError, false, sub.Name)
				continue
			}

			speed := interfaces.SerialDefaultSpeed
			if v, ok := sub.GetProperty("speed"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					speed = n
				}
			}

			databits := interfaces.SerialDefaultDataBits
			if v, ok := sub.GetProperty("databits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					databits = n
				}
			}

			stopbits := interfaces.SerialDefaultStopBits
			if v, ok := sub.GetProperty("stopbits"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					stopbits = n
				}
			}

			parity := interfaces.SerialDefaultParity
			if v, ok := sub.GetProperty("parity"); ok {
				if strings.TrimSpace(v) != "" {
					parity = v
				}
			}

			handler := func(data []byte, iface interfaces.Interface) {
				r.transport.Inbound(data, iface)
			}

			iface, err := interfaces.NewSerialInterface(sub.Name, port, speed, databits, stopbits, parity, handler)
			if err != nil {
				Logf("Failed to initialize Serial interface %v: %v", LogError, false, sub.Name, err)
				continue
			}
			applyIFACConfig(iface, ifacConfig)
			r.transport.RegisterInterface(iface)
			Logf("Started Serial interface %v on %v at %v bps", LogInfo, false, sub.Name, port, speed)
		}
	}
	return nil
}
