package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestSystemEventsPublisher_NilSafety pins the nil-safety contract documented
// on Publish: handlers call h.state.Events.Publish unconditionally, including
// in tests where AppState has no Redis, so a nil receiver, a nil rdb, and an
// empty event type must all be silent no-ops rather than panics.
func TestSystemEventsPublisher_NilSafety(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var p *SystemEventsPublisher
		p.Publish(context.Background(), "servers.changed", nil) // must not panic
	})

	t.Run("nil redis client", func(t *testing.T) {
		p := &SystemEventsPublisher{}
		p.Publish(context.Background(), "servers.changed", nil) // must not panic
	})

	t.Run("empty event type is a no-op even with a live client", func(t *testing.T) {
		rdb := newQueueTestRedis(t)
		p := NewSystemEventsPublisher(rdb)
		ctx := context.Background()

		sub := rdb.Subscribe(ctx, SystemEventsChannel)
		defer sub.Close()
		if _, err := sub.Receive(ctx); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		ch := sub.Channel()

		p.Publish(ctx, "", nil)

		select {
		case msg := <-ch:
			t.Fatalf("unexpected publish for an empty event type: %+v", msg)
		case <-time.After(200 * time.Millisecond):
			// expected: nothing published
		}
	})
}

func TestSystemEventsPublisher_ValidEvent_DeliversTypeAndPayload(t *testing.T) {
	rdb := newQueueTestRedis(t)
	p := NewSystemEventsPublisher(rdb)
	ctx := context.Background()

	sub := rdb.Subscribe(ctx, SystemEventsChannel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ch := sub.Channel()

	p.Publish(ctx, "servers.changed", map[string]interface{}{"serverId": "srv-1"})

	select {
	case msg := <-ch:
		var ev SystemEvent
		if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		if ev.Type != "servers.changed" {
			t.Errorf("Type = %q, want servers.changed", ev.Type)
		}
		if ev.Payload["serverId"] != "srv-1" {
			t.Errorf("Payload = %+v, want serverId=srv-1", ev.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the published event")
	}
}
