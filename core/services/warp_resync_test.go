package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestProcessResyncRequest_PushesFullPeerSetAndClearsKey(t *testing.T) {
	svc, fs, mr := enrollTestService(t)
	fs.InsertWarpPeer(storePeer(1, "pub1", "10.0.99.2"))
	fs.InsertWarpPeer(storePeer(1, "pub2", "10.0.99.3"))
	ctx := context.Background()
	svc.redis.Set(ctx, "dylaris:warp:resync-request", "leader-01", 0)

	if err := svc.processResyncRequest(ctx); err != nil {
		t.Fatalf("resync: %v", err)
	}

	vals, _ := mr.List("dylaris:warp:leader-01:queue")
	if len(vals) != 1 {
		t.Fatalf("expected 1 resync command, got %d", len(vals))
	}
	var cmd struct {
		Type  string `json:"type"`
		Peers []struct {
			Pubkey     string   `json:"pubkey"`
			WGIP       string   `json:"wg_ip"`
			AllowedIPs []string `json:"allowed_ips"`
		} `json:"peers"`
	}
	if err := json.Unmarshal([]byte(vals[0]), &cmd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cmd.Type != "resync" || len(cmd.Peers) != 2 {
		t.Fatalf("bad resync command: %+v", cmd)
	}
	if _, err := svc.redis.Get(ctx, "dylaris:warp:resync-request").Result(); err != redis.Nil {
		t.Fatal("resync-request key should be cleared after processing")
	}
}
