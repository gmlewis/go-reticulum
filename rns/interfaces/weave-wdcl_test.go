// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"testing"
)

// buildWeaveLogFD builds a WDCL_T_LOG payload (the fd slice Python's
// incoming_frame indexes) for a deterministic event: fd[0]=skip, fd[1..4]=
// timestamp ms (big-endian), fd[5]=level, fd[6..7]=event (big-endian),
// fd[8:]=data. This mirrors the Python frame layout captured as golden in
// Phase 20 task 1.
func buildWeaveLogFD(tsMS uint32, level byte, event uint16, data []byte) []byte {
	fd := make([]byte, 0, 8+len(data))
	fd = append(fd, 0x00) // skip byte (fd[0])
	fd = append(fd, byte(tsMS>>24), byte(tsMS>>16), byte(tsMS>>8), byte(tsMS))
	fd = append(fd, level)
	fd = append(fd, byte(event>>8), byte(event))
	fd = append(fd, data...)
	return fd
}

// TestWeaveEventTableGolden covers Phase 20 task 1: the WDCL event-type
// constants and the descriptions table match the Python Evt table exactly,
// including ET_BOARD_INIT = 0x0003 and its description "Board hardware
// initialization" (WeaveInterface.py:338-451). Each entry is the golden value
// captured from RNS 1.4.2.
func TestWeaveEventTableGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code uint16
		desc string
	}{
		{WeaveETMsg, "ET_MSG"}, // 0x0000 has no description entry
		{WeaveETBoardInit, "Board hardware initialization"},
		{WeaveETSystemBoot, "System boot"},
		{WeaveETCoreInit, "Core initialization"},
		{WeaveETDrvW80211Init, "W802.11 driver initialization"},
		{WeaveETDrvW80211Channel, "W802.11 channel configuration"},
		{WeaveETDrvW80211Power, "W802.11 TX power configuration"},
		{WeaveETProtoWDCLInit, "WDCL protocol initialization"},
		{WeaveETProtoWeaveRunning, "Weave protocol activation"},
		{WeaveETInterfaceRegistered, "Interface registration"},
		{WeaveETSyserrMemExhausted, "System memory exhausted"},
	}
	for _, tc := range cases {
		if tc.code == WeaveETMsg {
			if _, ok := weaveEventDescriptions[tc.code]; ok {
				t.Errorf("ET_MSG 0x0000 should have no description entry")
			}
			continue
		}
		got, ok := weaveEventDescriptions[tc.code]
		if !ok {
			t.Errorf("event 0x%04X missing from weaveEventDescriptions", tc.code)
			continue
		}
		if got != tc.desc {
			t.Errorf("event 0x%04X description = %q, want %q", tc.code, got, tc.desc)
		}
	}
	// ET_BOARD_INIT is the headline constant for this task.
	if WeaveETBoardInit != 0x0003 {
		t.Fatalf("WeaveETBoardInit = 0x%04X, want 0x0003", WeaveETBoardInit)
	}
}

// TestWeaveLogEventDecodeGolden covers Phase 20 task 1: DecodeWeaveLogEvent
// parses a WDCL_T_LOG payload into the exact (timestamp, level, event, data)
// Python extracts (WeaveInterface.py:725-728). The golden fields were captured
// from RNS 1.4.2 with ts=1000ms, level=INFO, event=ET_BOARD_INIT, data=[0x01].
func TestWeaveLogEventDecodeGolden(t *testing.T) {
	t.Parallel()
	fd := buildWeaveLogFD(1000, WeaveLogInfo, WeaveETBoardInit, []byte{0x01})
	f, err := DecodeWeaveLogEvent(fd)
	if err != nil {
		t.Fatalf("DecodeWeaveLogEvent: %v", err)
	}
	if f.Timestamp != 1.0 {
		t.Errorf("Timestamp = %v, want 1.0", f.Timestamp)
	}
	if f.Level != WeaveLogInfo {
		t.Errorf("Level = %d, want %d (INFO)", f.Level, WeaveLogInfo)
	}
	if f.Event != WeaveETBoardInit {
		t.Errorf("Event = 0x%04X, want 0x%04X (BOARD_INIT)", f.Event, WeaveETBoardInit)
	}
	if string(f.Data) != string([]byte{0x01}) {
		t.Errorf("Data = %v, want [0x01]", f.Data)
	}
}

// TestWeaveRenderBoardInitGolden covers Phase 20 task 1: a captured 0x0003
// (ET_BOARD_INIT) frame renders to the exact string Python's log_handle
// produces (WeaveInterface.py:759-804). The golden strings are the RNS 1.4.2
// output captured via a throwaway Python script, with the per-interface
// "weave0: " RNS.log prefix stripped (that prefix is added by the caller, not
// the decoder). ET_BOARD_INIT falls in the core-init..proto-weave-running
// range, so data[0] 0x01 -> ": Success", 0x00+ERROR -> ": Failure",
// 0x00+other -> ": Stopped", any other byte -> ": {hexrep}".
func TestWeaveRenderBoardInitGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		level byte
		data  []byte
		want  string
	}{
		{"success", WeaveLogInfo, []byte{0x01},
			"[1.0s] [Info] [Board hardware initialization]: Success"},
		{"failure", WeaveLogError, []byte{0x00},
			"[1.0s] [Error] [Board hardware initialization]: Failure"},
		{"stopped", WeaveLogInfo, []byte{0x00},
			"[1.0s] [Info] [Board hardware initialization]: Stopped"},
		{"other_byte", WeaveLogInfo, []byte{0x02},
			"[1.0s] [Info] [Board hardware initialization]: 02"},
	}
	for _, tc := range cases {
		fd := buildWeaveLogFD(1000, tc.level, WeaveETBoardInit, tc.data)
		f, err := DecodeWeaveLogEvent(fd)
		if err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		got := RenderWeaveLogEvent(f)
		if got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestWeaveRenderUnknownAndMsgGolden covers Phase 20 task 1: an event code not
// in the descriptions table renders as "0x{hexrep}" with the data hex-string,
// and ET_MSG renders as a bare message with no description bracket. Golden
// values captured from RNS 1.4.2.
func TestWeaveRenderUnknownAndMsgGolden(t *testing.T) {
	t.Parallel()
	// Unknown event 0xBEEF, data [0xAB,0xCD].
	fd := buildWeaveLogFD(1000, WeaveLogInfo, 0xBEEF, []byte{0xAB, 0xCD})
	f, err := DecodeWeaveLogEvent(fd)
	if err != nil {
		t.Fatalf("unknown: decode: %v", err)
	}
	want := "[1.0s] [Info] [0xbeef]: ab:cd"
	if got := RenderWeaveLogEvent(f); got != want {
		t.Errorf("unknown:\n got %q\nwant %q", got, want)
	}

	// ET_MSG with UTF-8 data.
	fd = buildWeaveLogFD(1000, WeaveLogInfo, WeaveETMsg, []byte("hello world"))
	f, err = DecodeWeaveLogEvent(fd)
	if err != nil {
		t.Fatalf("et_msg: decode: %v", err)
	}
	want = "[1.0s] [Info]: hello world"
	if got := RenderWeaveLogEvent(f); got != want {
		t.Errorf("et_msg:\n got %q\nwant %q", got, want)
	}
}

// TestWeavePrettytimeGolden covers Phase 20 task 1: weavePrettytime matches
// RNS.prettytime (non-verbose) for the values the decoder exercises. Golden
// strings captured from RNS 1.4.2.
func TestWeavePrettytimeGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		secs float64
		want string
	}{
		{0.0, "0s"},
		{1.0, "1.0s"},
		{1.5, "1.5s"},
		{59.0, "59.0s"},
		{60.0, "1m"},
		{61.0, "1m and 1.0s"},
		{125.5, "2m and 5.5s"},
		{3600.0, "1h"},
		{3661.0, "1h, 1m and 1.0s"},
		{1000.0, "16m and 40.0s"},
		{86400.0, "1d"},
		{900061.0, "10d, 10h, 1m and 1.0s"},
	}
	for _, tc := range cases {
		if got := weavePrettytime(tc.secs); got != tc.want {
			t.Errorf("weavePrettytime(%v) = %q, want %q", tc.secs, got, tc.want)
		}
	}
}

// TestWeaveHexrepGolden covers Phase 20 task 1: weaveHexrep matches RNS.hexrep
// (lowercase, colon-separated when delimiting). Golden values captured from
// RNS 1.4.2.
func TestWeaveHexrepGolden(t *testing.T) {
	t.Parallel()
	if got := weaveHexrep([]byte{0xAB, 0xCD}, true); got != "ab:cd" {
		t.Errorf("hexrep delimit=true: got %q want %q", got, "ab:cd")
	}
	if got := weaveHexrep([]byte{0xAB, 0xCD}, false); got != "abcd" {
		t.Errorf("hexrep delimit=false: got %q want %q", got, "abcd")
	}
	if got := weaveHexrep([]byte{0x02}, false); got != "02" {
		t.Errorf("hexrep single delimit=false: got %q want %q", got, "02")
	}
	// Unknown-event description uses the 16-bit event as undelimited hex.
	if got := weaveHexrepUint(0xBEEF, false); got != "beef" {
		t.Errorf("hexrepUint(0xBEEF,false): got %q want %q", got, "beef")
	}
}

// TestWeaveStatMemoryDecodeGolden covers Phase 20 task 2: DecodeWeaveStatMemory
// parses an 8-byte ET_STAT_MEMORY frame into free/total/used/used_pct exactly
// as Python's log_handle ET_STAT_MEMORY branch
// (WeaveInterface.py:759-764: int.from_bytes(data[:4],"big"),
// int.from_bytes(data[4:],"big"), used=total-free,
// used_pct=round(used/total*100, 2)). The golden (free, total, used, used_pct)
// tuples were captured from RNS 1.4.2 with the listed 8-byte frames.
func TestWeaveStatMemoryDecodeGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		free     uint32
		total    uint32
		wantFree uint32
		wantUsed uint32
		wantPct  float64
	}{
		{"half", 0x00000064, 0x000000C8, 100, 100, 50.0},
		{"frac", 0x00001234, 0x00005678, 4660, 17476, 78.95},
		{"third", 0x00000001, 0x00000003, 1, 2, 66.67},
		{"full", 0x00000000, 0x000186A0, 0, 100000, 100.0},
	}
	for _, tc := range cases {
		data := []byte{
			byte(tc.free >> 24), byte(tc.free >> 16), byte(tc.free >> 8), byte(tc.free),
			byte(tc.total >> 24), byte(tc.total >> 16), byte(tc.total >> 8), byte(tc.total),
		}
		stat, err := DecodeWeaveStatMemory(data)
		if err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		if stat.Free != tc.wantFree {
			t.Errorf("%s: Free = %d, want %d", tc.name, stat.Free, tc.wantFree)
		}
		if stat.Total != tc.total {
			t.Errorf("%s: Total = %d, want %d", tc.name, stat.Total, tc.total)
		}
		if stat.Used != tc.wantUsed {
			t.Errorf("%s: Used = %d, want %d", tc.name, stat.Used, tc.wantUsed)
		}
		if stat.UsedPct != tc.wantPct {
			t.Errorf("%s: UsedPct = %v, want %v", tc.name, stat.UsedPct, tc.wantPct)
		}
	}
}

// TestWeaveStatDispatchGolden covers Phase 20 task 3: HandleWeaveStatEvent
// applies the ET_STAT_CPU / ET_STAT_TASK_CPU / ET_STAT_MEMORY side-effects to a
// WeaveDeviceStat exactly as Python's log_handle stat branch does
// (WeaveInterface.py:755-764). Golden (cpu_load, active_tasks, memory counters,
// sample histories) captured from RNS 1.4.2 with a throwaway Python script:
//
//	ET_STAT_CPU [0x42]              -> cpu_load=66, cpu_stats=[{1000.0,66}]
//	ET_STAT_TASK_CPU [0x37]core     -> active_tasks["core"]={cpu_load:55,ts:1000}
//	ET_STAT_TASK_CPU [0x10]IDLE0    -> stored in active_tasks (GetActiveTasks filters)
//	ET_STAT_MEMORY free=100,total=200 -> free=100,total=200,used=100,pct=50.0,
//	                                     memory_stats=[{1000.0,100}]
func TestWeaveStatDispatchGolden(t *testing.T) {
	t.Parallel()
	const now = 1000.0
	d := NewWeaveDeviceStat()

	// ET_STAT_CPU: data[0] = 0x42 = 66.
	fd := buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatCPU, []byte{0x42})
	f, err := DecodeWeaveLogEvent(fd)
	if err != nil {
		t.Fatalf("cpu: decode: %v", err)
	}
	if err := HandleWeaveStatEvent(d, f, now); err != nil {
		t.Fatalf("cpu: handle: %v", err)
	}
	if d.CPULoad != 66 {
		t.Errorf("CPULoad = %d, want 66", d.CPULoad)
	}
	if len(d.CPUStats) != 1 {
		t.Fatalf("CPUStats len = %d, want 1", len(d.CPUStats))
	}
	if d.CPUStats[0].CPULoad != 66 || d.CPUStats[0].Timestamp != now {
		t.Errorf("CPUStats[0] = {%v,%v}, want {66,%v}", d.CPUStats[0].CPULoad, d.CPUStats[0].Timestamp, now)
	}

	// ET_STAT_TASK_CPU: data[0]=0x37=55 cpu_load, data[1:]="core" task id.
	fd = buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatTaskCPU, append([]byte{0x37}, []byte("core")...))
	f, err = DecodeWeaveLogEvent(fd)
	if err != nil {
		t.Fatalf("task: decode: %v", err)
	}
	if err := HandleWeaveStatEvent(d, f, now); err != nil {
		t.Fatalf("task: handle: %v", err)
	}
	core, ok := d.ActiveTasks["core"]
	if !ok {
		t.Fatalf("active_tasks missing \"core\"; have %v", d.ActiveTasks)
	}
	if core.CPULoad != 55 || core.Timestamp != now {
		t.Errorf("active_tasks[core] = {%v,%v}, want {55,%v}", core.CPULoad, core.Timestamp, now)
	}

	// ET_STAT_TASK_CPU for "IDLE0": stored raw (GetActiveTasks filters it).
	fd = buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatTaskCPU, append([]byte{0x10}, []byte("IDLE0")...))
	f, _ = DecodeWeaveLogEvent(fd)
	if err := HandleWeaveStatEvent(d, f, now); err != nil {
		t.Fatalf("idle: handle: %v", err)
	}
	if _, ok := d.ActiveTasks["IDLE0"]; !ok {
		t.Errorf("active_tasks missing \"IDLE0\" (raw map should keep it); have %v", d.ActiveTasks)
	}

	// ET_STAT_MEMORY: free=100 (0x64), total=200 (0xC8), 4-byte big-endian each.
	memData := []byte{0x00, 0x00, 0x00, 0x64, 0x00, 0x00, 0x00, 0xC8}
	fd = buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatMemory, memData)
	f, _ = DecodeWeaveLogEvent(fd)
	if err := HandleWeaveStatEvent(d, f, now); err != nil {
		t.Fatalf("memory: handle: %v", err)
	}
	if d.MemoryFree != 100 {
		t.Errorf("MemoryFree = %d, want 100", d.MemoryFree)
	}
	if d.MemoryTotal != 200 {
		t.Errorf("MemoryTotal = %d, want 200", d.MemoryTotal)
	}
	if d.MemoryUsed != 100 {
		t.Errorf("MemoryUsed = %d, want 100", d.MemoryUsed)
	}
	if d.MemoryUsedPct != 50.0 {
		t.Errorf("MemoryUsedPct = %v, want 50.0", d.MemoryUsedPct)
	}
	if len(d.MemoryStats) != 1 || d.MemoryStats[0].MemoryUsed != 100 || d.MemoryStats[0].Timestamp != now {
		t.Errorf("MemoryStats = %v, want [{%v,100}]", d.MemoryStats, now)
	}
}

// TestWeaveStatCapGolden covers Phase 20 task 3: the cpu/memory sample
// histories are capped at WeaveStatLenMax=120, reproducing
// collections.deque(maxlen=120) (WeaveInterface.py:590-591, 636-642). Feeding
// 126 cpu frames leaves 120 samples with the 6 oldest dropped: the golden's
// first remaining sample has cpu_load=5 (the initial 0x42 plus i=0..4 are
// dropped, so i=5 is the oldest kept).
func TestWeaveStatCapGolden(t *testing.T) {
	t.Parallel()
	const now = 1000.0
	d := NewWeaveDeviceStat()

	// Initial cpu frame with data 0x42, then 125 frames with data = i (0..124).
	fd := buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatCPU, []byte{0x42})
	f, _ := DecodeWeaveLogEvent(fd)
	_ = HandleWeaveStatEvent(d, f, now)
	for i := range 125 {
		fd := buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatCPU, []byte{byte(i)})
		f, _ := DecodeWeaveLogEvent(fd)
		_ = HandleWeaveStatEvent(d, f, now)
	}

	if len(d.CPUStats) != WeaveStatLenMax {
		t.Fatalf("CPUStats len = %d, want %d", len(d.CPUStats), WeaveStatLenMax)
	}
	// 126 total (0x42, 0..124); drop 6 oldest (0x42,0,1,2,3,4); first kept = i=5.
	if d.CPUStats[0].CPULoad != 5 {
		t.Errorf("CPUStats[0].CPULoad = %d, want 5 (oldest after cap)", d.CPUStats[0].CPULoad)
	}
	// The newest is the last fed: i=124.
	if d.CPUStats[len(d.CPUStats)-1].CPULoad != 124 {
		t.Errorf("CPUStats[-1].CPULoad = %d, want 124 (newest)", d.CPUStats[len(d.CPUStats)-1].CPULoad)
	}
}

// TestWeaveGetActiveTasksGolden covers Phase 20 task 3: GetActiveTasks filters
// and remaps the raw active_tasks table the way WeaveDevice.get_active_tasks
// does (WeaveInterface.py:664-675). "IDLE"-prefixed ids are dropped; remaining
// ids are remapped through weaveTaskDescriptions ("core" -> "System: Core");
// only tasks updated within the last 5 seconds (now - ts < 5) are kept, so a
// 10-second-old "protocol_wdcl" entry is excluded even though it is not IDLE.
func TestWeaveGetActiveTasksGolden(t *testing.T) {
	t.Parallel()
	d := NewWeaveDeviceStat()
	const now = 1000.0

	// "core" at now=1000 (recent).
	fd := buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatTaskCPU, append([]byte{0x37}, []byte("core")...))
	f, _ := DecodeWeaveLogEvent(fd)
	_ = HandleWeaveStatEvent(d, f, now)

	// "IDLE0" at now=1000 (recent, but IDLE-prefixed -> dropped).
	fd = buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatTaskCPU, append([]byte{0x10}, []byte("IDLE0")...))
	f, _ = DecodeWeaveLogEvent(fd)
	_ = HandleWeaveStatEvent(d, f, now)

	// "protocol_wdcl" at now=990 (stale: now-ts=10 >= 5 -> dropped).
	fd = buildWeaveLogFD(990000, WeaveLogInfo, WeaveETStatTaskCPU, append([]byte{0x01}, []byte("protocol_wdcl")...))
	f, _ = DecodeWeaveLogEvent(fd)
	_ = HandleWeaveStatEvent(d, f, 990.0)

	got := d.GetActiveTasks(now)
	if len(got) != 1 {
		t.Fatalf("GetActiveTasks len = %d, want 1; got %v", len(got), got)
	}
	core, ok := got["System: Core"]
	if !ok {
		t.Fatalf("GetActiveTasks missing \"System: Core\"; got %v", got)
	}
	if core.CPULoad != 55 {
		t.Errorf("GetActiveTasks[\"System: Core\"].CPULoad = %d, want 55", core.CPULoad)
	}
	if _, ok := got["Protocol: WDCL"]; ok {
		t.Errorf("stale protocol_wdcl should have been dropped; got %v", got)
	}
	if _, ok := got["IDLE0"]; ok {
		t.Errorf("IDLE0 should have been dropped; got %v", got)
	}
}

// TestWeaveStatUpdateCallbackGolden covers Phase 20 task 3: the OnStatsUpdate
// receiver notification fires under the Python guard condition
// `len(memory_stats) > 1` (WeaveInterface.py:638, 642) — for BOTH cpu and
// memory captures. So the first memory capture does NOT fire (history len 1),
// the second memory capture fires ("memory"), and a subsequent cpu capture
// then fires ("cpu") because the memory history now has 2 samples. Golden
// sequence captured from RNS 1.4.2: ["memory", "cpu"].
func TestWeaveStatUpdateCallbackGolden(t *testing.T) {
	t.Parallel()
	d := NewWeaveDeviceStat()
	const now = 1000.0
	var calls []string
	d.OnStatsUpdate = func(kind string) { calls = append(calls, kind) }

	memData := []byte{0x00, 0x00, 0x00, 0x64, 0x00, 0x00, 0x00, 0xC8}

	// 1st memory: history len 1 -> no callback.
	fd := buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatMemory, memData)
	f, _ := DecodeWeaveLogEvent(fd)
	_ = HandleWeaveStatEvent(d, f, now)

	// 2nd memory: history len 2 -> "memory".
	f, _ = DecodeWeaveLogEvent(buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatMemory, memData))
	_ = HandleWeaveStatEvent(d, f, now)

	// cpu capture: memory history len 2 -> "cpu".
	f, _ = DecodeWeaveLogEvent(buildWeaveLogFD(1000, WeaveLogInfo, WeaveETStatCPU, []byte{0x55}))
	_ = HandleWeaveStatEvent(d, f, now)

	want := []string{"memory", "cpu"}
	if len(calls) != len(want) {
		t.Fatalf("OnStatsUpdate calls = %v, want %v", calls, want)
	}
	for i, c := range calls {
		if c != want[i] {
			t.Errorf("OnStatsUpdate[%d] = %q, want %q", i, c, want[i])
		}
	}
}

// TestWeaveStatMemoryShortFrame covers Phase 20 task 2: a frame shorter than
// the 8-byte memory stat is rejected rather than panicking on a slice bounds.
func TestWeaveStatMemoryShortFrame(t *testing.T) {
	t.Parallel()
	if _, err := DecodeWeaveStatMemory([]byte{0x00, 0x01, 0x02, 0x03}); err == nil {
		t.Fatal("DecodeWeaveStatMemory accepted a 4-byte frame, want error")
	}
}
