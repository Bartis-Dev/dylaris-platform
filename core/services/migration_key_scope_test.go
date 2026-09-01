package services

import (
	"context"
	"strings"
	"testing"

	"dylaris-core/services/redisacl"
	"dylaris-pkg/queue"
)

// A migration phase must only be writable by the node that is being asked for
// it, and Redis has to be the thing that enforces that.
//
// The attack this closes: the node ACL granted "~dylaris:migration:*:status"
// with the wildcard standing for the SERVER UUID, read and write. Every node in
// the fleet - a machine a customer owns included - could therefore write the
// migration progress of any server on the platform, and Core read it as
// authority with no sender identity anywhere in the payload. A tenant looping a
// forged "transferred" for a neighbour's UUID during that neighbour's migration
// makes Core skip the transfer check, flip node_id to a target that never
// received the archive, and send migrate_cleanup to the source - which deletes
// the source copy. The neighbour's server is gone.
func TestMigrationStatusIsWritableOnlyByItsOwnNode(t *testing.T) {
	const victim, attacker = "node-victim", "node-attacker"
	const serverUUID = "3f2b9c7e-0000-4444-8888-aaaaaaaaaaaa"

	pattern := queue.MigrationNodeKeyPattern(attacker)
	victimKey := queue.MigrationStatusKey(victim, serverUUID)

	if matchesRedisPattern(pattern, victimKey) {
		t.Fatalf("the attacker's grant %q covers the victim's key %q; Redis would allow the forged write", pattern, victimKey)
	}
	if own := queue.MigrationStatusKey(attacker, serverUUID); !matchesRedisPattern(pattern, own) {
		t.Errorf("a node cannot write its OWN status key %q under grant %q", own, pattern)
	}
	// The target of a move reports "transferred" for a server it does not own
	// yet, which is why the wildcard could not simply be narrowed to the
	// servers a node currently holds. The key carries the reporter, not the
	// owner, so this has to keep working.
	if other := queue.MigrationStatusKey(attacker, "some-server-it-does-not-own-yet"); !matchesRedisPattern(pattern, other) {
		t.Error("a node cannot report on a server it is receiving; the target side of every migration would hang")
	}
}

// And the grant the node actually gets must be that pattern and nothing wider.
func TestNodeACLGrantsNoFleetWideMigrationWrite(t *testing.T) {
	rules := redisacl.BuildNodeACLRules("node-a", "pw", []string{"srv-1"})
	for _, r := range rules {
		s, ok := r.(string)
		if !ok || !strings.Contains(s, "migration") {
			continue
		}
		// A read-only grant (%R~) cannot forge anything.
		if strings.HasPrefix(s, "%R~") {
			continue
		}
		if strings.Contains(s, "*:status") || strings.Contains(s, "*:meta") {
			t.Errorf("the node still holds a fleet-wide migration WRITE grant: %q", s)
		}
	}
}

// matchesRedisPattern is Redis's own glob semantics, reduced to the one form
// these patterns use: a literal prefix ending in '*'. Deliberately strict - a
// pattern without a trailing star must match exactly - so a future grant with a
// star in the MIDDLE is not silently reported as safe by this test.
func matchesRedisPattern(pattern, key string) bool {
	if !strings.HasSuffix(pattern, "*") {
		return pattern == key
	}
	if strings.Count(pattern, "*") != 1 {
		return true // an unexpected shape: report a match so the test fails loudly
	}
	return strings.HasPrefix(key, strings.TrimSuffix(pattern, "*"))
}

// A node too old to know the new key name must say so, not just "timed out".
//
// The status key moved under the reporting node's token so Redis can refuse a
// forged phase. A node built before that writes the old fleet-wide name, which
// nothing reads any more - so the move fails, and the only clue an operator got
// was a bare timeout on a transfer that never started.
func TestATimeoutNamesAnOutdatedNode(t *testing.T) {
	rdb := newQueueTestRedis(t)
	ctx := context.Background()
	o := &MigrationOrchestrator{redis: rdb}

	t.Run("nothing reported at all is an ordinary timeout", func(t *testing.T) {
		if got := o.timeoutReason(ctx, "", "srv-quiet"); got != "timed out" {
			t.Errorf("timeoutReason = %q, want a plain timeout", got)
		}
	})

	t.Run("an old node reporting under the legacy key is named", func(t *testing.T) {
		if err := rdb.Set(ctx, queue.LegacyMigrationStatusKey("srv-old"), `{"phase":"staged"}`, 0).Err(); err != nil {
			t.Fatal(err)
		}
		got := o.timeoutReason(ctx, "", "srv-old")
		if !strings.Contains(got, "too old") {
			t.Errorf("timeoutReason = %q, want it to name the outdated node", got)
		}
	})

	t.Run("a node that DID report keeps the plain timeout", func(t *testing.T) {
		// It answered on the current key, so the legacy one is somebody else's
		// leftover and must not change the message.
		if err := rdb.Set(ctx, queue.LegacyMigrationStatusKey("srv-both"), `{"phase":"staged"}`, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if got := o.timeoutReason(ctx, "staged", "srv-both"); got != "timed out" {
			t.Errorf("timeoutReason = %q, want a plain timeout", got)
		}
	})
}
