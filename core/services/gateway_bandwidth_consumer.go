package services

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	"dylaris-core/store"
	"dylaris-pkg/protocol"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// GatewayBandwidthConsumerService ingests the edge/warp/beam telemetry streams,
// keeps the latest per-component sample in memory, aggregates per swarm host,
// mirrors the view to Redis for the panel, and persists one downsampled row per
// component per persistInterval. Leader-gated: only one Core persists + mirrors.
type GatewayBandwidthConsumerService struct {
	store  store.Store
	redis  *redis.Client
	coreID string
	leader leader.Election

	mu      sync.Mutex
	latest  map[string]protocol.GatewayStats // keyed by component ID
	streams map[string]bool                  // stream keys already being consumed
}

const (
	gwbwGroup           = "dylaris-core-gwbw"
	gwbwPersistInterval = 30 * time.Second // downsample cadence
	gwbwScanInterval    = 30 * time.Second
	gwbwMirrorTTL       = 90 * time.Second // > persist interval so a stale host still shows briefly
)

func NewGatewayBandwidthConsumerService(s store.Store, r *redis.Client, coreID string) *GatewayBandwidthConsumerService {
	return &GatewayBandwidthConsumerService{
		store:   s,
		redis:   r,
		coreID:  coreID,
		latest:  make(map[string]protocol.GatewayStats),
		streams: make(map[string]bool),
	}
}

// SetLeader wires the leader-election gate. Call once at boot.
func (s *GatewayBandwidthConsumerService) SetLeader(l leader.Election) { s.leader = l }

func (s *GatewayBandwidthConsumerService) Start(ctx context.Context) {
	if s.redis == nil {
		return
	}
	log.Println("Gateway Bandwidth Consumer started")
	go s.scanLoop(ctx)
	go s.persistLoop(ctx)
}

// scanLoop discovers the component stats streams and starts one consumer
// goroutine per newly seen stream. Consuming is unconditional (cheap); only
// persistence + the Redis mirror are leader-gated.
func (s *GatewayBandwidthConsumerService) scanLoop(ctx context.Context) {
	s.scan(ctx)
	t := time.NewTicker(gwbwScanInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.scan(ctx)
		}
	}
}

func (s *GatewayBandwidthConsumerService) scan(ctx context.Context) {
	for _, k := range s.discoverStreams(ctx) {
		s.mu.Lock()
		seen := s.streams[k]
		if !seen {
			s.streams[k] = true
		}
		s.mu.Unlock()
		if !seen {
			go s.consume(ctx, k)
		}
	}
}

// discoverStreams enumerates the edge/beam/warp components and returns their
// stats stream keys. Edges + beams live in SETs; warp leaders are known by
// their per-leader :alive liveness key.
func (s *GatewayBandwidthConsumerService) discoverStreams(ctx context.Context) []string {
	var keys []string
	if edges, err := s.redis.SMembers(ctx, "sys:edges").Result(); err == nil {
		for _, id := range edges {
			keys = append(keys, "dylaris:edge:"+id+":stats")
		}
	}
	if beams, err := s.redis.SMembers(ctx, "sys:beams").Result(); err == nil {
		for _, id := range beams {
			keys = append(keys, "dylaris:beam:"+id+":stats")
		}
	}
	// Warp: scan the per-leader alive keys (dylaris:warp:{id}:alive).
	var cursor uint64
	for {
		batch, next, err := s.redis.Scan(ctx, cursor, "dylaris:warp:*:alive", 100).Result()
		if err != nil {
			break
		}
		for _, ak := range batch {
			id := strings.TrimSuffix(strings.TrimPrefix(ak, "dylaris:warp:"), ":alive")
			if id != "" {
				keys = append(keys, "dylaris:warp:"+id+":stats")
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return keys
}

// consume reads one stream via a shared consumer group and updates the latest
// map. It parses with protocol.ParseGatewayStats so an unknown-version record
// is ignored (acked, not stored).
func (s *GatewayBandwidthConsumerService) consume(ctx context.Context, streamKey string) {
	s.redis.XGroupCreateMkStream(ctx, streamKey, gwbwGroup, "0")
	log.Printf("Gateway bandwidth consumer started for %s", streamKey)
	for {
		if ctx.Err() != nil {
			return
		}
		res, err := s.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    gwbwGroup,
			Consumer: s.coreID,
			Streams:  []string{streamKey, ">"},
			Count:    50,
			Block:    5 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(5 * time.Second)
			continue
		}
		for _, st := range res {
			var ackIDs []string
			for _, msg := range st.Messages {
				ackIDs = append(ackIDs, msg.ID)
				data, ok := msg.Values["data"].(string)
				if !ok {
					continue
				}
				gs, known, perr := protocol.ParseGatewayStats([]byte(data))
				if perr != nil || !known || gs.ID == "" {
					continue // bad json or unknown version: ack + drop
				}
				s.mu.Lock()
				s.latest[gs.ID] = gs
				s.mu.Unlock()
			}
			if len(ackIDs) > 0 {
				s.redis.XAck(ctx, streamKey, gwbwGroup, ackIDs...)
			}
		}
	}
}

// persistLoop runs the leader-gated downsample: every persistInterval it
// snapshots the latest map, mirrors the per-component + per-host view to Redis,
// and writes one row per component to gateway_bandwidth_stats.
func (s *GatewayBandwidthConsumerService) persistLoop(ctx context.Context) {
	t := time.NewTicker(gwbwPersistInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.leader != nil && !s.leader.IsLeader() {
				continue
			}
			s.persistOnce(ctx)
		}
	}
}

func (s *GatewayBandwidthConsumerService) persistOnce(ctx context.Context) {
	s.mu.Lock()
	snapshot := make(map[string]protocol.GatewayStats, len(s.latest))
	for k, v := range s.latest {
		snapshot[k] = v
	}
	s.mu.Unlock()
	if len(snapshot) == 0 {
		return
	}

	now := time.Now()
	rows := make([]models.GatewayBandwidthRow, 0, len(snapshot))
	for _, gs := range snapshot {
		rows = append(rows, models.GatewayBandwidthRow{
			Time: now, Component: gs.Component, ID: gs.ID, Host: gs.Host,
			Region: gs.Region, RxBps: gs.RxBps, TxBps: gs.TxBps, CapMbit: gs.CapMbit,
		})
		if b, err := json.Marshal(gs); err == nil {
			s.redis.Set(ctx, "dylaris:gwbw:component:"+gs.ID, b, gwbwMirrorTTL)
		}
	}
	for host, agg := range aggregateByHost(snapshot) {
		if b, err := json.Marshal(agg); err == nil {
			s.redis.Set(ctx, "dylaris:gwbw:host:"+host, b, gwbwMirrorTTL)
		}
	}
	if err := s.store.InsertGatewayBandwidthBatch(rows); err != nil {
		log.Printf("gateway bandwidth persist error: %v", err)
	}
}

// hostAggregate is the summed live throughput of all components co-located on
// one swarm host, plus the resolved host bandwidth budget.
type hostAggregate struct {
	Host        string `json:"host"`
	RxBps       uint64 `json:"rxBps"`
	TxBps       uint64 `json:"txBps"`
	BudgetMbit  int    `json:"budgetMbit"`
	CapMismatch bool   `json:"capMismatch"`
}

// aggregateByHost groups the latest per-component stats by host and applies the
// host-budget rule: the budget is the MAX cap_mbit among the host's components
// (they describe the same shared uplink, so max tolerates a misconfig without
// triple-counting). CapMismatch flags a host whose components report DIFFERENT
// non-zero caps so the panel can warn. cap_mbit==0 (unset) contributes to
// neither the budget nor the mismatch check. Components with an empty host are
// skipped (they cannot be co-located).
func aggregateByHost(latest map[string]protocol.GatewayStats) map[string]hostAggregate {
	out := map[string]hostAggregate{}
	for _, gs := range latest {
		if gs.Host == "" {
			continue
		}
		a := out[gs.Host]
		a.Host = gs.Host
		a.RxBps += gs.RxBps
		a.TxBps += gs.TxBps
		if gs.CapMbit > 0 {
			if a.BudgetMbit != 0 && a.BudgetMbit != gs.CapMbit {
				a.CapMismatch = true
			}
			if gs.CapMbit > a.BudgetMbit {
				a.BudgetMbit = gs.CapMbit
			}
		}
		out[gs.Host] = a
	}
	return out
}
