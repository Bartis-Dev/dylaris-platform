package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "dylaris-proto/node"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// RCON handlers. Two surfaces:
//   * Panel-internal: POST /api/servers/{id}/rcon (auth via session JWT)
//   * External:       POST /api/external/rcon/{uuid}/exec (auth via API key)
//
// Both funnel into a single helper that resolves the server, loads the
// stored RCON config, then dispatches the command via gRPC to the node.
// Permission gates differ — internal uses the existing server-access matrix
// (power-class action); external uses the API key's scope.

type RconHandler struct {
	state *AppState
}

func NewRconHandler(state *AppState) *RconHandler {
	return &RconHandler{state: state}
}

const (
	// rconExecTimeout is how long we wait for the node to come back with a
	// reply. MC servers typically reply instantly but a frozen server can
	// hang the TCP socket — we'd rather report timeout to the user than
	// stall a request thread.
	rconExecTimeout    = 5 * time.Second
	rconMaxCommandLen  = 1024
	defaultRconPort    = 25575
)

type rconRequest struct {
	Command   string `json:"command"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
}

type rconResponse struct {
	Success    bool   `json:"success"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// ExecForUser POST /api/servers/{id}/rcon — panel-side entry. Gated by the
// existing "power"-class permission (owner / admin / member-with-power).
func (h *RconHandler) ExecForUser(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	srv, err := h.state.Store.GetServerByID(serverID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	var req rconRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	resp := h.execAgainstServer(r.Context(), srv.ID, srv.UUID, srv.NodeID, req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ExecExternal POST /api/external/rcon/{uuid}/exec — automation entry.
// Auth + scope check happen in the middleware before we land here; once we
// hit this handler the key is already valid and scoped to the server UUID
// from the URL.
func (h *RconHandler) ExecExternal(w http.ResponseWriter, r *http.Request) {
	serverUUID := mux.Vars(r)["uuid"]
	srv, err := h.state.Store.GetServerByUUID(serverUUID)
	if err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	var req rconRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	resp := h.execAgainstServer(r.Context(), srv.ID, srv.UUID, srv.NodeID, req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *RconHandler) execAgainstServer(ctx context.Context, serverID int, serverUUID string, nodeID int, req rconRequest) rconResponse {
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		return rconResponse{Error: "command required"}
	}
	if len(req.Command) > rconMaxCommandLen {
		return rconResponse{Error: "command too long"}
	}

	enabled, port, password, err := h.state.Store.GetServerRconConfig(serverID)
	if err != nil {
		return rconResponse{Error: "failed to load rcon config"}
	}
	if !enabled || password == "" {
		return rconResponse{Error: "rcon not enabled for this server"}
	}
	if port == 0 {
		port = defaultRconPort
	}
	if h.state.GRPCRegistry == nil {
		return rconResponse{Error: "node registry not available"}
	}

	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 || timeoutMs > 10_000 {
		timeoutMs = int(rconExecTimeout / time.Millisecond)
	}

	msg := &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: serverUUID,
		Payload: &pb.NodeMessage_RconExecReq{
			RconExecReq: &pb.RconExecReq{
				Command:      req.Command,
				RconPassword: password,
				RconPort:     int32(port),
				TimeoutMs:    int32(timeoutMs),
			},
		},
	}
	respMsg, err := h.state.GRPCRegistry.SendRequest(nodeID, msg, time.Duration(timeoutMs+1000)*time.Millisecond)
	if err != nil {
		return rconResponse{Error: fmt.Sprintf("node call failed: %v", err)}
	}
	rconResp := respMsg.GetRconExecResp()
	if rconResp == nil {
		if errMsg := respMsg.GetError(); errMsg != nil {
			return rconResponse{Error: errMsg.Message}
		}
		return rconResponse{Error: "unexpected response from node"}
	}
	return rconResponse{
		Success:    rconResp.Ok,
		Output:     rconResp.Output,
		Error:      rconResp.Error,
		DurationMs: rconResp.DurationMs,
	}
}

// --- RCON config CRUD (Network sub-tab support) ---

type rconConfigRequest struct {
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`
	Password   string `json:"password,omitempty"`
	Regenerate bool   `json:"regenerate,omitempty"`
}

type rconConfigResponse struct {
	Success   bool   `json:"success"`
	Enabled   bool   `json:"enabled"`
	Port      int    `json:"port"`
	HasSecret bool   `json:"hasSecret"`
	Password  string `json:"password,omitempty"` // populated only when regenerated
	Message   string `json:"message,omitempty"`
}

// GetConfig GET /api/servers/{id}/rcon/config — returns enabled/port +
// whether a password is set. Password value is never returned.
func (h *RconHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if _, err := h.state.Store.GetServerByID(serverID); err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	enabled, port, password, err := h.state.Store.GetServerRconConfig(serverID)
	if err != nil {
		sendJSONError(w, "Failed to load rcon config", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(rconConfigResponse{
		Success:   true,
		Enabled:   enabled,
		Port:      port,
		HasSecret: password != "",
	})
}

// SetConfig PUT /api/servers/{id}/rcon/config — enable/disable, set port +
// password. If `regenerate`=true the panel asks the backend to mint a fresh
// random password instead of supplying one. Newly minted passwords are
// returned ONCE in the response so the user can copy them.
func (h *RconHandler) SetConfig(w http.ResponseWriter, r *http.Request) {
	serverID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if _, err := h.state.Store.GetServerByID(serverID); err != nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}
	var req rconConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		sendJSONError(w, "Invalid port", http.StatusBadRequest)
		return
	}

	_, existingPort, existingPassword, _ := h.state.Store.GetServerRconConfig(serverID)
	password := existingPassword
	exposeNew := ""
	if req.Regenerate || (req.Enabled && password == "") {
		newPw, err := generateRconPassword(24)
		if err != nil {
			sendJSONError(w, "Failed to generate password", http.StatusInternalServerError)
			return
		}
		password = newPw
		exposeNew = newPw
	} else if req.Password != "" {
		password = req.Password
	}
	port := req.Port
	if port == 0 {
		port = existingPort
	}
	if err := h.state.Store.SetServerRconConfig(serverID, req.Enabled, port, password); err != nil {
		sendJSONError(w, "Failed to save rcon config", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(rconConfigResponse{
		Success:   true,
		Enabled:   req.Enabled,
		Port:      port,
		HasSecret: password != "",
		Password:  exposeNew,
		Message:   "RCON config saved. Restart the server to apply.",
	})
}

// generateRconPassword returns a hex-encoded random password of `nBytes`*2
// characters. Hex (not base64) so it survives copy-paste into terminals and
// server.properties without escaping.
func generateRconPassword(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashAPIKey returns the canonical storage form of an API-key plaintext.
// Exported so API-key creation paths and external auth middleware agree.
func HashAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
