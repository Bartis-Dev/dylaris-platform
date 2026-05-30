package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"

	"github.com/gorilla/mux"
)

// Phase 14.3 — Modrinth publish flow.
//
// POST /api/modpacks/{id}/versions/{versionId}/publish
//
// Steps:
//   1. Resolve PAT (user must have one configured).
//   2. Ensure the Modrinth project exists — if pack.modrinth_project_id is
//      empty, create one with sensible defaults (license=arr, project_type=
//      modpack, side=required/optional per pack.loader).
//   3. Build .mrpack into a memory buffer (no on-disk staging).
//   4. POST /v2/version with the file + metadata derived from the version's
//      channel: draft→aborted (refuse to publish drafts), beta→version_type=
//      beta + status=unlisted, release→version_type=release + status from
//      pack.modrinth_visibility.
//   5. Stamp pack.modrinth_project_id (first time) + version.modrinth_version_id
//      + version.published_at + version.channel (in case of promote).

type publishRequest struct {
	// PromoteTo overrides version.channel if set. Lets the panel say
	// "publish this draft as beta" without a separate channel-change call.
	PromoteTo string `json:"promoteTo,omitempty"`
}

type publishResponse struct {
	Success           bool   `json:"success"`
	ModrinthProjectID string `json:"modrinthProjectId"`
	ModrinthVersionID string `json:"modrinthVersionId"`
	Message           string `json:"message,omitempty"`
}

// Publish is wired in main.go alongside the existing ModpacksHandler routes.
// Lives on a separate handler to keep the PAT-loader dependency from leaking
// into the CRUD-only handler.
type ModpacksPublishHandler struct {
	state    *AppState
	patHand  *ModrinthPATHandler
	userAgent string
}

func NewModpacksPublishHandler(state *AppState, patHand *ModrinthPATHandler, userAgent string) *ModpacksPublishHandler {
	if userAgent == "" {
		userAgent = "Dylaris/0.14 (+https://github.com/Bartis-Dev/dylaris-platform)"
	}
	return &ModpacksPublishHandler{state: state, patHand: patHand, userAgent: userAgent}
}

func (h *ModpacksPublishHandler) Publish(w http.ResponseWriter, r *http.Request) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	versionID, _ := strconv.Atoi(mux.Vars(r)["versionId"])
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	pack, err := h.state.Store.GetModpack(modpackID)
	if err != nil || pack.OwnerID != userID {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	version, err := h.state.Store.GetModpackVersion(versionID)
	if err != nil || version.ModpackID != modpackID {
		sendJSONError(w, "Version not found", http.StatusNotFound)
		return
	}

	var req publishRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional

	channel := version.Channel
	if req.PromoteTo != "" {
		switch req.PromoteTo {
		case models.ModpackChannelBeta, models.ModpackChannelRelease:
			channel = req.PromoteTo
		default:
			sendJSONError(w, "promoteTo must be beta or release", http.StatusBadRequest)
			return
		}
	}
	if channel == models.ModpackChannelDraft {
		sendJSONError(w, "Drafts cannot be published. Set channel to beta or release first.", http.StatusBadRequest)
		return
	}

	pat, username, err := h.patHand.LoadPAT(userID)
	if err != nil {
		sendJSONError(w, "Modrinth PAT not configured: "+err.Error(), http.StatusPreconditionRequired)
		return
	}
	mods, err := h.state.Store.ListModpackMods(versionID)
	if err != nil || len(mods) == 0 {
		sendJSONError(w, "Version has no mods yet — add some before publishing", http.StatusBadRequest)
		return
	}

	mrpack, err := buildMrpackBytes(pack, version, mods)
	if err != nil {
		sendJSONError(w, "Failed to build .mrpack: "+err.Error(), http.StatusInternalServerError)
		return
	}

	mc := services.NewModrinthClient(pat, h.userAgent)

	projectID := pack.ModrinthProjectID
	if projectID == "" {
		created, err := h.createProject(r, mc, pack)
		if err != nil {
			sendJSONError(w, "Modrinth create project failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		projectID = created.ID
		pack.ModrinthProjectID = projectID
		if err := h.state.Store.UpdateModpack(pack); err != nil {
			// Project landed on Modrinth but our DB didn't catch the ID;
			// surface this so the user knows their next publish will retry
			// instead of duplicating.
			sendJSONError(w, "Project created on Modrinth ("+projectID+") but local update failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	versionType := "beta"
	if channel == models.ModpackChannelRelease {
		versionType = "release"
	}
	status := "listed"
	if channel == models.ModpackChannelBeta || pack.ModrinthVisibility == "unlisted" {
		status = "unlisted"
	}

	filename := fmt.Sprintf("%s-%s.mrpack", pack.Slug, version.VersionString)
	created, err := mc.UploadVersion(r.Context(), services.CreateVersionRequest{
		Name:          version.VersionString,
		VersionNumber: version.VersionString,
		Changelog:     version.Changelog,
		Dependencies:  []string{},
		GameVersions:  []string{pack.McVersion},
		VersionType:   versionType,
		Loaders:       []string{pack.Loader},
		Featured:      channel == models.ModpackChannelRelease,
		Status:        status,
		ProjectID:     projectID,
	}, mrpack, filename)
	if err != nil {
		sendJSONError(w, "Modrinth upload failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Stamp local state.
	now := time.Now().UTC()
	version.ModrinthVersionID = created.ID
	version.Channel = channel
	version.PublishedAt = &now
	version.FileSize = int64(len(mrpack))
	if err := h.state.Store.UpdateModpackVersion(version); err != nil {
		sendJSONError(w, "Published "+created.ID+" but local stamp failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.state.Events.Publish(r.Context(), "modpack_versions.changed", map[string]interface{}{"modpackId": modpackID})
	h.state.Events.Publish(r.Context(), "modpacks.changed", map[string]interface{}{"ownerId": userID})

	json.NewEncoder(w).Encode(publishResponse{
		Success:           true,
		ModrinthProjectID: projectID,
		ModrinthVersionID: created.ID,
		Message:           "Published as " + username + "/" + pack.Slug + " (" + channel + ")",
	})
}

func (h *ModpacksPublishHandler) createProject(r *http.Request, mc *services.ModrinthClient, pack *models.Modpack) (*services.ProjectResponse, error) {
	// Server-required is the safe default for modpacks (most mods need
	// the server jars); modpacks always run on a client too. We keep the
	// pack.modrinth_visibility-derived isDraft path: unlisted packs go
	// live immediately but unlisted; listed packs land as "draft" so the
	// owner has to approve on modrinth.com (Modrinth review queue).
	body := pack.Summary
	if body == "" {
		body = pack.Name + " — published from Dylaris"
	}
	req := services.CreateProjectRequest{
		Slug:        pack.Slug,
		Title:       pack.Name,
		Description: pack.Summary,
		Body:        body,
		ProjectType: "modpack",
		ClientSide:  "required",
		ServerSide:  "required",
		License:     "arr",
		IsDraft:     pack.ModrinthVisibility == "listed",
		Categories:  []string{},
	}
	return mc.CreateProject(r.Context(), req)
}

// buildMrpackBytes is the in-memory counterpart to ExportMrpack's streaming
// download. The logic mirrors that handler so a publish produces the exact
// same bytes a user would have downloaded via the Export button.
func buildMrpackBytes(pack *models.Modpack, version *models.ModpackVersion, mods []models.ModpackMod) ([]byte, error) {
	idx := buildMrpackIndex(pack, version, mods)
	indexBytes, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	entry, err := zw.CreateHeader(&zip.FileHeader{
		Name:     "modrinth.index.json",
		Method:   zip.Deflate,
		Modified: time.Now(),
	})
	if err != nil {
		return nil, err
	}
	if _, err := entry.Write(indexBytes); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- Collaborators ---

type CollaboratorsHandler struct {
	state    *AppState
	patHand  *ModrinthPATHandler
	userAgent string
}

func NewCollaboratorsHandler(state *AppState, patHand *ModrinthPATHandler, userAgent string) *CollaboratorsHandler {
	if userAgent == "" {
		userAgent = "Dylaris/0.14 (+https://github.com/Bartis-Dev/dylaris-platform)"
	}
	return &CollaboratorsHandler{state: state, patHand: patHand, userAgent: userAgent}
}

type collaboratorAddRequest struct {
	Username string `json:"username"`
}

func (h *CollaboratorsHandler) require(r *http.Request) (*models.Modpack, *services.ModrinthClient, error) {
	modpackID, _ := strconv.Atoi(mux.Vars(r)["id"])
	userID, _ := r.Context().Value("userID").(string)
	pack, err := h.state.Store.GetModpack(modpackID)
	if err != nil || pack.OwnerID != userID {
		return nil, nil, fmt.Errorf("forbidden")
	}
	if pack.ModrinthProjectID == "" {
		return nil, nil, fmt.Errorf("publish the modpack to Modrinth first")
	}
	pat, _, err := h.patHand.LoadPAT(userID)
	if err != nil {
		return nil, nil, err
	}
	return pack, services.NewModrinthClient(pat, h.userAgent), nil
}

func (h *CollaboratorsHandler) List(w http.ResponseWriter, r *http.Request) {
	pack, mc, err := h.require(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := mc.ListProjectMembers(r.Context(), pack.ModrinthProjectID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"collaborators": rows,
	})
}

func (h *CollaboratorsHandler) Add(w http.ResponseWriter, r *http.Request) {
	pack, mc, err := h.require(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req collaboratorAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		sendJSONError(w, "username required", http.StatusBadRequest)
		return
	}
	if err := mc.InviteMember(r.Context(), pack.ModrinthProjectID, req.Username); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *CollaboratorsHandler) Remove(w http.ResponseWriter, r *http.Request) {
	pack, mc, err := h.require(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	modrinthUserID := mux.Vars(r)["modrinthUserId"]
	if err := mc.RemoveMember(r.Context(), pack.ModrinthProjectID, modrinthUserID); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
