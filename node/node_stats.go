package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"dylaris-agent"

	"github.com/redis/go-redis/v9"
)

type NodeSystemStats struct {
	Timestamp  int64   `json:"ts"`
	NodeID     string  `json:"node_id"`
	CPUPercent float64 `json:"cpu"`
	RAMUsed    uint64  `json:"ram_used"`
	RAMTotal   uint64  `json:"ram_total"`
	RAMPercent float64 `json:"ram_pct"`
	NetRxSpeed uint64  `json:"rx_speed"`
	NetTxSpeed uint64  `json:"tx_speed"`
}

func StartNodeSystemStats(ctx context.Context, rdb *redis.Client, nid string, streamMaxLen int64, mon *agent.Monitor) {
	if mon == nil {
		return
	}
	streamKey := "dylaris:node:" + nid + ":system"
	log.Printf("[Metrics] Node system stats started (stream maxlen: %d)", streamMaxLen)

	// Publish every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, err := mon.Snapshot()
			if err != nil {
				continue
			}
			payload := NodeSystemStats{
				Timestamp:  snap.Timestamp,
				NodeID:     nid,
				CPUPercent: snap.CPUPercent,
				RAMUsed:    snap.RAMUsed,
				RAMTotal:   snap.RAMTotal,
				RAMPercent: snap.RAMPercent,
				NetRxSpeed: snap.NetRxSpeed,
				NetTxSpeed: snap.NetTxSpeed,
			}
			data, _ := json.Marshal(payload)
			rdb.XAdd(ctx, &redis.XAddArgs{
				Stream: streamKey,
				MaxLen: streamMaxLen,
				Approx: true,
				Values: map[string]interface{}{"data": string(data)},
			})
		}
	}
}
