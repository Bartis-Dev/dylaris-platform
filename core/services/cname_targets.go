package services

import "strings"

// CNAMETargets expands the operator's single CNAME LABEL into one full target
// per hoster base domain: "route" + ["eu.example.com"] -> "route.eu.example.com".
//
// The label is stored alone (gateway_cname_target) so one setting covers every
// region, and it is NEVER a usable DNS name on its own. Two places had it right
// already - the route picker and the gateway settings tab, both via the panel's
// cnameTargetsFor - and the two added with the custom-domain claim did not: the
// verifier compared a resolved CNAME against the bare label "route", which no
// answer can ever equal, and the panel told the customer to point their domain
// at "route". So the CNAME half of the ownership proof could not pass, and a
// customer who followed the instruction created a record that resolves nowhere.
//
// Mirrors panel/src/lib/cnameTargets.ts, which cannot be imported here. Both
// lowercase, trim and de-duplicate, so the same label and hoster list produce
// the same list on both sides.
func CNAMETargets(label string, hosterBases []string) []string {
	l := strings.ToLower(strings.TrimSpace(label))
	if l == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(hosterBases))
	for _, b := range hosterBases {
		base := strings.ToLower(strings.TrimSpace(b))
		if base == "" {
			continue
		}
		fqdn := l + "." + base
		// Two hoster entries can differ only by validation mode.
		if seen[fqdn] {
			continue
		}
		seen[fqdn] = true
		out = append(out, fqdn)
	}
	return out
}
