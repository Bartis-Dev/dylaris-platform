package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dylaris-core/models"
	"dylaris-core/services/redisacl"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// NodeAdmissionHandler serves the admin admission config (join/IP mode + CIDRs)
// and the per-node reset-pairing action. All endpoints are admin-only.
type NodeAdmissionHandler struct {
	state *AppState
}

func NewNodeAdmissionHandler(state *AppState) *NodeAdmissionHandler {
	return &NodeAdmissionHandler{state: state}
}

// admissionPayload is the wire shape for GET/PUT of the join + IP mode.
type admissionPayload struct {
	JoinMode string `json:"joinMode"`
	IPMode   string `json:"ipMode"`
}

// GetAdmission GET /api/admin/settings/node-admission — current modes + CIDRs.
func (h *NodeAdmissionHandler) GetAdmission(w http.ResponseWriter, r *http.Request) {
	joinMode, err := h.state.Store.GetSetting("node_join_mode")
	if err != nil || joinMode == "" {
		joinMode = "open"
	}
	ipMode, err := h.state.Store.GetSetting("node_admission_ip_mode")
	if err != nil || ipMode == "" {
		ipMode = "allow"
	}
	cidrs, err := h.state.Store.ListAdmissionCIDRs()
	if err != nil {
		sendJSONError(w, "Failed to load CIDRs", http.StatusInternalServerError)
		return
	}
	if cidrs == nil {
		cidrs = []store.AdmissionCIDR{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"joinMode": joinMode,
		"ipMode":   ipMode,
		"cidrs":    cidrs,
	})
}

// SetAdmission PUT /api/admin/settings/node-admission — write join + IP mode.
func (h *NodeAdmissionHandler) SetAdmission(w http.ResponseWriter, r *http.Request) {
	var req admissionPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	validJoin := map[string]bool{"disabled": true, "open": true, "one-shot": true}
	validIP := map[string]bool{"allow": true, "deny": true}
	if !validJoin[req.JoinMode] || !validIP[req.IPMode] {
		sendJSONError(w, "Invalid joinMode or ipMode", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.SetSetting("node_join_mode", req.JoinMode); err != nil {
		sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.state.Store.SetSetting("node_admission_ip_mode", req.IPMode); err != nil {
		sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if uid := byonCallerID(r); uid != "" {
		_ = h.state.Store.InsertAuditIdentity(&models.AuditEventIdentity{
			EventType:   "node.admission_changed",
			ActorUserID: &uid,
			Metadata:    map[string]interface{}{"joinMode": req.JoinMode, "ipMode": req.IPMode},
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"joinMode": req.JoinMode,
		"ipMode":   req.IPMode,
	})
}

// AddCIDR POST /api/admin/settings/node-admission/cidrs — add one allowlist CIDR.
func (h *NodeAdmissionHandler) AddCIDR(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CIDR  string `json:"cidr"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	_, netw, perr := net.ParseCIDR(strings.TrimSpace(req.CIDR))
	if perr != nil {
		sendJSONError(w, "Invalid CIDR", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.AddAdmissionCIDR(netw.String(), strings.TrimSpace(req.Label)); err != nil {
		sendJSONError(w, "Failed to add CIDR", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "cidr": netw.String()})
}

// DeleteCIDR DELETE /api/admin/settings/node-admission/cidrs/{id}.
func (h *NodeAdmissionHandler) DeleteCIDR(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.state.Store.DeleteAdmissionCIDR(id); err != nil {
		sendJSONError(w, "Failed to delete CIDR", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// ResetPairing POST /api/admin/nodes/{id}/reset-pairing — REVOKE + RECOVER:
// clear the node's secret, hard-cut its live Redis ACL, and mint a single-use
// recovery token bound to its identity. The node row / owner / servers / backups
// are untouched; recovery re-provisions the ACL under a fresh secret on re-pair.
func (h *NodeAdmissionHandler) ResetPairing(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	node, err := h.state.Store.GetNodeByID(id)
	if err != nil || node == nil {
		sendJSONError(w, "Node not found", http.StatusNotFound)
		return
	}
	uid := byonCallerID(r)

	b := make([]byte, 32)
	if _, rerr := rand.Read(b); rerr != nil {
		sendJSONError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(b)
	t := time.Now().AddDate(0, 0, 7)
	if err := h.state.Store.CreateRecoveryToken(uid, token, node.Token, &t); err != nil {
		sendJSONError(w, "Failed to create recovery token", http.StatusInternalServerError)
		return
	}
	// Invalidate the current secret: HasSecret -> false forces the next reconnect
	// through the recovery branch (which re-issues a fresh secret under this id).
	if err := h.state.Store.SetNodeSecretEnc(node.ID, ""); err != nil {
		sendJSONError(w, "Failed to reset secret", http.StatusInternalServerError)
		return
	}
	// Hard-cut the live Redis ACL so a possibly-compromised node loses access at
	// once (ACL DELUSER disconnects live clients) instead of only at its next
	// reconnect. Best-effort. Recovery re-provisions all three users under the new
	// secret via EnsureNodeACL when the node re-pairs.
	if h.state.Redis != nil {
		redisacl.NewProvisioner(h.state.Redis).RemoveNodeACL(r.Context(), node.Token)
	}
	if uid != "" {
		_ = h.state.Store.InsertAuditIdentity(&models.AuditEventIdentity{
			EventType:   "node.pairing_reset",
			ActorUserID: &uid,
			Metadata:    map[string]interface{}{"nodeId": node.ID, "nodeToken": node.Token},
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"token":   token,
		"env":     "NODE_RECOVERY_TOKEN=" + token,
		"note":    "Shown once. Set NODE_RECOVERY_TOKEN on the node to re-pair under its existing identity, then remove it after a successful reconnect.",
	})
}
