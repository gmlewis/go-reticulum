// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// WDCL event decoding (RNS/Interfaces/WeaveInterface.py: Evt, LogFrame,
// incoming_frame WDCL_T_LOG branch, log_handle). This is pure protocol logic
// with no platform dependency, so it compiles on every GOOS and is unit-tested
// cross-platform; the linux-only WeaveInterface wires it into its serial
// receive path.

package interfaces

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// WDCL packet types (WeaveInterface.py:49-55, class WDCL).
const (
	WDCLTDiscover    = 0x00
	WDCLTConnect     = 0x01
	WDCLTCmd         = 0x02
	WDCLTLog         = 0x03
	WDCLTDisp        = 0x04
	WDCLTEndpointPkt = 0x05
	WDCLTEncapProto  = 0x06
)

// WDCL log-event type codes (WeaveInterface.py:338-389, class Evt). These are
// the ET_* constants exactly as Python defines them. Note: Python declares
// ET_DRV_W80211_INIT twice (0x1060 then 0x1061); the second assignment wins,
// so the effective value is 0x1061 — that is what the descriptions table and
// this constant use.
const (
	WeaveETMsg                    = 0x0000
	WeaveETSystemBoot             = 0x0001
	WeaveETCoreInit               = 0x0002
	WeaveETBoardInit              = 0x0003
	WeaveETDrvUARTInit            = 0x1000
	WeaveETDrvUSBCDCInit          = 0x1010
	WeaveETDrvUSBCDCHostAvail     = 0x1011
	WeaveETDrvUSBCDCHostSuspend   = 0x1012
	WeaveETDrvUSBCDCHostResume    = 0x1013
	WeaveETDrvUSBCDCConnected     = 0x1014
	WeaveETDrvUSBCDCReadErr       = 0x1015
	WeaveETDrvUSBCDCOverflow      = 0x1016
	WeaveETDrvUSBCDCDropped       = 0x1017
	WeaveETDrvUSBCDCTxTimeout     = 0x1018
	WeaveETDrvI2CInit             = 0x1020
	WeaveETDrvNVSInit             = 0x1030
	WeaveETDrvCryptoInit          = 0x1040
	WeaveETDrvDisplayInit         = 0x1050
	WeaveETDrvDisplayBusAvailable = 0x1051
	WeaveETDrvDisplayIOConfigured = 0x1052
	WeaveETDrvDisplayPanelCreated = 0x1053
	WeaveETDrvDisplayPanelReset   = 0x1054
	WeaveETDrvDisplayPanelInit    = 0x1055
	WeaveETDrvDisplayPanelEnable  = 0x1056
	WeaveETDrvDisplayRemoteEnable = 0x1057
	WeaveETDrvW80211Init          = 0x1061
	WeaveETDrvW80211Channel       = 0x1062
	WeaveETDrvW80211Power         = 0x1063
	WeaveETKrnLoggerInit          = 0x2000
	WeaveETKrnLoggerOutput        = 0x2001
	WeaveETKrnUIInit              = 0x2010
	WeaveETProtoWDCLInit          = 0x3000
	WeaveETProtoWDCLRunning       = 0x3001
	WeaveETProtoWDCLConnection    = 0x3002
	WeaveETProtoWDCLHostEndpoint  = 0x3003
	WeaveETProtoWeaveInit         = 0x3100
	WeaveETProtoWeaveRunning      = 0x3101
	WeaveETProtoWeaveEPAlive      = 0x3102
	WeaveETProtoWeaveEPTimeout    = 0x3103
	WeaveETSrvctlRemoteDisplay    = 0xA000
	WeaveETInterfaceRegistered    = 0xD000
	WeaveETStatState              = 0xE000
	WeaveETStatUptime             = 0xE001
	WeaveETStatTimebase           = 0xE002
	WeaveETStatCPU                = 0xE003
	WeaveETStatTaskCPU            = 0xE004
	WeaveETStatMemory             = 0xE005
	WeaveETStatStorage            = 0xE006
	WeaveETSyserrMemExhausted     = 0xF000
)

// WDCL log levels (WeaveInterface.py:489-498).
const (
	WeaveLogForce    = 0
	WeaveLogCritical = 1
	WeaveLogError    = 2
	WeaveLogWarning  = 3
	WeaveLogNotice   = 4
	WeaveLogInfo     = 5
	WeaveLogVerbose  = 6
	WeaveLogDebug    = 7
	WeaveLogExtreme  = 8
	WeaveLogSystem   = 9
)

// weaveEventDescriptions maps an event code to its human description
// (WeaveInterface.py:409-451, Evt.event_descriptions).
var weaveEventDescriptions = map[uint16]string{
	WeaveETSystemBoot:             "System boot",
	WeaveETCoreInit:               "Core initialization",
	WeaveETBoardInit:              "Board hardware initialization",
	WeaveETDrvUARTInit:            "UART driver initialization",
	WeaveETDrvUSBCDCInit:          "USB CDC driver initialization",
	WeaveETDrvUSBCDCHostAvail:     "USB CDC host became available",
	WeaveETDrvUSBCDCHostSuspend:   "USB CDC host suspend",
	WeaveETDrvUSBCDCHostResume:    "USB CDC host resume",
	WeaveETDrvUSBCDCConnected:     "USB CDC host connection",
	WeaveETDrvUSBCDCReadErr:       "USB CDC read error",
	WeaveETDrvUSBCDCOverflow:      "USB CDC overflow occurred",
	WeaveETDrvUSBCDCDropped:       "USB CDC dropped bytes",
	WeaveETDrvUSBCDCTxTimeout:     "USB CDC TX flush timeout",
	WeaveETDrvI2CInit:             "I2C driver initialization",
	WeaveETDrvNVSInit:             "NVS driver initialization",
	WeaveETDrvCryptoInit:          "Cryptography driver initialization",
	WeaveETDrvW80211Init:          "W802.11 driver initialization",
	WeaveETDrvW80211Channel:       "W802.11 channel configuration",
	WeaveETDrvW80211Power:         "W802.11 TX power configuration",
	WeaveETDrvDisplayInit:         "Display driver initialization",
	WeaveETDrvDisplayBusAvailable: "Display bus availability",
	WeaveETDrvDisplayIOConfigured: "Display I/O configuration",
	WeaveETDrvDisplayPanelCreated: "Display panel allocation",
	WeaveETDrvDisplayPanelReset:   "Display panel reset",
	WeaveETDrvDisplayPanelInit:    "Display panel initialization",
	WeaveETDrvDisplayPanelEnable:  "Display panel activation",
	WeaveETDrvDisplayRemoteEnable: "Remote display output activation",
	WeaveETKrnLoggerInit:          "Logging service initialization",
	WeaveETKrnLoggerOutput:        "Logging service output activation",
	WeaveETKrnUIInit:              "User interface service initialization",
	WeaveETProtoWDCLInit:          "WDCL protocol initialization",
	WeaveETProtoWDCLRunning:       "WDCL protocol activation",
	WeaveETProtoWDCLConnection:    "WDCL host connection",
	WeaveETProtoWDCLHostEndpoint:  "Weave host endpoint",
	WeaveETProtoWeaveInit:         "Weave protocol initialization",
	WeaveETProtoWeaveRunning:      "Weave protocol activation",
	WeaveETProtoWeaveEPAlive:      "Weave endpoint alive",
	WeaveETProtoWeaveEPTimeout:    "Weave endpoint disappeared",
	WeaveETSrvctlRemoteDisplay:    "Remote display service control event",
	WeaveETInterfaceRegistered:    "Interface registration",
	WeaveETSyserrMemExhausted:     "System memory exhausted",
}

// weaveLevels maps a log level to its name (WeaveInterface.py:499-510).
var weaveLevels = map[byte]string{
	WeaveLogForce:    "Forced",
	WeaveLogCritical: "Critical",
	WeaveLogError:    "Error",
	WeaveLogWarning:  "Warning",
	WeaveLogNotice:   "Notice",
	WeaveLogInfo:     "Info",
	WeaveLogVerbose:  "Verbose",
	WeaveLogDebug:    "Debug",
	WeaveLogExtreme:  "Extreme",
	WeaveLogSystem:   "System",
}

// weaveLevelName returns the name for a level, "Unknown" if not in the table
// (WeaveInterface.py:530-533, Evt.level).
func weaveLevelName(level byte) string {
	if name, ok := weaveLevels[level]; ok {
		return name
	}
	return "Unknown"
}

// weaveInterfaceTypes maps an interface-type code to its short name
// (WeaveInterface.py:453-471, Evt.interface_types).
var weaveInterfaceTypes = map[byte]string{
	0x01: "usb",
	0x02: "uart",
	0x03: "mw",
	0x04: "ble",
	0x05: "lora",
	0x06: "eth",
	0x07: "wifi",
	0x08: "tcp",
	0x09: "udp",
	0x0A: "ir",
	0x0B: "afsk",
	0x0C: "gpio",
	0x0D: "spi",
	0x0E: "i2c",
	0x0F: "can",
	0x10: "dma",
}

// weaveChannelDescriptions maps an 802.11 channel number to its description
// (WeaveInterface.py:473-488, Evt.channel_descriptions).
var weaveChannelDescriptions = map[byte]string{
	1:  "Channel 1 (2412 MHz)",
	2:  "Channel 2 (2417 MHz)",
	3:  "Channel 3 (2422 MHz)",
	4:  "Channel 4 (2427 MHz)",
	5:  "Channel 5 (2432 MHz)",
	6:  "Channel 6 (2437 MHz)",
	7:  "Channel 7 (2442 MHz)",
	8:  "Channel 8 (2447 MHz)",
	9:  "Channel 9 (2452 MHz)",
	10: "Channel 10 (2457 MHz)",
	11: "Channel 11 (2462 MHz)",
	12: "Channel 12 (2467 MHz)",
	13: "Channel 13 (2472 MHz)",
	14: "Channel 14 (2484 MHz)",
}

// WeaveLogFrame is a decoded WDCL log event (WeaveInterface.py:534-543,
// LogFrame). Timestamp is the event timestamp in seconds (raw ms / 1000),
// Level is the log level, Event is the event code, Data is the event payload.
type WeaveLogFrame struct {
	Timestamp float64
	Level     byte
	Event     uint16
	Data      []byte
}

// DecodeWeaveLogEvent parses the payload of a WDCL_T_LOG frame into a
// WeaveLogFrame (WeaveInterface.py:725-728, incoming_frame WDCL_T_LOG branch).
// fd is the bytes after the switch_id + packet-type + length, i.e. fd begins
// at the skip byte the Python code indexes as fd[0]:
//
//	fd[0]      skip (unused)
//	fd[1..4]   timestamp, big-endian milliseconds
//	fd[5]      level
//	fd[6..7]   event, big-endian
//	fd[8:]     data
func DecodeWeaveLogEvent(fd []byte) (WeaveLogFrame, error) {
	// The Python parse reads fd[1]..fd[7] and fd[8:], so it needs at least 8
	// bytes (it indexes fd[7]); shorter payloads are a malformed frame.
	if len(fd) < 8 {
		return WeaveLogFrame{}, fmt.Errorf("weave: log frame too short: %d bytes", len(fd))
	}
	ts := uint32(fd[1])<<24 | uint32(fd[2])<<16 | uint32(fd[3])<<8 | uint32(fd[4])
	lvl := fd[5]
	evt := uint16(fd[6])<<8 | uint16(fd[7])
	data := append([]byte(nil), fd[8:]...)
	return WeaveLogFrame{
		Timestamp: float64(ts) / 1000.0,
		Level:     lvl,
		Event:     evt,
		Data:      data,
	}, nil
}

// RenderWeaveLogEvent renders a decoded log frame to the human-readable line
// Python's log_handle produces (WeaveInterface.py:759-804), WITHOUT the
// per-interface "{rns_interface}: " prefix RNS.log adds. The timestamp is
// formatted with weavePrettytime (a duration, since the raw value is an uptime
// counter), matching RNS.prettytime.
func RenderWeaveLogEvent(f WeaveLogFrame) string {
	ts := weavePrettytime(f.Timestamp)
	level := weaveLevelName(f.Level)

	// ET_MSG: a free-form message — no event-description bracket
	// (WeaveInterface.py:770-774).
	if f.Event == WeaveETMsg {
		dataString := ""
		if len(f.Data) > 0 {
			dataString = string(f.Data)
		}
		return fmt.Sprintf("[%s] [%s]: %s", ts, level, dataString)
	}

	// Event description: known table entry, else "0x{hex}" (lines 776-777).
	eventDescription, ok := weaveEventDescriptions[f.Event]
	if !ok {
		eventDescription = fmt.Sprintf("0x%s", weaveHexrepUint(f.Event, false))
	}

	dataString := weaveEventDataString(f)

	return fmt.Sprintf("[%s] [%s] [%s]%s", ts, level, eventDescription, dataString)
}

// weaveEventDataString computes the ": ..." suffix for a non-MSG event
// (WeaveInterface.py:778-798). It mirrors the Python if/elif/else chain:
// ET_INTERFACE_REGISTERED → ": {type}{index}"; otherwise, when there is data,
// ": {hexrep}" with per-event overrides for USB-CDC-connected, 802.11 channel,
// 802.11 power, and the core..proto-weave-running success/failure/stopped
// range; empty data yields "".
func weaveEventDataString(f WeaveLogFrame) string {
	if f.Event == WeaveETInterfaceRegistered {
		if len(f.Data) >= 2 {
			ifaceIndex := f.Data[0]
			ifaceType := f.Data[1]
			typeName := "phy"
			if name, ok := weaveInterfaceTypes[ifaceType]; ok {
				typeName = name
			}
			return fmt.Sprintf(": %s%d", typeName, ifaceIndex)
		}
		return ""
	}
	if len(f.Data) == 0 {
		return ""
	}
	// Per-event overrides (lines 788-797).
	switch f.Event {
	case WeaveETDrvUSBCDCConnected:
		switch f.Data[0] {
		case 0x01:
			return ": Connected"
		case 0x00:
			return ": Disconnected"
		}
	case WeaveETDrvW80211Channel:
		if name, ok := weaveChannelDescriptions[f.Data[0]]; ok {
			return fmt.Sprintf(": %s", name)
		}
		return fmt.Sprintf(": %s", weaveHexrep(f.Data, true))
	case WeaveETDrvW80211Power:
		txPower := float64(f.Data[0]) * 0.25
		mW := int(math.Pow(10, txPower/10))
		return fmt.Sprintf(": %s dBm (%d mW)", pyFloatStr(txPower), mW)
	}
	// Core-init .. proto-weave-running range: success / failure / stopped
	// (lines 794-798).
	if f.Event >= WeaveETCoreInit && f.Event <= WeaveETProtoWeaveRunning {
		switch f.Data[0] {
		case 0x01:
			return ": Success"
		case 0x00:
			if f.Level == WeaveLogError {
				return ": Failure"
			}
			return ": Stopped"
		}
	}
	return fmt.Sprintf(": %s", weaveHexrep(f.Data, true))
}

// weaveHexrep renders bytes as lowercase hex, colon-separated when delimit is
// true (RNS.hexrep, __init__.py:176-183).
func weaveHexrep(data []byte, delimit bool) string {
	parts := make([]string, len(data))
	for i, b := range data {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	sep := ""
	if delimit {
		sep = ":"
	}
	return strings.Join(parts, sep)
}

// weaveHexrepUint renders a 16-bit event code as lowercase hex, optionally
// colon-separated (RNS.hexrep accepts a non-iterable and wraps it in a list;
// for a 16-bit value this yields one or two bytes). The unknown-event
// description uses delimit=false, producing a bare "beef".
func weaveHexrepUint(v uint16, delimit bool) string {
	b := []byte{byte(v >> 8), byte(v & 0xff)}
	return weaveHexrep(b, delimit)
}

// weavePrettytime formats a duration in seconds the way RNS.prettytime does in
// its non-verbose form (RNS/__init__.py:241-282, the default log_handle uses):
// up to four components (days "d", hours "h", minutes "m", seconds "s") joined
// by ", " with " and " before the last. Non-verbose uses the bare unit letter
// with no pluralization; an integer-valued second count prints as "X.0s" to
// match Python's str(float).
func weavePrettytime(seconds float64) string {
	neg := seconds < 0
	if neg {
		seconds = -seconds
	}
	days := int(seconds / (24 * 3600))
	seconds = math.Mod(seconds, 24*3600)
	hours := int(seconds / 3600)
	seconds = math.Mod(seconds, 3600)
	minutes := int(seconds / 60)
	seconds = math.Mod(seconds, 60)
	// Python uses round(seconds, 2); math.Round rounds half away from zero,
	// which matches for the uptime-derived values the decoder handles.
	secs := math.Round(seconds*100) / 100

	var comps []string
	if days > 0 {
		comps = append(comps, strconv.Itoa(days)+"d")
	}
	if hours > 0 {
		comps = append(comps, strconv.Itoa(hours)+"h")
	}
	if minutes > 0 {
		comps = append(comps, strconv.Itoa(minutes)+"m")
	}
	if secs > 0 {
		comps = append(comps, pyFloatStr(secs)+"s")
	}

	if len(comps) == 0 {
		return "0s"
	}
	var sb strings.Builder
	for i, c := range comps {
		switch {
		case i == 0:
			sb.WriteString(c)
		case i == len(comps)-1:
			sb.WriteString(" and ")
			sb.WriteString(c)
		default:
			sb.WriteString(", ")
			sb.WriteString(c)
		}
	}
	if neg {
		return "-" + sb.String()
	}
	return sb.String()
}

// pyFloatStr formats a float the way Python's str(float) does for the small,
// at-most-two-decimal values the renderer produces: integer-valued floats get
// a trailing ".0" (1.0 -> "1.0", 40.0 -> "40.0"); others keep their shortest
// representation (1.5 -> "1.5", 1.25 -> "1.25").
func pyFloatStr(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		return s + ".0"
	}
	return s
}

// WeaveStatLenMax is the number of stat samples retained per metric
// (WeaveInterface.py:561, WeaveDevice.STATLEN_MAX). It mirrors a Python
// collections.deque(maxlen=...): appending beyond the cap drops the oldest
// sample (see weaveAppendCapped).
const WeaveStatLenMax = 120

// weaveTaskDescriptions maps a task id to its human description
// (WeaveInterface.py:513-531, Evt.task_descriptions). Used by GetActiveTasks
// to replace a raw task id with its description.
var weaveTaskDescriptions = map[string]string{
	"taskLVGL":       "Driver: UI Renderer",
	"ui_service":     "Service: User Interface",
	"TinyUSB":        "Driver: USB",
	"drv_w80211":     "Driver: W802.11",
	"system_stats":   "System: Stats",
	"core":           "System: Core",
	"protocol_wdcl":  "Protocol: WDCL",
	"protocol_weave": "Protocol: Weave",
	"tiT":            "Protocol: TCP/IP",
	"ipc0":           "System: CPU 0 IPC",
	"ipc1":           "System: CPU 1 IPC",
	"esp_timer":      "Driver: Timers",
	"Tmr Svc":        "Service: Timers",
	"kernel_logger":  "Service: Logging",
	"remote_display": "Service: Remote Display",
	"wifi":           "System: WiFi Hardware",
	"sys_evt":        "System: Kernel Events",
}

// WeaveTaskStat is one entry of WeaveDevice.active_tasks
// (WeaveInterface.py:755-756, the ET_STAT_TASK_CPU branch of log_handle):
// data[0] is the task cpu_load and data[1:] is the UTF-8 task id.
type WeaveTaskStat struct {
	CPULoad   byte
	Timestamp float64
}

// WeaveCPUSample is one entry of WeaveDevice.cpu_stats
// (WeaveInterface.py:637, capture_stats_cpu).
type WeaveCPUSample struct {
	Timestamp float64
	CPULoad   byte
}

// WeaveMemorySample is one entry of WeaveDevice.memory_stats
// (WeaveInterface.py:641, capture_stats_memory).
type WeaveMemorySample struct {
	Timestamp  float64
	MemoryUsed uint32
}

// WeaveDeviceStat is the stat-handling surface of a WeaveDevice
// (WeaveInterface.py:578-596 + the ET_STAT_* branches of log_handle). It holds
// the latest cpu_load, the memory counters, the per-task cpu table, and the
// capped cpu/memory sample histories. OnStatsUpdate mirrors the Python
// receiver.stats_update notification: captureStatsCPU invokes it with "cpu"
// and captureStatsMemory with "memory", in both cases only when
// len(MemoryStats) > 1 — faithfully reproducing the Python receiver guard,
// which (as a quirk) checks the memory history for both captures
// (WeaveInterface.py:638, 642).
type WeaveDeviceStat struct {
	CPULoad       byte
	MemoryTotal   uint32
	MemoryFree    uint32
	MemoryUsed    uint32
	MemoryUsedPct float64
	ActiveTasks   map[string]WeaveTaskStat
	CPUStats      []WeaveCPUSample
	MemoryStats   []WeaveMemorySample
	OnStatsUpdate func(kind string)
}

// NewWeaveDeviceStat returns a zeroed WeaveDeviceStat with the ActiveTasks map
// allocated (the sample slices start nil and grow under capture).
func NewWeaveDeviceStat() *WeaveDeviceStat {
	return &WeaveDeviceStat{ActiveTasks: make(map[string]WeaveTaskStat)}
}

// HandleWeaveStatEvent applies a decoded log frame's stat side-effects to the
// device (WeaveInterface.py:755-764, the ET_STAT_TASK_CPU / ET_STAT_CPU /
// ET_STAT_MEMORY branches of log_handle). now stands in for time.time() so the
// dispatch is deterministic under test. Non-stat events are ignored (the Python
// if/elif chain falls through to the generic render branch, which is not part of
// this stat surface).
func HandleWeaveStatEvent(d *WeaveDeviceStat, f WeaveLogFrame, now float64) error {
	switch f.Event {
	case WeaveETStatTaskCPU:
		if len(f.Data) < 1 {
			return fmt.Errorf("weave: ET_STAT_TASK_CPU frame too short: %d bytes", len(f.Data))
		}
		taskID := string(f.Data[1:])
		d.ActiveTasks[taskID] = WeaveTaskStat{CPULoad: f.Data[0], Timestamp: now}
	case WeaveETStatCPU:
		if len(f.Data) < 1 {
			return fmt.Errorf("weave: ET_STAT_CPU frame too short: %d bytes", len(f.Data))
		}
		d.CPULoad = f.Data[0]
		d.captureStatsCPU(now)
	case WeaveETStatMemory:
		stat, err := DecodeWeaveStatMemory(f.Data)
		if err != nil {
			return err
		}
		d.MemoryFree = stat.Free
		d.MemoryTotal = stat.Total
		d.MemoryUsed = stat.Used
		d.MemoryUsedPct = stat.UsedPct
		d.captureStatsMemory(now)
	}
	return nil
}

// captureStatsCPU records the current cpu_load in the cpu history
// (WeaveInterface.py:636-638). The history is capped at WeaveStatLenMax. The
// receiver notification fires only when the memory history has more than one
// sample — a Python quirk faithfully reproduced here.
func (d *WeaveDeviceStat) captureStatsCPU(now float64) {
	d.CPUStats = weaveAppendCapped(d.CPUStats, WeaveCPUSample{Timestamp: now, CPULoad: d.CPULoad})
	if d.OnStatsUpdate != nil && len(d.MemoryStats) > 1 {
		d.OnStatsUpdate("cpu")
	}
}

// captureStatsMemory records the current memory_used in the memory history
// (WeaveInterface.py:640-642). The history is capped at WeaveStatLenMax. The
// receiver notification fires only when the memory history has more than one
// sample.
func (d *WeaveDeviceStat) captureStatsMemory(now float64) {
	d.MemoryStats = weaveAppendCapped(d.MemoryStats, WeaveMemorySample{Timestamp: now, MemoryUsed: d.MemoryUsed})
	if d.OnStatsUpdate != nil && len(d.MemoryStats) > 1 {
		d.OnStatsUpdate("memory")
	}
}

// GetActiveTasks returns the active-task table filtered and described the way
// WeaveDevice.get_active_tasks does (WeaveInterface.py:664-675): tasks whose id
// starts with "IDLE" are dropped, the remaining ids are mapped through
// weaveTaskDescriptions, and only tasks updated within the last 5 seconds
// (now - timestamp < 5) are kept. The returned map is keyed by the (possibly
// remapped) description.
func (d *WeaveDeviceStat) GetActiveTasks(now float64) map[string]WeaveTaskStat {
	out := make(map[string]WeaveTaskStat)
	for taskID, st := range d.ActiveTasks {
		if strings.HasPrefix(taskID, "IDLE") {
			continue
		}
		desc := taskID
		if name, ok := weaveTaskDescriptions[taskID]; ok {
			desc = name
		}
		if now-st.Timestamp < 5 {
			out[desc] = st
		}
	}
	return out
}

// weaveAppendCapped appends v to s, dropping the oldest entry once the slice
// exceeds WeaveStatLenMax, reproducing collections.deque(maxlen=...) semantics
// (WeaveInterface.py:590-591).
func weaveAppendCapped[T any](s []T, v T) []T {
	s = append(s, v)
	if len(s) > WeaveStatLenMax {
		s = s[len(s)-WeaveStatLenMax:]
	}
	return s
}

// WeaveMemoryStat is a decoded WDCL memory stat (WeaveInterface.py:759-764,
// the ET_STAT_MEMORY branch of log_handle). Free and Total are the 4-byte
// big-endian fields; Used is Total-Free; UsedPct is round(Used/Total*100, 2).
type WeaveMemoryStat struct {
	Free    uint32
	Total   uint32
	Used    uint32
	UsedPct float64
}

// DecodeWeaveStatMemory parses the 8-byte ET_STAT_MEMORY payload into a
// WeaveMemoryStat (WeaveInterface.py:759-764). Both fields are 4-byte
// big-endian unsigned integers (binary.BigEndian.Uint32); the used percentage
// is round(Used/Total*100, 2). A zero Total yields 0% (Python would divide by
// zero; the Go decoder is robust instead).
func DecodeWeaveStatMemory(data []byte) (WeaveMemoryStat, error) {
	if len(data) < 8 {
		return WeaveMemoryStat{}, fmt.Errorf("weave: memory stat frame too short: %d bytes", len(data))
	}
	free := binary.BigEndian.Uint32(data[:4])
	total := binary.BigEndian.Uint32(data[4:8])
	used := total - free
	var pct float64
	if total > 0 {
		pct = math.Round((float64(used)/float64(total))*100*100) / 100
	}
	return WeaveMemoryStat{Free: free, Total: total, Used: used, UsedPct: pct}, nil
}
