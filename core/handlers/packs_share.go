package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"dylaris-core/models"
	"dylaris-core/storage/modpack"

	"github.com/gorilla/mux"
)

type createShareLinkRequest struct {
	Kind          string `json:"kind"`
	ExpiresInDays int    `json:"expiresInDays,omitempty"`
}

// CreateShareLink POST /api/packs/{id}/builds/{buildId}/share-link
func (h *PacksHandler) CreateShareLink(w http.ResponseWriter, r *http.Request) {
	b, ok := h.loadOwnedBuild(r)
	if !ok {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	userID, _ := r.Context().Value("userID").(string)
	var req createShareLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Kind != models.ShareLinkClientMrpack && req.Kind != models.ShareLinkServerPack {
		sendJSONError(w, "kind must be client-mrpack or server-pack", http.StatusBadRequest)
		return
	}
	token, err := generatePlaintextKey()
	if err != nil {
		sendJSONError(w, "Failed to mint token", http.StatusInternalServerError)
		return
	}
	link := &models.ShareLink{
		BuildID:   b.ID,
		Kind:      req.Kind,
		Token:     token,
		CreatedBy: userID,
	}
	if req.ExpiresInDays > 0 {
		exp := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		link.ExpiresAt = &exp
	}
	id, err := h.state.Store.CreateShareLink(link)
	if err != nil {
		sendJSONError(w, "Failed to create share link", http.StatusInternalServerError)
		return
	}
	link.ID = id
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "link": link})
}

// ListShareLinks GET /api/packs/{id}/builds/{buildId}/share-links
func (h *PacksHandler) ListShareLinks(w http.ResponseWriter, r *http.Request) {
	b, ok := h.loadOwnedBuild(r)
	if !ok {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	links, err := h.state.Store.ListShareLinksByBuild(b.ID)
	if err != nil {
		sendJSONError(w, "Failed to list share links", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "links": links})
}

// RevokeShareLink DELETE /api/packs/{id}/builds/{buildId}/share-links/{linkId}
func (h *PacksHandler) RevokeShareLink(w http.ResponseWriter, r *http.Request) {
	// loadOwnedBuild proves the caller owns the pack+build in the path; the store
	// revoke is additionally created_by-scoped, so cross-user revoke is blocked.
	if _, ok := h.loadOwnedBuild(r); !ok {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	userID, _ := r.Context().Value("userID").(string)
	linkID, _ := strconv.Atoi(mux.Vars(r)["linkId"])
	if err := h.state.Store.RevokeShareLink(linkID, userID); err != nil {
		sendJSONError(w, "Share link not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ServeShare GET /api/share/{token} is PUBLIC, unauthenticated. Registered on the
// root router as a sibling of /solder so it bypasses setup-lock, maintenance, and
// auth (the token is the credential). The modpacks feature is gated in-handler.
func (h *PacksHandler) ServeShare(w http.ResponseWriter, r *http.Request) {
	// Uniform 404 for every negative case (feature off, unknown/revoked/expired
	// token, missing build/pack) so the endpoint is not a validity oracle.
	notFound := func() { sendJSONError(w, "Not found", http.StatusNotFound) }
	if !h.state.FeatureFlags.IsModpacksEnabled(r.Context()) {
		notFound()
		return
	}
	token := mux.Vars(r)["token"]
	link, err := h.state.Store.GetShareLinkByToken(token)
	if err != nil || link == nil || link.Revoked {
		notFound()
		return
	}
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		notFound()
		return
	}
	build, err := h.state.Store.GetPackBuild(link.BuildID)
	if err != nil || build == nil {
		notFound()
		return
	}
	pack, err := h.state.Store.GetPack(build.PackID)
	if err != nil || pack == nil {
		notFound()
		return
	}

	switch link.Kind {
	case models.ShareLinkClientMrpack:
		key, err := h.ensureInstallMrpack(pack, build)
		if err != nil {
			sendJSONError(w, "Failed to render pack", http.StatusInternalServerError)
			return
		}
		prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting, h.state.buildCoreStorageProvider)
		if err != nil || prov == nil {
			sendJSONError(w, "Storage unavailable", http.StatusInternalServerError)
			return
		}
		data, err := prov.Get(key)
		if err != nil {
			sendJSONError(w, "Failed to read pack", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-modrinth-modpack+zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", pack.InternalSlug+"-"+build.VersionString+".mrpack"))
		_, _ = w.Write(data)

	case models.ShareLinkServerPack:
		content, err := h.state.Store.ListBuildContent(build.ID)
		if err != nil {
			sendJSONError(w, "Failed to load content", http.StatusInternalServerError)
			return
		}
		data, err := h.renderServerPack(r.Context(), content)
		if err != nil {
			sendJSONError(w, "Failed to render server pack", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", pack.InternalSlug+"-"+build.VersionString+"-server.zip"))
		_, _ = w.Write(data)

	default:
		notFound()
	}
}
