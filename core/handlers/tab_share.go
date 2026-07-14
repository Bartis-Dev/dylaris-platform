package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// shareTokenAlphabet is URL-safe base62; a 16-char token carries ~95 bits of
// entropy, generated with crypto/rand and rejection sampling (no modulo bias).
const shareTokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const shareTokenLen = 16

func generateShareToken() (string, error) {
	// Reject bytes >= the largest multiple of 62 that fits in a byte so every
	// alphabet symbol is equiprobable.
	const limit = 256 - (256 % len(shareTokenAlphabet))
	out := make([]byte, shareTokenLen)
	var b [1]byte
	for i := 0; i < shareTokenLen; {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		if int(b[0]) >= limit {
			continue
		}
		out[i] = shareTokenAlphabet[int(b[0])%len(shareTokenAlphabet)]
		i++
	}
	return string(out), nil
}

// capReached reports whether an additive operation would exceed a max cap.
func capReached(current, max int) bool {
	return current >= max
}

func (h *ServerTabsHandler) countProxiedTabs(db *sql.DB, serverID int) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM server_tabs WHERE server_id=$1 AND mode='proxied'`, serverID).Scan(&n)
	return n, err
}

func (h *ServerTabsHandler) countUserShareLinks(db *sql.DB, userID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM server_tabs WHERE created_by=$1 AND share_token IS NOT NULL`, userID).Scan(&n)
	return n, err
}

// RotateShareLink POST /api/servers/{id}/tabs/{tabId}/share-link - (re)generate
// the unguessable slug for a proxied page tab.
func (h *ServerTabsHandler) RotateShareLink(w http.ResponseWriter, r *http.Request) {
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
	var mode, surface string
	if err := db.QueryRow(`SELECT mode, surface FROM server_tabs WHERE id=$1 AND server_id=$2`,
		tabID, serverID).Scan(&mode, &surface); err != nil {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	if mode != "proxied" || (surface != "page" && surface != "both") {
		sendJSONError(w, "Share links only apply to proxied page tabs", http.StatusBadRequest)
		return
	}
	tok, err := generateShareToken()
	if err != nil {
		sendJSONError(w, "Failed to generate share token", http.StatusInternalServerError)
		return
	}
	res, err := db.Exec(`UPDATE server_tabs SET share_token=$3 WHERE id=$1 AND server_id=$2`, tabID, serverID, tok)
	if err != nil {
		sendJSONError(w, "Failed to save share link", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	h.state.Events.Publish(r.Context(), "server_tabs.changed", map[string]interface{}{"serverId": serverID})
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "shareToken": tok})
}

// RevokeShareLink DELETE /api/servers/{id}/tabs/{tabId}/share-link - null the
// slug so the standalone page 404s. The tab itself stays.
func (h *ServerTabsHandler) RevokeShareLink(w http.ResponseWriter, r *http.Request) {
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
	res, err := db.Exec(`UPDATE server_tabs SET share_token=NULL WHERE id=$1 AND server_id=$2`, tabID, serverID)
	if err != nil {
		sendJSONError(w, "Failed to revoke share link", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		sendJSONError(w, "Not found", http.StatusNotFound)
		return
	}
	h.state.Events.Publish(r.Context(), "server_tabs.changed", map[string]interface{}{"serverId": serverID})
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
