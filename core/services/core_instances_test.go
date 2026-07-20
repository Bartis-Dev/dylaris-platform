package services

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newCoreInstancesRedis returns a client backed by an in-process Redis, plus
// the server so a test can seed keys directly.
func newCoreInstancesRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()}), mr
}

// seedHeartbeat writes a heartbeat exactly the way CoreHeartbeatService does,
// so the test cannot drift from the writer's key or payload shape.
func seedHeartbeat(t *testing.T, mr *miniredis.Miniredis, id string) {
	t.Helper()
	data, err := json.Marshal(CoreHeartbeat{ID: id, Region: "default", GRPCAddr: "10.0.0.1:25501"})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	mr.Set("dylaris:core:"+id, string(data))
}

func TestOnlineCoreIDs(t *testing.T) {
	tests := []struct {
		name string
		seed func(*testing.T, *miniredis.Miniredis)
		want []string
	}{
		{
			name: "an empty keyspace reports nobody",
			seed: func(*testing.T, *miniredis.Miniredis) {},
			want: []string{},
		},
		{
			name: "a single Core",
			seed: func(t *testing.T, mr *miniredis.Miniredis) { seedHeartbeat(t, mr, "core-a") },
			want: []string{"core-a"},
		},
		{
			name: "several Cores come back sorted",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				seedHeartbeat(t, mr, "core-c")
				seedHeartbeat(t, mr, "core-a")
				seedHeartbeat(t, mr, "core-b")
			},
			want: []string{"core-a", "core-b", "core-c"},
		},
		{
			// The leader lease lives at dylaris:core:leader and so matches the
			// same glob as the heartbeats. Counting it would report two Cores
			// on every single-Core install that has ever elected a leader,
			// which is all of them. With today's leader code the value is a
			// bare id, so the json check below would also reject it - this row
			// pins the outcome, not which of the two checks produced it.
			name: "the leader lease is not an instance",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				seedHeartbeat(t, mr, "core-a")
				mr.Set("dylaris:core:leader", "core-a")
			},
			want: []string{"core-a"},
		},
		{
			name: "the leader lease alone reports nobody",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				mr.Set("dylaris:core:leader", "core-a")
			},
			want: []string{},
		},
		{
			// THIS is the row the exclusion-by-name exists for, and the only
			// one that can tell the two checks apart. If the leader lease ever
			// starts holding a structured value - an owner plus a term, say -
			// its payload would parse as a heartbeat with a non-empty id and
			// the json check would wave it straight through. Excluding the key
			// by name is what keeps that future change from silently doubling
			// the count and disabling the host-path backend on single-Core
			// installs.
			name: "a leader lease holding heartbeat-shaped json is still not an instance",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				seedHeartbeat(t, mr, "core-a")
				mr.Set("dylaris:core:leader", `{"id":"core-a-leader","region":"default"}`)
			},
			want: []string{"core-a"},
		},
		{
			name: "a value that is not a heartbeat is skipped",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				seedHeartbeat(t, mr, "core-a")
				mr.Set("dylaris:core:junk", "not json at all")
			},
			want: []string{"core-a"},
		},
		{
			name: "a heartbeat with no id is skipped",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				seedHeartbeat(t, mr, "core-a")
				mr.Set("dylaris:core:blank", `{"id":"","region":"default"}`)
			},
			want: []string{"core-a"},
		},
		{
			// Identity comes from the payload, not the key name, so a stray
			// key holding a copy of a live heartbeat cannot inflate the count.
			name: "two keys carrying the same id count once",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				seedHeartbeat(t, mr, "core-a")
				data, _ := json.Marshal(CoreHeartbeat{ID: "core-a"})
				mr.Set("dylaris:core:core-a-copy", string(data))
			},
			want: []string{"core-a"},
		},
		{
			// Nothing outside the prefix may leak in.
			name: "unrelated keys are not counted",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				seedHeartbeat(t, mr, "core-a")
				mr.Set("dylaris:node:core-a", `{"id":"core-a"}`)
				mr.Set("dylaris:coreish", `{"id":"nope"}`)
			},
			want: []string{"core-a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rdb, mr := newCoreInstancesRedis(t)
			tc.seed(t, mr)

			got, err := OnlineCoreIDs(context.Background(), rdb)
			if err != nil {
				t.Fatalf("OnlineCoreIDs: unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestOnlineCoreIDs_CountsALargeKeyspace pins that a keyspace far larger than
// the 100-key SCAN batch is counted in full.
//
// It does NOT prove the cursor loop is followed, and must not be read as
// though it did: miniredis answers every SCAN in a single page regardless of
// COUNT ("we only ever return all at once, so no non-zero cursor can every be
// valid", cmd_generic.go), so an implementation that ignored the cursor
// entirely would pass this too. Verified by mutation: forcing the loop to
// break after the first batch leaves this test green. Proving the cursor loop
// needs a real Redis, which this suite does not have.
func TestOnlineCoreIDs_CountsALargeKeyspace(t *testing.T) {
	rdb, mr := newCoreInstancesRedis(t)
	const want = 250
	for i := 0; i < want; i++ {
		seedHeartbeat(t, mr, fmt.Sprintf("core-%03d", i))
	}

	got, err := OnlineCoreIDs(context.Background(), rdb)
	if err != nil {
		t.Fatalf("OnlineCoreIDs: unexpected error: %v", err)
	}
	if len(got) != want {
		t.Fatalf("found %d Cores, want %d", len(got), want)
	}
}

func TestOnlineCoreIDs_ReportsRedisFailure(t *testing.T) {
	rdb, mr := newCoreInstancesRedis(t)
	mr.Close() // the client now talks to nothing

	if _, err := OnlineCoreIDs(context.Background(), rdb); err == nil {
		t.Fatal("want an error when Redis is unreachable; a silent 0 would read as \"one Core online\" and wave through a host-path save")
	}
}

func TestOnlineCoreIDs_ReportsMissingClient(t *testing.T) {
	if _, err := OnlineCoreIDs(context.Background(), nil); err == nil {
		t.Fatal("want an error for a nil client, not a zero count")
	}
}
