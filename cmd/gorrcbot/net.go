// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the mesh directory: what the bot has actually heard on the
// network, and how far away each service is. It answers the question an
// operator asks before trying anything — is there a hub, a NomadNet node, or an
// LXMF propagation node within reach, and how many hops away is it?
//
// The answer comes from two places the bot already keeps: the announce cache,
// which remembers every announce it has heard, and the transport's path table,
// which knows the hop count and the interface to each destination. Nothing here
// reaches the network by itself, so the command is as fast as a table read and
// as safe to run as any other.
//
// A service the bot has heard but has no path to is reported as such rather
// than hidden, because "announced but unreachable" is exactly the state an
// operator needs to see.

package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// Mesh-directory wordings and bounds.
const (
	// netUsage is the usage line for the command.
	netUsage = "net [hops] [near <loc>]"
	// netUnavailableLine is the answer when the bot has no transport at all,
	// which is a unit-test wiring or a misconfiguration.
	netUnavailableLine = "mesh discovery is unavailable: this bot has no Reticulum transport"
	// netNoServicesLine is the answer when nothing has been heard.
	netNoServicesLine = "no mesh services heard yet"
	// netNoServicesInRangeLine is the answer when everything heard is further
	// than the hop limit.
	netNoServicesInRangeLine = "no mesh services heard within %v"
	// netHeader is the header of the directory.
	netHeader = "Known mesh services (within %v):"
	// netRow is one directory entry.
	netRow = "[%v] %v (hash %v) via %v"
	// netRowNoPath is one directory entry for a service with no path.
	netRowNoPath = "[no path] %v (hash %v)"
	// netDefaultHops is how far the directory reaches when the asker does not
	// say. Three hops is the usual extent of a mesh that still has usable
	// latency.
	netDefaultHops = 3
	// netMaxHops bounds the search, since every hop costs latency.
	netMaxHops = 15
	// netNearUnsupportedLine explains the near filter, which cannot be
	// implemented honestly: an RNS announce carries a name and a public key,
	// never a position.
	netNearUnsupportedLine = "an RNS announce carries no position, so near cannot filter the mesh directory; sitrep near answers what is close by"
)

// netServiceLabel names one announced destination the way an operator thinks of
// it. The aspect is the real RNS destination name, so a typo in this table
// would mislabel a service that really announced.
func netServiceLabel(entry announce) string {
	name := safeEcho(entry.Name, maxAnnounceNameBytes)
	switch entry.Aspect {
	case "rrc.hub":
		return namedService("Hub", name)
	case "lxmf.delivery":
		return "LXMF Delivery"
	case "lxmf.propagation":
		return "LXMF Propagation Node"
	case "nomadnetwork.node":
		return namedService("NomadNet Node", name)
	default:
		if name != "" {
			return namedService(entry.Aspect, name)
		}
		return entry.Aspect
	}
}

// namedService renders a service with the name it announced, when it announced
// one.
func namedService(kind, name string) string {
	if name == "" {
		return kind
	}
	return fmt.Sprintf("%v %q", kind, name)
}

// netService is one directory entry, after the announce and the path table have
// been merged.
type netService struct {
	// label is how the entry is named.
	label string
	// destHex is the announced destination hash, hex.
	destHex string
	// hops is the path's hop count, zero when there is no path.
	hops int
	// hasPath reports that the transport knows a path to the destination.
	hasPath bool
	// iface is the interface the path leaves by, empty without a path.
	iface string
}

// runNet renders the mesh directory.
func (c *commandContext) runNet() []string {
	if c.reg.paths == nil || c.reg.announces == nil {
		return []string{netUnavailableLine}
	}
	maxHops, near, rejected := parseNetArgs(c.Args)
	if rejected != nil {
		return rejected
	}
	if near != "" {
		return []string{netNearUnsupportedLine}
	}

	services := c.netServices(maxHops)
	if len(services) == 0 {
		if c.reg.announces.size() == 0 {
			return []string{netNoServicesLine}
		}
		return []string{fmt.Sprintf(netNoServicesInRangeLine, pluralCount(maxHops, "hop", "hops"))}
	}
	lines := []string{fmt.Sprintf(netHeader, pluralCount(maxHops, "hop", "hops"))}
	for _, service := range services {
		if !service.hasPath {
			lines = append(lines, fmt.Sprintf(netRowNoPath, service.label, shortHash(service.destHex)))
			continue
		}
		lines = append(lines, fmt.Sprintf(netRow, pathHops(service.hops), service.label,
			shortHash(service.destHex), service.iface))
	}
	return lines
}

// parseNetArgs parses the optional hop limit and the optional near filter.
func parseNetArgs(args string) (int, string, []string) {
	fields := strings.Fields(strings.TrimSpace(args))
	maxHops := netDefaultHops
	if len(fields) > 0 {
		if hops, err := strconv.Atoi(fields[0]); err == nil {
			if hops < 1 || hops > netMaxHops {
				return 0, "", []string{fmt.Sprintf("net: the hop limit must be between 1 and %v", netMaxHops)}
			}
			maxHops = hops
			fields = fields[1:]
		}
	}
	if len(fields) == 0 {
		return maxHops, "", nil
	}
	if !strings.EqualFold(fields[0], "near") {
		return 0, "", []string{"Usage: " + netUsage}
	}
	location := strings.Join(fields[1:], " ")
	if location == "" {
		return 0, "", []string{"Usage: " + netUsage}
	}
	if _, err := ParseLocation(location); err != nil {
		return 0, "", []string{"net: no location found — " + locationNotationHelp}
	}
	return maxHops, location, nil
}

// netServices merges the announce cache with the path table and returns the
// entries within the hop limit, nearest first. A service heard but with no path
// is kept and sorted last, since it is the one an operator may still be able to
// reach by asking for a path.
func (c *commandContext) netServices(maxHops int) []netService {
	now := c.now()
	out := make([]netService, 0, 16)
	for _, entry := range c.reg.announces.snapshot(now) {
		service := netService{label: netServiceLabel(entry), destHex: entry.DestHex}
		if destHash, err := hexToBytes(entry.DestHex); err == nil {
			if path := c.reg.paths.GetPathEntry(destHash); path != nil {
				service.hasPath = true
				service.hops = path.Hops
				service.iface = rns.InterfaceString(path.Interface)
			}
		}
		if service.hasPath && (service.hops <= 0 || service.hops > maxHops) {
			// The transport's unknown-hop sentinel is not a distance, and a
			// path beyond the limit is out of range.
			continue
		}
		out = append(out, service)
	}
	slices.SortFunc(out, func(a, b netService) int {
		if a.hasPath != b.hasPath {
			if !a.hasPath {
				return 1
			}
			return -1
		}
		if a.hops != b.hops {
			return a.hops - b.hops
		}
		return strings.Compare(a.destHex, b.destHex)
	})
	return out
}

// snapshot returns a copy of the announces the cache still considers current.
// The cache owns the table, so the copy is what a command may hold on to after
// the lock is released.
func (c *announceCache) snapshot(now time.Time) []announce {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]announce, 0, len(c.entries))
	for _, entry := range c.entries {
		if !entry.At.IsZero() && now.Sub(entry.At) > announceTTL {
			continue
		}
		out = append(out, entry)
	}
	return out
}
