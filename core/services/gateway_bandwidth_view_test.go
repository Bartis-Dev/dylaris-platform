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
	if got := evaluateAlerts(rows, 80, 5*time.Minute, now); len(got) != 0 {
		t.Fatalf("expected no alerts, got %+v", got)
	}
}

func TestAggregateHostHistory(t *testing.T) {
	t0 := time.Unix(1730000000, 0)
	t1 := time.Unix(1730000030, 0)
	rows := []models.GatewayBandwidthRow{
		{Time: t1, Component: "warp", ID: "eu-1", Host: "h1", RxBps: 100, TxBps: 900, CapMbit: 1000},
		{Time: t1, Component: "edge", ID: "eu-a", Host: "h1", RxBps: 50, TxBps: 100, CapMbit: 1000},
		{Time: t0, Component: "warp", ID: "eu-1", Host: "h1", RxBps: 10, TxBps: 20, CapMbit: 1000},
	}
	got := AggregateHostHistory(rows)
	if len(got) != 2 {
		t.Fatalf("expected 2 points, got %d: %+v", len(got), got)
	}
	// Ascending by ts: t0 first.
	if got[0].TS != t0.Unix() || got[0].TxBps != 20 {
		t.Fatalf("unexpected point[0]: %+v", got[0])
	}
	if got[1].TS != t1.Unix() || got[1].RxBps != 150 || got[1].TxBps != 1000 || got[1].CapMbit != 1000 {
		t.Fatalf("unexpected point[1]: %+v", got[1])
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
