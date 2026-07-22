package redisacl

// commandCats is the exhaustive category grant covering every command the node
// and log-shipper use: read/write/stream/pubsub/connection/transaction, minus
// dangerous/admin/scripting, plus explicit SCAN (SCAN is not in @dangerous;
// KEYS is and stays denied).
var commandCats = []string{
	"+@read", "+@write", "+@stream", "+@pubsub", "+@connection", "+@transaction",
	"-@dangerous", "-@admin", "-@scripting", "+scan",
}

// globalReadKeys are the shared keys the node accesses (NOT the shipper). The
// three the node only ever reads are read-only (%R~); dylaris:migration:* stays
// read+write because the node writes its own migration status/meta/endpoint keys.
func globalReadKeys() []string {
	return []string{
		"~dylaris:routing_mode", "~dylaris:file_access_mode",
		"%R~dylaris:placement:*",
		"~beam:bw_limit", "~beam:bw_up_internal", "~beam:bw_down_internal",
		// Upload-limit config the node enforces on the beam + SFTP + SaveFileContent
		// write paths (read-only), plus the per-user daily counter it reads AND
		// increments (read+write). Without these the node's quota reads return
		// NOPERM and the shared quota package fails OPEN, silently disabling the
		// node-side size cap + daily quota and never recording node uploads into
		// the shared bucket. The counter is per-user (not node-scoped), so the
		// node needs it for every user whose uploads it handles.
		"%R~beam:max_upload_bytes", "%R~beam:daily_upload_bytes",
		"~dylaris:beam:daily:*",
		"%R~dylaris:core:*",
		"%R~sftp:auth:*",
		"~dylaris:migration:*",
	}
}

// BuildNodeACLRules returns the ACL SETUSER argument list (after the username)
// for the node agent user: own node-scoped keys + assigned server keys + global
// reads + pub/sub channels + command categories.
func BuildNodeACLRules(token, password string, serverUUIDs []string) []interface{} {
	rules := []interface{}{"on", ">" + password, "resetkeys", "resetchannels"}
	rules = append(rules, "~dylaris:node:"+token+":*", "~dylaris:discovery:"+token, "~beam:node-endpoint:"+token)
	// The per-server storage-path mapping lives under the UN-prefixed
	// node:<token>:server:<uuid>:storage namespace (core handlers/servers_storage.go
	// writes it, node/storage.go reads+writes it) - NOT dylaris:node:*. Without this
	// grant the node gets NOPERM persisting/reading the storage mapping, which the
	// install + reconcile paths hit on every server. Scoped to this node's token.
	rules = append(rules, "~node:"+token+":*")
	// The node reads its own SFTP server list (sftp:node:<nodeName>:user:<user>,
	// nodeName == token) to resolve which server a virtual SFTP path targets.
	// Without this getUserServers gets NOPERM and every SFTP session sees an
	// empty root, so SFTP is dead under mandatory ACL.
	rules = append(rules, "%R~sftp:node:"+token+":*")
	for _, u := range serverUUIDs {
		rules = append(rules, "~dylaris:server:"+u+":*")
	}
	for _, k := range globalReadKeys() {
		rules = append(rules, k)
	}
	rules = append(rules, "&dylaris:backup:results", "&dylaris:backup:restores")
	for _, u := range serverUUIDs {
		rules = append(rules, "&dylaris:server:"+u+":stats:live")
	}
	for _, c := range commandCats {
		rules = append(rules, c)
	}
	return rules
}

// BuildShipperACLRules returns the ACL rules for the per-node shipper user:
// ONLY the assigned servers' keys + their stats:live channel. No node-scoped
// keys, no global reads, no :cmds.
func BuildShipperACLRules(password string, serverUUIDs []string) []interface{} {
	rules := []interface{}{"on", ">" + password, "resetkeys", "resetchannels"}
	for _, u := range serverUUIDs {
		rules = append(rules, "~dylaris:server:"+u+":*")
		rules = append(rules, "&dylaris:server:"+u+":stats:live")
	}
	for _, c := range commandCats {
		rules = append(rules, c)
	}
	return rules
}

// BuildLinkACLRules returns the ACL rules for a node's Link sidecar user: its own
// registration keys (by tunnel token), its discovery + beam-node + beam-endpoint
// keys (by node token), and read-only edge/relay registries. No node-scoped or
// server keys. tunnelToken = DeriveLinkToken(nodeToken, clusterSecret) (the Link's
// AgentSecret, the exact value it writes link:<tunnelToken> under).
func BuildLinkACLRules(password, nodeToken, tunnelToken string) []interface{} {
	rules := []interface{}{"on", ">" + password, "resetkeys", "resetchannels"}
	rules = append(rules,
		"~link:"+tunnelToken,
		"~online_link:"+tunnelToken,
		"~dylaris:errors:link:"+nodeToken,
		"~hub:link:discovery:"+nodeToken,
		"~beam:node:"+nodeToken,
		"%R~sys:edges", "%R~edge:registry:*", "%R~edge:cert:fingerprint:*",
		"%R~sys:beams", "%R~beam:registry:*",
		"%R~beam:node-endpoint:"+nodeToken,
	)
	for _, c := range commandCats {
		rules = append(rules, c)
	}
	return rules
}

// SetUserArgs prepends the ACL subcommand + username for rdb.Do(...).
func SetUserArgs(username string, rules []interface{}) []interface{} {
	out := make([]interface{}, 0, len(rules)+3)
	out = append(out, "ACL", "SETUSER", username)
	out = append(out, rules...)
	return out
}

// BuildRouteOnlyLinkACLRules scopes an external route-only link to exactly the keys
// it touches. No hub discovery, no beam: a route-only link has no NodeID and can
// neither publish nor resolve either.
func BuildRouteOnlyLinkACLRules(password, tunnelToken, instanceID string) []interface{} {
	rules := []interface{}{"on", ">" + password, "resetkeys", "resetchannels"}
	rules = append(rules,
		"~link:"+tunnelToken,
		"~online_link:"+tunnelToken,
		"~dylaris:errors:link:"+instanceID,
		"%R~sys:edges", "%R~edge:registry:*", "%R~edge:cert:fingerprint:*",
	)
	for _, c := range commandCats {
		rules = append(rules, c)
	}
	return rules
}
