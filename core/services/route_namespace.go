package services

import (
	"encoding/json"
	"strings"
)

// HosterDomainsSettingKey holds the JSON list of base domains this platform hands
// out subdomains under. Named here rather than typed twice because two packages
// now decide the same thing from it: handlers, when it refuses to create one more
// address, and the billing sweep, when it decides a tenant is holding too many.
// Those two answers have to agree - a route the create path allows and the sweep
// counts is a tenant cut off for something they were told was fine.
const HosterDomainsSettingKey = "gateway_hoster_domains"

// HosterBaseDomains parses the setting into the list of base domains we operate.
// Malformed JSON yields no domains, which makes DomainIsOurs answer false for
// everything - and that fails OPEN for the tenant (nothing counts against their
// cap) rather than closed. Deliberate: the cap exists to ration OUR namespace,
// and a namespace we cannot read is not one we are running out of.
func HosterBaseDomains(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var parsed []struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	out := make([]string, 0, len(parsed))
	for _, p := range parsed {
		if d := strings.ToLower(strings.TrimSpace(p.Domain)); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// DomainIsOurs reports whether a route domain sits under one of the base domains
// we operate.
//
// This is the whole definition of what the route cap counts. An address in our
// namespace is a thing we hand out and can run out of; a domain the customer
// owns and points at us with a CNAME costs us one DNS record and nothing else,
// so charging them for it would be pricing something we do not supply. That is
// why bringing your own domain is unlimited and ours is not.
//
// Suffix matching is anchored on a dot so "notdylaris.com" cannot pass as being
// under "dylaris.com".
func DomainIsOurs(domain string, bases []string) bool {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if domain == "" {
		return false
	}
	for _, base := range bases {
		if domain == base || strings.HasSuffix(domain, "."+base) {
			return true
		}
	}
	return false
}
