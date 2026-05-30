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

// Phase 14 — modpack authoring CRUD. Builder UI consumes these to manage
// packs + versions + mod selection. Publish + .mrpack export land in
// follow-up commits (P14.2/P14.3).

type ModpacksHandler struct {
	state *AppState
}

func NewModpacksHandler(state *AppState) *ModpacksHandler {
	return &ModpacksHandler{state: state}
}

var (
	// slugRegex pins URL-safe characters only — Modrinth expects matching
	// rules for project slugs so we validate now instead of failing
	// late on the Modrinth create-project call.
	slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]{1,62}[a-z0-9])?$`)

	validChannels = map[string]bool{
		models.ModpackChannelDraft:   true,
		models.ModpackChannelBeta:    true,
		models.ModpackChannelRelease: true,
	}
)

type modpackRequest struct {
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	Summary            string `json:"summary"`
	McVersion          string `json:"mcVersion"`
	Loader             string `json:"loader"`
	ModrinthVisibility string `json:"modrinthVisibility"`
}

func (h *ModpacksHandler) ownsModpack(r *http.Request, modpackID int) (*models.Modpack, bool) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		return nil, false
	}
	m, err := h.state.Store.GetModpack(modpackID)
	if err != nil {
		return nil, false
	}
	if m.OwnerID != userID {
		// Admins can read for support purposes but mutations stay user-scoped.
		isAdmin := r.Context().Value("isAdmin").(bool)
		if !isAdmin {
			return nil, false
		}
	}
	return m, true
}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// List GET /api/me/modpacks — packs owned by current user.
func (h *ModpacksHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	list, err := h.state.Store.ListModpacksByOwner(userID)
	if err != nil {
		sendJSONError(w, "Failed to list", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "modpacks": list})
}

func (h *ModpacksHandler) Get(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	m, ok := h.ownsModpack(r, modpackID)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "modpack": m})
}

func (h *ModpacksHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req modpackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		sendJSONError(w, "Name required", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugify(req.Name)
	}
	if !slugRegex.MatchString(slug) {
		sendJSONError(w, "Slug must be lowercase, 2-64 chars, alphanumeric / - / _", http.StatusBadRequest)
		return
	}
	visibility := req.ModrinthVisibility
	if visibility != "listed" && visibility != "unlisted" {
		visibility = "unlisted"
	}
	m := &models.Modpack{
		OwnerID:            userID,
		Name:               req.Name,
		Slug:               slug,
		Summary:            strings.TrimSpace(req.Summary),
		McVersion:          strings.TrimSpace(req.McVersion),
		Loader:             strings.TrimSpace(req.Loader),
		ModrinthVisibility: visibility,
	}
	id, err := h.state.Store.CreateModpack(m)
	if err != nil {
		sendJSONError(w, "Failed to create (slug may be taken)", http.StatusConflict)
		return
	}
	m.ID = id
	h.state.Events.Publish(r.Context(), "modpacks.changed", map[string]interface{}{"ownerId": userID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "modpack": m})
}

func (h *ModpacksHandler) Update(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	existing, ok := h.ownsModpack(r, modpackID)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req modpackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if v := strings.TrimSpace(req.Name); v != "" {
		existing.Name = v
	}
	if v := strings.TrimSpace(req.Summary); v != "" {
		existing.Summary = v
	}
	if v := strings.TrimSpace(req.McVersion); v != "" {
		existing.McVersion = v
	}
	if v := strings.TrimSpace(req.Loader); v != "" {
		existing.Loader = v
	}
	if req.ModrinthVisibility == "listed" || req.ModrinthVisibility == "unlisted" {
		existing.ModrinthVisibility = req.ModrinthVisibility
	}
	if err := h.state.Store.UpdateModpack(existing); err != nil {
		sendJSONError(w, "Failed to update", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "modpacks.changed", map[string]interface{}{"ownerId": existing.OwnerID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "modpack": existing})
}

func (h *ModpacksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	existing, ok := h.ownsModpack(r, modpackID)
	if !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := h.state.Store.DeleteModpack(modpackID, existing.OwnerID); err != nil {
		sendJSONError(w, "Failed to delete", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "modpacks.changed", map[string]interface{}{"ownerId": existing.OwnerID})
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- Versions ---

type versionRequest struct {
	VersionString string `json:"versionString"`
	Channel       string `json:"channel"`
	Changelog     string `json:"changelog"`
}

func (h *ModpacksHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if _, ok := h.ownsModpack(r, modpackID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	list, err := h.state.Store.ListModpackVersions(modpackID)
	if err != nil {
		sendJSONError(w, "Failed to list versions", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "versions": list})
}

func (h *ModpacksHandler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if _, ok := h.ownsModpack(r, modpackID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req versionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.VersionString = strings.TrimSpace(req.VersionString)
	if req.VersionString == "" {
		sendJSONError(w, "versionString required", http.StatusBadRequest)
		return
	}
	channel := req.Channel
	if channel == "" {
		channel = models.ModpackChannelDraft
	}
	if !validChannels[channel] {
		sendJSONError(w, "channel must be draft|beta|release", http.StatusBadRequest)
		return
	}
	v := &models.ModpackVersion{
		ModpackID:     modpackID,
		VersionString: req.VersionString,
		Channel:       channel,
		Changelog:     strings.TrimSpace(req.Changelog),
	}
	id, err := h.state.Store.CreateModpackVersion(v)
	if err != nil {
		sendJSONError(w, "Failed to create version (string may already exist)", http.StatusConflict)
		return
	}
	v.ID = id
	h.state.Events.Publish(r.Context(), "modpack_versions.changed", map[string]interface{}{"modpackId": modpackID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "version": v})
}

func (h *ModpacksHandler) DeleteVersion(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	versionID, _ := strconv.Atoi(mux.Vars(r)["versionId"])
	if _, ok := h.ownsModpack(r, modpackID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	ver, err := h.state.Store.GetModpackVersion(versionID)
	if err != nil || ver == nil {
		sendJSONError(w, "Version not found", http.StatusNotFound)
		return
	}
	if ver.Frozen {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   false,
			"error":     "version_frozen",
			"versionId": versionID,
			"message":   "Version is frozen — create a new version to change it.",
		})
		return
	}
	if err := h.state.Store.DeleteModpackVersion(versionID, modpackID); err != nil {
		sendJSONError(w, "Failed to delete version", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "modpack_versions.changed", map[string]interface{}{"modpackId": modpackID})
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- Mods within a version ---

type modAddRequest struct {
	ProjectID   string `json:"projectId"`
	ProjectSlug string `json:"projectSlug"`
	VersionID   string `json:"versionId"`
	Title       string `json:"title"`
	FileName    string `json:"fileName"`
	DownloadURL string `json:"downloadUrl"`
	SHA512      string `json:"sha512"`
	Side        string `json:"side"`
	Required    *bool  `json:"required,omitempty"`
}

func (h *ModpacksHandler) ListMods(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	versionID, _ := strconv.Atoi(mux.Vars(r)["versionId"])
	if _, ok := h.ownsModpack(r, modpackID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mods, err := h.state.Store.ListModpackMods(versionID)
	if err != nil {
		sendJSONError(w, "Failed to list mods", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "mods": mods})
}

func (h *ModpacksHandler) AddMod(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	versionID, _ := strconv.Atoi(mux.Vars(r)["versionId"])
	if _, ok := h.ownsModpack(r, modpackID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	ver, err := h.state.Store.GetModpackVersion(versionID)
	if err != nil || ver == nil {
		sendJSONError(w, "Version not found", http.StatusNotFound)
		return
	}
	if ver.Frozen {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   false,
			"error":     "version_frozen",
			"versionId": versionID,
			"message":   "Version is frozen — create a new version to change it.",
		})
		return
	}
	var req modAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.VersionID = strings.TrimSpace(req.VersionID)
	req.DownloadURL = strings.TrimSpace(req.DownloadURL)
	req.FileName = strings.TrimSpace(req.FileName)
	if req.ProjectID == "" || req.VersionID == "" || req.DownloadURL == "" || req.FileName == "" {
		sendJSONError(w, "projectId, versionId, downloadUrl, fileName required", http.StatusBadRequest)
		return
	}
	if !isAllowedModrinthURL(req.DownloadURL) {
		sendJSONError(w, "downloadUrl must point to cdn.modrinth.com", http.StatusBadRequest)
		return
	}
	side := req.Side
	if side == "" {
		side = "both"
	}
	required := true
	if req.Required != nil {
		required = *req.Required
	}
	m := &models.ModpackMod{
		ModpackVersionID:    versionID,
		ModrinthProjectID:   req.ProjectID,
		ModrinthProjectSlug: req.ProjectSlug,
		ModrinthVersionID:   req.VersionID,
		Title:               req.Title,
		FileName:            req.FileName,
		DownloadURL:         req.DownloadURL,
		SHA512:              req.SHA512,
		Side:                side,
		Required:            required,
	}
	id, err := h.state.Store.UpsertModpackMod(m)
	if err != nil {
		sendJSONError(w, "Failed to add mod", http.StatusInternalServerError)
		return
	}
	m.ID = id
	h.state.Events.Publish(r.Context(), "modpack_mods.changed", map[string]interface{}{
		"modpackId":        modpackID,
		"modpackVersionId": versionID,
	})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "mod": m})
}

func (h *ModpacksHandler) RemoveMod(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	versionID, _ := strconv.Atoi(mux.Vars(r)["versionId"])
	modID, _ := strconv.Atoi(mux.Vars(r)["modId"])
	if _, ok := h.ownsModpack(r, modpackID); !ok {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	ver, err := h.state.Store.GetModpackVersion(versionID)
	if err != nil || ver == nil {
		sendJSONError(w, "Version not found", http.StatusNotFound)
		return
	}
	if ver.Frozen {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   false,
			"error":     "version_frozen",
			"versionId": versionID,
			"message":   "Version is frozen — create a new version to change it.",
		})
		return
	}
	if err := h.state.Store.DeleteModpackMod(modID, versionID); err != nil {
		sendJSONError(w, "Failed to remove mod", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "modpack_mods.changed", map[string]interface{}{
		"modpackId":        modpackID,
		"modpackVersionId": versionID,
	})
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
