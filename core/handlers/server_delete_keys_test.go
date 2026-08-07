package handlers

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestCleanupDeletedServerKeys is the regression guard for leftover per-server
// Redis state. Most per-server keys carry a TTL, but the log stream and the
// stats buffer are streams with no expiry and no other remover, so every
// deleted server used to leak two of them permanently.
func TestCleanupDeletedServerKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	const gone = "owner_deadbeef01"
	const kept = "owner_stillalive"

	// The two that never expired, one per sub-server, plus a TTL'd sibling.
	for _, k := range []string{
		"dylaris:server:" + gone + ":logs:survival",
		"dylaris:server:" + gone + ":logs:creative",
		"dylaris:server:" + gone + ":stats:buffer",
	} {
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: k, Values: map[string]any{"line": "x"}}).Err(); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	if err := rdb.Set(ctx, "dylaris:server:"+gone+":java-heap", "512", 0).Err(); err != nil {
		t.Fatalf("seed java-heap: %v", err)
	}
	// A surviving server, and a key that merely shares the prefix up to the
	// uuid, to prove the sweep is bounded by the exact uuid.
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: "dylaris:server:" + kept + ":logs:survival", Values: map[string]any{"line": "y"}}).Err(); err != nil {
		t.Fatalf("seed kept: %v", err)
	}
	if err := rdb.Set(ctx, "dylaris:server:"+gone+"_suffix:java-heap", "1", 0).Err(); err != nil {
		t.Fatalf("seed lookalike: %v", err)
	}

	h := &ServerHandler{state: &AppState{Redis: rdb}}
	h.cleanupDeletedServerKeys(ctx, gone)

	for _, k := range []string{
		"dylaris:server:" + gone + ":logs:survival",
		"dylaris:server:" + gone + ":logs:creative",
		"dylaris:server:" + gone + ":stats:buffer",
		"dylaris:server:" + gone + ":java-heap",
	} {
		if n, _ := rdb.Exists(ctx, k).Result(); n != 0 {
			t.Errorf("%s still present after cleanup", k)
		}
	}
	for _, k := range []string{
		"dylaris:server:" + kept + ":logs:survival",
		"dylaris:server:" + gone + "_suffix:java-heap",
	} {
		if n, _ := rdb.Exists(ctx, k).Result(); n != 1 {
			t.Errorf("%s was removed; the sweep must be bounded by the exact uuid", k)
		}
	}
}

// An empty uuid must sweep nothing: "dylaris:server::*" would be harmless, but
// the guard also covers a caller that lost the value.
func TestCleanupDeletedServerKeys_EmptyUUIDIsANoop(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	if err := rdb.Set(ctx, "dylaris:server:owner_x:java-heap", "1", 0).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := &ServerHandler{state: &AppState{Redis: rdb}}
	h.cleanupDeletedServerKeys(ctx, "   ")

	if n, _ := rdb.Exists(ctx, "dylaris:server:owner_x:java-heap").Result(); n != 1 {
		t.Error("an empty uuid removed keys")
	}
}
