package services

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The DB and storage migration jobs are guarded by ONE cluster-wide Redis lock
// each, taken with SETNX at Start. Nothing else stops a second migration: the
// HTTP handlers add no check, and Start does not consult the persisted job. So
// the lock has to survive as long as the job, which is hours.
//
// It did not. Both heartbeats refreshed it with a bare EXPIRE, and EXPIRE on a
// missing key creates nothing (verified against the live Valkey: it returns 0
// and EXISTS stays 0). Redis here runs with save "" and appendonly no, so a
// restart wipes the lock while the job keeps running in-process - and the job
// record written beside it in the same heartbeat tick came back, because that
// one uses SET. Only the lock stayed gone, so a second Start succeeded.

const jobLockTestKey = "dylaris:test:joblock"

// The case the bare EXPIRE could not handle: the key is gone and the job is
// still running, so the lock has to come back.
func TestRefreshOrReclaimJobLock_ReclaimsAfterRedisLostTheKey(t *testing.T) {
	rdb := newQueueTestRedis(t)
	ctx := context.Background()

	ok, err := rdb.SetNX(ctx, jobLockTestKey, "job-1", time.Minute).Result()
	if err != nil || !ok {
		t.Fatalf("seed lock: ok=%v err=%v", ok, err)
	}

	// What a Redis restart does to an unpersisted key.
	if err := rdb.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	held, err := refreshOrReclaimJobLock(ctx, rdb, jobLockTestKey, "job-1", time.Minute)
	if err != nil {
		t.Fatalf("refresh after flush: %v", err)
	}
	if !held {
		t.Fatal("job-1 did not get its own lock back after the key was wiped")
	}

	// The point of holding it: a second migration must still be refused.
	second, err := rdb.SetNX(ctx, jobLockTestKey, "job-2", time.Minute).Result()
	if err != nil {
		t.Fatalf("second SETNX: %v", err)
	}
	if second {
		t.Fatal("a second job took the lock while the first was still running")
	}
}

// A lock another job holds must never be stolen, and the caller has to learn it
// was superseded rather than carry on believing it holds the lock.
func TestRefreshOrReclaimJobLock_DoesNotStealAnotherJobsLock(t *testing.T) {
	rdb := newQueueTestRedis(t)
	ctx := context.Background()

	if err := rdb.Set(ctx, jobLockTestKey, "job-2", time.Minute).Err(); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	held, err := refreshOrReclaimJobLock(ctx, rdb, jobLockTestKey, "job-1", time.Minute)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if held {
		t.Fatal("job-1 reported holding a lock owned by job-2")
	}

	owner, err := rdb.Get(ctx, jobLockTestKey).Result()
	if err != nil {
		t.Fatalf("read owner: %v", err)
	}
	if owner != "job-2" {
		t.Fatalf("owner overwritten: got %q, want job-2", owner)
	}
}

// The ordinary tick: our own lock gets its full TTL back.
func TestRefreshOrReclaimJobLock_ExtendsOwnLock(t *testing.T) {
	rdb := newQueueTestRedis(t)
	ctx := context.Background()

	if err := rdb.Set(ctx, jobLockTestKey, "job-1", 2*time.Second).Err(); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	held, err := refreshOrReclaimJobLock(ctx, rdb, jobLockTestKey, "job-1", time.Minute)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !held {
		t.Fatal("refreshing our own lock reported not-held")
	}

	ttl, err := rdb.TTL(ctx, jobLockTestKey).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 2*time.Second {
		t.Fatalf("TTL not extended: got %v, want ~1m", ttl)
	}
}

// Both heartbeats must go through the helper. A bare EXPIRE on either lock key
// is the exact regression this round fixed, and it is invisible at runtime
// until a Redis restart happens to coincide with a running migration.
func TestMigrationHeartbeatsDoNotRefreshLockWithBareExpire(t *testing.T) {
	cases := []struct {
		file    string
		lockKey string
	}{
		{"db_migration_job.go", "dbMigrationLockKey"},
		{"storage_migration_job.go", "storageMigrationLockKey"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			src, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			text := string(src)
			if strings.Contains(text, "Expire(context.Background(), "+tc.lockKey) ||
				strings.Contains(text, "Expire(ctx, "+tc.lockKey) {
				t.Errorf("%s refreshes %s with a bare EXPIRE, which cannot recreate the key once Redis has dropped it", tc.file, tc.lockKey)
			}
			if !strings.Contains(text, "refreshOrReclaimJobLock(context.Background(), s.redis, "+tc.lockKey) {
				t.Errorf("%s does not refresh %s through refreshOrReclaimJobLock", tc.file, tc.lockKey)
			}
		})
	}
}
