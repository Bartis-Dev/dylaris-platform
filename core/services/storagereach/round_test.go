package storagereach

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dylaris-core/storage"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
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
	// LocalProvider to cross-discover each other at a 20ms retry cadence
	// (a 15x gap, well clear of the 5x floor a CI runner's scheduling jitter
	// needs), but short enough that a test where a peer never shows (so the
	// round must wait out the full deadline by design) does not turn into a
	// multi-second sleep.
	return RoundOptions{Deadline: 300 * time.Millisecond, PollEvery: 20 * time.Millisecond}
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
		RoundOptions{Deadline: 150 * time.Millisecond, PollEvery: 20 * time.Millisecond})

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

// TestRunRound_OnProgressStaysZeroWhenAPeerNeverAppears checks OnProgress's
// behavior on a round that never completes, not incremental delivery: the
// coordinator's own inline Probe blocks for the whole Deadline before the
// polling loop below it ever runs (core-ghost has no beacon to find), so
// OnProgress fires exactly once here, with the round's final verdict. See
// TestRunRound_OnProgressSequenceIsNonDecreasingAndEndsAtFullCount for the
// counterpart scenario where every participant reports.
func TestRunRound_OnProgressStaysZeroWhenAPeerNeverAppears(t *testing.T) {
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

// TestRunRound_OnProgressSequenceIsNonDecreasingAndEndsAtFullCount reuses the
// TestRunRound_PassesWhenEveryParticipantReports scenario - a peer that does
// report - and checks what OnProgress actually promises: it fires more than
// once, the confirmed count it streams never goes backwards, and it lands on
// the full participant count once the round is done.
//
// A naive version of this test (every op local and instant against
// miniredis) empirically always saw exactly one callback: the coordinator's
// own inline Probe already blocks until it has seen every peer or the
// deadline, on the same PollEvery cadence the outer polling loop uses, so by
// the time that loop ran its first iteration, core-b's own report had in
// practice always already landed. A single-element seen never exercises the
// non-decreasing loop and can't tell a real streaming counter from one that
// only ever fires once at the end - exactly the dead-counter bug this test
// exists to catch.
//
// To force a genuine multi-step sequence, core-b's report SET is delayed via
// the same miniredis pre-hook seam
// TestRunRound_DeletesTheStagedConfigWhenPublishFailsAfterStagingTheConfig
// uses, so the write lands well after the coordinator's first poll. Nothing
// else about core-b is touched: its own local-disk beacon exchange with
// core-a (what actually decides whether either side is confirmed) still
// happens at full speed, so the eventual result is still a clean pass - only
// the Redis write that makes core-b's result visible to the coordinator is
// held back. The report key embeds the round id, which RunRound only
// generates once it starts, so the hook matches by key suffix rather than by
// an exact key (unlike the currentRoundKey match in the publish-fails test,
// which is known up front).
//
// Margins: PollEvery is 20ms; the report delay is 150ms, a 7.5x gap, clear of
// this file's 5x floor, so at least one poll is guaranteed to run before the
// delayed report lands. Deadline is 1s, a 6.7x gap over the delay, so the
// round never expires while waiting for it. RunRound still returns as soon
// as both reports are in rather than waiting out the deadline, so the real
// cost of this test is roughly the 150ms delay, not the 1s cap.
func TestRunRound_OnProgressSequenceIsNonDecreasingAndEndsAtFullCount(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	root := t.TempDir()
	ctx := context.Background()

	const delayedReportKeySuffix = ":report:core-b"
	const reportDelay = 150 * time.Millisecond
	mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
		if cmd == "SET" && len(args) > 0 && strings.HasSuffix(args[0], delayedReportKeySuffix) {
			time.Sleep(reportDelay)
		}
		return false
	})

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

	var seen []int
	opts := RoundOptions{Deadline: time.Second, PollEvery: 20 * time.Millisecond}
	opts.OnProgress = func(r RoundResult) { seen = append(seen, r.Confirmed) }

	c := NewCoordinator(rdb, "core-a", sharedFactory(root))
	res, err := c.RunRound(ctx, Config{Backend: "path", Path: "/mnt/shared"},
		[]string{"core-a", "core-b"}, opts)
	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	if perr := <-done; perr != nil {
		t.Fatalf("participant: %v", perr)
	}
	if !res.OK || res.Confirmed != 2 {
		t.Fatalf("res = %+v, want a passing 2/2 round", res)
	}

	// This is the assertion the old, vacuous version could never make: with
	// core-b's report artificially held back, the counter must move in more
	// than one step, or a dead OnProgress (fired once, only at the very end)
	// would pass unnoticed.
	if len(seen) < 2 {
		t.Fatalf("seen = %v, want at least 2 OnProgress calls; a counter that only ever fires once at the end is indistinguishable from a dead one", seen)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Fatalf("seen = %v, want a non-decreasing sequence", seen)
		}
	}
	if got := seen[len(seen)-1]; got != 2 {
		t.Fatalf("final progress Confirmed = %d, want 2", got)
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
//
// Neither of the next two tests can actually detect a regression of the fix
// they are named after: both let publishRound succeed completely and only
// then exercise the timeout / cancellation return paths. Once publishRound
// has returned successfully, the cleanup defer covers every later return no
// matter where in RunRound it was registered - that part was never broken.
// The real defect the fix closed is a publishRound that fails PARTWAY
// through, after the config key (holding the S3 secret) is already written
// but before the function returns; see
// TestRunRound_DeletesTheStagedConfigWhenPublishFailsAfterStagingTheConfig
// below, which is the one that actually exercises that window.

func TestRunRound_DeletesTheStagedConfigWhenTheRoundFailsClosed(t *testing.T) {
	rdb := newReachTestRedis(t)
	ctx := context.Background()
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))

	res, err := c.RunRound(ctx, Config{Backend: "s3", S3Bucket: "b", S3SecretKey: "super-secret"},
		[]string{"core-a", "core-ghost"},
		RoundOptions{Deadline: 120 * time.Millisecond, PollEvery: 20 * time.Millisecond})
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
	// 120ms cancellation against a 20ms poll is a 6x gap, clear of the 5x
	// floor; Deadline stays a full 5s so it is never in the running to win
	// the race against the cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	res, err := c.RunRound(ctx, Config{Backend: "s3", S3Bucket: "b", S3SecretKey: "super-secret"},
		[]string{"core-a", "core-ghost"},
		RoundOptions{Deadline: 5 * time.Second, PollEvery: 20 * time.Millisecond})
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

// TestRunRound_DeletesTheStagedConfigWhenPublishFailsAfterStagingTheConfig
// injects the actual fault the cleanup-defer fix closed: a Redis write that
// fails AFTER the config key (holding the S3 secret) is staged but before
// publishRound returns. miniredis's Server().SetPreHook runs ahead of every
// command server-side, so it can fail one specific SET - the current-round
// key, which publishRound writes last - while every other command (the meta
// SET, the config SET, and the cleanup DEL) passes through untouched. This
// is a test-side seam only; no production code changes.
func TestRunRound_DeletesTheStagedConfigWhenPublishFailsAfterStagingTheConfig(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	c := NewCoordinator(rdb, "core-a", sharedFactory(t.TempDir()))

	mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
		if cmd == "SET" && len(args) > 0 && args[0] == currentRoundKey {
			peer.WriteError("SIMULATED: current-round write failed")
			return true
		}
		return false
	})

	_, err = c.RunRound(ctx, Config{Backend: "s3", S3Bucket: "b", S3SecretKey: "super-secret"},
		[]string{"core-a"}, fastRound())
	if err == nil {
		t.Fatal("RunRound: want an error when publishRound fails after staging the config")
	}

	// The round id is not returned on this failure path (RunRound returns a
	// zero-value RoundResult), so look for any leftover config key by
	// pattern rather than by a specific round id - there is only one round
	// in this test.
	leftover, err := rdb.Keys(ctx, keyPrefix+"round:*:config").Result()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(leftover) != 0 {
		t.Fatalf("staged config key(s) survived a publish that failed partway through: %v", leftover)
	}
}
