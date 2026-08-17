package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/store"
	"dylaris-core/updates"
)

// fakeUpdatesStore embeds the (nil) store.Store interface and overrides only the
// methods the update-feed handler touches. Any other call would panic, which is
// the point: the test proves exactly which store surface the handler uses.
type fakeUpdatesStore struct {
	store.Store
	seenPlatform int
	seenGateway  int
	setPlatform  int
	setGateway   int
	setCalled    bool
	routingMode  string
}

// newUpdatesHandlerNoBaseline is NewUpdatesHandler with the build-baked
// baseline forced to empty.
//
// NewUpdatesHandler derives platformInstalled from the embedded feed.jsonl, so a
// test that asserts "all N remote lines are new" only holds while that file is
// empty - and it stops being empty the first time a real changelog line is
// appended. Tests that care about the delta arithmetic pin their own baseline
// here; TestGetUpdatesInstalledBaselineIsTheBakedFeed covers the wiring to the
// real file.
func newUpdatesHandlerNoBaseline(state *AppState, platformURL, gatewayURL string) *UpdatesHandler {
	h := NewUpdatesHandler(state, platformURL, gatewayURL)
	h.platformInstalled = 0
	h.platformBaked = nil
	return h
}

func (f *fakeUpdatesStore) GetUserUpdatesSeen(userID string) (int, int, error) {
	return f.seenPlatform, f.seenGateway, nil
}

func (f *fakeUpdatesStore) SetUserUpdatesSeen(userID string, platform, gateway int) error {
	f.setCalled = true
	f.setPlatform, f.setGateway = platform, gateway
	return nil
}

func (f *fakeUpdatesStore) GetSetting(key string) (string, error) {
	if key == "routing_mode" {
		return f.routingMode, nil
	}
	return "", nil
}

// adminCtx builds a request context matching AuthMiddleware's userID/isAdmin keys.
func adminCtx(userID string, isAdmin bool) context.Context {
	ctx := context.WithValue(context.Background(), "userID", userID)
	return context.WithValue(ctx, "isAdmin", isAdmin)
}

func TestBuildServiceBlock(t *testing.T) {
	line := func(s string) string {
		return `{"date":"2026-07-18","service":"platform","type":"feature","summary":"` + s + `"}`
	}
	remote := []string{line("A"), line("B"), line("C")}

	cases := []struct {
		name            string
		remote          []string
		installed       int
		seen            int
		wantLatest      int
		wantUnseen      int
		wantAvailable   bool
		wantEntries     int
		wantFirstSummry string // newest-first
	}{
		{"fresh install none seen", remote, 0, 0, 3, 3, true, 3, "C"},
		{"installed one seen none", remote, 1, 0, 3, 2, true, 2, "C"},
		{"all installed", remote, 3, 0, 3, 0, false, 0, ""},
		{"seen ahead of install", remote, 1, 3, 3, 0, true, 2, "C"},
		{"empty feed", nil, 0, 0, 0, 0, false, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildServiceBlock(c.remote, c.installed, c.seen)
			if got.LatestCount != c.wantLatest {
				t.Errorf("LatestCount = %d, want %d", got.LatestCount, c.wantLatest)
			}
			if got.Unseen != c.wantUnseen {
				t.Errorf("Unseen = %d, want %d", got.Unseen, c.wantUnseen)
			}
			if got.UpdateAvailable != c.wantAvailable {
				t.Errorf("UpdateAvailable = %v, want %v", got.UpdateAvailable, c.wantAvailable)
			}
			if len(got.NewEntries) != c.wantEntries {
				t.Fatalf("len(NewEntries) = %d, want %d", len(got.NewEntries), c.wantEntries)
			}
			if c.wantFirstSummry != "" && got.NewEntries[0].Summary != c.wantFirstSummry {
				t.Errorf("NewEntries[0].Summary = %q, want %q (newest first)", got.NewEntries[0].Summary, c.wantFirstSummry)
			}
		})
	}
}

func TestBuildServiceBlockCapsEntries(t *testing.T) {
	remote := make([]string, updatesEntryCap+10)
	for i := range remote {
		remote[i] = `{"summary":"x"}`
	}
	got := buildServiceBlock(remote, 0, 0)
	if len(got.NewEntries) != updatesEntryCap {
		t.Fatalf("len(NewEntries) = %d, want cap %d", len(got.NewEntries), updatesEntryCap)
	}
	if got.Unseen != len(remote) {
		t.Errorf("Unseen = %d, want %d (unseen counts beyond the entry cap)", got.Unseen, len(remote))
	}
}

// TestGetUpdatesInstalledBaselineIsTheBakedFeed covers what
// newUpdatesHandlerNoBaseline deliberately bypasses: the handler's "installed"
// mark really is the line count of the embedded feed. Asserted against
// updates.PlatformFeed() rather than a literal, so appending a changelog line
// never breaks it.
func TestGetUpdatesInstalledBaselineIsTheBakedFeed(t *testing.T) {
	h := NewUpdatesHandler(&AppState{Store: &fakeUpdatesStore{}}, "", "")
	want := updates.LineCount(updates.PlatformFeed())
	if h.platformInstalled != want {
		t.Errorf("platformInstalled = %d, want %d (the baked feed's line count)", h.platformInstalled, want)
	}
	if len(h.platformBaked) != want {
		t.Errorf("len(platformBaked) = %d, want %d", len(h.platformBaked), want)
	}
}

func TestGetUpdatesRequiresAdmin(t *testing.T) {
	h := newUpdatesHandlerNoBaseline(&AppState{Store: &fakeUpdatesStore{}}, "", "")
	req := httptest.NewRequest(http.MethodGet, "/api/updates", nil).WithContext(adminCtx("u1", false))
	rec := httptest.NewRecorder()
	h.GetUpdates(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin GetUpdates status = %d, want 403", rec.Code)
	}
}

func TestGetUpdatesAdminPlatformOnly(t *testing.T) {
	feed := `{"date":"2026-07-18","service":"platform","type":"feature","summary":"A"}
{"date":"2026-07-18","service":"platform","type":"fix","summary":"B"}
{"date":"2026-07-18","service":"platform","type":"change","summary":"C"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer srv.Close()

	// routingMode empty -> gateway off, so the gateway block is absent even with
	// a gateway URL set.
	fake := &fakeUpdatesStore{}
	h := newUpdatesHandlerNoBaseline(&AppState{Store: fake}, srv.URL, "")
	req := httptest.NewRequest(http.MethodGet, "/api/updates", nil).WithContext(adminCtx("u1", true))
	rec := httptest.NewRecorder()
	h.GetUpdates(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Success  bool                `json:"success"`
		Unseen   int                 `json:"unseen"`
		Platform updateServiceBlock  `json:"platform"`
		Gateway  *updateServiceBlock `json:"gateway"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if resp.Platform.LatestCount != 3 {
		t.Errorf("platform LatestCount = %d, want 3", resp.Platform.LatestCount)
	}
	if resp.Unseen != 3 {
		t.Errorf("unseen = %d, want 3", resp.Unseen)
	}
	if resp.Gateway != nil {
		t.Errorf("gateway block should be absent when routing is not gateway, got %+v", resp.Gateway)
	}
	if len(resp.Platform.NewEntries) != 3 || resp.Platform.NewEntries[0].Summary != "C" {
		t.Errorf("newEntries = %+v, want 3 newest-first (C,B,A)", resp.Platform.NewEntries)
	}
}

func TestGetUpdatesAdminWithGatewayEnabled(t *testing.T) {
	platformFeed := "{\"summary\":\"P1\"}\n{\"summary\":\"P2\"}\n"
	gatewayFeed := "{\"summary\":\"G1\"}\n{\"summary\":\"G2\"}\n{\"summary\":\"G3\"}\n"
	pSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(platformFeed)) }))
	defer pSrv.Close()
	gSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(gatewayFeed)) }))
	defer gSrv.Close()

	fake := &fakeUpdatesStore{routingMode: "gateway"}
	h := newUpdatesHandlerNoBaseline(&AppState{Store: fake}, pSrv.URL, gSrv.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/updates", nil).WithContext(adminCtx("u1", true))
	rec := httptest.NewRecorder()
	h.GetUpdates(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Unseen   int                 `json:"unseen"`
		Platform updateServiceBlock  `json:"platform"`
		Gateway  *updateServiceBlock `json:"gateway"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Gateway == nil {
		t.Fatal("gateway block should be present when routing is gateway and a gateway URL is set")
	}
	if resp.Gateway.LatestCount != 3 {
		t.Errorf("gateway LatestCount = %d, want 3", resp.Gateway.LatestCount)
	}
	if resp.Unseen != 5 { // platform 2 + gateway 3
		t.Errorf("combined unseen = %d, want 5", resp.Unseen)
	}
}

func TestGetUpdatesFailOpenOnUnreachableFeed(t *testing.T) {
	// Empty platform URL -> no fetch -> falls back to the (empty) baked baseline.
	h := newUpdatesHandlerNoBaseline(&AppState{Store: &fakeUpdatesStore{}}, "", "")
	req := httptest.NewRequest(http.MethodGet, "/api/updates", nil).WithContext(adminCtx("u1", true))
	rec := httptest.NewRecorder()
	h.GetUpdates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail-open)", rec.Code)
	}
	var resp struct {
		Unseen   int                `json:"unseen"`
		Platform updateServiceBlock `json:"platform"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Unseen != 0 || resp.Platform.LatestCount != 0 {
		t.Errorf("fail-open should yield no updates, got unseen=%d latest=%d", resp.Unseen, resp.Platform.LatestCount)
	}
}

func TestMarkUpdatesSeenServerComputed(t *testing.T) {
	feed := "{\"summary\":\"A\"}\n{\"summary\":\"B\"}\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer srv.Close()

	fake := &fakeUpdatesStore{seenGateway: 7} // prior gateway marker must be preserved
	h := newUpdatesHandlerNoBaseline(&AppState{Store: fake}, srv.URL, "")
	req := httptest.NewRequest(http.MethodPut, "/api/me/updates-seen", nil).WithContext(adminCtx("u1", true))
	rec := httptest.NewRecorder()
	h.MarkUpdatesSeen(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !fake.setCalled {
		t.Fatal("SetUserUpdatesSeen was not called")
	}
	if fake.setPlatform != 2 {
		t.Errorf("stored platform seen = %d, want 2 (feed line count)", fake.setPlatform)
	}
	if fake.setGateway != 7 {
		t.Errorf("stored gateway seen = %d, want 7 (prior marker preserved while gateway off)", fake.setGateway)
	}
}

func TestMarkUpdatesSeenRequiresAuth(t *testing.T) {
	h := newUpdatesHandlerNoBaseline(&AppState{Store: &fakeUpdatesStore{}}, "", "")
	req := httptest.NewRequest(http.MethodPut, "/api/me/updates-seen", nil).WithContext(adminCtx("", false))
	rec := httptest.NewRecorder()
	h.MarkUpdatesSeen(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
