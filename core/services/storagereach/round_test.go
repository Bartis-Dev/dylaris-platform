package storagereach

import (
	"context"
	"errors"
	"testing"
	"time"

	"dylaris-core/storage"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newReachTestRedis is declared ONCE here and reused by every other test file
// in this package - never redeclare it.
func newReachTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// sharedFactory hands every Core a LocalProvider on the SAME root, which is
// what a genuinely shared mount looks like.
func sharedFactory(root string) ProviderFactory {
	return func(cfg Config) (storage.StorageProvider, error) {
		return &storage.LocalProvider{BasePath: root}, nil
	}
}

func fastRound() RoundOptions {
	// 300ms is ample margin for two real participants sharing a temp-dir
	// LocalProvider to cross-discover each other at a 10ms retry cadence, but
	// short enough that a test where a peer never shows (so the round must
	// wait out the full deadline by design) does not turn into a multi-second
	// sleep.
	return RoundOptions{Deadline: 300 * time.Millisecond, PollEvery: 10 * time.Millisecond}
}

func TestRunRound_SingleParticipantPassesWithoutPeers(t *testing.T) {
	rdb := newReachTestRedis(t)
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))

	res, err := c.RunRound(context.Background(), Config{Backend: "path", Path: "/mnt/shared"},
		[]string{"core-a"}, fastRound())

	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	if !res.OK || res.Confirmed != 1 || res.Total != 1 {
		t.Fatalf("res = %+v, want a passing 1/1 round", res)
	}
}

func TestRunRound_PassesWhenEveryParticipantReports(t *testing.T) {
	rdb := newReachTestRedis(t)
	root := t.TempDir()
	ctx := context.Background()

	// core-b runs as a participant as soon as the round appears, exactly like
	// the service listener does in production.
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 300; i++ {
			id, err := PendingRoundID(ctx, rdb)
			if err == nil && id != "" {
				done <- RunParticipant(ctx, rdb, "core-b", id, sharedFactory(root))
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- errors.New("core-b never saw a round")
	}()

	c := NewCoordinator(rdb, "core-a", sharedFactory(root))
	res, err := c.RunRound(ctx, Config{Backend: "path", Path: "/mnt/shared"},
		[]string{"core-a", "core-b"}, fastRound())
	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	if perr := <-done; perr != nil {
		t.Fatalf("participant: %v", perr)
	}

	if !res.OK || res.Confirmed != 2 {
		t.Fatalf("res = %+v, want a passing 2/2 round", res)
	}
}

func TestRunRound_FailsClosedWhenAParticipantNeverReports(t *testing.T) {
	rdb := newReachTestRedis(t)
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))

	res, err := c.RunRound(context.Background(), Config{Backend: "path", Path: "/mnt/shared"},
		[]string{"core-a", "core-ghost"},
		RoundOptions{Deadline: 150 * time.Millisecond, PollEvery: 10 * time.Millisecond})

	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	if res.OK {
		t.Fatal("OK = true although core-ghost never reported")
	}
	if got := len(res.Results); got != 2 {
		t.Fatalf("got %d results, want one per participant", got)
	}
	for _, r := range res.Results {
		if r.CoreID == "core-ghost" && r.Status != StatusNoResponse {
			t.Fatalf("core-ghost = %s, want no-response", r.Status)
		}
	}
}

func TestRunRound_ReportsProgressAsItCollects(t *testing.T) {
	rdb := newReachTestRedis(t)
	var seen []int
	opts := fastRound()
	opts.Deadline = 200 * time.Millisecond
	opts.OnProgress = func(r RoundResult) { seen = append(seen, r.Confirmed) }

	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))
	if _, err := c.RunRound(context.Background(), Config{Backend: "path", Path: "/mnt/shared"},
		[]string{"core-a", "core-ghost"}, opts); err != nil {
		t.Fatalf("RunRound: %v", err)
	}

	if len(seen) == 0 {
		t.Fatal("OnProgress was never called; the panel would have no live counter")
	}
	// core-ghost never runs a probe, so core-a can never prove it sees every
	// participant either (Aggregate's not-shared rule convicts a reporter
	// that cannot see ANY listed peer's beacon, not just the peer that stayed
	// silent - see aggregate_test.go's "not-shared names the invisible peer").
	// The counter must therefore stay truthfully at 0, not climb to a false
	// "1 confirmed" the operator would misread as partial success.
	if seen[len(seen)-1] != 0 {
		t.Errorf("final progress Confirmed = %d, want 0", seen[len(seen)-1])
	}
}

func TestRunRound_DeletesTheStagedConfigWhenItEnds(t *testing.T) {
	// The staged config carries the S3 secret. Leaving it in Redis past the
	// round is the one thing this transport must never do.
	rdb := newReachTestRedis(t)
	ctx := context.Background()
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))

	res, err := c.RunRound(ctx, Config{Backend: "s3", S3Bucket: "b", S3SecretKey: "super-secret"},
		[]string{"core-a"}, fastRound())
	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}

	n, err := rdb.Exists(ctx, roundConfigKey(res.RoundID)).Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if n != 0 {
		t.Fatal("the staged config (with the S3 secret) outlived the round")
	}
}

func TestRunRound_UsesAFreshUnguessableRoundID(t *testing.T) {
	rdb := newReachTestRedis(t)
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))
	cfg := Config{Backend: "path", Path: "/mnt/shared"}

	first, err := c.RunRound(context.Background(), cfg, []string{"core-a"}, fastRound())
	if err != nil {
		t.Fatalf("first round: %v", err)
	}
	second, err := c.RunRound(context.Background(), cfg, []string{"core-a"}, fastRound())
	if err != nil {
		t.Fatalf("second round: %v", err)
	}

	if first.RoundID == second.RoundID {
		t.Fatal("two rounds shared a round id; stale beacons would be accepted as proof")
	}
	if len(first.RoundID) < 16 {
		t.Errorf("RoundID = %q, want an unguessable token", first.RoundID)
	}
}

func TestRunParticipant_ReportsUnreachableWhenTheProviderWillNotBuild(t *testing.T) {
	// A Core holding credentials that do not work must say so rather than
	// silently not reporting, or the operator sees "no-response" and looks at
	// the wrong problem.
	rdb := newReachTestRedis(t)
	ctx := context.Background()
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))

	done := make(chan error, 1)
	go func() {
		for i := 0; i < 300; i++ {
			id, err := PendingRoundID(ctx, rdb)
			if err == nil && id != "" {
				done <- RunParticipant(ctx, rdb, "core-b", id, func(cfg Config) (storage.StorageProvider, error) {
					return nil, errors.New("invalid credentials")
				})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- errors.New("core-b never saw a round")
	}()

	res, err := c.RunRound(ctx, Config{Backend: "s3", S3Bucket: "b"},
		[]string{"core-a", "core-b"}, fastRound())
	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	<-done

	if res.OK {
		t.Fatal("OK = true although core-b could not build a provider")
	}
	for _, r := range res.Results {
		if r.CoreID != "core-b" {
			continue
		}
		if r.Status != StatusUnreachable {
			t.Fatalf("core-b = %s, want unreachable", r.Status)
		}
		if r.Detail == "" {
			t.Error("Detail is empty; the operator is not told why")
		}
	}
}

func TestRunParticipant_UnknownRoundIsNotAnError(t *testing.T) {
	// A round that expired between the publish and this Core reading it is
	// normal, not a failure: the coordinator already gave up on it.
	rdb := newReachTestRedis(t)
	if err := RunParticipant(context.Background(), rdb, "core-b", "gone", sharedFactory(t.TempDir())); err != nil {
		t.Fatalf("RunParticipant(expired round) = %v, want nil", err)
	}
}

func TestPendingRoundID_EmptyWhenNoRoundIsRunning(t *testing.T) {
	rdb := newReachTestRedis(t)
	id, err := PendingRoundID(context.Background(), rdb)
	if err != nil {
		t.Fatalf("PendingRoundID: %v", err)
	}
	if id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
}

// The tests below are additional to the brief's nine: they cover the two
// staged-config exit paths TestRunRound_DeletesTheStagedConfigWhenItEnds does
// not - a failed (not just a passing) round, and a caller-cancelled context.
// The config key carries the S3 secret, so leaving it behind on ANY exit path
// is a security bug, not a missed cleanup.

func TestRunRound_DeletesTheStagedConfigWhenTheRoundFailsClosed(t *testing.T) {
	rdb := newReachTestRedis(t)
	ctx := context.Background()
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))

	res, err := c.RunRound(ctx, Config{Backend: "s3", S3Bucket: "b", S3SecretKey: "super-secret"},
		[]string{"core-a", "core-ghost"},
		RoundOptions{Deadline: 60 * time.Millisecond, PollEvery: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	if res.OK {
		t.Fatal("OK = true although core-ghost never reported")
	}

	n, err := rdb.Exists(ctx, roundConfigKey(res.RoundID)).Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if n != 0 {
		t.Fatal("the staged config (with the S3 secret) outlived a failed round")
	}
}

func TestRunRound_DeletesTheStagedConfigWhenTheContextIsCancelled(t *testing.T) {
	// core-ghost never reports, so the round has no way to finish on its own
	// before the deadline; cancelling ctx partway through is what ends it,
	// hitting the outer loop's ctx.Done() return branch specifically.
	rdb := newReachTestRedis(t)
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	res, err := c.RunRound(ctx, Config{Backend: "s3", S3Bucket: "b", S3SecretKey: "super-secret"},
		[]string{"core-a", "core-ghost"},
		RoundOptions{Deadline: 5 * time.Second, PollEvery: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("RunRound: want an error for a cancelled context")
	}
	if res.RoundID == "" {
		t.Fatal("res.RoundID is empty; cannot check the staged config key")
	}

	// The Del itself uses context.WithoutCancel, so it must succeed even
	// though ctx above is already done; check against a fresh context.
	n, existsErr := rdb.Exists(context.Background(), roundConfigKey(res.RoundID)).Result()
	if existsErr != nil {
		t.Fatalf("Exists: %v", existsErr)
	}
	if n != 0 {
		t.Fatal("the staged config (with the S3 secret) outlived a cancelled round")
	}
}
