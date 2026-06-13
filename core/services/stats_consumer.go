package services

import (
	"context"
	"dylaris-core/models"
	"dylaris-core/store"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type StatsConsumerService struct {
	store  store.Store
	redis  *redis.Client
	coreID string

	mu       sync.Mutex
	nodeKeys map[string]bool // tracked node batch stream keys
}

type statsBatchPayload struct {
	TS    int64            `json:"ts"`
	Stats []statsBatchItem `json:"stats"`
}

type statsBatchItem struct {
	UUID       string  `json:"uuid"`
	CPU        float64 `json:"cpu"`
	CPULimit   float64 `json:"cpuLimit"`
	MemUsed    int64   `json:"memUsed"`
	MemLimit   int64   `json:"memLimit"`
	Players    int     `json:"players"`
	MaxPlayers int     `json:"maxPlayers"`
}

func NewStatsConsumerService(s store.Store, r *redis.Client, coreID string) *StatsConsumerService {
	return &StatsConsumerService{
		store:    s,
		redis:    r,
		coreID:   coreID,
		nodeKeys: make(map[string]bool),
	}
}

func (s *StatsConsumerService) Start() {
	log.Println("Stats Consumer Service started")

	// Scan for node batch streams every 30s
	go func() {
		s.scanNodes()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.scanNodes()
		}
	}()
}

func (s *StatsConsumerService) scanNodes() {
	ctx := context.Background()
	keys, err := s.redis.Keys(ctx, "dylaris:discovery:*").Result()
	if err != nil {
		return
	}

	for _, key := range keys {
		val, err := s.redis.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var hb struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(val), &hb); err != nil || hb.ID == "" {
			continue
		}

		batchKey := "dylaris:node:" + hb.ID + ":stats:batch"

		s.mu.Lock()
		already := s.nodeKeys[batchKey]
		if !already {
			s.nodeKeys[batchKey] = true
		}
		s.mu.Unlock()

		if !already {
			go s.consumeStream(batchKey)
		}
	}
}

func (s *StatsConsumerService) consumeStream(streamKey string) {
	ctx := context.Background()
	group := "dylaris-core-stats"
	consumer := s.coreID

	// XGroupCreateMkStream is idempotent — error on re-create is expected and ignored.
	s.redis.XGroupCreateMkStream(ctx, streamKey, group, "0").Err()

	log.Printf("Stats consumer started for stream %s", streamKey)

	for {
		results, err := s.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{streamKey, ">"},
			Count:    50,
			Block:    10 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			// Stream may have been deleted; back off and retry
			time.Sleep(5 * time.Second)
			continue
		}

		for _, stream := range results {
			var rows []models.ServerStatRow
			var ackIDs []string

			for _, msg := range stream.Messages {
				data, ok := msg.Values["data"].(string)
				if !ok {
					s.redis.XAck(ctx, streamKey, group, msg.ID) // undeliverable: ack now
					continue
				}

				var batch statsBatchPayload
				if err := json.Unmarshal([]byte(data), &batch); err != nil {
					s.redis.XAck(ctx, streamKey, group, msg.ID) // undeliverable: ack now
					continue
				}

				ts := time.Unix(batch.TS, 0)
				for _, item := range batch.Stats {
					rows = append(rows, models.ServerStatRow{
						Time:       ts,
						ServerUUID: item.UUID,
						CPU:        item.CPU,
						CPULimit:   item.CPULimit,
						MemUsed:    item.MemUsed,
						MemLimit:   item.MemLimit,
						Players:    item.Players,
						MaxPlayers: item.MaxPlayers,
					})
				}
				ackIDs = append(ackIDs, msg.ID)
			}

			if len(rows) > 0 {
				if err := s.store.InsertStatsBatch(rows); err != nil {
					log.Printf("Stats batch insert error: %v", err)
					continue // don't ACK on failure so we retry
				}
			}

			if len(ackIDs) > 0 {
				s.redis.XAck(ctx, streamKey, group, ackIDs...)
			}
		}
	}
}
