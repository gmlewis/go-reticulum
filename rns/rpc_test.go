// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

func rpcWriteFrame(t *testing.T, conn net.Conn, v any) {
	t.Helper()
	payload, err := msgpack.Pack(v)
	if err != nil {
		t.Fatalf("msgpack.Pack error: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatalf("write header error: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write payload error: %v", err)
	}
}

func rpcReadFrame(t *testing.T, conn net.Conn) any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline error: %v", err)
	}
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Fatalf("read header error: %v", err)
	}
	size := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, size)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read payload error: %v", err)
	}
	v, err := msgpack.Unpack(buf)
	if err != nil {
		t.Fatalf("msgpack.Unpack error: %v", err)
	}
	return v
}

func mustDialRPC(t *testing.T, addr string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial rpc %q failed: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRPCAuthAndGetEndpoints(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	cfg, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`, t.Name(), sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)
	if !r.isSharedInstance {
		t.Fatalf("expected shared instance")
	}

	conn := mustDialRPC(t, fmt.Sprintf("127.0.0.1:%v", rpcPort))
	defer func() { _ = conn.Close() }()

	rpcWriteFrame(t, conn, map[string]any{"auth": rpcKeyHex})
	ack := rpcReadFrame(t, conn)
	ackMap, ok := ack.(map[any]any)
	if !ok || ackMap["ok"] != true {
		t.Fatalf("unexpected auth ack: %#v", ack)
	}

	rpcWriteFrame(t, conn, map[string]any{"get": "link_count"})
	if got := rpcReadFrame(t, conn); got == nil {
		t.Fatalf("expected link_count response, got nil")
	}

	rpcWriteFrame(t, conn, map[string]any{"get": "interface_stats"})
	stats := rpcReadFrame(t, conn)
	if _, ok := stats.(map[any]any); !ok {
		t.Fatalf("expected map response for interface_stats, got %#v", stats)
	}

	rpcWriteFrame(t, conn, map[string]any{"get": "path_table"})
	paths := rpcReadFrame(t, conn)
	if _, ok := paths.([]any); !ok {
		t.Fatalf("expected list response for path_table, got %#v", paths)
	}
}

func TestRPCRejectsInvalidAuth(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)

	cfg, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = 00112233445566778899aabbccddeeff

[logging]
loglevel = 4

[interfaces]
`, t.Name(), sharedPort, rpcPort))

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)

	conn := mustDialRPC(t, fmt.Sprintf("127.0.0.1:%v", rpcPort))
	defer func() { _ = conn.Close() }()

	rpcWriteFrame(t, conn, map[string]any{"auth": "deadbeef"})
	resp := rpcReadFrame(t, conn)
	m, ok := resp.(map[any]any)
	if !ok {
		t.Fatalf("unexpected auth response: %#v", resp)
	}
	if m["error"] != "unauthorized" {
		t.Fatalf("expected unauthorized error, got %#v", resp)
	}
}

func TestRPCAcceptsByteAuthKey(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	cfg, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`, t.Name(), sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)

	conn := mustDialRPC(t, fmt.Sprintf("127.0.0.1:%v", rpcPort))
	defer func() { _ = conn.Close() }()

	rpcWriteFrame(t, conn, map[string]any{"auth": []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}})
	ack := rpcReadFrame(t, conn)
	ackMap, ok := ack.(map[any]any)
	if !ok || ackMap["ok"] != true {
		t.Fatalf("unexpected auth ack: %#v", ack)
	}
}

func TestConnectedInstanceInterfaceStatsViaRPC(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	configTemplate := `[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`

	cfg1, cleanup1 := testutils.TempDir(t, tempDirPrefix)
	defer cleanup1()
	cfg2, cleanup2 := testutils.TempDir(t, tempDirPrefix)
	defer cleanup2()
	writeConfig(t, cfg1, fmt.Sprintf(configTemplate, t.Name()+"-1", sharedPort, rpcPort, rpcKeyHex))
	writeConfig(t, cfg2, fmt.Sprintf(configTemplate, t.Name()+"-2", sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r1 := mustTestNewReticulum(t, ts, cfg1)
	defer closeReticulum(t, r1)
	if !r1.isSharedInstance {
		t.Fatalf("expected first instance to be shared")
	}

	r2 := mustTestNewReticulum(t, ts, cfg2)
	defer closeReticulum(t, r2)
	if !r2.isConnectedToSharedInstance {
		t.Fatalf("expected second instance to be connected to shared instance")
	}

	stats, err := r2.InterfaceStats()
	if err != nil {
		t.Fatalf("InterfaceStats via RPC error: %v", err)
	}
	if stats == nil {
		t.Fatalf("InterfaceStats returned nil snapshot")
	}
	if len(stats.Interfaces) == 0 {
		t.Fatalf("expected at least one interface stat entry via RPC proxy")
	}
}

func TestRPCExpandedGetDropAndBlackholeSurface(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	cfg, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`, t.Name(), sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)

	conn := mustDialRPC(t, fmt.Sprintf("127.0.0.1:%v", rpcPort))
	defer func() { _ = conn.Close() }()

	rpcWriteFrame(t, conn, map[string]any{"auth": rpcKeyHex})
	_ = rpcReadFrame(t, conn)

	rpcWriteFrame(t, conn, map[string]any{"get": "packet_rssi", "packet_hash": []byte{0x01}})
	if got := rpcReadFrame(t, conn); got != nil {
		t.Fatalf("expected nil packet_rssi response, got %#v", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"get": "packet_snr", "packet_hash": []byte{0x01}})
	if got := rpcReadFrame(t, conn); got != nil {
		t.Fatalf("expected nil packet_snr response, got %#v", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"get": "packet_q", "packet_hash": []byte{0x01}})
	if got := rpcReadFrame(t, conn); got != nil {
		t.Fatalf("expected nil packet_q response, got %#v", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"get": "first_hop_timeout", "destination_hash": []byte{0x01}})
	if got := rpcReadFrame(t, conn); got == nil {
		t.Fatalf("expected first_hop_timeout response")
	}

	rpcWriteFrame(t, conn, map[string]any{"drop": "announce_queues"})
	if got := rpcReadFrame(t, conn); asInt(got) < 0 {
		t.Fatalf("unexpected drop announce_queues response %#v", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"blackhole_identity": []byte{0x01}, "until": int64(0), "reason": "test"})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected blackhole_identity=true, got %#v", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"unblackhole_identity": []byte{0x01}})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected unblackhole_identity=true, got %#v", got)
	}
}

func TestConnectedInstanceExpandedProxyMethods(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	configTemplate := `[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`

	cfg1, cleanup1 := testutils.TempDir(t, tempDirPrefix)
	defer cleanup1()
	cfg2, cleanup2 := testutils.TempDir(t, tempDirPrefix)
	defer cleanup2()
	writeConfig(t, cfg1, fmt.Sprintf(configTemplate, t.Name()+"-1", sharedPort, rpcPort, rpcKeyHex))
	writeConfig(t, cfg2, fmt.Sprintf(configTemplate, t.Name()+"-2", sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r1 := mustTestNewReticulum(t, ts, cfg1)
	defer closeReticulum(t, r1)

	r2 := mustTestNewReticulum(t, ts, cfg2)
	defer closeReticulum(t, r2)
	if !r2.isConnectedToSharedInstance {
		t.Fatalf("expected second instance to be connected to shared instance")
	}

	if _, err := r2.PathTable(0); err != nil {
		t.Fatalf("PathTable error: %v", err)
	}
	if _, err := r2.RateTable(); err != nil {
		t.Fatalf("RateTable error: %v", err)
	}
	if _, err := r2.BlackholedIdentities(); err != nil {
		t.Fatalf("BlackholedIdentities error: %v", err)
	}
	if _, err := r2.NextHop([]byte{0x01}); err != nil && !strings.Contains(err.Error(), "rpc next_hop failed") {
		t.Fatalf("NextHop unexpected error: %v", err)
	}
	if _, err := r2.NextHopInterfaceName([]byte{0x01}); err != nil && !strings.Contains(err.Error(), "rpc next_hop_if_name failed") {
		t.Fatalf("NextHopInterfaceName unexpected error: %v", err)
	}
	if _, err := r2.LinkCount(); err != nil {
		t.Fatalf("LinkCount error: %v", err)
	}
	if _, err := r2.FirstHopTimeout([]byte{0x01}); err != nil && !strings.Contains(err.Error(), "rpc first_hop_timeout failed") {
		t.Fatalf("FirstHopTimeout unexpected error: %v", err)
	}
	if _, err := r2.PacketRSSI([]byte{0x01}); err != nil && !strings.Contains(err.Error(), "rpc packet_rssi failed") {
		t.Fatalf("PacketRSSI unexpected error: %v", err)
	}
	if _, err := r2.PacketSNR([]byte{0x01}); err != nil && !strings.Contains(err.Error(), "rpc packet_snr failed") {
		t.Fatalf("PacketSNR unexpected error: %v", err)
	}
	if _, err := r2.PacketQ([]byte{0x01}); err != nil && !strings.Contains(err.Error(), "rpc packet_q failed") {
		t.Fatalf("PacketQ unexpected error: %v", err)
	}
	if _, err := r2.DropPath([]byte{0x01}); err != nil {
		t.Fatalf("DropPath error: %v", err)
	}
	if _, err := r2.DropAllVia([]byte{0x01}); err != nil {
		t.Fatalf("DropAllVia error: %v", err)
	}
	if _, err := r2.DropAnnounceQueues(); err != nil {
		t.Fatalf("DropAnnounceQueues error: %v", err)
	}
	if _, err := r2.BlackholeIdentity([]byte{0x01}, nil, "test"); err != nil {
		t.Fatalf("BlackholeIdentity error: %v", err)
	}
	if _, err := r2.UnblackholeIdentity([]byte{0x01}); err != nil {
		t.Fatalf("UnblackholeIdentity error: %v", err)
	}
}

func TestConnectedInstanceProxyMethodsPropagateRPCErrors(t *testing.T) {
	t.Parallel()

	r := &Reticulum{
		isConnectedToSharedInstance: true,
		sharedInstanceType:          "tcp",
		localControlPort:            reserveTCPPort(t),
		rpcKey:                      []byte{0x00, 0x11, 0x22, 0x33},
	}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "NextHop", call: func() error {
			_, err := r.NextHop([]byte{0x01})
			return err
		}},
		{name: "NextHopInterfaceName", call: func() error {
			_, err := r.NextHopInterfaceName([]byte{0x01})
			return err
		}},
		{name: "FirstHopTimeout", call: func() error {
			_, err := r.FirstHopTimeout([]byte{0x01})
			return err
		}},
		{name: "PacketRSSI", call: func() error {
			_, err := r.PacketRSSI([]byte{0x01})
			return err
		}},
		{name: "PacketSNR", call: func() error {
			_, err := r.PacketSNR([]byte{0x01})
			return err
		}},
		{name: "PacketQ", call: func() error {
			_, err := r.PacketQ([]byte{0x01})
			return err
		}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.call(); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestConnectedInstanceBlackholeIdentityInvalidHashShortCircuits(t *testing.T) {
	t.Parallel()

	r := &Reticulum{
		isConnectedToSharedInstance: true,
		sharedInstanceType:          "tcp",
		localControlPort:            reserveTCPPort(t),
		rpcKey:                      []byte{0x00, 0x11, 0x22, 0x33},
	}

	ok, err := r.BlackholeIdentity([]byte{0x01}, nil, "test")
	if err != nil {
		t.Fatalf("BlackholeIdentity unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected false for invalid hash length")
	}

	ok, err = r.UnblackholeIdentity([]byte{0x01})
	if err != nil {
		t.Fatalf("UnblackholeIdentity unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected false for invalid hash length")
	}
}

func TestRPCPathTableSchemaIncludesTimestamp(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	now := time.Now().Truncate(time.Second)
	destHash := []byte("0123456789abcdef")
	nextHop := []byte("fedcba9876543210")
	iface := &dummyInterface{name: "schema-iface"}

	ts.mu.Lock()
	ts.pathTable[string(destHash)] = &PathEntry{
		Timestamp: now,
		NextHop:   nextHop,
		Hops:      1,
		Expires:   now.Add(time.Hour),
		Interface: iface,
	}
	ts.mu.Unlock()

	r := &Reticulum{transport: ts}

	resp := r.handleRPCRequest(map[any]any{"get": "path_table", "max_hops": 0})
	rows, ok := resp.([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("expected one path_table row, got %#v", resp)
	}

	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("expected row map[string]any, got %#v", rows[0])
	}

	if _, ok := row["timestamp"]; !ok {
		t.Fatalf("expected timestamp field in path_table row, got %#v", row)
	}
	if _, ok := row["expires"]; !ok {
		t.Fatalf("expected expires field in path_table row, got %#v", row)
	}
	if row["interface"] != "schema-iface" {
		t.Fatalf("expected interface name schema-iface, got %#v", row["interface"])
	}
}

func TestRPCInterfaceStatsSchemaIncludesCoreFields(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := &dummyInterface{name: "stats-iface"}
	ts.RegisterInterface(iface)

	r := &Reticulum{transport: ts}

	resp := r.handleRPCRequest(map[any]any{"get": "interface_stats"})
	stats, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("expected stats map[string]any, got %#v", resp)
	}

	for _, key := range []string{"interfaces", "rxb", "txb", "rxs", "txs", "network_id"} {
		if _, ok := stats[key]; !ok {
			t.Fatalf("expected key %q in interface_stats, got %#v", key, stats)
		}
	}
	for _, key := range []string{"transport_id", "transport_uptime", "probe_responder", "rss"} {
		if _, ok := stats[key]; !ok {
			t.Fatalf("expected key %q in interface_stats, got %#v", key, stats)
		}
	}

	interfaces, ok := stats["interfaces"].([]any)
	if !ok || len(interfaces) == 0 {
		t.Fatalf("expected non-empty interfaces list, got %#v", stats["interfaces"])
	}

	entry, ok := interfaces[0].(map[string]any)
	if !ok {
		t.Fatalf("expected interface entry map[string]any, got %#v", interfaces[0])
	}

	for _, key := range []string{
		"name", "short_name", "hash", "type", "rxb", "txb", "rxs", "txs", "status", "mode", "bitrate",
		"clients", "incoming_announce_frequency", "outgoing_announce_frequency", "held_announces", "announce_queue",
		"peers", "ifac_signature", "ifac_size", "ifac_netname", "autoconnect_source",
	} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("expected key %q in interface entry, got %#v", key, entry)
		}
	}
}

func TestDecodeInterfaceStatsAllFields(t *testing.T) {
	t.Parallel()
	clients := 5
	raw := map[string]any{
		"rxb":              uint64(1000),
		"txb":              uint64(2000),
		"rxs":              1.5,
		"txs":              2.5,
		"transport_id":     []byte{0xaa, 0xbb},
		"network_id":       []byte{0xcc, 0xdd},
		"transport_uptime": 3600.0,
		"probe_responder":  []byte{0xee, 0xff},
		"interfaces": []any{
			map[string]any{
				"name": "TestIface", "type": "UDP", "status": true,
				"mode": 1, "bitrate": 9600, "rxb": uint64(500), "txb": uint64(600),
				"rxs": 10.0, "txs": 20.0, "clients": clients,
				"ifac_signature": []byte{1, 2, 3, 4, 5}, "ifac_size": 16,
				"ifac_netname": "mynet", "autoconnect_source": "auto1",
				"noise_floor": -120.0, "interference": -80.0,
				"interference_last_ts": 1000.0, "interference_last_dbm": -90.0,
				"cpu_load": 55.0, "cpu_temp": 42.0, "mem_load": 30.0,
				"battery_percent": 85, "battery_state": "charging",
				"airtime_short": 1.5, "airtime_long": 2.5,
				"channel_load_short": 3.5, "channel_load_long": 4.5,
				"switch_id": "sw1", "endpoint_id": "ep1", "via_switch_id": "vsw1",
				"peers": 3, "tunnelstate": "active",
				"i2p_b32": "abc.b32.i2p", "i2p_connectable": true,
				"announce_queue": 2, "held_announces": 7,
				"incoming_announce_frequency": 0.5, "outgoing_announce_frequency": 0.3,
			},
		},
	}

	snap := DecodeInterfaceStats(raw)
	if snap.RXB != 1000 {
		t.Errorf("RXB = %v, want 1000", snap.RXB)
	}
	if snap.TXB != 2000 {
		t.Errorf("TXB = %v, want 2000", snap.TXB)
	}
	if snap.RXS != 1.5 {
		t.Errorf("RXS = %v, want 1.5", snap.RXS)
	}
	if snap.TXS != 2.5 {
		t.Errorf("TXS = %v, want 2.5", snap.TXS)
	}
	if string(snap.TransportID) != string([]byte{0xaa, 0xbb}) {
		t.Errorf("TransportID = %x, want aabb", snap.TransportID)
	}
	if string(snap.NetworkID) != string([]byte{0xcc, 0xdd}) {
		t.Errorf("NetworkID = %x, want ccdd", snap.NetworkID)
	}
	if snap.TransportUptime == nil || *snap.TransportUptime != 3600.0 {
		t.Errorf("TransportUptime = %v, want 3600.0", snap.TransportUptime)
	}
	if string(snap.ProbeResponder) != string([]byte{0xee, 0xff}) {
		t.Errorf("ProbeResponder = %x, want eeff", snap.ProbeResponder)
	}

	if len(snap.Interfaces) != 1 {
		t.Fatalf("len(Interfaces) = %v, want 1", len(snap.Interfaces))
	}
	iface := snap.Interfaces[0]
	if iface.Name != "TestIface" {
		t.Errorf("Name = %q", iface.Name)
	}
	if iface.Mode != 1 {
		t.Errorf("Mode = %v, want 1", iface.Mode)
	}
	if iface.RXS != 10.0 {
		t.Errorf("RXS = %v, want 10.0", iface.RXS)
	}
	if iface.Clients == nil || *iface.Clients != 5 {
		t.Errorf("Clients = %v, want 5", iface.Clients)
	}
	if len(iface.IFACSignature) != 5 {
		t.Errorf("IFACSignature len = %v, want 5", len(iface.IFACSignature))
	}
	if iface.IFACSize != 16 {
		t.Errorf("IFACSize = %v, want 16", iface.IFACSize)
	}
	if iface.IFACNetname != "mynet" {
		t.Errorf("IFACNetname = %q, want mynet", iface.IFACNetname)
	}
	if iface.AutoconnectSource != "auto1" {
		t.Errorf("AutoconnectSource = %q, want auto1", iface.AutoconnectSource)
	}
	if iface.NoiseFloor == nil || *iface.NoiseFloor != -120.0 {
		t.Errorf("NoiseFloor = %v, want -120.0", iface.NoiseFloor)
	}
	if iface.CPULoad == nil || *iface.CPULoad != 55.0 {
		t.Errorf("CPULoad = %v, want 55.0", iface.CPULoad)
	}
	if iface.BatteryPercent == nil || *iface.BatteryPercent != 85 {
		t.Errorf("BatteryPercent = %v, want 85", iface.BatteryPercent)
	}
	if iface.BatteryState != "charging" {
		t.Errorf("BatteryState = %q, want charging", iface.BatteryState)
	}
	if iface.AirtimeShort == nil || *iface.AirtimeShort != 1.5 {
		t.Errorf("AirtimeShort = %v, want 1.5", iface.AirtimeShort)
	}
	if iface.SwitchID == nil || *iface.SwitchID != "sw1" {
		t.Errorf("SwitchID = %v, want sw1", iface.SwitchID)
	}
	if iface.Peers == nil || *iface.Peers != 3 {
		t.Errorf("Peers = %v, want 3", iface.Peers)
	}
	if iface.I2PB32 == nil || *iface.I2PB32 != "abc.b32.i2p" {
		t.Errorf("I2PB32 = %v, want abc.b32.i2p", iface.I2PB32)
	}
	if iface.I2PConnectable == nil || *iface.I2PConnectable != true {
		t.Errorf("I2PConnectable = %v, want true", iface.I2PConnectable)
	}
	if iface.AnnounceQueue == nil || *iface.AnnounceQueue != 2 {
		t.Errorf("AnnounceQueue = %v, want 2", iface.AnnounceQueue)
	}
	if iface.HeldAnnounces == nil || *iface.HeldAnnounces != 7 {
		t.Errorf("HeldAnnounces = %v, want 7", iface.HeldAnnounces)
	}
	if iface.InAnnounceFreq == nil || *iface.InAnnounceFreq != 0.5 {
		t.Errorf("InAnnounceFreq = %v, want 0.5", iface.InAnnounceFreq)
	}
	if iface.OutAnnounceFreq == nil || *iface.OutAnnounceFreq != 0.3 {
		t.Errorf("OutAnnounceFreq = %v, want 0.3", iface.OutAnnounceFreq)
	}
}

func TestDecodeInterfaceStatsNilOptionals(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"rxb": uint64(0), "txb": uint64(0),
		"interfaces": []any{
			map[string]any{
				"name": "Bare", "type": "TCP", "status": false,
				"mode": 0, "bitrate": 0, "rxb": uint64(0), "txb": uint64(0),
			},
		},
	}
	snap := DecodeInterfaceStats(raw)
	if len(snap.Interfaces) != 1 {
		t.Fatalf("len(Interfaces) = %v, want 1", len(snap.Interfaces))
	}
	iface := snap.Interfaces[0]
	if iface.Clients != nil {
		t.Errorf("Clients should be nil, got %v", *iface.Clients)
	}
	if iface.NoiseFloor != nil {
		t.Errorf("NoiseFloor should be nil, got %v", *iface.NoiseFloor)
	}
	if iface.CPULoad != nil {
		t.Errorf("CPULoad should be nil, got %v", *iface.CPULoad)
	}
	if iface.SwitchID != nil {
		t.Errorf("SwitchID should be nil, got %v", *iface.SwitchID)
	}
	if iface.I2PConnectable != nil {
		t.Errorf("I2PConnectable should be nil, got %v", *iface.I2PConnectable)
	}
	if snap.TransportID != nil {
		t.Errorf("TransportID should be nil, got %x", snap.TransportID)
	}
	if snap.TransportUptime != nil {
		t.Errorf("TransportUptime should be nil, got %v", *snap.TransportUptime)
	}
}

func TestConnectedInstanceManagementCallsRecoverAfterRPCServerRestart(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name               string
		sharedInstanceType string
		sameConfigDir      bool
	}{
		{name: "TCP", sharedInstanceType: "tcp", sameConfigDir: false},
	}

	if runtime.GOOS != "windows" {
		testCases = append(testCases, struct {
			name               string
			sharedInstanceType string
			sameConfigDir      bool
		}{name: "Unix", sharedInstanceType: "", sameConfigDir: true})
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sharedPort := reserveTCPPort(t)
			rpcPort := reserveTCPPort(t)
			rpcKeyHex := "00112233445566778899aabbccddeeff"

			cfg1, cleanup1 := testutils.TempDir(t, tempDirPrefix)
			defer cleanup1()
			cfg2, cleanup2 := testutils.TempDir(t, tempDirPrefix)
			defer cleanup2()
			if tc.sameConfigDir {
				cfg2 = cfg1
			}

			configTemplate := `[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v
`
			if tc.sharedInstanceType != "" {
				configTemplate += "shared_instance_type = " + tc.sharedInstanceType + "\n"
			}
			configTemplate += `
[logging]
loglevel = 4

[interfaces]
`

			configBody := fmt.Sprintf(configTemplate, t.Name(), sharedPort, rpcPort, rpcKeyHex)
			writeConfig(t, cfg1, configBody)
			if !tc.sameConfigDir {
				writeConfig(t, cfg2, configBody)
			}

			ts := NewTransportSystem(nil)
			r1 := mustTestNewReticulum(t, ts, cfg1)
			defer closeReticulum(t, r1)
			if !r1.isSharedInstance {
				t.Fatalf("expected first instance to be shared, got shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
			}

			r2 := mustTestNewReticulum(t, ts, cfg2)
			defer closeReticulum(t, r2)

			if !r2.isConnectedToSharedInstance {
				t.Fatalf("expected connected-to-shared role, got shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
			}

			if _, err := r2.LinkCount(); err != nil {
				t.Fatalf("baseline LinkCount failed before restart: %v", err)
			}

			if r1.rpcListener == nil {
				t.Fatalf("expected shared instance rpc listener")
			}
			if err := r1.rpcListener.Close(); err != nil {
				t.Fatalf("closing shared rpc listener failed: %v", err)
			}
			r1.rpcListener = nil

			if _, err := r2.LinkCount(); err == nil {
				t.Fatalf("expected LinkCount to fail while rpc listener is down")
			}

			if err := r1.startRPCListener(); err != nil {
				t.Fatalf("failed to restart rpc listener: %v", err)
			}
			if r1.rpcListener == nil {
				t.Fatalf("rpc listener did not restart")
			}

			deadline := time.Now().Add(2 * time.Second)
			for {
				if _, err := r2.LinkCount(); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("LinkCount did not recover after rpc listener restart")
				}
				time.Sleep(25 * time.Millisecond)
			}
		})
	}

}
