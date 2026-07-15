package errlog

import (
	"context"
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

func TestStreamKey(t *testing.T) {
	tests := []struct {
		service    string
		instanceID string
		want       string
	}{
		{"gate", "gate-1", "dylaris:errors:gate:gate-1"},
		{"link", "node-abc-123", "dylaris:errors:link:node-abc-123"},
		{"hub", "", "dylaris:errors:hub:"},
	}
	for _, tt := range tests {
		l := New(nil, tt.service, tt.instanceID)
		if got := l.StreamKey(); got != tt.want {
			t.Errorf("New(%q,%q).StreamKey() = %q, want %q", tt.service, tt.instanceID, got, tt.want)
		}
	}
}

// TestWrite_NilGuard asserts that a nil *Logger, or a Logger constructed with
// a nil redis client, is a safe no-op rather than a panic. This matters
// because errlog is used defensively across services that may not always
// have a redis client wired up yet.
func TestWrite_NilGuard(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil *Logger.write panicked: %v", r)
		}
	}()
	var nilLogger *Logger
	nilLogger.write("ERROR", "test", "should not panic")

	l := New(nil, "svc", "inst")
	l.Error("src", "message")
	l.Warn("src", "message")
	l.Info("src", "message")
	l.Errorf("src", "formatted %d", 1)
	l.Warnf("src", "formatted %d", 2)
}

func TestErrorThenReadEntries(t *testing.T) {
	rdb := newTestRedis(t)
	l := New(rdb, "gate", "inst-1")

	l.Error("gate:handler", "first error")
	l.Warn("gate:handler", "second warning")

	entries, err := ReadEntries(rdb, l.StreamKey(), 10)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	// Newest first: the second write (WARN) must come before the first (ERROR).
	if entries[0].Level != "WARN" || entries[0].Message != "second warning" {
		t.Errorf("entries[0] = %+v, want level WARN message %q", entries[0], "second warning")
	}
	if entries[1].Level != "ERROR" || entries[1].Message != "first error" {
		t.Errorf("entries[1] = %+v, want level ERROR message %q", entries[1], "first error")
	}
	for i, e := range entries {
		if e.Source != "gate:handler" {
			t.Errorf("entries[%d].Source = %q, want %q", i, e.Source, "gate:handler")
		}
		if e.Timestamp == "" {
			t.Errorf("entries[%d].Timestamp is empty", i)
		}
	}
}

func TestReadEntries_SkipsUnparseableJunk(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	l := New(rdb, "gate", "inst-2")

	l.Error("gate:handler", "real entry")

	// A junk entry with no "data" field at all.
	if _, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: l.StreamKey(),
		Values: map[string]interface{}{"other": "junk"},
	}).Result(); err != nil {
		t.Fatalf("seed junk (missing data): %v", err)
	}

	// A junk entry whose "data" field is not valid JSON.
	if _, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: l.StreamKey(),
		Values: map[string]interface{}{"data": "not-json-at-all"},
	}).Result(); err != nil {
		t.Fatalf("seed junk (bad json): %v", err)
	}

	entries, err := ReadEntries(rdb, l.StreamKey(), 10)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1 (junk entries must be skipped): %+v", len(entries), entries)
	}
	if entries[0].Message != "real entry" {
		t.Errorf("entries[0].Message = %q, want %q", entries[0].Message, "real entry")
	}
}

func TestScanErrorStreams(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	gateA := New(rdb, "gate", "a")
	gateB := New(rdb, "gate", "b")
	link1 := New(rdb, "link", "1")

	gateA.Error("src", "a")
	gateB.Error("src", "b")
	link1.Error("src", "l")

	// An unrelated key that happens to share the "gate" substring but is not
	// a per-instance error stream should never be returned.
	if err := rdb.Set(ctx, "dylaris:errors:gate-summary", "x", time.Minute).Err(); err != nil {
		t.Fatalf("seed unrelated key: %v", err)
	}

	keys, err := ScanErrorStreams(rdb, "gate")
	if err != nil {
		t.Fatalf("ScanErrorStreams: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len(keys) = %d, want 2: %v", len(keys), keys)
	}
	found := map[string]bool{}
	for _, k := range keys {
		found[k] = true
	}
	if !found[gateA.StreamKey()] || !found[gateB.StreamKey()] {
		t.Fatalf("keys = %v, want both %q and %q", keys, gateA.StreamKey(), gateB.StreamKey())
	}
	if found[link1.StreamKey()] {
		t.Fatalf("keys = %v, must not include unrelated service stream %q", keys, link1.StreamKey())
	}
}
