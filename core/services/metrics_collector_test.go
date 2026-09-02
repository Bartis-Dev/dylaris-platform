package services

import (
	"context"
	"dylaris-core/metrics"
	"dylaris-core/models"
	"dylaris-core/store"
	"testing"
	"time"
)

// metricsFakeStore embeds store.Store (nil) so it satisfies the full interface
// while implementing only what the collector calls.
type metricsFakeStore struct {
	store.Store
	nodes   []models.Node
	servers []models.Server
	users   int
}

func (f *metricsFakeStore) ListNodes() ([]models.Node, error) { return f.nodes, nil }
func (f *metricsFakeStore) CountUsers() (int, error)          { return f.users, nil }
func (f *metricsFakeStore) ListServers(string) ([]models.Server, error) {
	return f.servers, nil
}

type fakeLeader struct{ leader bool }

func (f fakeLeader) IsLeader() bool { return f.leader }

// captureStore records what the recorder flushed.
type captureStore struct{ rows []metrics.Row }

func (c *captureStore) Upsert(_ context.Context, rows []metrics.Row) error {
	c.rows = append(c.rows, rows...)
	return nil
}

func (c *captureStore) byMetric(metric string) []metrics.Row {
	var out []metrics.Row
	for _, r := range c.rows {
		if r.Key.Metric == metric {
			out = append(out, r)
		}
	}
	return out
}

func (c *captureStore) one(t *testing.T, metric string) metrics.Row {
	t.Helper()
	got := c.byMetric(metric)
	if len(got) != 1 {
		t.Fatalf("expected exactly one row for %q, got %d", metric, len(got))
	}
	return got[0]
}

// settingsMap is the smallest settingsReader FeatureFlags needs.
type settingsMap map[string]string

func (m settingsMap) GetSetting(k string) (string, error) { return m[k], nil }

func owner(s string) *string { return &s }

func newCollectorForTest(t *testing.T, st store.Store, on bool, isLeader bool) (*MetricsCollector, *captureStore) {
	t.Helper()
	capt := &captureStore{}
	rec := metrics.NewRecorder(capt, time.Hour)
	set := settingsMap{}
	if on {
		set[MetricsEnabledSetting] = "true"
	}
	c := NewMetricsCollector(st, nil, rec, NewFeatureFlags(set))
	c.SetLeader(fakeLeader{leader: isLeader})
	return c, capt
}

func TestCollectorRecordsNothingWhenNotLeader(t *testing.T) {
	// Every replica sees the same fleet. Without the gate a two-replica install
	// folds the same players into the same bucket twice and reports double -
	// which is exactly how the overage sweep once billed every customer per
	// replica.
	st := &metricsFakeStore{nodes: []models.Node{{Token: "n1", Status: "online"}}}
	c, capt := newCollectorForTest(t, st, true, false)

	c.sampleOnce(context.Background())
	if err := c.recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(capt.rows) != 0 {
		t.Fatalf("a follower recorded %d rows", len(capt.rows))
	}
}

func TestCollectorRecordsNothingWhenTheFeatureIsOff(t *testing.T) {
	// Default off. Recording a year of history is a decision with a date on it,
	// not something that starts because the software supports it.
	st := &metricsFakeStore{nodes: []models.Node{{Token: "n1", Status: "online"}}}
	c, capt := newCollectorForTest(t, st, false, true)

	c.sampleOnce(context.Background())
	if err := c.recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(capt.rows) != 0 {
		t.Fatalf("recorded %d rows with the feature off", len(capt.rows))
	}
}

func TestCollectorCountsTheThreeNodeKindsApart(t *testing.T) {
	st := &metricsFakeStore{
		users: 7,
		nodes: []models.Node{
			{Token: "p1", Status: "online"},
			{Token: "p2", Status: "offline"},
			{Token: "e1", Status: "online", Tags: "eu,external"},
			{Token: "b1", Status: "online", OwnerID: owner("u-1")},
			// Owned AND tagged external: the customer's, not the operator's.
			// Counting this as external would file a machine the operator does
			// not control under their own hardware.
			{Token: "b2", Status: "online", OwnerID: owner("u-2"), Tags: "external"},
		},
	}
	c, capt := newCollectorForTest(t, st, true, true)

	c.sampleOnce(context.Background())
	if err := c.recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := map[string]float64{
		"platform.nodes":          5,
		"platform.nodes_online":   4,
		"platform.nodes_platform": 2,
		"platform.nodes_external": 1,
		"platform.nodes_byon":     2,
		"platform.users":          7,
	}
	for metric, v := range want {
		if got := capt.one(t, metric).Bucket; got.Sum != v || got.Count != 1 {
			t.Errorf("%s = sum %v count %d, want sum %v count 1", metric, got.Sum, got.Count, v)
		}
	}
}

func TestAnOfflineNodeRecordsDownAndNotItsStaleReadings(t *testing.T) {
	// CPU and RAM on an offline node are the LAST values seen, not current
	// ones. Recording them puts a flatline into the average that reads like a
	// healthy idle machine - the graph would show the fleet coping while half
	// of it was unreachable.
	st := &metricsFakeStore{nodes: []models.Node{
		{Token: "down", Status: "offline", CPUUsage: 42, RAMTotal: 100, RAMFree: 40},
		{Token: "up", Status: "online", CPUUsage: 10, RAMTotal: 100, RAMFree: 40, Region: "eu"},
	}}
	c, capt := newCollectorForTest(t, st, true, true)

	c.sampleOnce(context.Background())
	if err := c.recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	ups := map[string]float64{}
	for _, r := range capt.byMetric("node.up") {
		ups[r.Key.Subject] = r.Bucket.Sum
	}
	if ups["down"] != 0 || ups["up"] != 1 {
		t.Fatalf("node.up = %v, want down:0 up:1", ups)
	}

	for _, m := range []string{"node.cpu_pct", "node.ram_pct", "node.ram_used_bytes"} {
		for _, r := range capt.byMetric(m) {
			if r.Key.Subject == "down" {
				t.Errorf("%s was recorded for an offline node", m)
			}
		}
	}
	// The live one keeps its region, so a per-region reading stays per-region.
	if got := capt.byMetric("node.cpu_pct"); len(got) != 1 || got[0].Key.Region != "eu" {
		t.Fatalf("live node cpu row wrong: %+v", got)
	}
}

func TestUpIsRecordedAsANumberSoUptimeIsAnAverage(t *testing.T) {
	// Availability has to be a series rather than inferred from missing rows: a
	// gap is ambiguous between "the node was down" and "nothing was sampling",
	// and those are opposite claims to make to a buyer.
	st := &metricsFakeStore{nodes: []models.Node{{Token: "n", Status: "online"}}}
	c, capt := newCollectorForTest(t, st, true, true)

	c.sampleOnce(context.Background())
	st.nodes[0].Status = "offline"
	c.sampleOnce(context.Background())
	c.sampleOnce(context.Background())
	if err := c.recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	row := capt.one(t, "node.up")
	if row.Bucket.Count != 3 || row.Bucket.Sum != 1 {
		t.Fatalf("node.up bucket = %+v, want 3 samples summing to 1", row.Bucket)
	}
	if got := row.Bucket.Avg(); got != 1.0/3.0 {
		t.Fatalf("uptime fraction = %v, want 1/3", got)
	}
	if row.Bucket.Min != 0 || row.Bucket.Max != 1 {
		t.Fatalf("min/max lost the range: %+v", row.Bucket)
	}
}
