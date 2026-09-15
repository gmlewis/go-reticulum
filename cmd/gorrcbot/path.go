// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the path command: "can I reach this peer, and how?". An RRC
// peer's identity hash is not a destination — an identity owns destinations, and
// only a destination has a path — so the command derives the destinations that
// identity publishes (lxmf.delivery, nomadnetwork.node, and rrc.hub for a hub)
// and reports the transport's path to each.
//
// Path lookups are deliberately non-blocking: the command layer runs on the
// engine's single dispatcher goroutine, so a cold lookup fires one path request
// and answers from the table as it stands, telling the asker to try again. The
// transport's own "unknown hops" sentinel is never printed as a hop count.

package main

import (
	"fmt"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

const (
	// pathUsage is the usage line for the path command.
	pathUsage = "path <nick|hash|name>"
	// pathUnavailableLine is the answer when the bot has no transport at all,
	// which is the case in a unit-test wiring and would be a misconfiguration
	// in production.
	pathUnavailableLine = "path lookups are unavailable: this bot has no Reticulum transport"
	// pathAskedLine is the answer to a destination with no known path. It is
	// honest about having asked rather than implying the peer is unreachable.
	pathAskedLine = "no path known; asked for one — try again in a few seconds"
	// pathNoAnnounceLine is the answer when the transport cannot recall the
	// peer's identity: without it there is no destination hash to look up.
	pathNoAnnounceLine = "no announce has been seen for %v, so I cannot derive their destinations"
	// pathUnknownHops is the wording for a path whose hop count the transport
	// does not know (rns.PathfinderM).
	pathUnknownHops = "an unknown number of hops"
)

// pathLookup is the slice of the Reticulum transport the path command needs. It
// is an interface so the command layer is testable without a Reticulum stack.
type pathLookup interface {
	// GetPathEntry returns the current path-table entry for a destination, or
	// nil when the transport knows no path to it.
	GetPathEntry(destHash []byte) *rns.PathInfo
	// RequestPath asks the network for a path to a destination.
	RequestPath(destHash []byte) error
	// PathCount reports how many destinations the path table holds.
	PathCount() int
	// Recall returns the identity with this identity hash, or nil when the
	// transport has never seen it announce.
	Recall(identityHash []byte) *rns.Identity
}

// livePathLookup adapts a running Reticulum transport to pathLookup.
type livePathLookup struct{ ts *rns.TransportSystem }

// GetPathEntry implements pathLookup.
func (l livePathLookup) GetPathEntry(destHash []byte) *rns.PathInfo {
	return l.ts.GetPathEntry(destHash)
}

// RequestPath implements pathLookup.
func (l livePathLookup) RequestPath(destHash []byte) error {
	return l.ts.RequestPath(destHash)
}

// PathCount implements pathLookup. The transport exposes the table as a snapshot,
// so its length is the count.
func (l livePathLookup) PathCount() int { return len(l.ts.GetPathTable()) }

// Recall implements pathLookup.
func (l livePathLookup) Recall(identityHash []byte) *rns.Identity {
	return rns.RecallIdentity(l.ts, identityHash)
}

// peerDestination is one destination an identity publishes.
type peerDestination struct {
	// name is the destination name, as an announce would print it.
	name string
	// app is the RNS application name.
	app string
	// aspects are the RNS aspects that follow the application name.
	aspects []string
	// hubOnly restricts this destination to the RRC hub identity: only a hub
	// serves rrc.hub, so reporting it for an ordinary peer would be noise.
	hubOnly bool
}

// peerDestinations are the destinations this command looks up, in the order it
// reports them. The names are the real, hyphenless RNS destination names; a
// filter or name with a wrong separator silently matches nothing.
var peerDestinations = []peerDestination{
	{name: "lxmf.delivery", app: "lxmf", aspects: []string{"delivery"}},
	{name: "nomadnetwork.node", app: "nomadnetwork", aspects: []string{"node"}},
	{name: "rrc.hub", app: "rrc", aspects: []string{"hub"}, hubOnly: true},
}

// runPath reports the transport's path to every destination the named peer
// publishes, asking the network for the ones it does not know yet.
func (c *commandContext) runPath() []string {
	if c.reg.paths == nil {
		return []string{pathUnavailableLine}
	}
	if c.Args == "" {
		return []string{
			"Usage: " + pathUsage,
			fmt.Sprintf("the path table holds %v", pluralCount(c.reg.paths.PathCount(), "destination", "destinations")),
		}
	}
	subject, rejected := c.pathSubject(c.Args)
	if rejected != nil {
		return rejected
	}
	identity := c.reg.paths.Recall(subject.hash)
	label := safeEcho(subject.label, maxEchoNickBytes)
	if identity == nil {
		return []string{fmt.Sprintf(pathNoAnnounceLine, label)}
	}

	destinations := c.pathDestinations(identity)
	lines := make([]string, 0, len(destinations)+1)
	lines = append(lines, fmt.Sprintf("path %v (%v): %v", label, shortHash(hexString(subject.hash)),
		pluralCount(len(destinations), "destination", "destinations")))
	for _, dest := range destinations {
		lines = append(lines, c.pathLine(dest, identity))
	}
	return lines
}

// pathSubject is the peer a path question is about: the identity hash whose
// destinations are looked up, and the name the answer uses for it.
type pathSubject struct {
	// hash is the identity hash to recall and derive destinations from.
	hash []byte
	// label is the nick or announced name the answer prints.
	label string
}

// pathSubject resolves a path command's token. The hub knows the peers it has
// shown this client; the announce cache knows the peers that announced on any
// aspect, which is the population a path question is usually about, so a token the
// hub cannot resolve is looked up there before it is refused.
func (c *commandContext) pathSubject(token string) (pathSubject, []string) {
	target, rejected := c.resolveTarget(token)
	if rejected == nil {
		return pathSubject{hash: target.Hash, label: pathTargetLabel(target)}, nil
	}
	announced, ok := c.reg.announces.lookup(token)
	if !ok || announced.IdentityHex == "" {
		// An announce without an identity hash names a destination, and a
		// destination is not something this command can derive paths from.
		return pathSubject{}, rejected
	}
	identityHash, err := hexToBytes(announced.IdentityHex)
	if err != nil {
		return pathSubject{}, rejected
	}
	label := announced.Name
	if label == "" {
		label = shortHash(announced.IdentityHex)
	}
	return pathSubject{hash: identityHash, label: label}, nil
}

// pathDestinations returns the destinations to report for one identity, dropping
// the hub-only one unless this identity really is the hub.
func (c *commandContext) pathDestinations(identity *rns.Identity) []peerDestination {
	// An identity always has a hash, but the hub's own hash is empty until its
	// WELCOME arrives: comparing two empty strings would hand every peer the
	// hub's rrc.hub destination.
	hubHash := c.conn().HubIdentityHash()
	isHub := len(hubHash) > 0 && hexString(identity.Hash) == hexString(hubHash)
	out := make([]peerDestination, 0, len(peerDestinations))
	for _, dest := range peerDestinations {
		if dest.hubOnly && !isHub {
			continue
		}
		out = append(out, dest)
	}
	return out
}

// pathLine reports one destination: its hash, the path the transport holds, and
// the age of that path. A destination with no path is reported honestly after one
// path request has been fired for it.
func (c *commandContext) pathLine(dest peerDestination, identity *rns.Identity) string {
	destHash := rns.CalculateHash(identity, dest.app, dest.aspects...)
	label := fmt.Sprintf("%v (%v)", dest.name, shortHash(hexString(destHash)))
	entry := c.reg.paths.GetPathEntry(destHash)
	if entry == nil {
		if err := c.reg.paths.RequestPath(destHash); err != nil && c.reg.bot != nil {
			// The transport logs the failure itself; the answer still says no
			// path is known, which is what is true from here.
			c.reg.bot.logf("path request for %v failed: %v", dest.name, err)
		}
		return label + ": " + pathAskedLine
	}
	now := c.req.Now
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("%v: %v via %v on %v, learned %v ago, %v", label,
		pathHops(entry.Hops), pathNextHop(entry.NextHop), rns.InterfaceString(entry.Interface),
		formatAge(now.Sub(entry.Timestamp)), formatHorizon(entry.Expires.Sub(now)))
}

// pathHops renders a hop count, never printing the transport's unknown sentinel
// as a number: PathfinderM means "unknown", not 128 hops.
func pathHops(hops int) string {
	if hops <= 0 || hops >= rns.PathfinderM {
		return pathUnknownHops
	}
	return pluralCount(hops, "hop", "hops")
}

// pathNextHop renders the hash of the peer a path goes through.
func pathNextHop(nextHop []byte) string {
	if len(nextHop) == 0 {
		return "an unknown next hop"
	}
	return shortHash(hexString(nextHop))
}

// pathTargetLabel is the name a path answer uses for the peer that was asked
// about: the nick the hub knows, else the token the asker typed.
func pathTargetLabel(target rrc.PeerTarget) string {
	if label := normalizeNick(target.Nick); label != "" {
		return label
	}
	return shortHash(target.HashHex)
}
