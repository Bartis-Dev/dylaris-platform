package quota

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestExceedsSizeCap(t *testing.T) {
	cases := []struct {
		name           string
		size, capBytes int64
		want           bool
	}{
		{"zero cap is unlimited", 999999, 0, false},
		{"negative cap is unlimited", 999999, -1, false},
		{"under the cap", 100, 200, false},
		{"exactly at the cap", 200, 200, false},
		{"one byte over", 201, 200, true},
		{"empty upload under cap", 0, 200, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExceedsSizeCap(c.size, c.capBytes); got != c.want {
				t.Errorf("ExceedsSizeCap(%d, %d) = %v, want %v", c.size, c.capBytes, got, c.want)
			}
		})
	}
}

func TestExceedsDaily(t *testing.T) {
	cases := []struct {
		name                  string
		used, limit, incoming int64
		want                  bool
	}{
		{"zero limit is unlimited", 100, 0, 999999, false},
		{"negative limit is unlimited", 100, -1, 999999, false},
		{"fits exactly at limit", 100, 200, 100, false},
		{"one byte over", 100, 200, 101, true},
		{"already at limit, empty upload", 200, 200, 0, false},
		{"already over", 300, 200, 0, true},
		{"comfortable headroom", 50, 1000, 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExceedsDaily(c.used, c.limit, c.incoming); got != c.want {
				t.Errorf("ExceedsDaily(%d, %d, %d) = %v, want %v", c.used, c.limit, c.incoming, got, c.want)
			}
		})
	}
}

// TestDailyKey pins the counter key format and, critically, that the day is
// normalized to UTC so node and core (possibly in different timezones) share the
// same window.
func TestDailyKey(t *testing.T) {
	cases := []struct {
		name     string
		username string
		day      time.Time
		want     string
	}{
		{
			name:     "utc midday",
			username: "alice",
			day:      time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC),
			want:     "dylaris:beam:daily:alice:2026-07-21",
		},
		{
			name:     "positive offset rolls back to prior utc day",
			username: "bob",
			day:      time.Date(2026, 7, 22, 0, 30, 0, 0, time.FixedZone("X", 2*3600)),
			want:     "dylaris:beam:daily:bob:2026-07-21",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DailyKey(c.username, c.day); got != c.want {
				t.Errorf("DailyKey(%q, %v) = %q, want %q", c.username, c.day, got, c.want)
			}
		})
	}
}

func newTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()}), mr
}

func TestMaxUploadCap(t *testing.T) {
	ctx := context.Background()
	rdb, mr := newTestRedis(t)

	if got := MaxUploadCap(ctx, rdb); got != 0 {
		t.Errorf("no cap set: got %d, want 0", got)
	}
	mr.Set(MaxUploadBytesKey, "4096")
	if got := MaxUploadCap(ctx, rdb); got != 4096 {
		t.Errorf("cap set: got %d, want 4096", got)
	}
	mr.Set(MaxUploadBytesKey, "not-a-number")
	if got := MaxUploadCap(ctx, rdb); got != 0 {
		t.Errorf("unparseable cap: got %d, want 0", got)
	}
	if got := MaxUploadCap(ctx, nil); got != 0 {
		t.Errorf("nil rdb: got %d, want 0", got)
	}
}

func TestCheckSizeCap(t *testing.T) {
	ctx := context.Background()
	rdb, mr := newTestRedis(t)

	if ok, _ := CheckSizeCap(ctx, rdb, 1<<30); !ok {
		t.Error("no cap configured should allow")
	}
	mr.Set(MaxUploadBytesKey, "1000")
	if ok, capB := CheckSizeCap(ctx, rdb, 1001); ok || capB != 1000 {
		t.Errorf("over cap: ok=%v cap=%d, want ok=false cap=1000", ok, capB)
	}
	if ok, _ := CheckSizeCap(ctx, rdb, 1000); !ok {
		t.Error("exactly at cap should allow")
	}
	if ok, _ := CheckSizeCap(ctx, nil, 1<<40); !ok {
		t.Error("nil rdb should allow")
	}
}

func TestCheckDailyQuota(t *testing.T) {
	ctx := context.Background()
	rdb, mr := newTestRedis(t)

	if ok, _, _ := CheckDailyQuota(ctx, rdb, "alice", 1<<30); !ok {
		t.Error("no limit configured should allow")
	}
	mr.Set(DailyUploadBytesKey, "1000")
	if ok, used, limit := CheckDailyQuota(ctx, rdb, "alice", 500); !ok || used != 0 || limit != 1000 {
		t.Errorf("no counter yet + under limit: ok=%v used=%d limit=%d, want ok=true used=0 limit=1000", ok, used, limit)
	}
	mr.Set(DailyKey("alice", time.Now()), "800")
	if ok, used, _ := CheckDailyQuota(ctx, rdb, "alice", 300); ok || used != 800 {
		t.Errorf("used 800 + incoming 300 vs limit 1000: ok=%v used=%d, want ok=false used=800", ok, used)
	}
	if ok, _, _ := CheckDailyQuota(ctx, rdb, "", 1<<30); !ok {
		t.Error("empty username should allow (never a shared bucket)")
	}
	if ok, _, _ := CheckDailyQuota(ctx, nil, "alice", 1<<30); !ok {
		t.Error("nil rdb should allow")
	}
}

func TestRecordDailyUsage(t *testing.T) {
	ctx := context.Background()
	rdb, mr := newTestRedis(t)
	key := DailyKey("bob", time.Now())

	RecordDailyUsage(ctx, rdb, "bob", 100)
	RecordDailyUsage(ctx, rdb, "bob", 50)
	if got, err := rdb.Get(ctx, key).Int64(); err != nil || got != 150 {
		t.Fatalf("counter = %d (err %v), want 150", got, err)
	}
	if ttl := mr.TTL(key); ttl <= 0 {
		t.Errorf("TTL not armed: %v", ttl)
	}

	// No-ops must not touch the counter.
	RecordDailyUsage(ctx, rdb, "", 100)    // empty username
	RecordDailyUsage(ctx, nil, "bob", 100) // nil rdb
	RecordDailyUsage(ctx, rdb, "bob", 0)   // non-positive
	if got, _ := rdb.Get(ctx, key).Int64(); got != 150 {
		t.Errorf("a no-op changed the counter to %d, want 150", got)
	}
}
