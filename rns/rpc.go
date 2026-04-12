// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func (r *Reticulum) startRPCListener() error {
	if !r.isSharedInstance {
		return nil
	}

	r.ensureRPCKey()
	if len(r.rpcKey) == 0 {
		return nil
	}

	listener, err := r.makeRPCListener()
	if err != nil {
		r.logger.Error("Could not start RPC listener: %v", err)
		return err
	}
	r.mu.Lock()
	r.rpcListener = listener
	r.mu.Unlock()

	go r.rpcLoop()
	return nil
}

func (r *Reticulum) ensureRPCKey() {
	if len(r.rpcKey) > 0 || r.transport == nil || r.transport.Identity() == nil {
		return
	}
	r.rpcKey = FullHash(r.transport.Identity().GetPrivateKey())
}

func (r *Reticulum) makeRPCListener() (net.Listener, error) {
	if r.useAFUnix() {
		instance := r.localSocketPath
		if instance == "" {
			instance = "default"
		}
		if runtime.GOOS == "linux" {
			return net.Listen("unix", "@rns/"+instance+"/rpc")
		}
		rpcPath := filepath.Join(r.configDir, ".rns-"+instance+"-rpc.sock")
		if err := os.Remove(rpcPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return net.Listen("unix", rpcPath)
	}

	addr := fmt.Sprintf("127.0.0.1:%v", r.localControlPort)
	return net.Listen("tcp", addr)
}

func (r *Reticulum) rpcLoop() {
	for {
		r.mu.Lock()
		listener := r.rpcListener
		r.mu.Unlock()
		if listener == nil {
			return
		}
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go r.handleRPCConn(conn)
	}
}

func (r *Reticulum) handleRPCConn(conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			r.logger.Debug("Failed closing RPC connection: %v", err)
		}
	}()

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return
	}
	authReq, err := readRPCFrame(conn)
	if err != nil {
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	authMap := asAnyMap(authReq)
	if authMap == nil {
		return
	}

	provided, ok := lookupAny(authMap, "auth")
	if !ok {
		return
	}

	if !r.rpcAuthValid(provided) {
		if err := writeRPCFrame(conn, map[string]any{"error": "unauthorized"}); err != nil {
			return
		}
		return
	}
	if err := writeRPCFrame(conn, map[string]any{"ok": true}); err != nil {
		return
	}

	for {
		req, err := readRPCFrame(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				r.logger.Error("An error occurred while handling RPC call from local client: %v", err)
			}
			return
		}

		resp := r.handleRPCRequest(req)
		if err := writeRPCFrame(conn, resp); err != nil {
			return
		}
	}
}

func (r *Reticulum) rpcAuthValid(auth any) bool {
	var provided []byte
	switch v := auth.(type) {
	case []byte:
		provided = v
	case string:
		decoded, err := hex.DecodeString(strings.TrimSpace(v))
		if err != nil {
			return false
		}
		provided = decoded
	default:
		return false
	}

	if len(provided) != len(r.rpcKey) {
		return false
	}
	return subtle.ConstantTimeCompare(provided, r.rpcKey) == 1
}

func readRPCFrame(conn net.Conn) (any, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size == 0 {
		return nil, errors.New("empty rpc frame")
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return msgpack.Unpack(buf)
}

func writeRPCFrame(conn net.Conn, v any) error {
	payload, err := msgpack.Pack(v)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err = conn.Write(payload)
	return err
}

func (r *Reticulum) handleRPCRequest(req any) any {
	m, ok := req.(map[any]any)
	if !ok {
		return map[string]any{"error": "invalid request"}
	}

	if getPath, ok := m["get"]; ok {
		path, _ := getPath.(string)
		switch path {
		case "interface_stats":
			return r.getInterfaceStats()
		case "path_table":
			maxHops, _ := asOptionalInt(m["max_hops"])
			return r.getPathTable(maxHops)
		case "next_hop":
			if dest, ok := decodeHashArg(m["destination_hash"]); ok {
				return r.getNextHop(dest)
			}
			return nil
		case "next_hop_if_name":
			if dest, ok := decodeHashArg(m["destination_hash"]); ok {
				return r.getNextHopInterfaceName(dest)
			}
			return ""
		case "link_count":
			return r.getLinkCount()
		case "rate_table":
			return r.getRateTable()
		case "blackholed_identities":
			return r.getBlackholedIdentities()
		case "first_hop_timeout":
			if dest, ok := decodeHashArg(m["destination_hash"]); ok {
				return r.getFirstHopTimeout(dest)
			}
			return 0
		case "packet_rssi":
			if packetHash, ok := decodeHashArg(m["packet_hash"]); ok {
				return r.getPacketRSSI(packetHash)
			}
			return nil
		case "packet_snr":
			if packetHash, ok := decodeHashArg(m["packet_hash"]); ok {
				return r.getPacketSNR(packetHash)
			}
			return nil
		case "packet_q":
			if packetHash, ok := decodeHashArg(m["packet_hash"]); ok {
				return r.getPacketQ(packetHash)
			}
			return nil
		default:
			return map[string]any{"error": "unsupported get path"}
		}
	}

	if dropPath, ok := m["drop"]; ok {
		path, _ := dropPath.(string)
		switch path {
		case "path":
			if dest, ok := decodeHashArg(m["destination_hash"]); ok {
				return r.transport.InvalidatePath(dest)
			}
			return false
		case "all_via":
			if via, ok := decodeHashArg(m["destination_hash"]); ok {
				return r.transport.InvalidatePathsViaNextHop(via)
			}
			return 0
		case "announce_queues":
			return r.dropAnnounceQueues()
		default:
			return map[string]any{"error": "unsupported drop path"}
		}
	}

	if blackholeIdentity, ok := m["blackhole_identity"]; ok {
		identityHash, ok := decodeHashArg(blackholeIdentity)
		if !ok {
			return false
		}
		var until *int64
		if rawUntil, exists := m["until"]; exists {
			if untilInt, ok := asOptionalInt64(rawUntil); ok {
				until = &untilInt
			}
		}
		reason := ""
		if rv, ok := m["reason"]; ok {
			reason = asString(rv)
		}
		return r.blackholeIdentity(identityHash, until, reason)
	}

	if unblackholeIdentity, ok := m["unblackhole_identity"]; ok {
		identityHash, ok := decodeHashArg(unblackholeIdentity)
		if !ok {
			return false
		}
		return r.unblackholeIdentity(identityHash)
	}

	return map[string]any{"error": "unsupported rpc request"}
}

func asOptionalInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case uint64:
		return int(n), true
	case uint32:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func asOptionalInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case uint32:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func decodeHashArg(v any) ([]byte, bool) {
	switch t := v.(type) {
	case []byte:
		if len(t) == 0 {
			return nil, false
		}
		out := make([]byte, len(t))
		copy(out, t)
		return out, true
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil, false
		}
		b, err := hex.DecodeString(t)
		if err != nil {
			return nil, false
		}
		return b, true
	default:
		return nil, false
	}
}

func (r *Reticulum) getInterfaceStats() map[string]any {
	interfacesOut := make([]any, 0)
	var totalRX uint64
	var totalTX uint64

	ts := r.transport
	if ts == nil {
		return map[string]any{
			"interfaces":       interfacesOut,
			"rxb":              totalRX,
			"txb":              totalTX,
			"rxs":              0,
			"txs":              0,
			"network_id":       nil,
			"transport_id":     nil,
			"transport_uptime": nil,
			"probe_responder":  nil,
			"rss":              nil,
		}
	}

	ifaces := ts.GetInterfaces()
	for _, iface := range ifaces {
		rx := iface.BytesReceived()
		tx := iface.BytesSent()
		totalRX += rx
		totalTX += tx
		interfacesOut = append(interfacesOut, map[string]any{
			"name":                        iface.Name(),
			"short_name":                  iface.Name(),
			"hash":                        []byte(iface.Name()),
			"type":                        iface.Type(),
			"rxb":                         rx,
			"txb":                         tx,
			"rxs":                         0,
			"txs":                         0,
			"status":                      iface.Status(),
			"mode":                        iface.Mode(),
			"bitrate":                     iface.Bitrate(),
			"clients":                     nil,
			"incoming_announce_frequency": 0.0,
			"outgoing_announce_frequency": 0.0,
			"held_announces":              0,
			"announce_queue":              nil,
			"peers":                       nil,
			"ifac_signature":              nil,
			"ifac_size":                   nil,
			"ifac_netname":                nil,
			"autoconnect_source":          nil,
		})
	}

	var networkID any
	if hash := ts.NetworkIdentityHash(); len(hash) > 0 {
		networkID = hash
	}

	var transportID any
	if ts.Identity() != nil && len(ts.Identity().Hash) > 0 {
		transportID = ts.Identity().Hash
	}

	var transportUptime any
	if sa := ts.StartedAt(); !sa.IsZero() {
		transportUptime = time.Since(sa).Seconds()
	}

	return map[string]any{
		"interfaces":       interfacesOut,
		"rxb":              totalRX,
		"txb":              totalTX,
		"rxs":              0,
		"txs":              0,
		"network_id":       networkID,
		"transport_id":     transportID,
		"transport_uptime": transportUptime,
		"probe_responder":  nil,
		"rss":              nil,
	}
}

func (r *Reticulum) getPathTable(maxHops int) []any {
	entries := r.transport.GetPathTable()
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		if maxHops > 0 && e.Hops > maxHops {
			continue
		}
		ifName := ""
		if e.Interface != nil {
			ifName = e.Interface.Name()
		}
		out = append(out, map[string]any{
			"hash":      e.Hash,
			"timestamp": float64(e.Timestamp.UnixNano()) / 1e9,
			"via":       e.NextHop,
			"hops":      e.Hops,
			"expires":   float64(e.Expires.UnixNano()) / 1e9,
			"interface": ifName,
		})
	}
	return out
}

func (r *Reticulum) getNextHop(destHash []byte) []byte {
	if r.transport == nil {
		return nil
	}
	entry := r.transport.GetPathEntry(destHash)
	if entry == nil {
		return nil
	}
	if entry.NextHop == nil {
		return nil
	}
	return entry.NextHop
}

func (r *Reticulum) getNextHopInterfaceName(destHash []byte) string {
	if r.transport == nil {
		return ""
	}
	entry := r.transport.GetPathEntry(destHash)
	if entry == nil || entry.Interface == nil {
		return ""
	}
	return entry.Interface.Name()
}

func (r *Reticulum) getLinkCount() int {
	if r.transport == nil {
		return 0
	}
	return len(r.transport.LinkTable())
}

func (r *Reticulum) getRateTable() []any {
	if r.transport == nil {
		return []any{}
	}
	rows := r.transport.GetRateTable()
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out
}

func (r *Reticulum) getBlackholedIdentities() []any {
	if r.transport == nil {
		return []any{}
	}
	rows := r.transport.GetBlackholedIdentities()
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out
}

func (r *Reticulum) getFirstHopTimeout(destinationHash []byte) int {
	const defaultPerHopTimeout = 6
	if r.transport == nil {
		return defaultPerHopTimeout
	}

	entry := r.transport.GetPathEntry(destinationHash)
	if entry == nil || entry.Interface == nil || entry.Interface.Bitrate() <= 0 {
		return defaultPerHopTimeout
	}

	latencySeconds := (1.0 / float64(entry.Interface.Bitrate())) * 8.0 * float64(MTU)
	return defaultPerHopTimeout + int(math.Ceil(latencySeconds))
}

func (r *Reticulum) getPacketRSSI(packetHash []byte) any {
	if r.transport == nil {
		return nil
	}
	v, ok := r.transport.GetPacketRSSI(packetHash)
	if !ok {
		return nil
	}
	return v
}

func (r *Reticulum) getPacketSNR(packetHash []byte) any {
	if r.transport == nil {
		return nil
	}
	v, ok := r.transport.GetPacketSNR(packetHash)
	if !ok {
		return nil
	}
	return v
}

func (r *Reticulum) getPacketQ(packetHash []byte) any {
	if r.transport == nil {
		return nil
	}
	v, ok := r.transport.GetPacketQ(packetHash)
	if !ok {
		return nil
	}
	return v
}

func (r *Reticulum) dropAnnounceQueues() int {
	if r.transport == nil {
		return 0
	}
	return r.transport.DropAnnounceQueues()
}

func (r *Reticulum) blackholeIdentity(identityHash []byte, until *int64, reason string) bool {
	if r.transport == nil {
		return false
	}
	return r.transport.BlackholeIdentity(identityHash, until, reason)
}

func (r *Reticulum) unblackholeIdentity(identityHash []byte) bool {
	if r.transport == nil {
		return false
	}
	return r.transport.UnblackholeIdentity(identityHash)
}

// InterfaceStat represents the statistics and status of a single network interface.
type InterfaceStat struct {
	Name    string
	Type    string
	Status  bool
	Mode    int
	Bitrate int
	RXB     uint64
	TXB     uint64
	RXS     float64
	TXS     float64
	Clients *int

	IFACSignature     []byte
	IFACSize          int
	IFACNetname       string
	AutoconnectSource string

	NoiseFloor         *float64
	Interference       *float64
	InterferenceLastTS *float64
	InterferenceLastDB *float64

	CPULoad        *float64
	CPUTemp        *float64
	MemLoad        *float64
	BatteryPercent *int
	BatteryState   string

	AirtimeShort    *float64
	AirtimeLong     *float64
	ChannelLoadShrt *float64
	ChannelLoadLong *float64

	SwitchID    *string
	EndpointID  *string
	ViaSwitchID *string
	Peers       *int
	TunnelState *string

	I2PB32          *string
	I2PConnectable  *bool
	AnnounceQueue   *int
	HeldAnnounces   *int
	InAnnounceFreq  *float64
	OutAnnounceFreq *float64
}

// InterfaceStatsSnapshot represents a snapshot of statistics for all
// network interfaces plus aggregate transport-level metadata.
type InterfaceStatsSnapshot struct {
	Interfaces      []InterfaceStat
	RXB             uint64
	TXB             uint64
	RXS             float64
	TXS             float64
	TransportID     []byte
	NetworkID       []byte
	TransportUptime *float64
	ProbeResponder  []byte
}

// InterfaceStats returns interface stats from local transport, or via RPC when
// this instance is connected to a shared local instance.
func (r *Reticulum) InterfaceStats() (*InterfaceStatsSnapshot, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "interface_stats"})
		if err != nil {
			return nil, err
		}
		return DecodeInterfaceStats(resp), nil
	}

	local := r.getInterfaceStats()
	return DecodeInterfaceStats(local), nil
}

// PathTable retrieves the current path table from the transport system, optionally limiting the results to a maximum number of hops.
func (r *Reticulum) PathTable(maxHops int) ([]any, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "path_table", "max_hops": maxHops})
		if err != nil {
			return nil, err
		}
		if out, ok := resp.([]any); ok {
			return out, nil
		}
		return []any{}, nil
	}
	return r.getPathTable(maxHops), nil
}

// RateTable retrieves the current announce rate table from the transport system.
func (r *Reticulum) RateTable() ([]any, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "rate_table"})
		if err != nil {
			return nil, err
		}
		if out, ok := resp.([]any); ok {
			return out, nil
		}
		return []any{}, nil
	}
	return r.getRateTable(), nil
}

// BlackholedIdentities retrieves the list of currently blackholed identities from the transport system.
func (r *Reticulum) BlackholedIdentities() ([]any, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "blackholed_identities"})
		if err != nil {
			return nil, err
		}
		if out, ok := resp.([]any); ok {
			return out, nil
		}
		return []any{}, nil
	}
	return r.getBlackholedIdentities(), nil
}

// NextHop determines the next hop interface hash for a given destination hash.
func (r *Reticulum) NextHop(destinationHash []byte) ([]byte, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "next_hop", "destination_hash": destinationHash})
		if err != nil {
			return []byte{}, fmt.Errorf("rpc next_hop failed: %w", err)
		}
		if out, ok := decodeHashArg(resp); ok {
			return out, nil
		}
		return []byte{}, nil
	}
	return r.getNextHop(destinationHash), nil
}

// NextHopInterfaceName retrieves the name of the interface that will be used for the next hop towards a destination.
func (r *Reticulum) NextHopInterfaceName(destinationHash []byte) (string, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "next_hop_if_name", "destination_hash": destinationHash})
		if err != nil {
			return "", fmt.Errorf("rpc next_hop_if_name failed: %w", err)
		}
		return asString(resp), nil
	}
	return r.getNextHopInterfaceName(destinationHash), nil
}

// LinkCount returns the total number of active links currently managed by the transport system.
func (r *Reticulum) LinkCount() (int, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "link_count"})
		if err != nil {
			return 0, err
		}
		return asInt(resp), nil
	}
	return r.getLinkCount(), nil
}

// FirstHopTimeout calculates the appropriate timeout in seconds for the first hop towards a given destination.
func (r *Reticulum) FirstHopTimeout(destinationHash []byte) (int, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "first_hop_timeout", "destination_hash": destinationHash})
		if err != nil {
			return 6, fmt.Errorf("rpc first_hop_timeout failed: %w", err)
		}
		timeout := asInt(resp)
		if r.forceSharedBitrate > 0 {
			simulatedLatency := int(math.Ceil((1.0 / float64(r.forceSharedBitrate)) * 8.0 * float64(MTU)))
			timeout += simulatedLatency
		}
		return timeout, nil
	}
	return r.getFirstHopTimeout(destinationHash), nil
}

// PacketRSSI retrieves the Received Signal Strength Indicator (RSSI) for a specific packet hash, if available.
func (r *Reticulum) PacketRSSI(packetHash []byte) (any, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "packet_rssi", "packet_hash": packetHash})
		if err != nil {
			return nil, fmt.Errorf("rpc packet_rssi failed: %w", err)
		}
		return resp, nil
	}
	return r.getPacketRSSI(packetHash), nil
}

// PacketSNR retrieves the Signal-to-Noise Ratio (SNR) for a specific packet hash, if available.
func (r *Reticulum) PacketSNR(packetHash []byte) (any, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "packet_snr", "packet_hash": packetHash})
		if err != nil {
			return nil, fmt.Errorf("rpc packet_snr failed: %w", err)
		}
		return resp, nil
	}
	return r.getPacketSNR(packetHash), nil
}

// PacketQ retrieves the Link Quality indicator for a specific packet hash, if available.
func (r *Reticulum) PacketQ(packetHash []byte) (any, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"get": "packet_q", "packet_hash": packetHash})
		if err != nil {
			return nil, fmt.Errorf("rpc packet_q failed: %w", err)
		}
		return resp, nil
	}
	return r.getPacketQ(packetHash), nil
}

// DropPath invalidates any known path to a given destination hash from the transport system's path table.
func (r *Reticulum) DropPath(destinationHash []byte) (bool, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"drop": "path", "destination_hash": destinationHash})
		if err != nil {
			return false, err
		}
		return asBool(resp), nil
	}
	return r.transport.InvalidatePath(destinationHash), nil
}

// DropAllVia invalidates all paths that go through the specified next hop destination hash.
func (r *Reticulum) DropAllVia(destinationHash []byte) (int, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"drop": "all_via", "destination_hash": destinationHash})
		if err != nil {
			return 0, err
		}
		return asInt(resp), nil
	}
	return r.transport.InvalidatePathsViaNextHop(destinationHash), nil
}

// DropAnnounceQueues clears all pending and queued announces from the transport system.
func (r *Reticulum) DropAnnounceQueues() (int, error) {
	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"drop": "announce_queues"})
		if err != nil {
			return 0, err
		}
		return asInt(resp), nil
	}
	return r.dropAnnounceQueues(), nil
}

// BlackholeIdentity adds an identity hash to the local blackhole list, preventing it from interacting with the network.
func (r *Reticulum) BlackholeIdentity(identityHash []byte, until *int64, reason string) (bool, error) {
	if len(identityHash) != TruncatedHashLength/8 {
		return false, nil
	}

	if r.isConnectedToSharedInstance {
		req := map[string]any{"blackhole_identity": identityHash, "reason": reason}
		if until != nil {
			req["until"] = *until
		}
		resp, err := r.callRPC(req)
		if err != nil {
			return false, err
		}
		return asBool(resp), nil
	}
	return r.blackholeIdentity(identityHash, until, reason), nil
}

// UnblackholeIdentity removes an identity hash from the local blackhole list, restoring its ability to interact with the network.
func (r *Reticulum) UnblackholeIdentity(identityHash []byte) (bool, error) {
	if len(identityHash) != TruncatedHashLength/8 {
		return false, nil
	}

	if r.isConnectedToSharedInstance {
		resp, err := r.callRPC(map[string]any{"unblackhole_identity": identityHash})
		if err != nil {
			return false, err
		}
		return asBool(resp), nil
	}
	return r.unblackholeIdentity(identityHash), nil
}

func (r *Reticulum) callRPC(req any) (any, error) {
	r.ensureRPCKey()
	if len(r.rpcKey) == 0 {
		return nil, errors.New("rpc key unavailable")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := r.callRPCOnce(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isTransientRPCError(err) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	return nil, lastErr
}

func (r *Reticulum) callRPCOnce(req any) (any, error) {
	conn, err := r.dialRPCServer()
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			r.logger.Debug("Failed closing RPC client connection: %v", closeErr)
		}
	}()

	if err := writeRPCFrame(conn, map[string]any{"auth": r.rpcKey}); err != nil {
		return nil, err
	}

	authResp, err := readRPCFrame(conn)
	if err != nil {
		return nil, err
	}
	authMap := asAnyMap(authResp)
	if authMap == nil {
		return nil, errors.New("invalid rpc auth response")
	}
	if v, ok := lookupAny(authMap, "error"); ok {
		return nil, fmt.Errorf("rpc auth failed: %v", v)
	}

	if err := writeRPCFrame(conn, req); err != nil {
		return nil, err
	}

	return readRPCFrame(conn)
}

func isTransientRPCError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "connection reset by peer") || strings.Contains(message, "broken pipe")
}

func (r *Reticulum) dialRPCServer() (net.Conn, error) {
	if r.useAFUnix() {
		instance := r.localSocketPath
		if instance == "" {
			instance = "default"
		}
		if runtime.GOOS == "linux" {
			return net.Dial("unix", "@rns/"+instance+"/rpc")
		}
		rpcPath := filepath.Join(r.configDir, ".rns-"+instance+"-rpc.sock")
		return net.Dial("unix", rpcPath)
	}

	addr := fmt.Sprintf("127.0.0.1:%v", r.localControlPort)
	return net.Dial("tcp", addr)
}

func DecodeInterfaceStats(raw any) *InterfaceStatsSnapshot {
	out := &InterfaceStatsSnapshot{}
	m := asAnyMap(raw)
	if m == nil {
		return out
	}

	out.RXB = asUint64(lookupAnyValue(m, "rxb"))
	out.TXB = asUint64(lookupAnyValue(m, "txb"))
	out.RXS = asFloat64(lookupAnyValue(m, "rxs"))
	out.TXS = asFloat64(lookupAnyValue(m, "txs"))
	out.TransportID = lookupOptBytes(m, "transport_id")
	out.NetworkID = lookupOptBytes(m, "network_id")
	out.TransportUptime = lookupOptFloat64(m, "transport_uptime")
	out.ProbeResponder = lookupOptBytes(m, "probe_responder")

	interfacesVal, ok := lookupAny(m, "interfaces")
	if !ok {
		return out
	}

	list, ok := interfacesVal.([]any)
	if !ok {
		return out
	}

	out.Interfaces = make([]InterfaceStat, 0, len(list))
	for _, item := range list {
		im := asAnyMap(item)
		if im == nil {
			continue
		}
		entry := InterfaceStat{
			Name:    asString(lookupAnyValue(im, "name")),
			Type:    asString(lookupAnyValue(im, "type")),
			Status:  asBool(lookupAnyValue(im, "status")),
			Mode:    asInt(lookupAnyValue(im, "mode")),
			Bitrate: asInt(lookupAnyValue(im, "bitrate")),
			RXB:     asUint64(lookupAnyValue(im, "rxb")),
			TXB:     asUint64(lookupAnyValue(im, "txb")),
			RXS:     asFloat64(lookupAnyValue(im, "rxs")),
			TXS:     asFloat64(lookupAnyValue(im, "txs")),
			Clients: lookupOptInt(im, "clients"),

			IFACSignature:     lookupOptBytes(im, "ifac_signature"),
			IFACSize:          asInt(lookupAnyValue(im, "ifac_size")),
			IFACNetname:       asString(lookupAnyValue(im, "ifac_netname")),
			AutoconnectSource: asString(lookupAnyValue(im, "autoconnect_source")),

			NoiseFloor:         lookupOptFloat64(im, "noise_floor"),
			Interference:       lookupOptFloat64(im, "interference"),
			InterferenceLastTS: lookupOptFloat64(im, "interference_last_ts"),
			InterferenceLastDB: lookupOptFloat64(im, "interference_last_dbm"),

			CPULoad:        lookupOptFloat64(im, "cpu_load"),
			CPUTemp:        lookupOptFloat64(im, "cpu_temp"),
			MemLoad:        lookupOptFloat64(im, "mem_load"),
			BatteryPercent: lookupOptInt(im, "battery_percent"),
			BatteryState:   asString(lookupAnyValue(im, "battery_state")),

			AirtimeShort:    lookupOptFloat64(im, "airtime_short"),
			AirtimeLong:     lookupOptFloat64(im, "airtime_long"),
			ChannelLoadShrt: lookupOptFloat64(im, "channel_load_short"),
			ChannelLoadLong: lookupOptFloat64(im, "channel_load_long"),

			SwitchID:    lookupOptString(im, "switch_id"),
			EndpointID:  lookupOptString(im, "endpoint_id"),
			ViaSwitchID: lookupOptString(im, "via_switch_id"),
			Peers:       lookupOptInt(im, "peers"),
			TunnelState: lookupOptString(im, "tunnelstate"),

			I2PB32:          lookupOptString(im, "i2p_b32"),
			I2PConnectable:  lookupOptBool(im, "i2p_connectable"),
			AnnounceQueue:   lookupOptInt(im, "announce_queue"),
			HeldAnnounces:   lookupOptInt(im, "held_announces"),
			InAnnounceFreq:  lookupOptFloat64(im, "incoming_announce_frequency"),
			OutAnnounceFreq: lookupOptFloat64(im, "outgoing_announce_frequency"),
		}
		out.Interfaces = append(out.Interfaces, entry)
	}

	return out
}
