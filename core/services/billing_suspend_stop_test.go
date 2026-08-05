package services

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"dylaris-core/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// stopOrderFakeStore records the order of the three writes the cutoff performs,
// because the ORDER is the fix: desired_state must be "stopped" before the node
// can act on the stop, or the node's reconciler sees a not-running container
// whose desired_state is still "online", calls it a crash and restarts it.
type stopOrderFakeStore struct {
	billingFakeStore

	desiredCalls []string // "<serverID>=<state>"
	statusWrites []string // "<serverID>=<status>"
	desiredErr   error
	node         *models.Node
	nodeErr      error
}

func (f *stopOrderFakeStore) UpdateServerDesiredState(id int, state string) error {
	if f.desiredErr != nil {
		return f.desiredErr
	}
	f.desiredCalls = append(f.desiredCalls, strconv.Itoa(id)+"="+state)
	return nil
}

func (f *stopOrderFakeStore) UpdateServerStatus(id int, status string) error {
	f.statusWrites = append(f.statusWrites, strconv.Itoa(id)+"="+status)
	return nil
}

func (f *stopOrderFakeStore) GetNodeByID(int) (*models.Node, error) {
	if f.nodeErr != nil {
		return nil, f.nodeErr
	}
	return f.node, nil
}

func newStopTestService(t *testing.T, fs *stopOrderFakeStore) *BillingLifecycleService {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &BillingLifecycleService{store: fs, queue: NewQueueService(rdb)}
}

// TestStopTenantServers_FlipsDesiredStateBeforeQueueing is the regression guard
// for a suspension that did not suspend: the cutoff queued a stop and nothing
// else, so the container went down and the node reconciler brought it back
// about five seconds later. Both suspension paths use this function, so the
// automatic non-payment cutoff was inert too.
func TestStopTenantServers_FlipsDesiredStateBeforeQueueing(t *testing.T) {
	fs := &stopOrderFakeStore{
		node: &models.Node{ID: 7, Token: "node-token"},
	}
	fs.servers = map[string][]models.Server{
		"u1": {{ID: 42, UUID: "srv-uuid", Status: "online", NodeID: 7}},
	}
	svc := newStopTestService(t, fs)

	svc.stopTenantServers(context.Background(), "u1")

	if len(fs.desiredCalls) != 1 || fs.desiredCalls[0] != "42=stopped" {
		t.Fatalf("desired_state writes = %v, want [42=stopped]; without this the node reconciler restarts the container", fs.desiredCalls)
	}
	if len(fs.statusWrites) != 1 || fs.statusWrites[0] != "42=stopping" {
		t.Errorf("status writes = %v, want [42=stopping] (mirrors PowerAction)", fs.statusWrites)
	}
}

// TestStopTenantServers_SkipsOfflineServers: only a running server needs
// stopping, and writing desired_state for the rest would be a needless write on
// every hourly enforcement pass over the same suspended tenant.
func TestStopTenantServers_SkipsOfflineServers(t *testing.T) {
	fs := &stopOrderFakeStore{node: &models.Node{ID: 7, Token: "node-token"}}
	fs.servers = map[string][]models.Server{
		"u1": {
			{ID: 1, UUID: "a", Status: "stopped", NodeID: 7},
			{ID: 2, UUID: "b", Status: "pending_setup", NodeID: 7},
		},
	}
	svc := newStopTestService(t, fs)

	svc.stopTenantServers(context.Background(), "u1")

	if len(fs.desiredCalls) != 0 {
		t.Errorf("desired_state writes = %v, want none for servers that are not online", fs.desiredCalls)
	}
}

// TestStopTenantServers_NoStopWhenDesiredStateWriteFails: if desired_state
// cannot be written, sending the stop anyway is worse than not stopping. The
// container goes down, the reconciler still reads "online", and it comes back
// up - a pointless restart of a live server for a tenant who then is not
// suspended either way. The next enforcement pass retries the whole thing.
func TestStopTenantServers_NoStopWhenDesiredStateWriteFails(t *testing.T) {
	fs := &stopOrderFakeStore{
		node:       &models.Node{ID: 7, Token: "node-token"},
		desiredErr: errors.New("db down"),
	}
	fs.servers = map[string][]models.Server{
		"u1": {{ID: 42, UUID: "srv-uuid", Status: "online", NodeID: 7}},
	}
	svc := newStopTestService(t, fs)

	svc.stopTenantServers(context.Background(), "u1")

	if len(fs.statusWrites) != 0 {
		t.Errorf("status writes = %v, want none: the cutoff must not proceed once desired_state failed", fs.statusWrites)
	}
}
