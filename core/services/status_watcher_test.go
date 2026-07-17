package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"
)

// statusWatcherFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods StatusWatcherService touches
// are overridden.
type statusWatcherFakeStore struct {
	store.Store

	serversByUUID  map[string]models.Server
	listServers    []models.Server
	listServersErr error
	edgeMotd       []store.ServerEdgeMotd
	edgeMotdErr    error

	statusCalls []schedStatusCall // {id, status}
	portCalls   []portCall
}

type portCall struct {
	id                      int
	hostPort, containerPort int
}

func (f *statusWatcherFakeStore) GetServerByUUID(uuid string) (*models.Server, error) {
	if s, ok := f.serversByUUID[uuid]; ok {
		return &s, nil
	}
	return nil, errors.New("server not found")
}

func (f *statusWatcherFakeStore) UpdateServerStatus(id int, status string) error {
	f.statusCalls = append(f.statusCalls, schedStatusCall{id, status})
	return nil
}

func (f *statusWatcherFakeStore) UpdateServerPorts(id, hostPort, containerPort int) error {
	f.portCalls = append(f.portCalls, portCall{id, hostPort, containerPort})
	return nil
}

func (f *statusWatcherFakeStore) ListServers(filterByUser string) ([]models.Server, error) {
	return f.listServers, f.listServersErr
}

func (f *statusWatcherFakeStore) ListServerEdgeMotd() ([]store.ServerEdgeMotd, error) {
	return f.edgeMotd, f.edgeMotdErr
}

func newStatusWatcherTest(t *testing.T, fs *statusWatcherFakeStore) *StatusWatcherService {
	t.Helper()
	rdb := newQueueTestRedis(t)
	return &StatusWatcherService{store: fs, redis: rdb}
}

func TestScan_StatusChange_UpdatesStoreAndPublishesOnce(t *testing.T) {
	fs := &statusWatcherFakeStore{
		serversByUUID: map[string]models.Server{
			"srv-1": {ID: 1, UUID: "srv-1", Status: "offline"},
			"srv-2": {ID: 2, UUID: "srv-2", Status: "offline"}, // no change expected
		},
	}
	svc := newStatusWatcherTest(t, fs)
	ctx := context.Background()

	if err := svc.redis.Set(ctx, "dylaris:server:srv-1:status", "online", 0).Err(); err != nil {
		t.Fatalf("seed status key: %v", err)
	}
	if err := svc.redis.Set(ctx, "dylaris:server:srv-2:status", "offline", 0).Err(); err != nil {
		t.Fatalf("seed status key: %v", err)
	}

	sub := svc.redis.Subscribe(ctx, SystemEventsChannel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ch := sub.Channel()

	svc.scan()

	if len(fs.statusCalls) != 1 || fs.statusCalls[0] != (schedStatusCall{1, "online"}) {
		t.Errorf("statusCalls = %+v, want exactly [{1 online}]", fs.statusCalls)
	}

	// Both scanned status keys must be consumed (deleted) regardless of whether
	// they changed, so the same event isn't reprocessed on the next tick.
	for _, key := range []string{"dylaris:server:srv-1:status", "dylaris:server:srv-2:status"} {
		if n, _ := svc.redis.Exists(ctx, key).Result(); n != 0 {
			t.Errorf("key %s should be deleted after scan, still exists", key)
		}
	}

	select {
	case msg := <-ch:
		var ev SystemEvent
		if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		if ev.Type != "servers.changed" {
			t.Fatalf("event type = %q, want servers.changed", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for servers.changed event")
	}

	select {
	case msg := <-ch:
		t.Fatalf("unexpected extra event: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: exactly one event for this whole tick
	}
}

func TestScan_NoStatusChange_DoesNotPublish(t *testing.T) {
	fs := &statusWatcherFakeStore{
		serversByUUID: map[string]models.Server{
			"srv-1": {ID: 1, UUID: "srv-1", Status: "online"},
		},
	}
	svc := newStatusWatcherTest(t, fs)
	ctx := context.Background()
	if err := svc.redis.Set(ctx, "dylaris:server:srv-1:status", "online", 0).Err(); err != nil {
		t.Fatalf("seed status key: %v", err)
	}

	sub := svc.redis.Subscribe(ctx, SystemEventsChannel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ch := sub.Channel()

	svc.scan()

	if len(fs.statusCalls) != 0 {
		t.Errorf("statusCalls = %+v, want none (status unchanged)", fs.statusCalls)
	}

	select {
	case msg := <-ch:
		t.Fatalf("unexpected event published for an unchanged status: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: no event
	}
}

func TestScan_MalformedKey_LeftUntouchedAndSkipped(t *testing.T) {
	fs := &statusWatcherFakeStore{serversByUUID: map[string]models.Server{}}
	svc := newStatusWatcherTest(t, fs)
	ctx := context.Background()

	// This still matches the SCAN glob "dylaris:server:*:status" (the wildcard
	// spans colons too) but splits into 5 parts, not the expected 4.
	const malformed = "dylaris:server:aaa:bbb:status"
	if err := svc.redis.Set(ctx, malformed, "online", 0).Err(); err != nil {
		t.Fatalf("seed malformed key: %v", err)
	}

	svc.scan()

	if len(fs.statusCalls) != 0 {
		t.Errorf("expected no store lookups for a malformed key, got %+v", fs.statusCalls)
	}
	if n, _ := svc.redis.Exists(ctx, malformed).Result(); n != 1 {
		t.Error("malformed key should be left in place (never reaches the Del call), but it is gone")
	}
}

func TestSyncPortsFromRedis(t *testing.T) {
	fs := &statusWatcherFakeStore{
		serversByUUID: map[string]models.Server{
			"srv-changed":           {ID: 10, HostPort: 25565, ContainerPort: 25565},
			"srv-same":              {ID: 11, HostPort: 25580, ContainerPort: 25565},
			"srv-default-container": {ID: 12, HostPort: 1000, ContainerPort: 0},
		},
	}
	svc := newStatusWatcherTest(t, fs)
	ctx := context.Background()

	seed := map[string]string{
		"dylaris:node:5:port:srv-changed":           "25566", // differs -> update
		"dylaris:node:5:port:srv-same":              "25580", // same -> no update
		"dylaris:node:5:port:srv-default-container": "2000",  // differs, container port defaults to 25565
		"dylaris:node:5:port:srv-unknown":           "1234",  // GetServerByUUID errors -> skip
		"dylaris:node:5:port:srv-bad-port":          "not-a-number",
	}
	for k, v := range seed {
		if err := svc.redis.Set(ctx, k, v, 0).Err(); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}

	changed := svc.syncPortsFromRedis(ctx)
	if !changed {
		t.Fatal("expected changed=true because at least one port differed")
	}

	byID := map[int]portCall{}
	for _, c := range fs.portCalls {
		byID[c.id] = c
	}
	if len(byID) != 2 {
		t.Fatalf("portCalls = %+v, want exactly 2 updates (srv-changed, srv-default-container)", fs.portCalls)
	}
	if c := byID[10]; c.hostPort != 25566 || c.containerPort != 25565 {
		t.Errorf("srv-changed update = %+v, want hostPort=25566 containerPort=25565", c)
	}
	if c := byID[12]; c.hostPort != 2000 || c.containerPort != 25565 {
		t.Errorf("srv-default-container update = %+v, want hostPort=2000 containerPort=25565 (defaulted)", c)
	}
}

func TestSyncPortsFromRedis_NoChanges_ReturnsFalse(t *testing.T) {
	fs := &statusWatcherFakeStore{
		serversByUUID: map[string]models.Server{
			"srv-1": {ID: 1, HostPort: 25565, ContainerPort: 25565},
		},
	}
	svc := newStatusWatcherTest(t, fs)
	ctx := context.Background()
	if err := svc.redis.Set(ctx, "dylaris:node:1:port:srv-1", "25565", 0).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if changed := svc.syncPortsFromRedis(ctx); changed {
		t.Error("expected changed=false when no port differs")
	}
	if len(fs.portCalls) != 0 {
		t.Errorf("expected no UpdateServerPorts calls, got %+v", fs.portCalls)
	}
}

func TestPublishDesiredStates_WritesEachServersDesiredState(t *testing.T) {
	fs := &statusWatcherFakeStore{
		listServers: []models.Server{
			{UUID: "srv-a", DesiredState: "online"},
			{UUID: "srv-b", DesiredState: "stopped"},
		},
	}
	svc := newStatusWatcherTest(t, fs)
	ctx := context.Background()

	svc.publishDesiredStates(ctx)

	a, err := svc.redis.Get(ctx, "dylaris:server:srv-a:desired_state").Result()
	if err != nil || a != "online" {
		t.Errorf("srv-a desired_state = %q (err=%v), want online", a, err)
	}
	b, err := svc.redis.Get(ctx, "dylaris:server:srv-b:desired_state").Result()
	if err != nil || b != "stopped" {
		t.Errorf("srv-b desired_state = %q (err=%v), want stopped", b, err)
	}
}

func TestPublishDesiredStates_ListError_NoWrites(t *testing.T) {
	fs := &statusWatcherFakeStore{listServersErr: errors.New("db down")}
	svc := newStatusWatcherTest(t, fs)
	ctx := context.Background()

	svc.publishDesiredStates(ctx) // must not panic; simply nothing to write

	n, err := svc.redis.Exists(ctx, "dylaris:server:srv-a:desired_state").Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if n != 0 {
		t.Error("expected no desired_state keys written when ListServers fails")
	}
}
