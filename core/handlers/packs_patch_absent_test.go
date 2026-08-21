package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// Both routes here are PATCHes whose bodies used plain (non-pointer) fields, so
// a body that simply left a field out decoded to the zero value and wrote it
// over the stored one. Two of those fields are not cosmetic:
//
//   - solder-config's `private` false PUBLISHES a private pack, exposing its
//     Solder listing and its whole mod list. Its own panel client declares the
//     field optional (`private?: boolean`).
//   - packs' `solderSlug` "" takes the pack off the public Solder API and
//     orphans every published build, because solderManifestKey is derived from
//     the slug.

type packPatchFakeStore struct {
	store.Store
	pack    *models.Pack
	updated *models.Pack
}

func (f *packPatchFakeStore) GetPack(id int) (*models.Pack, error) {
	if f.pack != nil && f.pack.ID == id {
		cp := *f.pack
		return &cp, nil
	}
	return nil, errors.New("not found")
}

func (f *packPatchFakeStore) UpdatePack(p *models.Pack) error {
	cp := *p
	f.updated = &cp
	return nil
}

func newPackPatchHandler() (*PacksHandler, *packPatchFakeStore) {
	fs := &packPatchFakeStore{pack: &models.Pack{
		ID:                1,
		OwnerID:           "owner-id",
		InternalName:      "My Pack",
		InternalSlug:      "my-pack",
		Summary:           "a summary",
		SolderSlug:        "my-pack",
		SolderDisplayName: "My Pack",
		Private:           true,
	}}
	return NewPacksHandler(&AppState{Store: fs}), fs
}

func packPatchRequest(path, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte(body)))
	r = mux.SetURLVars(r, map[string]string{"id": "1"})
	ctx := context.WithValue(r.Context(), "userID", "owner-id")
	ctx = context.WithValue(ctx, "isAdmin", false)
	ctx = context.WithValue(ctx, "username", "owner")
	return r.WithContext(ctx)
}

func TestSetSolderConfig_AbsentPrivateKeepsTheCurrentVisibility(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantPrivate bool
	}{
		{"omitted leaves a private pack private",
			`{"solderSlug":"my-pack","solderDisplayName":"My Pack"}`, true},
		{"explicit false still publishes",
			`{"solderSlug":"my-pack","solderDisplayName":"My Pack","private":false}`, false},
		{"explicit true still hides",
			`{"solderSlug":"my-pack","solderDisplayName":"My Pack","private":true}`, true},
		{"JSON null is treated as absent",
			`{"solderSlug":"my-pack","solderDisplayName":"My Pack","private":null}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, fs := newPackPatchHandler()
			rec := httptest.NewRecorder()

			h.SetSolderConfig(rec, packPatchRequest("/api/packs/1/solder-config", c.body))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			if fs.updated == nil {
				t.Fatal("nothing was persisted")
			}
			if fs.updated.Private != c.wantPrivate {
				t.Fatalf("private = %v, want %v", fs.updated.Private, c.wantPrivate)
			}
		})
	}
}

func TestUpdatePack_AbsentFieldsAreLeftAlone(t *testing.T) {
	h, fs := newPackPatchHandler()
	rec := httptest.NewRecorder()

	h.Update(rec, packPatchRequest("/api/packs/1", `{"name":"Renamed"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if fs.updated == nil {
		t.Fatal("nothing was persisted")
	}
	if fs.updated.InternalName != "Renamed" {
		t.Errorf("name = %q, want the submitted one", fs.updated.InternalName)
	}
	if fs.updated.SolderSlug != "my-pack" {
		t.Errorf("solder slug = %q; a rename must not take the pack off the Solder API", fs.updated.SolderSlug)
	}
	if fs.updated.Summary != "a summary" {
		t.Errorf("summary = %q, want the stored value", fs.updated.Summary)
	}
	if fs.updated.SolderDisplayName != "My Pack" {
		t.Errorf("solder display name = %q, want the stored value", fs.updated.SolderDisplayName)
	}
}

func TestUpdatePack_SubmittedFieldsStillApply(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantSlug   string
	}{
		{"a new slug is written", `{"solderSlug":"other-pack"}`, http.StatusOK, "other-pack"},
		{"an explicit empty slug still clears it", `{"solderSlug":""}`, http.StatusOK, ""},
		{"an invalid slug is now an error, not a silent no-op", `{"solderSlug":"Not A Slug"}`, http.StatusBadRequest, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, fs := newPackPatchHandler()
			rec := httptest.NewRecorder()

			h.Update(rec, packPatchRequest("/api/packs/1", c.body))

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			if c.wantStatus != http.StatusOK {
				if fs.updated != nil {
					t.Fatal("a rejected slug was persisted anyway")
				}
				return
			}
			if fs.updated == nil || fs.updated.SolderSlug != c.wantSlug {
				t.Fatalf("solder slug = %v, want %q", fs.updated, c.wantSlug)
			}
		})
	}
}

// Summary is the field an empty-string PATCH legitimately clears, so pin that
// "" and absent are now genuinely different.
func TestUpdatePack_ExplicitEmptySummaryStillClears(t *testing.T) {
	h, fs := newPackPatchHandler()
	rec := httptest.NewRecorder()

	h.Update(rec, packPatchRequest("/api/packs/1", `{"summary":""}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if fs.updated == nil || fs.updated.Summary != "" {
		t.Fatalf("summary = %v, want cleared", fs.updated)
	}
}
