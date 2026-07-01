package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"dylaris-core/models"

	"github.com/gorilla/mux"
)

type PacksHandler struct {
	state *AppState
}

func NewPacksHandler(state *AppState) *PacksHandler {
	return &PacksHandler{state: state}
}

var packSlugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]{1,62}[a-z0-9])?$`)

// ownsPack loads the pack and checks the caller owns it (admins may read).
func (h *PacksHandler) ownsPack(r *http.Request, packID int) (*models.Pack, bool) {
	p, err := h.state.Store.GetPack(packID)
	if err != nil || p == nil {
		return nil, false
	}
	userID, _ := r.Context().Value("userID").(string)
	if p.OwnerID == userID {
		return p, true
	}
	if isAdmin, _ := r.Context().Value("isAdmin").(bool); isAdmin {
		return p, true // read-only elsewhere; write handlers re-check ownership
	}
	return nil, false
}

type packRequest struct {
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	Summary           string `json:"summary"`
	SolderDisplayName string `json:"solderDisplayName"`
	SolderSlug        string `json:"solderSlug"`
}

func (h *PacksHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	packs, err := h.state.Store.ListPacksByOwner(userID)
	if err != nil {
		sendJSONError(w, "Failed to list packs", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "packs": packs})
}

func (h *PacksHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req packRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		sendJSONError(w, "Name is required", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugify(req.Name)
	}
	if !packSlugRe.MatchString(slug) {
		sendJSONError(w, "Invalid slug", http.StatusBadRequest)
		return
	}
	solderSlug := strings.TrimSpace(req.SolderSlug)
	if solderSlug != "" && !packSlugRe.MatchString(solderSlug) {
		sendJSONError(w, "Invalid solder slug", http.StatusBadRequest)
		return
	}
	p := &models.Pack{
		OwnerID:            userID,
		InternalName:       req.Name,
		InternalSlug:       slug,
		Summary:            strings.TrimSpace(req.Summary),
		SolderDisplayName:  strings.TrimSpace(req.SolderDisplayName),
		SolderSlug:         solderSlug,
		ModrinthVisibility: "unlisted",
	}
	id, err := h.state.Store.CreatePack(p)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique") {
			sendJSONError(w, "A pack with that slug already exists", http.StatusConflict)
		} else {
			sendJSONError(w, "Failed to create pack", http.StatusInternalServerError)
		}
		return
	}
	p.ID = id
	h.state.Events.Publish(r.Context(), "packs.changed", map[string]interface{}{"ownerId": userID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "pack": p})
}

func (h *PacksHandler) Get(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	p, ok := h.ownsPack(r, packID)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "pack": p})
}

func (h *PacksHandler) Update(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	userID, _ := r.Context().Value("userID").(string)
	p, err := h.state.Store.GetPack(packID)
	if err != nil || p == nil || p.OwnerID != userID {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req packRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if n := strings.TrimSpace(req.Name); n != "" {
		p.InternalName = n
	}
	p.Summary = strings.TrimSpace(req.Summary)
	p.SolderDisplayName = strings.TrimSpace(req.SolderDisplayName)
	if s := strings.TrimSpace(req.SolderSlug); s == "" || packSlugRe.MatchString(s) {
		p.SolderSlug = s
	}
	if err := h.state.Store.UpdatePack(p); err != nil {
		sendJSONError(w, "Failed to update", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "packs.changed", map[string]interface{}{"ownerId": userID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "pack": p})
}

func (h *PacksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	userID, _ := r.Context().Value("userID").(string)
	p, err := h.state.Store.GetPack(packID)
	if err != nil || p == nil || p.OwnerID != userID {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := h.state.Store.DeletePack(packID, userID); err != nil {
		sendJSONError(w, "Failed to delete", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "packs.changed", map[string]interface{}{"ownerId": userID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type buildRequest struct {
	VersionString string `json:"versionString"`
	Minecraft     string `json:"minecraft"`
	Loader        string `json:"loader"`
	LoaderVersion string `json:"loaderVersion"`
	Changelog     string `json:"changelog"`
}

func (h *PacksHandler) ListBuilds(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if _, ok := h.ownsPack(r, packID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	builds, err := h.state.Store.ListPackBuilds(packID)
	if err != nil {
		sendJSONError(w, "Failed to list builds", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "builds": builds})
}

func (h *PacksHandler) CreateBuild(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	userID, _ := r.Context().Value("userID").(string)
	p, err := h.state.Store.GetPack(packID)
	if err != nil || p == nil || p.OwnerID != userID {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req buildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.VersionString) == "" {
		sendJSONError(w, "versionString is required", http.StatusBadRequest)
		return
	}
	b := &models.PackBuild{
		PackID:        packID,
		VersionString: strings.TrimSpace(req.VersionString),
		Minecraft:     strings.TrimSpace(req.Minecraft),
		Loader:        strings.TrimSpace(req.Loader),
		LoaderVersion: strings.TrimSpace(req.LoaderVersion),
		Changelog:     req.Changelog,
		Channel:       models.ChannelDraft,
	}
	id, err := h.state.Store.CreatePackBuild(b)
	if err != nil {
		sendJSONError(w, "Failed to create build (version may exist)", http.StatusConflict)
		return
	}
	b.ID = id
	h.state.Events.Publish(r.Context(), "pack_builds.changed", map[string]interface{}{"packId": packID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "build": b})
}

func (h *PacksHandler) UpdateBuild(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	buildID, _ := strconv.Atoi(mux.Vars(r)["buildId"])
	userID, _ := r.Context().Value("userID").(string)
	p, err := h.state.Store.GetPack(packID)
	if err != nil || p == nil || p.OwnerID != userID {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	b, err := h.state.Store.GetPackBuild(buildID)
	if err != nil || b == nil || b.PackID != packID {
		sendJSONError(w, "Build not found", http.StatusNotFound)
		return
	}
	if b.Frozen {
		sendJSONError(w, "Build is published and frozen", http.StatusConflict)
		return
	}
	var req buildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if v := strings.TrimSpace(req.VersionString); v != "" {
		b.VersionString = v
	}
	b.Minecraft = strings.TrimSpace(req.Minecraft)
	b.Loader = strings.TrimSpace(req.Loader)
	b.LoaderVersion = strings.TrimSpace(req.LoaderVersion)
	b.Changelog = req.Changelog
	if err := h.state.Store.UpdatePackBuild(b); err != nil {
		sendJSONError(w, "Failed to update build", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "pack_builds.changed", map[string]interface{}{"packId": packID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "build": b})
}

func (h *PacksHandler) DeleteBuild(w http.ResponseWriter, r *http.Request) {
	packID, _ := strconv.Atoi(mux.Vars(r)["id"])
	buildID, _ := strconv.Atoi(mux.Vars(r)["buildId"])
	userID, _ := r.Context().Value("userID").(string)
	p, err := h.state.Store.GetPack(packID)
	if err != nil || p == nil || p.OwnerID != userID {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	b, err := h.state.Store.GetPackBuild(buildID)
	if err != nil || b == nil || b.PackID != packID {
		sendJSONError(w, "Build not found", http.StatusNotFound)
		return
	}
	if b.Frozen {
		sendJSONError(w, "Build is published and frozen", http.StatusConflict)
		return
	}
	if err := h.state.Store.DeletePackBuild(buildID, packID); err != nil {
		sendJSONError(w, "Failed to delete build", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "pack_builds.changed", map[string]interface{}{"packId": packID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
