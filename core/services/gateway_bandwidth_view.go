package services

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"

	"dylaris-pkg/protocol"

	"github.com/redis/go-redis/v9"
)

const (
	gwbwAlertThresholdPct = 80
	gwbwAlertWindow       = 5 * time.Minute
)

// GatewayComponentView is one live gateway component (edge, warp leader or beam)
// as read from the F0 component mirror. Alive is always true here: a component
// only has a mirror entry while it is reporting (the entry self-expires via the
// 90s TTL), so presence == alive.
type GatewayComponentView struct {
	Component string  `json:"component"`
	ID        string  `json:"id"`
	Host      string  `json:"host"`
	Region    string  `json:"region"`
	RxBps     uint64  `json:"rxBps"`
	TxBps     uint64  `json:"txBps"`
	CapMbit   int     `json:"capMbit"`
	UtilPct   float64 `json:"utilPct"`
	CapKnown  bool    `json:"capKnown"`
	Alive     bool    `json:"alive"`

	// What the machine costs, and how long this process has been up. Every
	// component already publishes all three in the same record the throughput
	// comes from; this view simply used to drop them, which is why the panel
	// could show a warp leader's bandwidth but not whether it was busy.
	CPUPct    float64 `json:"cpuPct"`
	RAMPct    float64 `json:"ramPct"`
	UptimeSec int64   `json:"uptimeSec,omitempty"`

	// The component's own gauges, passed through rather than translated: a warp
	// leader reports peers, a beam relay reports transfers in flight, and only
	// the caller knows which of them it is about to label. Translating here
	// would mean this file changing every time a component adds a number.
	Gauges map[string]float64 `json:"gauges,omitempty"`
}

// GatewayHostView is the summed throughput of all components co-located on one
// swarm host against the host budget (the F0 host aggregate).
type GatewayHostView struct {
	Host        string  `json:"host"`
	RxBps       uint64  `json:"rxBps"`
	TxBps       uint64  `json:"txBps"`
	BudgetMbit  int     `json:"budgetMbit"`
	UtilPct     float64 `json:"utilPct"`
	CapKnown    bool    `json:"capKnown"`
	CapMismatch bool    `json:"capMismatch"`
	Components  int     `json:"components"`
}

// GatewayAlert is one host or component that stayed at or above the threshold for
// the whole alert window.
type GatewayAlert struct {
	Kind      string  `json:"kind"` // "host" or "component"
	Host      string  `json:"host"`
	Component string  `json:"component,omitempty"`
	ID        string  `json:"id,omitempty"`
	UtilPct   float64 `json:"utilPct"`
	Threshold int     `json:"threshold"`
}

// GatewayBandwidthOverview is the panel payload. Slices are always non-nil so the
// JSON is [] not null.
type GatewayBandwidthOverview struct {
	Components []GatewayComponentView `json:"components"`
	Hosts      []GatewayHostView      `json:"hosts"`
	Alerts     []GatewayAlert         `json:"alerts"`
}

// BandwidthHistoryPoint is one time bucket of summed throughput for a history chart.
type BandwidthHistoryPoint struct {
	TS      int64  `json:"ts"`
	RxBps   uint64 `json:"rxBps"`
	TxBps   uint64 `json:"txBps"`
	CapMbit int    `json:"capMbit"`
}

// utilPct returns tx as a percentage of budget (megabit/s -> bits/s) and whether
// the budget was known. budgetMbit <= 0 (BANDWIDTH_MBIT unset) means UNKNOWN, not
// 0 percent.
func utilPct(txBps uint64, budgetMbit int) (float64, bool) {
	if budgetMbit <= 0 {
		return 0, false
	}
	return float64(txBps) / (float64(budgetMbit) * 1_000_000) * 100, true
}

type utilSample struct {
	t     time.Time
	util  float64
	known bool
}

// sustainedOver reports whether a series shows utilisation continuously at or
// above threshold for the window: at least 2 distinct-time samples, every
// sample's budget known, and the minimum utilisation across samples >= threshold
// (a single dip below clears it). It also returns the latest sample's utilisation
// for display.
func sustainedOver(samples []utilSample, thresholdPct int) (float64, bool) {
	if len(samples) < 2 {
		return 0, false
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].t.Before(samples[j].t) })
	min := math.MaxFloat64
	for _, s := range samples {
		if !s.known {
			return 0, false
		}
		if s.util < min {
			min = s.util
		}
	}
	if min < float64(thresholdPct) {
		return 0, false
	}
	return samples[len(samples)-1].util, true
}

// evaluateAlerts flags hosts and components sustained at or above thresholdPct for
// the whole window [now-window, now]. Rows are the raw per-component history; all
// rows of one persist tick share an identical Time (persistOnce stamps one
// timestamp per batch), so bucketing by Time reconstructs each tick, and the
// host budget per tick is the MAX cap among co-located components (the shared
// aggregateByHost rule).
func evaluateAlerts(rows []models.GatewayBandwidthRow, thresholdPct int, window time.Duration, now time.Time) []GatewayAlert {
	cutoff := now.Add(-window)

	type hostTickAgg struct {
		tx     uint64
		budget int
	}
	hostTicks := map[string]map[time.Time]*hostTickAgg{}
	compTicks := map[string]map[time.Time]models.GatewayBandwidthRow{}

	for _, r := range rows {
		if r.Time.Before(cutoff) {
			continue
		}
		if r.Host != "" {
			m := hostTicks[r.Host]
			if m == nil {
				m = map[time.Time]*hostTickAgg{}
				hostTicks[r.Host] = m
			}
			a := m[r.Time]
			if a == nil {
				a = &hostTickAgg{}
				m[r.Time] = a
			}
			a.tx += r.TxBps
			if r.CapMbit > a.budget {
				a.budget = r.CapMbit
			}
		}
		key := r.Component + "\x00" + r.ID
		cm := compTicks[key]
		if cm == nil {
			cm = map[time.Time]models.GatewayBandwidthRow{}
			compTicks[key] = cm
		}
		cm[r.Time] = r
	}

	// Non-nil so the JSON payload is [] not null, keeping the "slices are always
	// non-nil" contract that the panel relies on to render without a null guard.
	alerts := []GatewayAlert{}
	for host, ticks := range hostTicks {
		samples := make([]utilSample, 0, len(ticks))
		for tt, a := range ticks {
			u, known := utilPct(a.tx, a.budget)
			samples = append(samples, utilSample{t: tt, util: u, known: known})
		}
		if latest, ok := sustainedOver(samples, thresholdPct); ok {
			alerts = append(alerts, GatewayAlert{Kind: "host", Host: host, UtilPct: latest, Threshold: thresholdPct})
		}
	}
	for _, ticks := range compTicks {
		samples := make([]utilSample, 0, len(ticks))
		var last models.GatewayBandwidthRow
		var lastT time.Time
		for tt, r := range ticks {
			u, known := utilPct(r.TxBps, r.CapMbit)
			samples = append(samples, utilSample{t: tt, util: u, known: known})
			if tt.After(lastT) {
				lastT = tt
				last = r
			}
		}
		if latest, ok := sustainedOver(samples, thresholdPct); ok {
			alerts = append(alerts, GatewayAlert{
				Kind: "component", Host: last.Host, Component: last.Component, ID: last.ID,
				UtilPct: latest, Threshold: thresholdPct,
			})
		}
	}

	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].Kind != alerts[j].Kind {
			return alerts[i].Kind < alerts[j].Kind
		}
		if alerts[i].Host != alerts[j].Host {
			return alerts[i].Host < alerts[j].Host
		}
		if alerts[i].Component != alerts[j].Component {
			return alerts[i].Component < alerts[j].Component
		}
		return alerts[i].ID < alerts[j].ID
	})
	return alerts
}

// BandwidthSeries is one subject's throughput history: a single component
// (Component+ID) or a whole host.
type BandwidthSeries struct {
	Component string                  `json:"component,omitempty"`
	ID        string                  `json:"id,omitempty"`
	Host      string                  `json:"host,omitempty"`
	Region    string                  `json:"region,omitempty"`
	Points    []BandwidthHistoryPoint `json:"points"`
}

// BandwidthHistory is every series the bandwidth screen draws, in one payload.
//
// One request rather than one per card: the screen shows a sparkline for each
// component plus a full chart for whatever is selected, and at six components
// that is seven round trips for data that comes from a single table scan.
//
// The host series are computed HERE and not by summing the component series in
// the browser. Summing bucket maxima is not the maximum of the sums - each
// component peaks in its own second - so a client-side sum would report a host
// load that never happened. Below, the per-tick rows are summed first (rows of
// one persist tick share a timestamp) and only then reduced.
type BandwidthHistory struct {
	StepSec    int               `json:"stepSec"`
	Components []BandwidthSeries `json:"components"`
	Hosts      []BandwidthSeries `json:"hosts"`
}

// BuildBandwidthHistory turns raw per-component rows into the panel payload,
// reducing each step-wide bucket to its PEAK.
//
// Peak, not average, and that is the whole reason a step exists rather than
// sending every row: 24 hours at the 30-second persist cadence is 2880 points
// per component, and the obvious way to shrink it - averaging - is the one that
// lies in the dangerous direction. A link that saturated for two minutes inside
// a fifteen-minute bucket averages out to comfortable, and the reader is
// deciding whether to buy more uplink. A step of 0 (or one at or below the
// persist cadence) leaves the rows untouched, so the shortest range is raw.
func BuildBandwidthHistory(rows []models.GatewayBandwidthRow, step time.Duration) BandwidthHistory {
	out := BandwidthHistory{
		StepSec:    int(step / time.Second),
		Components: []BandwidthSeries{},
		Hosts:      []BandwidthSeries{},
	}

	type compKey struct{ component, id string }
	compBuckets := map[compKey]map[int64]*BandwidthHistoryPoint{}
	compMeta := map[compKey]BandwidthSeries{}

	// Host totals are summed at RAW tick resolution first; the reduction to
	// buckets happens afterwards, over the summed series.
	hostTicks := map[string]map[int64]*BandwidthHistoryPoint{}

	for _, r := range rows {
		ts := r.Time.Unix()
		ck := compKey{r.Component, r.ID}
		if _, ok := compMeta[ck]; !ok {
			compMeta[ck] = BandwidthSeries{Component: r.Component, ID: r.ID, Host: r.Host, Region: r.Region}
			compBuckets[ck] = map[int64]*BandwidthHistoryPoint{}
		}
		peakInto(compBuckets[ck], bucketOf(ts, step), r.RxBps, r.TxBps, r.CapMbit)

		if r.Host == "" {
			continue
		}
		m := hostTicks[r.Host]
		if m == nil {
			m = map[int64]*BandwidthHistoryPoint{}
			hostTicks[r.Host] = m
		}
		p := m[ts]
		if p == nil {
			p = &BandwidthHistoryPoint{TS: ts}
			m[ts] = p
		}
		p.RxBps += r.RxBps
		p.TxBps += r.TxBps
		// The shared-uplink rule the live view uses: co-located components
		// describe ONE link, so the budget is the largest cap among them rather
		// than their sum.
		if r.CapMbit > p.CapMbit {
			p.CapMbit = r.CapMbit
		}
	}

	for ck, meta := range compMeta {
		meta.Points = sortedPoints(compBuckets[ck])
		out.Components = append(out.Components, meta)
	}
	sort.Slice(out.Components, func(i, j int) bool {
		if out.Components[i].Host != out.Components[j].Host {
			return out.Components[i].Host < out.Components[j].Host
		}
		if out.Components[i].Component != out.Components[j].Component {
			return out.Components[i].Component < out.Components[j].Component
		}
		return out.Components[i].ID < out.Components[j].ID
	})

	for host, ticks := range hostTicks {
		reduced := map[int64]*BandwidthHistoryPoint{}
		for ts, p := range ticks {
			peakInto(reduced, bucketOf(ts, step), p.RxBps, p.TxBps, p.CapMbit)
		}
		out.Hosts = append(out.Hosts, BandwidthSeries{Host: host, Points: sortedPoints(reduced)})
	}
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].Host < out.Hosts[j].Host })
	return out
}

// bucketOf floors a timestamp onto the step grid. A step of 0 or less is the
// identity, which is what makes the shortest range raw rather than a special case.
func bucketOf(ts int64, step time.Duration) int64 {
	sec := int64(step / time.Second)
	if sec <= 1 {
		return ts
	}
	return ts - ts%sec
}

// peakInto keeps the largest reading seen in a bucket.
func peakInto(m map[int64]*BandwidthHistoryPoint, ts int64, rx, tx uint64, capMbit int) {
	p := m[ts]
	if p == nil {
		p = &BandwidthHistoryPoint{TS: ts}
		m[ts] = p
	}
	if rx > p.RxBps {
		p.RxBps = rx
	}
	if tx > p.TxBps {
		p.TxBps = tx
	}
	if capMbit > p.CapMbit {
		p.CapMbit = capMbit
	}
}

func sortedPoints(m map[int64]*BandwidthHistoryPoint) []BandwidthHistoryPoint {
	out := make([]BandwidthHistoryPoint, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// LoadGatewayBandwidthOverview reads the F0 Redis mirror (per-component and
// per-host aggregates, both TTL-bounded so only currently-reporting components
// appear) plus the recent history, and assembles the panel overview: live
// utilisation per component and per host, and the sustained-threshold alerts.
// Best-effort: a nil client or any Redis/store error yields an empty section, not
// a failure - the dashboard degrades to "no data" rather than erroring.
func LoadGatewayBandwidthOverview(ctx context.Context, rdb *redis.Client, st store.Store, now time.Time) GatewayBandwidthOverview {
	ov := GatewayBandwidthOverview{
		Components: []GatewayComponentView{},
		Hosts:      []GatewayHostView{},
		Alerts:     []GatewayAlert{},
	}
	if rdb != nil {
		if compKeys := scanKeys(ctx, rdb, "dylaris:gwbw:component:*"); len(compKeys) > 0 {
			if vals, err := rdb.MGet(ctx, compKeys...).Result(); err == nil {
				for _, v := range vals {
					raw, ok := v.(string)
					if !ok {
						continue
					}
					var gs protocol.GatewayStats
					if json.Unmarshal([]byte(raw), &gs) != nil {
						continue
					}
					u, known := utilPct(gs.TxBps, gs.CapMbit)
					ov.Components = append(ov.Components, GatewayComponentView{
						Component: gs.Component, ID: gs.ID, Host: gs.Host, Region: gs.Region,
						RxBps: gs.RxBps, TxBps: gs.TxBps, CapMbit: gs.CapMbit,
						UtilPct: u, CapKnown: known, Alive: true,
						CPUPct: gs.CPU, RAMPct: gs.RAMPct, UptimeSec: gs.UptimeSec,
						Gauges: gs.Gauges,
					})
				}
			}
		}
		perHost := map[string]int{}
		for _, c := range ov.Components {
			perHost[c.Host]++
		}
		if hostKeys := scanKeys(ctx, rdb, "dylaris:gwbw:host:*"); len(hostKeys) > 0 {
			if vals, err := rdb.MGet(ctx, hostKeys...).Result(); err == nil {
				for _, v := range vals {
					raw, ok := v.(string)
					if !ok {
						continue
					}
					var agg hostAggregate
					if json.Unmarshal([]byte(raw), &agg) != nil {
						continue
					}
					u, known := utilPct(agg.TxBps, agg.BudgetMbit)
					ov.Hosts = append(ov.Hosts, GatewayHostView{
						Host: agg.Host, RxBps: agg.RxBps, TxBps: agg.TxBps, BudgetMbit: agg.BudgetMbit,
						UtilPct: u, CapKnown: known, CapMismatch: agg.CapMismatch, Components: perHost[agg.Host],
					})
				}
			}
		}
	}
	if st != nil {
		if rows, err := st.GetGatewayBandwidthHistory(now.Add(-gwbwAlertWindow), "", ""); err == nil {
			ov.Alerts = evaluateAlerts(rows, gwbwAlertThresholdPct, gwbwAlertWindow, now)
		}
	}
	sort.Slice(ov.Components, func(i, j int) bool {
		if ov.Components[i].Host != ov.Components[j].Host {
			return ov.Components[i].Host < ov.Components[j].Host
		}
		if ov.Components[i].Component != ov.Components[j].Component {
			return ov.Components[i].Component < ov.Components[j].Component
		}
		return ov.Components[i].ID < ov.Components[j].ID
	})
	sort.Slice(ov.Hosts, func(i, j int) bool { return ov.Hosts[i].Host < ov.Hosts[j].Host })
	return ov
}

// RebalanceView is the F3 rebalancer status for the panel: the current mode and
// the most recent decisions (dry-run would-be moves and armed applied moves).
type RebalanceView struct {
	Mode      string         `json:"mode"`
	Decisions []warpDecision `json:"decisions"`
}

// LoadRebalanceView reads the mode setting + the recent-decisions Redis feed.
// Best-effort: a Redis error yields an empty decisions list, never an error.
func LoadRebalanceView(ctx context.Context, rc *redis.Client, f *FeatureFlags) RebalanceView {
	view := RebalanceView{Mode: f.WarpRebalanceMode(ctx), Decisions: []warpDecision{}}
	if rc == nil {
		return view
	}
	raws, err := rc.LRange(ctx, warpRebalanceDecisionsKey, 0, warpRebalanceDecisionsMax-1).Result()
	if err != nil {
		return view
	}
	for _, raw := range raws {
		var d warpDecision
		if json.Unmarshal([]byte(raw), &d) == nil {
			view.Decisions = append(view.Decisions, d)
		}
	}
	return view
}

// scanKeys returns every key matching the pattern via a cursor SCAN (bounded:
// the gwbw mirror keys have a 90s TTL and low cardinality). A SCAN error returns
// what was collected so far.
func scanKeys(ctx context.Context, rdb *redis.Client, match string) []string {
	var keys []string
	var cursor uint64
	for {
		batch, next, err := rdb.Scan(ctx, cursor, match, 100).Result()
		if err != nil {
			return keys
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return keys
}
