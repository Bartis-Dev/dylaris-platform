package services

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestLoadRebalanceView(t *testing.T) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	// Seed one decision (newest-first via LPUSH).
	rc.LPush(ctx, warpRebalanceDecisionsKey, `{"ts":1,"mode":"dry-run","applied":false,"moves":[{"pubkey":"p1","from":"L-a","to":"L-b","txBps":100}]}`)

	f := newFeatureFlagsForTest(map[string]string{"warp_rebalance_mode": "dry-run"})
	v := LoadRebalanceView(ctx, rc, f)
	if v.Mode != "dry-run" {
		t.Fatalf("mode = %q, want dry-run", v.Mode)
	}
	if len(v.Decisions) != 1 || v.Decisions[0].Moves[0].Pubkey != "p1" {
		t.Fatalf("decisions = %+v", v.Decisions)
	}
}
