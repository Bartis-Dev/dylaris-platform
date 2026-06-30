package redisacl

// commandCats is the exhaustive category grant covering every command the node
// and log-shipper use: read/write/stream/pubsub/connection/transaction, minus
// dangerous/admin/scripting, plus explicit SCAN (SCAN is not in @dangerous;
// KEYS is and stays denied).
var commandCats = []string{
	"+@read", "+@write", "+@stream", "+@pubsub", "+@connection", "+@transaction",
	"-@dangerous", "-@admin", "-@scripting", "+scan",
}

// globalReadKeys are the shared keys the node reads (NOT the shipper).
func globalReadKeys() []string {
	return []string{
		"~dylaris:routing_mode", "~dylaris:file_access_mode",
		"~dylaris:placement:*",
		"~beam:bw_limit", "~beam:bw_up_internal", "~beam:bw_down_internal",
		"~dylaris:core:*",
		"~sftp:auth:*",
		"~dylaris:migration:*",
	}
}

// BuildNodeACLRules returns the ACL SETUSER argument list (after the username)
// for the node agent user: own node-scoped keys + assigned server keys + global
// reads + pub/sub channels + command categories.
func BuildNodeACLRules(token, password string, serverUUIDs []string) []interface{} {
	rules := []interface{}{"on", ">" + password, "resetkeys", "resetchannels"}
	rules = append(rules, "~dylaris:node:"+token+":*", "~dylaris:discovery:"+token)
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

// SetUserArgs prepends the ACL subcommand + username for rdb.Do(...).
func SetUserArgs(username string, rules []interface{}) []interface{} {
	out := make([]interface{}, 0, len(rules)+3)
	out = append(out, "ACL", "SETUSER", username)
	out = append(out, rules...)
	return out
}
