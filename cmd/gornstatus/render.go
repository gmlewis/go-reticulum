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

	fmt.Fprintf(w, " %v\n", name)

	if ifstat.AutoconnectSource != "" {
		fmt.Fprintf(w, "    Source    : Auto-connect via <%v>\n", ifstat.AutoconnectSource)
	}

	if ifstat.IFACNetname != "" {
		fmt.Fprintf(w, "    Network   : %v\n", ifstat.IFACNetname)
	}

	fmt.Fprintf(w, "    Status    : %v\n", ss)

	if ifstat.Clients != nil && cs != "" {
		fmt.Fprintf(w, "    %v\n", cs)
	}

	if !strings.HasPrefix(name, "Shared Instance[") &&
		!strings.HasPrefix(name, "TCPInterface[Client") &&
		!strings.HasPrefix(name, "LocalInterface[") {
		fmt.Fprintf(w, "    Mode      : %v\n", ms)
	}

	if ifstat.Bitrate != 0 {
		fmt.Fprintf(w, "    Rate      : %v\n", speedStr(float64(ifstat.Bitrate)))
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
			fmt.Fprintf(w, "    Noise Fl. : %v dBm%v\n", *ifstat.NoiseFloor, nstr)
		} else {
			fmt.Fprintln(w, "    Noise Fl. : Unknown")
		}
	}

	if ifstat.CPULoad != nil {
		if *ifstat.CPULoad != 0 {
			fmt.Fprintf(w, "    CPU load  : %v %%\n", *ifstat.CPULoad)
		} else {
			fmt.Fprintln(w, "    CPU load  : Unknown")
		}
	}

	if ifstat.CPUTemp != nil {
		if *ifstat.CPUTemp != 0 {
			fmt.Fprintf(w, "    CPU temp  : %v°C\n", *ifstat.CPUTemp)
		} else {
			fmt.Fprintln(w, "    CPU load  : Unknown")
		}
	}

	if ifstat.MemLoad != nil {
		if ifstat.CPULoad != nil && *ifstat.CPULoad != 0 {
			fmt.Fprintf(w, "    Mem usage : %v %%\n", *ifstat.MemLoad)
		} else {
			fmt.Fprintln(w, "    Mem usage : Unknown")
		}
	}

	if ifstat.BatteryPercent != nil {
		fmt.Fprintf(w, "    Battery   : %v%% (%v)\n", *ifstat.BatteryPercent, ifstat.BatteryState)
	}

	if ifstat.AirtimeShort != nil && ifstat.AirtimeLong != nil {
		fmt.Fprintf(w, "    Airtime   : %v%% (15s), %v%% (1h)\n",
			*ifstat.AirtimeShort, *ifstat.AirtimeLong)
	}

	if ifstat.ChannelLoadShrt != nil && ifstat.ChannelLoadLong != nil {
		fmt.Fprintf(w, "    Ch. Load  : %v%% (15s), %v%% (1h)\n",
			*ifstat.ChannelLoadShrt, *ifstat.ChannelLoadLong)
	}

	if ifstat.SwitchID != nil {
		if *ifstat.SwitchID != "" {
			fmt.Fprintf(w, "    Switch ID : %v\n", *ifstat.SwitchID)
		} else {
			fmt.Fprintln(w, "    Switch ID : Unknown")
		}
	}

	if ifstat.EndpointID != nil {
		if *ifstat.EndpointID != "" {
			fmt.Fprintf(w, "    Endpoint  : %v\n", *ifstat.EndpointID)
		} else {
			fmt.Fprintln(w, "    Endpoint  : Unknown")
		}
	}

	if ifstat.ViaSwitchID != nil {
		if *ifstat.ViaSwitchID != "" {
			fmt.Fprintf(w, "    Via       : %v\n", *ifstat.ViaSwitchID)
		} else {
			fmt.Fprintln(w, "    Via       : Unknown")
		}
	}

	if ifstat.Peers != nil {
		fmt.Fprintf(w, "    Peers     : %v reachable\n", *ifstat.Peers)
	}

	if ifstat.TunnelState != nil {
		fmt.Fprintf(w, "    I2P       : %v\n", *ifstat.TunnelState)
	}

	if len(ifstat.IFACSignature) > 0 {
		tail := ifstat.IFACSignature
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		sigstr := "<…" + hex.EncodeToString(tail) + ">"
		fmt.Fprintf(w, "    Access    : %v-bit IFAC by %v\n",
			ifstat.IFACSize*8, sigstr)
	}

	if ifstat.I2PB32 != nil {
		fmt.Fprintf(w, "    I2P B32   : %v\n", *ifstat.I2PB32)
	}

	if astats && ifstat.AnnounceQueue != nil && *ifstat.AnnounceQueue > 0 {
		aqn := *ifstat.AnnounceQueue
		plural := "s"
		if aqn == 1 {
			plural = ""
		}
		fmt.Fprintf(w, "    Queued    : %v announce%v\n", aqn, plural)
	}

	if astats && ifstat.HeldAnnounces != nil && *ifstat.HeldAnnounces > 0 {
		aqn := *ifstat.HeldAnnounces
		plural := "s"
		if aqn == 1 {
			plural = ""
		}
		fmt.Fprintf(w, "    Held      : %v announce%v\n", aqn, plural)
	}

	if astats && ifstat.InAnnounceFreq != nil {
		outFreq := float64(0)
		if ifstat.OutAnnounceFreq != nil {
			outFreq = *ifstat.OutAnnounceFreq
		}
		fmt.Fprintf(w, "    Announces : %v↑\n", rns.PrettyFrequency(outFreq))
		fmt.Fprintf(w, "                %v↓\n", rns.PrettyFrequency(*ifstat.InAnnounceFreq))
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
	fmt.Fprintf(w, "\n Totals       : %v\n                %v\n", txstat, rxstat)
}

// renderTransportFooter writes the transport instance footer.
func renderTransportFooter(w io.Writer, stats *rns.InterfaceStatsSnapshot, lstr string) {
	if len(stats.TransportID) > 0 {
		fmt.Fprintf(w, "\n Transport Instance %v running\n", rns.PrettyHex(stats.TransportID))
		if len(stats.NetworkID) > 0 {
			fmt.Fprintf(w, " Network Identity   %v\n", rns.PrettyHex(stats.NetworkID))
		}
		if len(stats.ProbeResponder) > 0 {
			fmt.Fprintf(w, " Probe responder at %v active\n", rns.PrettyHex(stats.ProbeResponder))
		}
		if stats.TransportUptime != nil {
			fmt.Fprintf(w, " Uptime is %v%v\n", rns.PrettyTime(*stats.TransportUptime, false, false), lstr)
		}
	} else {
		if lstr != "" {
			fmt.Fprintf(w, "\n%v\n", lstr)
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

	fmt.Fprintf(w, "    Traffic   : %v\n                %v\n", txstat, rxstat)
}
