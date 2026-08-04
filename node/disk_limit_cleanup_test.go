package main

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestForgetDiskLimit_ClearsWhatRecordLeavesBehind pins the delete-path gap.
//
// recordDiskLimit writes with NO TTL, and its only delete fires when Core
// pushes "unlimited" (limitMB <= 0). Deleting a server never pushes that, so
// every deleted server left node:<node>:server:<uuid>:disk_limit_mb in Redis
// permanently - while the port and storage keys written alongside it were both
// released on the same path.
func TestForgetDiskLimit_ClearsWhatRecordLeavesBehind(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	const uuid = "srv-uuid-1"
	const otherUUID = "srv-uuid-2"

	recordDiskLimit(ctx, rdb, uuid, 4096)
	recordDiskLimit(ctx, rdb, otherUUID, 8192)

	if got := loadDiskLimit(ctx, rdb, uuid); got != 4096 {
		t.Fatalf("limit was not recorded: got %d", got)
	}

	forgetDiskLimit(ctx, rdb, uuid)

	if n, _ := rdb.Exists(ctx, diskLimitKey(uuid)).Result(); n != 0 {
		t.Error("the disk-limit key outlived the server")
	}
	// Scoped: a sibling server on the same node keeps its limit.
	if got := loadDiskLimit(ctx, rdb, otherUUID); got != 8192 {
		t.Errorf("sibling server lost its limit (got %d, want 8192)", got)
	}
}

// TestForgetDiskLimit_IsSafeWithoutRedisOrUUID matches recordDiskLimit's own
// guards: the delete path runs on a node that may have no Redis wired.
func TestForgetDiskLimit_IsSafeWithoutRedisOrUUID(t *testing.T) {
	forgetDiskLimit(context.Background(), nil, "srv")

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	forgetDiskLimit(context.Background(), rdb, "")
}

// TestRecordDiskLimit_UnlimitedStillClears is the pre-existing behaviour this
// must not have changed: pushing "unlimited" drops the cached value so the du
// path reports no limit rather than enforcing a stale one.
func TestRecordDiskLimit_UnlimitedStillClears(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	recordDiskLimit(ctx, rdb, "srv", 512)
	recordDiskLimit(ctx, rdb, "srv", 0)

	if got := loadDiskLimit(ctx, rdb, "srv"); got != 0 {
		t.Errorf("loadDiskLimit = %d, want 0 after an unlimited push", got)
	}
}
