package services

import (
	"context"
	"dylaris-core/metrics"
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// MetricsCollector samples the numbers Core already knows and hands them to the
// long-term recorder.
//
// Sampling only. It creates no new instrumentation: every value here comes from
// something that is already published - the node heartbeats, the edge registry,
// the link keep-alives, the store. That is what lets the record start on the
// day this ships rather than after the rest of the work, and the clock is the
// point: a month of history cannot be collected retroactively.
type MetricsCollector struct {
	store    store.Store
	redis    *redis.Client
	recorder *metrics.Recorder
	flags    *FeatureFlags
	leader   leader.Election
}

const (
	// Matches the cadence the gateway bandwidth consumer already samples at, so
	// the two sit on the same grid and can be read against each other.
	metricsSampleInterval = 30 * time.Second
)

// MetricsEnabledSetting is the switch. Default OFF: recording a year of history
// is a decision an operator makes, not something that starts because the
// software supports it.
const MetricsEnabledSetting = "feature_metrics_enabled"

func NewMetricsCollector(s store.Store, r *redis.Client, rec *metrics.Recorder, flags *FeatureFlags) *MetricsCollector {
	return &MetricsCollector{store: s, redis: r, recorder: rec, flags: flags}
}

// SetLeader wires the leader-election gate. Call once at boot.
func (c *MetricsCollector) SetLeader(l leader.Election) { c.leader = l }

func (c *MetricsCollector) Start(ctx context.Context) {
	if c.recorder == nil {
		return
	}
	log.Println("Metrics collector started")
	go c.loop(ctx)
	go c.recorder.Run(ctx, metrics.FlushInterval)
}

func (c *MetricsCollector) loop(ctx context.Context) {
	t := time.NewTicker(metricsSampleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.sampleOnce(ctx)
		}
	}
}

// sampleOnce takes one reading of everything.
//
// Leader-gated. Core runs several replicas and each one sees the same fleet, so
// without this every replica would fold the same numbers into the same bucket
// and a two-replica install would report twice the players. The counter that
// bills tenants was once wrong in exactly this way.
func (c *MetricsCollector) sampleOnce(ctx context.Context) {
	if c.leader != nil && !c.leader.IsLeader() {
		return
	}
	if c.flags != nil && !c.flags.Get(ctx, MetricsEnabledSetting, false) {
		return
	}
	now := time.Now()
	c.samplePlatform(ctx, now)
	c.sampleGateway(ctx, now)
}

// obs is the shorthand this file uses; the recorder tolerates a nil receiver so
// no call site needs its own guard.
func (c *MetricsCollector) obs(metric, subject, region string, v float64, at time.Time) {
	c.recorder.Observe(metrics.Key{Metric: metric, Subject: subject, Region: region}, v, at)
}

func (c *MetricsCollector) samplePlatform(ctx context.Context, now time.Time) {
	if c.store == nil {
		return
	}
	nodes, err := c.store.ListNodes()
	if err != nil {
		return
	}

	var online, platform, external, byon int
	for i := range nodes {
		n := &nodes[i]
		if n.Status == "online" {
			online++
		}
		switch n.Kind() {
		case models.NodeKindBYON:
			byon++
		case models.NodeKindExternal:
			external++
		default:
			platform++
		}
		c.sampleNode(ctx, n, now)
	}

	c.obs("platform.nodes", "", "", float64(len(nodes)), now)
	c.obs("platform.nodes_online", "", "", float64(online), now)
	c.obs("platform.nodes_platform", "", "", float64(platform), now)
	c.obs("platform.nodes_external", "", "", float64(external), now)
	c.obs("platform.nodes_byon", "", "", float64(byon), now)

	if users, err := c.store.CountUsers(); err == nil {
		c.obs("platform.users", "", "", float64(users), now)
	}

	servers, err := c.store.ListServers("")
	if err == nil {
		var up int
		for i := range servers {
			if servers[i].Status == "online" {
				up++
			}
		}
		c.obs("platform.servers", "", "", float64(len(servers)), now)
		c.obs("platform.servers_online", "", "", float64(up), now)
	}
}

// sampleNode records one machine's live figures.
//
// `node.up` is 1 or 0, and it is the series that answers "what was the uptime".
// A boolean averaged over a bucket IS the availability fraction, which is why
// it is recorded as a number rather than inferred later from gaps - a gap is
// ambiguous between "down" and "nothing was sampling".
func (c *MetricsCollector) sampleNode(_ context.Context, n *models.Node, now time.Time) {
	up := 0.0
	if n.Status == "online" {
		up = 1
	}
	c.obs("node.up", n.Token, n.Region, up, now)
	if up == 0 {
		// CPU and RAM from an offline node are the last values seen, not
		// current ones. Recording them would put a flatline into the average
		// that reads like a healthy idle machine.
		return
	}
	if n.CPUUsage >= 0 {
		c.obs("node.cpu_pct", n.Token, n.Region, n.CPUUsage, now)
	}
	if n.RAMTotal > 0 {
		used := float64(n.RAMTotal) - float64(n.RAMFree)
		c.obs("node.ram_used_bytes", n.Token, n.Region, used, now)
		c.obs("node.ram_pct", n.Token, n.Region, used/float64(n.RAMTotal)*100, now)
	}
	c.obs("node.servers", n.Token, n.Region, float64(n.ServerCount), now)
}

func (c *MetricsCollector) sampleGateway(ctx context.Context, now time.Time) {
	if c.redis == nil {
		return
	}
	edges := GetEdgesFromRedis(ctx, c.redis)
	var onlineEdges int
	var players int64
	for _, e := range edges {
		up := 0.0
		if e.Status == "online" {
			up = 1
			onlineEdges++
		}
		c.obs("edge.up", e.EdgeID, e.Region, up, now)
		if up == 0 || e.Stats == nil {
			continue
		}
		s := e.Stats
		c.obs("edge.cpu_pct", e.EdgeID, e.Region, s.CPU, now)
		c.obs("edge.ram_pct", e.EdgeID, e.Region, s.RAMPercent, now)
		c.obs("edge.rx_bps", e.EdgeID, e.Region, float64(s.RxSpeed), now)
		c.obs("edge.tx_bps", e.EdgeID, e.Region, float64(s.TxSpeed), now)
		c.obs("edge.players", e.EdgeID, e.Region, float64(s.ActiveMCStreams), now)
		players += s.ActiveMCStreams
	}
	if len(edges) > 0 {
		c.obs("platform.edges", "", "", float64(len(edges)), now)
		c.obs("platform.edges_online", "", "", float64(onlineEdges), now)
		// The players number the whole record is built around. It comes from
		// the edges because that is where a connection actually terminates.
		c.obs("platform.players", "", "", float64(players), now)
	}

	links := GetLinksFromRedis(ctx, c.redis)
	if len(links) > 0 {
		var up int
		for _, l := range links {
			if l.Online {
				up++
			}
		}
		c.obs("platform.links", "", "", float64(len(links)), now)
		c.obs("platform.links_online", "", "", float64(up), now)
	}

	if routes := CountRoutesFromRedis(ctx, c.redis); routes >= 0 {
		c.obs("platform.routes", "", "", float64(routes), now)
	}
}
