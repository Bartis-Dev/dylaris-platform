package services

import (
	"context"
	"testing"
	"time"
)

// Run refuses to start while the status says Running, so that flag decides
// whether a routing-mode switch is possible at all. Only two things ever clear
// it: the end of runBatches, and the deferred recover() - and a recover covers a
// panic, not a SIGKILL, an OOM or a container restart. Written with the same
// 24h TTL as the finished result, a Core killed mid-run therefore reported a
// migration that no longer existed and blocked every further switch for a day,
// recoverable only by deleting a Redis key by hand.
//
// So a running status has to be a claim that expires with its writer.
func TestWriteStatus_RunningStatusExpiresWithTheProcessThatWroteIt(t *testing.T) {
	rdb := newQueueTestRedis(t)
	m := &RoutingMigrationService{redis: rdb}
	ctx := context.Background()

	m.writeStatus(ctx, MigrationStatus{Running: true, Total: 40})

	ttl, err := rdb.TTL(ctx, routingMigrationKey).Result()
	if err != nil {
		t.Fatalf("TTL %s: %v", routingMigrationKey, err)
	}
	if ttl <= 0 {
		t.Fatalf("running status has no expiry (TTL %s): a crashed run blocks every later switch", ttl)
	}
	if ttl > routingMigrationRunningTTL {
		t.Fatalf("running status TTL = %s, want at most %s", ttl, routingMigrationRunningTTL)
	}
}

// The other half: once the run is over the record is a result, not a claim, and
// the panel still has to be able to show what happened.
func TestWriteStatus_FinishedStatusIsKeptForThePanel(t *testing.T) {
	rdb := newQueueTestRedis(t)
	m := &RoutingMigrationService{redis: rdb}
	ctx := context.Background()

	m.writeStatus(ctx, MigrationStatus{Running: false, Total: 40, Done: 38, Failed: 2})

	ttl, err := rdb.TTL(ctx, routingMigrationKey).Result()
	if err != nil {
		t.Fatalf("TTL %s: %v", routingMigrationKey, err)
	}
	if ttl <= routingMigrationRunningTTL {
		t.Fatalf("finished status TTL = %s, want the full %s so the result survives", ttl, routingMigrationStatusTTL)
	}
}

// updateProgress is what keeps a live migration's claim fresh, so it must write
// the short TTL too rather than quietly restoring the long one.
func TestUpdateProgress_KeepsTheRunningStatusShortLived(t *testing.T) {
	rdb := newQueueTestRedis(t)
	m := &RoutingMigrationService{redis: rdb}
	ctx := context.Background()

	m.writeStatus(ctx, MigrationStatus{Running: true, Total: 40})
	m.updateProgress(ctx, 4, 0, 40)

	ttl, err := rdb.TTL(ctx, routingMigrationKey).Result()
	if err != nil {
		t.Fatalf("TTL %s: %v", routingMigrationKey, err)
	}
	if ttl <= 0 || ttl > routingMigrationRunningTTL {
		t.Fatalf("TTL after a progress update = %s, want a fresh window of at most %s", ttl, routingMigrationRunningTTL)
	}
	if got := m.readStatus(ctx); !got.Running || got.Done != 4 {
		t.Fatalf("progress update lost the state: %+v", got)
	}
}

// The margin that makes the short TTL safe. The longest a live run can go
// without writing the status is the last server of one batch settling, then the
// pause, then the first server of the next batch settling. A TTL below that
// would expire under a healthy migration and let a second one start on top of
// it, which is worse than the bug it replaces.
func TestRoutingMigrationRunningTTLOutlastsTheLongestSilence(t *testing.T) {
	longestSilence := 2*routingRedeploySettleTimeout + routingBatchPause
	if routingMigrationRunningTTL <= longestSilence {
		t.Fatalf("routingMigrationRunningTTL = %s, must exceed the %s a healthy run can go without an update",
			routingMigrationRunningTTL, longestSilence)
	}
}

// The named constants have to be the ones the code actually uses, or the
// assertion above measures nothing. Guards against the literals creeping back.
func TestRoutingMigrationTimingConstantsAreTheOnesInUse(t *testing.T) {
	if routingRedeploySettleTimeout != 60*time.Second {
		t.Errorf("routingRedeploySettleTimeout = %s, want the 60s redeployServer polls for", routingRedeploySettleTimeout)
	}
	if routingBatchPause != 15*time.Second {
		t.Errorf("routingBatchPause = %s, want the 15s gap runBatches sleeps for", routingBatchPause)
	}
}
