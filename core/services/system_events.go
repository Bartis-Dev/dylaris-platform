// Package-level helper for the Phase 7 system-events Pub/Sub channel.
//
// The channel `dylaris:system:events` carries small JSON events that the panel
// consumes via SSE (GET /api/system/events) to refresh cached config without
// polling. Publishes are fire-and-forget — Redis being down is logged and
// swallowed so business logic never blocks on event delivery.
package services

import (
	"context"
	"encoding/json"
	"log"

	"github.com/redis/go-redis/v9"
)

// SystemEventsChannel is the single Redis Pub/Sub channel for platform-wide
// config-change events. One channel keeps subscriber wiring trivial (every
// panel session opens exactly one SSE stream); event type discrimination
// lives in the payload.
const SystemEventsChannel = "dylaris:system:events"

// SystemEvent is the wire envelope. Type is a dot-namespaced string
// (e.g. "regions.changed", "servers.changed"); Payload is optional and
// usually empty — the panel just re-fetches the relevant list on receipt.
type SystemEvent struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// SystemEventsPublisher wraps Redis publish so callers don't import redis
// directly and so we can swap the transport later (e.g. NATS) without
// touching handlers.
type SystemEventsPublisher struct {
	rdb *redis.Client
}

func NewSystemEventsPublisher(rdb *redis.Client) *SystemEventsPublisher {
	return &SystemEventsPublisher{rdb: rdb}
}

// Publish fires an event into the channel. Nil-safe on both receiver and
// rdb so handlers can call h.state.Events.Publish unconditionally even in
// tests where AppState has no Redis.
func (p *SystemEventsPublisher) Publish(ctx context.Context, eventType string, payload map[string]interface{}) {
	if p == nil || p.rdb == nil || eventType == "" {
		return
	}
	data, err := json.Marshal(SystemEvent{Type: eventType, Payload: payload})
	if err != nil {
		log.Printf("system-events: marshal failed for %s: %v", eventType, err)
		return
	}
	if err := p.rdb.Publish(ctx, SystemEventsChannel, data).Err(); err != nil {
		log.Printf("system-events: publish %s failed: %v", eventType, err)
	}
}
