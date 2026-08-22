package services

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"strings"
)

// Custom-domain ownership proof.
//
// A tenant may point their own domain at the platform, but only after showing
// they control it. Everything here answers one question: does this domain's DNS
// currently point at us? Only someone with DNS control can make it do that, so
// that IS the proof - no token exchange needed on the happy path.
//
// The TXT path at the bottom is the stricter fallback, used once a tenant has
// burned their attempts.

// DomainResolver is the DNS surface the proof needs, as an interface so tests
// can drive it without touching the network.
//
// Injectable on purpose, and not only for convenience: a resolver that answers
// NXDOMAIN with a search-domain wildcard (which is how a real lookup burned us
// once - a name that did not exist came back with a public address) must not be
// able to turn "not configured" into a pass. Every check below compares the
// answer against a value only we know or control, so a bogus answer fails
// closed rather than sneaking through.
type DomainResolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupTXT(ctx context.Context, host string) ([]string, error)
}

// netResolver is the production DomainResolver.
type netResolver struct{ r *net.Resolver }

// NewNetResolver returns the system-resolver implementation.
func NewNetResolver() DomainResolver { return &netResolver{r: net.DefaultResolver} }

func (n *netResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return n.r.LookupCNAME(ctx, host)
}
func (n *netResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return n.r.LookupHost(ctx, host)
}
func (n *netResolver) LookupTXT(ctx context.Context, host string) ([]string, error) {
	return n.r.LookupTXT(ctx, host)
}

// TXTVerifyPrefix is the label a tenant adds to prove ownership the strict way.
const TXTVerifyPrefix = "_dylaris-verify."

// normaliseHost lowercases and drops the trailing dot DNS answers carry.
func normaliseHost(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}

// CheckDomainPointsAtUs reports whether domain currently resolves to the
// platform, by either accepted route.
//
// TWO routes, and the second is not a convenience:
//
//  1. The CNAME target matches one we published.
//  2. Failing that, the resolved ADDRESS is one of our edges.
//
// A strict CNAME-target match alone would reject correctly configured, paying
// customers. An apex domain cannot carry a CNAME at all and has to use an
// A-record, and a Cloudflare-proxied ("orange cloud") record hides the real
// target behind Cloudflare's own - in both cases the customer did exactly what
// they were told and a CNAME-only check still says no.
//
// Both routes need DNS control to satisfy, which is the property being tested.
func CheckDomainPointsAtUs(ctx context.Context, res DomainResolver, domain string, cnameTargets, edgeAddrs []string) bool {
	domain = normaliseHost(domain)
	if domain == "" {
		return false
	}

	if cname, err := res.LookupCNAME(ctx, domain); err == nil {
		got := normaliseHost(cname)
		// A resolver returns the name itself when there is no CNAME; that is not
		// a match for anything and falls through to the address check.
		for _, t := range cnameTargets {
			if t = normaliseHost(t); t != "" && got == t {
				return true
			}
		}
	}

	if len(edgeAddrs) == 0 {
		return false
	}
	addrs, err := res.LookupHost(ctx, domain)
	if err != nil {
		return false
	}
	want := make(map[string]bool, len(edgeAddrs))
	for _, a := range edgeAddrs {
		if a = strings.TrimSpace(a); a != "" {
			want[a] = true
		}
	}
	for _, a := range addrs {
		if want[strings.TrimSpace(a)] {
			return true
		}
	}
	return false
}

// NewTXTToken mints the self-service unblock token.
func NewTXTToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "dylaris-verify=" + hex.EncodeToString(b), nil
}

// CheckTXTToken reports whether _dylaris-verify.<domain> carries token.
//
// This is the way back after a permanent block, and it is deliberately a
// STRONGER proof than the CNAME check it replaces rather than merely a more
// annoying one: a CNAME only shows the domain points here, which a shared-hosting
// or CDN setup can produce without full control, while a TXT record at a
// dylaris-specific label is a value only the zone's owner can publish - and only
// this user's token satisfies it.
//
// Compared in constant time. The token is a credential, and a timing signal is
// free to exploit here because the attacker controls how often they ask.
func CheckTXTToken(ctx context.Context, res DomainResolver, domain, token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	records, err := res.LookupTXT(ctx, TXTVerifyPrefix+normaliseHost(domain))
	if err != nil {
		return false
	}
	for _, r := range records {
		r = strings.TrimSpace(r)
		if len(r) == len(token) && subtle.ConstantTimeCompare([]byte(r), []byte(token)) == 1 {
			return true
		}
	}
	return false
}
