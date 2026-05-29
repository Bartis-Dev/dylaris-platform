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

// Phase 13 — per-server "Tabs". User defines extra tabs that show up in the
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
	ID          int    `json:"id"`
	ServerID    int    `json:"serverId"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	URL         string `json:"url"`
	Position    int    `json:"position"`
	Enabled     bool   `json:"enabled"`
	OpenInPanel bool   `json:"openInPanel"`
}

type serverTabRequest struct {
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	URL         string `json:"url"`
	Position    int    `json:"position"`
	Enabled     *bool  `json:"enabled,omitempty"`
	OpenInPanel *bool  `json:"openInPanel,omitempty"`
}

func (h *ServerTabsHandler) canAccess(r *http.Request, serverID int, mutating bool) bool {
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		return false
	}
	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(int)
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
	rows, err := db.Query(`SELECT id, server_id, name, icon, url, position, enabled, open_in_panel
		FROM server_tabs WHERE server_id=$1 ORDER BY position ASC, id ASC`, serverID)
	if err != nil {
		sendJSONError(w, "Query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []serverTabResponse{}
	for rows.Next() {
		var t serverTabResponse
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.Icon, &t.URL, &t.Position, &t.Enabled, &t.OpenInPanel); err == nil {
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
	if req.Icon == "" {
		req.Icon = "layout-grid"
	}
	if req.Name == "" {
		sendJSONError(w, "Name required", http.StatusBadRequest)
		return
	}
	if err := validateTabURL(req.URL); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
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
	userID, _ := r.Context().Value("userID").(int)
	var createdBy interface{}
	if userID > 0 {
		createdBy = userID
	}
	var id int
	err := db.QueryRow(`INSERT INTO server_tabs
		(server_id, name, icon, url, position, enabled, open_in_panel, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		serverID, req.Name, req.Icon, req.URL, req.Position, enabled, openInPanel, createdBy,
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
	if req.URL != "" {
		if err := validateTabURL(strings.TrimSpace(req.URL)); err != nil {
			sendJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	db := h.db()
	if db == nil {
		sendJSONError(w, "DB unavailable", http.StatusInternalServerError)
		return
	}
	// Patch-style: only fields the client sent are written. COALESCE keeps
	// the existing value for empty strings on optional fields.
	res, err := db.Exec(`UPDATE server_tabs SET
		name           = COALESCE(NULLIF($3, ''), name),
		icon           = COALESCE(NULLIF($4, ''), icon),
		url            = COALESCE(NULLIF($5, ''), url),
		position       = COALESCE($6, position),
		enabled        = COALESCE($7, enabled),
		open_in_panel  = COALESCE($8, open_in_panel)
		WHERE id=$1 AND server_id=$2`,
		tabID, serverID,
		strings.TrimSpace(req.Name),
		strings.TrimSpace(req.Icon),
		strings.TrimSpace(req.URL),
		nullableInt(req.Position),
		req.Enabled, req.OpenInPanel,
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
