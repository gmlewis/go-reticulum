// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

func renderPathTable(paths []rns.PathInfo, maxHops int, jsonOut bool, destinationFilter []byte) (string, error) {
	filtered := filterAndSortPathTable(paths, maxHops, destinationFilter)
	if jsonOut {
		return renderPathTableJSON(filtered)
	}
	return renderPathTableText(filtered), nil
}

func filterAndSortPathTable(paths []rns.PathInfo, maxHops int, destinationFilter []byte) []rns.PathInfo {
	filtered := make([]rns.PathInfo, 0, len(paths))
	for _, path := range paths {
		if maxHops > 0 && path.Hops > maxHops {
			continue
		}
		if len(destinationFilter) > 0 && string(path.Hash) != string(destinationFilter) {
			continue
		}
		filtered = append(filtered, path)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left := filtered[i]
		right := filtered[j]
		ln := rns.InterfaceString(left.Interface)
		rn := rns.InterfaceString(right.Interface)
		if ln != rn {
			return ln < rn
		}
		if left.Hops != right.Hops {
			return left.Hops < right.Hops
		}
		return false
	})
	return filtered
}

func renderPathTableText(paths []rns.PathInfo) string {
	var builder strings.Builder
	for _, path := range paths {
		// Match Python rnpath -t (Utilities/rnpath.py:287-289):
		//   "<hash> is N hop[m_str] away via <hex> on <iface> expires <ts>"
		// m_str is " " for 1 hop, "s" otherwise (note: the table view uses a
		// space for the singular, unlike the request view which uses "").
		plural := "s"
		if path.Hops == 1 {
			plural = " "
		}
		builder.WriteString(fmt.Sprintf("<%x> is %v hop%v away via <%x> on %v expires %v\n", path.Hash, path.Hops, plural, path.NextHop, rns.InterfaceString(path.Interface), path.Expires.Format("2006-01-02 15:04:05")))
	}
	return builder.String()
}

type pathTableJSONEntry struct {
	Hash      string  `json:"hash"`
	Timestamp float64 `json:"timestamp"`
	Via       string  `json:"via"`
	Hops      int     `json:"hops"`
	Expires   float64 `json:"expires"`
	Interface string  `json:"interface"`
}

func renderPathTableJSON(paths []rns.PathInfo) (string, error) {
	entries := make([]pathTableJSONEntry, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, pathTableJSONEntry{
			Hash:      fmt.Sprintf("%x", path.Hash),
			Timestamp: float64(path.Timestamp.UnixNano()) / 1e9,
			Via:       fmt.Sprintf("%x", path.NextHop),
			Hops:      path.Hops,
			Expires:   float64(path.Expires.UnixNano()) / 1e9,
			Interface: rns.InterfaceString(path.Interface),
		})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type pathTableTestInterface struct {
	name string
}

func (i pathTableTestInterface) Name() string                { return i.name }
func (i pathTableTestInterface) Type() string                { return "test" }
func (i pathTableTestInterface) Status() bool                { return true }
func (i pathTableTestInterface) IsOut() bool                 { return false }
func (i pathTableTestInterface) Mode() int                   { return interfaces.ModeFull }
func (i pathTableTestInterface) Bitrate() int                { return 0 }
func (i pathTableTestInterface) Send([]byte) error           { return nil }
func (i pathTableTestInterface) BytesReceived() uint64       { return 0 }
func (i pathTableTestInterface) BytesSent() uint64           { return 0 }
func (i pathTableTestInterface) Detach() error               { return nil }
func (i pathTableTestInterface) IsDetached() bool            { return false }
func (i pathTableTestInterface) Age() time.Duration          { return 0 }
func (i pathTableTestInterface) Gravity() int                { return 0 }
func (i pathTableTestInterface) RecursivePrs() bool          { return false }
func (i pathTableTestInterface) AnnouncesFromInternal() bool { return true }
func (i pathTableTestInterface) AnnouncesToInternal() *bool  { return nil }
func (i pathTableTestInterface) ReceivedAnnounce()           {}
func (i pathTableTestInterface) ShouldIngressLimit() bool    { return false }
func (i pathTableTestInterface) HoldAnnounce([]byte, interfaces.Interface, int, []byte) {
}
func (i pathTableTestInterface) ProcessHeldAnnounces() ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (i pathTableTestInterface) HeldAnnounces() int { return 0 }
func (i pathTableTestInterface) ReleaseHeldAnnounce([]byte) ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (i pathTableTestInterface) ReceivedPathRequest()               {}
func (i pathTableTestInterface) SentPathRequest()                   {}
func (i pathTableTestInterface) IncomingPrFrequency() float64       { return 0 }
func (i pathTableTestInterface) OutgoingPrFrequency() float64       { return 0 }
func (i pathTableTestInterface) ShouldIngressLimitPr() bool         { return false }
func (i pathTableTestInterface) ShouldEgressLimitPr() bool          { return false }
func (i pathTableTestInterface) AnnounceRateTarget() *int           { return nil }
func (i pathTableTestInterface) AnnounceRateGrace() *int            { return nil }
func (i pathTableTestInterface) AnnounceRatePenalty() *int          { return nil }
func (i pathTableTestInterface) IncomingAnnounceFrequency() float64 { return 0 }
func (i pathTableTestInterface) OutgoingAnnounceFrequency() float64 { return 0 }
func (i pathTableTestInterface) ICBurstActive() bool                { return false }
func (i pathTableTestInterface) ICBurstActivated() time.Time        { return time.Time{} }
func (i pathTableTestInterface) ICPrBurstActive() bool              { return false }
func (i pathTableTestInterface) ICPrBurstActivated() time.Time      { return time.Time{} }
