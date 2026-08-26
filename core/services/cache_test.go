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

// Boot must honour a stored dedicated endpoint even when it is not answering.
//
// This is the whole reason Adopt exists. Reconfigure returns early on a failed
// ping without recording cfg, which is right for a save and was wrong for boot:
// c.cfg stayed empty, so client() handed back the BUS and every cached Modrinth
// response went to the control plane - precisely what configuring a dedicated
// cache moves it away from. The isolation held after a save and not after a
// restart.
func TestCacheAdoptKeepsAnUnreachableEndpointInsteadOfFallingBackToTheBus(t *testing.T) {
	bus, busClient := newTestRedis(t)
	dead, _ := newTestRedis(t)
	addr := dead.Addr()
	dead.Close() // configured at some point, not answering now

	c := NewCache(busClient)
	ctx := context.Background()

	if err := c.Adopt(ctx, CacheConfig{Addr: addr}); err == nil {
		t.Fatal("Adopt reported success for an endpoint that is not answering")
	}

	c.Set(ctx, "k", "v", time.Minute)
	if bus.Exists("k") {
		t.Fatal("wrote to the panel Redis: an operator who moved the cache off the control plane " +
			"had it moved back by a restart, silently")
	}
	if _, ok := c.Get(ctx, "k"); ok {
		t.Error("a dead endpoint returned a hit")
	}
}

// ...and it has to SAY so. The settings screen renders the address from the
// stored settings while the health line comes from here, so a status that
// described the bus put a green tick next to the dedicated host.
func TestCacheStatusAfterAdoptingAnUnreachableEndpointDescribesThatEndpoint(t *testing.T) {
	_, busClient := newTestRedis(t)
	dead, _ := newTestRedis(t)
	addr := dead.Addr()
	dead.Close()

	c := NewCache(busClient)
	_ = c.Adopt(context.Background(), CacheConfig{Addr: addr})

	st := c.Status(context.Background())
	if !st.Dedicated {
		t.Error("status reports the bus while a dedicated endpoint is configured")
	}
	if st.Healthy {
		t.Error("status reports healthy for an endpoint that is not answering")
	}
	if st.Addr != addr {
		t.Errorf("status addr = %q, want %q", st.Addr, addr)
	}
	if st.Error == "" {
		t.Error("status carries no reason, which is the only thing the screen can act on")
	}
}

// The save path keeps its opposite behaviour: an address that does not answer is
// refused and nothing changes. Typing a bad host must not take the working cache
// away.
func TestCacheReconfigureLeavesAWorkingCacheAloneWhenTheNewAddressIsDead(t *testing.T) {
	_, busClient := newTestRedis(t)
	good, _ := newTestRedis(t)
	dead, _ := newTestRedis(t)
	deadAddr := dead.Addr()
	dead.Close()

	c := NewCache(busClient)
	ctx := context.Background()
	if err := c.Reconfigure(ctx, CacheConfig{Addr: good.Addr()}); err != nil {
		t.Fatalf("configuring a reachable endpoint failed: %v", err)
	}

	if err := c.Reconfigure(ctx, CacheConfig{Addr: deadAddr}); err == nil {
		t.Fatal("accepted an endpoint that is not answering")
	}

	c.Set(ctx, "k", "v", time.Minute)
	if !good.Exists("k") {
		t.Error("a refused save moved the cache off the endpoint that was working")
	}
	if st := c.Status(ctx); st.Addr != good.Addr() {
		t.Errorf("status addr = %q, want the endpoint that is still in use (%q)", st.Addr, good.Addr())
	}
}
