package xdp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

// TestDefaults pins the firewall's safe defaults exactly as the source
// defines them. A silent change here is a security regression (e.g. a wider
// protected-ports list or a looser rate limit shipping unnoticed).
func TestDefaults(t *testing.T) {
	got := Defaults()
	want := Config{
		Enabled:                false,
		HostMode:               false,
		Interface:              "",
		ProtectedPorts:         "25565",
		RateLimit:              1000,
		RateWindowMs:           1000,
		BanDurationMin:         30,
		MCMalformedLimit:       20,
		MCMalformedWindowMin:   2,
		MCInvalidHostLimit:     100,
		MCInvalidHostWindowMin: 2,
		MCBanDurationMin:       5,
		Whitelist:              "",
	}
	if got != want {
		t.Fatalf("Defaults() = %+v, want %+v", got, want)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	c := Config{
		Enabled:                true,
		HostMode:               true,
		Interface:              "eth0",
		ProtectedPorts:         "25565,25566",
		RateLimit:              500,
		RateWindowMs:           2000,
		BanDurationMin:         60,
		MCMalformedLimit:       10,
		MCMalformedWindowMin:   5,
		MCInvalidHostLimit:     50,
		MCInvalidHostWindowMin: 5,
		MCBanDurationMin:       15,
		Whitelist:              "10.0.0.1,10.0.0.2",
	}

	if err := Save(ctx, rdb, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, found, err := Load(ctx, rdb)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("Load: found = false, want true")
	}
	if got != c {
		t.Fatalf("Load() = %+v, want %+v", got, c)
	}
}

func TestLoad_KeyAbsent(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	got, found, err := Load(ctx, rdb)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if found {
		t.Fatal("Load: found = true, want false when key is absent")
	}
	if got != Defaults() {
		t.Fatalf("Load() = %+v, want Defaults() %+v", got, Defaults())
	}
}

func TestLoad_BadJSON(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	if err := rdb.Set(ctx, RedisConfigKey, "{not valid json", 0).Err(); err != nil {
		t.Fatalf("seed bad json: %v", err)
	}

	got, found, err := Load(ctx, rdb)
	if err == nil {
		t.Fatal("Load: expected error for malformed JSON")
	}
	if !strings.HasPrefix(err.Error(), "xdp config: parse:") {
		t.Fatalf("Load error = %q, want prefix %q", err.Error(), "xdp config: parse:")
	}
	if found {
		t.Fatal("Load: found = true, want false on parse error")
	}
	if got != Defaults() {
		t.Fatalf("Load() on parse error = %+v, want Defaults() %+v", got, Defaults())
	}
}

// TestConfig_JSONRoundTrip is a pure marshal/unmarshal check (no redis)
// confirming every field survives the wire format the gateway side also
// depends on.
func TestConfig_JSONRoundTrip(t *testing.T) {
	c := Config{
		Enabled:                true,
		HostMode:               false,
		Interface:              "wg0",
		ProtectedPorts:         "25565",
		RateLimit:              12345,
		RateWindowMs:           6789,
		BanDurationMin:         42,
		MCMalformedLimit:       7,
		MCMalformedWindowMin:   3,
		MCInvalidHostLimit:     8,
		MCInvalidHostWindowMin: 4,
		MCBanDurationMin:       9,
		Whitelist:              "192.168.1.0/24",
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != c {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, c)
	}
}
