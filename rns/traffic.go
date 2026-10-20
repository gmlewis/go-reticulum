// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// trafficCounter is the per-interface last-sample state used by
// count_traffic_loop (Python Transport.py:427-439): the timestamp of the last
// sample and the cumulative rxb/txb at that sample.
type trafficCounter struct {
	ts   time.Time
	rxb  uint64
	txb  uint64
	arxb uint64
	atxb uint64
	prxb uint64
	ptxb uint64
}

// interfaceTrafficCounters is the optional announce/path-request counter source
// the traffic loop reads (Python interface.arxb/atxb/prxb/ptxb).
type interfaceTrafficCounters interface {
	AnnounceCounters() (rxBytes, txBytes, rxCount, txCount uint64)
	PathRequestCounters() (rxBytes, txBytes, rxCount, txCount uint64)
}

// interfaceFrequencySource is the optional frequency source the loop sums
// (Python interface.incoming_announce_frequency() etc.).
type interfaceFrequencySource interface {
	IncomingAnnounceFrequency() float64
	OutgoingAnnounceFrequency() float64
	IncomingPrFrequency() float64
	OutgoingPrFrequency() float64
}

// trafficSpeedSetter is the optional interface method the loop uses to push
// computed per-interface speeds back onto the interface, mirroring Python
// setting interface.current_rx_speed/current_tx_speed. BaseInterface
// implements it; interfaces without it simply do not expose per-interface
// speeds (matching Python's `if hasattr(interface, "current_rx_speed")`).
type trafficSpeedSetter interface {
	SetTrafficSpeeds(rxSpeed, txSpeed, arxSpeed, atxSpeed, prxSpeed, ptxSpeed float64)
}

// parentedInterface is implemented by interfaces that have a parent (tunnel
// sub-interfaces, RNode multi children). The traffic loop skips them, matching
// Python's `if not hasattr(interface, "parent_interface") or
// interface.parent_interface == None` guard (Transport.py:426).
type parentedInterface interface {
	ParentInterface() interfaces.Interface
}

// countTrafficPass performs one iteration of count_traffic_loop at instant now
// (Python Transport.count_traffic_loop, Transport.py:419-451). For each
// top-level interface it computes the byte delta since the last sample and the
// bit-per-second speed crxs=(rx_diff*8)/ts_diff, pushes the speed onto the
// interface, and accumulates per-pass aggregate deltas/speeds. The aggregate
// traffic_rxb/txb grow by the per-pass deltas; speed_rx/tx become the per-pass
// speed sum. It returns the per-pass rx/tx byte deltas and rx/tx speeds.
//
// The clock is supplied by the caller so the timing math is deterministic and
// unit-testable; the production loop calls this with time.Now() once per
// second.
func (ts *TransportSystem) countTrafficPass(now time.Time) (rxb, txb uint64, rxs, txs float64) {
	var arxs, atxs, prxs, ptxs float64
	var arxf, atxf, prxf, ptxf float64
	var arxbDelta, atxbDelta, prxbDelta, ptxbDelta uint64
	ts.trafficMu.Lock()
	if ts.trafficCounters == nil {
		ts.trafficCounters = make(map[interfaces.Interface]*trafficCounter)
	}
	ts.trafficMu.Unlock()

	for _, iface := range ts.GetInterfaces() {
		// Skip sub-interfaces (Python: parent_interface == None guard).
		if p, ok := iface.(parentedInterface); ok && p.ParentInterface() != nil {
			continue
		}

		irxb := iface.BytesReceived()
		itxb := iface.BytesSent()
		var iarxb, iatxb, iprxb, iptxb uint64
		if counters, ok := iface.(interfaceTrafficCounters); ok {
			iarxb, iatxb, _, _ = counters.AnnounceCounters()
			iprxb, iptxb, _, _ = counters.PathRequestCounters()
		}

		ts.trafficMu.Lock()
		tc, exists := ts.trafficCounters[iface]
		if !exists {
			// First sample for this interface: initialise the counter and
			// contribute nothing this pass (Python else-branch,
			// Transport.py:441-442).
			ts.trafficCounters[iface] = &trafficCounter{
				ts: now, rxb: irxb, txb: itxb,
				arxb: iarxb, atxb: iatxb, prxb: iprxb, ptxb: iptxb,
			}
			ts.trafficMu.Unlock()
			continue
		}
		ts.trafficMu.Unlock()

		rxDiff := irxb - tc.rxb
		txDiff := itxb - tc.txb
		arxDiff := iarxb - tc.arxb
		atxDiff := iatxb - tc.atxb
		prxDiff := iprxb - tc.prxb
		ptxDiff := iptxb - tc.ptxb
		tsDiff := now.Sub(tc.ts).Seconds()
		var crxs, ctxs, carxs, catxs, cprxs, cptxs float64
		if tsDiff > 0 {
			crxs = float64(rxDiff*8) / tsDiff
			ctxs = float64(txDiff*8) / tsDiff
			carxs = float64(arxDiff*8) / tsDiff
			catxs = float64(atxDiff*8) / tsDiff
			cprxs = float64(prxDiff*8) / tsDiff
			cptxs = float64(ptxDiff*8) / tsDiff
		}
		if setter, ok := iface.(trafficSpeedSetter); ok {
			setter.SetTrafficSpeeds(crxs, ctxs, carxs, catxs, cprxs, cptxs)
		}
		rxb += rxDiff
		txb += txDiff
		rxs += crxs
		txs += ctxs
		arxbDelta += arxDiff
		atxbDelta += atxDiff
		prxbDelta += prxDiff
		ptxbDelta += ptxDiff

		arxs += carxs
		atxs += catxs
		prxs += cprxs
		ptxs += cptxs
		if freqs, ok := iface.(interfaceFrequencySource); ok {
			arxf += freqs.IncomingAnnounceFrequency()
			atxf += freqs.OutgoingAnnounceFrequency()
			prxf += freqs.IncomingPrFrequency()
			ptxf += freqs.OutgoingPrFrequency()
		}

		ts.trafficMu.Lock()
		tc.rxb = irxb
		tc.txb = itxb
		tc.arxb = iarxb
		tc.atxb = iatxb
		tc.prxb = iprxb
		tc.ptxb = iptxb
		tc.ts = now
		ts.trafficMu.Unlock()
	}

	ts.trafficMu.Lock()
	ts.trafficRxb += rxb
	ts.trafficTxb += txb
	ts.speedRx = rxs
	ts.speedTx = txs
	ts.announceRxb += arxbDelta
	ts.announceTxb += atxbDelta
	ts.announceSpeedRx = arxs
	ts.announceSpeedTx = atxs
	ts.announceFreqRx = arxf
	ts.announceFreqTx = atxf
	ts.prRxb += prxbDelta
	ts.prTxb += ptxbDelta
	ts.prSpeedRx = prxs
	ts.prSpeedTx = ptxs
	ts.prFreqRx = prxf
	ts.prFreqTx = ptxf
	ts.trafficMu.Unlock()
	return
}

// countTrafficLoop is the production traffic-counter loop (Python
// Transport.count_traffic_loop, Transport.py:419-451): once per second it runs
// countTrafficPass with the wall clock. It exits when stopCh is closed.
func (ts *TransportSystem) countTrafficLoop(stopCh <-chan struct{}, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			ts.countTrafficPass(time.Now())
		}
	}
}

// TrafficRxb returns the cumulative received-byte delta total
// (Transport.traffic_rxb), driven by count_traffic_loop.
func (ts *TransportSystem) TrafficRxb() uint64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.trafficRxb
}

// TrafficTxb returns the cumulative transmitted-byte delta total
// (Transport.traffic_txb).
func (ts *TransportSystem) TrafficTxb() uint64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.trafficTxb
}

// SpeedRx returns the current aggregate receive speed in bits/sec
// (Transport.speed_rx).
func (ts *TransportSystem) SpeedRx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.speedRx
}

// SpeedTx returns the current aggregate transmit speed in bits/sec
// (Transport.speed_tx).
func (ts *TransportSystem) SpeedTx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.speedTx
}

// AnnounceRxb returns the cumulative announce receive-byte delta
// (Transport.announce_rxb).
func (ts *TransportSystem) AnnounceRxb() uint64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.announceRxb
}

// AnnounceTxb returns the cumulative announce transmit-byte delta
// (Transport.announce_txb).
func (ts *TransportSystem) AnnounceTxb() uint64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.announceTxb
}

// AnnounceSpeedRx returns the aggregate announce receive speed (bits/sec).
func (ts *TransportSystem) AnnounceSpeedRx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.announceSpeedRx
}

// AnnounceSpeedTx returns the aggregate announce transmit speed (bits/sec).
func (ts *TransportSystem) AnnounceSpeedTx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.announceSpeedTx
}

// AnnounceFreqRx returns the summed incoming announce frequency (Hz).
func (ts *TransportSystem) AnnounceFreqRx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.announceFreqRx
}

// AnnounceFreqTx returns the summed outgoing announce frequency (Hz).
func (ts *TransportSystem) AnnounceFreqTx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.announceFreqTx
}

// PrRxb returns the cumulative path-request receive-byte delta (Transport.pr_rxb).
func (ts *TransportSystem) PrRxb() uint64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.prRxb
}

// PrTxb returns the cumulative path-request transmit-byte delta (Transport.pr_txb).
func (ts *TransportSystem) PrTxb() uint64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.prTxb
}

// PrSpeedRx returns the aggregate path-request receive speed (bits/sec).
func (ts *TransportSystem) PrSpeedRx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.prSpeedRx
}

// PrSpeedTx returns the aggregate path-request transmit speed (bits/sec).
func (ts *TransportSystem) PrSpeedTx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.prSpeedTx
}

// PrFreqRx returns the summed incoming path-request frequency (Hz).
func (ts *TransportSystem) PrFreqRx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.prFreqRx
}

// PrFreqTx returns the summed outgoing path-request frequency (Hz).
func (ts *TransportSystem) PrFreqTx() float64 {
	ts.trafficMu.Lock()
	defer ts.trafficMu.Unlock()
	return ts.prFreqTx
}
