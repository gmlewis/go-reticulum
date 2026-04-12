// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// renderDiscoveredInterfaces writes a table of discovered interfaces to w,
// matching the Python rnstatus.py format exactly.
func renderDiscoveredInterfaces(w io.Writer, ifs []rns.DiscoveredInterface) {
	if len(ifs) == 0 {
		return
	}

	_, _ = fmt.Fprintf(w, "%-25s %-12v %-12v %-12v %-8v %-15v\n", "Name", "Type", "Status", "Last Heard", "Value", "Location")
	_, _ = fmt.Fprintln(w, strings.Repeat("-", 89))

	now := float64(time.Now().UnixNano()) / 1e9
	for _, i := range ifs {
		name := i.Name
		if len(name) > 24 {
			name = name[:24] + "…"
		}

		ifType := strings.TrimSuffix(i.Type, "Interface")

		statusDisplay := i.Status
		switch i.Status {
		case "available":
			statusDisplay = "✓ Available"
		case "unknown":
			statusDisplay = "? Unknown"
		case "stale":
			statusDisplay = "× Stale"
		}

		diff := now - i.LastHeard
		lastHeardDisplay := ""
		if diff < 60 {
			lastHeardDisplay = "Just now"
		} else if diff < 3600 {
			lastHeardDisplay = fmt.Sprintf("%vm ago", int(diff/60))
		} else if diff < 86400 {
			lastHeardDisplay = fmt.Sprintf("%vh ago", int(diff/3600))
		} else {
			lastHeardDisplay = fmt.Sprintf("%vd ago", int(diff/86400))
		}

		location := "N/A"
		if i.Latitude != nil && i.Longitude != nil {
			location = fmt.Sprintf("%.4f, %.4f", *i.Latitude, *i.Longitude)
		}

		_, _ = fmt.Fprintf(w, "%-25s %-12v %-12v %-12v %-8v %-15v\n",
			name, ifType, statusDisplay, lastHeardDisplay, i.Value, location)
	}
}

// renderDiscoveredInterfaceDetails writes detailed info for discovered interfaces to w,
// matching the Python rnstatus.py format exactly.
func renderDiscoveredInterfaceDetails(w io.Writer, ifs []rns.DiscoveredInterface) {
	now := float64(time.Now().UnixNano()) / 1e9
	for idx, i := range ifs {
		statusDisplay := i.Status
		switch i.Status {
		case "available":
			statusDisplay = "Available"
		case "unknown":
			statusDisplay = "Unknown"
		case "stale":
			statusDisplay = "Stale"
		default:
			if len(statusDisplay) > 0 {
				statusDisplay = strings.ToUpper(statusDisplay[:1]) + statusDisplay[1:]
			}
		}

		dago := now - i.Discovered
		hago := now - i.LastHeard
		discoveredDisplay := rns.PrettyTime(dago, true, true) + " ago"
		lastHeardDisplay := rns.PrettyTime(hago, true, true) + " ago"

		transportStr := "Disabled"
		if i.Transport {
			transportStr = "Enabled"
		}

		location := "Unknown"
		if i.Latitude != nil && i.Longitude != nil {
			heightStr := ""
			if i.Height != nil {
				heightStr = fmt.Sprintf(", %vm h", *i.Height)
			}
			location = fmt.Sprintf("%.4f, %.4f%v", *i.Latitude, *i.Longitude, heightStr)
		}

		network := ""
		if i.TransportID != "" && i.NetworkID != "" && i.TransportID != i.NetworkID {
			network = i.NetworkID
		}

		if idx > 0 {
			_, _ = fmt.Fprintln(w, "\n"+strings.Repeat("=", 32)+"\n")
		}

		if network != "" {
			_, _ = fmt.Fprintf(w, "Network   ID : %v\n", network)
		}
		if i.TransportID != "" {
			_, _ = fmt.Fprintf(w, "Transport ID : %v\n", i.TransportID)
		}

		_, _ = fmt.Fprintf(w, "Name         : %v\n", i.Name)
		_, _ = fmt.Fprintf(w, "Type         : %v\n", i.Type)
		_, _ = fmt.Fprintf(w, "Status       : %v\n", statusDisplay)
		_, _ = fmt.Fprintf(w, "Transport    : %v\n", transportStr)
		_, _ = fmt.Fprintf(w, "Distance     : %v hop%v\n", i.Hops, plural(i.Hops))
		_, _ = fmt.Fprintf(w, "Discovered   : %v\n", discoveredDisplay)
		_, _ = fmt.Fprintf(w, "Last Heard   : %v\n", lastHeardDisplay)
		_, _ = fmt.Fprintf(w, "Location     : %v\n", location)

		if i.Frequency != nil {
			_, _ = fmt.Fprintf(w, "Frequency    : %v Hz\n", formatInt(*i.Frequency))
		}
		if i.Bandwidth != nil {
			_, _ = fmt.Fprintf(w, "Bandwidth    : %v Hz\n", formatInt(*i.Bandwidth))
		}
		if i.SF != nil {
			_, _ = fmt.Fprintf(w, "Sprd. Factor : %v\n", *i.SF)
		}
		if i.CR != nil {
			_, _ = fmt.Fprintf(w, "Coding Rate  : %v\n", *i.CR)
		}
		if i.Modulation != "" {
			_, _ = fmt.Fprintf(w, "Modulation   : %v\n", i.Modulation)
		}
		if i.ReachableOn != "" {
			_, _ = fmt.Fprintf(w, "Address      : %v\n", i.ReachableOn)
		}
		if i.Port != nil {
			_, _ = fmt.Fprintf(w, "Port         : %v\n", *i.Port)
		}

		_, _ = fmt.Fprintf(w, "Stamp Value  : %v\n", i.Value)

		_, _ = fmt.Fprintln(w, "\nConfiguration Entry:")
		configLines := strings.Split(strings.TrimSpace(i.ConfigEntry), "\n")
		for _, line := range configLines {
			_, _ = fmt.Fprintf(w, "  %v\n", line)
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func formatInt(n int) string {
	// Simple comma-formatter for integers
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	var res []string
	for len(s) > 3 {
		res = append([]string{s[len(s)-3:]}, res...)
		s = s[:len(s)-3]
	}
	if len(s) > 0 {
		res = append([]string{s}, res...)
	}
	return strings.Join(res, ",")
}

// renderInterface writes the per-interface output block to w,
// matching the Python rnstatus.py format exactly.
func renderInterface(w io.Writer, ifstat rns.InterfaceStat, astats bool) {
	name := ifstat.Name

	ss := "Up"
	if !ifstat.Status {
		ss = "Down"
	}
	ms := modeString(ifstat.Mode)
	cs := clientsString(name, ifstat.Clients)

	_, _ = fmt.Fprintf(w, " %v\n", name)

	if ifstat.AutoconnectSource != "" {
		_, _ = fmt.Fprintf(w, "    Source    : Auto-connect via <%v>\n", ifstat.AutoconnectSource)
	}

	if ifstat.IFACNetname != "" {
		_, _ = fmt.Fprintf(w, "    Network   : %v\n", ifstat.IFACNetname)
	}

	_, _ = fmt.Fprintf(w, "    Status    : %v\n", ss)

	if ifstat.Clients != nil && cs != "" {
		_, _ = fmt.Fprintf(w, "    %v\n", cs)
	}

	if !strings.HasPrefix(name, "Shared Instance[") &&
		!strings.HasPrefix(name, "TCPInterface[Client") &&
		!strings.HasPrefix(name, "LocalInterface[") {
		_, _ = fmt.Fprintf(w, "    Mode      : %v\n", ms)
	}

	if ifstat.Bitrate != 0 {
		_, _ = fmt.Fprintf(w, "    Rate      : %v\n", speedStr(float64(ifstat.Bitrate)))
	}

	if ifstat.NoiseFloor != nil {
		var nstr string
		if ifstat.Interference != nil {
			nf := *ifstat.Interference
			if nf != 0 {
				nstr = fmt.Sprintf("\n    Intrfrnc. : %v dBm", nf)
			} else {
				lstr := ", no interference"
				if ifstat.InterferenceLastTS != nil && ifstat.InterferenceLastDB != nil {
					lago := time.Since(time.Unix(int64(*ifstat.InterferenceLastTS), 0))
					ldbm := *ifstat.InterferenceLastDB
					lstr = fmt.Sprintf("\n    Intrfrnc. : %v dBm %v ago",
						ldbm, rns.PrettyTime(lago.Seconds(), false, true))
				}
				nstr = lstr
			}
		}
		if *ifstat.NoiseFloor != 0 {
			_, _ = fmt.Fprintf(w, "    Noise Fl. : %v dBm%v\n", *ifstat.NoiseFloor, nstr)
		} else {
			_, _ = fmt.Fprintln(w, "    Noise Fl. : Unknown")
		}
	}

	if ifstat.CPULoad != nil {
		if *ifstat.CPULoad != 0 {
			_, _ = fmt.Fprintf(w, "    CPU load  : %v %%\n", *ifstat.CPULoad)
		} else {
			_, _ = fmt.Fprintln(w, "    CPU load  : Unknown")
		}
	}

	if ifstat.CPUTemp != nil {
		if *ifstat.CPUTemp != 0 {
			_, _ = fmt.Fprintf(w, "    CPU temp  : %v°C\n", *ifstat.CPUTemp)
		} else {
			_, _ = fmt.Fprintln(w, "    CPU load  : Unknown")
		}
	}

	if ifstat.MemLoad != nil {
		if ifstat.CPULoad != nil && *ifstat.CPULoad != 0 {
			_, _ = fmt.Fprintf(w, "    Mem usage : %v %%\n", *ifstat.MemLoad)
		} else {
			_, _ = fmt.Fprintln(w, "    Mem usage : Unknown")
		}
	}

	if ifstat.BatteryPercent != nil {
		_, _ = fmt.Fprintf(w, "    Battery   : %v%% (%v)\n", *ifstat.BatteryPercent, ifstat.BatteryState)
	}

	if ifstat.AirtimeShort != nil && ifstat.AirtimeLong != nil {
		_, _ = fmt.Fprintf(w, "    Airtime   : %v%% (15s), %v%% (1h)\n",
			*ifstat.AirtimeShort, *ifstat.AirtimeLong)
	}

	if ifstat.ChannelLoadShrt != nil && ifstat.ChannelLoadLong != nil {
		_, _ = fmt.Fprintf(w, "    Ch. Load  : %v%% (15s), %v%% (1h)\n",
			*ifstat.ChannelLoadShrt, *ifstat.ChannelLoadLong)
	}

	if ifstat.SwitchID != nil {
		if *ifstat.SwitchID != "" {
			_, _ = fmt.Fprintf(w, "    Switch ID : %v\n", *ifstat.SwitchID)
		} else {
			_, _ = fmt.Fprintln(w, "    Switch ID : Unknown")
		}
	}

	if ifstat.EndpointID != nil {
		if *ifstat.EndpointID != "" {
			_, _ = fmt.Fprintf(w, "    Endpoint  : %v\n", *ifstat.EndpointID)
		} else {
			_, _ = fmt.Fprintln(w, "    Endpoint  : Unknown")
		}
	}

	if ifstat.ViaSwitchID != nil {
		if *ifstat.ViaSwitchID != "" {
			_, _ = fmt.Fprintf(w, "    Via       : %v\n", *ifstat.ViaSwitchID)
		} else {
			_, _ = fmt.Fprintln(w, "    Via       : Unknown")
		}
	}

	if ifstat.Peers != nil {
		_, _ = fmt.Fprintf(w, "    Peers     : %v reachable\n", *ifstat.Peers)
	}

	if ifstat.TunnelState != nil {
		_, _ = fmt.Fprintf(w, "    I2P       : %v\n", *ifstat.TunnelState)
	}

	if len(ifstat.IFACSignature) > 0 {
		tail := ifstat.IFACSignature
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		sigstr := "<…" + hex.EncodeToString(tail) + ">"
		_, _ = fmt.Fprintf(w, "    Access    : %v-bit IFAC by %v\n",
			ifstat.IFACSize*8, sigstr)
	}

	if ifstat.I2PB32 != nil {
		_, _ = fmt.Fprintf(w, "    I2P B32   : %v\n", *ifstat.I2PB32)
	}

	if astats && ifstat.AnnounceQueue != nil && *ifstat.AnnounceQueue > 0 {
		aqn := *ifstat.AnnounceQueue
		plural := "s"
		if aqn == 1 {
			plural = ""
		}
		_, _ = fmt.Fprintf(w, "    Queued    : %v announce%v\n", aqn, plural)
	}

	if astats && ifstat.HeldAnnounces != nil && *ifstat.HeldAnnounces > 0 {
		aqn := *ifstat.HeldAnnounces
		plural := "s"
		if aqn == 1 {
			plural = ""
		}
		_, _ = fmt.Fprintf(w, "    Held      : %v announce%v\n", aqn, plural)
	}

	if astats && ifstat.InAnnounceFreq != nil {
		outFreq := float64(0)
		if ifstat.OutAnnounceFreq != nil {
			outFreq = *ifstat.OutAnnounceFreq
		}
		_, _ = fmt.Fprintf(w, "    Announces : %v↑\n", rns.PrettyFrequency(outFreq))
		_, _ = fmt.Fprintf(w, "                %v↓\n", rns.PrettyFrequency(*ifstat.InAnnounceFreq))
	}

	renderTraffic(w, ifstat)
}

// linkStatsString returns the link stats string for the footer.
// If hasTransportID is true, it prepends a comma for embedding
// in the transport uptime line.
func linkStatsString(linkCount *int, hasTransportID bool) string {
	if linkCount == nil {
		return ""
	}
	ms := "ies"
	if *linkCount == 1 {
		ms = "y"
	}
	if hasTransportID {
		return fmt.Sprintf(", %v entr%v in link table", *linkCount, ms)
	}
	return fmt.Sprintf(" %v entr%v in link table", *linkCount, ms)
}

// renderTotals writes the traffic totals footer.
func renderTotals(w io.Writer, stats *rns.InterfaceStatsSnapshot) {
	rxbStr := "↓" + rns.PrettySize(float64(stats.RXB), "B")
	txbStr := "↑" + rns.PrettySize(float64(stats.TXB), "B")

	diff := len(rxbStr) - len(txbStr)
	if diff > 0 {
		txbStr += strings.Repeat(" ", diff)
	} else if diff < 0 {
		rxbStr += strings.Repeat(" ", -diff)
	}

	rxstat := rxbStr + "  " + rns.PrettySpeed(stats.RXS)
	txstat := txbStr + "  " + rns.PrettySpeed(stats.TXS)
	_, _ = fmt.Fprintf(w, "\n Totals       : %v\n                %v\n", txstat, rxstat)
}

// renderTransportFooter writes the transport instance footer.
func renderTransportFooter(w io.Writer, stats *rns.InterfaceStatsSnapshot, lstr string) {
	if len(stats.TransportID) > 0 {
		_, _ = fmt.Fprintf(w, "\n Transport Instance %v running\n", rns.PrettyHex(stats.TransportID))
		if len(stats.NetworkID) > 0 {
			_, _ = fmt.Fprintf(w, " Network Identity   %v\n", rns.PrettyHex(stats.NetworkID))
		}
		if len(stats.ProbeResponder) > 0 {
			_, _ = fmt.Fprintf(w, " Probe responder at %v active\n", rns.PrettyHex(stats.ProbeResponder))
		}
		if stats.TransportUptime != nil {
			_, _ = fmt.Fprintf(w, " Uptime is %v%v\n", rns.PrettyTime(*stats.TransportUptime, false, false), lstr)
		}
	} else {
		if lstr != "" {
			_, _ = fmt.Fprintf(w, "\n%v\n", lstr)
		}
	}
}

// renderTraffic writes the traffic line for a single interface.
func renderTraffic(w io.Writer, ifstat rns.InterfaceStat) {
	rxbStr := "↓" + rns.PrettySize(float64(ifstat.RXB), "B")
	txbStr := "↑" + rns.PrettySize(float64(ifstat.TXB), "B")

	diff := len(rxbStr) - len(txbStr)
	if diff > 0 {
		txbStr += strings.Repeat(" ", diff)
	} else if diff < 0 {
		rxbStr += strings.Repeat(" ", -diff)
	}

	rxstat := rxbStr
	txstat := txbStr
	if ifstat.RXS != 0 || ifstat.TXS != 0 {
		rxstat += "  " + rns.PrettySpeed(ifstat.RXS)
		txstat += "  " + rns.PrettySpeed(ifstat.TXS)
	}

	_, _ = fmt.Fprintf(w, "    Traffic   : %v\n                %v\n", txstat, rxstat)
}
