package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dylaris-pkg/protocol"
)

func TestUtilPct(t *testing.T) {
	cases := []struct {
		name   string
		tx     uint64
		budget int
		want   float64
		known  bool
	}{
		{"half of 1Gbps", 500_000_000, 1000, 50, true},
		{"unset budget is unknown", 500_000_000, 0, 0, false},
		{"negative budget is unknown", 10, -5, 0, false},
		{"over budget reads > 100", 2_000_000_000, 1000, 200, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, known := utilPct(c.tx, c.budget)
			if known != c.known || (known && got != c.want) {
				t.Fatalf("utilPct(%d,%d) = %v,%v; want %v,%v", c.tx, c.budget, got, known, c.want, c.known)
			}
		})
	}
}

func TestEvaluateAlerts(t *testing.T) {
	now := time.Unix(1730000600, 0)
	tick := func(secBack int) time.Time { return now.Add(time.Duration(-secBack) * time.Second) }
	// host "h1": two ticks both ~90% of 1Gbps -> host alert.
	// component warp:eu-1 on h1 carries it. edge:eu-a on h1 is small.
	// host "h2": dips (one tick 90%, one tick 40%) -> NO alert.
	// host "h3": one tick only (90%) -> NO alert (needs >= 2 samples).
	// host "h4": high tx but cap 0 (unknown) -> NO alert.
	rows := []models.GatewayBandwidthRow{
		{Time: tick(30), Component: "warp", ID: "eu-1", Host: "h1", RxBps: 10, TxBps: 900_000_000, CapMbit: 1000},
		{Time: tick(60), Component: "warp", ID: "eu-1", Host: "h1", RxBps: 10, TxBps: 910_000_000, CapMbit: 1000},
		{Time: tick(30), Component: "edge", ID: "eu-a", Host: "h1", RxBps: 5, TxBps: 1_000_000, CapMbit: 1000},
		{Time: tick(30), Component: "warp", ID: "eu-2", Host: "h2", RxBps: 10, TxBps: 900_000_000, CapMbit: 1000},
		{Time: tick(60), Component: "warp", ID: "eu-2", Host: "h2", RxBps: 10, TxBps: 400_000_000, CapMbit: 1000},
		{Time: tick(30), Component: "warp", ID: "eu-3", Host: "h3", RxBps: 10, TxBps: 900_000_000, CapMbit: 1000},
		{Time: tick(30), Component: "beam", ID: "b-1", Host: "h4", RxBps: 10, TxBps: 5_000_000_000, CapMbit: 0},
		{Time: tick(60), Component: "beam", ID: "b-1", Host: "h4", RxBps: 10, TxBps: 5_000_000_000, CapMbit: 0},
	}
	got := evaluateAlerts(rows, 80, 5*time.Minute, now)

	// Expect exactly: host h1, and component warp:eu-1 (also sustained > 80% of its own cap).
	var hostAlerts, compAlerts int
	sawH1, sawWarpEu1 := false, false
	for _, a := range got {
		switch a.Kind {
		case "host":
			hostAlerts++
			if a.Host == "h1" {
				sawH1 = true
			}
			if a.Host == "h2" || a.Host == "h3" || a.Host == "h4" {
				t.Fatalf("unexpected host alert: %+v", a)
			}
		case "component":
			compAlerts++
			if a.Component == "warp" && a.ID == "eu-1" {
				sawWarpEu1 = true
			}
		}
	}
	if !sawH1 || hostAlerts != 1 {
		t.Fatalf("want exactly 1 host alert (h1), got %d: %+v", hostAlerts, got)
	}
	if !sawWarpEu1 {
		t.Fatalf("want a component alert for warp:eu-1, got %+v", got)
	}
	_ = compAlerts
}

func TestEvaluateAlerts_EmptyWindow(t *testing.T) {
	now := time.Unix(1730000600, 0)
	// All rows older than the window -> no alerts.
	rows := []models.GatewayBandwidthRow{
		{Time: now.Add(-10 * time.Minute), Component: "warp", ID: "x", Host: "h1", TxBps: 999_000_000, CapMbit: 1000},
		{Time: now.Add(-11 * time.Minute), Component: "warp", ID: "x", Host: "h1", TxBps: 999_000_000, CapMbit: 1000},
	}
	got := evaluateAlerts(rows, 80, 5*time.Minute, now)
	if got == nil {
		t.Fatal("alerts must be non-nil (serializes to [] not null so the panel can .map without a guard)")
	}
	if len(got) != 0 {
		t.Fatalf("expected no alerts, got %+v", got)
	}
}

func TestBuildBandwidthHistorySeparatesComponentsAndSumsHosts(t *testing.T) {
	t0 := time.Unix(1730000000, 0)
	t1 := time.Unix(1730000030, 0)
	rows := []models.GatewayBandwidthRow{
		{Time: t1, Component: "warp", ID: "eu-1", Host: "h1", Region: "eu", RxBps: 100, TxBps: 900, CapMbit: 1000},
		{Time: t1, Component: "edge", ID: "eu-a", Host: "h1", Region: "eu", RxBps: 50, TxBps: 100, CapMbit: 1000},
		{Time: t0, Component: "warp", ID: "eu-1", Host: "h1", Region: "eu", RxBps: 10, TxBps: 20, CapMbit: 1000},
	}
	// Step 0: the shortest range is raw, so nothing is reduced here.
	got := BuildBandwidthHistory(rows, 0)

	if len(got.Components) != 2 {
		t.Fatalf("expected 2 component series, got %d: %+v", len(got.Components), got.Components)
	}
	// Sorted by host, then component, then id: edge before warp.
	if got.Components[0].Component != "edge" || got.Components[0].ID != "eu-a" {
		t.Fatalf("unexpected first series: %+v", got.Components[0])
	}
	if len(got.Components[0].Points) != 1 || got.Components[0].Points[0].TxBps != 100 {
		t.Fatalf("edge series should hold only the edge's own row: %+v", got.Components[0].Points)
	}
	if n := len(got.Components[1].Points); n != 2 {
		t.Fatalf("warp series has %d points, want 2", n)
	}

	if len(got.Hosts) != 1 {
		t.Fatalf("expected 1 host series, got %d", len(got.Hosts))
	}
	hp := got.Hosts[0].Points
	if len(hp) != 2 {
		t.Fatalf("host series has %d points, want 2: %+v", len(hp), hp)
	}
	// Ascending by ts, and the later tick is the sum of both components.
	if hp[0].TS != t0.Unix() || hp[0].TxBps != 20 {
		t.Fatalf("unexpected host point[0]: %+v", hp[0])
	}
	if hp[1].TS != t1.Unix() || hp[1].RxBps != 150 || hp[1].TxBps != 1000 || hp[1].CapMbit != 1000 {
		t.Fatalf("unexpected host point[1]: %+v", hp[1])
	}
}

// A host's peak is the peak of the SUMS, never the sum of the peaks.
//
// This is the whole reason the host series is built here instead of by adding
// the component series together in the browser. Two components on one host peak
// in different seconds; adding their bucket maxima reports a load the link
// never carried, and it does so in the direction that looks like an emergency.
func TestAHostPeakIsThePeakOfTheSums(t *testing.T) {
	base := time.Unix(1730000000, 0)
	rows := []models.GatewayBandwidthRow{
		// One tick: the edge is busy, the warp leader is idle.
		{Time: base, Component: "edge", ID: "a", Host: "h1", TxBps: 900, CapMbit: 1000},
		{Time: base, Component: "warp", ID: "w", Host: "h1", TxBps: 100, CapMbit: 1000},
		// The next tick, inside the same bucket: exactly the other way round.
		{Time: base.Add(30 * time.Second), Component: "edge", ID: "a", Host: "h1", TxBps: 100, CapMbit: 1000},
		{Time: base.Add(30 * time.Second), Component: "warp", ID: "w", Host: "h1", TxBps: 900, CapMbit: 1000},
	}
	got := BuildBandwidthHistory(rows, 5*time.Minute)

	if len(got.Hosts) != 1 || len(got.Hosts[0].Points) != 1 {
		t.Fatalf("expected both ticks in one bucket: %+v", got.Hosts)
	}
	if tx := got.Hosts[0].Points[0].TxBps; tx != 1000 {
		t.Fatalf("host peak = %d, want 1000; summing the per-component peaks would give 1800", tx)
	}
}

// A bucket keeps its PEAK. Averaging is the reduction that lies in the
// dangerous direction: a link that saturated for two minutes inside a fifteen
// minute bucket averages out to comfortable, and the reader is deciding whether
// to buy more uplink.
func TestABucketKeepsItsPeak(t *testing.T) {
	base := time.Unix(1730000000, 0)
	rows := []models.GatewayBandwidthRow{
		{Time: base, Component: "edge", ID: "a", Host: "h1", RxBps: 10, TxBps: 100, CapMbit: 1000},
		{Time: base.Add(30 * time.Second), Component: "edge", ID: "a", Host: "h1", RxBps: 90, TxBps: 950, CapMbit: 1000},
		{Time: base.Add(60 * time.Second), Component: "edge", ID: "a", Host: "h1", RxBps: 20, TxBps: 120, CapMbit: 1000},
	}
	got := BuildBandwidthHistory(rows, 5*time.Minute)
	if len(got.Components) != 1 || len(got.Components[0].Points) != 1 {
		t.Fatalf("expected one bucket: %+v", got.Components)
	}
	p := got.Components[0].Points[0]
	if p.TxBps != 950 || p.RxBps != 90 {
		t.Fatalf("bucket = rx %d tx %d, want the peak rx 90 tx 950 (the mean would be rx 40 tx 390)", p.RxBps, p.TxBps)
	}
	// Stamped with the floor of the step GRID, not with the first row's own
	// time: two components sampled a second apart must land on one x position
	// or their sparklines do not line up.
	want := base.Unix() - base.Unix()%300
	if p.TS != want {
		t.Fatalf("bucket stamped %d, want the floor of the step grid %d", p.TS, want)
	}
}

// Empty in, empty out - and as [] rather than null, which is what lets the
// panel iterate without a null guard. The same contract the overview keeps.
func TestBuildBandwidthHistoryIsEmptyNotNull(t *testing.T) {
	got := BuildBandwidthHistory(nil, time.Minute)
	if got.Components == nil || got.Hosts == nil {
		t.Fatalf("nil slices reach the panel as JSON null: %+v", got)
	}
	if got.StepSec != 60 {
		t.Fatalf("stepSec = %d, want 60", got.StepSec)
	}
}

func TestLoadGatewayBandwidthOverview(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	now := time.Unix(1730000600, 0)

	// Seed the component mirror (JSON of protocol.GatewayStats).
	comp := protocol.GatewayStats{
		Version: protocol.GatewayStatsVersion, Component: "warp", ID: "eu-1",
		Host: "web-eu-1", Region: "eu-central", RxBps: 10, TxBps: 900_000_000, CapMbit: 1000,
	}
	cb, _ := json.Marshal(comp)
	mr.Set("dylaris:gwbw:component:warp:eu-1", string(cb))
	// Seed the host mirror (JSON of hostAggregate).
	agg := hostAggregate{Host: "web-eu-1", RxBps: 10, TxBps: 900_000_000, BudgetMbit: 1000}
	ab, _ := json.Marshal(agg)
	mr.Set("dylaris:gwbw:host:web-eu-1", string(ab))

	// Store returns two ticks both ~90% -> a host alert is expected.
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	st := store.NewPostgresStore(db)
	histRows := sqlmock.NewRows([]string{"time", "component", "id", "host", "region", "rx_bps", "tx_bps", "cap_mbit"}).
		AddRow(now.Add(-30*time.Second), "warp", "eu-1", "web-eu-1", "eu-central", int64(10), int64(900_000_000), 1000).
		AddRow(now.Add(-60*time.Second), "warp", "eu-1", "web-eu-1", "eu-central", int64(10), int64(910_000_000), 1000)
	mock.ExpectQuery("SELECT time, component, id, host, region, rx_bps, tx_bps, cap_mbit").
		WillReturnRows(histRows)

	ov := LoadGatewayBandwidthOverview(ctx, rdb, st, now)

	if len(ov.Components) != 1 || ov.Components[0].ID != "eu-1" || !ov.Components[0].CapKnown || !ov.Components[0].Alive {
		t.Fatalf("unexpected components: %+v", ov.Components)
	}
	if ov.Components[0].UtilPct < 89 || ov.Components[0].UtilPct > 91 {
		t.Fatalf("unexpected component util: %v", ov.Components[0].UtilPct)
	}
	if len(ov.Hosts) != 1 || ov.Hosts[0].Host != "web-eu-1" || ov.Hosts[0].Components != 1 {
		t.Fatalf("unexpected hosts: %+v", ov.Hosts)
	}
	foundHostAlert := false
	for _, a := range ov.Alerts {
		if a.Kind == "host" && a.Host == "web-eu-1" {
			foundHostAlert = true
		}
	}
	if !foundHostAlert {
		t.Fatalf("expected a host alert for web-eu-1, got %+v", ov.Alerts)
	}
}

func TestLoadGatewayBandwidthOverview_NilRedis(t *testing.T) {
	ov := LoadGatewayBandwidthOverview(context.Background(), nil, nil, time.Unix(1730000600, 0))
	if ov.Components == nil || ov.Hosts == nil || ov.Alerts == nil {
		t.Fatalf("slices must be non-nil, got %+v", ov)
	}
	if len(ov.Components) != 0 || len(ov.Hosts) != 0 || len(ov.Alerts) != 0 {
		t.Fatalf("expected empty overview, got %+v", ov)
	}
}

// Regression: a non-nil store that returns an EMPTY history must still yield a
// non-nil Alerts slice. The NilRedis test above uses st==nil, which skips the
// evaluateAlerts overwrite, so it never exercised the path where evaluateAlerts
// returned nil and clobbered the initialized []. That nil serialized to JSON
// null and crashed the panel's summarizeAlerts(alerts).map.
func TestLoadGatewayBandwidthOverview_EmptyHistoryNonNilAlerts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	st := store.NewPostgresStore(db)
	mock.ExpectQuery("SELECT time, component, id, host, region, rx_bps, tx_bps, cap_mbit").
		WillReturnRows(sqlmock.NewRows([]string{"time", "component", "id", "host", "region", "rx_bps", "tx_bps", "cap_mbit"}))

	ov := LoadGatewayBandwidthOverview(context.Background(), nil, st, time.Unix(1730000600, 0))
	if ov.Alerts == nil {
		t.Fatal("Alerts must be non-nil even with an empty history (serializes to [] not null)")
	}
	if len(ov.Alerts) != 0 {
		t.Fatalf("expected no alerts, got %+v", ov.Alerts)
	}
}

// A saturated link with sampling dropouts must alert.
//
// This is the case the old rule could not express, and the reason it never
// fired anywhere. A stored sample is a ONE-SECOND instantaneous reading, not the
// tick's average, so a busy link records the occasional quiet second - measured
// in production on 2026-09-03, the minimum sample in a 10-minute window was
// between 0% and 58% of that window's own average. Requiring EVERY sample to
// clear 80% therefore asked for an average of two to ten times the cap.
//
// The shape below is taken from that measurement: twenty ticks, seventeen of
// them at 92% of a gigabit, three dropouts including one exact zero.
func TestASaturatedLinkAlertsDespiteSamplingDropouts(t *testing.T) {
	now := time.Unix(1730000600, 0)
	cap1G := 1000
	busy := int64(920_000_000)
	dropouts := map[int]int64{4: 0, 9: 300_000_000, 15: 250_000_000}

	var rows []models.GatewayBandwidthRow
	for i := 0; i < 20; i++ {
		tx := busy
		if v, dip := dropouts[i]; dip {
			tx = v
		}
		rows = append(rows, models.GatewayBandwidthRow{
			Time:      now.Add(time.Duration(-(i+1)*30) * time.Second),
			Component: "edge", ID: "eu-edge-01", Host: "eu-edge-01",
			TxBps: uint64(tx), RxBps: 1000, CapMbit: cap1G,
		})
	}

	got := evaluateAlerts(rows, 80, 10*time.Minute, now)
	var host, comp bool
	for _, a := range got {
		if a.Direction != "out" {
			continue
		}
		if a.Kind == "host" && a.Host == "eu-edge-01" {
			host = true
		}
		if a.Kind == "component" && a.ID == "eu-edge-01" {
			comp = true
		}
	}
	if !host || !comp {
		t.Fatalf("a link at 92%% for seventeen of twenty ticks raised no alert: %+v", got)
	}
}

// The other half of the same rule: a link that is quiet most of the time must
// NOT alert just because it spikes. Without this the median would only have
// traded one useless answer for another.
func TestAMostlyIdleLinkDoesNotAlertOnSpikes(t *testing.T) {
	now := time.Unix(1730000600, 0)
	var rows []models.GatewayBandwidthRow
	for i := 0; i < 20; i++ {
		tx := int64(150_000_000) // 15%
		if i%3 == 0 {
			tx = 990_000_000 // 99%, seven of twenty ticks
		}
		rows = append(rows, models.GatewayBandwidthRow{
			Time:      now.Add(time.Duration(-(i+1)*30) * time.Second),
			Component: "edge", ID: "spiky", Host: "spiky",
			TxBps: uint64(tx), RxBps: 1000, CapMbit: 1000,
		})
	}
	if got := evaluateAlerts(rows, 80, 10*time.Minute, now); len(got) != 0 {
		t.Fatalf("a link idle for two thirds of the window alerted: %+v", got)
	}
}

// An exactly-half-saturated link does not alert: the median of an even count is
// the LOWER middle, so eleven of twenty ticks have to clear the threshold.
func TestHalfTheWindowIsNotSustained(t *testing.T) {
	now := time.Unix(1730000600, 0)
	var rows []models.GatewayBandwidthRow
	for i := 0; i < 20; i++ {
		tx := int64(200_000_000)
		if i < 10 {
			tx = 950_000_000
		}
		rows = append(rows, models.GatewayBandwidthRow{
			Time:      now.Add(time.Duration(-(i+1)*30) * time.Second),
			Component: "edge", ID: "half", Host: "half",
			TxBps: uint64(tx), RxBps: 1000, CapMbit: 1000,
		})
	}
	if got := evaluateAlerts(rows, 80, 10*time.Minute, now); len(got) != 0 {
		t.Fatalf("exactly half the window over threshold alerted: %+v", got)
	}
}
