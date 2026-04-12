// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/gmlewis/go-reticulum/rns"
)

// jsonInterfaceStat is a JSON-serializable version of InterfaceStat
// where byte slices are converted to hex strings.
type jsonInterfaceStat struct {
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Status             bool     `json:"status"`
	Mode               int      `json:"mode"`
	Bitrate            int      `json:"bitrate"`
	RXB                uint64   `json:"rxb"`
	TXB                uint64   `json:"txb"`
	RXS                float64  `json:"rxs"`
	TXS                float64  `json:"txs"`
	Clients            *int     `json:"clients"`
	IFACSignature      string   `json:"ifac_signature,omitempty"`
	IFACSize           int      `json:"ifac_size"`
	IFACNetname        string   `json:"ifac_netname,omitempty"`
	AutoconnectSource  string   `json:"autoconnect_source,omitempty"`
	NoiseFloor         *float64 `json:"noise_floor,omitempty"`
	Interference       *float64 `json:"interference,omitempty"`
	InterferenceLastTS *float64 `json:"interference_last_ts,omitempty"`
	InterferenceLastDB *float64 `json:"interference_last_dbm,omitempty"`
	CPULoad            *float64 `json:"cpu_load,omitempty"`
	CPUTemp            *float64 `json:"cpu_temp,omitempty"`
	MemLoad            *float64 `json:"mem_load,omitempty"`
	BatteryPercent     *int     `json:"battery_percent,omitempty"`
	BatteryState       string   `json:"battery_state,omitempty"`
	AirtimeShort       *float64 `json:"airtime_short,omitempty"`
	AirtimeLong        *float64 `json:"airtime_long,omitempty"`
	ChannelLoadShort   *float64 `json:"channel_load_short,omitempty"`
	ChannelLoadLong    *float64 `json:"channel_load_long,omitempty"`
	SwitchID           *string  `json:"switch_id,omitempty"`
	EndpointID         *string  `json:"endpoint_id,omitempty"`
	ViaSwitchID        *string  `json:"via_switch_id,omitempty"`
	Peers              *int     `json:"peers,omitempty"`
	TunnelState        *string  `json:"tunnelstate,omitempty"`
	I2PB32             *string  `json:"i2p_b32,omitempty"`
	I2PConnectable     *bool    `json:"i2p_connectable,omitempty"`
	AnnounceQueue      *int     `json:"announce_queue,omitempty"`
	HeldAnnounces      *int     `json:"held_announces,omitempty"`
	InAnnounceFreq     *float64 `json:"incoming_announce_frequency,omitempty"`
	OutAnnounceFreq    *float64 `json:"outgoing_announce_frequency,omitempty"`
}

// jsonStatsSnapshot is a JSON-serializable version of InterfaceStatsSnapshot.
type jsonStatsSnapshot struct {
	Interfaces      []jsonInterfaceStat `json:"interfaces"`
	RXB             uint64              `json:"rxb"`
	TXB             uint64              `json:"txb"`
	RXS             float64             `json:"rxs"`
	TXS             float64             `json:"txs"`
	TransportID     string              `json:"transport_id,omitempty"`
	NetworkID       string              `json:"network_id,omitempty"`
	TransportUptime *float64            `json:"transport_uptime,omitempty"`
	ProbeResponder  string              `json:"probe_responder,omitempty"`
}

// toJSONSnapshot converts an InterfaceStatsSnapshot to its
// JSON-serializable form, encoding byte slices as hex strings.
func toJSONSnapshot(s *rns.InterfaceStatsSnapshot) jsonStatsSnapshot {
	out := jsonStatsSnapshot{
		RXB:             s.RXB,
		TXB:             s.TXB,
		RXS:             s.RXS,
		TXS:             s.TXS,
		TransportID:     bytesToHex(s.TransportID),
		NetworkID:       bytesToHex(s.NetworkID),
		TransportUptime: s.TransportUptime,
		ProbeResponder:  bytesToHex(s.ProbeResponder),
	}
	out.Interfaces = make([]jsonInterfaceStat, len(s.Interfaces))
	for i, iface := range s.Interfaces {
		out.Interfaces[i] = jsonInterfaceStat{
			Name:               iface.Name,
			Type:               iface.Type,
			Status:             iface.Status,
			Mode:               iface.Mode,
			Bitrate:            iface.Bitrate,
			RXB:                iface.RXB,
			TXB:                iface.TXB,
			RXS:                iface.RXS,
			TXS:                iface.TXS,
			Clients:            iface.Clients,
			IFACSignature:      bytesToHex(iface.IFACSignature),
			IFACSize:           iface.IFACSize,
			IFACNetname:        iface.IFACNetname,
			AutoconnectSource:  iface.AutoconnectSource,
			NoiseFloor:         iface.NoiseFloor,
			Interference:       iface.Interference,
			InterferenceLastTS: iface.InterferenceLastTS,
			InterferenceLastDB: iface.InterferenceLastDB,
			CPULoad:            iface.CPULoad,
			CPUTemp:            iface.CPUTemp,
			MemLoad:            iface.MemLoad,
			BatteryPercent:     iface.BatteryPercent,
			BatteryState:       iface.BatteryState,
			AirtimeShort:       iface.AirtimeShort,
			AirtimeLong:        iface.AirtimeLong,
			ChannelLoadShort:   iface.ChannelLoadShrt,
			ChannelLoadLong:    iface.ChannelLoadLong,
			SwitchID:           iface.SwitchID,
			EndpointID:         iface.EndpointID,
			ViaSwitchID:        iface.ViaSwitchID,
			Peers:              iface.Peers,
			TunnelState:        iface.TunnelState,
			I2PB32:             iface.I2PB32,
			I2PConnectable:     iface.I2PConnectable,
			AnnounceQueue:      iface.AnnounceQueue,
			HeldAnnounces:      iface.HeldAnnounces,
			InAnnounceFreq:     iface.InAnnounceFreq,
			OutAnnounceFreq:    iface.OutAnnounceFreq,
		}
	}
	return out
}

// renderDiscoveredJSON encodes discovered interfaces to w as JSON.
func renderDiscoveredJSON(w io.Writer, ifs []rns.DiscoveredInterface) error {
	data, err := json.MarshalIndent(ifs, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// renderJSON writes the JSON representation of stats to w.
func renderJSON(w io.Writer, stats *rns.InterfaceStatsSnapshot) error {
	snapshot := toJSONSnapshot(stats)
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// bytesToHex converts a byte slice to a hex string,
// returning empty string for nil/empty slices.
func bytesToHex(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}
