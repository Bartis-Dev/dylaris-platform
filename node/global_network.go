package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// wg0RoutedSubnets parses /proc/net/route content and returns every subnet routed
// via wg0, so a freshly-created local dylaris_net can avoid shadowing the warp
// tunnel. Best-effort: an unparseable line is skipped and a fully unparseable
// table yields no ranges (Docker's own non-overlap and the 10/8 reservation still
// apply).
func wg0RoutedSubnets(routeTable string) []*net.IPNet {
	var out []*net.IPNet
	for _, line := range strings.Split(routeTable, "\n") {
		f := strings.Fields(line)
		// /proc/net/route columns: Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		if len(f) < 8 || f[0] != "wg0" {
			continue
		}
		dest, derr := hexLEtoIP(f[1])
		mask, merr := hexLEtoIP(f[7])
		if derr != nil || merr != nil {
			continue
		}
		// Skip a default route (0.0.0.0/0 via wg0, i.e. full-tunnel): it is not a
		// specific subnet to avoid. Treating it as reserved would make every
		// candidate overlap and collapse the whole avoidance to Docker auto-assign.
		if ones, _ := net.IPMask(mask).Size(); ones == 0 {
			continue
		}
		out = append(out, &net.IPNet{IP: dest, Mask: net.IPMask(mask)})
	}
	return out
}

// hexLEtoIP decodes a little-endian hex IPv4 as /proc/net/route stores it
// (e.g. "0000000A" -> 10.0.0.0).
func hexLEtoIP(h string) (net.IP, error) {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 4 {
		return nil, fmt.Errorf("bad hex ip %q", h)
	}
	return net.IPv4(b[3], b[2], b[1], b[0]).To4(), nil
}

// reservedSubnets unions the ranges a new local dylaris_net must avoid: existing
// Docker network subnets, every range routed via wg0 (the warp tunnel), and - on
// a host-net node - all of 10.0.0.0/8, which is warp's home range and must never
// be shadowed by a local bridge. The result is fed to nextFreeSubnet, which then
// picks a free block from 172.16/12 or 192.168/16 on a host-net node.
func reservedSubnets(dockerSubnets []*net.IPNet, routeTable string, hostNet bool) []*net.IPNet {
	used := append([]*net.IPNet{}, dockerSubnets...)
	used = append(used, wg0RoutedSubnets(routeTable)...)
	if hostNet {
		used = append(used, parseCIDRs([]string{"10.0.0.0/8"})...)
	}
	return used
}
