package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
)

// The two tab-proxy caps an admin sets (proxied tabs per server, share links
// per user) were enforced in Create only. Everything a tabs.write holder needs
// to walk past them is a second call:
//
//   - direct tabs are uncapped, and Update happily flips one to "proxied"
//   - a proxied tab created with surface "tab" gets no share slug and so skips
//     the share-link count; PATCH it to "page" and RotateShareLink mints one
//
// These tests pin both, and pin that neither guard fires on an edit that adds
// nothing (an already-proxied tab, an already-issued slug) - a cap that froze
// existing rows at the limit would be its own bug.

type tabCapFakeStore struct {
	store.Store
	db       *sql.DB
	settings map[string]string
}

func (f *tabCapFakeStore) RawDB() *sql.DB { return f.db }

func (f *tabCapFakeStore) GetSetting(key string) (string, error) { return f.settings[key], nil }

func (f *tabCapFakeStore) GetServerByID(id int) (*models.Server, error) {
	if id == 1 {
		return &models.Server{ID: 1, UUID: "srv-uuid", OwnerID: "owner-id"}, nil
	}
	return nil, errors.New("not found")
}

func newTabCapHandler(t *testing.T, maxPerServer, maxShareLinks int) (*ServerTabsHandler, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// Unordered, so a missing cap query fails as "expectation not met" (the
	// count never ran) rather than as a downstream ordering mismatch that
	// reports itself as a 500 and hides which guard actually went away.
	mock.MatchExpectationsInOrder(false)

	fs := &tabCapFakeStore{db: db, settings: map[string]string{
		"feature_tab_proxy_enabled":          "true",
		"tab_proxy_allow_public_links":       "true",
		"tab_proxy_max_per_server":           strconv.Itoa(maxPerServer),
		"tab_proxy_max_share_links_per_user": strconv.Itoa(maxShareLinks),
	}}
	state := &AppState{Store: fs, FeatureFlags: services.NewFeatureFlags(fs)}
	return NewServerTabsHandler(state), mock
}

func tabPatchRequest(body string) *http.Request {
	r := httptest.NewRequest("PATCH", "/api/servers/1/tabs/4", bytes.NewReader([]byte(body)))
	r = mux.SetURLVars(r, map[string]string{"id": "1", "tabId": "4"})
	ctx := context.WithValue(r.Context(), "userID", "member-id")
	ctx = context.WithValue(ctx, "username", "member")
	ctx = context.WithValue(ctx, "isAdmin", false)
	return r.WithContext(ctx)
}

func TestUpdateTab_ProxiedCapAppliesToTheDirectToProxiedFlip(t *testing.T) {
	cases := []struct {
		name        string
		currentMode string
		proxiedNow  int
		maxPer      int
		wantStatus  int
		wantUpdate  bool
		// hostLabel is what the row already carries. Empty means the flip has
		// to mint one; a tab that was already proxied keeps the one it has,
		// because the label is in circulation the moment a link is copied.
		hostLabel string
	}{
		{"flip at the cap is refused", "direct", 2, 2, http.StatusConflict, false, ""},
		{"flip under the cap mints a host", "direct", 1, 2, http.StatusOK, true, ""},
		{"editing an already-proxied tab keeps its host", "proxied", 2, 2, http.StatusOK, true, "abcdefghij0123456789"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, mock := newTabCapHandler(t, c.maxPer, 20)

			mock.ExpectQuery(`SELECT mode FROM server_tabs`).
				WillReturnRows(sqlmock.NewRows([]string{"mode"}).AddRow(c.currentMode))
			if c.currentMode != "proxied" {
				mock.ExpectQuery(`SELECT COUNT\(\*\) FROM server_tabs`).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(c.proxiedNow))
			}
			if c.wantUpdate {
				// A tab that survives the cap gets a content host if it has
				// none: the host label is what the proxy routes on, so a
				// proxied tab without one would exist and be unreachable.
				mock.ExpectQuery(`SELECT proxy_host_label FROM server_tabs`).
					WillReturnRows(sqlmock.NewRows([]string{"proxy_host_label"}).AddRow(c.hostLabel))
				if c.hostLabel == "" {
					mock.ExpectExec(`UPDATE server_tabs SET proxy_host_label`).
						WillReturnResult(sqlmock.NewResult(0, 1))
				}
				mock.ExpectExec(`UPDATE server_tabs SET`).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}

			rec := httptest.NewRecorder()
			h.Update(rec, tabPatchRequest(`{"mode":"proxied","targetPort":8100,"targetPath":"/","surface":"tab","visibility":"private"}`))

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			// ExpectExec is only scripted on the allowed cases, so an
			// unexpected UPDATE fails here rather than passing silently.
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("db expectations: %v", err)
			}
		})
	}
}

func tabRotateRequest() *http.Request {
	r := httptest.NewRequest("POST", "/api/servers/1/tabs/4/share-link", nil)
	r = mux.SetURLVars(r, map[string]string{"id": "1", "tabId": "4"})
	ctx := context.WithValue(r.Context(), "userID", "someone-else")
	ctx = context.WithValue(ctx, "username", "member")
	ctx = context.WithValue(ctx, "isAdmin", false)
	return r.WithContext(ctx)
}

func TestRotateShareLink_CountsANewSlugAgainstTheOwnersAllowance(t *testing.T) {
	cases := []struct {
		name       string
		existing   sql.NullString
		createdBy  string
		used       int
		maxLinks   int
		wantStatus int
		wantUpdate bool
	}{
		{"first slug at the cap is refused", sql.NullString{}, "creator-id", 3, 3, http.StatusConflict, false},
		{"first slug under the cap is minted", sql.NullString{}, "creator-id", 2, 3, http.StatusOK, true},
		{"re-rolling an existing slug is not a new link",
			sql.NullString{String: "OLDTOKEN12345678", Valid: true}, "creator-id", 3, 3, http.StatusOK, true},
		{"a tab with no creator has no allowance to bill", sql.NullString{}, "", 99, 3, http.StatusOK, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, mock := newTabCapHandler(t, 10, c.maxLinks)

			mock.ExpectQuery(`SELECT mode, surface, share_token`).
				WillReturnRows(sqlmock.NewRows([]string{"mode", "surface", "share_token", "created_by"}).
					AddRow("proxied", "page", c.existing, c.createdBy))
			mintsNew := !c.existing.Valid || c.existing.String == ""
			if mintsNew && c.createdBy != "" {
				mock.ExpectQuery(`SELECT COUNT\(\*\) FROM server_tabs`).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(c.used))
			}
			if c.wantUpdate {
				mock.ExpectExec(`UPDATE server_tabs SET share_token`).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}

			rec := httptest.NewRecorder()
			h.RotateShareLink(rec, tabRotateRequest())

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("db expectations: %v", err)
			}
		})
	}
}
