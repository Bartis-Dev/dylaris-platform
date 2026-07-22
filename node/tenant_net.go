package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

const (
	// tenantPrefixDefault is the block size handed to a new owner: /26 = 64
	// addresses, ~60 usable server slots after network/gateway/node/broadcast.
	tenantPrefixDefault = 26
	// tenantSlotBase reserves .0 network, .1 gateway (Docker default) and .2 the
	// node. Server slots are 1-based and start at .3 (base + tenantSlotBase + slot).
	tenantSlotBase = 2
)

// tenantPools are the private ranges the allocator draws blocks from, in order.
// 10/8 first (largest); 172.16/12 and 192.168/16 are fallbacks per the design.
func tenantPools() []*net.IPNet {
	out := make([]*net.IPNet, 0, 3)
	for _, c := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, n, _ := net.ParseCIDR(c) // literals: err is always nil
		out = append(out, n)
	}
	return out
}

// cidrsOverlap reports whether two IPv4 CIDR ranges share any address.
func cidrsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

// parseCIDRs parses CIDR strings, skipping empties and non-IPv4/unparseable
// entries (a Docker network without an IPAM subnet yields "").
func parseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil || n.IP.To4() == nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

// capacityForPrefix returns the usable server slots in a subnet of the given
// prefix length: total addresses minus network, gateway, node and broadcast.
func capacityForPrefix(prefixLen int) int {
	usable := (1 << (32 - prefixLen)) - 4
	if usable < 0 {
		return 0
	}
	return usable
}

// nextPrefixLen returns the next-larger block size for an enlarge (/26 -> /25 ->
// /24). ok=false when the block cannot grow further (/24 is the widest allowed).
func nextPrefixLen(current int) (int, bool) {
	if current <= 24 {
		return 0, false
	}
	return current - 1, true
}

// nodeIPInSubnet returns the fixed IPv4 the node pins for itself: network + 2.
func nodeIPInSubnet(subnet *net.IPNet) net.IP {
	return offsetIP(subnet, uint32(tenantSlotBase))
}

// serverIPInSubnet returns the fixed IPv4 for a 1-based server slot: network +
// tenantSlotBase + slot (slot 1 = .3 when the subnet base is .0).
func serverIPInSubnet(subnet *net.IPNet, slot int) net.IP {
	return offsetIP(subnet, uint32(tenantSlotBase)+uint32(slot))
}

func offsetIP(subnet *net.IPNet, off uint32) net.IP {
	base := binary.BigEndian.Uint32(subnet.IP.To4())
	out := make(net.IP, 4)
	binary.BigEndian.PutUint32(out, base+off)
	return out
}

// nextFreeSubnet scans tenantPools in order and returns the first subnet of the
// given prefix length not overlapping any subnet in used. Errors when exhausted.
func nextFreeSubnet(used []*net.IPNet, prefixLen int) (*net.IPNet, error) {
	step := uint32(1) << (32 - prefixLen)
	mask := net.CIDRMask(prefixLen, 32)
	for _, pool := range tenantPools() {
		poolStart := binary.BigEndian.Uint32(pool.IP.To4())
		poolOnes, _ := pool.Mask.Size()
		poolEnd := poolStart + (uint32(1) << (32 - poolOnes)) // exclusive
		for base := poolStart; base >= poolStart && base+step <= poolEnd; base += step {
			ipBytes := make(net.IP, 4)
			binary.BigEndian.PutUint32(ipBytes, base)
			cand := &net.IPNet{IP: ipBytes, Mask: mask}
			overlap := false
			for _, u := range used {
				if cidrsOverlap(cand, u) {
					overlap = true
					break
				}
			}
			if !overlap {
				return cand, nil
			}
		}
	}
	return nil, fmt.Errorf("tenant allocator: no free /%d block in private pools", prefixLen)
}
