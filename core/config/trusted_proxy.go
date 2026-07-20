package config

import (
	"log"
	"net"
	"strings"
)

// defaultTrustedProxyCIDRs are the private ranges trusted when
// TRUSTED_PROXY_CIDRS is unset. The shipped reference reverse proxy sits on the
// private Docker network, so trusting these makes per-client rate limiting work
// with no configuration, while a public attacker's forged X-Forwarded-For entry
// - which is public, hence outside these - is never believed.
var defaultTrustedProxyCIDRs = []string{
	"127.0.0.0/8", "::1/128", // loopback
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC1918
	"fc00::/7",                    // IPv6 unique-local
	"169.254.0.0/16", "fe80::/10", // link-local (Docker/host networking)
}

// ParseTrustedProxyCIDRs turns the TRUSTED_PROXY_CIDRS value into networks.
//
//   - "" (unset)   -> the private ranges above.
//   - "none"/"off" -> an empty set: no proxy is trusted and XFF is ignored,
//     which is correct when Core is exposed directly to clients.
//   - a comma list -> exactly those CIDRs. A bare IP is accepted and treated as
//     a /32 (or /128). An unparseable entry is logged and skipped rather than
//     failing the boot, so one typo cannot take Core down; the surviving
//     entries still apply.
func ParseTrustedProxyCIDRs(raw string) []*net.IPNet {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return parseCIDRList(defaultTrustedProxyCIDRs)
	case strings.EqualFold(raw, "none"), strings.EqualFold(raw, "off"):
		return nil
	default:
		return parseCIDRList(strings.Split(raw, ","))
	}
}

func parseCIDRList(entries []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		// A bare IP has no mask; promote it to a single-host CIDR.
		if !strings.Contains(e, "/") {
			if ip := net.ParseIP(e); ip != nil {
				if ip.To4() != nil {
					e += "/32"
				} else {
					e += "/128"
				}
			}
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			log.Printf("config: ignoring unparseable TRUSTED_PROXY_CIDRS entry %q: %v", e, err)
			continue
		}
		out = append(out, n)
	}
	return out
}
