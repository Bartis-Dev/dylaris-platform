package services

import (
	"dylaris-core/metrics"
	"dylaris-core/store"
	"dylaris-pkg/protocol"
	"strings"
	"testing"
	"time"
)

func TestACumulativeCounterIsRecordedAsItsDelta(t *testing.T) {
	c := newCounterSource()

	// The FIRST reading of a total must record nothing. Postgres reports
	// commits since the database started, so recording the first sample would
	// enter months of history as if it had all happened in one 30-second bucket
	// - and the chart would open with a spike that never happened.
	if _, ok := c.delta("commits", 1_000_000); ok {
		t.Fatal("the first reading of a cumulative counter was recorded")
	}

	got, ok := c.delta("commits", 1_000_050)
	if !ok || got != 50 {
		t.Fatalf("delta = %v ok=%v, want 50 true", got, ok)
	}

	// A counter that went BACKWARDS means the process behind it restarted.
	// Recording cur-prev would be a huge negative number; recording cur would
	// be the same false spike as above. Neither: skip, and re-baseline.
	if _, ok := c.delta("commits", 7); ok {
		t.Fatal("a restarted counter was recorded instead of re-baselined")
	}
	got, ok = c.delta("commits", 10)
	if !ok || got != 3 {
		t.Fatalf("after a restart the baseline is wrong: %v ok=%v, want 3 true", got, ok)
	}
}

func TestCountersOfDifferentNamesDoNotShareABaseline(t *testing.T) {
	c := newCounterSource()
	c.delta("a", 100)
	c.delta("b", 5)
	if got, ok := c.delta("a", 110); !ok || got != 10 {
		t.Fatalf("a = %v ok=%v, want 10", got, ok)
	}
	if got, ok := c.delta("b", 6); !ok || got != 1 {
		t.Fatalf("b = %v ok=%v, want 1", got, ok)
	}
}

func TestParseRedisInfoTakesOnlyNumbers(t *testing.T) {
	raw := "# Clients\r\nconnected_clients:12\r\nredis_version:7.2.4\r\n\r\n# Memory\r\nused_memory:1048576\r\nmaxmemory_policy:noeviction\r\n"
	got := parseRedisInfo(raw)
	if got["connected_clients"] != 12 || got["used_memory"] != 1048576 {
		t.Fatalf("numbers not parsed: %v", got)
	}
	// A version string parsed as a number would be silently wrong rather than
	// absent, so non-numeric fields are dropped instead of coerced.
	if _, ok := got["redis_version"]; ok {
		t.Error("redis_version was coerced into a number")
	}
	if _, ok := got["maxmemory_policy"]; ok {
		t.Error("maxmemory_policy was coerced into a number")
	}
}

// gwSnapshot is a fixed gateway telemetry view.
type gwSnapshot []protocol.GatewayStats

func (g gwSnapshot) Snapshot() []protocol.GatewayStats { return g }

func newInfraCollector(t *testing.T) (*MetricsCollector, *captureStore) {
	t.Helper()
	capt := &captureStore{}
	rec := metrics.NewRecorder(capt, time.Hour)
	c := NewMetricsCollector(nil, nil, nil, fixedRecorder{rec}, NewFeatureFlags(settingsMap{MetricsEnabledSetting: "true"}))
	c.SetLeader(fakeLeader{leader: true})
	return c, capt
}

func TestGatewayCountersBecomeSeriesNamedByTheirComponent(t *testing.T) {
	c, capt := newInfraCollector(t)
	c.SetGatewayStats(gwSnapshot{{
		Component: "splice", ID: "host-1", Region: "eu",
		Counters: map[string]int64{"handover_ok": 4, "players_dropped": 1},
		Gauges:   map[string]float64{"active_sessions": 12},
	}})

	c.sampleGatewayComponents(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	for metric, want := range map[string]float64{
		"splice.handover_ok":     4,
		"splice.players_dropped": 1,
		"splice.active_sessions": 12,
	} {
		row := capt.one(t, metric)
		if row.Bucket.Sum != want {
			t.Errorf("%s = %v, want %v", metric, row.Bucket.Sum, want)
		}
		// The component id and region have to survive, or a fleet of edges
		// collapses into one unattributable line.
		if row.Key.Subject != "host-1" || row.Key.Region != "eu" {
			t.Errorf("%s lost its identity: %+v", metric, row.Key)
		}
	}
}

func TestAMalformedMetricNameIsDropped(t *testing.T) {
	// Names come from ANOTHER repository over a wire contract designed never to
	// change again, so they are validated here rather than trusted.
	//
	// What this check does and does not do is worth being exact about: it
	// judges the SHAPE of a name, not its meaning. "session_9f2a1b" looks like
	// a fixed identifier and is accepted - no static rule can tell it from a
	// legitimate name - so the protection against a producer emitting one name
	// per session is MaxCustomMetrics, tested separately below. This rule
	// catches the shapes that are unambiguously not identifiers: an address, a
	// dotted path, a second spelling in a different case.
	c, capt := newInfraCollector(t)
	c.SetGatewayStats(gwSnapshot{{
		Component: "splice", ID: "h1",
		Counters: map[string]int64{
			"handover_ok":        1,
			"10.0.0.4_bytes":     1,
			"PlayersDropped":     1,
			"player.notch.pings": 1,
			"handover-ok":        1,
		},
	}})
	c.sampleGatewayComponents(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(capt.rows) != 1 || capt.rows[0].Key.Metric != "splice.handover_ok" {
		var names []string
		for _, r := range capt.rows {
			names = append(names, r.Key.Metric)
		}
		t.Fatalf("recorded %v, want only splice.handover_ok", names)
	}
}

func TestOneRecordCannotPublishUnboundedSeries(t *testing.T) {
	counters := map[string]int64{}
	for i := 0; i < protocol.MaxCustomMetrics*3; i++ {
		counters["m"+strings.Repeat("x", i%20)+string(rune('a'+i%26))+string(rune('a'+i/26))] = 1
	}
	c, capt := newInfraCollector(t)
	c.SetGatewayStats(gwSnapshot{{Component: "edge", ID: "e1", Counters: counters}})
	c.sampleGatewayComponents(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(capt.rows) > protocol.MaxCustomMetrics {
		t.Fatalf("one record produced %d series, cap is %d", len(capt.rows), protocol.MaxCustomMetrics)
	}
}

func TestARestartIsSeenAsUptimeGoingBackwards(t *testing.T) {
	// Nothing else in the system reports that a gateway component restarted,
	// and the restart is half of the measurement: the splice's handover
	// counters mean one thing next to a restart and another without one.
	c, capt := newInfraCollector(t)
	at := time.Now()

	// First sighting: no restart can be claimed, because there is nothing to
	// compare against. Core restarting must not read as the whole fleet
	// restarting. The assertion is on the sample COUNT after the flush below -
	// three calls, two recorded.
	c.recordRestart(protocol.GatewayStats{Component: "edge", ID: "e1", UptimeSec: 500}, at)
	c.recordRestart(protocol.GatewayStats{Component: "edge", ID: "e1", UptimeSec: 530}, at)
	c.recordRestart(protocol.GatewayStats{Component: "edge", ID: "e1", UptimeSec: 4}, at)
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	row := capt.one(t, "edge.restarts")
	if row.Bucket.Count != 2 {
		t.Fatalf("recorded %d restart samples; the first sighting should record none", row.Bucket.Count)
	}
	if row.Bucket.Sum != 1 {
		t.Fatalf("restarts summed to %v, want exactly 1", row.Bucket.Sum)
	}
}

func TestAComponentTooOldToReportUptimeRecordsNoRestart(t *testing.T) {
	// One repo can be deployed before the other. An older build sends no
	// uptime, which decodes to 0 - and 0 is lower than any previous value, so
	// treating it as a reading would report a restart on every single sample.
	c, capt := newInfraCollector(t)
	at := time.Now()
	c.recordRestart(protocol.GatewayStats{Component: "warp", ID: "w1", UptimeSec: 900}, at)
	c.recordRestart(protocol.GatewayStats{Component: "warp", ID: "w1", UptimeSec: 0}, at)
	c.recordRestart(protocol.GatewayStats{Component: "warp", ID: "w1", UptimeSec: 0}, at)
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := capt.byMetric("warp.restarts"); len(got) != 0 {
		t.Fatalf("a build that reports no uptime produced %d restart rows", len(got))
	}
}

// trafficStore serves ListTrafficUsage and nothing else.
type trafficStore struct {
	store.Store
	rows []store.TrafficUsage
}

func (s *trafficStore) ListTrafficUsage(time.Time) ([]store.TrafficUsage, error) {
	return s.rows, nil
}

func TestUserTrafficIsRecordedAsShapeNotPerUser(t *testing.T) {
	// Four series rather than one per tenant, deliberately: a series per user
	// makes the store grow with the CUSTOMER LIST instead of with the code, and
	// per-user history is what the billing tables already are.
	capt := &captureStore{}
	rec := metrics.NewRecorder(capt, time.Hour)
	st := &trafficStore{rows: []store.TrafficUsage{
		{UserID: "a", EdgeBytes: 100, RelayBytes: 0},
		{UserID: "b", EdgeBytes: 200, RelayBytes: 100},
		{UserID: "c", EdgeBytes: 0, RelayBytes: 0},
	}}
	c := NewMetricsCollector(st, nil, nil, fixedRecorder{rec}, NewFeatureFlags(settingsMap{}))

	c.sampleUserTraffic(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	for metric, want := range map[string]float64{
		"platform.user_traffic_total_bytes": 400,
		"platform.user_traffic_avg_bytes":   400.0 / 3.0,
		"platform.user_traffic_min_bytes":   0,
		"platform.user_traffic_max_bytes":   300,
		"platform.billed_users":             3,
	} {
		if got := capt.one(t, metric).Bucket.Sum; got != want {
			t.Errorf("%s = %v, want %v", metric, got, want)
		}
	}
	for _, r := range capt.rows {
		if r.Key.Subject != "" {
			t.Errorf("%s was recorded per user (subject %q); that is one series per tenant forever",
				r.Key.Metric, r.Key.Subject)
		}
	}
}

func TestTheQuietestTenantDoesNotVanishFromTheMinimum(t *testing.T) {
	// The minimum has to start at the FIRST row, not at zero: seeding it with
	// the zero value makes every minimum 0 as soon as one tenant exists, which
	// reads as "somebody used nothing" whether or not anybody did.
	capt := &captureStore{}
	rec := metrics.NewRecorder(capt, time.Hour)
	st := &trafficStore{rows: []store.TrafficUsage{
		{UserID: "a", EdgeBytes: 500},
		{UserID: "b", EdgeBytes: 900},
	}}
	c := NewMetricsCollector(st, nil, nil, fixedRecorder{rec}, NewFeatureFlags(settingsMap{}))
	c.sampleUserTraffic(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := capt.one(t, "platform.user_traffic_min_bytes").Bucket.Sum; got != 500 {
		t.Fatalf("minimum = %v, want 500", got)
	}
}

// Warp leaders and beam relays cost a machine something, and until 2026-09-03
// the long-term record held nothing about it. CPU and RAM are typed fields on
// the telemetry record rather than entries in its Gauges map, so the loop that
// turns a component's own numbers into series walked straight past them.
func TestWarpAndBeamSystemLoadIsRecorded(t *testing.T) {
	c, capt := newInfraCollector(t)
	c.SetGatewayStats(gwSnapshot{
		{Component: "warp", ID: "w1", Region: "eu", CPU: 12.5, RAMPct: 28},
		{Component: "beam", ID: "b1", Region: "eu", CPU: 3, RAMPct: 9.5},
	})
	c.sampleGatewayComponents(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	for metric, want := range map[string]float64{
		"warp.cpu_pct": 12.5,
		"warp.ram_pct": 28,
		"beam.cpu_pct": 3,
		"beam.ram_pct": 9.5,
	} {
		row := capt.one(t, metric)
		if row.Bucket.Sum != want {
			t.Errorf("%s = %v, want %v", metric, row.Bucket.Sum, want)
		}
	}
	// The subject has to be the component id, because that is what the screen
	// showing these lines labels them with.
	if s := capt.one(t, "warp.cpu_pct").Key.Subject; s != "w1" {
		t.Errorf("warp.cpu_pct subject = %q, want the leader id", s)
	}
}

// A component that does not measure its machine publishes 0 for both. The
// splice and the link do exactly that, and recording it would put a permanent
// flat zero into the record - which reads like an idle machine rather than like
// an absent measurement.
func TestAComponentThatMeasuresNothingRecordsNoLoad(t *testing.T) {
	c, capt := newInfraCollector(t)
	c.SetGatewayStats(gwSnapshot{
		{Component: "splice", ID: "host-1", Counters: map[string]int64{"handover_ok": 1}},
		{Component: "link", ID: "l1", Gauges: map[string]float64{"active_tunnels": 2}},
	})
	c.sampleGatewayComponents(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"splice.cpu_pct", "splice.ram_pct", "link.cpu_pct", "link.ram_pct"} {
		if got := capt.byMetric(m); len(got) != 0 {
			t.Errorf("%s got %d rows; nothing publishes it", m, len(got))
		}
	}
	// Their real numbers still arrive - this guard is about the two fields they
	// leave empty, not about ignoring the component.
	capt.one(t, "splice.handover_ok")
	capt.one(t, "link.active_tunnels")
}

// An edge's CPU is recorded ONCE per tick, from the edge list in sampleGateway.
// Recording it here as well would fold two readings of one machine into one
// bucket and inflate its sample count, so the component loop skips edges - and
// this pins that, because the skip is invisible until somebody counts.
func TestAnEdgeIsNotRecordedTwice(t *testing.T) {
	c, capt := newInfraCollector(t)
	c.SetGatewayStats(gwSnapshot{{Component: "edge", ID: "e1", Region: "eu", CPU: 30, RAMPct: 44}})
	c.sampleGatewayComponents(time.Now())
	if err := c.recorders.Recorder().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := capt.byMetric("edge.cpu_pct"); len(got) != 0 {
		t.Fatalf("the component loop recorded edge.cpu_pct; sampleGateway already does, so the bucket would hold two readings of one machine")
	}
}
