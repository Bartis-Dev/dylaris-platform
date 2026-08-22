package redisacl

import "dylaris-pkg/queue"

// commandCats is the exhaustive category grant covering every command the node
// and log-shipper use: read/write/stream/pubsub/connection/transaction, minus
// dangerous/admin/scripting, plus explicit SCAN (SCAN is not in @dangerous;
// KEYS is and stays denied).
var commandCats = []string{
	"+@read", "+@write", "+@stream", "+@pubsub", "+@connection", "+@transaction",
	"-@dangerous", "-@admin", "-@scripting", "+scan",
}

// globalReadKeys are the shared keys the node accesses (NOT the shipper). The
// ones the node only ever reads are read-only (%R~); dylaris:migration:* stays
// read+write because the node writes its own migration status/meta/endpoint keys,
// and dylaris:beam:daily:* because it increments that counter.
//
// These are GLOBAL, not node-scoped, so a read+write grant here is a write
// handed to every node in the fleet, tenant-owned BYON nodes included. The three
// beam:bw_* keys are the bandwidth throttle the node is subject to - writable by
// the throttled party is the wrong way round - and routing_mode/file_access_mode
// are Core-authoritative platform switches. node/main.go reads all five with
// rdb.Get and writes none of them, node/beam_throttle.go likewise; verified by
// sweeping node/ for writes to each key.
func globalReadKeys() []string {
	return []string{
		"%R~dylaris:routing_mode", "%R~dylaris:file_access_mode",
		"%R~dylaris:placement:*",
		// Link sidecar update policy + check interval. Core-authoritative, read
		// by node/link_update.go via loadModesFromRedis and never written by the
		// node. Without them the reads return NOPERM and every node silently
		// falls back to the built-in default instead of the operator's setting.
		"%R~dylaris:link_update_policy", "%R~dylaris:link_update_interval_min",
		"%R~beam:bw_limit", "%R~beam:bw_up_internal", "%R~beam:bw_down_internal",
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
	}
}

// migrationKeys are the migration grants, split by what the node actually does
// with each sub-namespace instead of the single "~dylaris:migration:*" this
// replaced - which was read+write over the WHOLE namespace, so any node,
// tenant-owned BYON machines included, could rewrite any other server's
// migration state or forge a peer's transfer endpoint.
//
// Scoped by SUB-NAMESPACE rather than per assigned server on purpose: the ACL is
// rebuilt from ListServersByNode on a reconcile tick, so a server migrating IN is
// not yet in the destination node's list at the moment that node has to write its
// status. Per-server grants would make inbound migration fail until the next
// sweep. Sub-namespace scoping keeps the write surface to the three keys the node
// genuinely owns without depending on that timing.
//
// Verified against node/: it writes :status and :meta (migration_commands.go) and
// its own endpoint (migration_server.go), reads a peer's endpoint to pull from it,
// and never touches :orchestration or dylaris:migration:stream - so both stay out.
func migrationKeys(token string) []string {
	return []string{
		// Its own transfer endpoint. Peers' endpoints are readable because a pull
		// migration has to resolve the source node's address.
		"~dylaris:migration:endpoint:" + token,
		"%R~dylaris:migration:endpoint:*",
		// Progress it reports for the server it is moving, in either direction.
		"~dylaris:migration:*:status",
		"~dylaris:migration:*:meta",
		// Core-authoritative plan. The node reads nothing from it today; read-only
		// so that stays true by construction rather than by convention.
		"%R~dylaris:migration:*:orchestration",
	}
}

// BuildNodeACLRules returns the ACL SETUSER argument list (after the username)
// for the node agent user: own node-scoped keys + assigned server keys + global
// reads + pub/sub channels + command categories.
func BuildNodeACLRules(token, password string, serverUUIDs []string) []interface{} {
	rules := []interface{}{"on", ">" + password, "resetkeys", "resetchannels"}
	rules = append(rules, "~dylaris:node:"+token+":*", "~dylaris:discovery:"+token, "~beam:node-endpoint:"+token)
	// The node's own error stream, read by the panel via
	// services.ErrorStreamServices. Scoped to this node's token exactly like the
	// Link's, so one node cannot write into another's diagnostics.
	//
	// This is the node's ONLY channel that survives a broken control plane: when
	// the gRPC dial fails - a certificate pin that does not match, a Core that
	// refuses the proof - Core learns nothing at all, because the connection it
	// would have learned it from is the one that failed. The node still holds its
	// cached secret and can still reach Redis, so it is the only party in a
	// position to say why it looks offline.
	rules = append(rules, "~dylaris:errors:node:"+token)
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
	// SFTP password hashes for the users entitled to THIS node, written per node
	// by core/services/sftp_sync.go.
	//
	// This replaced "%R~sftp:auth:*", which was keyed by username alone and so
	// handed every node - a tenant's BYON machine included - the bcrypt hash of
	// every account on the platform, whether or not that user had anything on it.
	rules = append(rules, "%R~"+SFTPAuthKeyPrefix(token)+"*")
	for _, k := range migrationKeys(token) {
		rules = append(rules, k)
	}
	for _, u := range serverUUIDs {
		rules = append(rules, "~dylaris:server:"+u+":*")
	}
	for _, k := range globalReadKeys() {
		rules = append(rules, k)
	}
	// Backup + restore reporting, scoped to THIS node's channel.
	//
	// These were "&dylaris:backup:results" and "&dylaris:backup:restores" - one
	// fleet-wide name each, granted to every node including a tenant-owned BYON
	// machine. Core read the runId straight out of the payload, and Pub/Sub
	// carries no sender identity, so any node could close, complete or resize any
	// run in the fleet: marking a foreign run "success" fires the retention prune,
	// which deletes that job's older archives from storage, and the reported size
	// counts against the owner's backup quota.
	//
	// Per-token, like every other node-scoped name here, so Redis itself refuses a
	// cross-node publish. Core additionally re-derives the owning node from the run
	// and compares it with the token in the channel name.
	rules = append(rules,
		"&"+queue.BackupResultsChannel(token),
		"&"+queue.BackupRestoresChannel(token))
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
func BuildShipperACLRules(password, serverUUID string) []interface{} {
	rules := []interface{}{"on", ">" + password, "resetkeys", "resetchannels"}
	rules = append(rules, "~dylaris:server:"+serverUUID+":*")
	rules = append(rules, "&dylaris:server:"+serverUUID+":stats:live")
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
		// beam:cert:fingerprint:* is the twin of edge:cert:fingerprint:* above,
		// and it was missing. The Link pins the beam relay's self-signed
		// certificate against it exactly as it pins an edge's, and fails CLOSED
		// on a Redis error - so NOPERM here did not degrade to trust-on-first-use,
		// it made every beam tunnel attempt abort with a TLS alert, forever. The
		// relay logged "link auth read error: remote error: tls: bad certificate"
		// on a loop while the fingerprints matched perfectly, and beam over the
		// relay was simply dead on every node.
		"%R~sys:beams", "%R~beam:registry:*", "%R~beam:cert:fingerprint:*",
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
