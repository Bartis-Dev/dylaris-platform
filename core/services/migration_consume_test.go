package services

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// failFastRedis points a client at a port nothing is listening on, with
// go-redis's own dial retries disabled.
//
// Disabling them is the point. Against the stock client each attempt costs
// about 2.1s, because go-redis retries the dial internally before returning -
// measured, not assumed - which is slow enough to hide the difference between a
// paced loop and an unpaced one inside any reasonable test window. Stripping
// that away leaves the loop's OWN pacing as the only thing setting the rate,
// which is exactly what this test is about.
func failFastRedis(t *testing.T) *redis.Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatalf("close the reserved port: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr:        addr,
		MaxRetries:  -1,
		DialTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestConsumeDoesNotHotLoopOnAnUnreachableRedis is the test the backoff exists
// for. The queue consumer returns its setup error immediately when it cannot
// reach Redis, so before the backoff this loop retried as fast as that error
// came back, spawning a leadership-watch goroutine per turn and never widening
// the gap however long the outage lasted.
//
// The orchestrator is built with a nil leader on purpose: that is the branch
// with no other delay in it, so it is the worst case rather than the average.
func TestConsumeDoesNotHotLoopOnAnUnreachableRedis(t *testing.T) {
	o := &MigrationOrchestrator{redis: failFastRedis(t)}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	o.consume(ctx)

	attempts := strings.Count(buf.String(), "retrying in")

	if attempts == 0 {
		t.Fatal("consume logged no retries against an unreachable Redis, so this test is not exercising the retry path")
	}
	// Backed off, the delays are 1s then 2s, so a 3s window holds two or three
	// attempts. Unpaced, each turn costs only the 50ms dial, which is two
	// orders of magnitude more. The bound sits far above the expected count so
	// timing variation cannot fail it, and far below the unpaced count so the
	// two remain clearly separated.
	if attempts > 10 {
		t.Fatalf("consume retried %d times in 3s, want a backed-off handful (the loop is spinning)", attempts)
	}
	t.Logf("retry attempts in 3s: %d", attempts)
}
