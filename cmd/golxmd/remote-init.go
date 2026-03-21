// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

var osExit = os.Exit // for unit testing

func (c *clientT) remoteInit(configDirArg string, rnsConfigDir string, verbosity int, quietness int, identityPathArg string) (*rns.Reticulum, error) {
	if identityPathArg == "" {
		resolvedConfigDir := resolveConfigDir(configDirArg)

		c.configpath = filepath.Join(resolvedConfigDir, "config")
		c.identitypath = filepath.Join(resolvedConfigDir, "identity")
		c.identity = nil

		if !isDir(resolvedConfigDir) {
			rns.Logf("Specified configuration directory does not exist, exiting now", rns.LogError, false)
			osExit(201)
			return nil, nil
		}
		if !isFile(c.identitypath) {
			rns.Logf("Identity file not found in specified configuration directory, exiting now", rns.LogError, false)
			osExit(202)
			return nil, nil
		} else {
			var err error
			c.identity, err = rns.FromFile(c.identitypath)
			if err != nil {
				rns.Logf("Could not load the Primary Identity from %v", rns.LogError, false, c.identitypath)
				osExit(4)
				return nil, nil
			}
		}
	} else {
		if !isFile(identityPathArg) {
			rns.Logf("Identity file not found in specified configuration directory, exiting now", rns.LogError, false)
			osExit(202)
			return nil, nil
		} else {
			var err error
			c.identity, err = rns.FromFile(identityPathArg)
			if err != nil {
				rns.Logf("Could not load the Primary Identity from %v", rns.LogError, false, identityPathArg)
				osExit(4)
				return nil, nil
			}
		}
	}

	targetloglevel := -1
	if c.configpath != "" {
		if cfg, err := loadConfig(filepath.Dir(c.configpath)); err == nil && cfg != nil {
			targetloglevel = cfg.LogLevel
		}
	}
	if targetloglevel == -1 {
		targetloglevel = 3
	}
	if verbosity != 0 || quietness != 0 {
		targetloglevel = targetloglevel + verbosity - quietness
	}

	if c.ts == nil {
		c.ts = rns.NewTransportSystem()
	}
	reticulum, err := rns.NewReticulum(c.ts, rnsConfigDir)
	if err != nil {
		rns.Logf("Could not initialize Reticulum, exiting now", rns.LogError, false)
		osExit(1)
		return nil, nil
	}

	rns.SetLogLevel(targetloglevel)
	rns.SetLogDest(rns.LogStdout)

	return reticulum, nil
}

func (c *clientT) getTargetIdentity(remote string, timeoutArg time.Duration) *rns.Identity {
	if remote == "" {
		return c.identity
	}

	destinationHash, err := rns.HexToBytes(remote)
	if err != nil || len(destinationHash) != rns.TruncatedHashLength/8 {
		msg := "Invalid remote destination hash"
		if err != nil {
			msg = fmt.Sprintf("Invalid remote destination hash: %v", err)
		} else {
			msg = fmt.Sprintf("Invalid remote destination hash: Destination hash length must be %v characters", rns.TruncatedHashLength/8*2)
		}
		fmt.Println(msg)
		osExit(203)
		return nil
	}

	if c.ts == nil {
		c.ts = rns.NewTransportSystem()
	}
	remoteIdentity := c.ts.Recall(destinationHash)
	if remoteIdentity != nil {
		return remoteIdentity
	}

	if !c.ts.HasPath(destinationHash) {
		_ = c.ts.RequestPath(destinationHash)
		start := time.Now()
		for !c.ts.HasPath(destinationHash) {
			remoteIdentity = c.ts.Recall(destinationHash)
			if remoteIdentity != nil {
				return remoteIdentity
			}
			if time.Since(start) > timeoutArg {
				fmt.Println("Resolving remote identity timed out, exiting now")
				osExit(200)
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	return c.ts.Recall(destinationHash)
}

const StatsGetPath = "/pn/get/stats"

func (c *clientT) queryStatus(id *rns.Identity, remoteIdentityArg *rns.Identity, timeoutArg time.Duration, exitOnFail bool) (any, error) {
	if remoteIdentityArg == nil {
		remoteIdentityArg = id
	}

	if c.ts == nil {
		c.ts = rns.NewTransportSystem()
	}
	controlDestination, err := rns.NewDestination(c.ts, remoteIdentityArg, rns.DestinationOut, rns.DestinationSingle, "lxmf", "propagation", "control")
	if err != nil {
		return nil, err
	}

	start := time.Now()
	checkTimeout := func() error {
		if time.Since(start) > timeoutArg {
			if exitOnFail {
				fmt.Println("Getting lxmd statistics timed out, exiting now")
				osExit(200)
				return nil
			}
			// In Python, it returns LXMF.LXMPeer.LXMPeer.ERROR_TIMEOUT
			return fmt.Errorf("timeout")
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}

	if !c.ts.HasPath(controlDestination.Hash) {
		_ = c.ts.RequestPath(controlDestination.Hash)
		for !c.ts.HasPath(controlDestination.Hash) {
			if err := checkTimeout(); err != nil {
				return nil, err
			}
		}
	}

	link, err := rns.NewLink(c.ts, controlDestination)
	if err != nil {
		return nil, err
	}

	for link.GetStatus() != rns.LinkActive {
		if err := checkTimeout(); err != nil {
			return nil, err
		}
	}

	if err := link.Identify(id); err != nil {
		return nil, err
	}

	requestReceipt, err := link.Request(StatsGetPath, nil, nil, nil, nil, 0)
	if err != nil {
		return nil, err
	}

	for requestReceipt.GetStatus() != rns.RequestReady {
		if err := checkTimeout(); err != nil {
			return nil, err
		}
	}

	link.Teardown()
	return requestReceipt.Response, nil
}

func (c *clientT) getStatus(remote string, configDirArg string, rnsConfigDir string, verbosity int, quietness int, timeout time.Duration, showStatus bool, showPeers bool, identityPathArg string) {
	reticulum, err := c.remoteInit(configDirArg, rnsConfigDir, verbosity, quietness, identityPathArg)
	if err != nil {
		fmt.Printf("Remote initialization failed: %v\n", err)
		osExit(1)
		return
	}
	defer func() {
		if err := reticulum.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	targetIdentity := c.getTargetIdentity(remote, timeout)
	response, err := c.queryStatus(c.identity, targetIdentity, timeout, true)
	if err != nil {
		fmt.Printf("Query status failed: %v\n", err)
		osExit(1)
		return
	}

	if response == nil {
		fmt.Println("Empty response received")
		osExit(207)
		return
	}

	s, ok := response.(map[string]any)
	if !ok {
		fmt.Println("Invalid response format received")
		osExit(208)
		return
	}

	// Python: mutil = round((s["messagestore"]["bytes"]/s["messagestore"]["limit"])*100, 2)
	ms := s["messagestore"].(map[string]any)
	msBytes := anyToFloat64(ms["bytes"])
	msLimit := anyToFloat64(ms["limit"])
	msUtilStr := "0%"
	if msLimit != 0 {
		mutil := (msBytes / msLimit) * 100
		msUtilStr = formatRound2(mutil) + "%"
	}

	whoStr := "all nodes"
	if s["from_static_only"].(bool) {
		whoStr = "static peers only"
	}

	availablePeers := 0
	unreachablePeers := 0
	peeredIncoming := 0.0
	peeredOutgoing := 0.0
	peeredRxBytes := 0.0
	peeredTxBytes := 0.0

	peers := s["peers"].(map[string]any)
	for _, peerVal := range peers {
		p := peerVal.(map[string]any)
		pm := p["messages"].(map[string]any)
		peeredIncoming += anyToFloat64(pm["incoming"])
		peeredOutgoing += anyToFloat64(pm["outgoing"])
		peeredRxBytes += anyToFloat64(p["rx_bytes"])
		peeredTxBytes += anyToFloat64(p["tx_bytes"])

		if p["alive"].(bool) {
			availablePeers++
		} else {
			unreachablePeers++
		}
	}

	clients := s["clients"].(map[string]any)
	cprr := anyToFloat64(clients["client_propagation_messages_received"])
	cprs := anyToFloat64(clients["client_propagation_messages_served"])
	upi := anyToFloat64(s["unpeered_propagation_incoming"])
	uprx := anyToFloat64(s["unpeered_propagation_rx_bytes"])

	totalIncoming := peeredIncoming + upi + cprr
	totalRxBytes := peeredRxBytes + uprx
	df := 0.0
	if totalIncoming != 0 {
		df = peeredOutgoing / totalIncoming
	}

	dhs := fmt.Sprintf("<%x>", s["destination_hash"].([]byte))
	uts := rns.PrettyTime(anyToFloat64(s["uptime"]), false, false)
	fmt.Printf("\nLXMF Propagation Node running on %v, uptime is %v\n", dhs, uts)

	if showStatus {
		msb := rns.PrettySize(msBytes, "")
		msl := rns.PrettySize(msLimit, "")
		ptl := rns.PrettySize(anyToFloat64(s["propagation_limit"])*1000, "")
		psl := rns.PrettySize(anyToFloat64(s["sync_limit"])*1000, "")
		uprxStr := rns.PrettySize(uprx, "")
		mscnt := anyToFloat64(ms["count"])
		stp := anyToFloat64(s["total_peers"])
		smp := anyToFloat64(s["max_peers"])
		sdp := anyToFloat64(s["discovered_peers"])
		ssp := anyToFloat64(s["static_peers"])
		psc := anyToFloat64(s["target_stamp_cost"])
		scf := anyToFloat64(s["stamp_cost_flexibility"])
		pc := anyToFloat64(s["peering_cost"])
		pcm := anyToFloat64(s["max_peering_cost"])

		fmt.Printf("Messagestore contains %v messages, %v (%v utilised of %v)\n", mscnt, msb, msUtilStr, msl)
		fmt.Printf("Required propagation stamp cost is %v, flexibility is %v\n", psc, scf)
		fmt.Printf("Peering cost is %v, max remote peering cost is %v\n", pc, pcm)
		fmt.Printf("Accepting propagated messages from %v\n", whoStr)
		fmt.Printf("%v message limit, %v sync limit\n", ptl, psl)
		fmt.Printf("\n")
		fmt.Printf("Peers   : %v total (peer limit is %v)\n", stp, smp)
		fmt.Printf("          %v discovered, %v static\n", sdp, ssp)
		fmt.Printf("          %v available, %v unreachable\n", availablePeers, unreachablePeers)
		fmt.Printf("\n")
		fmt.Printf("Traffic : %v messages received in total (%v)\n", totalIncoming, rns.PrettySize(totalRxBytes, ""))
		fmt.Printf("          %v messages received from peered nodes (%v)\n", peeredIncoming, rns.PrettySize(peeredRxBytes, ""))
		fmt.Printf("          %v messages received from unpeered nodes (%v)\n", upi, uprxStr)
		fmt.Printf("          %v messages transferred to peered nodes (%v)\n", peeredOutgoing, rns.PrettySize(peeredTxBytes, ""))
		fmt.Printf("          %v propagation messages received directly from clients\n", cprr)
		fmt.Printf("          %v propagation messages served to clients\n", cprs)
		fmt.Printf("          Distribution factor is %v\n", formatRound2(df))
		fmt.Printf("\n")
	}

	if showPeers {
		if !showStatus {
			fmt.Printf("\n")
		}

		for peerID, peerVal := range peers {
			ind := "  "
			p := peerVal.(map[string]any)
			t := "Unknown peer    "
			if s, ok := p["type"].(string); ok {
				switch s {
				case "static":
					t = "Static peer     "
				case "discovered":
					t = "Discovered peer "
				}
			}

			a := "Unreachable"
			if p["alive"].(bool) {
				a = "Available"
			}

			now := anyToFloat64(s["now"])
			if now == 0 {
				now = float64(time.Now().UnixNano()) / 1e9
			}

			h := now - anyToFloat64(p["last_heard"])
			if h < 0 {
				h = 0
			}

			hops := int(anyToFloat64(p["network_distance"]))
			hs := "hops unknown"
			switch hops {
			case rns.PathfinderM:
				hs = "hops unknown"
			case 1:
				hs = "1 hop away"
			default:
				hs = fmt.Sprintf("%v hops away", hops)
			}

			pm := p["messages"].(map[string]any)
			pk := p["peering_key"]
			psc := p["target_stamp_cost"]
			psf := p["stamp_cost_flexibility"]
			pc := p["peering_cost"]

			pkStr := "Not generated"
			if pk != nil {
				pkStr = fmt.Sprintf("Generated, value is %v", pk)
			}

			ls := "never synced"
			if anyToFloat64(p["last_sync_attempt"]) != 0 {
				lsa := anyToFloat64(p["last_sync_attempt"])
				ls = fmt.Sprintf("last synced %v ago", rns.PrettyTime(now-lsa, false, false))
			}

			sstr := rns.PrettySpeed(anyToFloat64(p["str"]))
			sler := rns.PrettySpeed(anyToFloat64(p["ler"]))

			stl := "Unknown"
			if p["transfer_limit"] != nil {
				stl = rns.PrettySize(anyToFloat64(p["transfer_limit"])*1000, "")
			}
			ssl := "unknown"
			if p["sync_limit"] != nil {
				ssl = rns.PrettySize(anyToFloat64(p["sync_limit"])*1000, "")
			}

			srxb := rns.PrettySize(anyToFloat64(p["rx_bytes"]), "")
			stxb := rns.PrettySize(anyToFloat64(p["tx_bytes"]), "")
			pmo := anyToFloat64(pm["offered"])
			pmout := anyToFloat64(pm["outgoing"])
			pmi := anyToFloat64(pm["incoming"])
			pmuh := anyToFloat64(pm["unhandled"])
			ar := anyToFloat64(p["acceptance_rate"]) * 100

			nn := ""
			if p["name"] != nil {
				nn = p["name"].(string)
				nn = strings.TrimSpace(nn)
				nn = strings.ReplaceAll(nn, "\n", "")
				nn = strings.ReplaceAll(nn, "\r", "")
				if len(nn) > 45 {
					nn = nn[:45] + "..."
				}
			}

			dhs := rns.PrettyHexFromString(peerID)
			fmt.Printf("%v%v%v\n", ind, t, dhs)
			if nn != "" {
				fmt.Printf("%vName       : %v\n", ind+ind, nn)
			}
			fmt.Printf("%vStatus     : %v, %v, last heard %v ago\n", ind+ind, a, hs, rns.PrettyTime(h, false, false))
			fmt.Printf("%vCosts      : Propagation %v (flex %v), peering %v\n", ind+ind, psc, psf, pc)
			fmt.Printf("%vSync key   : %v\n", ind+ind, pkStr)
			fmt.Printf("%vSpeeds     : %v STR, %v LER\n", ind+ind, sstr, sler)
			fmt.Printf("%vLimits     : %v message limit, %v sync limit\n", ind+ind, stl, ssl)
			fmt.Printf("%vMessages   : %v offered, %v outgoing, %v incoming, %v%% acceptance rate\n", ind+ind, pmo, pmout, pmi, formatRound2(ar))
			fmt.Printf("%vTraffic    : %v received, %v sent\n", ind+ind, srxb, stxb)
			msSuffix := ""
			if pmuh != 1 {
				msSuffix = "s"
			}
			fmt.Printf("%vSync state : %v unhandled message%v, %v\n", ind+ind, pmuh, msSuffix, ls)
			fmt.Printf("\n")
		}
	}
}

const SyncRequestPath = "/pn/peer/sync"
const UnpeerRequestPath = "/pn/peer/unpeer"

func (c *clientT) requestUnpeerInternal(id *rns.Identity, targetHash []byte, remoteIdentityArg *rns.Identity, timeoutArg time.Duration, exitOnFail bool) (any, error) {
	if remoteIdentityArg == nil {
		remoteIdentityArg = id
	}

	if c.ts == nil {
		c.ts = rns.NewTransportSystem()
	}
	controlDestination, err := rns.NewDestination(c.ts, remoteIdentityArg, rns.DestinationOut, rns.DestinationSingle, "lxmf", "propagation", "control")
	if err != nil {
		return nil, err
	}

	start := time.Now()
	checkTimeout := func() error {
		if time.Since(start) > timeoutArg {
			if exitOnFail {
				fmt.Println("Requesting lxmd peering break timed out, exiting now")
				osExit(200)
				return nil
			}
			return fmt.Errorf("timeout")
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}

	if !c.ts.HasPath(controlDestination.Hash) {
		_ = c.ts.RequestPath(controlDestination.Hash)
		for !c.ts.HasPath(controlDestination.Hash) {
			if err := checkTimeout(); err != nil {
				return nil, err
			}
		}
	}

	link, err := rns.NewLink(c.ts, controlDestination)
	if err != nil {
		return nil, err
	}

	for link.GetStatus() != rns.LinkActive {
		if err := checkTimeout(); err != nil {
			return nil, err
		}
	}

	if err := link.Identify(id); err != nil {
		return nil, err
	}

	requestReceipt, err := link.Request(UnpeerRequestPath, targetHash, nil, nil, nil, 0)
	if err != nil {
		return nil, err
	}

	for requestReceipt.GetStatus() != rns.RequestReady {
		if err := checkTimeout(); err != nil {
			return nil, err
		}
	}

	link.Teardown()
	return requestReceipt.Response, nil
}

func (c *clientT) requestSyncInternal(id *rns.Identity, targetHash []byte, remoteIdentityArg *rns.Identity, timeoutArg time.Duration, exitOnFail bool) (any, error) {
	if remoteIdentityArg == nil {
		remoteIdentityArg = id
	}

	if c.ts == nil {
		c.ts = rns.NewTransportSystem()
	}
	controlDestination, err := rns.NewDestination(c.ts, remoteIdentityArg, rns.DestinationOut, rns.DestinationSingle, "lxmf", "propagation", "control")
	if err != nil {
		return nil, err
	}

	start := time.Now()
	checkTimeout := func() error {
		if time.Since(start) > timeoutArg {
			if exitOnFail {
				fmt.Println("Requesting lxmd peer sync timed out, exiting now")
				osExit(200)
				return nil
			}
			return fmt.Errorf("timeout")
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}

	if !c.ts.HasPath(controlDestination.Hash) {
		_ = c.ts.RequestPath(controlDestination.Hash)
		for !c.ts.HasPath(controlDestination.Hash) {
			if err := checkTimeout(); err != nil {
				return nil, err
			}
		}
	}

	link, err := rns.NewLink(c.ts, controlDestination)
	if err != nil {
		return nil, err
	}

	for link.GetStatus() != rns.LinkActive {
		if err := checkTimeout(); err != nil {
			return nil, err
		}
	}

	if err := link.Identify(id); err != nil {
		return nil, err
	}

	requestReceipt, err := link.Request(SyncRequestPath, targetHash, nil, nil, nil, 0)
	if err != nil {
		return nil, err
	}

	for requestReceipt.GetStatus() != rns.RequestReady {
		if err := checkTimeout(); err != nil {
			return nil, err
		}
	}

	link.Teardown()
	return requestReceipt.Response, nil
}

const (
	LXMPeerErrorNoIdentity   = 0xf0
	LXMPeerErrorNoAccess     = 0xf1
	LXMPeerErrorInvalidKey   = 0xf3
	LXMPeerErrorInvalidData  = 0xf4
	LXMPeerErrorInvalidStamp = 0xf5
	LXMPeerErrorThrottled    = 0xf6
	LXMPeerErrorNotFound     = 0xfd
	LXMPeerErrorTimeout      = 0xfe
)

func (c *clientT) requestSync(target string, remote string, configDirArg string, rnsConfigDir string, verbosity int, quietness int, timeout time.Duration, identityPathArg string) {
	peerDestinationHash, err := rns.HexToBytes(target)
	if err != nil || len(peerDestinationHash) != rns.TruncatedHashLength/8 {
		msg := "Invalid peer destination hash"
		if err != nil {
			msg = fmt.Sprintf("Invalid peer destination hash: %v", err)
		} else {
			msg = fmt.Sprintf("Invalid peer destination hash: Destination hash length must be %v characters", rns.TruncatedHashLength/8*2)
		}
		fmt.Println(msg)
		osExit(203)
		return
	}

	reticulum, err := c.remoteInit(configDirArg, rnsConfigDir, verbosity, quietness, identityPathArg)
	if err != nil {
		fmt.Printf("Remote initialization failed: %v\n", err)
		osExit(1)
		return
	}
	defer func() {
		if err := reticulum.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	targetIdentity := c.getTargetIdentity(remote, timeout)
	if c.ts == nil {
		c.ts = reticulum.Transport()
	}
	response, err := c.requestSyncInternal(c.identity, peerDestinationHash, targetIdentity, timeout, true)
	if err != nil {
		fmt.Printf("Request sync failed: %v\n", err)
		osExit(1)
		return
	}

	if response == nil {
		fmt.Println("Empty response received")
		osExit(207)
		return
	}

	// Python comparison: if response == LXMF.LXMPeer.LXMPeer.ERROR_NO_IDENTITY:
	// We need to handle the case where response is an int (error code)
	if code, ok := response.(int); ok {
		switch code {
		case LXMPeerErrorNoIdentity:
			fmt.Println("Remote received no identity")
			osExit(203)
			return
		case LXMPeerErrorNoAccess:
			fmt.Println("Access denied")
			osExit(204)
			return
		case LXMPeerErrorInvalidData:
			fmt.Println("Invalid data received by remote")
			osExit(205)
			return
		case LXMPeerErrorNotFound:
			fmt.Println("The requested peer was not found")
			osExit(206)
			return
		}
	} else if code, ok := response.(uint64); ok {
		// msgpack might unpack as uint64
		switch code {
		case uint64(LXMPeerErrorNoIdentity):
			fmt.Println("Remote received no identity")
			osExit(203)
			return
		case uint64(LXMPeerErrorNoAccess):
			fmt.Println("Access denied")
			osExit(204)
			return
		case uint64(LXMPeerErrorInvalidData):
			fmt.Println("Invalid data received by remote")
			osExit(205)
			return
		case uint64(LXMPeerErrorNotFound):
			fmt.Println("The requested peer was not found")
			osExit(206)
			return
		}
	}

	fmt.Printf("Sync requested for peer <%x>\n", peerDestinationHash)
	osExit(0)
}

func (c *clientT) requestUnpeer(target string, remote string, configDirArg string, rnsConfigDir string, verbosity int, quietness int, timeout time.Duration, identityPathArg string) {
	peerDestinationHash, err := rns.HexToBytes(target)
	if err != nil || len(peerDestinationHash) != rns.TruncatedHashLength/8 {
		msg := "Invalid peer destination hash"
		if err != nil {
			msg = fmt.Sprintf("Invalid peer destination hash: %v", err)
		} else {
			msg = fmt.Sprintf("Invalid peer destination hash: Destination hash length must be %v characters", rns.TruncatedHashLength/8*2)
		}
		fmt.Println(msg)
		osExit(203)
		return
	}

	reticulum, err := c.remoteInit(configDirArg, rnsConfigDir, verbosity, quietness, identityPathArg)
	if err != nil {
		fmt.Printf("Remote initialization failed: %v\n", err)
		osExit(1)
		return
	}
	defer func() {
		if err := reticulum.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	targetIdentity := c.getTargetIdentity(remote, timeout)
	if c.ts == nil {
		c.ts = reticulum.Transport()
	}
	response, err := c.requestUnpeerInternal(c.identity, peerDestinationHash, targetIdentity, timeout, true)
	if err != nil {
		fmt.Printf("Request unpeer failed: %v\n", err)
		osExit(1)
		return
	}

	if response == nil {
		fmt.Println("Empty response received")
		osExit(207)
		return
	}

	if code, ok := response.(int); ok {
		switch code {
		case LXMPeerErrorNoIdentity:
			fmt.Println("Remote received no identity")
			osExit(203)
			return
		case LXMPeerErrorNoAccess:
			fmt.Println("Access denied")
			osExit(204)
			return
		case LXMPeerErrorInvalidData:
			fmt.Println("Invalid data received by remote")
			osExit(205)
			return
		case LXMPeerErrorNotFound:
			fmt.Println("The requested peer was not found")
			osExit(206)
			return
		}
	} else if code, ok := response.(uint64); ok {
		switch code {
		case uint64(LXMPeerErrorNoIdentity):
			fmt.Println("Remote received no identity")
			osExit(203)
			return
		case uint64(LXMPeerErrorNoAccess):
			fmt.Println("Access denied")
			osExit(204)
			return
		case uint64(LXMPeerErrorInvalidData):
			fmt.Println("Invalid data received by remote")
			osExit(205)
			return
		case uint64(LXMPeerErrorNotFound):
			fmt.Println("The requested peer was not found")
			osExit(206)
			return
		}
	}

	fmt.Printf("Broke peering with <%x>\n", peerDestinationHash)
	osExit(0)
}

func anyToFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case uint64:
		return float64(val)
	default:
		return 0
	}
}

// formatRound2 formats a float rounded to 2 decimal places, matching
// Python's str(round(v, 2)) output which trims trailing zeros but
// keeps at least one decimal digit.
func formatRound2(v float64) string {
	rounded := math.Round(v*100) / 100
	s := fmt.Sprintf("%.2f", rounded)
	s = strings.TrimRight(s, "0")
	if strings.HasSuffix(s, ".") {
		s += "0"
	}
	return s
}
