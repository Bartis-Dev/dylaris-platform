package queue

import "strings"

// Node -> Core mod-install reporting.
//
// install_mod used to be fire-and-forget. The node logged a 404, a hash
// mismatch and a successful write to its own stdout and told Core none of them,
// while Core wrote the "installed" row BEFORE dispatching and never revisited
// it. A mod that never landed was indistinguishable from one that did, and the
// panel offered to update it.
//
// Same shape as the backup channels next door, and for the same reason: Pub/Sub
// carries no sender identity, so the channel name is the only thing a message
// can be attributed by, and a fleet-wide name would let any node - a
// tenant-owned BYON machine included - report on any server's mods. Per token,
// so Redis refuses a cross-node publish outright, and Core additionally
// re-derives the server's node and compares it with the token in the name.
const modResultsChannelPrefix = "dylaris:mods:results:"

// ModResultsPattern is what Core PSUBSCRIBEs. A node token is Core-minted and
// carries no Redis glob metacharacters, so the trailing "*" matches exactly one
// token.
const ModResultsPattern = modResultsChannelPrefix + "*"

// ModResultsChannel is the channel a node publishes its mod-install results on.
// nodeToken is the node's own Core-assigned identity.
func ModResultsChannel(nodeToken string) string {
	return modResultsChannelPrefix + nodeToken
}

// NodeTokenFromModChannel extracts the publishing node's token from a channel
// name delivered by a pattern subscription. ok is false for anything that is
// not this channel, and for an empty token - both are unattributable and must
// be dropped rather than read as "nothing to check".
func NodeTokenFromModChannel(channel string) (token string, ok bool) {
	rest, found := strings.CutPrefix(channel, modResultsChannelPrefix)
	if !found {
		return "", false
	}
	return rest, rest != ""
}
