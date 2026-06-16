package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// readNew pulls the next batch of new messages for the consumer (test helper).
func readNew(t *testing.T, c *Consumer) []redis.XMessage {
	t.Helper()
	res, err := c.rdb.XReadGroup(context.Background(), &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.name,
		Streams:  []string{c.stream, ">"},
		Count:    10,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatalf("XReadGroup: %v", err)
	}
	var out []redis.XMessage
	for _, st := range res {
		out = append(out, st.Messages...)
	}
	return out
}

func pendingCount(t *testing.T, c *Consumer) int64 {
	t.Helper()
	p, err := c.rdb.XPending(context.Background(), c.stream, c.group).Result()
	if err != nil {
		t.Fatalf("XPending: %v", err)
	}
	return p.Count
}

func TestPublishConsumeRoundTrip(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	c := NewConsumer(rdb, "q", "g", "c1")
	c.Block = 50 * time.Millisecond
	if err := c.EnsureGroup(ctx); err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	if _, err := Publish(ctx, rdb, "q", []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := make(chan string, 1)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.Run(runCtx, func(_ context.Context, data []byte) error { got <- string(data); return nil })

	select {
	case v := <-got:
		if v != "hello" {
			t.Fatalf("got %q, want hello", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestRetryThenAckOnRecovery(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	c := NewConsumer(rdb, "q", "g", "c1")
	if err := c.EnsureGroup(ctx); err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	if _, err := Publish(ctx, rdb, "q", []byte("x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := 0
	failOnce := func(_ context.Context, _ []byte) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}
		return nil
	}

	// First delivery: handler fails -> message left pending, not acked.
	for _, m := range readNew(t, c) {
		c.handleOne(ctx, m, failOnce)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
	if pendingCount(t, c) != 1 {
		t.Fatalf("pending=%d, want 1 after failure", pendingCount(t, c))
	}

	// Recovery reprocesses own pending; handler now succeeds -> acked.
	c.recoverPending(ctx, failOnce)
	if calls != 2 {
		t.Fatalf("calls=%d, want 2 after recovery", calls)
	}
	if pendingCount(t, c) != 0 {
		t.Fatalf("pending=%d, want 0 after successful retry", pendingCount(t, c))
	}
}

func TestDedupSkipsAlreadyProcessed(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	c := NewConsumer(rdb, "q", "g", "c1")
	if err := c.EnsureGroup(ctx); err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	if _, err := Publish(ctx, rdb, "q", []byte("y")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs := readNew(t, c)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]

	// Simulate "processed but ACK lost": the done-marker exists on redelivery.
	rdb.Set(ctx, c.doneKey(m.ID), "1", time.Hour)

	called := false
	c.handleOne(ctx, m, func(_ context.Context, _ []byte) error { called = true; return nil })
	if called {
		t.Fatal("handler ran for an already-processed message")
	}
	if pendingCount(t, c) != 0 {
		t.Fatalf("pending=%d, want 0 (dedup path must ACK)", pendingCount(t, c))
	}
}

func TestDeadLetterAfterMaxDeliveries(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	c := NewConsumer(rdb, "q", "g", "c1")
	c.MaxDeliveries = 2
	if err := c.EnsureGroup(ctx); err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	if _, err := Publish(ctx, rdb, "q", []byte("poison")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msgs := readNew(t, c)
	m := msgs[0]
	alwaysFail := func(_ context.Context, _ []byte) error { return errors.New("nope") }

	c.handleOne(ctx, m, alwaysFail) // attempt 1 -> pending
	if pendingCount(t, c) != 1 {
		t.Fatalf("pending=%d, want 1 after first failure", pendingCount(t, c))
	}
	c.handleOne(ctx, m, alwaysFail) // attempt 2 == MaxDeliveries -> dead-letter + ack

	if pendingCount(t, c) != 0 {
		t.Fatalf("pending=%d, want 0 after dead-letter", pendingCount(t, c))
	}
	n, err := rdb.XLen(ctx, c.deadKey()).Result()
	if err != nil {
		t.Fatalf("XLen dead: %v", err)
	}
	if n != 1 {
		t.Fatalf("dead-letter len=%d, want 1", n)
	}
}
