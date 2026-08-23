package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/models"
)

// TestProvision_RoutePool pins where the purchased ADDRESS pool lands. It is not
// a user_billing column: routes were already capped by gateway_route_limits, and
// keeping a second cap for the same thing is how the two drift apart. The scope
// string matters - effectiveRouteLimit resolves "user:<id>" then "user_default"
// then "global", so a wrong scope does not fail, it silently falls through to the
// platform default and hands the tenant the wrong number of addresses.
func TestProvision_RoutePool(t *testing.T) {
	const uid = "u1"

	t.Run("a purchased pool is written to the per-user scope", func(t *testing.T) {
		fs := &storeLinkFakeStore{users: map[string]*models.User{uid: {ID: uid}}}
		h := newStoreLinkHandler(fs, newStoreLinkRedis(t), true)
		rec := httptest.NewRecorder()
		h.Provision(rec, storeLinkPost("/api/store/provision", map[string]interface{}{
			"uuid": uid, "action": "activate", "maxNodes": 2, "maxRoutes": 12,
		}, storeLinkTestKey))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if len(fs.routeLimitSets) != 1 {
			t.Fatalf("routeLimitSets = %+v, want exactly 1", fs.routeLimitSets)
		}
		if got := fs.routeLimitSets[0]; got.scope != "user:"+uid || got.max != 12 {
			t.Fatalf("wrote %+v, want {user:%s 12}", got, uid)
		}
	})

	t.Run("an omitted pool touches nothing", func(t *testing.T) {
		fs := &storeLinkFakeStore{users: map[string]*models.User{uid: {ID: uid}}}
		h := newStoreLinkHandler(fs, newStoreLinkRedis(t), true)
		rec := httptest.NewRecorder()
		h.Provision(rec, storeLinkPost("/api/store/provision", map[string]interface{}{
			"uuid": uid, "action": "activate", "maxNodes": 2,
		}, storeLinkTestKey))
		if len(fs.routeLimitSets) != 0 || len(fs.routeLimitDeletes) != 0 {
			t.Fatalf("sets=%+v deletes=%+v, want neither", fs.routeLimitSets, fs.routeLimitDeletes)
		}
	})

	// Clearing must DELETE the row, not write 0. The scope chain treats a missing
	// per-user row as "use the platform default"; a stored 0 would read as a hard
	// zero and leave the tenant unable to point a single address anywhere.
	t.Run("a cleared pool deletes the row rather than writing zero", func(t *testing.T) {
		for _, body := range []map[string]interface{}{
			{"uuid": uid, "action": "activate", "maxRoutes": 0},
			{"uuid": uid, "action": "activate", "maxRoutes": nil},
		} {
			fs := &storeLinkFakeStore{users: map[string]*models.User{uid: {ID: uid}}}
			h := newStoreLinkHandler(fs, newStoreLinkRedis(t), true)
			rec := httptest.NewRecorder()
			h.Provision(rec, storeLinkPost("/api/store/provision", body, storeLinkTestKey))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if len(fs.routeLimitSets) != 0 {
				t.Fatalf("wrote %+v, want a delete instead", fs.routeLimitSets)
			}
			if len(fs.routeLimitDeletes) != 1 || fs.routeLimitDeletes[0] != "user:"+uid {
				t.Fatalf("deletes = %+v, want [user:%s]", fs.routeLimitDeletes, uid)
			}
		}
	})

	t.Run("suspend never rewrites the pool", func(t *testing.T) {
		fs := &storeLinkFakeStore{users: map[string]*models.User{uid: {ID: uid}}}
		h := newStoreLinkHandler(fs, newStoreLinkRedis(t), true)
		rec := httptest.NewRecorder()
		h.Provision(rec, storeLinkPost("/api/store/provision", map[string]interface{}{
			"uuid": uid, "action": "suspend", "maxRoutes": 99,
		}, storeLinkTestKey))
		if len(fs.routeLimitSets) != 0 || len(fs.routeLimitDeletes) != 0 {
			t.Fatalf("sets=%+v deletes=%+v, want neither", fs.routeLimitSets, fs.routeLimitDeletes)
		}
	})

	t.Run("a failed pool write is reported, not swallowed", func(t *testing.T) {
		fs := &storeLinkFakeStore{
			users:         map[string]*models.User{uid: {ID: uid}},
			routeLimitErr: errStoreLinkEntitlement,
		}
		h := newStoreLinkHandler(fs, newStoreLinkRedis(t), true)
		rec := httptest.NewRecorder()
		h.Provision(rec, storeLinkPost("/api/store/provision", map[string]interface{}{
			"uuid": uid, "action": "activate", "maxRoutes": 12,
		}, storeLinkTestKey))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
		}
	})
}
