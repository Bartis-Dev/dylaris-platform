package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// NodeEnrollHandler mints + manages per-user node enrollment tokens (BYON). A
// tenant mints a token in the panel, then runs their node with it as
// NODE_ENROLL_TOKEN; on first heartbeat the node is bound to that user (wired in
// the discovery binding chunk). All endpoints require feature_byon_enabled.
type NodeEnrollHandler struct {
	state *AppState
}

func NewNodeEnrollHandler(state *AppState) *NodeEnrollHandler {
	return &NodeEnrollHandler{state: state}
}

// maxNodeEnrollTokenExpiryDays caps a tenant-supplied expiresDays so a leaked
// or mistyped huge value cannot mint an effectively-eternal enroll token.
const maxNodeEnrollTokenExpiryDays = 30

// MintToken POST /api/nodes/enroll-token — generate a new enroll token for the
// calling user. The plaintext is returned ONCE; only its hash is stored.
func (h *NodeEnrollHandler) MintToken(w http.ResponseWriter, r *http.Request) {
	if !byonActive(h.state, r) {
		sendJSONError(w, "BYON is not enabled", http.StatusForbidden)
		return
	}
	userID := byonCallerID(r)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Label       string `json:"label"`
		ExpiresDays int    `json:"expiresDays"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		sendJSONError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(b)

	// Default a 7-day expiry when none is given, so a leaked token is not valid
	// forever. Single-use (consumed on enroll) already limits reuse; 7 days is
	// enough to set a node up. Cap a tenant-supplied value too: an unbounded
	// expiresDays would let a mistyped or malicious huge number mint an
	// effectively-eternal enroll token.
	days := req.ExpiresDays
	if days <= 0 {
		days = 7
	} else if days > maxNodeEnrollTokenExpiryDays {
		days = maxNodeEnrollTokenExpiryDays
	}
	t := time.Now().AddDate(0, 0, days)
	exp := &t
	if err := h.state.Store.CreateNodeEnrollToken(userID, token, strings.TrimSpace(req.Label), exp); err != nil {
		sendJSONError(w, "Failed to create token", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"token":              token,
		"grpcTlsFingerprint": h.state.GRPCTLSFingerprint,
		"note":               "Shown once. Start your node with NODE_ENROLL_TOKEN set to this value. When GRPC_TLS_ENABLED, also set GRPC_TLS_FINGERPRINT.",
	})
}

// ListTokens GET /api/nodes/enroll-token — the caller's tokens (metadata only).
func (h *NodeEnrollHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	if !byonActive(h.state, r) {
		sendJSONError(w, "BYON is not enabled", http.StatusForbidden)
		return
	}
	userID := byonCallerID(r)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	tokens, err := h.state.Store.ListNodeEnrollTokens(userID)
	if err != nil {
		sendJSONError(w, "Failed to load tokens", http.StatusInternalServerError)
		return
	}
	if tokens == nil {
		tokens = []store.NodeEnrollToken{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tokens": tokens})
}

// RevokeToken DELETE /api/nodes/enroll-token/{id} — revoke one of the caller's
// tokens (scoped to owner).
func (h *NodeEnrollHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	if !byonActive(h.state, r) {
		sendJSONError(w, "BYON is not enabled", http.StatusForbidden)
		return
	}
	userID := byonCallerID(r)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	id := mux.Vars(r)["id"]
	if err := h.state.Store.DeleteNodeEnrollToken(id, userID); err != nil {
		sendJSONError(w, "Failed to revoke token", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
