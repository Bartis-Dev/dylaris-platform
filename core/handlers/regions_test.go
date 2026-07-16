package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"dylaris-core/models"
	"dylaris-core/store"
)

// regionsFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only the methods CreateRegion and DeleteRegion
// touch are overridden. Any other call would panic - these tests never make
// one. InsertAuditIdentity is a no-op recorder since LogIdentityAudit is
// called unconditionally on every successful mutation.
type regionsFakeStore struct {
	store.Store

	createCalls []models.Region
	createErr   error

	getRegionResult *models.Region
	getRegionErr    error

	deleteCalls []string
	deleteErr   error

	countServers    map[string]int
	countNodes      map[string]int
	insertAuditCall int
}

func (f *regionsFakeStore) CreateRegion(r *models.Region) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.createCalls = append(f.createCalls, *r)
	return nil
}

func (f *regionsFakeStore) GetRegion(id string) (*models.Region, error) {
	return f.getRegionResult, f.getRegionErr
}

func (f *regionsFakeStore) DeleteRegion(id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, id)
	return nil
}

func (f *regionsFakeStore) CountServersInRegion(regionID string) (int, error) {
	return f.countServers[regionID], nil
}

func (f *regionsFakeStore) CountNodesInRegion(regionID string) (int, error) {
	return f.countNodes[regionID], nil
}

func (f *regionsFakeStore) InsertAuditIdentity(*models.AuditEventIdentity) error {
	f.insertAuditCall++
	return nil
}

func regionsReq(method, path string, isAdmin bool, urlVars map[string]string, body map[string]interface{}) *http.Request {
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if urlVars != nil {
		r = mux.SetURLVars(r, urlVars)
	}
	return r.WithContext(context.WithValue(r.Context(), "isAdmin", isAdmin))
}

// --- CreateRegion ---

// Phase 4 Task 14: regions.write is now enforced at the route chokepoint
// (RequireCap wrapping in routes.go), not in-handler, so the old pure
// non-admin-forbidden case moved to routes_authz_test.go
// (TestCap_RegionsPanel), which runs through the real resolver.

// TestCreateRegion_IDValidation pins regionIDRegex
// (^[a-z][a-z0-9\-]{0,30}[a-z0-9]$, regions.go:17). IMPORTANT real-behavior
// note: the handler lowercases the id via strings.ToLower BEFORE running the
// regex, so an uppercase-only id is normalized and ACCEPTED, not rejected -
// only characters that remain invalid after lowering (leading digit,
// trailing hyphen, out-of-range length) actually 400.
func TestCreateRegion_IDValidation(t *testing.T) {
	cases := []struct {
		name       string
		id         string
		wantStatus int
		wantCalled bool
	}{
		{"leading digit rejected", "1abc", http.StatusBadRequest, false},
		{"trailing hyphen rejected", "abc-", http.StatusBadRequest, false},
		{"too long (33 chars) rejected", strings.Repeat("a", 33), http.StatusBadRequest, false},
		{"single character rejected (regex needs a distinct first+last char)", "a", http.StatusBadRequest, false},
		{"empty id rejected", "", http.StatusBadRequest, false},
		{"valid lowercase id accepted", "us-east-2", http.StatusOK, true},
		{"exactly 32 chars (the max) accepted", strings.Repeat("a", 32), http.StatusOK, true},
		// Real behavior, not what the brief assumed: uppercase is lowercased
		// BEFORE validation, so it normalizes through rather than 400s.
		{"uppercase id is lowercased first and accepted (NOT rejected)", "MyRegion", http.StatusOK, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &regionsFakeStore{}
			h := NewRegionsHandler(&AppState{Store: fs})
			rec := httptest.NewRecorder()

			h.CreateRegion(rec, regionsReq("POST", "/api/admin/regions", true, nil, map[string]interface{}{
				"id": c.id, "displayName": "Some Region",
			}))

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.wantStatus, rec.Body.String())
			}
			if got := len(fs.createCalls) == 1; got != c.wantCalled {
				t.Fatalf("createCalls = %+v, want called=%v", fs.createCalls, c.wantCalled)
			}
			if c.wantCalled {
				if fs.createCalls[0].ID != strings.ToLower(c.id) {
					t.Fatalf("persisted ID = %q, want lowercased %q", fs.createCalls[0].ID, strings.ToLower(c.id))
				}
			}
		})
	}
}

func TestCreateRegion_DisplayNameRequired(t *testing.T) {
	fs := &regionsFakeStore{}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.CreateRegion(rec, regionsReq("POST", "/api/admin/regions", true, nil, map[string]interface{}{
		"id": "eu", "displayName": "   ",
	}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Display name is required") {
		t.Fatalf("body = %s, want 'Display name is required'", rec.Body.String())
	}
	if len(fs.createCalls) != 0 {
		t.Fatalf("expected no CreateRegion call, got %+v", fs.createCalls)
	}
}

func TestCreateRegion_EnabledDefaultsTrueUnlessExplicit(t *testing.T) {
	t.Run("omitted enabled defaults to true", func(t *testing.T) {
		fs := &regionsFakeStore{}
		h := NewRegionsHandler(&AppState{Store: fs})
		rec := httptest.NewRecorder()

		h.CreateRegion(rec, regionsReq("POST", "/api/admin/regions", true, nil, map[string]interface{}{
			"id": "eu", "displayName": "Europe",
		}))

		if rec.Code != http.StatusOK || len(fs.createCalls) != 1 || !fs.createCalls[0].Enabled {
			t.Fatalf("status=%d createCalls=%+v, want 200 and Enabled=true", rec.Code, fs.createCalls)
		}
	})

	t.Run("explicit false is honored", func(t *testing.T) {
		fs := &regionsFakeStore{}
		h := NewRegionsHandler(&AppState{Store: fs})
		rec := httptest.NewRecorder()

		h.CreateRegion(rec, regionsReq("POST", "/api/admin/regions", true, nil, map[string]interface{}{
			"id": "eu", "displayName": "Europe", "enabled": false,
		}))

		if rec.Code != http.StatusOK || len(fs.createCalls) != 1 || fs.createCalls[0].Enabled {
			t.Fatalf("status=%d createCalls=%+v, want 200 and Enabled=false", rec.Code, fs.createCalls)
		}
	})
}

// TestCreateRegion_ResponseIsTheRereadRow pins that the response echoes back
// h.state.Store.GetRegion(id) (picking up e.g. created_at), falling back to
// the just-built region only if that re-read comes back nil.
func TestCreateRegion_ResponseIsTheRereadRow(t *testing.T) {
	fs := &regionsFakeStore{getRegionResult: &models.Region{ID: "eu", DisplayName: "Europe (from DB)", Enabled: true}}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.CreateRegion(rec, regionsReq("POST", "/api/admin/regions", true, nil, map[string]interface{}{
		"id": "eu", "displayName": "Europe",
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool          `json:"success"`
		Region  models.Region `json:"region"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Region.DisplayName != "Europe (from DB)" {
		t.Fatalf("response region = %+v, want the re-read DisplayName from GetRegion", resp.Region)
	}
}

func TestCreateRegion_InvalidJSON(t *testing.T) {
	fs := &regionsFakeStore{}
	h := NewRegionsHandler(&AppState{Store: fs})
	r := httptest.NewRequest("POST", "/api/admin/regions", bytes.NewReader([]byte("not json")))
	r = r.WithContext(context.WithValue(r.Context(), "isAdmin", true))
	rec := httptest.NewRecorder()

	h.CreateRegion(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRegion_StoreErrorIsConflict(t *testing.T) {
	fs := &regionsFakeStore{createErr: errors.New("duplicate key")}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.CreateRegion(rec, regionsReq("POST", "/api/admin/regions", true, nil, map[string]interface{}{
		"id": "eu", "displayName": "Europe",
	}))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// --- DeleteRegion ---

// Phase 4 Task 14: regions.delete is now enforced at the route chokepoint
// (RequireCap wrapping in routes.go), not in-handler, so the old pure
// non-admin-forbidden case moved to routes_authz_test.go
// (TestCap_RegionsPanel), which runs through the real resolver.

func TestDeleteRegion_DefaultRegionRefused(t *testing.T) {
	fs := &regionsFakeStore{countServers: map[string]int{"default": 0}, countNodes: map[string]int{"default": 0}}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.DeleteRegion(rec, regionsReq("DELETE", "/api/admin/regions/default", true, map[string]string{"id": "default"}, nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Cannot delete the default region") {
		t.Fatalf("body = %s, want 'Cannot delete the default region'", rec.Body.String())
	}
	if len(fs.deleteCalls) != 0 {
		t.Fatalf("expected no DeleteRegion call, got %+v", fs.deleteCalls)
	}
}

func TestDeleteRegion_RefusedWhenServersReference(t *testing.T) {
	fs := &regionsFakeStore{countServers: map[string]int{"eu": 3}}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.DeleteRegion(rec, regionsReq("DELETE", "/api/admin/regions/eu", true, map[string]string{"id": "eu"}, nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "still has servers") {
		t.Fatalf("body = %s, want 'still has servers'", rec.Body.String())
	}
	if len(fs.deleteCalls) != 0 {
		t.Fatalf("expected no DeleteRegion call, got %+v", fs.deleteCalls)
	}
}

func TestDeleteRegion_RefusedWhenNodesReference(t *testing.T) {
	fs := &regionsFakeStore{countServers: map[string]int{"eu": 0}, countNodes: map[string]int{"eu": 2}}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.DeleteRegion(rec, regionsReq("DELETE", "/api/admin/regions/eu", true, map[string]string{"id": "eu"}, nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "still has nodes") {
		t.Fatalf("body = %s, want 'still has nodes'", rec.Body.String())
	}
	if len(fs.deleteCalls) != 0 {
		t.Fatalf("expected no DeleteRegion call, got %+v", fs.deleteCalls)
	}
}

func TestDeleteRegion_EmptyRegionDeleted(t *testing.T) {
	fs := &regionsFakeStore{countServers: map[string]int{"eu": 0}, countNodes: map[string]int{"eu": 0}}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.DeleteRegion(rec, regionsReq("DELETE", "/api/admin/regions/eu", true, map[string]string{"id": "eu"}, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(fs.deleteCalls) != 1 || fs.deleteCalls[0] != "eu" {
		t.Fatalf("deleteCalls = %+v, want [eu]", fs.deleteCalls)
	}
}

func TestDeleteRegion_StoreErrorIsInternalServerError(t *testing.T) {
	fs := &regionsFakeStore{deleteErr: errors.New("db down")}
	h := NewRegionsHandler(&AppState{Store: fs})
	rec := httptest.NewRecorder()

	h.DeleteRegion(rec, regionsReq("DELETE", "/api/admin/regions/eu", true, map[string]string{"id": "eu"}, nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}
