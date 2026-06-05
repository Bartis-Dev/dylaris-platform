package services

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dylaris-core/store"
)

// fakeWarpStore implements the narrow warpStore surface Enroll uses.
type fakeWarpStore struct {
	peers  map[string]store.WarpPeer // pubkey → peer
	byKey  map[int][]store.WarpPeer
	nextID int
}

func newFakeWarpStore() *fakeWarpStore {
	return &fakeWarpStore{peers: map[string]store.WarpPeer{}, byKey: map[int][]store.WarpPeer{}}
}
func (f *fakeWarpStore) InsertWarpPeer(p store.WarpPeer) (int, error) {
	f.nextID++
	p.ID = f.nextID
	f.peers[p.Pubkey] = p
	f.byKey[p.APIKeyID] = append(f.byKey[p.APIKeyID], p)
	return p.ID, nil
}
func (f *fakeWarpStore) GetWarpPeerByPubkey(pk string) (*store.WarpPeer, error) {
	if p, ok := f.peers[pk]; ok {
		return &p, nil
	}
	return nil, errNotFound
}
func (f *fakeWarpStore) ListWarpPeersByKey(id int) ([]store.WarpPeer, error) { return f.byKey[id], nil }
func (f *fakeWarpStore) ListAllWarpPeers() ([]store.WarpPeer, error) {
	var out []store.WarpPeer
	for _, p := range f.peers {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeWarpStore) DeleteWarpPeerByPubkey(pk string) error {
	if p, ok := f.peers[pk]; ok {
		delete(f.peers, pk)
		rest := f.byKey[p.APIKeyID][:0]
		for _, q := range f.byKey[p.APIKeyID] {
			if q.Pubkey != pk {
				rest = append(rest, q)
			}
		}
		f.byKey[p.APIKeyID] = rest
	}
	return nil
}

var errNotFound = errString("not found")

type errString string

func (e errString) Error() string { return string(e) }

func enrollTestService(t *testing.T) (*WarpService, *fakeWarpStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	fs := newFakeWarpStore()
	svc := &WarpService{warp: fs, redis: rdb, clientSubnet: "10.0.99.0/24", leaderID: "leader-01"}
	return svc, fs, mr
}

func storePeer(keyID int, pub, ip string) store.WarpPeer {
	return store.WarpPeer{APIKeyID: keyID, Pubkey: pub, WGIP: ip, LeaderID: "leader-01"}
}

func TestEnroll_GeneralKey_AllocatesAndPushesAddPeer(t *testing.T) {
	svc, _, mr := enrollTestService(t)
	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	resp, err := svc.Enroll(context.Background(), key, "pubkeyA", nil)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if resp.WGIP == "" || resp.WGSubnet != "10.0.99.0/24" {
		t.Fatalf("bad resp %+v", resp)
	}
	vals, _ := mr.List("dylaris:warp:leader-01:queue")
	if len(vals) != 1 {
		t.Fatalf("expected 1 queued command, got %d", len(vals))
	}
}

func TestEnroll_GeneralKey_BlocksAtLimit(t *testing.T) {
	svc, _, _ := enrollTestService(t)
	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 1, OnNewConn: "block"}
	if _, err := svc.Enroll(context.Background(), key, "pubA", nil); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	if _, err := svc.Enroll(context.Background(), key, "pubB", nil); err == nil {
		t.Fatal("expected block at max_conns=1")
	}
}

func TestEnroll_Idempotent_SamePubkeySameIP(t *testing.T) {
	svc, _, _ := enrollTestService(t)
	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	r1, _ := svc.Enroll(context.Background(), key, "pubA", nil)
	r2, err := svc.Enroll(context.Background(), key, "pubA", nil)
	if err != nil {
		t.Fatalf("re-enroll: %v", err)
	}
	if r1.WGIP != r2.WGIP {
		t.Fatalf("re-enroll changed IP: %s → %s", r1.WGIP, r2.WGIP)
	}
}

func TestEnroll_FixedKey_KillOldEvictsPrevious(t *testing.T) {
	svc, fs, mr := enrollTestService(t)
	key := store.WarpAPIKey{ID: 1, Policy: "fixed", MaxConns: 1, OnNewConn: "kill_old"}
	if _, err := svc.Enroll(context.Background(), key, "pubOld", nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.Enroll(context.Background(), key, "pubNew", nil); err != nil {
		t.Fatalf("second (kill_old): %v", err)
	}
	if _, err := fs.GetWarpPeerByPubkey("pubOld"); err == nil {
		t.Fatal("old peer should have been evicted")
	}
	vals, _ := mr.List("dylaris:warp:leader-01:queue")
	if len(vals) != 3 {
		t.Fatalf("expected 3 commands (add, remove, add), got %d", len(vals))
	}
}
