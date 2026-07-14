package main

// beamConnectPlan returns the ordered list of transports ConnectToServer should try
// for one connect attempt, most-preferred first. It is pure (no I/O) so the ordering
// and gating rules are unit-tested without touching the network.
//
//   - "lan":    a co-located pinned-TLS dial to the node's LAN IPs. Requires a pinned
//     fingerprint AND at least one LAN IP. Tried FIRST regardless of relay presence.
//   - "relay":  the IP-hiding relay hop. Present whenever a relay address is known.
//   - "public": a pinned-TLS dial to the node's public address. Requires a fingerprint
//     and is only offered with no relay (Core omits the public address behind a relay,
//     so publicAddr is empty there anyway; this is belt-and-braces).
//
// An unpinnable node (no fingerprint) never yields a direct step ("lan"/"public"), so
// the app never attempts a plaintext direct dial.
func beamConnectPlan(hasFingerprint bool, lanIPs []string, relayAddr, publicAddr string) []string {
	plan := make([]string, 0, 3)
	if hasFingerprint && len(lanIPs) > 0 {
		plan = append(plan, "lan")
	}
	if relayAddr != "" {
		plan = append(plan, "relay")
	}
	if hasFingerprint && relayAddr == "" && publicAddr != "" {
		plan = append(plan, "public")
	}
	return plan
}
