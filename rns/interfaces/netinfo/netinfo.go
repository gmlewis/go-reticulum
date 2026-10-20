// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package netinfo enumerates the host's network adapters, mirroring
// RNS/Interfaces/util/netinfo.py: the adapter name/index pairs and per-adapter
// IPv4/IPv6 addresses the AutoInterface uses to discover peers. Python does
// this with ctypes getifaddrs; Go uses the net package, which exposes the same
// adapter name, index, flags and addresses.
package netinfo

import (
	"net"
	"sort"
	"syscall"
)

// Address families used as keys by IfAddresses, mirroring Python's
// AF_INET/AF_INET6 (socket constants).
const (
	AFInet  = syscall.AF_INET
	AFInet6 = syscall.AF_INET6
)

// Address is one adapter address. Addr is the host address; IPv4 entries also
// carry Prefix and Broadcast, while IPv6 entries mirror Python's
// (address, flowinfo, scope_id) form with Addr and no prefix.
type Address struct {
	Addr      string
	Prefix    int
	Broadcast string
	IsIPv4    bool
	NiceName  string
}

// Adapter is one host network adapter.
type Adapter struct {
	Name     string
	NiceName string
	Index    int
	IPs      []Address
}

// GetAdapters returns the host adapters in interface-index order. When
// includeUnconfigured is false, adapters without any address are omitted,
// mirroring Python get_adapters(include_unconfigured=False).
func GetAdapters(includeUnconfigured bool) ([]Adapter, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Adapter, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			addrs = nil
		}
		ips := make([]Address, 0, len(addrs))
		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				if ip4 := v.IP.To4(); ip4 != nil {
					ones, _ := v.Mask.Size()
					ips = append(ips, Address{
						Addr:      ip4.String(),
						Prefix:    ones,
						Broadcast: broadcastAddress(ip4, ones),
						IsIPv4:    true,
						NiceName:  iface.Name,
					})
					continue
				}
				if v.IP.To16() != nil {
					ips = append(ips, Address{Addr: v.IP.String(), NiceName: iface.Name})
				}
			case *net.IPAddr:
				if ip4 := v.IP.To4(); ip4 != nil {
					ips = append(ips, Address{Addr: ip4.String(), IsIPv4: true, NiceName: iface.Name})
					continue
				}
				ips = append(ips, Address{Addr: v.IP.String(), NiceName: iface.Name})
			}
		}
		if !includeUnconfigured && len(ips) == 0 {
			continue
		}
		out = append(out, Adapter{Name: iface.Name, NiceName: iface.Name, Index: iface.Index, IPs: ips})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// Interfaces returns the names of every adapter, including unconfigured ones,
// mirroring Python netinfo.interfaces().
func Interfaces() ([]string, error) {
	adapters, err := GetAdapters(true)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name)
	}
	return names, nil
}

// InterfaceNamesToIndexes maps each adapter name to its index, mirroring
// Python netinfo.interface_names_to_indexes().
func InterfaceNamesToIndexes() (map[string]int, error) {
	adapters, err := GetAdapters(true)
	if err != nil {
		return nil, err
	}
	results := make(map[string]int, len(adapters))
	for _, a := range adapters {
		results[a.Name] = a.Index
	}
	return results, nil
}

// InterfaceNameToNiceName returns the adapter's friendly name, or ok=false when
// the adapter is unknown, mirroring Python
// netinfo.interface_name_to_nice_name().
func InterfaceNameToNiceName(name string) (string, bool) {
	adapters, err := GetAdapters(true)
	if err != nil {
		return "", false
	}
	for _, a := range adapters {
		if a.Name == name {
			return a.NiceName, true
		}
	}
	return "", false
}

// IfAddresses returns an adapter's addresses keyed by address family, in the
// shape Python netinfo.ifaddresses() produces: IPv4 entries carry
// addr/prefix/broadcast, IPv6 entries carry addr only.
func IfAddresses(name string) (map[int][]Address, error) {
	adapters, err := GetAdapters(true)
	if err != nil {
		return nil, err
	}
	out := map[int][]Address{}
	for _, a := range adapters {
		if a.Name != name {
			continue
		}
		for _, ip := range a.IPs {
			if ip.IsIPv4 {
				out[AFInet] = append(out[AFInet], ip)
			} else {
				out[AFInet6] = append(out[AFInet6], ip)
			}
		}
	}
	return out, nil
}

// broadcastAddress computes the IPv4 broadcast address for ip/prefix.
func broadcastAddress(ip net.IP, prefix int) string {
	mask := net.CIDRMask(prefix, 32)
	b := make(net.IP, 4)
	for i := range 4 {
		b[i] = ip[i] | ^mask[i]
	}
	return b.String()
}
