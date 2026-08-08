package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/store"
)

// schedTaskFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods ScheduledTaskService touches
// are overridden. Any other call would panic - the tests never make one.
type schedTaskFakeStore struct {
	store.Store

	due    []models.ScheduledTask
	dueErr error

	servers map[int]models.Server
	nodes   map[int]models.Node

	billing    map[string]*store.UserBilling
	billingErr error

	desiredCalls []schedDesiredCall
	statusCalls  []schedStatusCall
	enabledCalls []schedEnabledCall
	runRecords   []schedRunRecord
}

type schedDesiredCall struct {
	id    int
	state string
}
type schedStatusCall struct {
	id     int
	status string
}
type schedEnabledCall struct {
	id      int
	enabled bool
	nextRun *time.Time
}
type schedRunRecord struct {
	id      int
	status  string
	errMsg  string
	nextRun *time.Time
}

func (f *schedTaskFakeStore) ListDueScheduledTasks(now time.Time, limit int) ([]models.ScheduledTask, error) {
	return f.due, f.dueErr
}

func (f *schedTaskFakeStore) GetServerByID(id int) (*models.Server, error) {
	if s, ok := f.servers[id]; ok {
		return &s, nil
	}
	return nil, errors.New("server not found")
}

func (f *schedTaskFakeStore) GetNodeByID(id int) (*models.Node, error) {
	if n, ok := f.nodes[id]; ok {
		return &n, nil
	}
	return nil, errors.New("node not found")
}

// GetUserBilling backs the suspension gate on a restart task. An absent entry
// mirrors the real store, which answers "active" for a user with no billing row
// at all - so only an explicit entry or billingErr changes the outcome.
func (f *schedTaskFakeStore) GetUserBilling(userID string) (*store.UserBilling, error) {
	if f.billingErr != nil {
		return nil, f.billingErr
	}
	if b, ok := f.billing[userID]; ok {
		return b, nil
	}
	return &store.UserBilling{UserID: userID, Status: "active"}, nil
}

func (f *schedTaskFakeStore) UpdateServerDesiredState(id int, state string) error {
	f.desiredCalls = append(f.desiredCalls, schedDesiredCall{id, state})
	return nil
}

func (f *schedTaskFakeStore) UpdateServerStatus(id int, status string) error {
	f.statusCalls = append(f.statusCalls, schedStatusCall{id, status})
	return nil
}

func (f *schedTaskFakeStore) SetScheduledTaskEnabled(id int, enabled bool, nextRun *time.Time) error {
	f.enabledCalls = append(f.enabledCalls, schedEnabledCall{id, enabled, nextRun})
	return nil
}

func (f *schedTaskFakeStore) RecordScheduledTaskRun(id int, ranAt time.Time, status, errMsg string, nextRun *time.Time) error {
	f.runRecords = append(f.runRecords, schedRunRecord{id, status, errMsg, nextRun})
	return nil
}

func newSchedTestService(t *testing.T, fs *schedTaskFakeStore) (*ScheduledTaskService, func()) {
	t.Helper()
	rdb := newQueueTestRedis(t)
	svc := &ScheduledTaskService{store: fs, redis: rdb, queue: NewQueueService(rdb)}
	return svc, func() {}
}

func TestComputeNextRun(t *testing.T) {
	from := time.Date(2026, 7, 15, 10, 15, 30, 0, time.UTC)

	cases := []struct {
		name    string
		cron    string
		want    time.Time
		wantErr bool
	}{
		{"every minute", "* * * * *", time.Date(2026, 7, 15, 10, 16, 0, 0, time.UTC), false},
		{"hourly descriptor", "@hourly", time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC), false},
		{"daily descriptor", "@daily", time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), false},
		{"explicit daily", "0 0 * * *", time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), false},
		{"invalid cron", "not a cron", time.Time{}, true},
		{"wrong field count", "* * * *", time.Time{}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ComputeNextRun(c.cron, from)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got next=%v", c.cron, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ComputeNextRun(%q): %v", c.cron, err)
			}
			if !got.Equal(c.want) {
				t.Errorf("ComputeNextRun(%q) = %v, want %v", c.cron, got, c.want)
			}
		})
	}
}

func TestRunDue_NoDueTasks_NoOp(t *testing.T) {
	fs := &schedTaskFakeStore{}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.runRecords) != 0 {
		t.Fatalf("expected no run records, got %+v", fs.runRecords)
	}
}

func TestRunDue_RestartTask_Success(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{1: {ID: 1, UUID: "srv-1", NodeID: 5}},
		nodes:   map[int]models.Node{5: {ID: 5, Token: "node-tok-5"}},
		due: []models.ScheduledTask{
			{ID: 100, ServerID: 1, TaskType: "restart", ScheduleCron: "* * * * *"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.desiredCalls) != 1 || fs.desiredCalls[0] != (schedDesiredCall{1, "online"}) {
		t.Errorf("desiredCalls = %+v, want [{1 online}]", fs.desiredCalls)
	}
	if len(fs.statusCalls) != 1 || fs.statusCalls[0] != (schedStatusCall{1, "starting"}) {
		t.Errorf("statusCalls = %+v, want [{1 starting}]", fs.statusCalls)
	}

	payload := readStreamPayload(t, svc.redis, "dylaris:node:node-tok-5:cmds")
	if payload["action"] != "restart" {
		t.Errorf("action = %v, want restart", payload["action"])
	}
	cfg, _ := payload["config"].(map[string]interface{})
	if cfg["uuid"] != "srv-1" {
		t.Errorf("config.uuid = %v, want srv-1", cfg["uuid"])
	}

	if len(fs.runRecords) != 1 {
		t.Fatalf("runRecords = %+v, want 1 entry", fs.runRecords)
	}
	rr := fs.runRecords[0]
	if rr.status != "ok" || rr.errMsg != "" {
		t.Errorf("run record = %+v, want status=ok errMsg=empty", rr)
	}
	if rr.nextRun == nil {
		t.Errorf("expected nextRun to be set for a valid cron")
	}
	if len(fs.enabledCalls) != 0 {
		t.Errorf("expected no SetScheduledTaskEnabled calls for a valid cron, got %+v", fs.enabledCalls)
	}
}

func TestRunDue_SayTask_Success(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{2: {ID: 2, UUID: "srv-2"}},
		due: []models.ScheduledTask{
			{ID: 200, ServerID: 2, TaskType: "say", ScheduleCron: "@hourly", Payload: "hello world"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	vals, err := svc.redis.LRange(context.Background(), "dylaris:server:srv-2:input", 0, -1).Result()
	if err != nil {
		t.Fatalf("LRange: %v", err)
	}
	if len(vals) != 1 || vals[0] != "say hello world" {
		t.Fatalf("stdin queue = %v, want [say hello world]", vals)
	}

	if len(fs.runRecords) != 1 || fs.runRecords[0].status != "ok" {
		t.Fatalf("run record = %+v, want status=ok", fs.runRecords)
	}
}

func TestRunDue_SayTask_EmptyPayload_RecordsError(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{2: {ID: 2, UUID: "srv-2"}},
		due: []models.ScheduledTask{
			{ID: 201, ServerID: 2, TaskType: "say", ScheduleCron: "@hourly", Payload: ""},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.runRecords) != 1 {
		t.Fatalf("runRecords = %+v, want 1 entry", fs.runRecords)
	}
	rr := fs.runRecords[0]
	if rr.status != "error" || rr.errMsg == "" {
		t.Errorf("run record = %+v, want status=error with a message", rr)
	}
}

func TestRunDue_UnknownTaskType_RecordsError(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{2: {ID: 2, UUID: "srv-2"}},
		due: []models.ScheduledTask{
			{ID: 202, ServerID: 2, TaskType: "bogus", ScheduleCron: "@hourly"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.runRecords) != 1 || fs.runRecords[0].status != "error" {
		t.Fatalf("runRecords = %+v, want single error entry", fs.runRecords)
	}
}

func TestRunDue_ServerNotFound_RecordsError(t *testing.T) {
	fs := &schedTaskFakeStore{
		due: []models.ScheduledTask{
			{ID: 300, ServerID: 999, TaskType: "restart", ScheduleCron: "@hourly"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.runRecords) != 1 || fs.runRecords[0].status != "error" {
		t.Fatalf("runRecords = %+v, want single error entry", fs.runRecords)
	}
}

func TestRunDue_RestartTask_NodeNotFound_RecordsError(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{1: {ID: 1, UUID: "srv-1", NodeID: 5}},
		// nodes map deliberately empty -> GetNodeByID fails
		due: []models.ScheduledTask{
			{ID: 301, ServerID: 1, TaskType: "restart", ScheduleCron: "@hourly"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.runRecords) != 1 || fs.runRecords[0].status != "error" {
		t.Fatalf("runRecords = %+v, want single error entry", fs.runRecords)
	}
}

// TestRunDue_RestartTask_QueueNil pins that the "queue not available" guard
// fires BEFORE the desired-state/status writes - a restart that can't reach
// the node must not leave the server looking like it is starting.
func TestRunDue_RestartTask_QueueNil(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{1: {ID: 1, UUID: "srv-1", NodeID: 5}},
		nodes:   map[int]models.Node{5: {ID: 5, Token: "node-tok-5"}},
		due: []models.ScheduledTask{
			{ID: 302, ServerID: 1, TaskType: "restart", ScheduleCron: "@hourly"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()
	svc.queue = nil

	svc.runDue(context.Background())

	if len(fs.desiredCalls) != 0 || len(fs.statusCalls) != 0 {
		t.Errorf("expected no state-update calls when queue is nil, got desired=%+v status=%+v", fs.desiredCalls, fs.statusCalls)
	}
	if len(fs.runRecords) != 1 || fs.runRecords[0].status != "error" {
		t.Fatalf("runRecords = %+v, want single error entry", fs.runRecords)
	}
}

// TestRunDue_InvalidCron_DisablesTaskAndOverridesOKStatus pins that a bad cron
// string wins over an otherwise-successful execute: the task is disabled and
// the recorded status/error reflect the parse failure, not "ok".
func TestRunDue_InvalidCron_DisablesTaskAndOverridesOKStatus(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{2: {ID: 2, UUID: "srv-2"}},
		due: []models.ScheduledTask{
			{ID: 400, ServerID: 2, TaskType: "say", ScheduleCron: "garbage cron", Payload: "hi"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.enabledCalls) != 1 {
		t.Fatalf("enabledCalls = %+v, want exactly one disable call", fs.enabledCalls)
	}
	ec := fs.enabledCalls[0]
	if ec.id != 400 || ec.enabled != false || ec.nextRun != nil {
		t.Errorf("disable call = %+v, want {400 false <nil>}", ec)
	}

	if len(fs.runRecords) != 1 {
		t.Fatalf("runRecords = %+v, want 1 entry", fs.runRecords)
	}
	rr := fs.runRecords[0]
	if rr.status != "error" || rr.errMsg == "" || rr.nextRun != nil {
		t.Errorf("run record = %+v, want status=error, non-empty errMsg, nil nextRun", rr)
	}
}

func TestRunDue_PublishesServersChangedOnlyOnRestart(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{1: {ID: 1, UUID: "srv-1", NodeID: 5}},
		nodes:   map[int]models.Node{5: {ID: 5, Token: "node-tok-5"}},
		due: []models.ScheduledTask{
			{ID: 500, ServerID: 1, TaskType: "restart", ScheduleCron: "@hourly"},
		},
	}
	rdb := newQueueTestRedis(t)
	svc := &ScheduledTaskService{store: fs, redis: rdb, queue: NewQueueService(rdb), events: NewSystemEventsPublisher(rdb)}

	ctx := context.Background()
	sub := rdb.Subscribe(ctx, SystemEventsChannel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ch := sub.Channel()

	svc.runDue(ctx)

	got := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case msg := <-ch:
			var ev SystemEvent
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				t.Fatalf("unmarshal event: %v", err)
			}
			got[ev.Type] = true
		case <-deadline:
			t.Fatalf("timed out waiting for events, got so far: %v", got)
		}
	}
	if !got["servers.changed"] || !got["scheduled_tasks.changed"] {
		t.Errorf("events = %v, want both servers.changed and scheduled_tasks.changed", got)
	}
}

func TestRunDue_SayOnly_DoesNotPublishServersChanged(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{2: {ID: 2, UUID: "srv-2"}},
		due: []models.ScheduledTask{
			{ID: 501, ServerID: 2, TaskType: "say", ScheduleCron: "@hourly", Payload: "hi"},
		},
	}
	rdb := newQueueTestRedis(t)
	svc := &ScheduledTaskService{store: fs, redis: rdb, queue: NewQueueService(rdb), events: NewSystemEventsPublisher(rdb)}

	ctx := context.Background()
	sub := rdb.Subscribe(ctx, SystemEventsChannel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ch := sub.Channel()

	svc.runDue(ctx)

	// Expect exactly one event: scheduled_tasks.changed.
	select {
	case msg := <-ch:
		var ev SystemEvent
		if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		if ev.Type != "scheduled_tasks.changed" {
			t.Fatalf("first event = %q, want scheduled_tasks.changed", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scheduled_tasks.changed event")
	}

	select {
	case msg := <-ch:
		var ev SystemEvent
		_ = json.Unmarshal([]byte(msg.Payload), &ev)
		t.Fatalf("unexpected extra event published: %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// expected: no second event
	}
}

// --- suspension parity with the HTTP power gate ---
//
// handlers/servers_lifecycle.go refuses start/restart while the owner's billing
// is suspended. This executor took no such check, so a tenant's own nightly
// "restart" task handed their server back after the billing lifecycle had
// stopped it, until the next hourly enforcement pass stopped it again.

func TestRunDue_RestartTask_SuspendedOwner_IsSkipped(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{1: {ID: 1, UUID: "srv-1", NodeID: 5, OwnerID: "owner-1"}},
		nodes:   map[int]models.Node{5: {ID: 5, Token: "node-tok-5"}},
		billing: map[string]*store.UserBilling{"owner-1": {UserID: "owner-1", Status: "suspended"}},
		due: []models.ScheduledTask{
			{ID: 100, ServerID: 1, TaskType: "restart", ScheduleCron: "* * * * *"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	// The DB write is the part that actually defeats the suspension: the node
	// reconciler reads desired_state, so flipping it back to online restarts the
	// server even if the queued command were lost.
	if len(fs.desiredCalls) != 0 {
		t.Errorf("desiredCalls = %+v, want none for a suspended owner", fs.desiredCalls)
	}
	if len(fs.statusCalls) != 0 {
		t.Errorf("statusCalls = %+v, want none for a suspended owner", fs.statusCalls)
	}
	msgs, err := svc.redis.XRange(context.Background(), "dylaris:node:node-tok-5:cmds", "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("queued %d command(s) for a suspended owner, want 0", len(msgs))
	}

	// Recorded as an error, not silently dropped: the tenant sees why on the
	// task's last run, and next_run still advances so it resumes on its own once
	// payment is settled.
	if len(fs.runRecords) != 1 {
		t.Fatalf("runRecords = %+v, want 1 entry", fs.runRecords)
	}
	if fs.runRecords[0].status != "error" {
		t.Errorf("status = %q, want error", fs.runRecords[0].status)
	}
	if !strings.Contains(fs.runRecords[0].errMsg, "suspended") {
		t.Errorf("errMsg = %q, want it to mention the suspension", fs.runRecords[0].errMsg)
	}
	if fs.runRecords[0].nextRun == nil {
		t.Error("nextRun = nil, want the schedule to keep advancing through a suspension")
	}
	if len(fs.enabledCalls) != 0 {
		t.Errorf("enabledCalls = %+v, want the task left enabled", fs.enabledCalls)
	}
}

// Fails CLOSED, unlike the HTTP gate: the real store answers "active" for a user
// with no billing row, so an error is a genuine database failure and skipping
// one firing costs a deferred restart the next tick retries.
func TestRunDue_RestartTask_BillingUnreadable_IsSkipped(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers:    map[int]models.Server{1: {ID: 1, UUID: "srv-1", NodeID: 5, OwnerID: "owner-1"}},
		nodes:      map[int]models.Node{5: {ID: 5, Token: "node-tok-5"}},
		billingErr: errors.New("db down"),
		due: []models.ScheduledTask{
			{ID: 100, ServerID: 1, TaskType: "restart", ScheduleCron: "* * * * *"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	if len(fs.desiredCalls) != 0 {
		t.Errorf("desiredCalls = %+v, want none when the billing status cannot be read", fs.desiredCalls)
	}
	if len(fs.runRecords) != 1 || fs.runRecords[0].status != "error" {
		t.Fatalf("runRecords = %+v, want a single error record", fs.runRecords)
	}
}

// Scope pin: the console "send command" handler has no suspension gate, so the
// say task must not grow one either. The executor mirrors the HTTP contract
// exactly - no stricter, no looser.
func TestRunDue_SayTask_SuspendedOwner_StillRuns(t *testing.T) {
	fs := &schedTaskFakeStore{
		servers: map[int]models.Server{2: {ID: 2, UUID: "srv-2", OwnerID: "owner-1"}},
		billing: map[string]*store.UserBilling{"owner-1": {UserID: "owner-1", Status: "suspended"}},
		due: []models.ScheduledTask{
			{ID: 501, ServerID: 2, TaskType: "say", ScheduleCron: "@hourly", Payload: "hi"},
		},
	}
	svc, done := newSchedTestService(t, fs)
	defer done()

	svc.runDue(context.Background())

	got, err := svc.redis.LRange(context.Background(), "dylaris:server:srv-2:input", 0, -1).Result()
	if err != nil {
		t.Fatalf("LRange: %v", err)
	}
	if len(got) != 1 || got[0] != "say hi" {
		t.Errorf("stdin queue = %v, want [\"say hi\"]", got)
	}
}
