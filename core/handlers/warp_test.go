package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dylaris-core/services"
	"dylaris-core/store"
)

// warpFakeStore embeds store.Store (nil) so it satisfies the full interface at
// compile time; only the methods the warp handler/service touch are overridden.
// Any other method call would panic — the test never makes one.
type warpFakeStore struct {
	store.Store
	settings map[string]string
	peers    map[string]store.WarpPeer
	nextID   int
}

func (f *warpFakeStore) GetSetting(k string) (string, error) { return f.settings[k], nil }
func (f *warpFakeStore) InsertWarpPeer(p store.WarpPeer) (int, error) {
	f.nextID++
	p.ID = f.nextID
	f.peers[p.Pubkey] = p
	return p.ID, nil
}
func (f *warpFakeStore) GetWarpPeerByPubkey(pk string) (*store.WarpPeer, error) {
	if p, ok := f.peers[pk]; ok {
		return &p, nil
	}
	return nil, warpErr("not found")
}
func (f *warpFakeStore) DeleteWarpPeerByPubkey(pk string) error { delete(f.peers, pk); return nil }
func (f *warpFakeStore) SetWarpPeerAssignedLeader(pubkey, leaderID string) error {
	p, ok := f.peers[pubkey]
	if !ok {
		return warpErr("not found")
	}
	p.AssignedLeader = leaderID
	f.peers[pubkey] = p
	return nil
}

// Multi-hub: a single seeded region "leader-01" (10.0.99.0/24) with one leader.
func (f *warpFakeStore) ListWarpRegions() ([]store.WarpRegion, error) {
	return []store.WarpRegion{{Region: "leader-01", Subnet: "10.0.99.0/24", Enabled: true}}, nil
}
func (f *warpFakeStore) GetWarpRegion(region string) (*store.WarpRegion, error) {
	if region == "leader-01" {
		return &store.WarpRegion{Region: "leader-01", Subnet: "10.0.99.0/24", Enabled: true}, nil
	}
	return nil, warpErr("no such region")
}
func (f *warpFakeStore) ListWarpLeaders() ([]store.WarpLeader, error) {
	return []store.WarpLeader{{LeaderID: "leader-01", Region: "leader-01", Endpoint: "vpn.example.com:51820", Enabled: true}}, nil
}
func (f *warpFakeStore) CountWarpPeersByRegion() (map[string]int, error) {
	out := map[string]int{}
	for _, p := range f.peers {
		out[p.Region]++
	}
	return out, nil
}
func (f *warpFakeStore) EnrollPeerTx(keyID, limit int, onNewConn, pubkey, fixedIP, region string, allocIP func(taken map[string]bool) (string, error)) (string, string, error) {
	wgIP := fixedIP
	if wgIP == "" {
		taken := map[string]bool{}
		for _, p := range f.peers {
			taken[p.WGIP] = true
		}
		ip, err := allocIP(taken)
		if err != nil {
			return "", "", err
		}
		wgIP = ip
	}
	_, _ = f.InsertWarpPeer(store.WarpPeer{APIKeyID: keyID, Pubkey: pubkey, WGIP: wgIP, Region: region})
	return wgIP, "", nil
}

func (f *warpFakeStore) CreateWarpAPIKey(k store.WarpAPIKey) (int, error) {
	f.nextID++
	return f.nextID, nil
}

type warpErr string

func (e warpErr) Error() string { return string(e) }

func adminMintReq(body map[string]interface{}) *http.Request {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/admin/warp/keys", bytes.NewReader(b))
	return r.WithContext(context.WithValue(r.Context(), "isAdmin", true))
}

func TestMintAPIKey_FixedWGIP_RequiresRegion(t *testing.T) {
	h := newWarpTestHandler(t)
	rec := httptest.NewRecorder()
	h.MintAPIKey(rec, adminMintReq(map[string]interface{}{"fixed_wg_ip": "10.0.99.50"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestMintAPIKey_FixedWGIP_Invalid(t *testing.T) {
	h := newWarpTestHandler(t)
	rec := httptest.NewRecorder()
	// .1 is the leader-reserved first host of 10.0.99.0/24.
	h.MintAPIKey(rec, adminMintReq(map[string]interface{}{"fixed_wg_ip": "10.0.99.1", "region": "leader-01"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestMintAPIKey_FixedWGIP_Valid(t *testing.T) {
	h := newWarpTestHandler(t)
	rec := httptest.NewRecorder()
	h.MintAPIKey(rec, adminMintReq(map[string]interface{}{"fixed_wg_ip": "10.0.99.50", "region": "leader-01"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func newWarpTestHandler(t *testing.T) *WarpHandler {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	fs := &warpFakeStore{
		// Enrollment requires gateway routing; set it so the happy-path test
		// exercises a fully enabled platform.
		settings: map[string]string{"routing_mode": "gateway"},
		peers:    map[string]store.WarpPeer{},
	}
	svc := services.NewWarpService(fs, rdb, "test-secret")
	state := &AppState{Store: fs, Redis: rdb}
	return NewWarpHandler(state, svc)
}

func withTestWarpKey(r *http.Request) *http.Request {
	key := store.WarpAPIKey{ID: 1, Policy: "general", MaxConns: 5, OnNewConn: "block"}
	return r.WithContext(context.WithValue(r.Context(), warpKeyCtx, key))
}

func TestEnroll_HappyPath_ReturnsConfigWithLeaderInfo(t *testing.T) {
	h := newWarpTestHandler(t)
	body, _ := json.Marshal(map[string]interface{}{
		"public_key":     "clientPubKey",
		"tunnel_subnets": []string{"10.0.0.0/24"},
	})
	req := httptest.NewRequest("POST", "/api/warp/enroll", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.Enroll(rec, withTestWarpKey(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["wg_ip"] == "" || resp["leader_public_key"] == "" || resp["leader_endpoint"] != "vpn.example.com:51820" {
		t.Fatalf("missing leader info in response: %+v", resp)
	}
}
