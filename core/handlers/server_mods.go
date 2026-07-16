package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"strings"

	"dylaris-core/models"

	"github.com/gorilla/mux"
)

// Install / list / uninstall Modrinth-sourced mods per server.
// We dispatch download work to the node via the existing Queue (mailbox)
// pipeline so the node-side worker can lift its own implementation when
// it picks up the command — no new proto round trip needed for V1.

type ServerModsHandler struct {
	state *AppState
}

func NewServerModsHandler(state *AppState) *ServerModsHandler {
	return &ServerModsHandler{state: state}
}

type installModRequest struct {
	ProjectID    string `json:"projectId"`
	ProjectSlug  string `json:"projectSlug"`
	VersionID    string `json:"versionId"`
	Title        string `json:"title"`
	FileName     string `json:"fileName"`
	DownloadURL  string `json:"downloadUrl"`
	SHA512       string `json:"sha512"`
	TargetDir    string `json:"targetDir"` // "mods" or "plugins" — derived from loader if empty
}

// modrinthAllowedHosts pins where we'll dispatch downloads from. Modrinth
// only serves files from cdn.modrinth.com — anything else in a "downloadUrl"
// field would be a maliciously-mutated payload.
var modrinthAllowedHosts = map[string]bool{
	"cdn.modrinth.com": true,
}

// getServer fetches the server the route's RequireCap already gated access
// to. The pure authorization check used to live here (checkServerAccess); the
// router's mods.read/mods.write/mods.delete RequireCap now enforces it before
// this handler ever runs.
func (h *ServerModsHandler) getServer(serverID int) (*models.Server, bool) {
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		return nil, false
	}
	return srv, true
}

func (h *ServerModsHandler) List(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	srv, ok := h.getServer(serverID)
	if !ok {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	mods, err := h.state.Store.ListServerMods(serverID, srv.ActiveSubServer)
	if err != nil {
		sendJSONError(w, "Failed to list mods", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"mods":    mods,
	})
}

// ModpackContents returns the modpack snapshot for the active sub-server: the
// Modrinth-identified members of the pack this server was installed from. Empty
// when the server is not a modpack server. Same mods.read gate as the mods
// endpoints. Backs the panel's advisory cross-check + modpack banner.
func (h *ServerModsHandler) ModpackContents(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	srv, ok := h.getServer(serverID)
	if !ok {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	contents, err := h.state.Store.ListServerModpackContents(serverID, srv.ActiveSubServer)
	if err != nil {
		sendJSONError(w, "Failed to list modpack contents", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"contents": contents,
	})
}

func (h *ServerModsHandler) Install(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	srv, ok := h.getServer(serverID)
	if !ok {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	var req installModRequest
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
	// Trust-but-verify: the panel sends a downloadUrl it pulled from Modrinth,
	// but a tampered POST could send anything. Enforce that the host is a
	// known Modrinth CDN.
	if !isAllowedModrinthURL(req.DownloadURL) {
		sendJSONError(w, "downloadUrl must point to cdn.modrinth.com", http.StatusBadRequest)
		return
	}
	if srv.ActiveSubServer == "" {
		sendJSONError(w, "No active sub-server — set one up first", http.StatusBadRequest)
		return
	}
	targetDir := req.TargetDir
	if targetDir == "" {
		targetDir = defaultTargetDirForLoader(srv.InstallerType)
	}
	if targetDir != "mods" && targetDir != "plugins" {
		sendJSONError(w, "targetDir must be 'mods' or 'plugins'", http.StatusBadRequest)
		return
	}
	// Filename sanitation: no slashes, no upward traversal.
	cleanName := path.Base(req.FileName)
	if cleanName == "" || cleanName == "." || cleanName == ".." || strings.ContainsAny(cleanName, "\\/") {
		sendJSONError(w, "invalid fileName", http.StatusBadRequest)
		return
	}

	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err != nil {
		sendJSONError(w, "Node unavailable", http.StatusServiceUnavailable)
		return
	}
	if h.state.Queue == nil {
		sendJSONError(w, "Queue unavailable", http.StatusServiceUnavailable)
		return
	}

	configPayload := map[string]interface{}{
		"uuid":            srv.UUID,
		"activeSubServer": srv.ActiveSubServer,
		"targetDir":       targetDir,
		"fileName":        cleanName,
		"downloadUrl":     req.DownloadURL,
		"sha512":          req.SHA512,
	}
	if err := h.state.Queue.SendCommand(context.Background(), node.Token, "install_mod", configPayload, nil); err != nil {
		sendJSONError(w, "Failed to queue install", http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("userID").(string)
	var installedBy *string
	if userID != "" {
		v := userID
		installedBy = &v
	}
	mod := &models.ServerMod{
		ServerID:            serverID,
		SubServerName:       srv.ActiveSubServer,
		ModrinthProjectID:   req.ProjectID,
		ModrinthProjectSlug: req.ProjectSlug,
		ModrinthVersionID:   req.VersionID,
		Title:               req.Title,
		FileName:            cleanName,
		SHA512:              req.SHA512,
		InstalledBy:         installedBy,
	}
	if _, err := h.state.Store.UpsertServerMod(mod); err != nil {
		sendJSONError(w, "Install queued, but DB write failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.state.Events.Publish(r.Context(), "server_mods.changed", map[string]interface{}{
		"serverId": serverID,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Mod install queued. Restart the server to apply.",
	})
}

func (h *ServerModsHandler) Uninstall(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	srv, ok := h.getServer(serverID)
	if !ok {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	modID, _ := strconv.Atoi(mux.Vars(r)["modId"])
	mods, _ := h.state.Store.ListServerMods(serverID, srv.ActiveSubServer)
	var target *models.ServerMod
	for i := range mods {
		if mods[i].ID == modID {
			target = &mods[i]
			break
		}
	}
	if target == nil {
		sendJSONError(w, "Mod not found", http.StatusNotFound)
		return
	}

	node, err := h.state.Store.GetNodeByID(srv.NodeID)
	if err != nil {
		sendJSONError(w, "Node unavailable", http.StatusServiceUnavailable)
		return
	}
	targetDir := defaultTargetDirForLoader(srv.InstallerType)
	if h.state.Queue != nil {
		_ = h.state.Queue.SendCommand(context.Background(), node.Token, "remove_mod", map[string]interface{}{
			"uuid":            srv.UUID,
			"activeSubServer": srv.ActiveSubServer,
			"targetDir":       targetDir,
			"fileName":        target.FileName,
		}, nil)
	}
	if err := h.state.Store.DeleteServerMod(modID, serverID); err != nil {
		sendJSONError(w, "Failed to remove mod entry", http.StatusInternalServerError)
		return
	}

	h.state.Events.Publish(r.Context(), "server_mods.changed", map[string]interface{}{
		"serverId": serverID,
	})

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func isAllowedModrinthURL(u string) bool {
	// Cheap check — full url.Parse adds error-handling weight; we only
	// need scheme+host validation here.
	if !strings.HasPrefix(u, "https://") {
		return false
	}
	rest := strings.TrimPrefix(u, "https://")
	slash := strings.IndexByte(rest, '/')
	if slash < 1 {
		return false
	}
	host := rest[:slash]
	return modrinthAllowedHosts[host]
}

// defaultTargetDirForLoader returns "mods" for fabric/forge/quilt/neoforge,
// "plugins" for paper/spigot/bukkit/purpur. Unknown loader → "mods" (safer
// default since dropping a jar in "mods" never breaks a paper server, but
// dropping a fabric mod in "plugins" leaves it silently inactive).
func defaultTargetDirForLoader(loader string) string {
	switch strings.ToLower(loader) {
	case "paper", "spigot", "bukkit", "purpur", "pufferfish", "velocity", "waterfall", "bungeecord":
		return "plugins"
	default:
		return "mods"
	}
}
