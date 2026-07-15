package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// trafficFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods TrafficAggregator touches are
// overridden. Any other call would panic - the tests never make one.
type trafficFakeStore struct {
	store.Store

	owners     map[string]string
	ownersErr  error
	ownersCall int

	backupBytes    map[string]int64
	backupBytesErr error

	settings map[string]string

	addUsageCalls   []trafficUsageCall
	addUsageErrFor  map[string]error
	setBackupCalls  []trafficBackupCall
	setBackupErrFor map[string]error
}

type trafficUsageCall struct {
	tenant           string
	period           time.Time
	edge, relayBytes int64
}
type trafficBackupCall struct {
	tenant string
	period time.Time
	bytes  int64
}

func (f *trafficFakeStore) TenantServerOwners() (map[string]string, error) {
	f.ownersCall++
	return f.owners, f.ownersErr
}

func (f *trafficFakeStore) TenantBackupBytes() (map[string]int64, error) {
	return f.backupBytes, f.backupBytesErr
}

func (f *trafficFakeStore) AddTrafficUsage(userID string, period time.Time, edgeBytes, relayBytes int64) error {
	f.addUsageCalls = append(f.addUsageCalls, trafficUsageCall{userID, period, edgeBytes, relayBytes})
	if f.addUsageErrFor != nil {
		return f.addUsageErrFor[userID]
	}
	return nil
}

func (f *trafficFakeStore) SetTrafficBackupBytes(userID string, period time.Time, backupBytes int64) error {
	f.setBackupCalls = append(f.setBackupCalls, trafficBackupCall{userID, period, backupBytes})
	if f.setBackupErrFor != nil {
		return f.setBackupErrFor[userID]
	}
	return nil
}

func (f *trafficFakeStore) GetSetting(key string) (string, error) {
	v, ok := f.settings[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func newTrafficAggregatorTest(t *testing.T, fs *trafficFakeStore, byonEnabled bool) (*TrafficAggregator, *redis.Client) {
	t.Helper()
	rdb := newQueueTestRedis(t)
	if fs.settings == nil {
		fs.settings = map[string]string{}
	}
	fs.settings["feature_byon_enabled"] = "false"
	if byonEnabled {
		fs.settings["feature_byon_enabled"] = "true"
	}
	flags := NewFeatureFlags(fs)
	agg := NewTrafficAggregator(fs, rdb, flags)
	return agg, rdb
}

func mustHSet(t *testing.T, rdb *redis.Client, key string, fields map[string]interface{}) {
	t.Helper()
	if err := rdb.HSet(context.Background(), key, fields).Err(); err != nil {
		t.Fatalf("HSet %s: %v", key, err)
	}
}

func mustSet(t *testing.T, rdb *redis.Client, key, value string) {
	t.Helper()
	if err := rdb.Set(context.Background(), key, value, 0).Err(); err != nil {
		t.Fatalf("Set %s: %v", key, err)
	}
}

func assertRedisString(t *testing.T, rdb *redis.Client, key, want string) {
	t.Helper()
	got, err := rdb.Get(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func TestTrafficAggregator_BYONDisabled_NoOp(t *testing.T) {
	fs := &trafficFakeStore{owners: map[string]string{"srv-1": "tenant-a"}}
	agg, _ := newTrafficAggregatorTest(t, fs, false)

	agg.runOnce(context.Background())

	if fs.ownersCall != 0 {
		t.Errorf("TenantServerOwners called %d times, want 0 when BYON is disabled", fs.ownersCall)
	}
	if len(fs.addUsageCalls) != 0 {
		t.Errorf("expected no AddTrafficUsage calls, got %+v", fs.addUsageCalls)
	}
}

func TestTrafficAggregator_NoTenants_NoOp(t *testing.T) {
	fs := &trafficFakeStore{owners: map[string]string{}}
	agg, _ := newTrafficAggregatorTest(t, fs, true)

	agg.runOnce(context.Background())

	if len(fs.addUsageCalls) != 0 {
		t.Errorf("expected no AddTrafficUsage calls with zero tenants, got %+v", fs.addUsageCalls)
	}
	if len(fs.setBackupCalls) != 0 {
		t.Errorf("expected no SetTrafficBackupBytes calls with zero tenants, got %+v", fs.setBackupCalls)
	}
}

func TestTrafficAggregator_ComputesDeltaAcrossEdgeAndRelay(t *testing.T) {
	fs := &trafficFakeStore{
		owners: map[string]string{
			"srv-uuid-1": "tenant-a",
			"srv-uuid-2": "tenant-b",
		},
		backupBytes: map[string]int64{},
	}
	agg, rdb := newTrafficAggregatorTest(t, fs, true)
	ctx := context.Background()

	mustHSet(t, rdb, "dylaris:traffic:edge:srv-uuid-1", map[string]interface{}{"rx": "1000", "tx": "2000"}) // current 3000
	mustSet(t, rdb, "dylaris:traffic:agg:seen:edge:srv-uuid-1", "1000")                                     // seen 1000 -> delta 2000

	mustHSet(t, rdb, "dylaris:traffic:edge:srv-uuid-2", map[string]interface{}{"rx": "500"}) // current 500, no seen -> delta 500

	mustHSet(t, rdb, "dylaris:traffic:relay:srv-uuid-1", map[string]interface{}{"up": "100", "down": "200"}) // current 300, no seen -> delta 300

	// Not in owners -> must not be billed to anyone.
	mustHSet(t, rdb, "dylaris:traffic:edge:unknown-uuid", map[string]interface{}{"rx": "999"})

	agg.runOnce(ctx)

	byTenant := map[string]trafficUsageCall{}
	for _, c := range fs.addUsageCalls {
		byTenant[c.tenant] = c
	}
	if len(byTenant) != 2 {
		t.Fatalf("addUsageCalls = %+v, want exactly tenant-a and tenant-b", fs.addUsageCalls)
	}
	if a := byTenant["tenant-a"]; a.edge != 2000 || a.relayBytes != 300 {
		t.Errorf("tenant-a usage = %+v, want edge=2000 relay=300", a)
	}
	if b := byTenant["tenant-b"]; b.edge != 500 || b.relayBytes != 0 {
		t.Errorf("tenant-b usage = %+v, want edge=500 relay=0", b)
	}

	wantPeriod := monthStartUTC(time.Now())
	if !byTenant["tenant-a"].period.Equal(wantPeriod) {
		t.Errorf("period = %v, want %v", byTenant["tenant-a"].period, wantPeriod)
	}

	// Seen keys advance to the new "current" totals so the same bytes are not re-billed.
	assertRedisString(t, rdb, "dylaris:traffic:agg:seen:edge:srv-uuid-1", "3000")
	assertRedisString(t, rdb, "dylaris:traffic:agg:seen:edge:srv-uuid-2", "500")
	assertRedisString(t, rdb, "dylaris:traffic:agg:seen:relay:srv-uuid-1", "300")
}

func TestTrafficAggregator_NegativeDeltaTreatedAsCounterReset(t *testing.T) {
	fs := &trafficFakeStore{
		owners:      map[string]string{"srv-uuid-1": "tenant-a"},
		backupBytes: map[string]int64{},
	}
	agg, rdb := newTrafficAggregatorTest(t, fs, true)
	ctx := context.Background()

	// seen (5000) > current (200): the counter key must have expired and been
	// recreated from zero - the whole current value is billed, not a negative delta.
	mustHSet(t, rdb, "dylaris:traffic:edge:srv-uuid-1", map[string]interface{}{"rx": "200"})
	mustSet(t, rdb, "dylaris:traffic:agg:seen:edge:srv-uuid-1", "5000")

	agg.runOnce(ctx)

	if len(fs.addUsageCalls) != 1 || fs.addUsageCalls[0].edge != 200 {
		t.Fatalf("addUsageCalls = %+v, want a single call with edge=200", fs.addUsageCalls)
	}
}

func TestTrafficAggregator_ZeroDeltaSkipped(t *testing.T) {
	fs := &trafficFakeStore{
		owners:      map[string]string{"srv-uuid-1": "tenant-a"},
		backupBytes: map[string]int64{},
	}
	agg, rdb := newTrafficAggregatorTest(t, fs, true)
	ctx := context.Background()

	mustHSet(t, rdb, "dylaris:traffic:edge:srv-uuid-1", map[string]interface{}{"rx": "500"})
	mustSet(t, rdb, "dylaris:traffic:agg:seen:edge:srv-uuid-1", "500") // delta = 0

	agg.runOnce(ctx)

	if len(fs.addUsageCalls) != 0 {
		t.Errorf("expected no billing call for a zero delta, got %+v", fs.addUsageCalls)
	}
}

func TestTrafficAggregator_AddTrafficUsageError_LeavesSeenUntouchedForRetry(t *testing.T) {
	fs := &trafficFakeStore{
		owners:         map[string]string{"srv-uuid-1": "tenant-a"},
		backupBytes:    map[string]int64{},
		addUsageErrFor: map[string]error{"tenant-a": errors.New("db down")},
	}
	agg, rdb := newTrafficAggregatorTest(t, fs, true)
	ctx := context.Background()

	mustHSet(t, rdb, "dylaris:traffic:edge:srv-uuid-1", map[string]interface{}{"rx": "1000"})
	mustSet(t, rdb, "dylaris:traffic:agg:seen:edge:srv-uuid-1", "200") // delta 800, but the DB write will fail

	agg.runOnce(ctx)

	if len(fs.addUsageCalls) != 1 {
		t.Fatalf("expected exactly one attempted AddTrafficUsage call, got %+v", fs.addUsageCalls)
	}
	// seen must remain at the pre-tick value so the same 800-byte delta is retried next tick.
	assertRedisString(t, rdb, "dylaris:traffic:agg:seen:edge:srv-uuid-1", "200")
}

func TestTrafficAggregator_SnapshotBackupStorage_UnionOfOwnersAndBackupTenants(t *testing.T) {
	fs := &trafficFakeStore{
		owners:      map[string]string{"srv-1": "tenant-a"},
		backupBytes: map[string]int64{"tenant-a": 500, "tenant-b": 999}, // tenant-b kept backups after deleting all servers
	}
	agg, _ := newTrafficAggregatorTest(t, fs, true)

	agg.runOnce(context.Background())

	byTenant := map[string]int64{}
	for _, c := range fs.setBackupCalls {
		byTenant[c.tenant] = c.bytes
	}
	if len(byTenant) != 2 {
		t.Fatalf("setBackupCalls = %+v, want both tenant-a and tenant-b", fs.setBackupCalls)
	}
	if byTenant["tenant-a"] != 500 || byTenant["tenant-b"] != 999 {
		t.Errorf("backup snapshot = %+v, want tenant-a=500 tenant-b=999", byTenant)
	}
}

func TestTrafficAggregator_TenantBackupBytesError_DoesNotBlockTrafficBilling(t *testing.T) {
	fs := &trafficFakeStore{
		owners:         map[string]string{"srv-uuid-1": "tenant-a"},
		backupBytesErr: errors.New("db down"),
	}
	agg, rdb := newTrafficAggregatorTest(t, fs, true)
	ctx := context.Background()
	mustHSet(t, rdb, "dylaris:traffic:edge:srv-uuid-1", map[string]interface{}{"rx": "1000"})

	agg.runOnce(ctx)

	if len(fs.addUsageCalls) != 1 {
		t.Errorf("traffic billing must proceed even when the backup-bytes lookup fails, got %+v", fs.addUsageCalls)
	}
	if len(fs.setBackupCalls) != 0 {
		t.Errorf("expected no backup-snapshot calls when TenantBackupBytes errors, got %+v", fs.setBackupCalls)
	}
}

func TestSumByteFields(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   int64
	}{
		{"empty", map[string]string{}, 0},
		{"single field", map[string]string{"rx": "100"}, 100},
		{"multiple fields summed", map[string]string{"rx": "100", "tx": "250"}, 350},
		{"unparseable field ignored", map[string]string{"rx": "100", "label": "not-a-number"}, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sumByteFields(c.fields); got != c.want {
				t.Errorf("sumByteFields(%v) = %d, want %d", c.fields, got, c.want)
			}
		})
	}
}

func TestMonthStartUTC(t *testing.T) {
	loc := time.FixedZone("UTC+9", 9*60*60)
	in := time.Date(2026, 7, 15, 23, 30, 0, 0, loc) // 2026-07-15 14:30 UTC
	want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if got := monthStartUTC(in); !got.Equal(want) {
		t.Errorf("monthStartUTC(%v) = %v, want %v", in, got, want)
	}
}
