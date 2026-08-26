package services

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return mr, c
}

func TestCacheDefaultsToTheBus(t *testing.T) {
	bus, busClient := newTestRedis(t)
	c := NewCache(busClient)
	ctx := context.Background()

	c.Set(ctx, "k", "v", time.Minute)
	if got, ok := c.Get(ctx, "k"); !ok || got != "v" {
		t.Fatalf("Get = (%q,%v), want (v,true)", got, ok)
	}
	if !bus.Exists("k") {
		t.Error("the value should be in the bus Redis when no dedicated endpoint is set")
	}
	st := c.Status(ctx)
	if st.Dedicated || !st.Healthy {
		t.Errorf("status = %+v, want dedicated=false healthy=true", st)
	}
}

func TestCacheUsesTheDedicatedEndpointAndLeavesTheBusAlone(t *testing.T) {
	bus, busClient := newTestRedis(t)
	dedicated, _ := newTestRedis(t)

	c := NewCache(busClient)
	ctx := context.Background()
	if err := c.Reconfigure(ctx, CacheConfig{Addr: dedicated.Addr()}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	t.Cleanup(c.Close)

	c.Set(ctx, "k", "v", time.Minute)
	if !dedicated.Exists("k") {
		t.Error("the value should have gone to the dedicated endpoint")
	}
	// The whole point of moving the cache is that the control-plane Redis stops
	// carrying it.
	if bus.Exists("k") {
		t.Error("the bus Redis must not receive cache writes once a dedicated endpoint is set")
	}
	if got, ok := c.Get(ctx, "k"); !ok || got != "v" {
		t.Errorf("Get = (%q,%v), want (v,true)", got, ok)
	}

	st := c.Status(ctx)
	if !st.Dedicated || !st.Healthy || st.Addr != dedicated.Addr() {
		t.Errorf("status = %+v, want dedicated at %s", st, dedicated.Addr())
	}
}

func TestCacheRefusesAnEndpointItCannotReach(t *testing.T) {
	_, busClient := newTestRedis(t)
	c := NewCache(busClient)
	// 127.0.0.1:1 is reliably closed; the point is that Reconfigure reports the
	// failure instead of quietly leaving the cache pointed at nothing.
	if err := c.Reconfigure(context.Background(), CacheConfig{Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("Reconfigure accepted an unreachable endpoint")
	}
}

func TestCacheDoesNotFallBackToTheBusWhenTheDedicatedEndpointDies(t *testing.T) {
	// An operator who moved the cache off the control plane moved it on purpose.
	// Silently putting it back when the endpoint stops answering would undo that
	// without saying so, so the cache degrades to no cache instead.
	bus, busClient := newTestRedis(t)
	dedicated, _ := newTestRedis(t)

	c := NewCache(busClient)
	ctx := context.Background()
	if err := c.Reconfigure(ctx, CacheConfig{Addr: dedicated.Addr()}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	t.Cleanup(c.Close)

	dedicated.Close() // the endpoint goes away

	c.Set(ctx, "k", "v", time.Minute)
	if bus.Exists("k") {
		t.Error("a dead dedicated endpoint must not send cache writes back to the bus Redis")
	}
	if _, ok := c.Get(ctx, "k"); ok {
		t.Error("a dead endpoint should read as a cache miss, not as a hit from somewhere else")
	}
	if st := c.Status(ctx); st.Healthy || st.Error == "" {
		t.Errorf("status = %+v, want unhealthy with a reason", st)
	}
}

func TestCacheClearingTheAddressReturnsToTheBus(t *testing.T) {
	bus, busClient := newTestRedis(t)
	dedicated, _ := newTestRedis(t)

	c := NewCache(busClient)
	ctx := context.Background()
	if err := c.Reconfigure(ctx, CacheConfig{Addr: dedicated.Addr()}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	if err := c.Reconfigure(ctx, CacheConfig{}); err != nil {
		t.Fatalf("Reconfigure back to default: %v", err)
	}
	t.Cleanup(c.Close)

	c.Set(ctx, "k", "v", time.Minute)
	if !bus.Exists("k") {
		t.Error("clearing the address should put the cache back on the bus Redis")
	}
}

func TestCacheGetManyIsAlignedWithItsKeys(t *testing.T) {
	// The availability check indexes the result against its key list, so a short
	// slice would silently shift every answer onto the wrong project.
	_, busClient := newTestRedis(t)
	c := NewCache(busClient)
	ctx := context.Background()

	c.SetMany(ctx, map[string]string{"a": "1", "c": "0"}, time.Minute)
	got := c.GetMany(ctx, []string{"a", "b", "c"})
	if len(got) != 3 {
		t.Fatalf("GetMany returned %d entries for 3 keys", len(got))
	}
	if got[0] != "1" || got[1] != "" || got[2] != "0" {
		t.Errorf("GetMany = %v, want [1  0]", got)
	}
}

func TestCacheOperationsAreSafeWithNoClient(t *testing.T) {
	// A Cache built before Redis is up, or one whose endpoint is gone, must be a
	// no-op rather than a panic: a cache is never a request's dependency.
	c := NewCache(nil)
	ctx := context.Background()
	c.Set(ctx, "k", "v", time.Minute)
	c.SetMany(ctx, map[string]string{"k": "v"}, time.Minute)
	if _, ok := c.Get(ctx, "k"); ok {
		t.Error("a cache with no client reported a hit")
	}
	if got := c.GetMany(ctx, []string{"a", "b"}); len(got) != 2 {
		t.Errorf("GetMany returned %d entries for 2 keys", len(got))
	}
	if st := c.Status(ctx); st.Healthy {
		t.Error("a cache with no client reported healthy")
	}
}
