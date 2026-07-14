package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Per-server "Tabs". User defines extra tabs that show up in the
// Server Detail nav strip and render a URL (Minimap, BlueMap, custom plugin
// dashboards) either inside an iframe in the panel (open_in_panel=true) or
// by opening the URL in a new tab. V1 stops there — automatic Gateway
// route creation + container-side HTTP reverse-proxy is a follow-up.

type ServerTabsHandler struct {
	state *AppState
}

func NewServerTabsHandler(state *AppState) *ServerTabsHandler {
	return &ServerTabsHandler{state: state}
}

type serverTabResponse struct {
	ID             int        `json:"id"`
	ServerID       int        `json:"serverId"`
	Name           string     `json:"name"`
	Icon           string     `json:"icon"`
	URL            string     `json:"url"`
	Position       int        `json:"position"`
	Enabled        bool       `json:"enabled"`
	OpenInPanel    bool       `json:"openInPanel"`
	Mode           string     `json:"mode"`
	TargetPort     int        `json:"targetPort"`
	TargetPath     string     `json:"targetPath"`
	Surface        string     `json:"surface"`
	Visibility     string     `json:"visibility"`
	ShareToken     string     `json:"shareToken"`
	ShareExpiresAt *time.Time `json:"shareExpiresAt"`
}

type serverTabRequest struct {
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	URL         string `json:"url"`
	Position    int    `json:"position"`
	Enabled     *bool  `json:"enabled,omitempty"`
	OpenInPanel *bool  `json:"openInPanel,omitempty"`
	Mode        string `json:"mode"`
	TargetPort  *int   `json:"targetPort,omitempty"`
	TargetPath  string `json:"targetPath"`
	Surface     string `json:"surface"`
	Visibility  string `json:"visibility"`
}

func (h *ServerTabsHandler) canAccess(r *http.Request, serverID int, mutating bool) bool {
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		return false
	}
	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)
	if mutating {
		// CRUD is owner/admin only (treated as config-class, same as
		// server.properties + Scheduled Tasks).
		return checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "config")
	}
	// Reading the tab list is anyone who can see the server.
	return checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "overview")
}

func (h *ServerTabsHandler) db() *sql.DB {
	if h.state.Store == nil {
		return nil
	}
	provider, ok := h.state.Store.(sparkDBProvider)
	if !ok {
		return nil
	}
	return provider.RawDB()
}

func (h *ServerTabsHandler) List(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if !h.canAccess(r, serverID, false) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	db := h.db()
	if db == nil {
		sendJSONError(w, "DB unavailable", http.StatusInternalServerError)
		return
	}
	rows, err := db.Query(`SELECT id, server_id, name, icon, url, position, enabled, open_in_panel,
		mode, COALESCE(target_port,0), target_path, surface, visibility,
		COALESCE(share_token,''), share_expires_at
		FROM server_tabs WHERE server_id=$1 ORDER BY position ASC, id ASC`, serverID)
	if err != nil {
		sendJSONError(w, "Query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []serverTabResponse{}
	for rows.Next() {
		var t serverTabResponse
		var expires sql.NullTime
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.Icon, &t.URL, &t.Position, &t.Enabled, &t.OpenInPanel,
			&t.Mode, &t.TargetPort, &t.TargetPath, &t.Surface, &t.Visibility, &t.ShareToken, &expires); err == nil {
			if expires.Valid {
				et := expires.Time
				t.ShareExpiresAt = &et
			}
			out = append(out, t)
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"tabs":    out,
	})
}

func (h *ServerTabsHandler) Create(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if !h.canAccess(r, serverID, true) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req serverTabRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	req.Icon = strings.TrimSpace(req.Icon)
	req.Mode = strings.TrimSpace(req.Mode)
	req.TargetPath = strings.TrimSpace(req.TargetPath)
	req.Surface = strings.TrimSpace(req.Surface)
	req.Visibility = strings.TrimSpace(req.Visibility)
	if req.Icon == "" {
		req.Icon = "layout-grid"
	}
	if req.Name == "" {
		sendJSONError(w, "Name required", http.StatusBadRequest)
		return
	}
	if req.Mode == "" {
		req.Mode = "direct"
	}
	targetPort := 0
	if req.Mode == "proxied" {
		if req.Surface == "" {
			req.Surface = "tab"
		}
		if req.Visibility == "" {
			req.Visibility = "private"
		}
		if req.TargetPath == "" {
			req.TargetPath = "/"
		}
		if req.TargetPort != nil {
			targetPort = *req.TargetPort
		}
		if err := validateProxiedTab(targetPort, req.TargetPath, req.Surface, req.Visibility); err != nil {
			sendJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		req.Mode = "direct"
		req.Surface = "tab"
		req.Visibility = "private"
		req.TargetPath = "/"
		if err := validateTabURL(req.URL); err != nil {
			sendJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	db := h.db()
	if db == nil {
		sendJSONError(w, "DB unavailable", http.StatusInternalServerError)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	openInPanel := true
	if req.OpenInPanel != nil {
		openInPanel = *req.OpenInPanel
	}
	userID, _ := r.Context().Value("userID").(string)
	var createdBy interface{}
	if userID != "" {
		createdBy = userID
	}
	var portArg interface{}
	if req.Mode == "proxied" {
		portArg = targetPort
	}
	var id int
	err := db.QueryRow(`INSERT INTO server_tabs
		(server_id, name, icon, url, position, enabled, open_in_panel, created_by,
		 mode, target_port, target_path, surface, visibility)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
		serverID, req.Name, req.Icon, req.URL, req.Position, enabled, openInPanel, createdBy,
		req.Mode, portArg, req.TargetPath, req.Surface, req.Visibility,
	).Scan(&id)
	if err != nil {
		sendJSONError(w, "Failed to create tab", http.StatusInternalServerError)
		return
	}

	h.state.Events.Publish(r.Context(), "server_tabs.changed", map[string]interface{}{
		"serverId": serverID,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"id":      id,
	})
}

func (h *ServerTabsHandler) Update(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if !h.canAccess(r, serverID, true) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	tabID, _ := strconv.Atoi(mux.Vars(r)["tabId"])
	var req serverTabRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Mode = strings.TrimSpace(req.Mode)
	req.TargetPath = strings.TrimSpace(req.TargetPath)
	req.Surface = strings.TrimSpace(req.Surface)
	req.Visibility = strings.TrimSpace(req.Visibility)
	if req.URL != "" {
		if err := validateTabURL(strings.TrimSpace(req.URL)); err != nil {
			sendJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	targetPort := 0
	if req.TargetPort != nil {
		targetPort = *req.TargetPort
	}
	// PATCH only writes fields the caller actually sent (see COALESCE below),
	// so validate each proxied field the same way: only when present. Render,
	// auth and proxy dispatch downstream trust these DB values, so an invalid
	// port/path/enum must never reach the UPDATE.
	if err := validateProxiedTabPatch(targetPort, req.TargetPath, req.Mode, req.Surface, req.Visibility); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	db := h.db()
	if db == nil {
		sendJSONError(w, "DB unavailable", http.StatusInternalServerError)
		return
	}
	// Patch-style: only fields the client sent are written. COALESCE keeps
	// the existing value for empty strings on optional fields.
	var portArg interface{}
	if req.TargetPort != nil {
		portArg = *req.TargetPort
	}
	res, err := db.Exec(`UPDATE server_tabs SET
		name           = COALESCE(NULLIF($3, ''), name),
		icon           = COALESCE(NULLIF($4, ''), icon),
		url            = COALESCE(NULLIF($5, ''), url),
		position       = COALESCE($6, position),
		enabled        = COALESCE($7, enabled),
		open_in_panel  = COALESCE($8, open_in_panel),
		mode           = COALESCE(NULLIF($9, ''),  mode),
		target_port    = COALESCE($10, target_port),
		target_path    = COALESCE(NULLIF($11, ''), target_path),
		surface        = COALESCE(NULLIF($12, ''), surface),
		visibility     = COALESCE(NULLIF($13, ''), visibility)
		WHERE id=$1 AND server_id=$2`,
		tabID, serverID,
		strings.TrimSpace(req.Name),
		strings.TrimSpace(req.Icon),
		strings.TrimSpace(req.URL),
		nullableInt(req.Position),
		req.Enabled, req.OpenInPanel,
		req.Mode,
		portArg,
		req.TargetPath,
		req.Surface,
		req.Visibility,
	)
	if err != nil {
		sendJSONError(w, "Failed to save tab", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}

	h.state.Events.Publish(r.Context(), "server_tabs.changed", map[string]interface{}{
		"serverId": serverID,
	})

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *ServerTabsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if !h.canAccess(r, serverID, true) {
		sendJSONError(w, "Forbidden", http.StatusForbidden)
		return
	}
	tabID, _ := strconv.Atoi(mux.Vars(r)["tabId"])
	db := h.db()
	if db == nil {
		sendJSONError(w, "DB unavailable", http.StatusInternalServerError)
		return
	}
	res, err := db.Exec(`DELETE FROM server_tabs WHERE id=$1 AND server_id=$2`, tabID, serverID)
	if err != nil {
		sendJSONError(w, "Delete failed", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}

	h.state.Events.Publish(r.Context(), "server_tabs.changed", map[string]interface{}{
		"serverId": serverID,
	})

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// validateTabURL ensures the URL parses + uses http/https. We don't pin
// hosts (unlike Modrinth) since custom tabs are the user's own infra and
// they're responsible for what they wire up.
func validateTabURL(raw string) error {
	if raw == "" {
		return errBadTabURL("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errBadTabURL("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errBadTabURL("url must be http or https")
	}
	return nil
}

// validateProxiedTab checks the proxy-mode fields: a container port in
// [1,65535] and a base path anchored at "/". surface/visibility are
// constrained to the known enums so the render + auth logic never sees an
// unexpected value.
func validateProxiedTab(port int, path, surface, visibility string) error {
	if port < 1 || port > 65535 {
		return errBadTabURL("target port must be between 1 and 65535")
	}
	if path == "" || path[0] != '/' {
		return errBadTabURL("target path must start with /")
	}
	switch surface {
	case "tab", "page", "both":
	default:
		return errBadTabURL("surface must be tab, page or both")
	}
	switch visibility {
	case "private", "public":
	default:
		return errBadTabURL("visibility must be private or public")
	}
	return nil
}

// validateProxiedTabPatch is the Update-path counterpart to validateProxiedTab.
// Update is a partial PATCH (COALESCE/NULLIF keeps any field the caller
// didn't send), so unlike Create there is no single point where every
// proxied field is guaranteed present. Each field is therefore validated
// only when the caller actually set it: port != 0, path/mode/surface/
// visibility != "". A field left at its zero value is presumed unset and
// passes through untouched by the UPDATE.
func validateProxiedTabPatch(port int, path, mode, surface, visibility string) error {
	if port != 0 && (port < 1 || port > 65535) {
		return errBadTabURL("target port must be between 1 and 65535")
	}
	if path != "" && path[0] != '/' {
		return errBadTabURL("target path must start with /")
	}
	if mode != "" {
		switch mode {
		case "direct", "proxied":
		default:
			return errBadTabURL("mode must be direct or proxied")
		}
	}
	if surface != "" {
		switch surface {
		case "tab", "page", "both":
		default:
			return errBadTabURL("surface must be tab, page or both")
		}
	}
	if visibility != "" {
		switch visibility {
		case "private", "public":
		default:
			return errBadTabURL("visibility must be private or public")
		}
	}
	return nil
}

type tabErr struct{ msg string }

func (e tabErr) Error() string { return e.msg }
func errBadTabURL(s string) error {
	return tabErr{msg: s}
}

// nullableInt returns nil for 0, the value otherwise — used so a missing
// `position` field in the patch doesn't clobber the existing one.
func nullableInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

// Quiet unused-import warning when downstream changes drop time references.
var _ = time.Time{}
